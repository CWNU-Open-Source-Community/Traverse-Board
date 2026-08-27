package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const (
	canonicalRiskEscalationMigration136Checksum = "6fdf459cf1fe6ae94370118a983598f4bc6e469d383a1f43aa211d863c034e4d"
	canonicalLegacyRepairMigration138Checksum   = "39c6c11969c53b7e14d50ee5a16868522ca5cd6b476ee16b083eb0ff8a93c1b9"
)

func legacyWindowsPreviewMigration136Statements(t testing.TB) []string {
	t.Helper()
	legacy := make([]string, 0, len(riskEscalationStatements)-2)
	removed := 0
	for _, statement := range riskEscalationStatements {
		trimmed := strings.TrimSpace(statement)
		if strings.HasPrefix(trimmed,
			"CREATE TRIGGER trg_risk_escalation_supervisor_authority_insert") ||
			strings.HasPrefix(trimmed,
				"CREATE TRIGGER trg_host_command_supervisor_envelope_immutable") {
			removed++
			continue
		}
		legacy = append(legacy, statement)
	}
	if removed != 2 {
		t.Fatalf("legacy v136 fixture removed %d triggers, want 2", removed)
	}
	return legacy
}

func TestSchemaV138RepairsExactLegacyWindowsPreviewV136(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-windows-preview-v136.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO workspaces
		(id, name, root_path, created_at) VALUES (?, ?, ?, ?)`,
		"legacy-v136-workspace", "Legacy v136 workspace", `C:\legacy-v136-workspace`,
		"2026-08-25T21:10:38Z"); err != nil {
		state.Close()
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DELETE FROM schema_migrations WHERE version = 138`,
		`DROP TRIGGER trg_standard_code_delivery_insert`,
		`DROP TRIGGER trg_standard_code_delivery_update_immutable`,
		`DROP TRIGGER trg_standard_code_delivery_delete_immutable`,
		`DROP INDEX idx_standard_code_deliveries_run_event`,
		`DROP TABLE standard_code_deliveries`,
		`DELETE FROM schema_migrations WHERE version = 137`,
		`DROP TRIGGER trg_risk_escalation_supervisor_authority_insert`,
		`DROP TRIGGER trg_host_command_supervisor_envelope_immutable`,
	} {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			state.Close()
			t.Fatalf("restore legacy v136 with %q: %v", statement, err)
		}
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE schema_migrations SET checksum = ?
		WHERE version = 136`, legacyWindowsPreviewMigration136Checksum); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 136 {
		state.Close()
		t.Fatalf("restored schema version=%d want=136 err=%v", version, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	plan := migrationPlan()
	canonicalItem := plan[135]
	if checksum := migrationChecksum(canonicalItem); checksum !=
		canonicalRiskEscalationMigration136Checksum {
		t.Fatalf("canonical v136 checksum drifted: got %s want %s", checksum,
			canonicalRiskEscalationMigration136Checksum)
	}
	legacyItem := canonicalItem
	legacyItem.Statements = legacyWindowsPreviewMigration136Statements(t)
	if checksum := migrationChecksum(legacyItem); checksum !=
		legacyWindowsPreviewMigration136Checksum {
		t.Fatalf("legacy v136 fixture checksum=%s want=%s", checksum,
			legacyWindowsPreviewMigration136Checksum)
	}
	if checksum := migrationChecksum(plan[137]); checksum !=
		canonicalLegacyRepairMigration138Checksum {
		t.Fatalf("canonical v138 checksum drifted: got %s want %s", checksum,
			canonicalLegacyRepairMigration138Checksum)
	}
	if !acceptedMigrationChecksum(canonicalItem, legacyWindowsPreviewMigration136Checksum) {
		t.Fatal("exact legacy v136 checksum was not accepted")
	}
	if acceptedMigrationChecksum(plan[134], legacyWindowsPreviewMigration136Checksum) ||
		acceptedMigrationChecksum(canonicalItem, strings.Repeat("0", 64)) {
		t.Fatal("legacy v136 compatibility widened beyond the exact version and checksum")
	}
	applied := make(map[int]appliedMigration, 136)
	for _, item := range plan[:136] {
		applied[item.Version] = appliedMigration{Name: item.Name,
			Checksum: migrationChecksum(item)}
	}
	applied[136] = appliedMigration{Name: canonicalItem.Name,
		Checksum: legacyWindowsPreviewMigration136Checksum}
	if err := validateMigrationPlan(plan, applied); err != nil {
		t.Fatalf("exact legacy v136 plan was rejected: %v", err)
	}
	applied[136] = appliedMigration{Name: "wrong name",
		Checksum: legacyWindowsPreviewMigration136Checksum}
	if err := validateMigrationPlan(plan, applied); err == nil {
		t.Fatal("legacy v136 checksum was accepted under the wrong migration name")
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
	var recordedChecksum string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations
		WHERE version = 136`).Scan(&recordedChecksum); err != nil ||
		recordedChecksum != legacyWindowsPreviewMigration136Checksum {
		t.Fatalf("legacy v136 ledger was rewritten: checksum=%q err=%v",
			recordedChecksum, err)
	}
	var workspaceName, workspaceRoot string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT name, root_path FROM workspaces
		WHERE id = ?`, "legacy-v136-workspace").Scan(&workspaceName, &workspaceRoot); err != nil ||
		workspaceName != "Legacy v136 workspace" || workspaceRoot != `C:\legacy-v136-workspace` {
		t.Fatalf("legacy v136 application data changed: name=%q root=%q err=%v",
			workspaceName, workspaceRoot, err)
	}
	for _, name := range []string{
		"trg_risk_escalation_supervisor_authority_insert",
		"trg_host_command_supervisor_envelope_immutable",
	} {
		var sql string
		err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
			WHERE type = 'trigger' AND name = ?`, name).Scan(&sql)
		got := strings.TrimSuffix(strings.TrimSpace(sql), ";")
		want := strings.TrimSuffix(strings.TrimSpace(requireMigrationTrigger(name,
			riskEscalationStatements)), ";")
		if err != nil || got != want {
			t.Fatalf("repaired trigger %s differs from canonical v136 SQL: %q err=%v",
				name, sql, err)
		}
	}
	assertNoForeignKeyViolations(t, upgraded.db)
	assertSchemaV138SupervisorTriggerBehavior(t, ctx, upgraded)
}

