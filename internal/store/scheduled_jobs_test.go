package store

import (
	"path/filepath"
	"testing"
)

// removeSchemaV120ForTestStatements restores a v119 database. Every historical
// downgrade helper eventually reaches removeSchemaV118ForTestStatements, so the
// newest schema must remain the first link in that chain.
func removeSchemaV120ForTestStatements() []string {
	return []string{
		`DROP TABLE scheduled_job_notifications`,
		`DROP TABLE scheduled_job_rounds`,
		`DROP TABLE scheduled_job_operations`,
		`DROP TABLE scheduled_job_authorizations`,
		`DROP TABLE scheduled_jobs`,
		`DELETE FROM schema_migrations WHERE version = 120`,
	}
}

func TestSchemaV120UpgradesV119Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled-jobs-v119.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV120ForTestStatements() {
		if _, err := state.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("downgrade v120 with %q: %v", statement, err)
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
	if version, err := upgraded.SchemaVersion(t.Context()); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	for _, table := range []string{
		"scheduled_jobs", "scheduled_job_authorizations", "scheduled_job_operations",
		"scheduled_job_rounds", "scheduled_job_notifications",
	} {
		var count int
		if err := upgraded.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master
			WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}
