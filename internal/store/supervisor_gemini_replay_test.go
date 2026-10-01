package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

type geminiStoreRuntime struct{}

func (geminiStoreRuntime) ResolveCredential(context.Context) (string, error) { return "fixture", nil }
func (geminiStoreRuntime) MapModel(string) (string, error)                   { return "gemini-3.7-flash", nil }
func (geminiStoreRuntime) Apply(string, http.Header, map[string]any) error   { return nil }
func (geminiStoreRuntime) BindingDigest() string                             { return strings.Repeat("a", 64) }

type geminiStoreTransport func(*http.Request) (*http.Response, error)

func (f geminiStoreTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSupervisorGeminiReplayAtomicTwoRoundsAndReopen(t *testing.T) {
	f := newProviderReplayFixture(t)
	first := f.response.ToolCalls[0]
	payload, err := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool, json.RawMessage(`{"title":"second evidence","content":"saved after reopen"}`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := runmutation.SupervisorToolCallID(runmutation.SupervisorToolOperationKey(f.turn.Run.ID, f.turn.Checkpoint.NextTurn, "note_create", string(payload)), 2)
	if err != nil {
		t.Fatal(err)
	}
	accepted := []llm.ToolCall{first, {ID: id, Name: "note_create", Arguments: payload}}
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round := int(count.Add(1))
		var body struct {
			Messages []struct {
				Role       string `json:"role"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID           string `json:"id"`
					ExtraContent struct {
						Google struct {
							Signature string `json:"thought_signature"`
						} `json:"google"`
					} `json:"extra_content"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) != round*2-1 {
			t.Error("reopen changed native tool-turn history")
			w.WriteHeader(400)
			return
		}
		for prior := 1; prior < round; prior++ {
			assistant, result := body.Messages[prior*2-1], body.Messages[prior*2]
			if assistant.Role != "assistant" || len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != fmt.Sprintf("native-Gemini-tool-%d", prior) ||
				assistant.ToolCalls[0].ExtraContent.Google.Signature != fmt.Sprintf("private-Gemini-store-%d \n", prior) || result.Role != "tool" || result.ToolCallID != assistant.ToolCalls[0].ID {
				t.Error("reopened SQLite replay lost its signature, source response or paired native IDs")
			}
		}
		var calls []any
		finish, text := "stop", "done"
		if round <= 2 {
			finish, text = "tool_calls", "public continuation"
			calls = []any{map[string]any{"id": fmt.Sprintf("native-Gemini-tool-%d", round), "type": "function",
				"function":      map[string]string{"name": "note_create", "arguments": string(accepted[round-1].Arguments)},
				"extra_content": map[string]any{"google": map[string]string{"thought_signature": fmt.Sprintf("private-Gemini-store-%d \n", round)}}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("native-Gemini-response-%d", round), "model": "upstream-Gemini-snapshot",
			"choices": []any{map[string]any{"index": 0, "finish_reason": finish, "message": map[string]any{"role": "assistant", "content": text, "tool_calls": calls}}},
			"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}})
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	provider, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{Name: "test", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", DefaultModel: "model", Runtime: geminiStoreRuntime{},
		HTTPClient: &http.Client{Transport: geminiStoreTransport(func(request *http.Request) (*http.Response, error) {
			copy := request.Clone(request.Context())
			copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
			return http.DefaultTransport.RoundTrip(copy)
		})}})
	if err != nil {
		t.Fatal(err)
	}
	f.start(t)
	request := llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "continue"}}}
	cp := f.turn.Checkpoint
	var encoded [][]byte
	for round := 0; round < 2; round++ {
		attempt := f.attempt
		if round != 0 {
			attempt.Number, attempt.ToolRound, attempt.Outcome = round+1, round, ""
			if _, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, attempt); err != nil {
				t.Fatal(err)
			}
		}
		response, err := provider.Chat(t.Context(), request)
		if err != nil || response.Replay == nil || response.Model != "model" {
			t.Fatal("actual Gemini response did not produce bound replay", err)
		}
		response.ToolCalls = []llm.ToolCall{accepted[round]}
		response.Replay, err = response.Replay.BindToolCalls(response.ToolCalls)
		if err != nil {
			t.Fatal(err)
		}
		attempt.Outcome = llm.OutcomeSuccess
		if round == 0 {
			if _, err := f.store.db.Exec(`CREATE TRIGGER fail_gemini_replay BEFORE INSERT ON run_supervisor_provider_replay BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), cp, attempt, *response); err == nil {
				t.Fatal("failed private insert left successful Gemini state")
			}
			rounds, err := f.store.ListSupervisorToolRounds(t.Context(), cp)
			if err != nil || len(rounds) != 0 {
				t.Fatal("failed private insert left executable tools", err)
			}
			eventList, err := f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
			if err != nil || countRunEventType(eventList, events.ModelCompletedEvent) != 0 {
				t.Fatal("failed private insert left a successful event", err)
			}
			if _, err := f.store.db.Exec(`DROP TRIGGER fail_gemini_replay`); err != nil {
				t.Fatal(err)
			}
		}
		previous := cp
		cp, err = f.store.RecordSupervisorModelCompleted(t.Context(), cp, attempt, *response)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), previous, attempt, *response); err != nil {
			t.Fatal("exact native terminal retry failed", err)
		}
		changed := *response
		changed.Replay = nil
		if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), cp, attempt, changed); err == nil {
			t.Fatal("terminal retry dropped native signatures")
		}
		bytes, err := response.Replay.EncodeForStore()
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, bytes)
		if _, err := f.store.RecordSupervisorToolExecutionStarted(t.Context(), cp, accepted[round].ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.store.RecordSupervisorToolResult(t.Context(), cp, domain.SupervisorToolResult{CallID: accepted[round].ID, Status: domain.SupervisorToolCompleted, ResultJSON: `{"ok":true}`, CompletedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
		f.store, err = Open(f.path)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := f.store.LoadSupervisorProviderReplay(t.Context(), cp)
		if err != nil || len(loaded) != round+1 {
			t.Fatal("SQLite reopen lost prior Gemini response", err)
		}
		for prior, expected := range encoded {
			got, err := loaded[prior+1].EncodeForStore()
			if err != nil || string(got) != string(expected) || loaded[prior+1].ValidateToolCalls([]llm.ToolCall{accepted[prior]}) != nil {
				t.Fatal("reopen changed native private bytes or accepted tool authority", err)
			}
		}
		request.Messages = append(request.Messages, llm.Message{Role: "assistant", Content: loaded[round+1].AssistantText(), ToolCalls: []llm.ToolCall{accepted[round]}, Replay: loaded[round+1]}, llm.Message{Role: "user", ToolResults: []llm.ToolResult{{ToolCallID: accepted[round].ID, Content: `{"ok":true}`}}})
	}
	final, err := provider.Chat(t.Context(), request)
	if err != nil || final.Text != "done" || len(final.ToolCalls) != 0 || count.Load() != 3 {
		t.Fatal("two reopened tool rounds did not resume to completion", err)
	}
	stale := cp
	stale.LeaseGeneration++
	if _, err := f.store.LoadSupervisorProviderReplay(t.Context(), stale); err == nil {
		t.Fatal("stale fence exposed private Gemini state")
	}
	page, err := f.store.ListRunSupervisorToolRoundsPage(t.Context(), f.turn.Run.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	eventList, err := f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal([]any{page, eventList})
	if strings.Contains(string(public), "private-Gemini") || strings.Contains(string(public), "native-Gemini") {
		t.Fatal("native signatures or identities reached public history")
	}
}
