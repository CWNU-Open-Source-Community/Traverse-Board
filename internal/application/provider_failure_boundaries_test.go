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

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/store"
)

func boundaryAnthropicEvent(w http.ResponseWriter, payload any) {
	raw, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
}

func boundaryAnthropicText(w http.ResponseWriter, text string) {
	boundaryAnthropicEvent(w, map[string]any{"type": "message_start", "message": map[string]any{
		"model": "model", "usage": map[string]any{"input_tokens": 2, "output_tokens": 0}}})
	boundaryAnthropicEvent(w, map[string]any{"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""}})
	boundaryAnthropicEvent(w, map[string]any{"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": text}})
	boundaryAnthropicEvent(w, map[string]any{"type": "content_block_stop", "index": 0})
}

func boundaryAnthropicSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	boundaryAnthropicText(w, rootActionResponse(domain.RootActionContinue, "recovered reply", "", ""))
	boundaryAnthropicEvent(w, map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 3}})
	boundaryAnthropicEvent(w, map[string]any{"type": "message_stop"})
	w.(http.Flusher).Flush()
}

func boundaryHTTPAnthropic(t *testing.T, endpoint string, timeout time.Duration) *llm.AnthropicCompatibleProvider {
	t.Helper()
	p, err := llm.NewAnthropicCompatibleProvider(llm.AnthropicCompatibleConfig{
		Name: "usage-test", BaseURL: endpoint, APIKey: "fixture-only", DefaultModel: "model",
		HTTPClient: &http.Client{Timeout: timeout},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func boundaryMoneySupervisor(t *testing.T, st *store.SQLiteStore, provider llm.Provider, budget domain.Budget) (domain.Run, *application.AgentRunner) {
	t.Helper()
	run, supervisor, router := newQualifiedHTTPProviderSupervisor(t, st, provider, budget)
	importSupervisorPriceSnapshot(t, t.Context(), st)
	window := llm.DefaultContextWindow()
	window.DefaultOutputTokens, window.MaxOutputTokens = 256, 512
	if err := router.SetContextWindow(llm.ModelRef{Provider: provider.Name(), Model: "model"}, window); err != nil {
		t.Fatal(err)
	}
	return run, supervisor.WithMonetaryBudget(application.NewMonetaryBudgetService(st)).
		WithModelRetryPolicy(application.ModelRetryPolicy{MaxAttempts: 2})
}

func assertBoundaryNoTools(t *testing.T, st *store.SQLiteStore, runID string) {
	t.Helper()
	timeline, err := st.ListRunEvents(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{events.SupervisorToolBatchEvent, events.SupervisorToolExecutionStartedEvent, events.ToolStartedEvent} {
		if countEventType(timeline, kind) != 0 {
			t.Fatalf("failed response dispatched a tool: %s", kind)
		}
	}
	notes, err := st.ListNotes(t.Context(), domain.NoteFilter{RunID: runID})
	if err != nil || len(notes) != 0 {
		t.Fatalf("failed tool created durable work: notes=%+v err=%v", notes, err)
	}
}

func TestSupervisorHTTPTimeoutRetriesWithoutFailedOutputOrTools(t *testing.T) {
	for _, phase := range []string{"header", "partial_text", "completed_tool_without_terminal"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "http-retry.db")
			st, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			var calls atomic.Int64
			release := make(chan struct{})
			requests := make(chan string, 2)
			firstMoney := make(chan domain.MonetaryUsage, 1)
			var run domain.Run
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				requests <- string(raw)
				if calls.Add(1) > 1 {
					boundaryAnthropicSuccess(w)
					return
				}
				money, err := st.GetMonetaryUsage(t.Context(), run.ID)
				if err != nil {
					t.Error(err)
				}
				firstMoney <- money
				if phase != "header" {
					w.Header().Set("Content-Type", "text/event-stream")
					boundaryAnthropicText(w, rootActionResponse(domain.RootActionContinue, "uncommitted-answer-canary", "", ""))
					if phase == "completed_tool_without_terminal" {
						boundaryAnthropicEvent(w, map[string]any{"type": "content_block_start", "index": 1,
							"content_block": map[string]any{"type": "tool_use", "id": "failed_tool_canary", "name": "note_create", "input": map[string]any{}}})
						boundaryAnthropicEvent(w, map[string]any{"type": "content_block_delta", "index": 1,
							"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"title":"failed-note-canary"}`}})
						boundaryAnthropicEvent(w, map[string]any{"type": "content_block_stop", "index": 1})
					}
					_, _ = io.WriteString(w, `data: {"unfinished":`)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(release) })
			provider := boundaryHTTPAnthropic(t, server.URL, 400*time.Millisecond)
			var supervisor *application.AgentRunner
			run, supervisor = boundaryMoneySupervisor(t, st, provider, domain.Budget{MaxTurns: 3, MaxCostUSD: 1})
			result, err := supervisor.Step(t.Context(), run.ID)
			if err != nil || result.ModelAttempts != 2 || calls.Load() != 2 || result.Text != "recovered reply" ||
				result.ProtocolRepairs != 0 || result.ToolCalls != 0 || result.ModelOutcome != llm.OutcomeSuccess {
				t.Fatalf("bounded retry did not recover cleanly: calls=%d result=%+v err=%v", calls.Load(), result, err)
			}
			firstReserve := <-firstMoney
			if firstReserve.ReservedMicros <= 0 {
				t.Fatal("sent request lacked a monetary reservation")
			}
			_ = <-requests
			second := <-requests
			for _, marker := range []string{"uncommitted-answer-canary", "failed_tool_canary", "failed-note-canary"} {
				if strings.Contains(second, marker) {
					t.Fatalf("failed output or tool pairing replayed in retry: %s", marker)
				}
			}
			timeline, err := st.ListRunEvents(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if countEventType(timeline, events.ModelStartedEvent) != 2 || countEventType(timeline, events.ModelFailedEvent) != 1 ||
				countEventType(timeline, events.ModelCompletedEvent) != 1 || countEventType(timeline, events.ProtocolRepairRequestedEvent) != 0 ||
				countEventType(timeline, events.RunExecutionLeaseReleasedEvent) != 1 {
				t.Fatalf("retry receipt sequence changed: %+v", timeline)
			}
			assertBoundaryNoTools(t, st, run.ID)
			messages, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range messages {
				if strings.Contains(message.Content, "uncommitted-answer-canary") {
					t.Fatal("failed model text committed as conversation history")
				}
			}
			money := readOrdinaryMoneyUsage(t, st, run.ID)
			if money.SettledMicros != firstReserve.ReservedMicros+8 ||
				money.ReleasedMicros != money.ReservedMicros-money.SettledMicros {
				t.Fatalf("unknown first usage became free or partial usage was accepted: initial=%+v final=%+v", firstReserve, money)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			recovered := readOrdinaryMoneyUsage(t, reopened, run.ID)
			if !reflect.DeepEqual(recovered, money) || calls.Load() != 2 {
				t.Fatalf("reopen changed accounting or replayed HTTP: original=%+v recovered=%+v calls=%d", money, recovered, calls.Load())
			}
			t.Logf("actual_http_calls=2 failed_tools=0 protocol_repairs=0 unknown_settled=%d known_settled=8", firstReserve.ReservedMicros)
		})
	}
}

func TestSupervisorHTTPCallerStopsDoNotRetryAndRetainUnknownCost(t *testing.T) {
	for _, stop := range []string{"caller_cancel", "run_deadline"} {
		t.Run(stop, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "http-stop.db")
			st, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if calls.Add(1) == 1 {
					close(started)
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(release) })
			budget := domain.Budget{MaxTurns: 3, MaxCostUSD: 1}
			if stop == "run_deadline" {
				budget.TimeoutSeconds = 1
			}
			run, supervisor := boundaryMoneySupervisor(t, st, boundaryHTTPAnthropic(t, server.URL, 5*time.Second), budget)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if stop == "caller_cancel" {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			result, err := supervisor.Step(ctx, run.ID)
			code := apperror.CodeCancelled
			phase := domain.SupervisorTurnStarted
			if stop == "run_deadline" {
				code, phase = apperror.CodeDeadlineExceeded, domain.SupervisorTurnFailed
			}
			if apperror.CodeOf(err) != code || result.ModelOutcome != llm.OutcomeCancelled || result.ModelAttempts != 1 ||
				calls.Load() != 1 || result.Checkpoint.Phase != phase {
				t.Fatalf("caller stop retried or lost recovery state: result=%+v code=%s calls=%d err=%v", result, apperror.CodeOf(err), calls.Load(), err)
			}
			assertOrdinaryMoneyNoAssistant(t, st, run)
			assertBoundaryNoTools(t, st, run.ID)
			money := readOrdinaryMoneyUsage(t, st, run.ID)
			if money.ReservedMicros <= 0 || money.ReleasedMicros != 0 {
				t.Fatalf("cancelled sent request became free: %+v", money)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if recovered := readOrdinaryMoneyUsage(t, reopened, run.ID); !reflect.DeepEqual(recovered, money) || calls.Load() != 1 {
				t.Fatalf("stop accounting changed after reopen: initial=%+v recovered=%+v", money, recovered)
			}
		})
	}
}

func TestSupervisorHTTPTimeoutRechecksMoneyBeforeRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "http-money-gate.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var calls atomic.Int64
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	// The native request/schema estimate plus the explicit 256-token output
	// window fits one reservation at the fixture price, but not two.
	run, supervisor := boundaryMoneySupervisor(t, st, boundaryHTTPAnthropic(t, server.URL, 400*time.Millisecond),
		domain.Budget{MaxTurns: 3, MaxCostUSD: 0.04})
	result, err := supervisor.Step(t.Context(), run.ID)
	if apperror.CodeOf(err) != apperror.CodeResourceExhausted || calls.Load() != 1 {
		t.Fatalf("retry ignored the remaining monetary allowance: calls=%d result=%+v err=%v", calls.Load(), result, err)
	}
	assertOrdinaryMoneyNoAssistant(t, st, run)
	assertBoundaryNoTools(t, st, run.ID)
	money := readOrdinaryMoneyUsage(t, st, run.ID)
	if money.ReservedMicros <= money.CapMicros/2 || money.SettledMicros != money.ReservedMicros || money.ReleasedMicros != 0 {
		t.Fatalf("unknown sent request was released to finance a retry: %+v", money)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if recovered := readOrdinaryMoneyUsage(t, reopened, run.ID); !reflect.DeepEqual(recovered, money) || calls.Load() != 1 {
		t.Fatalf("unknown monetary charge changed after reopen: %+v", recovered)
	}
}
