package store

import (
	"context"
	"path/filepath"
	"testing"
)

func removeSchemaV130ForTestStatements() []string {
	return []string{
		`DROP TRIGGER trg_supervisor_tool_stream_identity_immutable`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_insert`,
		`DROP INDEX idx_supervisor_tool_stream_call_identity`,
		`DROP INDEX idx_supervisor_tool_stream_item_identity`,
		`ALTER TABLE run_supervisor_tool_calls DROP COLUMN stream_call_id`,
		`ALTER TABLE run_supervisor_tool_calls DROP COLUMN stream_item_id`,
		`ALTER TABLE run_supervisor_tool_calls DROP COLUMN stream_response_id`,
		`DELETE FROM schema_migrations WHERE version = 130`,
	}
}

func TestSchemaV130AddsImmutableItemStreamToolIdentities(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "item-stream-v129.db")
	legacy := openSchemaV128Store(t, path)
	if err := legacy.applyMigration(ctx, migrationPlan()[128]); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, versionErr := upgraded.SchemaVersion(ctx); versionErr != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema version=%d want=%d err=%v", version, LatestSchemaVersion, versionErr)
	}
	for _, column := range []string{"stream_response_id", "stream_item_id", "stream_call_id"} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('run_supervisor_tool_calls') WHERE name = ?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing v130 column %q: count=%d err=%v", column, count, err)
		}
	}
	for _, index := range []string{"idx_supervisor_tool_stream_item_identity",
		"idx_supervisor_tool_stream_call_identity"} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
			WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing v130 index %q: count=%d err=%v", index, count, err)
		}
	}
}
