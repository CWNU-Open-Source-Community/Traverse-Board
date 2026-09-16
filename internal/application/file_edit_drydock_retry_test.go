package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestFileEditDrydockApplyRetriesFailedBeforeCursorWithSameKey(t *testing.T) {
	f := newDrydockApplyCursorRetryFixture(t)
	f.failing.failBefore = 1
	if _, err := f.apply.Apply(t.Context(), f.request); err == nil || f.failing.beforeFailures != 1 {
		t.Fatalf("before cursor failure not reached: failures=%d err=%v", f.failing.beforeFailures, err)
	}
	f.assertNoCompletedApply(t)
	transaction := f.transaction(t)
	if transaction.Status != workspacecheckpoint.TransactionPrepared || transaction.AfterCheckpointID != "" {
		t.Fatalf("before cursor failure falsely sealed mutation journal: %+v", transaction)
	}
	if got := readDrydockTestFile(t, f.ownedFile()); got != "base\n" {
		t.Fatalf("file changed before the before cursor persisted: %q", got)
	}
	beforeRetry, _, err := f.fixture.state.GetDrydockByRun(t.Context(), f.fixture.run.ID)
	if err != nil || !reflect.DeepEqual(beforeRetry, f.owned) {
		t.Fatalf("failed before cursor changed owned metadata: %+v err=%v", beforeRetry, err)
	}
	result, err := f.apply.Apply(t.Context(), f.request)
	if err != nil || !result.Replayed || !result.FileWritten || result.Result.Status != fileedit.ApplyCompleted {
		t.Fatalf("same-key recovery=%+v err=%v", result, err)
	}
	completed := f.transaction(t)
	if completed.ID != transaction.ID || completed.Status != workspacecheckpoint.TransactionCompleted ||
		completed.AfterCheckpointID == "" || completed.AfterCheckpointID == completed.BeforeCheckpointID {
		t.Fatalf("same-key recovery lacks a real after checkpoint: %+v", completed)
	}
	owned, _, err := f.fixture.state.GetDrydockByRun(t.Context(), f.fixture.run.ID)
	if err != nil || owned.LastCheckpointID != completed.AfterCheckpointID || f.failing.completeCalls != 1 {
		t.Fatalf("Apply result preceded owned cursor: owned=%+v completions=%d err=%v", owned, f.failing.completeCalls, err)
	}
	assertDrydockRecoveryPreservesSource(t, f.fixture, f.fixture.state, f.sourceCursor)
}

func TestFileEditDrydockApplyCannotCompleteWhileAfterCursorKeepsFailing(t *testing.T) {
	f := newDrydockApplyCursorRetryFixture(t)
	f.failing.failAfter = true
	if _, err := f.apply.Apply(t.Context(), f.request); err == nil || f.failing.afterFailures == 0 {
		t.Fatalf("after cursor failure not reached: failures=%d err=%v", f.failing.afterFailures, err)
	}
	f.assertNoCompletedApply(t)
	transaction := f.transaction(t)
	if transaction.Status != workspacecheckpoint.TransactionCompleted || transaction.AfterCheckpointID == "" ||
		transaction.BeforeCheckpointID == transaction.AfterCheckpointID {
		t.Fatalf("real mutation was not sealed before cursor failure: %+v", transaction)
	}
	if got := readDrydockTestFile(t, f.ownedFile()); got != "reviewed cursor retry\n" {
		t.Fatalf("first Apply did not write reviewed content: %q", got)
	}
	// Preserve a distinct filesystem timestamp as well as file identity. A
	// same-content rewrite during retry must not hide behind the final hash.
	marker := time.Unix(1_600_000_000, 0)
	if err := os.Chtimes(f.ownedFile(), marker, marker); err != nil {
		t.Fatal(err)
	}
	fileAfterWrite, err := os.Stat(f.ownedFile())
	if err != nil {
		t.Fatal(err)
	}
	beforeRetry, found, err := f.fixture.state.GetDrydockByRun(t.Context(), f.fixture.run.ID)
	if err != nil || !found || beforeRetry.LastCheckpointID != transaction.BeforeCheckpointID {
		t.Fatalf("failed after cursor persisted unexpectedly: %+v found=%t err=%v", beforeRetry, found, err)
	}
	failures := f.failing.afterFailures
	if _, err := f.apply.Apply(t.Context(), f.request); err == nil || f.failing.afterFailures <= failures {
		t.Fatalf("same-key retry swallowed after cursor failure: failures=%d err=%v", f.failing.afterFailures, err)
	}
	f.assertNoCompletedApply(t)
	stillPending, _, err := f.fixture.state.GetDrydockByRun(t.Context(), f.fixture.run.ID)
	if err != nil || !reflect.DeepEqual(stillPending, beforeRetry) {
		t.Fatalf("failed retry changed owned metadata: %+v err=%v", stillPending, err)
	}
	assertDrydockApplyFileNotRewritten(t, f.ownedFile(), fileAfterWrite)
	assertDrydockRecoveryPreservesSource(t, f.fixture, f.fixture.state, f.sourceCursor)

	f.failing.failAfter = false
	result, err := f.apply.Apply(t.Context(), f.request)
	if err != nil || !result.Replayed || result.FileWritten || result.Result.Status != fileedit.ApplyCompleted {
		t.Fatalf("same-key recovery after storage repaired=%+v err=%v", result, err)
	}
	recovered, _, err := f.fixture.state.GetDrydockByRun(t.Context(), f.fixture.run.ID)
	if err != nil || recovered.LastCheckpointID != transaction.AfterCheckpointID ||
		recovered.Generation != beforeRetry.Generation+1 || f.failing.completeCalls != 1 {
		t.Fatalf("recovery did not seal cursor before Apply: owned=%+v completions=%d err=%v", recovered, f.failing.completeCalls, err)
	}
	if afterRetry := f.transaction(t); !reflect.DeepEqual(afterRetry, transaction) {
		t.Fatalf("retry rewrote the terminal mutation journal: %+v", afterRetry)
	}
	assertDrydockApplyFileNotRewritten(t, f.ownedFile(), fileAfterWrite)
	assertDrydockRecoveryPreservesSource(t, f.fixture, f.fixture.state, f.sourceCursor)
}

