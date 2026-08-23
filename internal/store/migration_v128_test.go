package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaV128ExtendsDockerAdmissionPermissionWithoutLosingGuards(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "standard-code-docker-v127.db")
	legacy := openSchemaV127Store(t, path)
	var before string
	if err := legacy.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'sandbox_docker_product_admissions'`).
		Scan(&before); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "'workspace_access'") {
		t.Fatalf("v127 fixture unexpectedly accepts Workspace Access: %s", before)
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
		t.Fatalf("schema version=%d want=%d err=%v", version, LatestSchemaVersion,
			versionErr)
	}
	var after string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'sandbox_docker_product_admissions'`).
		Scan(&after); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"'workspace_access'", "'approval'", "'full_access'", "'debug'"} {
		if !strings.Contains(after, mode) {
			t.Fatalf("v128 Docker admission constraint omitted %s: %s", mode, after)
		}
	}
	for _, name := range []string{
		"trg_sandbox_docker_product_admission_insert",
		"trg_sandbox_docker_product_cancellation_insert",
		"trg_sandbox_docker_product_start_request_insert",
		"trg_sandbox_docker_product_launch_insert",
		"trg_sandbox_docker_product_receipt_insert",
		"trg_sandbox_docker_product_admission_update_immutable",
		"trg_sandbox_docker_product_admission_delete_immutable",
	} {
		var stored string
		if err := upgraded.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master
			WHERE type = 'trigger' AND name = ?`, name).Scan(&stored); err != nil ||
			stored != name {
			t.Fatalf("v128 did not restore trigger %q: stored=%q err=%v", name, stored, err)
		}
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}

func openSchemaV127Store(t testing.TB, path string) *SQLiteStore {
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
	for _, item := range migrationPlan()[:127] {
		if err := state.applyMigration(context.Background(), item); err != nil {
			_ = state.Close()
			t.Fatalf("apply schema v127 fixture migration %d: %v", item.Version, err)
		}
	}
	return state
}
