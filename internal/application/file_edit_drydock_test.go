package application

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

// The source deliberately differs from its committed worktree. A file identity
// check must exercise real SQLite/Git resolution, not interchangeable mock roots.
func TestFileEditConfiguredSourceUsesOwnedDrydock(t *testing.T) {
	fixture, owned := newFileEditDrydockFixture(t)
	service := NewFileEditProposalService(fixture.state, policy.NewDefaultChecker()).WithDrydock(fixture.service)
	source, err := service.IssueSource(t.Context(), fixture.run.ID, "tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	if source.WorkspaceID != owned.WorkspaceID || source.Content != "base\n" {
		t.Fatalf("configured source used another directory: workspace=%s content=%q; want %s base LF",
			source.WorkspaceID, source.Content, owned.WorkspaceID)
	}
	if got := readDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt")); got != "user source\r\n" {
		t.Fatalf("source changed: %q", got)
	}
}

func TestFileEditDrydockReviewApplyInverseAndCursorStayOnOwnedWorkspace(t *testing.T) {
	fixture, owned := newFileEditDrydockFixture(t)
	ctx := t.Context()
	checkpoints, err := NewWorkspaceCheckpointService(fixture.state,
		standardCodeThreadTestRuntime().ExecutionPermissionCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.WithCheckpointService(checkpoints)
	// The source cursor already exists. The private Drydock boundary must not
	// replace it while sealing the different worktree's before/after snapshots.
	if _, _, err := checkpoints.Capture(ctx, WorkspaceCheckpointCaptureRequest{RunID: fixture.run.ID,
		OperationKey: "source-cursor", RequestedBy: "cli_operator", Title: "Source history"}); err != nil {
		t.Fatal(err)
	}
	sourceCursor, found, err := fixture.state.GetWorkspaceCheckpointRunState(ctx, fixture.run.ID)
	if err != nil || !found || sourceCursor.WorkspaceID != fixture.workspace.ID {
		t.Fatalf("source cursor=%+v found=%t err=%v", sourceCursor, found, err)
	}
	proposal := NewFileEditProposalService(fixture.state, policy.NewDefaultChecker()).WithDrydock(fixture.service)
	source, err := proposal.IssueSource(ctx, fixture.run.ID, "tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	created, err := proposal.Propose(ctx, CreateFileEditProposalRequest{Version: FileEditProposalProtocolVersion,
		RunID: fixture.run.ID, SourceHandle: source.Handle, ProposedText: "reviewed Drydock edit\n"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Edit.WorkspaceID != owned.WorkspaceID || created.Edit.OriginalText != "base\n" {
		t.Fatalf("proposal lost owned identity: %+v", created.Edit)
	}
	request := ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion,
		RunID: fixture.run.ID, EditID: created.Edit.ID, OperationKey: "owned-file-apply-0001", AppliedBy: "operator"}
	apply := NewFileEditApplyService(fixture.state, policy.NewDefaultChecker(), checkpoints).WithDrydock(fixture.service)
	if _, err := apply.Apply(ctx, request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("unreviewed apply error=%v", err)
	}
	review := NewFileEditReviewService(fixture.state).WithDrydock(fixture.service)
	if _, err := review.Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion,
		RunID: fixture.run.ID, EditID: created.Edit.ID, Action: FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	approved, err := fixture.state.GetApprovalByProposal(ctx, created.Edit.ID)
	if err != nil || approved.WorkspaceID != owned.WorkspaceID || approved.RunID != fixture.run.ID {
		t.Fatalf("approval target=%+v err=%v", approved, err)
	}
	interrupted := &fileEditFailTerminalOnce{SQLiteStore: fixture.state, fail: true}
	if _, err := NewFileEditApplyService(interrupted, policy.NewDefaultChecker(), checkpoints).
		WithDrydock(fixture.service).Apply(ctx, request); err == nil {
		t.Fatal("injected terminal persistence failure was not reached")
	}
	sealedBeforeRetry, _, err := fixture.state.GetDrydockByRun(ctx, fixture.run.ID)
	if err != nil || sealedBeforeRetry.LastCheckpointID == owned.LastCheckpointID {
		t.Fatalf("Apply result attempted before owned checkpoint completed: %+v err=%v", sealedBeforeRetry, err)
	}
	applied, err := apply.Apply(ctx, request)
	if err != nil || applied.FileWritten || !applied.Replayed || applied.Operation.WorkspaceID != owned.WorkspaceID ||
		applied.Result.Status != fileedit.ApplyCompleted {
		t.Fatalf("prepared recovery=%+v err=%v", applied, err)
	}
	if replayedWorkspace, _, err := fixture.state.GetDrydockByRun(ctx, fixture.run.ID); err != nil || replayedWorkspace.Generation != sealedBeforeRetry.Generation ||
		replayedWorkspace.LastCheckpointID != sealedBeforeRetry.LastCheckpointID {
		t.Fatalf("retry repeated owned checkpoint attribution: %+v err=%v", replayedWorkspace, err)
	}
	if got := readDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt")); got != "user source\r\n" {
		t.Fatalf("apply changed source: %q", got)
	}
	if got := readDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt")); got != "reviewed Drydock edit\n" {
		t.Fatalf("apply missed Drydock: %q", got)
	}
	after, _, err := fixture.state.GetDrydockByRun(ctx, fixture.run.ID)
	if err != nil || after.LastCheckpointID == owned.LastCheckpointID || after.Generation <= owned.Generation {
		t.Fatalf("owned cursor did not advance: %+v err=%v", after, err)
	}
	if cursor, _, err := fixture.state.GetWorkspaceCheckpointRunState(ctx, fixture.run.ID); err != nil || !reflect.DeepEqual(cursor, sourceCursor) {
		t.Fatalf("owned apply rewrote source cursor: %+v err=%v", cursor, err)
	}
	transactions, err := fixture.state.ListWorkspaceCheckpointTransactions(ctx, fixture.run.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var matched bool
	for _, transaction := range transactions {
		if transaction.TriggerReceiptID != created.Edit.ID {
			continue
		}
		matched = transaction.WorkspaceID == owned.WorkspaceID && transaction.Status == workspacecheckpoint.TransactionCompleted
		for _, id := range []string{transaction.BeforeCheckpointID, transaction.AfterCheckpointID} {
			checkpoint, err := fixture.state.GetWorkspaceCheckpoint(ctx, id)
			if err != nil || checkpoint.WorkspaceID != owned.WorkspaceID || checkpoint.RunID != fixture.run.ID {
				t.Fatalf("checkpoint target=%+v err=%v", checkpoint, err)
			}
		}
	}
	if !matched {
		t.Fatal("applied edit has no completed owned checkpoint transaction")
	}
	// Reopening SQLite and omitting runtime wiring proves the durable terminal
	// replay is independent of any current filesystem target or execution lease.
	reopened, err := store.Open(fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	readOnly := &fileEditNoWorkspaceReads{SQLiteStore: reopened}
	replay, err := NewFileEditApplyService(readOnly, policy.NewDefaultChecker()).Apply(ctx, request)
	if err != nil || !replay.Replayed || replay.FileWritten || readOnly.reads != 0 ||
		replay.Result != applied.Result {
		t.Fatalf("terminal replay read current target: replay=%+v reads=%d err=%v", replay, readOnly.reads, err)
	}
	inverse, err := proposal.ProposeRevert(ctx, CreateFileEditRevertProposalRequest{Version: FileEditProposalProtocolVersion,
		RunID: fixture.run.ID, SourceEditID: created.Edit.ID, OperationKey: "owned-file-inverse-0001"})
	if err != nil || inverse.Edit.WorkspaceID != owned.WorkspaceID || inverse.Edit.ProposedText != "base\n" {
		t.Fatalf("inverse=%+v err=%v", inverse, err)
	}
	if _, err := review.Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion,
		RunID: fixture.run.ID, EditID: inverse.Edit.ID, Action: FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	request.EditID, request.OperationKey = inverse.Edit.ID, "owned-file-inverse-apply-0001"
	if _, err := apply.Apply(ctx, request); err != nil {
		t.Fatal(err)
	}
	if got := readDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt")); got != "base\n" {
		t.Fatalf("inverse did not restore Drydock: %q", got)
	}
	if got := readDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt")); got != "user source\r\n" {
		t.Fatalf("inverse touched source: %q", got)
	}
}

