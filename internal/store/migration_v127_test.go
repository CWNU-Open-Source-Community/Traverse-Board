package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaV127AddsImmutableDrydockOwnershipAndExtendsCheckpointScope(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "drydock-v126.db")
	legacy := openSchemaV126Store(t, path)
	if err := legacy.SaveWorkspace(ctx, WorkspaceRecord{ID: "workspace-v127-source",
		Name: "v127-source", RootPath: filepath.Join(t.TempDir(), "source")}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.applyMigration(ctx, migrationPlan()[126]); err != nil {
		t.Fatal(err)
	}
	var v127Trigger string
	if err := legacy.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'trigger' AND name = 'trg_workspace_checkpoint_insert_scope'`).Scan(&v127Trigger); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v127Trigger, "drydock_workspaces") ||
		!strings.Contains(v127Trigger, "drydock.workspace_id = NEW.workspace_id") {
		t.Fatalf("v127 checkpoint scope lacks exact Drydock ownership: %s", v127Trigger)
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
	for _, binding := range []string{"FROM run_file_drydock_bindings owner",
		"JOIN drydock_workspaces drydock ON drydock.id=owner.drydock_id",
		"owner.run_id=NEW.run_id", "owner.mission_id=NEW.mission_id",
		"owner.session_id=NEW.session_id", "owner.workspace_id=NEW.workspace_id",
		"drydock.state<>'cleaned'", "thread.last_run_id=owner.run_id"} {
		if !strings.Contains(triggerSQL, binding) {
			t.Fatalf("latest checkpoint scope lost exact Drydock ownership %q: %s", binding, triggerSQL)
		}
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}

func openSchemaV126Store(t testing.TB, path string) *SQLiteStore {
	t.Helper()
	return openHistoricalTestDatabase(t, path, 126)
}
