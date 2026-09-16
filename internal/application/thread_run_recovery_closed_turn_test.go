package application_test

import (
	"context"
	"reflect"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

// A service read can finish before another connection closes the failed turn.
// Return that exact earlier read to exercise the subsequent write fence without
// goroutine scheduling or sleeps deciding whether the interleaving occurred.
type staleClosedTurnRecoveryStore struct {
	*store.SQLiteStore
	candidate domain.ThreadRunRecovery
}

func (s *staleClosedTurnRecoveryStore) GetThreadRunRecovery(context.Context, string) (domain.ThreadRunRecovery, bool, error) {
	return s.candidate, true, nil
}

func TestThreadRunRecoveryRejectsTurnClosedAfterCandidateRead(t *testing.T) {
	for _, path := range []string{"store_recovery", "store_next_turn", "service_stale_read"} {
		t.Run(path, func(t *testing.T) {
			provider := &lifecycleProvider{failures: []error{
				apperror.New(apperror.CodeFailedPrecondition, "failure before ordinary turn closure"),
			}}
			st, _, request := threadControlFixture(t, provider)
			router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
			router.RegisterProvider(provider)
			legacy := application.NewThreadTurnService(&historicalFailedTurnStore{st},
				application.NewRunLifecycleControlService(st),
				application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
			first, err := legacy.Execute(t.Context(), request)
			if err == nil || first.Execution == nil || provider.calls != 1 {
				t.Fatalf("fixture did not leave exactly one failed attempt: %#v calls=%d err=%v", first, provider.calls, err)
			}
			handoff := first.Execution.Handoff
			runID := first.Submission.Run.ID
			candidate, found, err := st.GetThreadRunRecovery(t.Context(), request.ThreadID)
			if err != nil || !found || !candidate.Quiescent || !candidate.Disposition.AllowsRunRecovery() ||
				candidate.RunID != runID || candidate.HandoffOperationID != handoff.Operation.ID {
				t.Fatalf("expected exact recoverable candidate: %#v found=%t err=%v", candidate, found, err)
			}
			failure, closed, err := st.EndFailedThreadTurn(t.Context(), request.ThreadID, runID, handoff.Operation.ID)
			if err != nil || !closed {
				t.Fatalf("fixture could not close the observed failed input: %#v closed=%t err=%v", failure, closed, err)
			}
			if recovery, found, err := st.GetThreadRunRecovery(t.Context(), request.ThreadID); err != nil || found {
				t.Fatalf("closed turn still surfaced for recovery: %#v found=%t err=%v", recovery, found, err)
			}
			queued, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{
				RunID: runID, SessionID: first.Submission.Run.SessionID,
				Content:      "new input must not be cancelled by a stale recovery",
				OperationKey: "queued-after-closed-failure", RequestedBy: request.RequestedBy,
			})
			if err != nil {
				t.Fatal(err)
			}
			beforeRun, err := st.GetRun(t.Context(), runID)
			if err != nil || beforeRun.Status != domain.RunPaused {
				t.Fatalf("closed failure should remain paused and continuable: %#v err=%v", beforeRun, err)
			}
			beforeCheckpoint, _, err := st.GetSupervisorCheckpoint(t.Context(), runID)
			if err != nil || beforeCheckpoint.AttemptID != "" || beforeCheckpoint.Phase != domain.SupervisorIdle {
				t.Fatalf("closure did not settle the failed attempt: %#v err=%v", beforeCheckpoint, err)
			}
			beforeEvents, err := st.ListRunEvents(t.Context(), runID)
			if err != nil {
				t.Fatal(err)
			}
			recoverRequest := application.RecoverThreadRunRequest{
				Version: domain.ThreadRunRecoveryProtocolVersion, ThreadID: request.ThreadID,
				RunID: runID, HandoffOperationID: handoff.Operation.ID,
				OperationKey: "stale-recovery-after-closure", RequestedBy: request.RequestedBy,
			}
			switch path {
			case "store_recovery":
				_, _, _, err = st.RecoverThreadRunFromFailedHandoff(t.Context(), recoverRequest.ThreadID,
					runID, recoverRequest.HandoffOperationID, recoverRequest.RequestedBy, recoverRequest.OperationKey)
			case "store_next_turn":
				_, _, _, err = st.ContinueThreadRunFromFailedHandoff(t.Context(), recoverRequest.ThreadID,
					runID, recoverRequest.HandoffOperationID, recoverRequest.RequestedBy, recoverRequest.OperationKey)
			case "service_stale_read":
				_, err = application.NewThreadRunRecoveryService(&staleClosedTurnRecoveryStore{st, candidate}).
					Recover(t.Context(), recoverRequest)
			}
			if apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("stale recovery crossed the closed-turn boundary: %v", err)
			}
			afterRun, err := st.GetRun(t.Context(), runID)
			if err != nil || !reflect.DeepEqual(beforeRun, afterRun) {
				t.Fatalf("stale recovery mutated the continuable Run: before=%#v after=%#v err=%v", beforeRun, afterRun, err)
			}
			afterCheckpoint, _, err := st.GetSupervisorCheckpoint(t.Context(), runID)
			if err != nil || !reflect.DeepEqual(beforeCheckpoint, afterCheckpoint) {
				t.Fatalf("stale recovery mutated the closed checkpoint: %#v err=%v", afterCheckpoint, err)
			}
			afterEvents, err := st.ListRunEvents(t.Context(), runID)
			if err != nil || !reflect.DeepEqual(beforeEvents, afterEvents) {
				t.Fatalf("rejected recovery changed the durable event ledger: err=%v", err)
			}
			saved, found, err := st.GetRunExecutionHandoff(t.Context(), handoff.Operation.KeyDigest)
			if err != nil || !found || !reflect.DeepEqual(saved, handoff) {
				t.Fatalf("stale recovery rewrote the original handoff: %#v err=%v", saved, err)
			}
			savedFailure, found, err := st.GetThreadTurnFailure(t.Context(), runID, first.Submission.Message.ID)
			if err != nil || !found || !reflect.DeepEqual(savedFailure, failure) {
				t.Fatalf("stale recovery rewrote the sealed failure: %#v err=%v", savedFailure, err)
			}
			savedInput, err := st.GetOperatorSteering(t.Context(), queued.Message.ID)
			if err != nil || savedInput.Status != domain.OperatorSteeringPending || provider.calls != 1 {
				t.Fatalf("stale recovery cancelled or replayed input: %#v calls=%d err=%v", savedInput, provider.calls, err)
			}
		})
	}
}