type fileEditNoWorkspaceReads struct {
	*store.SQLiteStore
	reads int
}

type fileEditFailTerminalOnce struct {
	*store.SQLiteStore
	fail bool
}

func (s *fileEditFailTerminalOnce) CompleteFileEditApply(ctx context.Context, result fileedit.ApplyResult) (fileedit.ApplyResult, bool, error) {
	if s.fail {
		s.fail = false
		return fileedit.ApplyResult{}, false, errors.New("injected interruption after owned boundary")
	}
	return s.SQLiteStore.CompleteFileEditApply(ctx, result)
}

func (s *fileEditNoWorkspaceReads) GetWorkspaceInfo(context.Context, string) (session.WorkspaceInfo, error) {
	s.reads++
	return session.WorkspaceInfo{}, errors.New("completed replay must not resolve a current workspace")
}

func TestFileEditDrydockSourceHandleRejectsChangedGeneration(t *testing.T) {
	fixture, owned := newFileEditDrydockFixture(t)
	service := NewFileEditProposalService(fixture.state, policy.NewDefaultChecker()).WithDrydock(fixture.service)
	source, err := service.IssueSource(t.Context(), fixture.run.ID, "tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileEditProposalService(fixture.state, policy.NewDefaultChecker()).IssueSource(
		t.Context(), fixture.run.ID, "tracked.txt"); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("missing Drydock runtime fell back to source: %v", err)
	}
	if _, err := fixture.service.Use(t.Context(), DrydockUseRequest{RunID: fixture.run.ID,
		ExpectedGeneration: owned.Generation, OperationKey: "another-owned-generation", RequestedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Propose(t.Context(), CreateFileEditProposalRequest{Version: FileEditProposalProtocolVersion,
		RunID: fixture.run.ID, SourceHandle: source.Handle, ProposedText: "stale proposal\n"}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale generation was accepted: %v", err)
	}
	items, err := fixture.state.ListFileEdits(t.Context(), fileedit.ListFilter{SessionID: fixture.run.SessionID})
	if err != nil || len(items) != 0 {
		t.Fatalf("rejected handle persisted proposal: %d err=%v", len(items), err)
	}
}

func TestFileEditOldSourceProposalNeverBecomesDrydockAuthority(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "source proposal before preset")
	ctx := t.Context()
	run, err := NewRunService(fixture.state).Start(ctx, fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture.run = run
	proposals := NewFileEditProposalService(fixture.state, policy.NewDefaultChecker())
	reviews := NewFileEditReviewService(fixture.state)
	makeProposal := func(text string, approve bool) fileedit.Edit {
		t.Helper()
		source, err := proposals.IssueSource(ctx, run.ID, "tracked.txt")
		if err != nil {
			t.Fatal(err)
		}
		created, err := proposals.Propose(ctx, CreateFileEditProposalRequest{Version: FileEditProposalProtocolVersion,
			RunID: run.ID, SourceHandle: source.Handle, ProposedText: text})
		if err != nil {
			t.Fatal(err)
		}
		if approve {
			if _, err := reviews.Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion,
				RunID: run.ID, EditID: created.Edit.ID, Action: FileEditApproveIntent}); err != nil {
				t.Fatal(err)
			}
		}
		return created.Edit
	}
	appliedSource := makeProposal("legacy source edit\n", true)
	applyRequest := ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion,
		RunID: run.ID, EditID: appliedSource.ID, OperationKey: "legacy-source-apply", AppliedBy: "operator"}
	if _, err := NewFileEditApplyService(fixture.state, policy.NewDefaultChecker()).Apply(ctx, applyRequest); err != nil {
		t.Fatal(err)
	}
	approvedSource := makeProposal("unapplied old authority\n", true)
	pendingSource := makeProposal("pending old authority\n", false)
	owned := configureFileEditDrydockFixture(t, fixture)
	reviews.WithDrydock(fixture.service)
	if _, err := reviews.Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion,
		RunID: run.ID, EditID: pendingSource.ID, Action: FileEditApproveIntent}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("old source review became owned authority: %v", err)
	}
	staleApply := applyRequest
	staleApply.EditID, staleApply.OperationKey = approvedSource.ID, "old-approved-after-preset"
	if _, err := NewFileEditApplyService(fixture.state, policy.NewDefaultChecker()).WithDrydock(fixture.service).
		Apply(ctx, staleApply); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("old source apply was reinterpreted: %v", err)
	}
	if _, err := proposals.WithDrydock(fixture.service).ProposeRevert(ctx, CreateFileEditRevertProposalRequest{
		Version: FileEditProposalProtocolVersion, RunID: run.ID, SourceEditID: appliedSource.ID,
		OperationKey: "old-source-inverse"}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("old source inverse was reinterpreted: %v", err)
	}
	// The historical decision can still be denied and its completed receipt read.
	if _, err := reviews.Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion,
		RunID: run.ID, EditID: pendingSource.ID, Action: FileEditDeny}); err != nil {
		t.Fatal(err)
	}
	readOnly := &fileEditNoWorkspaceReads{SQLiteStore: fixture.state}
	if replay, err := NewFileEditApplyService(readOnly, policy.NewDefaultChecker()).Apply(ctx, applyRequest); err != nil || !replay.Replayed || replay.FileWritten || readOnly.reads != 0 {
		t.Fatalf("historical source replay=%+v reads=%d err=%v", replay, readOnly.reads, err)
	}
	if got := readDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt")); got != "legacy source edit\n" {
		t.Fatalf("old operation touched source: %q", got)
	}
	if got := readDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt")); got != "base\n" {
		t.Fatalf("old operation touched Drydock: %q", got)
	}
}

