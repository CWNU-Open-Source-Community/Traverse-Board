package store

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV146DefersRunningThreadPermissionAndMaterializesSuccessor(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v145-deferred-thread-permission.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 145); err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal:    "defer a permission preference until the successor Run",
		Profile: "code", Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := state.applyMigration(ctx, plan[145]); err != nil {
		t.Fatal(err)
	}

	permissions := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})
	selected, err := permissions.Change(ctx,
		application.ChangeThreadExecutionPermissionRequest{
			ThreadID:               threadRecord.ID,
			Mode:                   string(domain.RunExecutionPermissionWorkspaceAccess),
			OperationKey:           "migration-v146-deferred-thread-permission-0001",
			RequestedBy:            "test_operator",
			Reason:                 "apply bounded Workspace Access to the next Run",
			ConfirmWorkspaceAccess: true,
		})
	if err != nil || selected.CurrentRunID != run.ID ||
		selected.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred {
		t.Fatalf("deferred selection=%+v err=%v", selected, err)
	}
	currentPermission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || currentPermission.Mode != domain.RunExecutionPermissionConservative ||
		currentPermission.Revision != 1 {
		t.Fatalf("current Run permission changed: %+v err=%v", currentPermission, err)
	}
	currentRun, err := state.GetRun(ctx, run.ID)
	if err != nil || currentRun.Status != domain.RunRunning {
		t.Fatalf("current Run lifecycle changed: %+v err=%v", currentRun, err)
	}

	if _, err := runs.Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue with the deferred permission preference",
			OperationKey: "migration-v146-successor-message-0001",
			RequestedBy:  "test_operator",
		})
	if err != nil || !continued.SuccessorCreated {
		t.Fatalf("successor=%+v err=%v", continued, err)
	}
	successorPermission, err := state.GetRunExecutionPermission(ctx, continued.Run.ID)
	if err != nil || successorPermission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		successorPermission.Revision != 2 || successorPermission.ProcessEnabled ||
		successorPermission.ExecutionAuthorized || successorPermission.CapabilityGrant {
		t.Fatalf("successor did not materialize deferred preference: %+v err=%v",
			successorPermission, err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 146 {
		t.Fatalf("schema version=%d want=146 err=%v", version, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}
