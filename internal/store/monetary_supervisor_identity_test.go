package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/pricing"
)

func reserveSupervisorMoney(t *testing.T, st *SQLiteStore, runID string, a llm.ModelAttempt, amount int64) {
	t.Helper()
	prices, found, err := st.ActivePriceSnapshot(context.Background())
	if err != nil || !found {
		t.Fatalf("prices %v", err)
	}
	_, replayed, err := st.ReserveModelCost(context.Background(), domain.MonetaryReserveRequest{RunID: runID, Scope: domain.MonetaryScopeRoot, Provider: a.Provider, Model: a.Model, AttemptNumber: a.MonetaryAttemptNumber(), ReservedMicros: amount, PriceFingerprint: prices.Fingerprint, EstimateSource: "identity-test"})
	if err != nil || replayed {
		t.Fatalf("reserve replay=%t err=%v", replayed, err)
	}
}

func TestMonetaryReservationUsesOriginalPriceAfterRotation(t *testing.T) {
	st, run, lease := newMonetarySupervisor(t)
	ctx := context.Background()
	turn, err := st.BeginSupervisorTurn(ctx, lease, "price snapshot")
	if err != nil {
		t.Fatal(err)
	}
	a := llm.ModelAttempt{SupervisorAttemptID: turn.Checkpoint.AttemptID, Number: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
	reserveSupervisorMoney(t, st, run.ID, a, 1000)
	if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, a); err != nil {
		t.Fatal(err)
	}
	prices := monetaryTestSnapshot(t, time.Now().UTC())
	prices.ID = "rotated-more-expensive"
	prices.Entries[0].InputPerMillionMicros *= 100
	prices.Entries[0].OutputPerMillionMicros *= 100
	prices.Fingerprint = pricing.Fingerprint(prices)
	if _, _, err := st.ImportPriceSnapshot(ctx, prices); err != nil {
		t.Fatal(err)
	}
	entry, found, err := st.ModelReservationPrice(ctx, run.ID, domain.MonetaryScopeRoot, a.MonetaryAttemptNumber())
	if err != nil || !found || entry.EstimateCost(2, 3, 0, 0) != 8 {
		t.Fatalf("reserved price changed %#v %t %v", entry, found, err)
	}
	a.Outcome = llm.OutcomeSuccess
	if _, err := st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, a, llm.ChatResponse{Text: "known", Usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}); err != nil {
		t.Fatal(err)
	}
	usage, err := st.GetMonetaryUsage(ctx, run.ID)
	if err != nil || usage.SettledMicros != 8 || usage.ReleasedMicros != 992 {
		t.Fatalf("reconcile used current prices %#v %v", usage, err)
	}
}

func TestMonetaryLegacyAmbiguousTerminalOnlyRetainsExposure(t *testing.T) {
	st, run, _ := newMonetarySupervisor(t)
	a := llm.ModelAttempt{Number: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
	reserveSupervisorMoney(t, st, run.ID, a, 1000)
	for _, subject := range []string{"old-one/model/1", "old-two/model/1"} {
		insertMonetaryIdentityEvent(t, st, run, events.ModelCompletedEvent, subject, map[string]any{"model_attempt": 1, "provider": "mock", "model": "mock-code", "usage": llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}})
	}
	usage, err := st.GetMonetaryUsage(context.Background(), run.ID)
	if err != nil || usage.SettledMicros != 0 || usage.ReleasedMicros != 0 {
		t.Fatalf("ambiguous terminal guessed %#v %v", usage, err)
	}
	if count, err := st.ReleaseOpenMonetaryReservations(context.Background(), run.ID); err != nil || count != 0 {
		t.Fatalf("ambiguous sent reservation released %d %v", count, err)
	}
}

