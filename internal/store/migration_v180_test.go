package store

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func readPreV180CommandRuntimeJob(t testing.TB, state *SQLiteStore, id string) (runner.CommandRuntimeJob, error) {
	t.Helper()
	return scanCommandRuntimeJob(state.db.QueryRowContext(t.Context(), `SELECT `+
		commandRuntimeV179Columns+`, 0 FROM command_runtime_jobs WHERE id=?`, id))
}

// Start with the actual immutable v177 schema and valid historical tuples.
// Upgrade to v179 first, then exercise the real Open -> v180 migration.
func TestSchemaV180PreservesHistoricalCommandJobsAndAuthority(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionWorkspaceAccess, domain.RunExecutionPermissionFullAccess, domain.RunExecutionPermissionDebug} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "command-v179-history.db")
			state := openUnmigratedSQLiteStore(t, path)
			if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 177); err != nil {
				t.Fatal(err)
			}
			adapter := commandruntimeadapter.HostUnsandboxed(strings.Repeat("a", 64))
			if mode == domain.RunExecutionPermissionWorkspaceAccess {
				adapter = commandruntimeadapter.SandboxedWorkspace("local_windows_lpac", "local-windows-lpac.v1", strings.Repeat("b", 64))
			}
			job := commandRuntimeMigrationJob(t, state, mode, adapter)
			insertV162CommandRuntimeJob(t, state, job, 73)
			for _, migration := range migrationPlan()[177:179] {
				if err := state.applyMigration(ctx, migration); err != nil {
					t.Fatal(err)
				}
			}
			before, err := readPreV180CommandRuntimeJob(t, state, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			actors := legacyFixtureRows(t, state, "command_runtime_job_agents")
			permissions := legacyFixtureRows(t, state, "run_execution_permission_snapshots")
			ledger, err := state.loadAppliedMigrations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			state, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			after, err := state.GetCommandRuntimeJob(ctx, job.ID)
			if err != nil || !reflect.DeepEqual(before, after) || after.RunAuthorizationFence != 0 {
				t.Fatalf("historical job or authority changed: before=%+v after=%+v err=%v", before, after, err)
			}
			var rowid int64
			if err := state.db.QueryRowContext(ctx, `SELECT rowid FROM command_runtime_jobs WHERE id=?`, job.ID).Scan(&rowid); err != nil || rowid != 73 {
				t.Fatal("original rowid changed", rowid, err)
			}
			if !reflect.DeepEqual(actors, legacyFixtureRows(t, state, "command_runtime_job_agents")) || !reflect.DeepEqual(permissions, legacyFixtureRows(t, state, "run_execution_permission_snapshots")) {
				t.Fatal("historical actor or permission rows changed")
			}
			currentLedger, err := state.loadAppliedMigrations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for version, entry := range ledger {
				if currentLedger[version] != entry {
					t.Fatalf("old migration %d changed", version)
				}
			}
			for _, query := range []string{`UPDATE command_runtime_jobs SET run_authorization_fence=1 WHERE id=?`, `UPDATE command_runtime_jobs SET permission_mode='full' WHERE id=?`, `DELETE FROM command_runtime_jobs WHERE id=?`} {
				if _, err := state.db.ExecContext(ctx, query, job.ID); err == nil {
					t.Fatal("retained job authority or receipt was mutable", query)
				}
			}
			assertNoForeignKeyViolations(t, state.db)
			assertLatestMigrationLedger(t, state, migrationPlan())
		})
	}
}
