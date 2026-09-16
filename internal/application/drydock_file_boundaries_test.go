package application

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestDrydockFileBoundaryRestartReconcilesOwnedInterruptedMutation(t *testing.T) {
	fixture, owned := newFileEditDrydockFixture(t)
	sourceCheckpoints, sourceCursor := prepareDrydockBoundarySourceCursor(t, fixture)
	fixture.service.WithCheckpointService(sourceCheckpoints)
	request := WorkspaceMutationBoundaryRequest{RunID: fixture.run.ID,
		Kind: workspacecheckpoint.TransactionFileTool, OperationKey: "interrupted-owned-file-boundary",
		TriggerReceiptID: "edit-owned-interrupted"}
	prepared, err := fixture.service.FileEditCheckpointService().BeginBoundary(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	writeDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt"), "written before process interruption\n")
	// No CompleteBoundary call: only the prepared journal and changed real file
	// survive into a new SQLite connection and a reconstructed application service.
	reopened, resumed := reopenDrydockBoundaryService(t, fixture)
	count, err := resumed.ReconcileWorkspaceCheckpoints(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("reconcile count=%d err=%v", count, err)
	}
	transaction, found, err := reopened.GetWorkspaceCheckpointTransaction(t.Context(), prepared.Transaction.ID)
	if err != nil || !found || transaction.Status != workspacecheckpoint.TransactionInterrupted ||
		transaction.ErrorCode != "process_restart_reconciliation" || transaction.WorkspaceID != owned.WorkspaceID {
		t.Fatalf("interrupted journal=%+v found=%t err=%v", transaction, found, err)
	}
	after, err := reopened.GetWorkspaceCheckpoint(t.Context(), transaction.AfterCheckpointID)
	if err != nil || after.WorkspaceID != owned.WorkspaceID || after.RunID != fixture.run.ID ||
		after.Trigger != workspacecheckpoint.TriggerRewindResult || after.Phase != workspacecheckpoint.PhaseAfter ||
		after.TriggerReceiptID != transaction.ID || after.ParentCheckpointID != prepared.Before.ID ||
		len(after.IncompleteReasons) == 0 || after.RecoveryLevel == workspacecheckpoint.RecoveryComplete {
		t.Fatalf("interrupted after checkpoint=%+v err=%v", after, err)
	}
	if !strings.Contains(strings.Join(after.IncompleteReasons, " "), "process restart interrupted") {
		t.Fatalf("interruption disappeared from checkpoint evidence: %v", after.IncompleteReasons)
	}
	recovered, found, err := reopened.GetDrydockByRun(t.Context(), fixture.run.ID)
	if err != nil || !found || recovered.LastCheckpointID != after.ID {
		t.Fatalf("owned cursor=%+v found=%t err=%v", recovered, found, err)
	}
	assertDrydockRecoveryPreservesSource(t, fixture, reopened, sourceCursor)
	if got := readDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt")); got != "written before process interruption\n" {
		t.Fatalf("reconciliation rewrote the interrupted file: %q", got)
	}
	assertDrydockSecondReconciliationIsEmpty(t, fixture, reopened, resumed, recovered)
}

