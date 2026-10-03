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
	_, run, err := newMigrationFixtureRunService(t, legacy).Create(ctx,
		application.CreateRunRequest{Goal: "preserve a historical permission",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	legacyResult, err := selectHistoricalRunPermission(ctx, legacy, run.ID,
		domain.RunExecutionPermissionFullAccess, "workspace-access-legacy-full-0001",
		"persist historical full access")
	if err != nil {
		t.Fatal(err)
	}
	if legacyResult.Permission.Revision != 2 {
		t.Fatalf("legacy revision=%d", legacyResult.Permission.Revision)
	}

	// Exercise the actual migration that introduced Workspace Access while
	// keeping its legacy writer fixtures on their supported schema.
	if err := legacy.applyMigration(ctx, migrationPlan()[125]); err != nil {
		t.Fatal(err)
	}
	if version, err := legacy.SchemaVersion(ctx); err != nil || version != 126 {
		t.Fatalf("schema version=%d want=126 err=%v", version, err)
	}
	historical, err := legacy.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || historical.Mode != domain.RunExecutionPermissionFullAccess ||
		historical.Revision != 2 || historical.ID != legacyResult.Permission.ID {
		t.Fatalf("historical mode changed during migration: %+v err=%v", historical, err)
	}
	replayed, replay, err := legacy.TransitionRunExecutionPermission(ctx,
		legacyResult.Permission, legacyResult.operation, legacyResult.event)
	if err != nil || !replay || replayed.ID != historical.ID {
		t.Fatalf("legacy operation replay was not preserved: %+v replay=%t err=%v", replayed, replay, err)
	}

	selected, err := selectHistoricalRunPermission(ctx, legacy, run.ID,
		domain.RunExecutionPermissionWorkspaceAccess, "workspace-access-select-0001",
		"select bounded Workspace execution")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Permission.Revision != 3 ||
		selected.Permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		selected.Permission.ProcessEnabled || selected.Permission.ExecutionAuthorized ||
		selected.Permission.CapabilityGrant {
		t.Fatalf("Workspace Access snapshot widened authority: %+v", selected.Permission)
	}
	if stored, replay, err := legacy.TransitionRunExecutionPermission(ctx,
		selected.Permission, selected.operation, selected.event); err != nil ||
		!replay || stored.ID != selected.Permission.ID {
		t.Fatalf("Workspace Access replay=%+v replay=%t err=%v", stored, replay, err)
	}
	reset, err := selectHistoricalRunPermission(ctx, legacy, run.ID,
		domain.RunExecutionPermissionConservative, "workspace-access-reset-0001",
		"return to conservative boundary")
	if err != nil || reset.Permission.Revision != 4 ||
		reset.Permission.Mode != domain.RunExecutionPermissionConservative {
		t.Fatalf("Workspace Access downgrade=%+v err=%v", reset, err)
	}
	if _, err := legacy.db.ExecContext(ctx, `INSERT INTO run_execution_permission_snapshots
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
	assertNoForeignKeyViolations(t, legacy.db)
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
	for _, selection := range []historicalRunPermissionSelection{legacyResult, selected, reset} {
		stored, err := upgraded.GetRunExecutionPermissionSnapshot(ctx, selection.Permission.ID)
		if err != nil || stored != selection.Permission {
			t.Fatalf("historical permission changed: before=%+v after=%+v err=%v", selection.Permission, stored, err)
		}
		operation, found, err := upgraded.GetRunExecutionPermissionOperation(ctx, selection.operation.KeyDigest)
		if err != nil || !found || operation != selection.operation {
			t.Fatalf("historical operation changed: %+v found=%t err=%v", operation, found, err)
		}
		stored, replay, err := upgraded.TransitionRunExecutionPermission(ctx,
			selection.Permission, selection.operation, selection.event)
		if err != nil || !replay || stored != selection.Permission {
			t.Fatalf("historical Store replay changed: %+v replay=%t err=%v", stored, replay, err)
		}
	}
	if _, err := application.NewRunExecutionPermissionService(upgraded,
		domain.ExecutionPermissionRuntimeCapabilities{}).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "workspace-access-legacy-full-0001", RequestedBy: "test_operator",
			Reason: "persist historical full access", ConfirmDangerFullAccess: true,
		}); err == nil || !strings.Contains(err.Error(), "ask, auto, or full") {
		t.Fatalf("retired public permission writer reopened: %v", err)
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
	return openHistoricalTestDatabase(t, path, 125)
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
	runtimeAuthority := domain.NewExecutionPermissionRuntimeAuthority()
	fence, err := runtimeAuthority.IssueRunAuthorizationFence(running.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{RuntimeAuthority: runtimeAuthority}).Change(
		ctx, application.ChangeRunExecutionPermissionRequest{
			RunID: running.ID, Mode: string(domain.RunExecutionPermissionAuto),
			OperationKey: "workspace-access-revoke-lease-0001",
			RequestedBy:  "test_operator", Reason: "invalidate old execution owner",
		})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Permission.Mode != domain.RunExecutionPermissionAuto ||
		selected.Permission.ProcessEnabled || selected.Permission.ExecutionAuthorized ||
		selected.Permission.CapabilityGrant || runtimeAuthority.AllowsRunAuthorizationFence(running.ID, fence) {
		t.Fatalf("permission change retained old runtime authority: %+v", selected)
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
