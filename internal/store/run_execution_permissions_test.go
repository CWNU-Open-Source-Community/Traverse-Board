package store

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

func TestRunExecutionPermissionIsImmutableIdempotentAndRuntimeGated(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "run-execution-permission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	runs := application.NewRunService(st)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "select an execution permission mode", Profile: "code",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	closed := application.NewRunExecutionPermissionService(st,
		domain.ExecutionPermissionRuntimeCapabilities{})
	initial, err := closed.Current(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Mode != domain.RunExecutionPermissionConservative ||
		initial.Revision != 1 || initial.ProcessEnabled ||
		initial.ExecutionAuthorized || initial.CapabilityGrant {
		t.Fatalf("unexpected initial permission: %+v", initial)
	}
	request := application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
		OperationKey: "permission-operation-0001", RequestedBy: "test_operator",
		Reason: "test full access", ConfirmDangerFullAccess: true,
	}
	if _, err := closed.Change(ctx, request); apperror.CodeOf(err) != apperror.CodePolicyDenied {
		t.Fatalf("persisted selection bypassed runtime gate: %v", err)
	}
	open := application.NewRunExecutionPermissionService(st,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		})
	selected, err := open.Change(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Replayed || selected.Permission.Revision != 2 ||
		selected.Permission.Mode != domain.RunExecutionPermissionFullAccess ||
		selected.Permission.ProcessEnabled || selected.Permission.ExecutionAuthorized ||
		selected.Permission.CapabilityGrant {
		t.Fatalf("unexpected selected permission: %+v", selected)
	}
	replayed, err := open.Change(ctx, request)
	if err != nil || !replayed.Replayed ||
		replayed.Permission.ID != selected.Permission.ID {
		t.Fatalf("permission replay changed result: %+v err=%v", replayed, err)
	}
	request.Mode = string(domain.RunExecutionPermissionApproval)
	request.ConfirmDangerFullAccess = false
	request.ConfirmUserApproval = true
	if _, err := open.Change(ctx, request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("reused operation key error=%v", err)
	}
	for _, statement := range []string{
		`UPDATE run_execution_permission_snapshots SET process_enabled = 1 WHERE id = ?`,
		`DELETE FROM run_execution_permission_snapshots WHERE id = ?`,
	} {
		if _, err := st.db.ExecContext(ctx, statement, selected.Permission.ID); err == nil {
			t.Fatalf("immutable permission statement succeeded: %s", statement)
		}
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil || len(eventList) == 0 ||
		eventList[len(eventList)-1].Type != events.RunExecutionPermissionSelectedEvent {
		t.Fatalf("permission event missing: events=%#v err=%v", eventList, err)
	}
}

func TestSchemaV88BackfillsConservativeExecutionPermission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v87-execution-permission.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, run, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{
		Goal: "legacy v87 Run", Profile: "review",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV88ForTestStatements() {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("downgrade v88 fixture with %q: %v", statement, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	permission, err := upgraded.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if permission.Mode != domain.RunExecutionPermissionConservative ||
		permission.Revision != 1 || permission.RequestedBy != "schema_v88" ||
		permission.ProcessEnabled || permission.ExecutionAuthorized ||
		permission.CapabilityGrant {
		t.Fatalf("unexpected v88 compatibility permission: %+v", permission)
	}
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
}

func removeSchemaV88ForTestStatements() []string {
	return []string{
		`DROP TRIGGER trg_run_execution_permission_operation_delete_immutable`,
		`DROP TRIGGER trg_run_execution_permission_operation_update_immutable`,
		`DROP TRIGGER trg_run_execution_permission_snapshot_delete_immutable`,
		`DROP TRIGGER trg_run_execution_permission_snapshot_update_immutable`,
		`DROP TRIGGER trg_run_execution_permission_operation_insert`,
		`DROP TRIGGER trg_run_execution_permission_snapshot_insert`,
		`DROP TABLE run_execution_permission_operations`,
		`DROP INDEX idx_run_execution_permission_snapshots_run_revision`,
		`DROP TABLE run_execution_permission_snapshots`,
		`DELETE FROM schema_migrations WHERE version = 88`,
	}
}