type drydockApplyCursorRetryFixture struct {
	fixture      drydockApplicationFixture
	owned        drydock.Workspace
	sourceCursor workspacecheckpoint.RunState
	failing      *drydockApplyCursorFailures
	apply        *FileEditApplyService
	request      ApplyFileEditRequest
}

func newDrydockApplyCursorRetryFixture(t *testing.T) drydockApplyCursorRetryFixture {
	t.Helper()
	fixture, owned := newFileEditDrydockFixture(t)
	checkpoints, sourceCursor := prepareDrydockBoundarySourceCursor(t, fixture)
	proposal := NewFileEditProposalService(fixture.state, policy.NewDefaultChecker()).WithDrydock(fixture.service)
	source, err := proposal.IssueSource(t.Context(), fixture.run.ID, "tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	created, err := proposal.Propose(t.Context(), CreateFileEditProposalRequest{Version: FileEditProposalProtocolVersion,
		RunID: fixture.run.ID, SourceHandle: source.Handle, ProposedText: "reviewed cursor retry\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileEditReviewService(fixture.state).WithDrydock(fixture.service).Review(t.Context(),
		ReviewFileEditRequest{Version: FileEditReviewProtocolVersion, RunID: fixture.run.ID,
			EditID: created.Edit.ID, Action: FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	failing := &drydockApplyCursorFailures{SQLiteStore: fixture.state}
	drydocks, err := NewDrydockService(failing, fixture.executor)
	if err != nil {
		t.Fatal(err)
	}
	drydocks.WithCheckpointService(checkpoints)
	return drydockApplyCursorRetryFixture{fixture: fixture, owned: owned, sourceCursor: sourceCursor,
		failing: failing, apply: NewFileEditApplyService(failing, policy.NewDefaultChecker(), checkpoints).WithDrydock(drydocks),
		request: ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion, RunID: fixture.run.ID,
			EditID: created.Edit.ID, OperationKey: "owned-apply-cursor-retry", AppliedBy: "operator"}}
}

func (f drydockApplyCursorRetryFixture) ownedFile() string {
	return filepath.Join(f.owned.Path, "tracked.txt")
}

func (f drydockApplyCursorRetryFixture) assertNoCompletedApply(t *testing.T) {
	t.Helper()
	digest := runmutation.FileEditApplyOperationDigest(f.request.RunID, f.request.EditID, f.request.OperationKey)
	_, result, found, err := f.fixture.state.GetFileEditApplyOperation(t.Context(), digest)
	if err != nil || !found || result != nil || f.failing.completeCalls != 0 {
		t.Fatalf("cursor failure completed Apply: result=%+v found=%t calls=%d err=%v", result, found, f.failing.completeCalls, err)
	}
}

func (f drydockApplyCursorRetryFixture) transaction(t *testing.T) workspacecheckpoint.Transaction {
	t.Helper()
	transactions, err := f.fixture.state.ListWorkspaceCheckpointTransactions(t.Context(), f.fixture.run.ID, 20)
	if err != nil || len(transactions) != 1 || transactions[0].TriggerReceiptID != f.request.EditID ||
		transactions[0].WorkspaceID != f.owned.WorkspaceID {
		t.Fatalf("exact single owned journal=%+v err=%v", transactions, err)
	}
	return transactions[0]
}

func assertDrydockApplyFileNotRewritten(t *testing.T, path string, expected os.FileInfo) {
	t.Helper()
	actual, err := os.Stat(path)
	if err != nil || !os.SameFile(actual, expected) || !actual.ModTime().Equal(expected.ModTime()) {
		t.Fatalf("same-key retry rewrote the already applied file: actual=%v expected=%v err=%v", actual, expected, err)
	}
}

type drydockApplyCursorFailures struct {
	*store.SQLiteStore
	failBefore     int
	failAfter      bool
	beforeFailures int
	afterFailures  int
	completeCalls  int
}

func (s *drydockApplyCursorFailures) AdvanceDrydock(ctx context.Context, value drydock.Workspace,
	expectedGeneration int64, receipt drydock.Receipt,
) (drydock.Workspace, bool, error) {
	checkpoint, err := s.SQLiteStore.GetWorkspaceCheckpoint(ctx, value.LastCheckpointID)
	if err != nil {
		return drydock.Workspace{}, false, err
	}
	if checkpoint.Phase == workspacecheckpoint.PhaseBefore && s.failBefore > 0 {
		s.failBefore--
		s.beforeFailures++
		return drydock.Workspace{}, false, errors.New("injected before cursor persistence failure")
	}
	if checkpoint.Phase == workspacecheckpoint.PhaseAfter && s.failAfter {
		s.afterFailures++
		return drydock.Workspace{}, false, errors.New("injected persistent after cursor failure")
	}
	return s.SQLiteStore.AdvanceDrydock(ctx, value, expectedGeneration, receipt)
}

func (s *drydockApplyCursorFailures) CompleteFileEditApply(ctx context.Context,
	result fileedit.ApplyResult,
) (fileedit.ApplyResult, bool, error) {
	s.completeCalls++
	return s.SQLiteStore.CompleteFileEditApply(ctx, result)
}
