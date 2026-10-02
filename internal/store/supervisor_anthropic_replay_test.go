package store

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestSupervisorAnthropicReplayAtomicTwoRoundsAndReopen(t *testing.T) {
	f := newProviderReplayFixture(t)
	firstCall := f.response.ToolCalls[0]
	secondPayload, err := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool,
		json.RawMessage(`{"title":"second evidence","content":"saved after reopen"}`))
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := runmutation.SupervisorToolCallID(runmutation.SupervisorToolOperationKey(f.turn.Run.ID,
		f.turn.Checkpoint.NextTurn, "note_create", string(secondPayload)), 2)
	if err != nil {
		t.Fatal(err)
	}
	secondCall := llm.ToolCall{ID: secondID, Name: "note_create", Arguments: secondPayload}
	accepted := []llm.ToolCall{firstCall, secondCall}
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(w, "fixture request failed", 500)
			return
		}
		round := int(requestCount.Add(1))
		if round == 2 {
			var blocks []map[string]any
			if len(body.Messages) != 3 || json.Unmarshal(body.Messages[1].Content, &blocks) != nil || len(blocks) != 4 ||
				blocks[0]["thinking"] != "" || blocks[0]["signature"] != "private-native-signature-1  \n" ||
				blocks[1]["data"] != "private-native-redacted-1" || blocks[3]["id"] != "wire-native-1" {
				t.Error("database reopen changed the actual native continuation request")
			}
		}
		if round < 1 || round > 2 {
			http.Error(w, "unexpected fixture round", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": fmt.Sprintf("msg_native_store_%d", round), "type": "message", "role": "assistant", "model": "model", "stop_reason": "tool_use",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "", "signature": fmt.Sprintf("private-native-signature-%d  \n", round)},
				map[string]any{"type": "redacted_thinking", "data": fmt.Sprintf("private-native-redacted-%d", round)},
				map[string]any{"type": "text", "text": "Public continuation."},
				map[string]any{"type": "tool_use", "id": fmt.Sprintf("wire-native-%d", round), "name": "note_create", "input": accepted[round-1].Arguments},
			},
			"usage": map[string]int{"input_tokens": 2, "output_tokens": 3},
		})
	}))
	defer server.Close()
	provider, err := llm.NewAnthropicCompatibleProvider(llm.AnthropicCompatibleConfig{Name: "test", BaseURL: server.URL,
		APIKey: "fixture-secret", DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	f.start(t)
	request := llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "continue"}}}
	response, err := provider.Chat(t.Context(), request)
	if err != nil || response.Replay == nil {
		t.Fatalf("native fixture did not produce replay: %v", err)
	}
	response.ToolCalls = []llm.ToolCall{firstCall}
	response.Replay, err = response.Replay.BindToolCalls(response.ToolCalls)
	if err != nil {
		t.Fatal(err)
	}
	f.attempt.Outcome = llm.OutcomeSuccess
	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_native_replay BEFORE INSERT ON run_supervisor_provider_replay BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, *response); err == nil {
		t.Fatal("private write failure left a successful native response")
	}
	rounds, err := f.store.ListSupervisorToolRounds(t.Context(), f.turn.Checkpoint)
	if err != nil || len(rounds) != 0 {
		t.Fatal("private write failure left executable tools", err)
	}
	eventList, err := f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil || countRunEventType(eventList, events.ModelCompletedEvent) != 0 {
		t.Fatal("private write failure left a successful terminal event", err)
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER fail_native_replay`); err != nil {
		t.Fatal(err)
	}
	cp, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, *response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, *response); err != nil {
		t.Fatal("exact native terminal retry rejected", err)
	}
	changed := *response
	changed.Replay = nil
	if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), cp, f.attempt, changed); err == nil {
		t.Fatal("native terminal retry removed private state")
	}
	firstBytes, err := response.Replay.EncodeForStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordSupervisorToolExecutionStarted(t.Context(), cp, firstCall.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.RecordSupervisorToolResult(t.Context(), cp, domain.SupervisorToolResult{CallID: firstCall.ID,
		Status: domain.SupervisorToolCompleted, ResultJSON: `{"ok":true}`, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	reopen := func() {
		t.Helper()
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
		f.store, err = Open(f.path)
		if err != nil {
			t.Fatal(err)
		}
	}
	reopen()
	loaded, err := f.store.LoadSupervisorProviderReplay(t.Context(), cp)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("first native response was lost on reopen: %v", err)
	}
	request.Messages = append(request.Messages, llm.Message{Role: "assistant", Content: loaded[1].AssistantText(),
		ToolCalls: []llm.ToolCall{firstCall}, Replay: loaded[1]}, llm.Message{Role: "user", ToolResults: []llm.ToolResult{{ToolCallID: firstCall.ID, Content: `{"ok":true}`}}})
	secondAttempt := f.attempt
	secondAttempt.Number, secondAttempt.ToolRound, secondAttempt.Outcome = 2, 1, ""
	if _, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, secondAttempt); err != nil {
		t.Fatal(err)
	}
	second, err := provider.Chat(t.Context(), request)
	if err != nil || second.Replay == nil {
		t.Fatalf("native continuation after reopen failed: %v", err)
	}
	second.ToolCalls = []llm.ToolCall{secondCall}
	second.Replay, err = second.Replay.BindToolCalls(second.ToolCalls)
	if err != nil {
		t.Fatal(err)
	}
	secondAttempt.Outcome = llm.OutcomeSuccess
	cp, err = f.store.RecordSupervisorModelCompleted(t.Context(), cp, secondAttempt, *second)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.Replay.EncodeForStore()
	if err != nil {
		t.Fatal(err)
	}
	reopen()
	loaded, err = f.store.LoadSupervisorProviderReplay(t.Context(), cp)
	if err != nil || len(loaded) != 2 {
		t.Fatalf("second reopen did not retain both native responses: %v", err)
	}
	for index, expected := range [][]byte{firstBytes, secondBytes} {
		got, err := loaded[index+1].EncodeForStore()
		if err != nil || string(got) != string(expected) || loaded[index+1].ValidateToolCalls([]llm.ToolCall{accepted[index]}) != nil {
			t.Fatal("private native bytes or tool bindings changed between database opens", err)
		}
	}
	stale := cp
	stale.LeaseGeneration++
	if _, err := f.store.LoadSupervisorProviderReplay(t.Context(), stale); err == nil {
		t.Fatal("stale fence exposed native private state")
	}
	page, err := f.store.ListRunSupervisorToolRoundsPage(t.Context(), f.turn.Run.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	eventList, err = f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal([]any{page, eventList})
	if strings.Contains(string(public), "private-native-") || strings.Contains(string(public), "wire-native-") {
		t.Fatal("native thinking, signature, redacted data or wire identity reached public history")
	}
}
