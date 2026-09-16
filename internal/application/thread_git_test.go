package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/gitmutation"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/store"
)

func threadGitApplicationFixture(t *testing.T) (*ThreadGitService, *store.SQLiteStore, string, string, string) {
	t.Helper()
	f := newGitAdvancedApplicationFixture(t)
	if _, _, err := f.state.ReleaseRunExecutionLease(t.Context(), f.lease); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunService(f.state).Pause(t.Context(), f.run.ID); err != nil {
		t.Fatal(err)
	}
	thread, err := f.state.GetThreadByRun(t.Context(), f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := repository.NewMutationExecutor()
	remote, _ := repository.NewRemoteExecutor(nil)
	remote.AllowLocalRemotesForTest()
	checkpoints, err := NewWorkspaceCheckpointService(f.state, f.capabilities)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewThreadGitService(f.state, local, remote, checkpoints, nil, f.capabilities)
	return svc, f.state, thread.ID, f.run.ID, f.root
}

func TestThreadGitPausedSelectedCommitAndReadOnlyReplay(t *testing.T) {
	svc, st, threadID, runID, root := threadGitApplicationFixture(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("selected\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.txt"), []byte("other user's staged file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, "-C", root, "add", "other.txt")
	request := ThreadGitPreviewRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: ThreadGitSpec{Operation: "commit", Paths: []string{"selected.txt"}, Message: "selected file"}}
	preview, err := svc.Preview(ctx, threadID, request)
	if err != nil || !preview.CanExecute {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	execute := ThreadGitExecuteRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: request.Spec, OperationKey: "commit-selected-one", ExpectedPreviewFingerprint: preview.PreviewFingerprint, RequestedBy: "desktop-ui"}
	result, err := svc.Execute(ctx, threadID, execute)
	if err != nil || result.State != "completed" || !result.ReceiptSaved {
		t.Fatalf("execute=%#v err=%v", result, err)
	}
	if runFixtureGit(t, "-C", root, "show", ":other.txt") != "other user's staged file" {
		t.Fatal("unrelated staging lost")
	}
	if strings.Contains(runFixtureGit(t, "-C", root, "ls-tree", "--name-only", "HEAD"), "other.txt") {
		t.Fatal("unrelated file committed")
	}
	before, _ := st.ListRunEvents(ctx, runID)
	if err = os.WriteFile(filepath.Join(root, "selected.txt"), []byte("newer unreviewed content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		replay, err := svc.Execute(ctx, threadID, execute)
		if err != nil || !replay.Replayed || replay.CommitOID != result.CommitOID {
			t.Fatalf("replay=%#v err=%v", replay, err)
		}
	}
	observed, err := svc.Observe(ctx, threadID, execute.OperationKey)
	if err != nil || observed.CommitOID != result.CommitOID {
		t.Fatalf("observe=%#v err=%v", observed, err)
	}
	after, _ := st.ListRunEvents(ctx, runID)
	if len(before) != len(after) {
		t.Fatal("replay or observation mutated events")
	}
	run, _ := st.GetRun(ctx, runID)
	if run.Status != domain.RunPaused {
		t.Fatalf("operator Git resumed task: %s", run.Status)
	}
	lease, found, _ := st.GetRunExecutionLease(ctx, runID)
	if found && lease.ActiveAt(time.Now().UTC()) {
		t.Fatal("Git left an active lease")
	}
	execute.Spec.Message = "different"
	if _, err = svc.Execute(ctx, threadID, execute); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed key payload not rejected: %v", err)
	}
}

type threadGitLoseCompletionStore struct{ *store.SQLiteStore }

func (s threadGitLoseCompletionStore) CompleteGitMutationOperation(context.Context, string, repository.MutationRecord, time.Time) (repository.MutationRecord, bool, error) {
	return repository.MutationRecord{}, false, errors.New("fixture lost commit receipt")
}

func TestThreadGitUnknownCommitObservedFromExactStoredMarker(t *testing.T) {
	svc, st, threadID, runID, root := threadGitApplicationFixture(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("exact\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := ThreadGitPreviewRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: ThreadGitSpec{Operation: "commit", Paths: []string{"selected.txt"}, Message: "one commit"}}
	preview, err := svc.Preview(ctx, threadID, request)
	if err != nil {
		t.Fatal(err)
	}
	svc.store = threadGitLoseCompletionStore{st}
	execute := ThreadGitExecuteRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: request.Spec, OperationKey: "unknown-commit", ExpectedPreviewFingerprint: preview.PreviewFingerprint, RequestedBy: "desktop-ui"}
	_, err = svc.Execute(ctx, threadID, execute)
	if err == nil {
		t.Fatal("fixture should lose receipt")
	}
	before := runFixtureGit(t, "-C", root, "rev-list", "--count", "HEAD")
	svc.store = st
	observed, err := svc.Observe(ctx, threadID, execute.OperationKey)
	if err != nil || observed.State != "completed" || !observed.Observed || observed.ReceiptSaved {
		t.Fatalf("observe=%#v err=%v", observed, err)
	}
	record, found, err := st.GetGitMutationByKey(ctx, threadGitKey(threadID, execute.OperationKey))
	if err != nil || !found || record.CompletedAt != nil || record.StartedAt == nil {
		t.Fatalf("read filled old receipt %#v %v", record, err)
	}
	if runFixtureGit(t, "-C", root, "rev-list", "--count", "HEAD") != before {
		t.Fatal("observation recommitted")
	}
}

type threadGitLosePushCompletionStore struct{ *store.SQLiteStore }

func (s threadGitLosePushCompletionStore) CompleteRemoteOperation(context.Context, string, gitmutation.RemoteRecord, time.Time) (gitmutation.RemoteRecord, bool, error) {
	return gitmutation.RemoteRecord{}, false, errors.New("fixture lost push receipt")
}

func TestThreadGitUnknownPushObservedWithoutRepeating(t *testing.T) {
	svc, st, threadID, runID, _ := threadGitApplicationFixture(t)
	ctx := t.Context()
	bare := filepath.Join(t.TempDir(), "bare.git")
	runFixtureGit(t, "init", "--bare", "-q", bare)
	request := ThreadGitPreviewRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: ThreadGitSpec{Operation: "push_branch", RemoteURL: "file:///" + filepath.ToSlash(bare), Branch: "reviewed"}}
	preview, err := svc.Preview(ctx, threadID, request)
	if err != nil || !preview.CanExecute {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	svc.store = threadGitLosePushCompletionStore{st}
	execute := ThreadGitExecuteRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: request.Spec, OperationKey: "unknown-push", ExpectedPreviewFingerprint: preview.PreviewFingerprint, RequestedBy: "desktop-ui"}
	_, err = svc.Execute(ctx, threadID, execute)
	if err == nil {
		t.Fatal("fixture should lose receipt")
	}
	svc.store = st
	observed, err := svc.Observe(ctx, threadID, execute.OperationKey)
	if err != nil || observed.State != "completed" || !observed.Observed || observed.RemoteOID == "" || observed.ReceiptSaved {
		t.Fatalf("observe=%#v err=%v", observed, err)
	}
	record, _, _ := st.GetGitRemoteByKey(ctx, threadGitKey(threadID, execute.OperationKey))
	if record.CompletedAt != nil {
		t.Fatal("observation backfilled receipt")
	}
}

func TestThreadGitPausedManagedWorktreeKeepsTaskDirectory(t *testing.T) {
	svc, st, threadID, runID, root := threadGitApplicationFixture(t)
	ctx := t.Context()
	executor, err := repository.NewAdvancedExecutor(filepath.Join(t.TempDir(), "managed"), true)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := NewGitAdvancedService(st, executor, svc.capabilities)
	if err != nil {
		t.Fatal(err)
	}
	svc.WithAdvanced(advanced)
	spec := ThreadGitSpec{Operation: "worktree_create", Branch: "independent-work", WorktreeName: "independent"}
	preview, err := svc.Preview(ctx, threadID, ThreadGitPreviewRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: spec})
	if err != nil || !preview.CanExecute {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	request := ThreadGitExecuteRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: spec, OperationKey: "create-managed-worktree", ExpectedPreviewFingerprint: preview.PreviewFingerprint, RequestedBy: "desktop-ui"}
	result, err := svc.Execute(ctx, threadID, request)
	if err != nil || result.State != "completed" || result.WorktreePath == "" {
		t.Fatalf("execute=%#v err=%v", result, err)
	}
	if _, err = os.Stat(filepath.Join(result.WorktreePath, "base.txt")); err != nil {
		t.Fatal(err)
	}
	bound, err := svc.CaptureThreadGitContext(ctx, threadID)
	if err != nil || bound.RootPath != root {
		t.Fatalf("task was moved: %#v %v", bound, err)
	}
	replay, err := svc.Execute(ctx, threadID, request)
	if err != nil || !replay.Replayed || replay.WorktreePath != result.WorktreePath {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	record, found, err := st.GetGitAdvancedOperation(ctx, result.OperationID)
	if err != nil || !found || record.Status != gitadvanced.OperationSucceeded {
		t.Fatalf("record=%#v %v", record, err)
	}
}

func TestThreadGitActiveLeaseAndChangedPermissionRejectBeforeIntent(t *testing.T) {
	svc, st, threadID, runID, root := threadGitApplicationFixture(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("not committed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := ThreadGitPreviewRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: ThreadGitSpec{Operation: "commit", Paths: []string{"selected.txt"}, Message: "must reject"}}
	preview, err := svc.Preview(ctx, threadID, input)
	if err != nil {
		t.Fatal(err)
	}
	request := ThreadGitExecuteRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: input.Spec, OperationKey: "busy-or-revoked-key", ExpectedPreviewFingerprint: preview.PreviewFingerprint, RequestedBy: "desktop-ui"}
	head := runFixtureGit(t, "-C", root, "rev-parse", "HEAD")
	lease, err := st.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: runID, OwnerID: "other-current-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Execute(ctx, threadID, request); err == nil {
		t.Fatal("foreign active lease accepted")
	}
	if _, _, err = st.ReleaseRunExecutionLease(ctx, lease.Lease); err != nil {
		t.Fatal(err)
	}
	_, err = NewRunExecutionPermissionService(st, svc.capabilities).Change(ctx, ChangeRunExecutionPermissionRequest{RunID: runID, Mode: "conservative", OperationKey: "thread-git-revoke-permission", RequestedBy: "fixture", Reason: "revoke before Git confirmation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Execute(ctx, threadID, request); err == nil {
		t.Fatal("revoked Git permission accepted")
	}
	if _, found, err := st.GetGitMutationByKey(ctx, threadGitKey(threadID, request.OperationKey)); err != nil || found {
		t.Fatalf("blocked operation created intent: %v %v", found, err)
	}
	if runFixtureGit(t, "-C", root, "rev-parse", "HEAD") != head {
		t.Fatal("blocked operation changed HEAD")
	}
}

func TestThreadGitDrydockCommitAndCheckpointUseActualTaskDirectory(t *testing.T) {
	f := newDrydockApplicationFixture(t, "thread Git source")
	ctx := t.Context()
	writeDrydockTestFile(t, filepath.Join(f.sourceRoot, "tracked.txt"), "user source\r\n")
	owned := mustCreateDrydock(t, f)
	caps := standardCodeThreadTestRuntime().ExecutionPermissionCapabilities
	if _, err := NewRunExecutionProfileService(f.state).Change(ctx, ChangeRunExecutionProfileRequest{RunID: f.run.ID, Profile: "local", OperationKey: "git-drydock-local-profile", RequestedBy: "fixture", Reason: "isolated Git fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunExecutionPermissionService(f.state, caps).Change(ctx, ChangeRunExecutionPermissionRequest{RunID: f.run.ID, Mode: "approval", OperationKey: "git-drydock-permission", RequestedBy: "fixture", Reason: "exact Git confirmation", ConfirmUserApproval: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunService(f.state).Start(ctx, f.run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunService(f.state).Pause(ctx, f.run.ID); err != nil {
		t.Fatal(err)
	}
	cp, err := NewWorkspaceCheckpointService(f.state, caps)
	if err != nil {
		t.Fatal(err)
	}
	local, err := repository.NewMutationExecutor()
	if err != nil {
		t.Fatal(err)
	}
	svc := NewThreadGitService(f.state, local, nil, cp, f.service, caps)
	if _, _, err = cp.Capture(ctx, WorkspaceCheckpointCaptureRequest{RunID: f.run.ID, OperationKey: "git-preserve-source-cursor", RequestedBy: "cli_operator", Title: "Source history"}); err != nil {
		t.Fatal(err)
	}
	sourceCursor, _, err := f.state.GetWorkspaceCheckpointRunState(ctx, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := f.state.GetThreadByRun(ctx, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	writeDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt"), "task selected content\n")
	head := runFixtureGit(t, "-C", f.sourceRoot, "rev-parse", "HEAD")
	preview, err := svc.Preview(ctx, thread.ID, ThreadGitPreviewRequest{Version: ThreadGitProtocolVersion, RunID: f.run.ID, Spec: ThreadGitSpec{Operation: "commit", Paths: []string{"tracked.txt"}, Message: "Drydock only"}})
	if err != nil || !preview.CanExecute || preview.WorkspaceID != owned.WorkspaceID {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	result, err := svc.Execute(ctx, thread.ID, ThreadGitExecuteRequest{Version: ThreadGitProtocolVersion, RunID: f.run.ID, Spec: preview.Spec, OperationKey: "git-owned-workspace-commit", ExpectedPreviewFingerprint: preview.PreviewFingerprint, RequestedBy: "desktop-ui"})
	if err != nil || result.State != "completed" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if got := runFixtureGit(t, "-C", owned.Path, "show", "HEAD:tracked.txt"); got != "task selected content" {
		t.Fatalf("wrong committed content: %s", got)
	}
	if runFixtureGit(t, "-C", f.sourceRoot, "rev-parse", "HEAD") != head || readDrydockTestFile(t, filepath.Join(f.sourceRoot, "tracked.txt")) != "user source\r\n" {
		t.Fatal("task Git changed source repository")
	}
	op, found, err := f.state.GetGitMutationByKey(ctx, threadGitKey(thread.ID, "git-owned-workspace-commit"))
	if err != nil || !found || op.WorkspaceID != owned.WorkspaceID {
		t.Fatalf("wrong saved Git target: %#v %v", op, err)
	}
	boundary, found, err := f.state.GetWorkspaceCheckpointTransactionByOperation(ctx, workspaceBoundaryOperationDigest(f.run.ID, "git_mutation", threadGitKey(thread.ID, "git-owned-workspace-commit")))
	if err != nil || !found || boundary.WorkspaceID != owned.WorkspaceID {
		t.Fatalf("wrong checkpoint target: %#v %v", boundary, err)
	}
	afterCursor, _, err := f.state.GetWorkspaceCheckpointRunState(ctx, f.run.ID)
	if err != nil || afterCursor != sourceCursor {
		t.Fatalf("Drydock Git changed source history cursor: %#v %v", afterCursor, err)
	}
	latest, _, err := f.state.GetDrydockByRun(ctx, f.run.ID)
	if err != nil || latest.LastCheckpointID != boundary.AfterCheckpointID || latest.ExpectedHead != result.CommitOID {
		t.Fatalf("Drydock cursor lost Git result: %#v %v", latest, err)
	}
	blocked, err := svc.Preview(ctx, thread.ID, ThreadGitPreviewRequest{Version: ThreadGitProtocolVersion, RunID: f.run.ID, Spec: ThreadGitSpec{Operation: "switch_branch", Branch: "main"}})
	if err != nil || blocked.CanExecute || blocked.BlockedReason == "" {
		t.Fatalf("owned branch switch not blocked: %#v %v", blocked, err)
	}
}
