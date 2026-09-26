package store

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
)

func recordContextRecoverySource(t *testing.T, f *providerReplayFixture) domain.SupervisorCheckpoint {
	t.Helper()
	f.start(t)
	f.attempt.Outcome, f.attempt.FailureReason, f.attempt.ErrorText = llm.OutcomePermanent, llm.ProviderFailureContextLimit, "input context limit"
	cp, err := f.store.RecordSupervisorModelFailed(t.Context(), f.turn.Checkpoint, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	return cp
}

func TestSupervisorContextRecoveryRequiresReductionAtAtomicStart(t *testing.T) {
	for _, owner := range []string{"same_lease", "reopen", "new_lease"} {
		t.Run(owner, func(t *testing.T) {
			f := newProviderReplayFixture(t)
			cp := recordContextRecoverySource(t, f)
			if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); err != nil || !claimed {
				t.Fatalf("claim=%t err=%v", claimed, err)
			}
			if owner != "same_lease" {
				if err := f.store.Close(); err != nil {
					t.Fatal(err)
				}
				var err error
				f.store, err = Open(f.path)
				if err != nil {
					t.Fatal(err)
				}
			}
			if owner == "new_lease" {
				expireTestRunExecutionLease(t, t.Context(), f.store, f.lease)
				lease := acquireTestRunExecutionLease(t, t.Context(), f.store, f.turn.Run.ID)
				recovered, err := f.store.BeginSupervisorTurn(t.Context(), lease, "continue tools")
				if err != nil {
					t.Fatal(err)
				}
				cp = recovered.Checkpoint
			}
			global, transport, err := f.store.NextSupervisorModelAttempt(t.Context(), cp, 0, 0)
			if err != nil || global != 2 || transport != 1 {
				t.Fatalf("recovery group=%d/%d err=%v", global, transport, err)
			}
			next := f.attempt
			next.Number, next.TransportAttempt, next.Outcome, next.FailureReason, next.ErrorText = global, transport, "", "", ""
			for _, estimate := range []int{0, f.attempt.InputEstimate, f.attempt.InputEstimate + 1} {
				next.InputEstimate = estimate
				if err := f.store.CheckSupervisorContextRecoveryInput(t.Context(), cp, next); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
					t.Fatalf("estimate=%d passed recovery preflight: %v", estimate, err)
				}
				if inserted, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); inserted || apperror.CodeOf(err) != apperror.CodeResourceExhausted {
					t.Fatalf("estimate=%d authorized unchanged recovery: inserted=%t err=%v", estimate, inserted, err)
				}
			}
			log, err := f.store.ListRunEvents(t.Context(), cp.RunID)
			if err != nil || countRunEventType(log, events.ModelStartedEvent) != 1 {
				t.Fatal("rejected starts left model work", err)
			}
			next.InputEstimate = f.attempt.InputEstimate - 1
			if err := f.store.CheckSupervisorContextRecoveryInput(t.Context(), cp, next); err != nil {
				t.Fatal("effective reduction failed preflight", err)
			}
			if inserted, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); !inserted || err != nil {
				t.Fatalf("effective reduction rejected: inserted=%t err=%v", inserted, err)
			}
			if inserted, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); inserted || err != nil {
				t.Fatalf("exact start replay changed: inserted=%t err=%v", inserted, err)
			}
			next.InputEstimate--
			if _, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatal("start replay changed its reduction proof", err)
			}
		})
	}
}

func TestSupervisorContextRecoveryCannotRetryBetweenFailureAndClaim(t *testing.T) {
	for _, owner := range []string{"same_lease", "reopen", "new_lease"} {
		t.Run(owner, func(t *testing.T) {
			f := newProviderReplayFixture(t)
			f.attempt.MaxAttempts = 3
			cp := recordContextRecoverySource(t, f)
			if owner != "same_lease" {
				if err := f.store.Close(); err != nil {
					t.Fatal(err)
				}
				var err error
				f.store, err = Open(f.path)
				if err != nil {
					t.Fatal(err)
				}
			}
			if owner == "new_lease" {
				expireTestRunExecutionLease(t, t.Context(), f.store, f.lease)
				lease := acquireTestRunExecutionLease(t, t.Context(), f.store, f.turn.Run.ID)
				turn, err := f.store.BeginSupervisorTurn(t.Context(), lease, "continue tools")
				if err != nil {
					t.Fatal(err)
				}
				cp = turn.Checkpoint
			}
			next := f.attempt
			next.Number, next.TransportAttempt, next.Outcome, next.FailureReason, next.ErrorText = 2, 2, "", "", ""
			if err := f.store.CheckSupervisorContextRecoveryInput(t.Context(), cp, next); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
				t.Fatalf("unclaimed overflow passed preflight: %v", err)
			}
			if started, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); started || apperror.CodeOf(err) != apperror.CodeResourceExhausted {
				t.Fatalf("unclaimed overflow restarted unchanged input: started=%v err=%v", started, err)
			}
			log, err := f.store.ListRunEvents(t.Context(), cp.RunID)
			if err != nil || countRunEventType(log, events.ModelStartedEvent) != 1 {
				t.Fatal("rejected recovery added a model attempt", err)
			}
		})
	}
}

