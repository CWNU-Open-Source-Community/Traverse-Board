package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestWorkspaceRestorePreparationRechecksExecutionAcrossStores(t *testing.T) {
	for _, change := range []string{"resume", "lease", "inactive_session"} {
		t.Run(change, func(t *testing.T) {
			primary, secondary, intent := newWorkspaceRestoreInterlockFixture(t)
			ctx := t.Context()
			// The application captured a paused binding before another connection
			// changed execution authority. Preparation must use the durable state.
			switch change {
			case "resume":
				if err := resumeCheckpointRun(ctx, secondary, intent.RunID); err != nil {
					t.Fatal(err)
				}
			case "lease":
				acquireTestRunExecutionLease(t, ctx, secondary, intent.RunID)
			case "inactive_session":
				thread, err := secondary.GetThreadByRun(ctx, intent.RunID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := secondary.TransitionThread(ctx, thread.ID, domain.ThreadArchive,
					thread.Version, "checkpoint_test_operator", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := primary.CreateWorkspaceCheckpointTransaction(ctx, intent); err == nil {
				t.Fatal("stale paused binding prepared a restore after execution authority changed")
			}
			if _, found, err := primary.GetWorkspaceCheckpointTransactionByOperation(ctx,
				intent.OperationKeyDigest); err != nil || found {
				t.Fatalf("rejected restore was persisted: found=%t err=%v", found, err)
			}
		})
	}
}

func TestWorkspaceRestoreReservesRunUntilTerminalAndReplaysAcrossStores(t *testing.T) {
	for _, kind := range []workspacecheckpoint.TransactionKind{
		workspacecheckpoint.TransactionRewind, workspacecheckpoint.TransactionUndo,
		workspacecheckpoint.TransactionRedo, workspacecheckpoint.TransactionFork,
	} {
		t.Run(string(kind), func(t *testing.T) {
			primary, secondary, intent := newWorkspaceRestoreInterlockFixture(t)
			ctx := t.Context()
			intent.Kind = kind
			if kind == workspacecheckpoint.TransactionFork {
				intent.ForkWorkspaceRoot, intent.ForkBranch = t.TempDir(), "checkpoint-fork-interlock"
			}
			stored, _, err := primary.CreateWorkspaceCheckpointTransaction(ctx, intent)
			if err != nil {
				t.Fatal(err)
			}
			for _, status := range []workspacecheckpoint.TransactionStatus{
				workspacecheckpoint.TransactionPrepared, workspacecheckpoint.TransactionApplying,
			} {
				if status != stored.Status {
					stored.Status, stored.UpdatedAt = status, time.Now().UTC()
					stored, _, err = primary.UpdateWorkspaceCheckpointTransaction(ctx, stored)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := resumeCheckpointRun(ctx, secondary, intent.RunID); apperror.CodeOf(err) != apperror.CodeConflict {
					t.Fatalf("%s allowed HTTP resume: %v", status, err)
				}
				if _, err := application.NewRunService(secondary).Resume(ctx, intent.RunID); apperror.CodeOf(err) != apperror.CodeConflict {
					t.Fatalf("%s allowed legacy resume: %v", status, err)
				}
				if _, err := secondary.AcquireRunExecutionLease(ctx, checkpointLeaseRequest(intent.RunID)); apperror.CodeOf(err) != apperror.CodeConflict {
					t.Fatalf("%s allowed an execution lease: %v", status, err)
				}
				replay, replayed, err := secondary.CreateWorkspaceCheckpointTransaction(ctx, intent)
				if err != nil || !replayed || replay.ID != stored.ID || replay.Status != status {
					t.Fatalf("active restore replay=%+v replayed=%t err=%v", replay, replayed, err)
				}
			}
			stored.Status = workspacecheckpoint.TransactionCompleted
			stored.AfterCheckpointID = stored.BeforeCheckpointID
			if kind == workspacecheckpoint.TransactionUndo {
				stored.Status = workspacecheckpoint.TransactionFailed
				stored.ErrorCode = "external_change"
			}
			stored.UpdatedAt = time.Now().UTC()
			stored.CompletedAt = &stored.UpdatedAt
			stored, _, err = primary.UpdateWorkspaceCheckpointTransaction(ctx, stored)
			if err != nil {
				t.Fatal(err)
			}
			if err := resumeCheckpointRun(ctx, secondary, intent.RunID); err != nil {
				t.Fatalf("terminal restore prevented resume: %v", err)
			}
			acquireTestRunExecutionLease(t, ctx, secondary, intent.RunID)
			// Retrying after completion must not re-run the fresh paused/no-lease
			// checks or reopen the transaction while the Run is executing again.
			replay, replayed, err := primary.CreateWorkspaceCheckpointTransaction(ctx, intent)
			if err != nil || !replayed || replay.ID != stored.ID || replay.Status != stored.Status {
				t.Fatalf("terminal restore replay=%+v replayed=%t err=%v", replay, replayed, err)
			}
		})
	}
}

func TestWorkspaceRestoreConcurrentExecutionInterlockAcrossStores(t *testing.T) {
	for _, contender := range []string{"http_resume", "legacy_resume", "lease"} {
		t.Run(contender, func(t *testing.T) {
			primary, secondary, intent := newWorkspaceRestoreInterlockFixture(t)
			ctx := t.Context()
			start := make(chan struct{})
			var group sync.WaitGroup
			var restoreErr, executionErr error
			group.Add(2)
			go func() {
				defer group.Done()
				<-start
				_, _, restoreErr = primary.CreateWorkspaceCheckpointTransaction(ctx, intent)
			}()
			go func() {
				defer group.Done()
				<-start
				switch contender {
				case "http_resume":
					executionErr = resumeCheckpointRun(ctx, secondary, intent.RunID)
				case "legacy_resume":
					_, executionErr = application.NewRunService(secondary).Resume(ctx, intent.RunID)
				case "lease":
					_, executionErr = secondary.AcquireRunExecutionLease(ctx, checkpointLeaseRequest(intent.RunID))
				}
			}()
			close(start)
			group.Wait()
			if (restoreErr == nil) == (executionErr == nil) {
				t.Fatalf("exactly one operation must succeed: restore=%v execution=%v", restoreErr, executionErr)
			}
			_, restored, err := primary.GetWorkspaceCheckpointTransactionByOperation(ctx, intent.OperationKeyDigest)
			if err != nil || restored != (restoreErr == nil) {
				t.Fatalf("restore persistence diverged: restored=%t err=%v", restored, err)
			}
			run, err := primary.GetRun(ctx, intent.RunID)
			if err != nil {
				t.Fatal(err)
			}
			lease, found, err := primary.GetRunExecutionLease(ctx, intent.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if restored && (run.Status != domain.RunPaused || (found && lease.ActiveAt(time.Now().UTC()))) {
				t.Fatalf("restore overlaps execution: run=%s lease=%+v", run.Status, lease)
			}
		})
	}
}

func newWorkspaceRestoreInterlockFixture(t *testing.T) (*SQLiteStore, *SQLiteStore, workspacecheckpoint.Transaction) {
	t.Helper()
	primary, run, mission, root := newWorkspaceCheckpointStoreFixture(t)
	t.Cleanup(func() { _ = primary.Close() })
	ctx := t.Context()
	service := application.NewRunService(primary)
	if _, err := service.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Pause(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	var databasePath string
	if err := primary.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&databasePath); err != nil {
		t.Fatal(err)
	}
	secondary, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondary.Close() })
	now := time.Now().UTC()
	snapshot := captureStoreCheckpoint(t, run, mission, root, "restore-interlock-before",
		"restore-interlock-receipt", workspacecheckpoint.PhaseBefore, now)
	if _, _, err := primary.CreateWorkspaceCheckpoint(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	intent := workspacecheckpoint.Transaction{
		ID: "restore-interlock-transaction", ProtocolVersion: workspacecheckpoint.ProtocolVersion,
		OperationKeyDigest: storeCheckpointDigest("restore-interlock-operation"),
		RequestFingerprint: storeCheckpointDigest("restore-interlock-request"),
		RunID:              run.ID, WorkspaceID: mission.WorkspaceID, Kind: workspacecheckpoint.TransactionRewind,
		TriggerReceiptID: "restore-interlock-receipt", BeforeCheckpointID: snapshot.Checkpoint.ID,
		ExpectedCurrentCheckpointID: snapshot.Checkpoint.ID, TargetCheckpointID: snapshot.Checkpoint.ID,
		Status: workspacecheckpoint.TransactionPrepared, RecoveryLevel: workspacecheckpoint.RecoveryComplete,
		ConflictJSON: "[]", CreatedAt: now, UpdatedAt: now,
	}
	return primary, secondary, intent
}

func resumeCheckpointRun(ctx context.Context, state *SQLiteStore, runID string) error {
	_, err := application.NewRunLifecycleControlService(state).Apply(ctx, application.ControlRunLifecycleRequest{
		Version: domain.RunLifecycleControlProtocolVersion, RunID: runID,
		Action: domain.RunLifecycleResume, OperationKey: "checkpoint-resume-operation-0001",
		RequestedBy: "checkpoint_test_operator",
	})
	return err
}

func checkpointLeaseRequest(runID string) domain.AcquireRunExecutionLeaseRequest {
	return domain.AcquireRunExecutionLeaseRequest{RunID: runID, OwnerID: "checkpoint-test-executor", TTL: time.Minute}
}
