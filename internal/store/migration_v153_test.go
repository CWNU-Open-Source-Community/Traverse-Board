package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/repository"
)

func removeSchemaV153ForTestStatements() []string {
	return append(removeSchemaV154ForTestStatements(), []string{`DROP TRIGGER trg_file_edit_apply_operation_insert`,
		requireMigrationTrigger("trg_file_edit_apply_operation_insert", agentCodeToolStatements),
		`DELETE FROM schema_migrations WHERE version = 153`}...)
}

func TestSchemaV153PreservesSourceApplyHistoryAndMigrationChecksums(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "v152-file-history.db")
	state := openUnmigratedSQLiteStore(t, path)
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 152); err != nil {
		t.Fatal(err)
	}
	restoreLegacyInputs := addCurrentInputColumnsForLegacySeed(t, state)
	run, source := newV153SourceRun(t, state, "upgrade")
	edit := v153Propose(t, state, run, source.ID, source.RootPath, "historical", time.Now().UTC())
	reviewed, err := application.NewFileEditReviewService(state).Review(ctx, application.ReviewFileEditRequest{
		Version: application.FileEditReviewProtocolVersion, RunID: run.ID, EditID: edit.ID, Action: application.FileEditApproveIntent})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := application.NewFileEditApplyService(state, policy.NewDefaultChecker(), nil).Apply(ctx,
		application.ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion, RunID: run.ID, EditID: reviewed.Edit.ID,
			OperationKey: "v152-historical-source-apply", AppliedBy: "test_operator"})
	if err != nil || !applied.FileWritten {
		t.Fatalf("legacy source apply: %+v %v", applied, err)
	}
	ledgerBefore, err := state.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restoreLegacyInputs()
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	ledgerAfter, err := upgraded.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for version, previous := range ledgerBefore {
		if !reflect.DeepEqual(ledgerAfter[version], previous) {
			t.Fatalf("migration %d ledger was rewritten", version)
		}
	}
	if len(ledgerBefore) != 152 || len(ledgerAfter) != LatestSchemaVersion {
		t.Fatalf("ledger sizes %d -> %d", len(ledgerBefore), len(ledgerAfter))
	}
	stored, err := upgraded.GetFileEdit(ctx, edit.ID)
	if err != nil || !reflect.DeepEqual(stored, applied.Edit) {
		t.Fatalf("source edit changed: %+v %v", stored, err)
	}
	operation, result, found, err := upgraded.GetFileEditApplyOperation(ctx, applied.Operation.KeyDigest)
	if err != nil || !found || !reflect.DeepEqual(operation, applied.Operation) || result == nil || !reflect.DeepEqual(*result, applied.Result) {
		t.Fatalf("source Apply journal changed: %+v %+v %t %v", operation, result, found, err)
	}
	replay, err := application.NewFileEditApplyService(upgraded, policy.NewDefaultChecker(), nil).Apply(ctx,
		application.ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion, RunID: run.ID, EditID: edit.ID,
			OperationKey: "v152-historical-source-apply", AppliedBy: "test_operator"})
	if err != nil || !replay.Replayed || replay.Operation != applied.Operation {
		t.Fatalf("historical replay: %+v %v", replay, err)
	}
	// The new guard also preserves ordinary source-only application.
	next := v153Propose(t, upgraded, run, source.ID, source.RootPath, "ordinary-after-upgrade", time.Now().UTC())
	if _, err := application.NewFileEditReviewService(upgraded).Review(ctx, application.ReviewFileEditRequest{
		Version: application.FileEditReviewProtocolVersion, RunID: run.ID, EditID: next.ID, Action: application.FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	if current, err := application.NewFileEditApplyService(upgraded, policy.NewDefaultChecker(), nil).Apply(ctx,
		application.ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion, RunID: run.ID, EditID: next.ID,
			OperationKey: "v153-ordinary-source-apply", AppliedBy: "test_operator"}); err != nil || !current.FileWritten {
		t.Fatalf("ordinary source apply after migration: %+v %v", current, err)
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}

func TestSchemaV153ApplyGuardRequiresExactAvailableDrydock(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "v153-apply-scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	run, source := newV153SourceRun(t, state, "apply-scope")
	legacy := v153Approved(t, state, run, v153Propose(t, state, run, source.ID, source.RootPath, "source-pending", time.Now().UTC()), nil)
	service, owned := v153CreateDrydock(t, state, run)
	target := v153Approved(t, state, run, v153Propose(t, state, run, owned.WorkspaceID, owned.Path, "target-pending", time.Now().UTC()), service)
	_, sibling, err := application.NewRunService(state).Create(ctx, application.CreateRunRequest{Goal: "same source sibling", Profile: "code", WorkspaceID: source.ID})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err = application.NewRunService(state).Start(ctx, sibling.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		edit   fileedit.Edit
		change func(*fileedit.ApplyOperation)
	}{
		{name: "source fallback", edit: legacy},
		{name: "wrong Run", edit: target, change: func(op *fileedit.ApplyOperation) { op.RunID = sibling.ID }},
		{name: "wrong source", edit: target, change: func(op *fileedit.ApplyOperation) { op.WorkspaceID = source.ID }},
		{name: "missing target", edit: target, change: func(op *fileedit.ApplyOperation) { op.WorkspaceID = "workspace-missing" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := v153ApplyOperation(run, test.edit, test.name)
			if test.change != nil {
				test.change(&op)
			}
			if _, _, _, err := state.PrepareFileEditApply(ctx, op); err == nil {
				t.Fatal("invalid target admitted")
			}
			if _, _, found, err := state.GetFileEditApplyOperation(ctx, op.KeyDigest); err != nil || found {
				t.Fatalf("rejection left a journal: %t %v", found, err)
			}
		})
	}
	// Direct database lifecycle fixture: preserve all immutable ownership fields,
	// advance its generation, and test the insertion trigger independently of the
	// application resolver. No command or file write is represented here.
	if _, err := state.db.ExecContext(ctx, `UPDATE drydock_workspaces SET state='recovery_required',
		generation=generation+1,recovery_reason='test_not_ready' WHERE id=?`, owned.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := state.PrepareFileEditApply(ctx, v153ApplyOperation(run, target, "not ready")); err == nil {
		t.Fatal("unready target admitted")
	}
	if belongs, err := state.FileEditWorkspaceBelongsToRun(ctx, run.ID, target.SessionID, target.WorkspaceID); err != nil || !belongs {
		t.Fatalf("unready historical ownership disappeared: %t %v", belongs, err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE drydock_workspaces SET state='ready',
		generation=generation+1,recovery_reason='' WHERE id=?`, owned.ID); err != nil {
		t.Fatal(err)
	}
	op := v153ApplyOperation(run, target, "exact target")
	stored, _, replayed, err := state.PrepareFileEditApply(ctx, op)
	if err != nil || replayed || stored.WorkspaceID != owned.WorkspaceID || stored.EventSequence <= 0 {
		t.Fatalf("exact target: %+v %t %v", stored, replayed, err)
	}
	if _, _, replayed, err := state.PrepareFileEditApply(ctx, op); err != nil || !replayed {
		t.Fatalf("exact replay: %t %v", replayed, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func TestSchemaV153RunFilePageFiltersOwnershipBeforePagination(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "v153-pages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	run, source := newV153SourceRun(t, state, "pages")
	at := time.Now().UTC()
	older := v153Propose(t, state, run, source.ID, source.RootPath, "source-history", at)
	_, owned := v153CreateDrydock(t, state, run)
	newer := v153Propose(t, state, run, owned.WorkspaceID, owned.Path, "owned-target", at.Add(time.Second))
	_, sibling, err := application.NewRunService(state).Create(ctx, application.CreateRunRequest{Goal: "newer unrelated records", Profile: "code", WorkspaceID: source.ID})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err = application.NewRunService(state).Start(ctx, sibling.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		v153Propose(t, state, sibling, source.ID, source.RootPath, fmt.Sprintf("unrelated-%d", index), at.Add(time.Duration(index+2)*time.Second))
	}
	for offset, want := range []fileedit.Edit{newer, older} {
		page, err := state.ListRunFileEditPreviewsPage(ctx, run.ID, offset, 1)
		if err != nil || len(page) != 1 || page[0].ID != want.ID || page[0].WorkspaceID != want.WorkspaceID {
			t.Fatalf("offset %d page=%+v %v", offset, page, err)
		}
	}
	if page, err := state.ListRunFileEditPreviewsPage(ctx, run.ID, 2, 1); err != nil || len(page) != 0 {
		t.Fatalf("unrelated rows leaked: %+v %v", page, err)
	}
	if belongs, err := state.FileEditWorkspaceBelongsToRun(ctx, sibling.ID, run.SessionID, owned.WorkspaceID); err != nil || belongs {
		t.Fatalf("sibling claimed Drydock: %t %v", belongs, err)
	}
	if page, err := state.ListRunFileEditPreviewsPage(ctx, sibling.ID, 0, 10); err != nil || len(page) != 5 {
		t.Fatalf("sibling page=%+v %v", page, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func newV153SourceRun(t *testing.T, state *SQLiteStore, suffix string) (domain.Run, WorkspaceRecord) {
	t.Helper()
	root := newWorkspaceCheckpointGitRepository(t)
	workspace := WorkspaceRecord{ID: "workspace-v153-" + suffix, Name: "v153-" + suffix, RootPath: root}
	if err := state.SaveWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(t.Context(), application.CreateRunRequest{Goal: "v153 " + suffix, Profile: "code", WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	run, err = application.NewRunService(state).Start(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run, workspace
}

func v153CreateDrydock(t *testing.T, state *SQLiteStore, run domain.Run) (*application.DrydockService, drydock.Workspace) {
	t.Helper()
	executor, err := repository.NewDrydockExecutor(filepath.Join(t.TempDir(), "managed"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewDrydockService(state, executor)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Create(t.Context(), application.DrydockCreateRequest{RunID: run.ID, OperationKey: "v153-preview", RequestedBy: "test_operator"})
	if err != nil || !preview.TrustRequired {
		t.Fatalf("Drydock preview: %+v %v", preview, err)
	}
	created, err := service.Create(t.Context(), application.DrydockCreateRequest{RunID: run.ID, OperationKey: "v153-create", RequestedBy: "test_operator",
		ConfirmWorkspaceTrust: true, ExpectedTrustDigest: preview.TrustDigest})
	if err != nil || created.Workspace == nil {
		t.Fatalf("Drydock create: %+v %v", created, err)
	}
	return service, *created.Workspace
}

func v153Propose(t *testing.T, state *SQLiteStore, run domain.Run, workspaceID, root, value string, at time.Time) fileedit.Edit {
	t.Helper()
	edit, err := fileedit.NewManager(state).PrepareProposal(t.Context(), fileedit.Proposal{SessionID: run.SessionID,
		WorkspaceID: workspaceID, WorkspaceRoot: root, Path: "file.txt", ProposedText: value + "\n"})
	if err != nil {
		t.Fatal(err)
	}
	edit.CreatedAt, edit.UpdatedAt = at, at
	edit, err = state.SaveFileEdit(t.Context(), edit)
	if err != nil {
		t.Fatal(err)
	}
	return edit
}

func v153Approved(t *testing.T, state *SQLiteStore, run domain.Run, edit fileedit.Edit, drydocks *application.DrydockService) fileedit.Edit {
	t.Helper()
	updated, err := application.NewFileEditReviewService(state).WithDrydock(drydocks).Review(t.Context(),
		application.ReviewFileEditRequest{Version: application.FileEditReviewProtocolVersion, RunID: run.ID,
			EditID: edit.ID, Action: application.FileEditApproveIntent})
	if err != nil {
		t.Fatal(err)
	}
	return updated.Edit
}

func v153ApplyOperation(run domain.Run, edit fileedit.Edit, key string) fileedit.ApplyOperation {
	return fileedit.ApplyOperation{ProtocolVersion: fileedit.FileEditApplyProtocolVersion, KeyDigest: fileedit.HashText(key),
		RequestFingerprint: fileedit.HashText("request-" + key), RunID: run.ID, SessionID: run.SessionID, WorkspaceID: edit.WorkspaceID,
		EditID: edit.ID, Operation: edit.Operation, Path: edit.Path, OriginalHash: edit.OriginalHash, ProposedHash: edit.ProposedHash,
		ObservedHash: edit.OriginalHash, AppliedBy: "test_operator", CreatedAt: time.Now().UTC()}
}
