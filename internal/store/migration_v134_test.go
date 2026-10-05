package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaV134UpgradesV133Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "web-evidence-v133.db")
	state := openHistoricalTestDatabase(t, path, 133)
	// The immutable historical prefix above is the upgrade input.
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
