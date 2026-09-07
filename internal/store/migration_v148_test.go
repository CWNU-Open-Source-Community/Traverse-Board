package store

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV148DefersPreparingThreadPermissionAndMaterializesSuccessor(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v147-preparing-thread-permission.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 147); err != nil {
		t.Fatal(err)
	}
	run, threadRecord := preparingThreadPermissionFixture(t, ctx, state)
	service := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})
	request := application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
		OperationKey: "migration-v148-preparing-before-upgrade-0001",
		RequestedBy:  "test_operator", Reason: "apply Workspace access to the successor",
		ConfirmWorkspaceAccess: true,
	}
	if _, err := service.Change(ctx, request); err == nil {
		t.Fatal("v147 unexpectedly accepted a deferred preparing operation")
	}
	if err := state.applyMigration(ctx, plan[147]); err != nil {
		t.Fatal(err)
	}
	request.OperationKey = "migration-v148-preparing-after-upgrade-0001"
	assertPreparingPermissionDeferredAndMaterialized(t, ctx, state, service,
		run, threadRecord, request)
	if version, err := state.SchemaVersion(ctx); err != nil || version != 148 {
		t.Fatalf("schema version=%d want=148 err=%v", version, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func TestCleanInstallV148DefersPreparingThreadPermission(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "clean-v148-preparing-thread-permission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	run, threadRecord := preparingThreadPermissionFixture(t, ctx, state)
	service := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})
	assertPreparingPermissionDeferredAndMaterialized(t, ctx, state, service,
		run, threadRecord, application.ChangeThreadExecutionPermissionRequest{
			ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
			OperationKey: "clean-v148-preparing-deferred-0001", RequestedBy: "test_operator",
			Reason:                 "apply Workspace access after preparing completes",
			ConfirmWorkspaceAccess: true,
		})
	if version, err := state.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("clean schema version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
}

func preparingThreadPermissionFixture(t *testing.T, ctx context.Context,
	state *SQLiteStore,
) (domain.Run, domain.Thread) {
	t.Helper()
	mission, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "defer permission while preparing",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	run = transitionThreadLifecycleFixtureRun(t, ctx, state, mission, run,
		domain.RunPreparing)
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run, threadRecord
}

func assertPreparingPermissionDeferredAndMaterialized(t *testing.T, ctx context.Context,
	state *SQLiteStore, service *application.ThreadExecutionPermissionService,
	run domain.Run, threadRecord domain.Thread,
	request application.ChangeThreadExecutionPermissionRequest,
) {
	t.Helper()
	before, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := service.Change(ctx, request)
	if err != nil || selected.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred ||
		selected.CurrentRunID != run.ID {
		t.Fatalf("preparing preference was not deferred: result=%+v err=%v", selected, err)
	}
	after, err := state.GetRunExecutionPermission(ctx, run.ID)
	storedRun, runErr := state.GetRun(ctx, run.ID)
	if err != nil || runErr != nil || after.ID != before.ID ||
		after.Revision != before.Revision || storedRun.Status != domain.RunPreparing {
		t.Fatalf("deferred preference changed preparing Run: before=%+v after=%+v run=%+v errors=%v/%v",
			before, after, storedRun, err, runErr)
	}
	cancelled, err := application.NewRunService(state).Cancel(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue with deferred Workspace access",
			OperationKey: "preparing-deferred-successor-" + run.ID,
			RequestedBy:  "test_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, successor.Run.ID)
	if err != nil || cancelled.Status != domain.RunCancelled || !successor.SuccessorCreated ||
		permission.Mode != domain.RunExecutionPermissionWorkspaceAccess {
		t.Fatalf("successor did not materialize deferred permission: cancelled=%+v successor=%+v permission=%+v err=%v",
			cancelled, successor, permission, err)
	}
}
