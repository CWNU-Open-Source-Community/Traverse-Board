package store

import (
	"path/filepath"
	"reflect"

	"testing"
)

func TestSchemaV154KeepsExistingHistoryAndAddsOnlyExplicitThreadBindings(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "v153-history.db")
	state := openHistoricalTestDatabase(t, path, 153)
	restoreLegacyInputs := addCurrentInputColumnsForLegacySeed(t, state)
	run, _ := newV153SourceRun(t, state, "binding-upgrade")
	before, err := state.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restoreLegacyInputs()
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	after, err := upgraded.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for version, record := range before {
		if !reflect.DeepEqual(record, after[version]) {
			t.Fatalf("historical migration %d changed", version)
		}
	}
	preserved, err := upgraded.GetRun(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(preserved, run) {
		t.Fatalf("existing run changed: %+v %v", preserved, err)
	}
	var count int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_drydock_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("upgrade invented a binding: %d %v", count, err)
	}
	if _, err := upgraded.db.ExecContext(ctx, `INSERT INTO thread_drydock_bindings(run_id,thread_id,drydock_id,predecessor_run_id,created_at) VALUES(?,?,?,?,?)`, run.ID, "wrong-thread", "wrong-drydock", run.ID, ts(run.CreatedAt)); err == nil {
		t.Fatal("unattributed directory binding was accepted")
	}
}
