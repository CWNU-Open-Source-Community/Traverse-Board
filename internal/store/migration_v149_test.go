package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSchemaV149AddsSourceBoundWebFetchAuthorizations(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v148-web-fetch-authorizations.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 148); err != nil {
		t.Fatal(err)
	}
	if err := state.applyMigration(ctx, plan[148]); err != nil {
		t.Fatal(err)
	}
	var tableCount, identityTriggerCount, deleteTriggerCount int
	if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'web_fetch_authorizations'`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'trigger' AND name = 'trg_web_fetch_authorizations_identity_immutable'`).
		Scan(&identityTriggerCount); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'trigger' AND name = 'trg_web_fetch_authorizations_delete_immutable'`).
		Scan(&deleteTriggerCount); err != nil {
		t.Fatal(err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 149 ||
		tableCount != 1 || identityTriggerCount != 1 || deleteTriggerCount != 1 {
		t.Fatalf("v149 schema mismatch: version=%d table=%d identity=%d delete=%d err=%v",
			version, tableCount, identityTriggerCount, deleteTriggerCount, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func TestCleanInstallV149IncludesWebFetchAuthorizations(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "clean-v149-web-fetch-authorizations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var count int
	if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'web_fetch_authorizations'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion || count != 1 {
		t.Fatalf("clean v149 schema mismatch: version=%d table=%d err=%v", version, count, err)
	}
}
