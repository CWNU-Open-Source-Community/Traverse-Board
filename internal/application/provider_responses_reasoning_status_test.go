package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

// Decode the persisted private replay through a fresh SQLite connection before
// the actual second HTTP request. All writes still use the original store.
type reasoningStatusReopenReplayStore struct {
	*store.SQLiteStore
	path  string
	reads atomic.Int32
}

func (s *reasoningStatusReopenReplayStore) LoadSupervisorProviderReplay(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint,
) (map[int]*llm.ProviderReplay, error) {
	reopened, err := store.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer reopened.Close()
	replay, err := reopened.LoadSupervisorProviderReplay(ctx, checkpoint)
	if err == nil && len(replay) != 0 {
		s.reads.Add(1)
	}
	return replay, err
}

func TestSupervisorResponsesOptionalReasoningStatusSurvivesSQLiteReplay(t *testing.T) {
	for mask := 0; mask < 9; mask++ {
		t.Run(fmt.Sprintf("omitted_%03b", mask), func(t *testing.T) {
			const addedOpaque = "application-unfinished-reasoning-canary"
			const doneOpaque = "application-completed-reasoning-canary"
			const summary = "application-private-reasoning-summary-canary"
			const wireCall = "optional_reasoning_original_wire_call"
			var requests atomic.Int32
			continuations := make(chan []map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Input  []map[string]any `json:"input"`
					Stream bool             `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Stream {
					t.Errorf("expected actual streaming request: stream=%t err=%v", body.Stream, err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				n := requests.Add(1)
				if n > 2 {
					t.Error("unexpected retry or protocol repair")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if n == 2 {
					continuations <- body.Input
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(event any) {
					raw, err := json.Marshal(event)
					if err != nil {
						t.Error(err)
						return
					}
					_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
				}
				responseID := fmt.Sprintf("response_%d", n)
				emit(map[string]any{"type": "response.created", "response": map[string]any{
					"id": responseID, "object": "response", "status": "in_progress", "model": "model"}})
				var output []map[string]any
				if n == 1 {
					reason := func(bit int, status, opaque string) map[string]any {
						item := map[string]any{"id": "reasoning_1", "type": "reasoning", "encrypted_content": opaque,
							"summary": []any{map[string]any{"type": "summary_text", "text": summary}}}
						if mask == 8 {
							item["status"] = nil
						} else if mask&bit == 0 {
							item["status"] = status
						}
						return item
					}
					emit(map[string]any{"type": "response.output_item.added", "item": reason(1, "in_progress", addedOpaque)})
					emit(map[string]any{"type": "response.output_item.done", "item": reason(2, "completed", doneOpaque)})
					call := map[string]any{"id": "function_1", "type": "function_call", "status": "in_progress",
						"call_id": wireCall, "name": "work_item_create", "arguments": ""}
					emit(map[string]any{"type": "response.output_item.added", "item": call})
					call["status"] = "completed"
					call["arguments"] = `{"title":"Inspect optional reasoning","priority":"high"}`
					emit(map[string]any{"type": "response.function_call_arguments.done", "item_id": "function_1", "arguments": call["arguments"]})
					emit(map[string]any{"type": "response.output_item.done", "item": call})
					output = []map[string]any{reason(4, "completed", doneOpaque), call}
				} else {
					content := rootActionResponse(domain.RootActionContinue, "Optional reasoning replay completed", "", "")
					item := map[string]any{"id": "message_2", "type": "message", "status": "in_progress", "role": "assistant"}
					emit(map[string]any{"type": "response.output_item.added", "item": item})
					emit(map[string]any{"type": "response.output_text.delta", "item_id": "message_2", "delta": content})
					item["status"] = "completed"
					item["content"] = []any{map[string]any{"type": "output_text", "text": content}}
					emit(map[string]any{"type": "response.output_item.done", "item": item})
					output = []map[string]any{item}
				}
				emit(map[string]any{"type": "response.completed", "response": map[string]any{
					"id": responseID, "object": "response", "status": "completed", "model": "model", "output": output,
					"usage": map[string]int{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}}})
			}))
			defer server.Close()
			provider, err := llm.NewOpenAIResponsesProvider(llm.OpenAIResponsesConfig{
				Name: "usage-test", BaseURL: server.URL, APIKey: "local-fixture", DefaultModel: "model"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "optional-reasoning.db")
			st, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			run, _, router := newQualifiedHTTPProviderSupervisor(t, st, provider,
				domain.Budget{MaxTurns: 3, MaxToolCalls: 3, MaxCostUSD: 1})
			importSupervisorPriceSnapshot(t, t.Context(), st)
			window := llm.DefaultContextWindow()
			window.DefaultOutputTokens, window.MaxOutputTokens = 256, 512
			if err := router.SetContextWindow(llm.ModelRef{Provider: provider.Name(), Model: "model"}, window); err != nil {
				t.Fatal(err)
			}
			replayStore := &reasoningStatusReopenReplayStore{SQLiteStore: st, path: path}
			supervisor := application.NewAgentRunner(replayStore, router, policy.NewDefaultChecker()).
				WithMonetaryBudget(application.NewMonetaryBudgetService(st)).
				WithModelRetryPolicy(application.ModelRetryPolicy{MaxAttempts: 2})
			result, err := supervisor.Step(t.Context(), run.ID)
			if err != nil || result.ModelOutcome != llm.OutcomeSuccess || result.ModelAttempts != 2 ||
				result.ToolCalls != 1 || result.ProtocolRepairs != 0 || result.Text != "Optional reasoning replay completed" ||
				result.Checkpoint.InputTokens != 4 || result.Checkpoint.OutputTokens != 6 || result.Checkpoint.TotalTokens != 10 ||
				requests.Load() != 2 || replayStore.reads.Load() == 0 {
				t.Fatalf("native tool/reopen continuation failed: result=%+v err=%v requests=%d replay_reads=%d",
					result, err, requests.Load(), replayStore.reads.Load())
			}
			continuation := <-continuations
			private := -1
			for i, item := range continuation {
				if item["type"] == "reasoning" {
					if private >= 0 {
						t.Fatal("reasoning replay duplicated")
					}
					private = i
				}
			}
			if private < 0 || private+2 >= len(continuation) || continuation[private]["encrypted_content"] != doneOpaque ||
				continuation[private+1]["type"] != "function_call" || continuation[private+1]["call_id"] != wireCall ||
				continuation[private+2]["type"] != "function_call_output" || continuation[private+2]["call_id"] != wireCall {
				t.Fatal("reopened replay lost completed reasoning order or original tool call pairing")
			}
			_, statusPresent := continuation[private]["status"]
			if statusPresent != (mask&4 == 0 && mask != 8) {
				t.Fatal("internal lifecycle stamped a status onto the native replay")
			}
			raw, _ := json.Marshal(continuation)
			if strings.Contains(string(raw), addedOpaque) {
				t.Fatal("unfinished reasoning reached the second model request")
			}
			timeline, err := st.ListRunEvents(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if countEventType(timeline, events.ModelStartedEvent) != 2 || countEventType(timeline, events.ModelCompletedEvent) != 2 ||
				countEventType(timeline, events.ModelFailedEvent) != 0 || countEventType(timeline, events.ProtocolRepairRequestedEvent) != 0 ||
				countEventType(timeline, events.SupervisorToolBatchEvent) != 1 {
				t.Fatalf("native continuation retried, repaired or lost a terminal receipt: %+v", timeline)
			}
			messages, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
			if err != nil {
				t.Fatal(err)
			}
			items, err := st.ListWorkItems(t.Context(), domain.WorkItemFilter{RunID: run.ID})
			if err != nil || len(items) != 1 || items[0].Title != "Inspect optional reasoning" {
				t.Fatalf("native tool did not execute exactly once: items=%+v err=%v", items, err)
			}
			raw, _ = json.Marshal([]any{result, timeline, messages, items})
			for _, marker := range []string{addedOpaque, doneOpaque, summary} {
				if strings.Contains(string(raw), marker) {
					t.Fatalf("private reasoning escaped the public or durable authority boundary: %q", marker)
				}
			}
			money := readOrdinaryMoneyUsage(t, st, run.ID)
			usage, err := st.GetRunAgentUsage(t.Context(), run.ID)
			if err != nil || money.SettledMicros != 16 || money.ReservedMicros <= 16 || money.ReleasedMicros != money.ReservedMicros-16 ||
				usage.RootTokens != 10 || usage.SpecialistTokens != 0 || usage.TotalTokens != 10 {
				t.Fatalf("native continuation changed usage/settlement: money=%+v usage=%+v err=%v", money, usage, err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			recoveredUsage, err := reopened.GetRunAgentUsage(t.Context(), run.ID)
			if err != nil || !reflect.DeepEqual(recoveredUsage, usage) ||
				!reflect.DeepEqual(readOrdinaryMoneyUsage(t, reopened, run.ID), money) || requests.Load() != 2 {
				t.Fatalf("reopen changed settlement or repeated HTTP: usage=%+v err=%v", recoveredUsage, err)
			}
			t.Logf("actual_http_calls=2 native_tools=1 fresh_sqlite_replay_reads=%d tokens=10 settled_micros=16 protocol_repairs=0", replayStore.reads.Load())
		})
	}
}