func newMonetarySupervisor(t *testing.T) (*SQLiteStore, domain.Run, domain.RunExecutionLease) {
	t.Helper()
	st, run := newMonetaryTestStore(t)
	ctx := context.Background()
	if _, _, err := st.ImportPriceSnapshot(ctx, monetaryTestSnapshot(t, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	return st, run, acquireTestRunExecutionLease(t, ctx, st, run.ID)
}

func TestMonetarySupervisorTwoTurnsHaveDistinctDurableReservations(t *testing.T) {
	st, run, lease := newMonetarySupervisor(t)
	ctx := context.Background()
	var keys []int64
	for i := 1; i <= 2; i++ {
		turn, err := st.BeginSupervisorTurn(ctx, lease, fmt.Sprintf("input %d", i))
		if err != nil {
			t.Fatal(err)
		}
		n, _, err := st.NextSupervisorModelAttempt(ctx, turn.Checkpoint, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("fixture did not reproduce repeated Number1: %d", n)
		}
		a := llm.ModelAttempt{SupervisorAttemptID: turn.Checkpoint.AttemptID, Number: n, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
		wrong := a
		wrong.SupervisorAttemptID = "another-execution-attempt"
		if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, wrong); apperror.CodeOf(err) != apperror.CodeConflict {
			t.Fatalf("foreign monetary identity was accepted: %v", err)
		}
		keys = append(keys, a.MonetaryAttemptNumber())
		reserveSupervisorMoney(t, st, run.ID, a, 1000)
		if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, a); err != nil {
			t.Fatal(err)
		}
		a.Outcome = llm.OutcomeSuccess
		response := llm.ChatResponse{Text: fmt.Sprintf("completed %d", i), Usage: llm.Usage{InputTokens: i * 2, OutputTokens: i * 3, TotalTokens: i * 5}}
		cp, err := st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, a, response)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, a, response); err != nil {
			t.Fatal(err)
		}
		// Losing a completion acknowledgement must not change the original
		// terminal or hide counters already persisted for that exact call.
		failed := a
		failed.Outcome = llm.OutcomePermanent
		failed.ErrorText = "completion acknowledgement unavailable"
		accounted, ackErr := st.RecordSupervisorModelFailedWithUsage(ctx, turn.Checkpoint, failed, response.Usage, 0)
		if apperror.CodeOf(ackErr) != apperror.CodeConflict || accounted.AttemptID != cp.AttemptID || accounted.ExecutionMillis != cp.ExecutionMillis || accounted.TotalTokens != cp.TotalTokens {
			t.Fatalf("exact committed usage was not acknowledged: %#v %v", accounted, ackErr)
		}
		wrongUsage := response.Usage
		wrongUsage.InputTokens++
		unmatched, ackErr := st.RecordSupervisorModelFailedWithUsage(ctx, turn.Checkpoint, failed, wrongUsage, 0)
		if apperror.CodeOf(ackErr) != apperror.CodeConflict || unmatched.RunID != "" {
			t.Fatal("different usage acquired the committed checkpoint")
		}
		if _, _, _, err := st.CompleteSupervisorTurn(ctx, cp, response, domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionContinue, Message: response.Text}, policy.Decision{Allowed: true}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if keys[0] == keys[1] {
		t.Fatal("two turns reused monetary identity")
	}
	usage, err := st.GetMonetaryUsage(ctx, run.ID)
	if err != nil || usage.ReservedMicros != 2000 || usage.SettledMicros != 24 || usage.ReleasedMicros != 1976 || usage.RemainingMicros != 1999976 {
		t.Fatalf("two-turn accounting %#v %v", usage, err)
	}
	var rows, starts, terminals int
	st.db.QueryRow(`SELECT COUNT(*) FROM run_monetary_reservations WHERE run_id=? AND status='settled'`, run.ID).Scan(&rows)
	st.db.QueryRow(`SELECT COUNT(*) FROM run_events WHERE run_id=? AND type='model.started' AND json_extract(payload_json,'$.supervisor_attempt_id') IS NOT NULL`, run.ID).Scan(&starts)
	st.db.QueryRow(`SELECT COUNT(*) FROM run_events WHERE run_id=? AND type='model.completed' AND json_extract(payload_json,'$.supervisor_attempt_id') IS NOT NULL`, run.ID).Scan(&terminals)
	if rows != 2 || starts != 2 || terminals != 2 {
		t.Fatalf("durable identity/replay %d/%d/%d", rows, starts, terminals)
	}
	var filename string
	var seq int
	var dbname string
	if err := st.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &dbname, &filename); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.GetMonetaryUsage(ctx, run.ID)
	if err != nil || after.ReservedMicros != usage.ReservedMicros || after.SettledMicros != usage.SettledMicros || after.ReleasedMicros != usage.ReleasedMicros {
		t.Fatalf("reopen %#v %v", after, err)
	}
}

