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

func TestRunBrowserCDPPermissionIsImmutableIdempotentAndDebugGated(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "run-browser-cdp-permission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, run, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "select a browser CDP permission mode", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true,
	}
	service := application.NewRunBrowserCDPPermissionService(st, capabilities)
	initial, err := service.Current(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Mode != domain.RunBrowserCDPPermissionRestricted || initial.Revision != 1 ||
		initial.TransportEnabled || initial.BrowserStartAuthorized ||
		initial.RuntimeAuthorized || initial.CapabilityGrant {
		t.Fatalf("unexpected initial browser CDP permission: %+v", initial)
	}
	request := application.ChangeRunBrowserCDPPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunBrowserCDPPermissionFullDebug),
		OperationKey: "browser-cdp-permission-operation-0001",
		RequestedBy:  "test_operator", Reason: "test complete debug CDP",
		ConfirmFullCDPDebug: true,
	}
	if _, err := service.Change(ctx, request); apperror.CodeOf(err) != apperror.CodePolicyDenied {
		t.Fatalf("full CDP bypassed execution Debug gate: %v", err)
	}
	executionPermissions := application.NewRunExecutionPermissionService(st,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			DebugMaximumAccessEnabled: true,
		})
	if _, err := executionPermissions.Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunExecutionPermissionDebug),
			OperationKey: "browser-cdp-debug-execution-permission-0001",
			RequestedBy:  "test_operator", Reason: "prepare exact Debug boundary",
			ConfirmDebugAccess: true,
		}); err != nil {
		t.Fatal(err)
	}
	selected, err := service.Change(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Replayed || selected.Permission.Revision != 2 ||
		selected.Permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		selected.Permission.TransportEnabled || selected.Permission.BrowserStartAuthorized ||
		selected.Permission.RuntimeAuthorized || selected.Permission.CapabilityGrant {
		t.Fatalf("unexpected selected browser CDP permission: %+v", selected)
	}
	replayed, err := service.Change(ctx, request)
	if err != nil || !replayed.Replayed ||
		replayed.Permission.ID != selected.Permission.ID {
		t.Fatalf("browser CDP replay changed result: %+v err=%v", replayed, err)
	}
	request.Mode = string(domain.RunBrowserCDPPermissionRestricted)
	request.ConfirmFullCDPDebug = false
	if _, err := service.Change(ctx, request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("reused browser CDP operation key error=%v", err)
	}
	for _, statement := range []string{
		`UPDATE run_browser_cdp_permission_snapshots SET transport_enabled = 1 WHERE id = ?`,
		`DELETE FROM run_browser_cdp_permission_snapshots WHERE id = ?`,
	} {
		if _, err := st.db.ExecContext(ctx, statement, selected.Permission.ID); err == nil {
			t.Fatalf("immutable browser CDP permission statement succeeded: %s", statement)
		}
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil || len(eventList) == 0 ||
		eventList[len(eventList)-1].Type != events.RunBrowserCDPPermissionSelectedEvent {
		t.Fatalf("browser CDP permission event missing: events=%#v err=%v", eventList, err)
	}
}

func TestSchemaV91BackfillsRestrictedBrowserCDPPermission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v90-browser-cdp-permission.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, run, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{
		Goal: "legacy v90 Run", Profile: "review",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV91ForTestStatements() {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("downgrade v91 fixture with %q: %v", statement, err)
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
	permission, err := upgraded.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if permission.Mode != domain.RunBrowserCDPPermissionRestricted ||
		permission.Revision != 1 || permission.RequestedBy != "schema_v91" ||
		permission.TransportEnabled || permission.BrowserStartAuthorized ||
		permission.RuntimeAuthorized || permission.CapabilityGrant {
		t.Fatalf("unexpected v91 compatibility browser CDP permission: %+v", permission)
	}
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
}

func removeSchemaV91ForTestStatements() []string {
	return append(removeSchemaV92ForTestStatements(), []string{
		`DROP TRIGGER trg_run_browser_cdp_permission_operation_delete_immutable`,
		`DROP TRIGGER trg_run_browser_cdp_permission_operation_update_immutable`,
		`DROP TRIGGER trg_run_browser_cdp_permission_snapshot_delete_immutable`,
		`DROP TRIGGER trg_run_browser_cdp_permission_snapshot_update_immutable`,
		`DROP TRIGGER trg_run_browser_cdp_permission_operation_insert`,
		`DROP TRIGGER trg_run_browser_cdp_permission_snapshot_insert`,
		`DROP TABLE run_browser_cdp_permission_operations`,
		`DROP INDEX idx_run_browser_cdp_permission_snapshots_run_revision`,
		`DROP TABLE run_browser_cdp_permission_snapshots`,
		`DELETE FROM schema_migrations WHERE version = 91`,
	}...)
}
