package application

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestThreadDrydockPreparedCleanupBlocksExecutionAndPublicationAcrossConnections(t *testing.T) {
	f, physical := newFileEditDrydockFixture(t)
	ctx := t.Context()
	other, err := store.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	digest, fingerprint := strings.Repeat("c", 64), strings.Repeat("d", 64)
	at := time.Now().UTC()
	if err := f.state.BeginThreadDrydockCleanup(ctx, f.run.ID, physical.ID, physical.Generation, digest, fingerprint, at); err != nil {
		t.Fatal(err)
	}
	if err := other.BeginThreadDrydockCleanup(ctx, f.run.ID, physical.ID, physical.Generation, digest, fingerprint, at); err != nil {
		t.Fatalf("same exact prepared cleanup could not be resumed: %v", err)
	}
	if err := other.BeginThreadDrydockCleanup(ctx, f.run.ID, physical.ID, physical.Generation, digest, strings.Repeat("e", 64), at); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("different cleanup intent reused its key: %v", err)
	}
	if _, err := f.service.Use(ctx, DrydockUseRequest{RunID: f.run.ID, ExpectedGeneration: physical.Generation,
		OperationKey: "use-while-cleanup-is-reserved", RequestedBy: "operator"}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("metadata lifecycle advanced during reserved cleanup: %v", err)
	}
	if _, err := other.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: f.run.ID, OwnerID: "cleanup-racing-worker", TTL: time.Minute}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("execution acquired a directory while cleanup was reserved: %v", err)
	}
	if _, err := NewRunService(f.state).Fail(ctx, f.run.ID, "end context while exact cleanup awaits confirmation"); err != nil {
		t.Fatal(err)
	}
	thread, err := f.state.GetThreadByRun(ctx, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewThreadServiceWithExecutionCapabilities(other, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities).WithDrydock(f.service).Submit(ctx, SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: thread.ID, OperationKey: "cleanup-blocks-next-context", Content: "Continue the task", RequestedBy: "operator"})
	if err == nil {
		t.Fatal("successor published while physical cleanup was unresolved")
	}
	current, err := f.state.GetThread(ctx, thread.ID)
	if err != nil || current.ActiveRunID != "" || current.LastRunID != f.run.ID {
		t.Fatalf("cleanup fence allowed partial publication: %+v %v", current, err)
	}
	if err := other.BeginThreadDrydockCleanup(ctx, f.run.ID, physical.ID, physical.Generation, digest, fingerprint, at); err != nil {
		t.Fatalf("terminal Run could not confirm original cleanup identity: %v", err)
	}
	unchanged, found, err := f.state.GetDrydockByRun(ctx, f.run.ID)
	if err != nil || !found || unchanged.Generation != physical.Generation || unchanged.LastCheckpointID != physical.LastCheckpointID {
		t.Fatalf("reservation pretended to remove or checkpoint files: %+v %v", unchanged, err)
	}
	if got := readDrydockTestFile(t, filepath.Join(physical.Path, "tracked.txt")); got != "base\n" {
		t.Fatalf("reservation changed physical content: %q", got)
	}
	if got := readDrydockTestFile(t, filepath.Join(f.sourceRoot, "tracked.txt")); got != "user source\r\n" {
		t.Fatalf("reservation changed user source: %q", got)
	}
}