func newFileEditDrydockFixture(t *testing.T) (drydockApplicationFixture, drydock.Workspace) {
	t.Helper()
	fixture := newDrydockApplicationFixture(t, "file edit owned target")
	writeDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt"), "user source\r\n")
	owned := configureFileEditDrydockFixture(t, fixture)
	fixture.run, _ = fixture.state.GetRun(t.Context(), fixture.run.ID)
	return fixture, owned
}

func configureFileEditDrydockFixture(t *testing.T, fixture drydockApplicationFixture) drydock.Workspace {
	t.Helper()
	preset, err := NewStandardCodePresetService(fixture.state, fixture.service, standardCodeThreadTestRuntime())
	if err != nil {
		t.Fatal(err)
	}
	request := ConfigureStandardCodeRequest{Version: domain.StandardCodePresetProtocolVersion,
		RunID: fixture.run.ID, Action: "configure", BackendIntent: "local",
		OperationKey: "file-edit-drydock-configure", RequestedBy: "operator"}
	if fixture.run.Status == domain.RunRunning {
		request.Action = "pause_and_configure"
	}
	preview, err := preset.Configure(t.Context(), request)
	if err != nil || !preview.TrustRequired {
		t.Fatalf("preset preview=%+v err=%v", preview, err)
	}
	request.ConfirmWorkspaceTrust, request.ExpectedTrustDigest = true, preview.TrustDigest
	if _, err := preset.Configure(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	owned, found, err := fixture.state.GetDrydockByRun(t.Context(), fixture.run.ID)
	if err != nil || !found {
		t.Fatalf("owned workspace found=%t err=%v", found, err)
	}
	current, err := fixture.state.GetRun(t.Context(), fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status == domain.RunPaused {
		_, err = NewRunService(fixture.state).Resume(t.Context(), fixture.run.ID)
	} else {
		_, err = NewRunService(fixture.state).Start(t.Context(), fixture.run.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	return owned
}
