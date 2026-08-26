package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func removeSchemaV135ForTestStatements() []string {
	createCalls := standardCodeSupervisorToolCallCreate(
		"run_supervisor_tool_calls_v134", false)
	removeV135 := []string{
		`PRAGMA foreign_keys = OFF`,
		`PRAGMA legacy_alter_table = ON`,
		`DROP TRIGGER trg_standard_code_supervisor_ledger_insert`,
		`DROP TRIGGER trg_standard_code_supervisor_ledger_update_immutable`,
		`DROP TRIGGER trg_standard_code_supervisor_ledger_delete_immutable`,
		`DROP INDEX idx_standard_code_supervisor_ledger_run_event`,
		`DROP INDEX idx_standard_code_supervisor_ledger_intent`,
		`DROP INDEX idx_standard_code_supervisor_ledger_call`,
		`DROP TABLE standard_code_supervisor_ledger`,
		`DROP TRIGGER trg_supervisor_tool_call_model_attempt`,
		`DROP TRIGGER trg_supervisor_tool_round_completion`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_immutable`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_insert`,
		`DROP INDEX idx_run_supervisor_tool_calls_pending`,
		`DROP INDEX idx_supervisor_tool_stream_call_identity`,
		`DROP INDEX idx_supervisor_tool_stream_item_identity`,
		`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v135`,
		createCalls,
		`INSERT INTO run_supervisor_tool_calls_v134 SELECT *
			FROM run_supervisor_tool_calls_v135`,
		`DROP TABLE run_supervisor_tool_calls_v135`,
		`ALTER TABLE run_supervisor_tool_calls_v134 RENAME TO run_supervisor_tool_calls`,
		requireMigrationStatement("CREATE INDEX idx_run_supervisor_tool_calls_pending",
			githubReviewStatements),
		requireMigrationStatement("CREATE UNIQUE INDEX idx_supervisor_tool_stream_item_identity",
			itemStreamToolIdentityStatements),
		requireMigrationStatement("CREATE UNIQUE INDEX idx_supervisor_tool_stream_call_identity",
			itemStreamToolIdentityStatements),
		requireMigrationTrigger("trg_supervisor_tool_call_model_attempt",
			githubReviewStatements),
		requireMigrationTrigger("trg_supervisor_tool_round_completion",
			githubReviewStatements),
		requireMigrationTrigger("trg_supervisor_tool_stream_identity_insert",
			itemStreamToolIdentityStatements),
		requireMigrationTrigger("trg_supervisor_tool_stream_identity_immutable",
			itemStreamToolIdentityStatements),
		`DELETE FROM schema_migrations WHERE version = 135`,
		`PRAGMA legacy_alter_table = OFF`,
		`PRAGMA foreign_keys = ON`,
	}
	return append(removeSchemaV136ForTestStatements(), removeV135...)
}

func TestSchemaV135AddsEmptyImmutableStandardCodeSupervisorLedger(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "standard-code-supervisor-v134.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	downStatements := removeSchemaV135ForTestStatements()
	for _, statement := range downStatements {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			state.Close()
			t.Fatalf("restore schema v134 with %q: %v", statement, err)
		}
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 134 {
		state.Close()
		t.Fatalf("restored schema version=%d want=134 err=%v", version, err)
	}
	var legacyCallsSQL string
	if err := state.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'run_supervisor_tool_calls'`).
		Scan(&legacyCallsSQL); err != nil || strings.Contains(legacyCallsSQL,
		"code_workspace_symbols") {
		state.Close()
		t.Fatalf("restored v134 unexpectedly accepts Code Intel calls: %q err=%v",
			legacyCallsSQL, err)
	}
	for _, toolName := range []string{"web_search", "web_fetch", "web_citation"} {
		if strings.Count(legacyCallsSQL, "'"+toolName+"'") != 3 {
			state.Close()
			t.Fatalf("restored v134 lost %s Supervisor constraints:\n%s", toolName,
				legacyCallsSQL)
		}
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
	var count int
	if err := upgraded.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM standard_code_supervisor_ledger`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("v135 fabricated supervisor history: count=%d err=%v", count, err)
	}
	var callsSQL string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'run_supervisor_tool_calls'`).Scan(&callsSQL); err != nil ||
		!strings.Contains(callsSQL, "code_workspace_symbols") ||
		!strings.Contains(callsSQL, "code_type_hierarchy") {
		t.Fatalf("v135 Code Intel durable call registry is incomplete: %q err=%v",
			callsSQL, err)
	}
	for _, name := range []string{
		"trg_standard_code_supervisor_ledger_insert",
		"trg_standard_code_supervisor_ledger_update_immutable",
		"trg_standard_code_supervisor_ledger_delete_immutable",
	} {
		var triggerSQL string
		if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
			WHERE type = 'trigger' AND name = ?`, name).Scan(&triggerSQL); err != nil ||
			!strings.Contains(triggerSQL, "standard_code_supervisor_ledger") {
			t.Fatalf("v135 trigger %s missing or invalid: %q err=%v", name, triggerSQL, err)
		}
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}
