package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func removeSchemaV137ForTestStatements() []string {
	return []string{
		`DROP TRIGGER trg_standard_code_delivery_insert`,
		`DROP TRIGGER trg_standard_code_delivery_update_immutable`,
		`DROP TRIGGER trg_standard_code_delivery_delete_immutable`,
		`DROP INDEX idx_standard_code_deliveries_run_event`,
		`DROP TABLE standard_code_deliveries`,
		`DELETE FROM schema_migrations WHERE version = 137`,
	}
}

func TestSchemaV137AddsEmptyImmutableStandardCodeDeliveryLedger(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "standard-code-delivery-v136.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV137ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			state.Close()
			t.Fatalf("restore schema v136 with %q: %v", statement, err)
		}
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 136 {
		state.Close()
		t.Fatalf("restored schema version=%d want=136 err=%v", version, err)
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
		`SELECT COUNT(*) FROM standard_code_deliveries`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("v137 fabricated delivery history: count=%d err=%v", count, err)
	}
	for _, name := range []string{"trg_standard_code_delivery_insert",
		"trg_standard_code_delivery_update_immutable",
		"trg_standard_code_delivery_delete_immutable"} {
		var sql string
		if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
			WHERE type = 'trigger' AND name = ?`, name).Scan(&sql); err != nil ||
			!strings.Contains(sql, "standard_code_deliver") {
			t.Fatalf("v137 trigger %s missing or invalid: %q err=%v", name, sql, err)
		}
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}