func TestMonetaryLegacyReservationFollowsOriginalStartNotLaterNumber(t *testing.T) {
	st, run, lease := newMonetarySupervisor(t)
	ctx := context.Background()
	turn, err := st.BeginSupervisorTurn(ctx, lease, "legacy input")
	if err != nil {
		t.Fatal(err)
	}
	a := llm.ModelAttempt{Number: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
	reserveSupervisorMoney(t, st, run.ID, a, 1000)
	if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, a); err != nil {
		t.Fatal(err)
	}
	// A saved later turn reused Number1 in older releases. Its terminal is
	// deliberately different; it must never settle the first call's row.
	insertMonetaryIdentityEvent(t, st, run, events.ModelStartedEvent, "later-attempt/model/1", map[string]any{"model_attempt": 1, "max_attempts": 1, "provider": "mock", "model": "mock-code"})
	insertMonetaryIdentityEvent(t, st, run, events.ModelCompletedEvent, "later-attempt/model/1", map[string]any{"model_attempt": 1, "provider": "mock", "model": "mock-code", "usage": llm.Usage{InputTokens: 99, OutputTokens: 99, TotalTokens: 198}})
	// Open the same durable file with the new code before reconciling the old
	// identity. No row is migrated or rekeyed to obtain this result.
	var filename, dbname string
	var dbseq int
	if err := st.db.QueryRow(`PRAGMA database_list`).Scan(&dbseq, &dbname, &filename); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	usage, err := st.GetMonetaryUsage(ctx, run.ID)
	if err != nil || usage.SettledMicros != 0 || usage.ReleasedMicros != 0 {
		t.Fatalf("later turn stole legacy reservation %#v %v", usage, err)
	}
	if _, _, err := st.ReleaseModelCost(ctx, domain.MonetaryReleaseRequest{RunID: run.ID, Scope: domain.MonetaryScopeRoot, AttemptNumber: 1}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("sent legacy release accepted: %v", err)
	}
	if n, err := st.ReleaseOpenMonetaryReservations(ctx, run.ID); err != nil || n != 0 {
		t.Fatalf("unknown legacy exposure released %d %v", n, err)
	}
	a.Outcome = llm.OutcomeSuccess
	if _, err := st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, a, llm.ChatResponse{Text: "original", Usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}); err != nil {
		t.Fatal(err)
	}
	usage, err = st.GetMonetaryUsage(ctx, run.ID)
	if err != nil || usage.SettledMicros != 8 || usage.ReleasedMicros != 992 {
		t.Fatalf("original legacy receipt lost %#v %v", usage, err)
	}
	var key int64
	if err := st.db.QueryRow(`SELECT attempt_number FROM run_monetary_reservations WHERE run_id=?`, run.ID).Scan(&key); err != nil || key != 1 {
		t.Fatalf("legacy row was rekeyed %d %v", key, err)
	}
}