func TestSupervisorContextRecoveryEstimateComesOnlyFromDurableStart(t *testing.T) {
	t.Run("missing_source_estimate", func(t *testing.T) {
		f := newProviderReplayFixture(t)
		f.attempt.InputEstimate = 0
		cp := recordContextRecoverySource(t, f)
		f.attempt.InputEstimate = 1000
		if _, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); err == nil {
			t.Fatal("caller supplied an estimate missing from the durable source")
		}
	})
	t.Run("changed_caller_estimate", func(t *testing.T) {
		f := newProviderReplayFixture(t)
		cp := recordContextRecoverySource(t, f)
		original := f.attempt.InputEstimate
		_, err := f.store.db.Exec(`INSERT INTO run_supervisor_context_recoveries
			(run_id,turn,attempt_id,tool_round,protocol_repair,model_attempt,original_input_tokens,provider,model,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, cp.RunID, cp.NextTurn, cp.AttemptID, 0, 0, f.attempt.Number, original+1, f.attempt.Provider, f.attempt.Model, ts(time.Now().UTC()))
		if err == nil {
			t.Fatal("source trigger accepted an estimate different from the original request")
		}
		f.attempt.InputEstimate = original * 2
		if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); !claimed || err != nil {
			t.Fatalf("claim=%t err=%v", claimed, err)
		}
		var stored int
		if err := f.store.db.QueryRow(`SELECT original_input_tokens FROM run_supervisor_context_recoveries WHERE run_id=?`, cp.RunID).Scan(&stored); err != nil || stored != original {
			t.Fatalf("caller changed durable original input: got=%d want=%d err=%v", stored, original, err)
		}
	})
}

func TestSupervisorContextRecoveryReductionIsScopedToModelPhase(t *testing.T) {
	for _, phase := range []string{"tool_round", "protocol_repair"} {
		t.Run(phase, func(t *testing.T) {
			f := newProviderReplayFixture(t)
			cp := recordContextRecoverySource(t, f)
			if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); !claimed || err != nil {
				t.Fatalf("claim=%t err=%v", claimed, err)
			}
			next := f.attempt
			next.Number, next.TransportAttempt, next.Outcome, next.FailureReason, next.ErrorText = 2, 1, "", "", ""
			next.InputEstimate--
			if _, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); err != nil {
				t.Fatal(err)
			}
			var err error
			if phase == "tool_round" {
				next.Outcome = llm.OutcomeSuccess
				cp, err = f.store.RecordSupervisorModelCompleted(t.Context(), cp, next, f.response)
				if err != nil {
					t.Fatal(err)
				}
				callID := f.response.ToolCalls[0].ID
				if _, err := f.store.RecordSupervisorToolExecutionStarted(t.Context(), cp, callID); err != nil {
					t.Fatal(err)
				}
				if _, _, err := f.store.RecordSupervisorToolResult(t.Context(), cp, domain.SupervisorToolResult{CallID: callID, Status: domain.SupervisorToolCompleted, ResultJSON: `{"ok":true}`, CompletedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
				next.ToolRound = 1
			} else {
				cp, err = f.store.RecordSupervisorProtocolFailure(t.Context(), cp, next, llm.ChatResponse{Text: "invalid root action", Usage: f.response.Usage}, "invalid root action", true)
				if err != nil {
					t.Fatal(err)
				}
				next.ProtocolRepair = 1
			}
			next.Number, next.TransportAttempt, next.Outcome = 3, 1, ""
			next.InputEstimate = f.attempt.InputEstimate * 2
			if inserted, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); !inserted || err != nil {
				t.Fatalf("old phase claim restricted new phase: inserted=%t err=%v", inserted, err)
			}
			next.Outcome, next.FailureReason, next.ErrorText = llm.OutcomePermanent, llm.ProviderFailureContextLimit, "new phase context limit"
			cp, err = f.store.RecordSupervisorModelFailed(t.Context(), cp, next)
			if err != nil {
				t.Fatal(err)
			}
			if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, next); !claimed || err != nil {
				t.Fatalf("new phase claim=%t err=%v", claimed, err)
			}
			next.Number, next.TransportAttempt, next.Outcome, next.FailureReason, next.ErrorText = 4, 1, "", "", ""
			if _, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
				t.Fatal("new phase bypassed its own reduction bound", err)
			}
			next.InputEstimate--
			if _, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); err != nil {
				t.Fatal("new phase reduction rejected", err)
			}
			if phase == "tool_round" {
				rounds, err := f.store.ListSupervisorToolRounds(t.Context(), cp)
				if err != nil || len(rounds) != 1 || !rounds[0].Complete() || len(rounds[0].Calls) != 1 || rounds[0].Calls[0].ResultJSON != `{"ok":true}` {
					t.Fatal("recovery changed the completed tool receipt", err)
				}
			}
		})
	}
}

func TestSupervisorContextRecoverySecondLimitCannotResumeAsTransportRetry(t *testing.T) {
	for _, secondLimit := range []bool{true, false} {
		name := "network_retry_remains_available"
		if secondLimit {
			name = "second_context_limit_is_terminal"
		}
		t.Run(name, func(t *testing.T) {
			f := newProviderReplayFixture(t)
			cp := recordContextRecoverySource(t, f)
			if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); !claimed || err != nil {
				t.Fatalf("claim=%t err=%v", claimed, err)
			}
			next := f.attempt
			next.Number, next.TransportAttempt, next.Outcome, next.FailureReason, next.ErrorText = 2, 1, "", "", ""
			next.InputEstimate--
			if _, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); err != nil {
				t.Fatal(err)
			}
			started := next
			next.Outcome, next.FailureReason, next.ErrorText = llm.OutcomeRetryable, llm.ProviderFailureNetwork, "network unavailable"
			next.RetryPlanned = true
			if secondLimit {
				next.Outcome, next.FailureReason, next.ErrorText = llm.OutcomePermanent, llm.ProviderFailureContextLimit, "still too large"
				next.RetryPlanned = false
			}
			cp, err := f.store.RecordSupervisorModelFailed(t.Context(), cp, next)
			if err != nil {
				t.Fatal(err)
			}
			if inserted, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, started); inserted || err != nil {
				t.Fatalf("exact old start replay was rejected: inserted=%t err=%v", inserted, err)
			}
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			f.store, err = Open(f.path)
			if err != nil {
				t.Fatal(err)
			}
			expireTestRunExecutionLease(t, t.Context(), f.store, f.lease)
			lease := acquireTestRunExecutionLease(t, t.Context(), f.store, f.turn.Run.ID)
			recovered, err := f.store.BeginSupervisorTurn(t.Context(), lease, "continue tools")
			if err != nil {
				t.Fatal(err)
			}
			cp = recovered.Checkpoint
			next = started
			next.Number, next.TransportAttempt, err = f.store.NextSupervisorModelAttempt(t.Context(), cp, 0, 0)
			if err != nil || next.Number != 3 || next.TransportAttempt != 2 {
				t.Fatalf("unexpected recovery group counters: %d/%d %v", next.Number, next.TransportAttempt, err)
			}
			preflightErr := f.store.CheckSupervisorContextRecoveryInput(t.Context(), cp, next)
			inserted, startErr := f.store.RecordSupervisorModelStarted(t.Context(), cp, next)
			if secondLimit {
				if inserted || apperror.CodeOf(preflightErr) != apperror.CodeResourceExhausted || apperror.CodeOf(startErr) != apperror.CodeResourceExhausted {
					t.Fatalf("second overflow resumed: inserted=%t preflight=%v start=%v", inserted, preflightErr, startErr)
				}
			} else if !inserted || preflightErr != nil || startErr != nil {
				t.Fatalf("bounded network retry rejected: inserted=%t preflight=%v start=%v", inserted, preflightErr, startErr)
			}
		})
	}
}
