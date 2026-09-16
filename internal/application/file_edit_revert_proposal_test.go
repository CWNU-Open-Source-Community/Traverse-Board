package application_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

type revertProposalFixture struct {
	state          *store.SQLiteStore
	database, root string
	run            domain.Run
	source         fileedit.Edit
}

func newRevertProposalFixture(t *testing.T, operation string) revertProposalFixture {
	t.Helper()
	root := t.TempDir()
	database := filepath.Join(t.TempDir(), "revert.db")
	state, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	if err := state.SaveWorkspace(t.Context(), store.WorkspaceRecord{ID: "workspace-revert",
		Name: "revert", RootPath: root}); err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(state).Create(t.Context(), application.CreateRunRequest{
		Goal: "review file changes", Profile: "code", WorkspaceID: "workspace-revert",
		Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	original, proposed, expected := "before\n", "after\n", fileedit.HashText("before\n")
	if operation == fileedit.OperationCreate {
		original, expected = "", "missing"
	}
	if operation == fileedit.OperationDelete {
		proposed = ""
	}
	if operation != fileedit.OperationCreate {
		if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source, err := fileedit.NewManager(state).Propose(t.Context(), fileedit.Proposal{
		SessionID: run.SessionID, WorkspaceID: "workspace-revert", WorkspaceRoot: root,
		Path: "target.txt", Operation: operation, ProposedText: proposed, ExpectedOriginalHash: expected})
	if err != nil {
		t.Fatal(err)
	}
	approveRevertFixture(t, state, run.ID, source.ID)
	applied, err := application.NewFileEditApplyService(state, policy.NewDefaultChecker()).Apply(t.Context(),
		application.ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion,
			RunID: run.ID, EditID: source.ID, OperationKey: "source-apply-operation-0001", AppliedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	return revertProposalFixture{state: state, database: database, root: root, run: run, source: applied.Edit}
}

func approveRevertFixture(t *testing.T, state *store.SQLiteStore, runID, editID string) {
	t.Helper()
	_, err := application.NewFileEditReviewService(state).Review(t.Context(), application.ReviewFileEditRequest{
		Version: application.FileEditReviewProtocolVersion, RunID: runID, EditID: editID,
		Action: application.FileEditApproveIntent})
	if err != nil {
		t.Fatal(err)
	}
}

func (f revertProposalFixture) request(key string) application.CreateFileEditRevertProposalRequest {
	return application.CreateFileEditRevertProposalRequest{Version: application.FileEditProposalProtocolVersion,
		RunID: f.run.ID, SourceEditID: f.source.ID, OperationKey: key}
}

func TestFileEditRevertProposalRoundTripPreservesOtherFilesAndReplaysAfterRestart(t *testing.T) {
	for _, operation := range []string{fileedit.OperationReplace, fileedit.OperationCreate, fileedit.OperationDelete} {
		t.Run(operation, func(t *testing.T) {
			f := newRevertProposalFixture(t, operation)
			if err := os.WriteFile(filepath.Join(f.root, "user.txt"), []byte("external change\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			service := application.NewFileEditProposalService(f.state, policy.NewDefaultChecker())
			request := f.request("inverse-proposal-operation-0001")
			result, err := service.ProposeRevert(t.Context(), request)
			if err != nil || result.Replayed || result.Edit.Status != fileedit.StatusProposed {
				t.Fatalf("proposal=%#v err=%v", result, err)
			}
			if actual, err := fileedit.CurrentHash(f.root, "target.txt"); err != nil || actual != f.source.ProposedHash {
				t.Fatalf("proposal changed file: %s %v", actual, err)
			}
			approveRevertFixture(t, f.state, f.run.ID, result.Edit.ID)
			applyRequest := application.ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion,
				RunID: f.run.ID, EditID: result.Edit.ID, OperationKey: "inverse-apply-operation-0001", AppliedBy: "test_operator"}
			applied, err := application.NewFileEditApplyService(f.state, policy.NewDefaultChecker()).Apply(t.Context(), applyRequest)
			if err != nil || applied.Result.Status != fileedit.ApplyCompleted {
				t.Fatalf("apply=%#v err=%v", applied, err)
			}
			if actual, err := fileedit.CurrentHash(f.root, "target.txt"); err != nil || actual != f.source.OriginalHash {
				t.Fatalf("inverse hash: %s %v", actual, err)
			}
			if user, err := os.ReadFile(filepath.Join(f.root, "user.txt")); err != nil || string(user) != "external change\n" {
				t.Fatalf("external file lost: %q %v", user, err)
			}
			unchanged, err := f.state.GetFileEdit(t.Context(), f.source.ID)
			if err != nil || unchanged != f.source {
				t.Fatalf("source receipt changed: %#v %v", unchanged, err)
			}
			if _, err := application.NewRunService(f.state).Cancel(t.Context(), f.run.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.state.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(f.database)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			// A replay must not read a newly changed file or reset the applied edit.
			if err := os.WriteFile(filepath.Join(f.root, "target.txt"), []byte("later user edit\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			replay, err := application.NewFileEditProposalService(reopened, policy.NewDefaultChecker()).ProposeRevert(t.Context(), request)
			if err != nil || !replay.Replayed || replay.Edit.ID != result.Edit.ID || replay.Edit.Status != fileedit.StatusApplied {
				t.Fatalf("replay=%#v err=%v", replay, err)
			}
			applyReplay, err := application.NewFileEditApplyService(reopened, policy.NewDefaultChecker()).Apply(t.Context(), applyRequest)
			if err != nil || !applyReplay.Replayed || applyReplay.FileWritten {
				t.Fatalf("terminal apply replay=%#v err=%v", applyReplay, err)
			}
		})
	}
}

func TestFileEditRevertProposalExplicitSourceHandlesCreateAndDelete(t *testing.T) {
	for _, operation := range []string{fileedit.OperationCreate, fileedit.OperationDelete} {
		t.Run(operation, func(t *testing.T) {
			f := newRevertProposalFixture(t, operation)
			request := f.request("explicit-inverse-exact-source")
			request.SourceRunID, request.Path, request.ExpectedSHA256 = f.run.ID, f.source.Path, f.source.ProposedHash
			result, err := application.NewFileEditProposalService(f.state, policy.NewDefaultChecker()).ProposeRevert(t.Context(), request)
			if err != nil || result.Edit.Status != fileedit.StatusProposed ||
				result.Edit.OriginalHash != f.source.ProposedHash || result.Edit.ProposedHash != f.source.OriginalHash {
				t.Fatalf("explicit source inverse=%+v err=%v", result, err)
			}
			want := fileedit.OperationDelete
			if operation == fileedit.OperationDelete {
				want = fileedit.OperationCreate
			}
			if result.Edit.Operation != want {
				t.Fatalf("inverse operation=%s want=%s", result.Edit.Operation, want)
			}
			if actual, err := fileedit.CurrentHash(f.root, f.source.Path); err != nil || actual != f.source.ProposedHash {
				t.Fatalf("proposing changed the file: %s %v", actual, err)
			}
		})
	}
}

func TestFileEditRevertProposalConcurrentStoresNeverResetDecision(t *testing.T) {
	f := newRevertProposalFixture(t, fileedit.OperationReplace)
	second, err := store.Open(f.database)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	services := []*application.FileEditProposalService{
		application.NewFileEditProposalService(f.state, policy.NewDefaultChecker()),
		application.NewFileEditProposalService(second, policy.NewDefaultChecker())}
	request := f.request("inverse-concurrent-operation-0001")
	var results [2]application.CreateFileEditProposalResult
	var failures [2]error
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := range services {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			results[i], failures[i] = services[i].ProposeRevert(t.Context(), request)
		}(i)
	}
	close(start)
	wait.Wait()
	if failures[0] != nil || failures[1] != nil || results[0].Edit.ID != results[1].Edit.ID || results[0].Replayed == results[1].Replayed {
		t.Fatalf("concurrent=%#v errors=%v", results, failures)
	}
	approveRevertFixture(t, f.state, f.run.ID, results[0].Edit.ID)
	for _, service := range services {
		replay, err := service.ProposeRevert(t.Context(), request)
		if err != nil || !replay.Replayed || replay.Edit.Status != fileedit.StatusApproved {
			t.Fatalf("review was reset: %#v %v", replay, err)
		}
	}
	// Even a direct insert-only caller cannot replace content at this identity.
	changed := results[0].Edit
	changed.OriginalText = "different source\n"
	changed.OriginalHash = fileedit.HashText(changed.OriginalText)
	if _, _, err := second.CreateFileEditIfAbsent(t.Context(), changed); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatalf("content collision accepted: %v", err)
	}
	stored, err := f.state.GetFileEdit(t.Context(), changed.ID)
	if err != nil || stored.Status != fileedit.StatusApproved || stored.OriginalText != "after\n" {
		t.Fatalf("stored proposal overwritten: %#v %v", stored, err)
	}
}

func TestFileEditRevertProposalDenialAllowsNewAttemptAndRejectsChangedTarget(t *testing.T) {
	f := newRevertProposalFixture(t, fileedit.OperationReplace)
	service := application.NewFileEditProposalService(f.state, policy.NewDefaultChecker())
	request := f.request("inverse-denial-operation-0001")
	first, err := service.ProposeRevert(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewFileEditReviewService(f.state).Review(t.Context(), application.ReviewFileEditRequest{
		Version: application.FileEditReviewProtocolVersion, RunID: f.run.ID, EditID: first.Edit.ID, Action: application.FileEditDeny}); err != nil {
		t.Fatal(err)
	}
	replay, err := service.ProposeRevert(t.Context(), request)
	if err != nil || !replay.Replayed || replay.Edit.Status != fileedit.StatusDenied {
		t.Fatalf("denied replay=%#v %v", replay, err)
	}
	second, err := service.ProposeRevert(t.Context(), f.request("inverse-denial-operation-0002"))
	if err != nil || second.Edit.ID == first.Edit.ID || second.Edit.Status != fileedit.StatusProposed {
		t.Fatalf("new attempt=%#v %v", second, err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "target.txt"), []byte("later edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProposeRevert(t.Context(), f.request("inverse-denial-operation-0003")); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed target accepted: %v", err)
	}
	approveRevertFixture(t, f.state, f.run.ID, second.Edit.ID)
	_, err = application.NewFileEditApplyService(f.state, policy.NewDefaultChecker()).Apply(t.Context(), application.ApplyFileEditRequest{
		Version: fileedit.FileEditApplyProtocolVersion, RunID: f.run.ID, EditID: second.Edit.ID,
		OperationKey: "inverse-stale-apply-operation-0001", AppliedBy: "test_operator"})
	if apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale approved target accepted: %v", err)
	}
}

func TestFileEditRevertProposalRejectsUnrecoverableSourceAndInactiveRun(t *testing.T) {
	for _, mutation := range []string{"redacted", "incomplete", "move", "paused"} {
		t.Run(mutation, func(t *testing.T) {
			f := newRevertProposalFixture(t, fileedit.OperationReplace)
			source := f.source
			switch mutation {
			case "redacted":
				source.SecretsRedacted = true
			case "incomplete":
				source.OriginalText = "truncated"
			case "move":
				source.DestinationPath = "elsewhere.txt"
			case "paused":
				if _, err := application.NewRunService(f.state).Pause(t.Context(), f.run.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mutation != "paused" {
				if _, err := f.state.SaveFileEdit(t.Context(), source); err != nil {
					t.Fatal(err)
				}
			}
			_, err := application.NewFileEditProposalService(f.state, policy.NewDefaultChecker()).ProposeRevert(t.Context(), f.request("inverse-invalid-operation-0001"))
			if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("invalid %s accepted: %v", mutation, err)
			}
		})
	}
}

type wrongRevertRunStore struct {
	*store.SQLiteStore
	run domain.Run
}

type pauseBeforeProposalInsertStore struct {
	*store.SQLiteStore
	runID string
}

func (s pauseBeforeProposalInsertStore) CreateFileEditIfAbsent(ctx context.Context, edit fileedit.Edit) (fileedit.Edit, bool, error) {
	if _, err := application.NewRunService(s.SQLiteStore).Pause(ctx, s.runID); err != nil {
		return fileedit.Edit{}, false, err
	}
	return s.SQLiteStore.CreateFileEditIfAbsent(ctx, edit)
}

func TestFileEditRevertProposalRechecksRunInsideAtomicInsert(t *testing.T) {
	f := newRevertProposalFixture(t, fileedit.OperationReplace)
	service := application.NewFileEditProposalService(pauseBeforeProposalInsertStore{f.state, f.run.ID}, policy.NewDefaultChecker())
	if _, err := service.ProposeRevert(t.Context(), f.request("inverse-state-race-operation-0001")); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("Run changed after preparation but proposal was persisted: %v", err)
	}
	edits, err := f.state.ListFileEdits(t.Context(), fileedit.ListFilter{SessionID: f.run.SessionID})
	if err != nil || len(edits) != 1 || edits[0].ID != f.source.ID {
		t.Fatalf("rejected insertion left a proposal: %#v %v", edits, err)
	}
}

func (s wrongRevertRunStore) GetRun(context.Context, string) (domain.Run, error) { return s.run, nil }

func TestFileEditRevertProposalRequiresSourceApprovalRunIdentity(t *testing.T) {
	f := newRevertProposalFixture(t, fileedit.OperationReplace)
	otherRun := f.run
	otherRun.ID = "different-run-same-session"
	service := application.NewFileEditProposalService(wrongRevertRunStore{f.state, otherRun}, policy.NewDefaultChecker())
	request := f.request("inverse-wrong-run-operation-0001")
	request.RunID = otherRun.ID
	if _, err := service.ProposeRevert(t.Context(), request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("source Run mismatch accepted: %v", err)
	}
}

func TestFileEditApplyManualLeaseRejectsActiveExecutionThenReleasesOwnLease(t *testing.T) {
	f := newRevertProposalFixture(t, fileedit.OperationReplace)
	result, err := application.NewFileEditProposalService(f.state, policy.NewDefaultChecker()).ProposeRevert(t.Context(), f.request("inverse-lease-operation-0001"))
	if err != nil {
		t.Fatal(err)
	}
	approveRevertFixture(t, f.state, f.run.ID, result.Edit.ID)
	held, err := f.state.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{RunID: f.run.ID,
		OwnerID: "model-owner", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	request := application.ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion,
		RunID: f.run.ID, EditID: result.Edit.ID, OperationKey: "inverse-lease-apply-operation-0001", AppliedBy: "test_operator"}
	second, err := store.Open(f.database)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	service := application.NewFileEditApplyService(second, policy.NewDefaultChecker())
	if _, err := service.Apply(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("active model lease bypassed: %v", err)
	}
	if current, err := fileedit.CurrentHash(f.root, "target.txt"); err != nil || current != f.source.ProposedHash {
		t.Fatalf("busy apply changed target: %s %v", current, err)
	}
	if _, _, err := f.state.ReleaseRunExecutionLease(t.Context(), held.Lease); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	lease, found, err := f.state.GetRunExecutionLease(t.Context(), f.run.ID)
	if err != nil || !found || lease.Status != domain.RunExecutionLeaseReleased || lease.Generation <= held.Lease.Generation {
		t.Fatalf("manual lease not released: %#v %v", lease, err)
	}
}
