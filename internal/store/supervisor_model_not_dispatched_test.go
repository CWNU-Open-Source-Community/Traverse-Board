package store

import (
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func TestNotDispatchedReceiptReconcilesWithoutRelease(t *testing.T) {
	for _, purpose := range []string{"", llm.ModelPurposeContextCompaction} {
		t.Run("purpose_"+purpose, func(t *testing.T) {
			st, run, lease := newMonetarySupervisor(t)
			ctx := t.Context()
			turn, err := st.BeginSupervisorTurn(ctx, lease, "not-sent output policy")
			if err != nil {
				t.Fatal(err)
			}
			a := llm.ModelAttempt{SupervisorAttemptID: turn.Checkpoint.AttemptID, Number: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
			if purpose != "" {
				a.SupervisorAttemptID = ""
				a.Purpose = purpose
				tx, txErr := st.db.BeginTx(ctx, nil)
				if txErr != nil {
					t.Fatal(txErr)
				}
				snapshot, snapshotErr := readSupervisorCompactionSnapshot(ctx, tx, turn.Checkpoint)
				_ = tx.Rollback()
				if snapshotErr != nil {
					t.Fatal(snapshotErr)
				}
				a.CompactionSourceSHA256 = supervisorCompactionSourceHash(snapshot)
				a.Number, err = st.NextSupervisorCompactionAttempt(ctx, turn.Checkpoint)
				if err != nil {
					t.Fatal(err)
				}
			}
			reserveSupervisorMoney(t, st, run.ID, a, 1000)
			if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, a); err != nil {
				t.Fatal(err)
			}
			a.Outcome = llm.OutcomePermanent
			a.ErrorText = "configuration changed locally"
			if _, err := st.RecordSupervisorModelNotDispatched(ctx, turn.Checkpoint, a); err != nil {
				t.Fatal(err)
			}
			if _, err := st.RecordSupervisorModelNotDispatched(ctx, turn.Checkpoint, a); err != nil {
				t.Fatalf("exact replay: %v", err)
			}
			var status string
			if err := st.db.QueryRowContext(ctx, "SELECT status FROM run_monetary_reservations WHERE run_id=?", run.ID).Scan(&status); err != nil || status != "reserved" {
				t.Fatalf("fixture must stop before release: %s %v", status, err)
			}
			var seq int64
			if err := st.db.QueryRowContext(ctx, "SELECT MAX(sequence) FROM run_events WHERE run_id=?", run.ID).Scan(&seq); err != nil {
				t.Fatal(err)
			}
			// Use the actual database file and a new connection to exercise the
			// crash window between durable failure and application-side release.
			var dbSeq int
			var dbName, path string
			if err := st.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&dbSeq, &dbName, &path); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			u, err := reopened.GetMonetaryUsage(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if u.SettledMicros != 0 || u.ReleasedMicros != 1000 || u.ReservedMicros != 1000 {
				t.Fatalf("reopen charged local failure: %+v", u)
			}
			if _, replay, err := reopened.ReleaseModelCost(ctx, domain.MonetaryReleaseRequest{RunID: run.ID, Scope: domain.MonetaryScopeRoot, AttemptNumber: a.MonetaryAttemptNumber()}); err != nil || !replay {
				t.Fatalf("release not idempotent: %v %v", replay, err)
			}
			var after int64
			if err := reopened.db.QueryRowContext(ctx, "SELECT MAX(sequence) FROM run_events WHERE run_id=?", run.ID).Scan(&after); err != nil || after != seq {
				t.Fatalf("restart replayed work/events: %d -> %d %v", seq, after, err)
			}
		})
	}
}

func TestNotDispatchedReceiptCannotReplaceReceivedUsage(t *testing.T) {
	st, run, lease := newMonetarySupervisor(t)
	ctx := t.Context()
	turn, err := st.BeginSupervisorTurn(ctx, lease, "remote zero remains unknown")
	if err != nil {
		t.Fatal(err)
	}
	a := llm.ModelAttempt{SupervisorAttemptID: turn.Checkpoint.AttemptID, Number: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
	reserveSupervisorMoney(t, st, run.ID, a, 1000)
	if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, a); err != nil {
		t.Fatal(err)
	}
	a.Outcome = llm.OutcomePermanent
	a.ErrorText = "remote failure"
	if _, err := st.RecordSupervisorModelFailedWithUsage(ctx, turn.Checkpoint, a, llm.Usage{}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordSupervisorModelNotDispatched(ctx, turn.Checkpoint, a); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("replaced sent receipt: %v", err)
	}
	u, err := st.GetMonetaryUsage(ctx, run.ID)
	if err != nil || u.SettledMicros != 1000 || u.ReleasedMicros != 0 {
		t.Fatalf("remote unknown became free: %+v %v", u, err)
	}
}