func TestDrydockFileBoundaryRestartRecoversTerminalPendingCursor(t *testing.T) {
	fixture, owned := newFileEditDrydockFixture(t)
	sourceCheckpoints, sourceCursor := prepareDrydockBoundarySourceCursor(t, fixture)
	failing := &drydockBoundaryAdvanceFailure{SQLiteStore: fixture.state}
	service, err := NewDrydockService(failing, fixture.executor)
	if err != nil {
		t.Fatal(err)
	}
	service.WithCheckpointService(sourceCheckpoints)
	boundaries := service.FileEditCheckpointService()
	request := WorkspaceMutationBoundaryRequest{RunID: fixture.run.ID,
		Kind: workspacecheckpoint.TransactionFileTool, OperationKey: "owned-file-terminal-pending-cursor",
		TriggerReceiptID: "edit-owned-pending-cursor"}
	prepared, err := boundaries.BeginBoundary(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	writeDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt"), "completed file before cursor failure\n")
	failing.failNext = true
	if _, err := boundaries.CompleteBoundary(t.Context(), request, nil); err == nil || failing.failNext {
		t.Fatalf("expected injected Drydock persistence failure, reached=%t err=%v", !failing.failNext, err)
	}
	transaction, found, err := fixture.state.GetWorkspaceCheckpointTransaction(t.Context(), prepared.Transaction.ID)
	if err != nil || !found || transaction.Status != workspacecheckpoint.TransactionCompleted ||
		transaction.AfterCheckpointID == "" {
		t.Fatalf("terminal journal missing before cursor recovery: %+v found=%t err=%v", transaction, found, err)
	}
	beforeRecovery, _, err := fixture.state.GetDrydockByRun(t.Context(), fixture.run.ID)
	if err != nil || beforeRecovery.LastCheckpointID != prepared.Before.ID {
		t.Fatalf("failed cursor write was incorrectly committed: %+v err=%v", beforeRecovery, err)
	}
	pending, err := fixture.state.ListWorkspaceCheckpointTransactionsPendingCursor(t.Context(), 20)
	if err != nil || len(pending) != 1 || pending[0].ID != transaction.ID {
		t.Fatalf("owned pending cursor projection=%+v err=%v", pending, err)
	}
	reopened, resumed := reopenDrydockBoundaryService(t, fixture)
	count, err := resumed.ReconcileWorkspaceCheckpoints(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("pending cursor reconcile count=%d err=%v", count, err)
	}
	recovered, found, err := reopened.GetDrydockByRun(t.Context(), fixture.run.ID)
	if err != nil || !found || recovered.LastCheckpointID != transaction.AfterCheckpointID ||
		recovered.Generation != beforeRecovery.Generation+1 {
		t.Fatalf("recovered cursor=%+v found=%t err=%v", recovered, found, err)
	}
	stored, _, err := reopened.GetWorkspaceCheckpointTransaction(t.Context(), transaction.ID)
	if err != nil || !reflect.DeepEqual(stored, transaction) {
		t.Fatalf("cursor recovery rewrote terminal journal: %+v err=%v", stored, err)
	}
	after, err := reopened.GetWorkspaceCheckpoint(t.Context(), transaction.AfterCheckpointID)
	if err != nil || after.Trigger != workspacecheckpoint.TriggerFileTool ||
		after.TriggerReceiptID != request.TriggerReceiptID || after.WorkspaceID != owned.WorkspaceID {
		t.Fatalf("completed file after checkpoint=%+v err=%v", after, err)
	}
	assertDrydockRecoveryPreservesSource(t, fixture, reopened, sourceCursor)
	if got := readDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt")); got != "completed file before cursor failure\n" {
		t.Fatalf("cursor recovery rewrote file: %q", got)
	}
	assertDrydockSecondReconciliationIsEmpty(t, fixture, reopened, resumed, recovered)
}

func TestDrydockFileBoundaryRestartRejectsExternalChangeAfterTerminalCursorFailure(t *testing.T) {
	fixture, owned := newFileEditDrydockFixture(t)
	sourceCheckpoints, sourceCursor := prepareDrydockBoundarySourceCursor(t, fixture)
	failing := &drydockBoundaryAdvanceFailure{SQLiteStore: fixture.state}
	service, err := NewDrydockService(failing, fixture.executor)
	if err != nil {
		t.Fatal(err)
	}
	boundaries := service.WithCheckpointService(sourceCheckpoints).FileEditCheckpointService()
	request := WorkspaceMutationBoundaryRequest{RunID: fixture.run.ID,
		Kind: workspacecheckpoint.TransactionFileTool, OperationKey: "owned-terminal-cursor-external-change",
		TriggerReceiptID: "edit-owned-external-change"}
	prepared, err := boundaries.BeginBoundary(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(owned.Path, "tracked.txt")
	writeDrydockTestFile(t, filePath, "reviewed and captured version\n")
	failing.failNext = true
	if _, err := boundaries.CompleteBoundary(t.Context(), request, nil); err == nil || failing.failNext {
		t.Fatalf("expected injected Drydock persistence failure, reached=%t err=%v", !failing.failNext, err)
	}
	transaction, found, err := fixture.state.GetWorkspaceCheckpointTransaction(t.Context(), prepared.Transaction.ID)
	if err != nil || !found || transaction.Status != workspacecheckpoint.TransactionCompleted ||
		transaction.AfterCheckpointID == "" {
		t.Fatalf("terminal journal=%+v found=%t err=%v", transaction, found, err)
	}
	sealed, err := fixture.state.GetWorkspaceCheckpointSnapshot(t.Context(), transaction.AfterCheckpointID)
	if err != nil {
		t.Fatal(err)
	}
	beforeRecovery, found, err := fixture.state.GetDrydockByRun(t.Context(), fixture.run.ID)
	if err != nil || !found || beforeRecovery.LastCheckpointID != prepared.Before.ID {
		t.Fatalf("pending cursor=%+v found=%t err=%v", beforeRecovery, found, err)
	}
	// This change occurs after the terminal snapshot was durably sealed and
	// before a different process attempts to finish its pending cursor write.
	writeDrydockTestFile(t, filePath, "later external work must remain unattributed\n")
	reopened, resumed := reopenDrydockBoundaryService(t, fixture)
	count, err := resumed.ReconcileWorkspaceCheckpoints(t.Context())
	if count != 0 || apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatalf("external change was attributed to the sealed checkpoint: count=%d err=%v", count, err)
	}
	actual, found, err := reopened.GetDrydockByRun(t.Context(), fixture.run.ID)
	if err != nil || !found || !reflect.DeepEqual(actual, beforeRecovery) {
		t.Fatalf("failed recovery changed owned metadata: %+v found=%t err=%v", actual, found, err)
	}
	stored, found, err := reopened.GetWorkspaceCheckpointTransaction(t.Context(), transaction.ID)
	if err != nil || !found || !reflect.DeepEqual(stored, transaction) {
		t.Fatalf("failed recovery rewrote the sealed journal: %+v found=%t err=%v", stored, found, err)
	}
	currentSnapshot, err := reopened.GetWorkspaceCheckpointSnapshot(t.Context(), transaction.AfterCheckpointID)
	if err != nil || !reflect.DeepEqual(currentSnapshot, sealed) {
		t.Fatalf("failed recovery rewrote the sealed snapshot: %+v err=%v", currentSnapshot.Checkpoint, err)
	}
	pending, err := reopened.ListWorkspaceCheckpointTransactionsPendingCursor(t.Context(), 20)
	if err != nil || len(pending) != 1 || pending[0].ID != transaction.ID {
		t.Fatalf("failed recovery lost the pending cursor: %+v err=%v", pending, err)
	}
	assertDrydockRecoveryPreservesSource(t, fixture, reopened, sourceCursor)
	if got := readDrydockTestFile(t, filePath); got != "later external work must remain unattributed\n" {
		t.Fatalf("failed recovery rewrote external work: %q", got)
	}
}

