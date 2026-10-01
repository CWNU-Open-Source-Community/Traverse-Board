package application_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/store"
)

func ollamaTerminationHTTPFixture(t *testing.T, text, tools, reason string,
	beforeTerminal bool,
) (*llm.OllamaProvider, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"capabilities": []string{"completion", "tools"},
				"model_info":   map[string]any{"fixture.context_length": 4096},
			})
		case "/api/chat":
			calls.Add(1)
			var request struct {
				Stream bool `json:"stream"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.Stream {
				t.Errorf("application did not send a native streaming request: %+v err=%v", request, err)
				return
			}
			message := map[string]any{"role": "assistant", "content": text}
			if tools != "" {
				var arguments any = map[string]any{
					"title": "ollama-partial-note-canary", "body": "uncommitted note",
				}
				if tools == "invalid" {
					arguments = `{"title":`
				}
				message["tool_calls"] = []map[string]any{{"function": map[string]any{
					"name": "note_create", "arguments": arguments,
				}}}
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			if beforeTerminal {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"model": "model", "done": false, "message": message,
				})
				message = map[string]any{"role": "assistant", "content": ""}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": "model", "done": true, "done_reason": reason, "message": message,
				"prompt_eval_count": 2, "eval_count": 3,
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	provider, err := llm.NewOllamaProvider(llm.OllamaConfig{Name: "usage-test", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ProbeModel(t.Context(), "model"); err != nil {
		t.Fatal(err)
	}
	return provider, calls
}

func ollamaTerminationCases() []struct {
	name           string
	tools          string
	beforeTerminal bool
	reason         string
} {
	return []struct {
		name           string
		tools          string
		beforeTerminal bool
		reason         string
	}{
		{"length_text_same_event", "", false, "length"},
		{"length_text_before_terminal", "", true, "length"},
		{"length_valid_tool_same_event", "valid", false, "length"},
		{"length_valid_tool_before_terminal", "valid", true, "length"},
		{"length_invalid_tool_same_event", "invalid", false, "length"},
		{"length_invalid_tool_before_terminal", "invalid", true, "length"},
		{"normal_stop", "", true, "stop"},
	}
}

func assertOllamaTerminationSettlement(t *testing.T, st *store.SQLiteStore, run domain.Run,
	calls *atomic.Int64, failed, specialist bool,
) {
	t.Helper()
	timeline, err := st.ListRunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantFailed, wantCompleted := 0, 1
	if failed {
		wantFailed, wantCompleted = 1, 0
	}
	if calls.Load() != 1 || countEventType(timeline, events.ModelStartedEvent) != 1 ||
		countEventType(timeline, events.ModelFailedEvent) != wantFailed ||
		countEventType(timeline, events.ModelCompletedEvent) != wantCompleted ||
		countEventType(timeline, events.ProtocolRepairRequestedEvent) != 0 {
		t.Fatalf("terminal retried, repaired or became successful: calls=%d events=%+v", calls.Load(), timeline)
	}
	for _, event := range timeline {
		if event.Type != events.ModelFailedEvent {
			continue
		}
		var receipt struct {
			Outcome       llm.Outcome               `json:"outcome"`
			FailureReason llm.ProviderFailureReason `json:"failure_reason"`
			Error         string                    `json:"error"`
		}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &receipt); err != nil ||
			receipt.Outcome != llm.OutcomePermanent ||
			!specialist && receipt.FailureReason != llm.ProviderFailureOutputLimit ||
			specialist && !strings.Contains(receipt.Error, "stopped at the output limit") {
			t.Fatalf("durable output-limit reason was lost: %+v err=%v", receipt, err)
		}
		if strings.Contains(event.PayloadJSON, "ollama-partial-note-canary") {
			t.Fatal("raw tool arguments escaped in the failure receipt")
		}
	}
	assertBoundaryNoTools(t, st, run.ID)
	money := readOrdinaryMoneyUsage(t, st, run.ID)
	if money.SettledMicros != 8 || money.ReservedMicros <= 8 ||
		money.ReleasedMicros != money.ReservedMicros-8 {
		t.Fatalf("known failed usage became free or unknown: %+v", money)
	}
	usage, err := st.GetRunAgentUsage(t.Context(), run.ID)
	if err != nil || usage.TotalTokens != 5 ||
		!specialist && (usage.RootTokens != 5 || usage.SpecialistTokens != 0) ||
		specialist && (usage.SpecialistTokens != 5 || usage.RootTokens != 0) {
		t.Fatalf("usage charged to the wrong agent: %+v err=%v", usage, err)
	}
	t.Logf("actual_http_calls=1 tokens=5 settled_micros=8 output_limit=%t specialist=%t tools=0 protocol_repairs=0", failed, specialist)
}

func assertOllamaTerminationReopen(t *testing.T, path string, st *store.SQLiteStore,
	run domain.Run, calls *atomic.Int64,
) {
	t.Helper()
	money := readOrdinaryMoneyUsage(t, st, run.ID)
	usage, err := st.GetRunAgentUsage(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
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
	if err != nil || !reflect.DeepEqual(readOrdinaryMoneyUsage(t, reopened, run.ID), money) ||
		!reflect.DeepEqual(recoveredUsage, usage) || calls.Load() != 1 {
		t.Fatalf("reopen changed settlement or repeated HTTP: usage=%+v err=%v calls=%d", recoveredUsage, err, calls.Load())
	}
}

func TestSupervisorOllamaLengthRetainsUsageWithoutToolsOrRetry(t *testing.T) {
	for _, test := range ollamaTerminationCases() {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ollama-root.db")
			st, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			text := rootActionResponse(domain.RootActionContinue, "Ollama lifecycle reply", "", "")
			provider, calls := ollamaTerminationHTTPFixture(t, text, test.tools, test.reason, test.beforeTerminal)
			run, supervisor := boundaryMoneySupervisor(t, st, provider,
				domain.Budget{MaxTurns: 3, MaxToolCalls: 3, MaxCostUSD: 1})
			result, err := supervisor.Step(t.Context(), run.ID)
			failed := test.reason == "length"
			if (err != nil) != failed || result.ModelAttempts != 1 || result.ProtocolRepairs != 0 ||
				result.ToolCalls != 0 || failed && result.ModelOutcome != llm.OutcomePermanent ||
				!failed && (result.ModelOutcome != llm.OutcomeSuccess || result.Text != "Ollama lifecycle reply") {
				t.Fatalf("Supervisor promoted or repaired incomplete output: result=%+v err=%v", result, err)
			}
			if failed && (apperror.CodeOf(err) != apperror.CodeResourceExhausted ||
				llm.ProviderErrorReason(err) != llm.ProviderFailureOutputLimit) {
				t.Fatalf("Supervisor lost the typed output-limit cause: %v", err)
			}
			checkpoint, found, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
			if err != nil || !found || checkpoint.InputTokens != 2 || checkpoint.OutputTokens != 3 ||
				checkpoint.TotalTokens != 5 {
				t.Fatalf("terminal usage was lost: checkpoint=%+v found=%t err=%v", checkpoint, found, err)
			}
			if failed {
				assertOrdinaryMoneyNoAssistant(t, st, run)
			}
			assertOllamaTerminationSettlement(t, st, run, calls, failed, false)
			assertOllamaTerminationReopen(t, path, st, run, calls)
		})
	}
}

func TestSpecialistOllamaLengthRetainsUsageWithoutToolsOrRetry(t *testing.T) {
	for _, test := range ollamaTerminationCases() {
		t.Run(test.name, func(t *testing.T) {
			text := specialistResponse(t, domain.SpecialistAction{Version: domain.SpecialistLifecycleVersion,
				Kind: domain.SpecialistActionContinue, Message: "Ollama lifecycle reply"})
			provider, calls := ollamaTerminationHTTPFixture(t, text, test.tools, test.reason, test.beforeTerminal)
			path, st, run, child, runner, _ := newHTTPMoneySpecialist(t, provider, 1)
			result, err := runner.Step(t.Context(), run.ID, child.ID)
			failed := test.reason == "length"
			if (err != nil) != failed || result.ModelAttempts != 1 || result.ProtocolRepairs != 0 ||
				result.Usage.InputTokens != 2 || result.Usage.OutputTokens != 3 || result.Usage.TotalTokens != 5 ||
				failed && result.ModelOutcome != llm.OutcomePermanent ||
				!failed && (result.ModelOutcome != llm.OutcomeSuccess || result.Action.Message != "Ollama lifecycle reply") {
				t.Fatalf("Specialist promoted or repaired incomplete output: result=%+v err=%v", result, err)
			}
			if failed && (apperror.CodeOf(err) != apperror.CodeResourceExhausted ||
				llm.ProviderErrorReason(err) != llm.ProviderFailureOutputLimit) {
				t.Fatalf("Specialist lost the typed output-limit cause: %v", err)
			}
			attempt, found, err := st.GetAgentAttempt(t.Context(), result.AttemptID)
			if err != nil || !found || attempt.Usage.TotalTokens != 5 {
				t.Fatalf("Specialist lost its durable terminal usage: attempt=%+v found=%t err=%v", attempt, found, err)
			}
			if failed {
				if !strings.Contains(attempt.Failure.Reason, "stopped at the output limit") {
					t.Fatalf("Specialist lost its durable output-limit diagnostic: %+v", attempt.Failure)
				}
				messages, err := st.ListSessionMessages(t.Context(), result.SessionID, true)
				if err != nil {
					t.Fatal(err)
				}
				for _, message := range messages {
					if message.Role == "assistant" {
						t.Fatal("incomplete Specialist output became durable conversation history")
					}
				}
			}
			assertOllamaTerminationSettlement(t, st, run, calls, failed, true)
			assertOllamaTerminationReopen(t, path, st, run, calls)
		})
	}
}
