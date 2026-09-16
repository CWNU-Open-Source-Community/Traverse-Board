package application

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func authorizeDrydockRestoreForTest(t *testing.T, fixture drydockApplicationFixture) {
	t.Helper()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true}
	checkpoints, err := NewWorkspaceCheckpointService(fixture.state, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.WithCheckpointService(checkpoints)
	_, err = NewRunExecutionPermissionService(fixture.state, capabilities).Change(t.Context(),
		ChangeRunExecutionPermissionRequest{RunID: fixture.run.ID, Mode: string(domain.RunExecutionPermissionApproval),
			OperationKey: "authorize-reviewed-restore", RequestedBy: "operator", Reason: "Review explicit restoration", ConfirmUserApproval: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixture.state.GetRun(t.Context(), fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status == domain.RunCreated {
		if _, err := NewRunService(fixture.state).Start(t.Context(), run.ID); err != nil {
			t.Fatal(err)
		}
	}
	if run.Status != domain.RunPaused {
		if _, err := NewRunService(fixture.state).Pause(t.Context(), run.ID); err != nil {
			t.Fatal(err)
		}
	}
}

type interruptDrydockRestoreStore struct {
	*store.SQLiteStore
	once bool
}

func (s *interruptDrydockRestoreStore) AdvanceDrydock(ctx context.Context, workspace drydock.Workspace,
	generation int64, receipt drydock.Receipt,
) (drydock.Workspace, bool, error) {
	if s.once && receipt.Operation == drydock.OperationRewind {
		s.once = false
		return drydock.Workspace{}, false, errors.New("injected interruption after real restore before receipt")
	}
	return s.SQLiteStore.AdvanceDrydock(ctx, workspace, generation, receipt)
}

func TestThreadDrydockRestoresHistoricalCheckpointWithCurrentIdentityAndRecoversJournal(t *testing.T) {
	f := newDrydockApplicationFixture(t, "continued restoration")
	ctx := t.Context()
	physical := mustCreateDrydock(t, f)
	baseline := physical.LastCheckpointID
	writeDrydockTestFile(t, filepath.Join(physical.Path, "tracked.txt"), "previous task edit\n")
	attributed, err := f.service.Checkpoint(ctx, DrydockCheckpointRequest{RunID: f.run.ID,
		ExpectedGeneration: physical.Generation, OperationKey: "before-restore-continuation", RequestedBy: "operator", ConfirmObservedChanges: true})
	if err != nil {
		t.Fatal(err)
	}
	physical = attributed.Workspace
	if _, err := NewRunService(f.state).Start(ctx, f.run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunService(f.state).Fail(ctx, f.run.ID, "execution epoch ended"); err != nil {
		t.Fatal(err)
	}
	thread, err := f.state.GetThreadByRun(ctx, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := NewThreadServiceWithExecutionCapabilities(f.state, domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true}).WithDrydock(f.service).Submit(ctx,
		SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: thread.ID,
			OperationKey: "next-restore-context", Content: "Review the previous change", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := f.service.Reconcile(ctx); err != nil || result.RecoveryRequired != 0 || result.Unchanged != 1 {
		t.Fatalf("old physical owner reconciled a transferred directory: %+v %v", result, err)
	}
	request := DrydockRewindRequest{RunID: f.run.ID, TargetCheckpointID: baseline,
		ExpectedGeneration: physical.Generation, OperationKey: "restore-original-state", RequestedBy: "desktop_operator"}
	if _, err := f.service.Rewind(ctx, request); err == nil {
		t.Fatal("old epoch obtained a new restoration")
	}
	request.RunID = continued.Run.ID
	preview, err := f.service.Rewind(ctx, request)
	if err != nil || len(preview.Preview.Changes) != 1 || len(preview.Preview.Conflicts) != 0 {
		t.Fatalf("historical physical preview=%+v err=%v", preview, err)
	}
	currentFixture := f
	currentFixture.run = continued.Run
	authorizeDrydockRestoreForTest(t, currentFixture)
	interrupted := &interruptDrydockRestoreStore{SQLiteStore: f.state, once: true}
	service, err := NewDrydockService(interrupted, f.executor)
	if err != nil {
		t.Fatal(err)
	}
	service.WithCheckpointService(f.service.checkpoints)
	request.Confirm = true
	if _, err := service.Rewind(ctx, request); err == nil {
		t.Fatal("expected injected lost lifecycle write")
	} else {
		t.Logf("interruption: %v", err)
	}
	if got := readDrydockTestFile(t, filepath.Join(physical.Path, "tracked.txt")); got != "base\n" {
		t.Fatalf("actual restored content=%q", got)
	}
	digest := drydockOperationDigest(drydock.OperationRewind, request.RunID, request.OperationKey)
	journal, found, err := f.state.GetWorkspaceCheckpointTransactionByOperation(ctx, digest)
	if err != nil || !found || journal.Status != workspacecheckpoint.TransactionPrepared {
		t.Fatalf("restore did not retain its journal: %+v %v", journal, err)
	}
	if _, err := f.state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: continued.Run.ID, OwnerID: "racing-restoration", TTL: time.Minute}); err == nil {
		t.Fatal("execution crossed a pending physical restore")
	}
	reopened, err := store.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted, err := NewDrydockService(reopened, f.executor)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := NewWorkspaceCheckpointService(reopened, domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	restarted.WithCheckpointService(checkpoints)
	if _, err := restarted.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ReconcileWorkspaceCheckpoints(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.Rewind(ctx, request)
	if err != nil || result.After == nil || result.After.RunID != continued.Run.ID || result.After.SessionID != continued.Run.SessionID ||
		result.After.ParentCheckpointID != journal.BeforeCheckpointID || result.Receipt == nil || result.Receipt.RunID != continued.Run.ID {
		t.Fatalf("same-key restore confirmation=%+v err=%v", result, err)
	}
	replay, err := restarted.Rewind(ctx, request)
	if err != nil || !replay.Replayed || replay.Receipt.ID != result.Receipt.ID {
		t.Fatalf("duplicate restore changed receipt: %+v %v", replay, err)
	}
	journal, _, err = reopened.GetWorkspaceCheckpointTransactionByOperation(ctx, digest)
	if err != nil || journal.Status != workspacecheckpoint.TransactionCompleted || journal.AfterCheckpointID != result.After.ID {
		t.Fatalf("confirmed restore journal=%+v %v", journal, err)
	}
	if _, found, err := reopened.GetWorkspaceCheckpointRunState(ctx, continued.Run.ID); err != nil || found {
		t.Fatalf("restore changed the ordinary source cursor: %t %v", found, err)
	}
	old, err := reopened.GetWorkspaceCheckpointSnapshot(ctx, baseline)
	if err != nil || old.Checkpoint.RunID != f.run.ID || old.Checkpoint.SessionID != f.run.SessionID {
		t.Fatal("historical checkpoint identity was rewritten")
	}
	if got := readDrydockTestFile(t, filepath.Join(f.sourceRoot, "tracked.txt")); got != "base\n" {
		t.Fatalf("restore changed original project: %q", got)
	}
	forked, err := restarted.Fork(ctx, DrydockForkRequest{RunID: continued.Run.ID, TargetCheckpointID: baseline,
		ExpectedCurrentCheckpointID: result.After.ID, ExpectedGeneration: result.Workspace.Generation,
		OperationKey: "fork-historical-physical-state", RequestedBy: "desktop_operator", Confirm: true,
		WorkspaceName: "reviewed historical fork", WorkspaceRoot: filepath.Join(t.TempDir(), "historical fork"), Branch: "history-review", Goal: "Review isolated historical state"})
	if err != nil || forked.Fork.Run.ID == continued.Run.ID || forked.Fork.Target.RunID != f.run.ID || forked.Receipt.RunID != continued.Run.ID {
		t.Fatalf("historical physical fork=%+v err=%v", forked, err)
	}
}
