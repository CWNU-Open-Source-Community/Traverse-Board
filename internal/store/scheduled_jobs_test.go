package store

import (
	"path/filepath"
	"testing"
)

func TestSchemaV122UpgradesV121Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled-jobs-v121.db")
	state := openHistoricalTestDatabase(t, path, 121)

	// The immutable historical prefix above is the upgrade input.
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
