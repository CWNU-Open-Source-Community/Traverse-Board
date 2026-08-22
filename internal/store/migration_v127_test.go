package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// removeSchemaV127ForTestStatements restores the exact v126 checkpoint scope
// guard after removing the Drydock-owned schema. Historical downgrade fixtures
// must not leave a future table reference behind.
func removeSchemaV127ForTestStatements() []string {
	return []string{
		`DROP TRIGGER trg_workspace_checkpoint_insert_scope`,
		`CREATE TRIGGER trg_workspace_checkpoint_insert_scope
			BEFORE INSERT ON workspace_checkpoints
			WHEN NOT EXISTS (
				SELECT 1 FROM runs run
				JOIN missions mission ON mission.id = run.mission_id
				JOIN sessions session_record ON session_record.id = run.session_id
				WHERE run.id = NEW.run_id AND mission.id = NEW.mission_id
					AND session_record.id = NEW.session_id
					AND mission.workspace_id = NEW.workspace_id
					AND session_record.workspace_id = NEW.workspace_id
			)
			BEGIN SELECT RAISE(ABORT, 'workspace checkpoint Run binding is invalid'); END`,
		`DROP TRIGGER trg_drydock_synthetic_workspace_update_immutable`,
		`DROP TRIGGER trg_drydock_mission_insert_scope`,
		`DROP TRIGGER trg_drydock_mission_update_scope`,
		`DROP TRIGGER trg_drydock_session_insert_scope`,
		`DROP TRIGGER trg_drydock_session_update_scope`,
		`DROP TABLE drydock_lifecycle_receipts`,
		`DROP TABLE drydock_delivery_proposals`,
		`DROP TABLE drydock_workspaces`,
		`DROP TABLE drydock_workspace_trust`,
		`DELETE FROM schema_migrations WHERE version = 127`,
	}
}

func TestSchemaV127AddsImmutableDrydockOwnershipAndExtendsCheckpointScope(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "drydock-v126.db")
	legacy := openSchemaV126Store(t, path)
	if err := legacy.SaveWorkspace(ctx, WorkspaceRecord{ID: "workspace-v127-source",
		Name: "v127-source", RootPath: filepath.Join(t.TempDir(), "source")}); err != nil {
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
	for _, table := range []string{"drydock_workspace_trust", "drydock_workspaces",
		"drydock_delivery_proposals", "drydock_lifecycle_receipts"} {
		var name string
		if err := upgraded.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).
			Scan(&name); err != nil || name != table {
			t.Fatalf("Drydock table %q unavailable: name=%q err=%v", table, name, err)
		}
	}
	var triggerSQL string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'trigger' AND name = 'trg_workspace_checkpoint_insert_scope'`).
		Scan(&triggerSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(triggerSQL, "drydock_workspaces") ||
		!strings.Contains(triggerSQL, "drydock.workspace_id = NEW.workspace_id") {
		t.Fatalf("checkpoint scope was not extended with exact Drydock ownership: %s", triggerSQL)
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}

func TestSchemaV127DowngradeFixtureRestoresV126AndReupgrades(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "drydock-downgrade.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV127ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 126 {
		t.Fatalf("downgraded schema version=%d want=126 err=%v", version, err)
	}
	var triggerSQL string
	if err := state.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'trigger' AND name = 'trg_workspace_checkpoint_insert_scope'`).
		Scan(&triggerSQL); err != nil || strings.Contains(triggerSQL, "drydock_workspaces") {
		t.Fatalf("v126 checkpoint scope was not restored: err=%v sql=%s", err, triggerSQL)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if version, err := reopened.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("re-upgraded schema version=%d want=%d err=%v", version,
			LatestSchemaVersion, err)
	}
}

func openSchemaV126Store(t testing.TB, path string) *SQLiteStore {
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
	for _, item := range migrationPlan()[:126] {
		if err := state.applyMigration(context.Background(), item); err != nil {
			_ = state.Close()
			t.Fatalf("apply schema v126 fixture migration %d: %v", item.Version, err)
		}
	}
	return state
}
