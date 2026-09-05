package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func removeSchemaV136ForTestStatements() []string {
	createLegacyGrants := requireMigrationStatement(
		"CREATE TABLE approval_session_grants (", sessionGrantAndToolBudgetStatements)
	createLegacyGrants = strings.Replace(createLegacyGrants,
		"CREATE TABLE approval_session_grants (",
		"CREATE TABLE approval_session_grants_v135_restore (", 1)
	prefix := append(removeSchemaV139ForTestStatements(), []string{
		`DELETE FROM schema_migrations WHERE version = 138`,
		`PRAGMA foreign_keys = OFF`,
		`PRAGMA legacy_alter_table = ON`,
		`DROP TRIGGER trg_standard_code_delivery_insert`,
		`DROP TRIGGER trg_standard_code_delivery_update_immutable`,
		`DROP TRIGGER trg_standard_code_delivery_delete_immutable`,
		`DROP INDEX idx_standard_code_deliveries_run_event`,
		`DROP TABLE standard_code_deliveries`,
		`DELETE FROM schema_migrations WHERE version = 137`,
		`DROP TRIGGER trg_risk_escalation_supervisor_authority_insert`,
		`DROP TRIGGER trg_host_command_supervisor_envelope_immutable`,
		`DROP TRIGGER trg_risk_escalation_proposal_update_immutable`,
		`DROP TRIGGER trg_risk_escalation_proposal_delete_immutable`,
		`DROP TRIGGER trg_risk_escalation_operation_update_immutable`,
		`DROP TRIGGER trg_risk_escalation_operation_delete_immutable`,
		`DROP TRIGGER trg_approval_grant_consumption_update_immutable`,
		`DROP TRIGGER trg_approval_grant_consumption_delete_immutable`,
		`DROP TRIGGER trg_risk_escalation_intent_update_immutable`,
		`DROP TRIGGER trg_risk_escalation_intent_delete_immutable`,
		`DROP TRIGGER trg_risk_escalation_result_update_immutable`,
		`DROP TRIGGER trg_risk_escalation_result_delete_immutable`,
		`DROP TRIGGER trg_risk_escalation_invalidation_update_immutable`,
		`DROP TRIGGER trg_risk_escalation_invalidation_delete_immutable`,
		`DROP TABLE risk_escalation_invalidations`,
		`DROP TABLE risk_escalation_results`,
		`DROP TABLE risk_escalation_execution_intents`,
		`DROP TABLE risk_escalation_operations`,
		`DROP TABLE risk_escalation_proposals`,
		`DROP TABLE approval_grant_consumptions`,
	}...)
	restoreCalls := rebuildRiskEscalationSupervisorToolCalls(
		standardCodeSupervisorToolCallCreate("run_supervisor_tool_calls_v135_restore", true),
		"run_supervisor_tool_calls_v135_restore", "run_supervisor_tool_calls_v136_restore")
	suffix := []string{
		`DROP INDEX idx_approval_session_grants_expiry`,
		`DROP INDEX idx_approval_session_grants_active_scope`,
		`DROP INDEX idx_approval_session_grants_run_status_updated_at`,
		`ALTER TABLE approval_session_grants RENAME TO approval_session_grants_v136_restore`,
		createLegacyGrants,
		`INSERT INTO approval_session_grants_v135_restore
			(id, run_id, session_id, workspace_id, tool_name, action_class, status,
			request_fingerprint, reason, revocation_reason, granted_by, revoked_by,
			version, created_at, updated_at, revoked_at)
			SELECT id, run_id, session_id, workspace_id, tool_name, action_class, status,
			request_fingerprint, reason, revocation_reason, granted_by, revoked_by,
			version, created_at, updated_at, revoked_at
			FROM approval_session_grants_v136_restore`,
		`DROP TABLE approval_session_grants_v136_restore`,
		`ALTER TABLE approval_session_grants_v135_restore RENAME TO approval_session_grants`,
		`CREATE UNIQUE INDEX idx_approval_session_grants_active_scope
			ON approval_session_grants(session_id, workspace_id, tool_name, action_class)
			WHERE status = 'active'`,
		`CREATE INDEX idx_approval_session_grants_run_status_updated_at
			ON approval_session_grants(run_id, status, updated_at)`,
		`DELETE FROM schema_migrations WHERE version = 136`,
		`PRAGMA legacy_alter_table = OFF`,
		`PRAGMA foreign_keys = ON`,
	}
	return append(append(prefix, restoreCalls...), suffix...)
}

func TestSchemaV136AddsDurableRiskEscalationLedger(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "risk-escalation-v135.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV136ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			state.Close()
			t.Fatalf("restore schema v135 with %q: %v", statement, err)
		}
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 135 {
		state.Close()
		t.Fatalf("restored schema version=%d want=135 err=%v", version, err)
	}
	if _, err := state.db.ExecContext(ctx,
		`SELECT scope_fingerprint FROM approval_session_grants LIMIT 1`); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "no such column") {
		state.Close()
		t.Fatalf("v135 unexpectedly retained bounded grant columns: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("upgraded schema version=%d want=%d err=%v", version,
			LatestSchemaVersion, err)
	}
	for _, table := range []string{
		"approval_grant_consumptions", "risk_escalation_proposals",
		"risk_escalation_operations", "risk_escalation_execution_intents",
		"risk_escalation_results", "risk_escalation_invalidations",
	} {
		var name string
		if err := upgraded.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).
			Scan(&name); err != nil || name != table {
			t.Fatalf("v136 table %s missing: name=%q err=%v", table, name, err)
		}
	}
	var callsSQL string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'run_supervisor_tool_calls'`).Scan(&callsSQL); err != nil ||
		!strings.Contains(callsSQL,
			"tool_name IN ('host_command_propose', 'mcp_tool_call', 'workspace_list'") {
		t.Fatalf("v136 Supervisor risk authority constraint is missing: %q err=%v",
			callsSQL, err)
	}
	for _, trigger := range []string{
		"trg_risk_escalation_supervisor_authority_insert",
		"trg_host_command_supervisor_envelope_immutable",
	} {
		var name string
		if err := upgraded.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master
			WHERE type = 'trigger' AND name = ?`, trigger).Scan(&name); err != nil ||
			name != trigger {
			t.Fatalf("v136 Supervisor authority trigger %s missing: name=%q err=%v",
				trigger, name, err)
		}
	}
	var scope sql.NullString
	if err := upgraded.db.QueryRowContext(ctx,
		`SELECT scope_fingerprint FROM approval_session_grants LIMIT 1`).Scan(&scope); err != nil &&
		err != sql.ErrNoRows {
		t.Fatalf("v136 bounded grant columns are unavailable: %v", err)
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}