func TestMonetaryLateUsagePreservesLaterSupervisorTurn(t *testing.T) {
	st, run, lease := newMonetarySupervisor(t)
	ctx := context.Background()
	first, err := st.BeginSupervisorTurn(ctx, lease, "first input")
	if err != nil {
		t.Fatal(err)
	}
	a := llm.ModelAttempt{SupervisorAttemptID: first.Checkpoint.AttemptID, Number: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
	reserveSupervisorMoney(t, st, run.ID, a, 1000)
	if _, err := st.RecordSupervisorModelStarted(ctx, first.Checkpoint, a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FailSupervisorTurn(ctx, first.Checkpoint, "transport no longer owned by the turn", 0); err != nil {
		t.Fatal(err)
	}
	if paused, err := st.GetRun(ctx, run.ID); err != nil || paused.Status != domain.RunPaused {
		t.Fatalf("failed turn must pause before explicit continuation: run=%#v err=%v", paused, err)
	}
	if _, err := application.NewRunService(st).Resume(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	second, err := st.BeginSupervisorTurn(ctx, lease, "new input must remain current")
	if err != nil {
		t.Fatal(err)
	}
	if second.Checkpoint.AttemptID == first.Checkpoint.AttemptID {
		t.Fatal("fixture did not create a later turn")
	}
	a.Outcome = llm.OutcomeCancelled
	a.ErrorText = "late usage from old transport"
	wrong := a
	wrong.SupervisorAttemptID = second.Checkpoint.AttemptID
	if _, err := st.RecordSupervisorModelFailedWithUsage(ctx, first.Checkpoint, wrong, llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, 0); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("late receipt could change original identity: %v", err)
	}
	after, err := st.RecordSupervisorModelFailedWithUsage(ctx, first.Checkpoint, a, llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := second.Checkpoint
	if after.AttemptID != before.AttemptID || after.NextTurn != before.NextTurn || after.Phase != before.Phase || after.PendingInput != before.PendingInput || after.LeaseID != before.LeaseID || after.RepairPhase != before.RepairPhase || after.TotalTokens != before.TotalTokens+5 {
		t.Fatalf("late receipt overwrote later turn before=%#v after=%#v", before, after)
	}
	money, err := st.GetMonetaryUsage(ctx, run.ID)
	if err != nil || money.SettledMicros != 8 {
		t.Fatalf("late receipt money %#v %v", money, err)
	}
}

func insertMonetaryIdentityEvent(t *testing.T, st *SQLiteStore, run domain.Run, kind, subject string, payload map[string]any) {
	t.Helper()
	event, err := events.New(run.ID, run.MissionID, kind, "model_gateway", subject, payload)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = insertRunEventTx(context.Background(), tx, event); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestMonetarySupervisorLateUsageCannotReviveOrReplaceCurrentTurn(t *testing.T) {
	for _, kind := range []string{"known", "unknown", "total_only"} {
		t.Run(kind, func(t *testing.T) {
			st, run, lease := newMonetarySupervisor(t)
			ctx := context.Background()
			turn, err := st.BeginSupervisorTurn(ctx, lease, "original input")
			if err != nil {
				t.Fatal(err)
			}
			a := llm.ModelAttempt{SupervisorAttemptID: turn.Checkpoint.AttemptID, Number: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
			reserveSupervisorMoney(t, st, run.ID, a, 1000)
			if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, a); err != nil {
				t.Fatal(err)
			}
			if _, err := application.NewRunService(st).Cancel(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			before, _, err := st.GetSupervisorCheckpoint(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			money, err := st.GetMonetaryUsage(ctx, run.ID)
			if err != nil || money.ReleasedMicros != 0 || money.SettledMicros != 0 {
				t.Fatalf("cancel released unknown %#v %v", money, err)
			}
			a.Outcome = llm.OutcomeCancelled
			a.ErrorText = "stopped after request started"
			u := llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}
			if kind == "total_only" {
				u = llm.Usage{TotalTokens: 124}
			}
			var after domain.SupervisorCheckpoint
			if kind == "unknown" {
				after, err = st.RecordSupervisorModelFailed(ctx, turn.Checkpoint, a)
			} else {
				after, err = st.RecordSupervisorModelFailedWithUsage(ctx, turn.Checkpoint, a, u, 17)
			}
			if err != nil {
				t.Fatal(err)
			}
			if after.PendingInput != before.PendingInput || after.Phase != before.Phase || after.AttemptID != before.AttemptID || after.LeaseID != before.LeaseID {
				t.Fatal("late receipt changed execution state")
			}
			if kind != "unknown" && after.TotalTokens != int64(u.TotalTokens) {
				t.Fatal("known usage lost")
			}
			if kind != "unknown" {
				if _, err := st.RecordSupervisorModelFailedWithUsage(ctx, turn.Checkpoint, a, u, 17); err != nil {
					t.Fatal(err)
				}
				changedError := a
				changedError.ErrorText = "failure publication acknowledgement unavailable"
				accounted, ackErr := st.RecordSupervisorModelFailedWithUsage(ctx, turn.Checkpoint, changedError, u, 17)
				if apperror.CodeOf(ackErr) != apperror.CodeConflict || accounted.AttemptID != after.AttemptID || accounted.ExecutionMillis != after.ExecutionMillis || accounted.TotalTokens != after.TotalTokens {
					t.Fatalf("same-type committed usage acknowledgement %#v %v", accounted, ackErr)
				}
			}
			money, err = st.GetMonetaryUsage(ctx, run.ID)
			want := int64(1000)
			if kind == "known" {
				want = 8
			}
			if err != nil || money.SettledMicros != want || money.ReleasedMicros != 1000-want {
				t.Fatalf("late settlement %#v %v", money, err)
			}
			current, err := st.GetRun(ctx, run.ID)
			if err != nil || current.Status != domain.RunCancelled {
				t.Fatal("run revived")
			}
			var raw string
			st.db.QueryRow(`SELECT payload_json FROM run_events WHERE run_id=? AND type='model.failed' AND source='model_gateway'`, run.ID).Scan(&raw)
			var receipt map[string]any
			if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt["usage_unknown"] != (kind != "known") {
				t.Fatalf("unknown receipt incorrect %s", raw)
			}
		})
	}
}
