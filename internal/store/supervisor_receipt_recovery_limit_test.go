package store

import (
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/llm"
	"testing"
)

func TestReceiptRecoveryPlanningLimitPersistsAndRemainsPhaseScoped(t *testing.T) {
	f := newProviderReplayFixture(t)
	cp := recordContextRecoverySource(t, f)
	if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); err != nil || !claimed {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, err = Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.store.Close()
	limit, found, err := f.store.SupervisorContextRecoveryInputLimit(t.Context(), cp, 0, 0)
	if err != nil || !found || limit != f.attempt.InputEstimate-1 {
		t.Fatalf("limit=%d found=%v err=%v", limit, found, err)
	}
	for _, phase := range [][2]int{{1, 0}, {0, 1}} {
		if _, found, err := f.store.SupervisorContextRecoveryInputLimit(t.Context(), cp, phase[0], phase[1]); err != nil || found {
			t.Fatal("recovery leaked into another phase", err)
		}
	}
	stale := cp
	stale.AttemptID = "different-attempt"
	if _, _, err := f.store.SupervisorContextRecoveryInputLimit(t.Context(), stale, 0, 0); err == nil {
		t.Fatal("stale checkpoint read capacity")
	}
}

func TestReceiptRecoveryPlanningZeroCapacityIsNotUnlimited(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.attempt.InputEstimate = 1
	cp := recordContextRecoverySource(t, f)
	if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); err != nil || !claimed {
		t.Fatal(err)
	}
	limit, found, err := f.store.SupervisorContextRecoveryInputLimit(t.Context(), cp, 0, 0)
	if err != nil || !found || limit != 0 {
		t.Fatalf("limit=%d found=%v err=%v", limit, found, err)
	}
}

func TestReceiptRecoveryPlanningRejectsUnclaimedAndExhaustedPhase(t *testing.T) {
	f := newProviderReplayFixture(t)
	cp := recordContextRecoverySource(t, f)
	if _, _, err := f.store.SupervisorContextRecoveryInputLimit(t.Context(), cp, 0, 0); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatal("unclaimed failure permits planning", err)
	}
	if claimed, err := f.store.ClaimSupervisorContextRecovery(t.Context(), cp, f.attempt); !claimed || err != nil {
		t.Fatal(err)
	}
	next := f.attempt
	next.Number, next.TransportAttempt, next.Outcome, next.FailureReason, next.ErrorText = 2, 1, "", "", ""
	next.InputEstimate--
	if _, err := f.store.RecordSupervisorModelStarted(t.Context(), cp, next); err != nil {
		t.Fatal(err)
	}
	next.Outcome, next.FailureReason, next.ErrorText = llm.OutcomePermanent, llm.ProviderFailureContextLimit, "still too large"
	var err error
	cp, err = f.store.RecordSupervisorModelFailed(t.Context(), cp, next)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, err = Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.SupervisorContextRecoveryInputLimit(t.Context(), cp, 0, 0); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatal("exhausted recovery permits planning after reopen", err)
	}
}