type drydockBoundaryAdvanceFailure struct {
	*store.SQLiteStore
	failNext bool
}

func (s *drydockBoundaryAdvanceFailure) AdvanceDrydock(ctx context.Context, workspace drydock.Workspace,
	expectedGeneration int64, receipt drydock.Receipt,
) (drydock.Workspace, bool, error) {
	if s.failNext {
		s.failNext = false
		return drydock.Workspace{}, false, errors.New("injected Drydock cursor persistence failure")
	}
	return s.SQLiteStore.AdvanceDrydock(ctx, workspace, expectedGeneration, receipt)
}

func prepareDrydockBoundarySourceCursor(t *testing.T, fixture drydockApplicationFixture) (*WorkspaceCheckpointService, workspacecheckpoint.RunState) {
	t.Helper()
	checkpoints, err := NewWorkspaceCheckpointService(fixture.state,
		standardCodeThreadTestRuntime().ExecutionPermissionCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := checkpoints.Capture(t.Context(), WorkspaceCheckpointCaptureRequest{RunID: fixture.run.ID,
		OperationKey: "source-history-before-owned-recovery", RequestedBy: "cli_operator", Title: "Source history"}); err != nil {
		t.Fatal(err)
	}
	cursor, found, err := fixture.state.GetWorkspaceCheckpointRunState(t.Context(), fixture.run.ID)
	if err != nil || !found || cursor.WorkspaceID != fixture.workspace.ID {
		t.Fatalf("source cursor=%+v found=%t err=%v", cursor, found, err)
	}
	return checkpoints, cursor
}

func reopenDrydockBoundaryService(t *testing.T, fixture drydockApplicationFixture) (*store.SQLiteStore, *DrydockService) {
	t.Helper()
	state, err := store.Open(fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	service, err := NewDrydockService(state, fixture.executor)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := NewWorkspaceCheckpointService(state,
		standardCodeThreadTestRuntime().ExecutionPermissionCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	return state, service.WithCheckpointService(checkpoints)
}

func assertDrydockRecoveryPreservesSource(t *testing.T, fixture drydockApplicationFixture,
	state *store.SQLiteStore, expected workspacecheckpoint.RunState,
) {
	t.Helper()
	actual, found, err := state.GetWorkspaceCheckpointRunState(t.Context(), fixture.run.ID)
	if err != nil || !found || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Drydock recovery moved source cursor: %+v found=%t err=%v", actual, found, err)
	}
	if got := readDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt")); got != "user source\r\n" {
		t.Fatalf("Drydock recovery wrote source file: %q", got)
	}
}

func assertDrydockSecondReconciliationIsEmpty(t *testing.T, fixture drydockApplicationFixture,
	state *store.SQLiteStore, service *DrydockService, expected drydock.Workspace,
) {
	t.Helper()
	count, err := service.ReconcileWorkspaceCheckpoints(t.Context())
	if err != nil || count != 0 {
		t.Fatalf("repeated recovery count=%d err=%v", count, err)
	}
	current, _, err := state.GetDrydockByRun(t.Context(), fixture.run.ID)
	if err != nil || !reflect.DeepEqual(current, expected) {
		t.Fatalf("repeated recovery changed owned metadata: %+v err=%v", current, err)
	}
}