// Actual SQLite/Git and file review/apply. No Job or OS execution is simulated
// as a passed verification; command assertions cover its real scope resolver.
func TestThreadDrydockSuccessorAppliesWithCurrentIdentityAndPreservesPhysicalOwner(t *testing.T) {
	f, original := newFileEditDrydockFixture(t)
	ctx := t.Context()
	st := f.state
	checkpoints, err := NewWorkspaceCheckpointService(st, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	f.service.WithCheckpointService(checkpoints)
	oldSource, err := NewFileEditProposalService(st, policy.NewDefaultChecker()).WithDrydock(f.service).IssueSource(ctx, f.run.ID, "tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := st.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: f.run.ID, OwnerID: "old-physical-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunService(st).Fail(ctx, f.run.ID, "finish old epoch"); err != nil {
		t.Fatal(err)
	}
	thread, err := st.GetThreadByRun(ctx, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	request := SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: thread.ID, OperationKey: "current-physical-context", Content: "Make the next change in this working directory", RequestedBy: "operator"}
	service := NewThreadServiceWithExecutionCapabilities(other, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities).WithDrydock(f.service)
	if _, err := service.Submit(ctx, request); err == nil {
		t.Fatal("another connection published a holder while the physical directory had a live lease")
	}
	if _, _, err := st.ReleaseRunExecutionLease(ctx, lease.Lease); err != nil {
		t.Fatal(err)
	}
	result, err := service.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	run, err := NewRunService(st).Start(ctx, result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: f.run.ID, OwnerID: "old-worker-reentry", TTL: time.Minute}); err == nil {
		t.Fatal("old epoch regained execution")
	}
	if _, err := NewRunService(st).Resume(ctx, f.run.ID); err == nil {
		t.Fatal("old epoch resumed after publication")
	}
	for _, test := range []struct {
		run, session string
		want         bool
	}{{run.ID, run.SessionID, true}, {f.run.ID, f.run.SessionID, false}, {run.ID, f.run.SessionID, false}} {
		ok, err := commandRuntimeDrydockBound(ctx, st, original, test.run, run.MissionID, test.session, f.workspace.ID)
		if err != nil || ok != test.want {
			t.Fatalf("command logical binding run=%s session=%s got=%t want=%t err=%v", test.run, test.session, ok, test.want, err)
		}
	}
	proposal := NewFileEditProposalService(st, policy.NewDefaultChecker()).WithDrydock(f.service)
	if _, err := proposal.Propose(ctx, CreateFileEditProposalRequest{Version: FileEditProposalProtocolVersion, RunID: run.ID, SourceHandle: oldSource.Handle, ProposedText: "stale source handle\n"}); err == nil {
		t.Fatal("old Run source handle authorized a new Run proposal")
	}
	source, err := proposal.IssueSource(ctx, run.ID, "tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	created, err := proposal.Propose(ctx, CreateFileEditProposalRequest{Version: FileEditProposalProtocolVersion, RunID: run.ID, SourceHandle: source.Handle, ProposedText: "new epoch reviewed change\n"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Edit.SessionID != run.SessionID || created.Edit.WorkspaceID != original.WorkspaceID {
		t.Fatal("new edit used physical creator identity")
	}
	if _, err := NewFileEditReviewService(st).WithDrydock(f.service).Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion, RunID: run.ID, EditID: created.Edit.ID, Action: FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	applyRequest := ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion, RunID: run.ID, EditID: created.Edit.ID, OperationKey: "new-epoch-apply-0001", AppliedBy: "operator"}
	apply := NewFileEditApplyService(st, policy.NewDefaultChecker(), checkpoints).WithDrydock(f.service)
	applied, err := apply.Apply(ctx, applyRequest)
	if err != nil || !applied.FileWritten || applied.Result.Status != fileedit.ApplyCompleted {
		var inspect func(error)
		inspect = func(e error) {
			if e == nil {
				return
			}
			t.Logf("apply error cause: %T %v", e, e)
			if many, ok := e.(interface{ Unwrap() []error }); ok {
				for _, child := range many.Unwrap() {
					inspect(child)
				}
			} else if one, ok := e.(interface{ Unwrap() error }); ok {
				inspect(one.Unwrap())
			}
		}
		inspect(err)
		db, openErr := sql.Open("sqlite3", f.databasePath)
		if openErr == nil {
			defer db.Close()
			for _, name := range []string{"trg_workspace_checkpoint_parent_binding", "trg_workspace_checkpoint_insert_scope"} {
				var text string
				if queryErr := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name=?`, name).Scan(&text); queryErr == nil {
					t.Logf("actual %s=%s", name, text)
				}
			}
		}

		t.Fatalf("new holder apply=%+v err=%v", applied, err)
	}
	updated, found, err := st.GetDrydockByRun(ctx, f.run.ID)
	if err != nil || !found || updated.RunID != f.run.ID || updated.SessionID != f.run.SessionID || updated.WorkspaceID != original.WorkspaceID {
		t.Fatalf("physical owner changed: %+v %v", updated, err)
	}
	if _, found, err := st.GetDrydockByRun(ctx, run.ID); err != nil || found {
		t.Fatalf("successor fabricated a physical owner: %v", err)
	}
	cp, err := st.GetWorkspaceCheckpoint(ctx, updated.LastCheckpointID)
	if err != nil || cp.RunID != run.ID || cp.SessionID != run.SessionID || cp.WorkspaceID != original.WorkspaceID {
		t.Fatalf("current checkpoint attributed to creator: %+v %v", cp, err)
	}
	transactions, err := st.ListWorkspaceCheckpointTransactions(ctx, run.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var matched bool
	for _, txn := range transactions {
		if txn.TriggerReceiptID == created.Edit.ID {
			matched = txn.Status == workspacecheckpoint.TransactionCompleted && txn.AfterCheckpointID == cp.ID
			before, err := st.GetWorkspaceCheckpoint(ctx, txn.BeforeCheckpointID)
			if err != nil || before.RunID != run.ID || before.ParentCheckpointID != original.LastCheckpointID {
				t.Fatalf("cross-epoch parent was not exact physical cursor: %+v %v", before, err)
			}
		}
	}
	if !matched {
		t.Fatal("current Run has no completed edit journal")
	}
	if got := readDrydockTestFile(t, filepath.Join(f.sourceRoot, "tracked.txt")); got != "user source\r\n" {
		t.Fatalf("source changed: %q", got)
	}
	if got := readDrydockTestFile(t, filepath.Join(original.Path, "tracked.txt")); got != "new epoch reviewed change\n" {
		t.Fatalf("physical directory lost new change: %q", got)
	}
	physicalSHA, err := runner.CommandRuntimeWorkspaceRootSHA256(updated.Path)
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA, err := runner.CommandRuntimeWorkspaceRootSHA256(f.sourceRoot)
	if err != nil || physicalSHA == sourceSHA {
		t.Fatal("command roots were conflated")
	}
	if _, found, err := st.GetWorkspaceCheckpointRunState(ctx, run.ID); err != nil || found {
		t.Fatalf("file write polluted source cursor: found=%t err=%v", found, err)
	}
	if _, err := NewRunService(st).Fail(ctx, run.ID, "finish current epoch"); err != nil {
		t.Fatal(err)
	}
	replayed, err := NewFileEditApplyService(other, policy.NewDefaultChecker()).Apply(ctx, applyRequest)
	if err != nil || !replayed.Replayed || replayed.FileWritten {
		t.Fatalf("terminal replay needed current runtime: %+v %v", replayed, err)
	}
	if _, err := proposal.IssueSource(ctx, f.run.ID, "tracked.txt"); apperror.CodeOf(err) != apperror.CodeFailedPrecondition && apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("old write source still available: %v", err)
	}
}
