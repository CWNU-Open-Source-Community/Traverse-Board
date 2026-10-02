package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
)

type kimiSupervisorRuntime struct{}

func (kimiSupervisorRuntime) ResolveCredential(context.Context) (string, error) {
	return "fixture-only", nil
}
func (kimiSupervisorRuntime) MapModel(string) (string, error)                 { return "kimi-k3", nil }
func (kimiSupervisorRuntime) Apply(string, http.Header, map[string]any) error { return nil }
func (kimiSupervisorRuntime) BindingDigest() string                           { return strings.Repeat("b", 64) }

type kimiSupervisorTransport func(*http.Request) (*http.Response, error)

func (f kimiSupervisorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func kimiSupervisorFixtureRouter(t *testing.T, handler http.Handler) *llm.Router {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	p, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{Name: "kimi-root", BaseURL: "https://api.moonshot.ai/v1/chat/completions",
		DefaultModel: "model", Runtime: kimiSupervisorRuntime{}, HTTPClient: &http.Client{Transport: kimiSupervisorTransport(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != "https://api.moonshot.ai/v1/chat/completions" {
				t.Error("K3 endpoint scope changed")
			}
			copy := request.Clone(request.Context())
			copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
			return http.DefaultTransport.RoundTrip(copy)
		})}})
	if err != nil {
		t.Fatal(err)
	}
	ref := llm.ModelRef{Provider: p.Name(), Model: "model"}
	router := llm.NewRouter(ref)
	router.RegisterProvider(p)
	profile, err := router.HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}
	// Test-only admission of this synthetic fixture. No operator grant, paid
	// qualification, or real user authorization is created by the test.
	now := time.Now().UTC()
	if err := router.SetHarnessQualification(ref, llm.HarnessQualification{ProtocolVersion: llm.ModelHarnessProtocolVersion, BindingDigest: profile.BindingDigest,
		ToolCallsQualified: true, ToolResultsQualified: true, StrictJSONQualified: true, StreamingQualified: true, QualifiedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return router
}

func TestSupervisorKimiPrivateToolAndOrdinaryHistorySurvivesReopen(t *testing.T) {
	var requests atomic.Int32
	ordinary := rootActionResponse(domain.RootActionContinue, "Two work items and one note recorded", "", "")
	router := kimiSupervisorFixtureRouter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]json.RawMessage `json:"messages"`
			Model    string                       `json:"model"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "kimi-k3" {
			t.Error("invalid mapped request")
			w.WriteHeader(400)
			return
		}
		count := int(requests.Add(1))
		assistantCount, resultCount := 0, 0
		for _, message := range body.Messages {
			var role, reason, content, callID string
			_ = json.Unmarshal(message["role"], &role)
			_ = json.Unmarshal(message["reasoning_content"], &reason)
			_ = json.Unmarshal(message["content"], &content)
			_ = json.Unmarshal(message["tool_call_id"], &callID)
			if role == "assistant" {
				assistantCount++
				if reason != fmt.Sprintf(" private-K3-root-%d \n雪", assistantCount) {
					t.Error("native reasoning lost its original position or value")
				}
				if assistantCount == 3 && content != ordinary {
					t.Error("native lifecycle JSON was replaced by the public display message")
				}
				if assistantCount < 3 {
					var calls []struct {
						ID string `json:"id"`
					}
					if json.Unmarshal(message["tool_calls"], &calls) != nil || len(calls) == 0 {
						t.Error("native tool assistant lost its calls")
					}
					for index, call := range calls {
						want := fmt.Sprintf("native-root-%d-%d", assistantCount, index)
						if call.ID != want {
							t.Error("durable alias replaced native tool ID")
						}
					}
				}
			}
			if role == "tool" {
				resultCount++
				want := "native-root-1-0"
				if resultCount == 2 {
					want = "native-root-1-1"
				} else if resultCount == 3 {
					want = "native-root-2-0"
				}
				if callID != want || content == "" {
					t.Error("recorded result lost its native pairing")
				}
			}
			if strings.Contains(content, "supervisor-tools:") {
				t.Error("public tool summary substituted for native history")
			}
		}
		if count == 2 && (assistantCount != 1 || resultCount != 2) || count == 3 && (assistantCount != 2 || resultCount != 3) ||
			count == 4 && (assistantCount != 3 || resultCount != 3) {
			t.Error("native segment missing, duplicated, or reordered")
		}
		if count > 4 {
			t.Error("historical tools caused another model call")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(delta any, finish string) {
			event := map[string]any{"id": fmt.Sprintf("native-root-response-%d", count), "model": "upstream-k3-snapshot",
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
			if finish != "" {
				event["usage"] = map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}
			}
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		emit(map[string]any{"role": "assistant", "reasoning_content": fmt.Sprintf(" private-K3-root-%d \n雪", count)}, "")
		if count < 3 {
			tools := []any{}
			number := 1
			if count == 1 {
				number = 2
			}
			for index := 0; index < number; index++ {
				name, args := "note_create", `{"title":"Observation","content":"One note recorded"}`
				if count == 1 {
					name = "work_item_create"
					args = fmt.Sprintf(`{"title":"Inspect parser %d","priority":"high"}`, index)
				}
				tools = append(tools, map[string]any{"index": index, "id": fmt.Sprintf("native-root-%d-%d", count, index), "type": "function", "function": map[string]string{"name": name, "arguments": args}})
			}
			emit(map[string]any{"content": "I will record the observations.", "tool_calls": tools}, "tool_calls")
		} else {
			content := ordinary
			if count == 4 {
				content = rootActionResponse(domain.RootActionContinue, "Historical tools were retained without repeating them", "", "")
			}
			emit(map[string]any{"content": content}, "stop")
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	path := filepath.Join(t.TempDir(), "kimi-root.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	run := newStartedRunForProvider(t, st, "kimi-root", domain.Budget{MaxTurns: 3, MaxToolCalls: 4})
	newSupervisor := func() *application.RunSupervisor {
		return application.NewRunSupervisor(st, router, policy.NewDefaultChecker()).WithModelRetryPolicy(application.ModelRetryPolicy{MaxAttempts: 1})
	}
	first, err := newSupervisor().Step(t.Context(), run.ID)
	if err != nil || first.ToolCalls != 3 || first.Checkpoint.TotalTokens != 15 {
		t.Fatal("initial native loop failed", err, first.ToolCalls, first.Checkpoint.TotalTokens)
	}
	before, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 20)
	if err != nil || len(before) != 2 {
		t.Fatal("native rounds were not recorded", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newSupervisor().StepWithInput(t.Context(), run.ID, "Fresh user follow-up")
	if err != nil || second.ToolCalls != 0 || second.Checkpoint.TotalTokens != 20 || requests.Load() != 4 {
		t.Fatal("reopen lost native history", err, second.ToolCalls, second.Checkpoint.TotalTokens)
	}
	after, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("historical tool restore changed the execution ledger")
	}
	messages, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	eventList, err := st.ListRunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal([]any{messages, eventList, after, first, second})
	if strings.Contains(string(public), "private-K3-root") || strings.Contains(string(public), "native-root-response") {
		t.Fatal("private native metadata leaked into public history or events")
	}
}

func TestSupervisorKimiRejectsLegacyAssistantHistoryBeforeModelCall(t *testing.T) {
	var requests atomic.Int32
	router := kimiSupervisorFixtureRouter(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(400) }))
	st, err := store.Open(filepath.Join(t.TempDir(), "kimi-old-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := newStartedRunForProvider(t, st, "kimi-root", domain.Budget{MaxTurns: 3})
	if _, err := st.SaveSessionMessage(t.Context(), session.NewMessage(run.SessionID, "assistant", "old answer has no native state")); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunSupervisor(st, router, policy.NewDefaultChecker()).Step(t.Context(), run.ID); err == nil || requests.Load() != 0 {
		t.Fatal("legacy history caused a paid request or silently acquired replay", err, requests.Load())
	}
}