func assertSchemaV138SupervisorTriggerBehavior(t testing.TB, ctx context.Context,
	state *SQLiteStore,
) {
	t.Helper()
	rows, err := state.db.QueryContext(ctx, `SELECT name FROM sqlite_master
		WHERE type = 'trigger' AND tbl_name = 'run_supervisor_tool_calls'
			AND name NOT IN (
				'trg_risk_escalation_supervisor_authority_insert',
				'trg_host_command_supervisor_envelope_immutable'
			)`)
	if err != nil {
		t.Fatal(err)
	}
	var unrelated []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		unrelated = append(unrelated, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range unrelated {
		quoted := strings.ReplaceAll(name, `"`, `""`)
		if _, err := state.db.ExecContext(ctx, `DROP TRIGGER "`+quoted+`"`); err != nil {
			t.Fatalf("drop unrelated Supervisor trigger %s: %v", name, err)
		}
	}
	if _, err := state.db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := state.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
			t.Errorf("restore foreign key enforcement: %v", err)
		}
	}()

	insert := `INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id,
		 tool_name, payload_json, authority_json, status, created_at)
		VALUES (?, 1, ?, 1, ?, 1, ?, 'host_command_propose', ?, ?, 'pending', ?)`
	if _, err := state.db.ExecContext(ctx, insert,
		"v138-trigger-run", "v138-trigger-attempt", 1, "v138-invalid-authority",
		`{"version":"risk_escalation.v1"}`, "", "2026-08-28T00:00:00Z"); err == nil ||
		!strings.Contains(err.Error(),
			"risk escalation Supervisor authority does not match its protocol") {
		t.Fatalf("mismatched risk authority was not rejected by repaired trigger: %v", err)
	}
	if _, err := state.db.ExecContext(ctx, insert,
		"v138-trigger-run", "v138-trigger-attempt", 1, "v138-valid-authority",
		`{"version":"risk_escalation.v1"}`, `{}`, "2026-08-28T00:00:00Z"); err != nil {
		t.Fatalf("valid risk authority was rejected by repaired trigger: %v", err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE run_supervisor_tool_calls
		SET payload_json = ? WHERE run_id = ? AND call_id = ?`,
		`{"version":"risk_escalation.v1","changed":true}`,
		"v138-trigger-run", "v138-valid-authority"); err == nil ||
		!strings.Contains(err.Error(), "host command Supervisor envelope is immutable") {
		t.Fatalf("host command envelope mutation was not rejected by repaired trigger: %v", err)
	}
	if _, err := state.db.ExecContext(ctx, `DELETE FROM run_supervisor_tool_calls
		WHERE run_id = ?`, "v138-trigger-run"); err != nil {
		t.Fatal(err)
	}
}
