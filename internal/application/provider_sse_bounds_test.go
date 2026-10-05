package application_test

import (
	"context"
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
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/store"
)

func TestSupervisorSSEBoundsPartialToolsStayUnexecutedAfterReopen(t *testing.T) {
	for _, kind := range []string{"anthropic", "responses"} {
		for _, mode := range []string{"limit", "truncate", "cancel"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "sse-bounds.db")
				st, err := store.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = st.Close() })
				var calls atomic.Int32
				var run domain.Run
				ready, release := make(chan struct{}), make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if calls.Add(1) != 1 {
						t.Error("SSE protocol/cancellation failure retried")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					emit := func(value any) {
						raw, err := json.Marshal(value)
						if err != nil {
							t.Error(err)
							return
						}
						_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
					}
					arguments := `{"title":"sse-uncommitted-note-canary","body":"unaccepted tool"}`
					if kind == "anthropic" {
						emit(map[string]any{"type": "message_start", "message": map[string]any{
							"model": "model", "usage": map[string]int{"input_tokens": 2, "output_tokens": 0}}})
						emit(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
							"type": "tool_use", "id": "sse_failed_tool", "name": "note_create", "input": map[string]any{}}})
						emit(map[string]any{"type": "content_block_delta", "index": 0,
							"delta": map[string]any{"type": "input_json_delta", "partial_json": arguments}})
						emit(map[string]any{"type": "content_block_stop", "index": 0})
					} else {
						emit(map[string]any{"type": "response.created", "response": map[string]any{
							"id": "response_1", "object": "response", "status": "in_progress", "model": "model"}})
						call := map[string]any{"id": "function_1", "type": "function_call", "status": "in_progress",
							"call_id": "sse_failed_tool", "name": "note_create", "arguments": ""}
						emit(map[string]any{"type": "response.output_item.added", "item": call})
						call["status"], call["arguments"] = "completed", arguments
						emit(map[string]any{"type": "response.function_call_arguments.done", "item_id": "function_1", "arguments": arguments})
						emit(map[string]any{"type": "response.output_item.done", "item": call})
					}
					if mode == "limit" {
						if kind == "anthropic" {
							_, _ = fmt.Fprintf(w, "data: {\"type\":\"ping\",\"secret\":\"sse-private-body-canary\",\"a\":\"%s\",\ndata: \"b\":\"%s\"}\n",
								strings.Repeat("x", 600000), strings.Repeat("x", 600000))
						} else {
							_, _ = io.WriteString(w, strings.Repeat("data: \t\n", 4097))
						}
					} else {
						_, _ = io.WriteString(w, "data:\ndata: {\"type\":\"sse-private-body-canary\"")
					}
					w.(http.Flusher).Flush()
					close(ready)
					if mode == "cancel" {
						select {
						case <-r.Context().Done():
						case <-release:
						}
					}
				}))
				defer server.Close()
				defer close(release)
				var provider llm.Provider
				if kind == "anthropic" {
					provider, err = llm.NewAnthropicCompatibleProvider(llm.AnthropicCompatibleConfig{
						Name: "usage-test", BaseURL: server.URL, APIKey: "fixture-only", DefaultModel: "model"})
				} else {
					provider, err = llm.NewOpenAIResponsesProvider(llm.OpenAIResponsesConfig{
						Name: "usage-test", BaseURL: server.URL, APIKey: "fixture-only", DefaultModel: "model"})
				}
				if err != nil {
					t.Fatal(err)
				}
				var supervisor *application.AgentRunner
				run, supervisor = boundaryMoneySupervisor(t, st, provider,
					domain.Budget{MaxTurns: 3, MaxToolCalls: 3, MaxCostUSD: 1})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				type completion struct {
					result application.LifecycleResult
					err    error
				}
				finished := make(chan completion, 1)
				go func() {
					result, err := supervisor.Step(ctx, run.ID)
					finished <- completion{result, err}
				}()
				select {
				case <-ready:
				case <-time.After(10 * time.Second):
					t.Fatal("fixture did not reach the incomplete SSE event")
				}
				if mode == "cancel" {
					cancel()
				}
				var completed completion
				select {
				case completed = <-finished:
				case <-time.After(10 * time.Second):
					t.Fatal("Supervisor did not exit after the SSE failure")
				}
				result, stepErr := completed.result, completed.err
				wantOutcome := llm.OutcomeInvalidResponse
				if mode == "cancel" {
					wantOutcome = llm.OutcomeCancelled
				}
				if stepErr == nil || result.ModelOutcome != wantOutcome || result.ModelAttempts != 1 ||
					result.ToolCalls != 0 || result.ProtocolRepairs != 0 || calls.Load() != 1 {
					t.Fatalf("partial tool escaped the stream boundary: result=%+v err=%v requests=%d", result, stepErr, calls.Load())
				}
				assertBoundaryNoTools(t, st, run.ID)
				assertOrdinaryMoneyNoAssistant(t, st, run)
				money := readOrdinaryMoneyUsage(t, st, run.ID)
				if money.ReservedMicros <= 0 || money.SettledMicros != money.ReservedMicros || money.ReleasedMicros != 0 ||
					result.Checkpoint.TotalTokens != 0 {
					t.Fatalf("incomplete sent usage was invented or released: %+v tokens=%d", money, result.Checkpoint.TotalTokens)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := store.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if !reflect.DeepEqual(readOrdinaryMoneyUsage(t, reopened, run.ID), money) || calls.Load() != 1 {
					t.Fatal("reopen changed accounting or repeated HTTP")
				}
				timeline, err := reopened.ListRunEvents(t.Context(), run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if countEventType(timeline, events.ModelFailedEvent) != 1 || countEventType(timeline, events.ModelCompletedEvent) != 0 ||
					countEventType(timeline, events.ProtocolRepairRequestedEvent) != 0 || countEventType(timeline, events.RunExecutionLeaseReleasedEvent) != 1 {
					t.Fatal("persistent failure/lease receipts changed")
				}
				messages, err := reopened.ListSessionMessages(t.Context(), run.SessionID, true)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal([]any{result, timeline, messages})
				for _, marker := range []string{"sse-uncommitted-note-canary", "sse-private-body-canary"} {
					if strings.Contains(string(raw), marker) || strings.Contains(stepErr.Error(), marker) {
						t.Fatalf("raw SSE/tool input leaked through public persistence: %s", marker)
					}
				}
				t.Logf("actual_http_calls=1 accepted_tools=0 protocol_repairs=0 settled_unknown_micros=%d persisted_failure=%s", money.SettledMicros, wantOutcome)
			})
		}
	}
}
