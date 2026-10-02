package application

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/standardcodedelivery"
	"cyberagent-workbench/internal/store"
)

// These projection fixtures exercise the actual Go parsers/projector and SQLite
// completion boundary. A synthetic delivery report is not execution evidence.
func TestKimiNativeReplyAllowsAcceptedGoActionProjections(t *testing.T) {
	for _, name := range []string{"verified_delivery", "thread_finish_summary", "plain_operator_reply", "trailing_commentary"} {
		t.Run(name, func(t *testing.T) {
			native := `{"version":"root_lifecycle.v1","action":"finish","message":"Provider-only wording","summary":"Provider-only summary"}`
			var action domain.RootAction
			var err error
			switch name {
			case "verified_delivery":
				action, err = parseRootAction(native)
				machine, _ := newStandardCodeSupervisorTestMachine(domain.StandardCodeSupervisorDeliver, domain.ExecutionPhaseDeliver)
				machine.report = &standardcodedelivery.Report{Status: standardcodedelivery.StatusPassed, Verified: true,
					ReceiptSHA256: strings.Repeat("a", 64), FinalCheckpoint: standardcodedelivery.Checkpoint{ID: "checkpoint-final"},
					Links: standardcodedelivery.Links{Self: "/synthetic-delivery-report"}}
				action = machine.ProjectDeliveryAction(action)
			case "thread_finish_summary":
				native = `{"version":"root_lifecycle.v1","action":"finish","message":"This reply is complete"}`
				action, err = parseRootActionForTurn(native, true)
			case "plain_operator_reply":
				native = "Here is the bounded plain operator reply."
				var ok bool
				action, ok = publicReplyRootAction(native)
				if !ok {
					t.Fatal("existing plain reply recovery refused the fixture")
				}
			case "trailing_commentary":
				native += "\nThis is bounded provider commentary."
				var ok bool
				action, _, ok = recoverRootActionWithTrailingCommentary(native)
				if !ok {
					t.Fatal("existing commentary recovery refused the fixture")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "kimi-projection.db")
			st, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			service := NewRunService(st)
			_, run, err := service.Create(t.Context(), CreateRunRequest{Goal: "validate existing Go projection", Profile: "review",
				ModelRoute: "kimi-projection/model", Budget: domain.Budget{MaxTurns: 3}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Start(t.Context(), run.ID); err != nil {
				t.Fatal(err)
			}
			claim, err := st.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{
				RunID: run.ID, OwnerID: "kimi-projection-test", TTL: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			turn, err := st.BeginSupervisorTurn(t.Context(), claim.Lease, "Use the current accepted Go action")
			if err != nil {
				t.Fatal(err)
			}
			attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "kimi-projection", Model: "model"}
			if _, err := st.RecordSupervisorModelStarted(t.Context(), turn.Checkpoint, attempt); err != nil {
				t.Fatal(err)
			}
			id := "native-projection-" + name
			private, err := json.Marshal(map[string]any{"version": 4, "provider": attempt.Provider, "model": attempt.Model,
				"transport": "openai_chat_completions", "binding": strings.Repeat("a", 64), "response_id": id,
				"parts": []any{map[string]any{"kind": "kimi_metadata", "id": llm.StableStreamID("kimi-native-replay", id, "metadata", "0"),
					"opaque": map[string]any{"wire_model": "kimi-k3", "upstream_model": "kimi-k3", "content_state": "string", "reasoning_content": "private-projection-sentinel"}},
					map[string]any{"kind": "text", "id": llm.StableStreamID("kimi-native-replay", id, "text", "0"), "text": native}},
				"calls": []any{}})
			if err != nil {
				t.Fatal(err)
			}
			replay, err := llm.DecodeProviderReplay(private)
			if err != nil {
				t.Fatal(err)
			}
			response := llm.ChatResponse{Provider: attempt.Provider, Model: attempt.Model, Text: native, Replay: replay,
				Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
			attempt.Outcome = llm.OutcomeSuccess
			checkpoint, err := st.RecordSupervisorModelCompleted(t.Context(), turn.Checkpoint, attempt, response)
			if err != nil {
				t.Fatal(err)
			}
			completed, _, messages, err := st.CompleteSupervisorTurn(t.Context(), checkpoint, response, action, policy.Decision{Allowed: true}, 0)
			if err != nil || messages.Assistant.Content != action.Message {
				t.Fatal("Go projection conflicted with native history", err)
			}
			if action.Kind == domain.RootActionFinish && completed.Status != domain.RunCompleted {
				t.Fatal("accepted finish did not complete")
			}
			if _, _, _, err := st.CompleteSupervisorTurn(t.Context(), checkpoint, response, action, policy.Decision{Allowed: true}, 0); err != nil {
				t.Fatal("exact native and accepted-action retry failed", err)
			}
			changed := action
			changed.Message = "A changed accepted projection"
			if _, _, _, err := st.CompleteSupervisorTurn(t.Context(), checkpoint, response, changed, policy.Decision{Allowed: true}, 0); err == nil {
				t.Fatal("retry changed the Go-accepted action")
			}
			list, err := st.ListRunEvents(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			completedEvents := 0
			for _, event := range list {
				if event.Type == events.AgentTurnCompletedEvent {
					completedEvents++
					var payload map[string]any
					if json.Unmarshal([]byte(event.PayloadJSON), &payload) != nil {
						t.Fatal("invalid completion event")
					}
					digest, _ := payload["accepted_action_sha256"].(string)
					if len(digest) != 64 {
						t.Fatal("accepted projection has no immutable completion seal")
					}
				}
			}
			if completedEvents != 1 {
				t.Fatal("completion retry duplicated an accepted event")
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := st.CompleteSupervisorTurn(t.Context(), checkpoint, response, action, policy.Decision{Allowed: true}, 0); err != nil {
				t.Fatal("reopen changed native/projection retry", err)
			}
			if action.Kind == domain.RootActionContinue {
				next, err := st.BeginSupervisorTurn(t.Context(), claim.Lease, "Fresh user follow-up")
				if err != nil {
					t.Fatal(err)
				}
				loaded, err := st.LoadSupervisorAssistantHistory(t.Context(), next.Checkpoint, []int64{messages.Assistant.ID})
				if err != nil || loaded[messages.Assistant.ID].Replay.AssistantText() != native {
					t.Fatal("plain native reply lost during reopen", err)
				}
			}
		})
	}
}
