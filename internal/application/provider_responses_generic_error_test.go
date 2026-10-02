package application_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/store"
)

func TestSupervisorResponsesGenericErrorRetryPrivacyAndUnknownCost(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		kind       llm.Outcome
		reason     llm.ProviderFailureReason
		recover    bool
		calls      int64
		moneyCap   float64
	}{
		{"capacity_recovers", "server_error", llm.OutcomeRetryable, llm.ProviderFailureCapacity, true, 2, 1},
		{"rate_limit_recovers", "rate_limit_exceeded", llm.OutcomeRateLimited, llm.ProviderFailureRateLimit, true, 2, 1},
		{"authentication_stops", "invalid_api_key", llm.OutcomePermanent, llm.ProviderFailureAuthentication, false, 1, 1},
		{"unknown_stops", "server_error-private-code-canary", llm.OutcomePermanent, llm.ProviderFailureProtocolIncompatible, false, 1, 1},
		{"attempt_limit", "server_error", llm.OutcomeRetryable, llm.ProviderFailureCapacity, false, 2, 1},
		{"money_limit", "server_error", llm.OutcomeRetryable, llm.ProviderFailureCapacity, false, 1, 0.04},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "responses-generic-error.db")
			st, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			var calls atomic.Int64
			requests := make(chan string, 2)
			firstMoney := make(chan domain.MonetaryUsage, 1)
			var run domain.Run
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				n := calls.Add(1)
				if n > 2 {
					t.Error("generic error exceeded the configured HTTP attempt limit")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				requests <- string(raw)
				if n == 1 {
					money, err := st.GetMonetaryUsage(t.Context(), run.ID)
					if err != nil {
						t.Error(err)
					}
					firstMoney <- money
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(payload any) {
					encoded, err := json.Marshal(payload)
					if err != nil {
						t.Error(err)
						return
					}
					_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
				}
				id := fmt.Sprintf("response_%d", n)
				emit(map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "status": "in_progress", "model": "model"}})
				text := rootActionResponse(domain.RootActionContinue, "uncommitted-answer-canary", "", "")
				success := n == 2 && tc.recover
				if success {
					text = rootActionResponse(domain.RootActionContinue, "recovered reply", "", "")
				}
				item := map[string]any{"id": "message_1", "type": "message", "status": "in_progress", "role": "assistant"}
				emit(map[string]any{"type": "response.output_item.added", "item": item})
				emit(map[string]any{"type": "response.output_text.delta", "item_id": "message_1", "delta": text})
				item["status"], item["content"] = "completed", []any{map[string]any{"type": "output_text", "text": text}}
				emit(map[string]any{"type": "response.output_item.done", "item": item})
				if success {
					emit(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "object": "response", "status": "completed", "model": "model",
						"usage": map[string]int{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}}})
					return
				}
				call := map[string]any{"id": "failed_function", "type": "function_call", "status": "in_progress", "call_id": "failed_tool_canary", "name": "note_create", "arguments": ""}
				emit(map[string]any{"type": "response.output_item.added", "item": call})
				call["status"], call["arguments"] = "completed", `{"title":"failed-note-canary"}`
				emit(map[string]any{"type": "response.function_call_arguments.done", "item_id": "failed_function", "arguments": call["arguments"]})
				emit(map[string]any{"type": "response.output_item.done", "item": call})
				emit(map[string]any{"type": "error", "sequence_number": 8, "code": tc.code,
					"message": "private-error-message-canary 私有正文标记 sk-fixture-credential-canary", "param": "private-error-param-canary",
					"usage": map[string]int{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}})
			}))
			defer server.Close()
			provider, err := llm.NewOpenAIResponsesProvider(llm.OpenAIResponsesConfig{
				Name: "usage-test", BaseURL: server.URL, APIKey: "fixture-only", DefaultModel: "model",
			})
			if err != nil {
				t.Fatal(err)
			}
			var supervisor *application.RunSupervisor
			run, supervisor = boundaryMoneySupervisor(t, st, provider, domain.Budget{MaxTurns: 3, MaxCostUSD: tc.moneyCap})
			result, stepErr := supervisor.Step(t.Context(), run.ID)
			if calls.Load() != tc.calls || (stepErr == nil) != tc.recover || result.ProtocolRepairs != 0 || result.ToolCalls != 0 {
				t.Fatalf("failure escaped existing retry/tool boundaries: calls=%d result=%+v err=%v", calls.Load(), result, stepErr)
			}
			if tc.name == "money_limit" {
				if apperror.CodeOf(stepErr) != apperror.CodeResourceExhausted {
					t.Fatalf("retry did not enforce the monetary gate: %v", stepErr)
				}
			} else if result.ModelAttempts != int(tc.calls) || tc.recover && (result.Text != "recovered reply" || result.ModelOutcome != llm.OutcomeSuccess) ||
				!tc.recover && result.ModelOutcome != tc.kind {
				t.Fatalf("unexpected terminal result: %+v", result)
			}
			firstReserve := <-firstMoney
			if firstReserve.ReservedMicros <= 0 {
				t.Fatal("sent request had no monetary reservation")
			}
			_ = <-requests
			if tc.calls == 2 {
				second := <-requests
				for _, marker := range []string{"uncommitted-answer-canary", "failed_tool_canary", "failed-note-canary"} {
					if strings.Contains(second, marker) {
						t.Fatalf("failed output replayed into the next request: %s", marker)
					}
				}
			}
			assertBoundaryNoTools(t, st, run.ID)
			money := readOrdinaryMoneyUsage(t, st, run.ID)
			if tc.recover {
				if money.SettledMicros != firstReserve.ReservedMicros+8 || result.Checkpoint.TotalTokens != 5 {
					t.Fatalf("generic error produced known/free usage: initial=%+v final=%+v tokens=%d", firstReserve, money, result.Checkpoint.TotalTokens)
				}
			} else if money.SettledMicros != money.ReservedMicros || money.ReleasedMicros != 0 || result.Checkpoint.TotalTokens != 0 {
				t.Fatalf("unknown sent usage was released: %+v tokens=%d", money, result.Checkpoint.TotalTokens)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if recovered := readOrdinaryMoneyUsage(t, reopened, run.ID); !reflect.DeepEqual(recovered, money) || calls.Load() != tc.calls {
				t.Fatalf("reopen changed accounting or replayed HTTP: %+v calls=%d", recovered, calls.Load())
			}
			timeline, err := reopened.ListRunEvents(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			failures, completed := int(tc.calls), 0
			if tc.recover {
				failures, completed = 1, 1
			}
			if countEventType(timeline, events.ModelFailedEvent) != failures || countEventType(timeline, events.ModelCompletedEvent) != completed ||
				countEventType(timeline, events.ProtocolRepairRequestedEvent) != 0 || countEventType(timeline, events.RunExecutionLeaseReleasedEvent) != 1 {
				t.Fatal("durable terminal receipt counts changed")
			}
			for _, event := range timeline {
				if event.Type == events.ModelFailedEvent {
					var payload struct {
						Outcome llm.Outcome               `json:"outcome"`
						Reason  llm.ProviderFailureReason `json:"failure_reason"`
					}
					if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil || payload.Outcome != tc.kind || payload.Reason != tc.reason {
						t.Fatalf("durable failure classification changed: %+v err=%v", payload, err)
					}
				}
			}
			messages, err := reopened.ListSessionMessages(t.Context(), run.SessionID, true)
			if err != nil {
				t.Fatal(err)
			}
			cp, found, err := reopened.GetSupervisorCheckpoint(t.Context(), run.ID)
			if err != nil || !found {
				t.Fatalf("lost durable checkpoint: found=%t err=%v", found, err)
			}
			raw, err := json.Marshal([]any{timeline, messages, cp, result})
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{"private-error-message-canary", "私有正文标记", "sk-fixture-credential-canary", "private-error-param-canary", "server_error-private-code-canary", "uncommitted-answer-canary", "failed-note-canary"} {
				if strings.Contains(string(raw), marker) || stepErr != nil && strings.Contains(stepErr.Error(), marker) {
					t.Fatalf("private or failed output persisted after reopen: %s", marker)
				}
			}
			t.Logf("actual_http_calls=%d failed_tools=0 protocol_repairs=0 first_unknown_settled=%d final_settled=%d known_tokens=%d", tc.calls, firstReserve.ReservedMicros, money.SettledMicros, result.Checkpoint.TotalTokens)
		})
	}
}
