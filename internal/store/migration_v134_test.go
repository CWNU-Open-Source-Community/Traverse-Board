package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func removeSchemaV134ForTestStatements() []string {
	createCalls := requireMigrationStatement(
		"CREATE TABLE run_supervisor_tool_calls_v131 (", commandRuntimeAdapterStatements)
	createCalls = replaceCommandRuntimeMigrationFragment(createCalls,
		"CREATE TABLE run_supervisor_tool_calls_v131 (",
		"CREATE TABLE run_supervisor_tool_calls_v133 (")
	removeV134 := []string{
		`DROP TRIGGER trg_supervisor_tool_call_model_attempt`,
		`DROP TRIGGER trg_supervisor_tool_round_completion`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_immutable`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_insert`,
		`DROP INDEX idx_run_supervisor_tool_calls_pending`,
		`DROP INDEX idx_supervisor_tool_stream_call_identity`,
		`DROP INDEX idx_supervisor_tool_stream_item_identity`,
		`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v134`,
		createCalls,
		`INSERT INTO run_supervisor_tool_calls_v133 SELECT *
			FROM run_supervisor_tool_calls_v134`,
		`DROP TABLE run_supervisor_tool_calls_v134`,
		`ALTER TABLE run_supervisor_tool_calls_v133 RENAME TO run_supervisor_tool_calls`,
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
		`DROP TABLE web_evidence_operations`,
		`DROP TABLE web_evidence_citations`,
		`DROP TABLE web_evidence_snapshots`,
		`DROP TABLE web_evidence_sources`,
		`DELETE FROM schema_migrations WHERE version = 134`,
	}
	return append(removeSchemaV135ForTestStatements(), removeV134...)
}

func TestSchemaV134UpgradesV133Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "web-evidence-v133.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV134ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			state.Close()
			t.Fatalf("restore schema v133: %v\n%s", err, statement)
		}
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 133 {
		state.Close()
		t.Fatalf("restored schema version=%d want=133 err=%v", version, err)
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
		t.Fatalf("upgraded schema version=%d want=%d err=%v",
			version, LatestSchemaVersion, err)
	}
	for _, table := range []string{
		"web_evidence_sources", "web_evidence_snapshots",
		"web_evidence_citations", "web_evidence_operations",
	} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).
			Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
	var supervisorSchema string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'run_supervisor_tool_calls'`).
		Scan(&supervisorSchema); err != nil {
		t.Fatal(err)
	}
	for _, toolName := range []string{"web_search", "web_fetch", "web_citation"} {
		if strings.Count(supervisorSchema, "'"+toolName+"'") != 3 {
			t.Fatalf("Supervisor schema did not bind %s to the tool and authority constraints:\n%s",
				toolName, supervisorSchema)
		}
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}
