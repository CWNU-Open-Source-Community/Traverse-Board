package store

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Cumulative legacy fixtures must restore the pre-Thread triggers before
// removing the tables they reference. Production migration history is intact.
func removeSchemaV154ForTestStatements() []string {
	var out []string
	seen := map[string]bool{}
	for _, statements := range [][]string{threadPlanOnDemandContinuationStatements, planDeliveryOptionalCheckpointStatements, threadStandardCodeContinuationStatements, threadPlanContinuationStatements, threadDrydockDeliveryScopeStatements, threadDrydockRuntimeScopeStatements, threadDrydockBindingStatements, threadDrydockCleanupStatements} {
		for _, statement := range statements {
			fields := strings.Fields(statement)
			if len(fields) < 3 || fields[0] != "CREATE" || fields[1] != "TRIGGER" {
				continue
			}
			name := fields[2]
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, "DROP TRIGGER IF EXISTS "+name)
			var prior string
			for _, migration := range migrationPlan() {
				if migration.Version >= 154 {
					break
				}
				for _, candidate := range migration.Statements {
					parts := strings.Fields(candidate)
					if len(parts) >= 3 && parts[0] == "CREATE" && parts[1] == "TRIGGER" && parts[2] == name {
						prior = candidate
					}
				}
			}
			if prior != "" {
				out = append(out, prior)
			}
		}
	}
	out = append(out, "DROP VIEW IF EXISTS thread_plan_on_demand_completed_sources", "DROP VIEW IF EXISTS plan_on_demand_completion_events", "DROP VIEW IF EXISTS thread_plan_completed_sources", "DROP VIEW IF EXISTS thread_plan_continuation_sources", "ALTER TABLE plan_delivery_selections DROP COLUMN manual_acceptance", "DROP VIEW IF EXISTS run_file_drydock_bindings", "DROP TABLE IF EXISTS thread_drydock_bindings", "DROP TABLE IF EXISTS drydock_cleanup_operations", "DELETE FROM schema_migrations WHERE version IN (154,155,156)")
	return out
}

func TestSchemaV154KeepsExistingHistoryAndAddsOnlyExplicitThreadBindings(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "v153-history.db")
	state := openUnmigratedSQLiteStore(t, path)
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 153); err != nil {
		t.Fatal(err)
	}
	run, _ := newV153SourceRun(t, state, "binding-upgrade")
	before, err := state.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
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
