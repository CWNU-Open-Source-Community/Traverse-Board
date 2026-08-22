package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

// Historical downgrade fixtures only need migration v126 to be pending. The
// older fixtures either rebuild or remove the permission table themselves.
func removeSchemaV126ForTestStatements() []string {
	return append(removeSchemaV127ForTestStatements(),
		`DELETE FROM schema_migrations WHERE version = 126`)
}

func TestSchemaV126PreservesHistoricalModesAndAddsWorkspaceAccess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace-access-v125.db")
	legacy := openSchemaV125Store(t, path)
	_, run, err := application.NewRunService(legacy).Create(ctx,
		application.CreateRunRequest{Goal: "preserve a historical permission",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	legacyCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
	}
	legacyRequest := application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
		OperationKey: "workspace-access-legacy-full-0001", RequestedBy: "test_operator",
		Reason: "persist historical full access", ConfirmDangerFullAccess: true,
	}
	legacyResult, err := application.NewRunExecutionPermissionService(
		legacy, legacyCapabilities).Change(ctx, legacyRequest)
	if err != nil {
		t.Fatal(err)
	}
	if legacyResult.Permission.Revision != 2 {
		t.Fatalf("legacy revision=%d", legacyResult.Permission.Revision)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	historical, err := upgraded.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || historical.Mode != domain.RunExecutionPermissionFullAccess ||
		historical.Revision != 2 {
		t.Fatalf("historical mode changed during migration: %+v err=%v", historical, err)
	}
	replayed, err := application.NewRunExecutionPermissionService(
		upgraded, legacyCapabilities).Change(ctx, legacyRequest)
	if err != nil || !replayed.Replayed || replayed.Permission.ID != historical.ID {
		t.Fatalf("legacy operation replay was not preserved: %+v err=%v", replayed, err)
	}

	workspaceCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: true,
	}
	workspaceRequest := application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
		OperationKey: "workspace-access-select-0001", RequestedBy: "test_operator",
		Reason: "select bounded Workspace execution", ConfirmWorkspaceAccess: true,
	}
	selected, err := application.NewRunExecutionPermissionService(
		upgraded, workspaceCapabilities).Change(ctx, workspaceRequest)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Permission.Revision != 3 ||
		selected.Permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		selected.Permission.ProcessEnabled || selected.Permission.ExecutionAuthorized ||
		selected.Permission.CapabilityGrant {
		t.Fatalf("Workspace Access snapshot widened authority: %+v", selected.Permission)
	}
	if replay, err := application.NewRunExecutionPermissionService(
		upgraded, workspaceCapabilities).Change(ctx, workspaceRequest); err != nil ||
		!replay.Replayed || replay.Permission.ID != selected.Permission.ID {
		t.Fatalf("Workspace Access replay=%+v err=%v", replay, err)
	}

	reset, err := application.NewRunExecutionPermissionService(
		upgraded, domain.ExecutionPermissionRuntimeCapabilities{}).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunExecutionPermissionConservative),
			OperationKey: "workspace-access-reset-0001", RequestedBy: "test_operator",
			Reason: "return to conservative boundary",
		})
	if err != nil || reset.Permission.Revision != 4 ||
		reset.Permission.Mode != domain.RunExecutionPermissionConservative {
		t.Fatalf("Workspace Access downgrade=%+v err=%v", reset, err)
	}

	if _, err := upgraded.db.ExecContext(ctx, `INSERT INTO run_execution_permission_snapshots
		(id, run_id, mission_id, revision, protocol_version, mode, approval_policy,
		command_scope, filesystem_scope, network_scope, persistent_terminal,
		background_process, agent_terminal_input, risk_tier, required_gate,
		policy_version, operator_confirmed, process_enabled, execution_authorized,
		capability_grant, requested_by, reason, created_at)
		SELECT id || '-invalid', run_id, mission_id, revision + 1, protocol_version,
			'workspace_access', approval_policy, command_scope, filesystem_scope,
			network_scope, persistent_terminal, background_process, agent_terminal_input,
			risk_tier, required_gate, policy_version, operator_confirmed,
			process_enabled, execution_authorized, capability_grant,
			requested_by, reason, created_at
		FROM run_execution_permission_snapshots WHERE id = ?`, reset.Permission.ID); err == nil {
		t.Fatal("SQLite accepted a workspace_access row with conservative controls")
	}
	assertNoForeignKeyViolations(t, upgraded.db)
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if current, err := reopened.GetRunExecutionPermission(ctx, run.ID); err != nil ||
		current.Mode != domain.RunExecutionPermissionConservative || current.Revision != 4 {
		t.Fatalf("reopen changed permission history: %+v err=%v", current, err)
	}
}

func openSchemaV125Store(t testing.TB, path string) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		t.Fatal(err)
	}
	state := &SQLiteStore{db: db, home: filepath.Dir(path)}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	);`); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrationPlan()[:125] {
		if err := state.applyMigration(context.Background(), item); err != nil {
			_ = state.Close()
			t.Fatalf("apply schema v125 fixture migration %d: %v", item.Version, err)
		}
	}
	return state
}

func assertNoForeignKeyViolations(t testing.TB, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check;`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("foreign-key violation table=%s row=%v parent=%s key=%d",
			table, rowID, parent, foreignKeyID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceAccessPermissionChangeRevokesActiveLease(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "workspace-access-lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	runs := application.NewRunService(state)
	_, created, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "revoke stale execution authority", Profile: "code",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := runs.Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: running.ID,
			OwnerID: "workspace-access-old-owner", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Pause(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	selected, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true}).Change(
		ctx, application.ChangeRunExecutionPermissionRequest{
			RunID: running.ID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
			OperationKey: "workspace-access-revoke-lease-0001",
			RequestedBy:  "test_operator", Reason: "invalidate old execution owner",
			ConfirmWorkspaceAccess: true,
		})
	if err != nil {
		t.Fatal(err)
	}
	current, found, err := state.GetRunExecutionLease(ctx, running.ID)
	if err != nil || !found || current.Status != domain.RunExecutionLeaseReleased ||
		current.LeaseID != lease.Lease.LeaseID || current.Generation != lease.Lease.Generation ||
		selected.Permission.Revision != 2 {
		t.Fatalf("stale lease survived revision change: lease=%+v selected=%+v err=%v",
			current, selected, err)
	}
	if _, err := state.RenewRunExecutionLease(ctx, lease.Lease, time.Minute); err == nil ||
		!strings.Contains(err.Error(), "lost or expired") {
		t.Fatalf("old lease renewed after permission drift: %v", err)
	}
}
