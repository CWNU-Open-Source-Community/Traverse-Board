package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func openRunSeedBoundaryStore(t *testing.T, version int) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seed-boundary.db")
	if version == 0 {
		state, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = state.Close() })
		return state
	}
	state := openUnmigratedSQLiteStore(t, path)
	if err := applyMigrationPrefixForTest(t.Context(), state, migrationPlan(), version); err != nil {
		t.Fatal(err)
	}
	return state
}

// Count every application table, not only runs: failed creation must also roll
// back the Session, Mission, graph, snapshots, Thread, and their event records.
func runSeedBoundaryRows(t *testing.T, state *SQLiteStore) map[string]int {
	t.Helper()
	rows, err := state.db.QueryContext(t.Context(), `SELECT name FROM sqlite_schema
		WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int, len(names))
	for _, name := range names {
		var count int
		if err := state.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[name] = count
	}
	return counts
}

func assertRunSeedPermission(t *testing.T, state *SQLiteStore, run domain.Run, legacy bool) {
	t.Helper()
	permission, err := state.GetRunExecutionPermission(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.GetThreadExecutionPermission(t.Context(), domain.InitialThreadID(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	runProtocol, threadProtocol, mode := domain.RunApprovalPermissionProtocolVersion, domain.ThreadApprovalPermissionProtocolVersion, domain.RunExecutionPermissionAsk
	if legacy {
		runProtocol, threadProtocol, mode = domain.RunExecutionPermissionProtocolVersion, domain.ThreadExecutionPermissionProtocolVersion, domain.RunExecutionPermissionConservative
	}
	if permission.ProtocolVersion != runProtocol || permission.Mode != mode || permission.Revision != 1 || permission.ExecutionAuthorized || permission.OperatorConfirmed {
		t.Fatalf("unexpected Run initial permission: %#v", permission)
	}
	if thread.ProtocolVersion != threadProtocol || thread.Mode != mode || thread.Revision != 1 || thread.ExecutionAuthorized || thread.OperatorConfirmed {
		t.Fatalf("unexpected Thread initial preference: %#v", thread)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func TestHistoricalRunSeedUsesCurrentV2AskAtAndAfter178(t *testing.T) {
	for _, version := range []int{178, 0} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			state := openRunSeedBoundaryStore(t, version)
			before, err := state.loadAppliedMigrations(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			_, run, err := newMigrationFixtureRunService(t, state).Create(t.Context(), application.CreateRunRequest{Goal: "current writer through historical fixture selector", Budget: domain.Budget{MaxTurns: 2}})
			if err != nil {
				t.Fatal(err)
			}
			assertRunSeedPermission(t, state, run, false)
			if after, err := state.loadAppliedMigrations(t.Context()); err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("fixture changed migration ledger: %v", err)
			}
		})
	}
}

func TestHistoricalRunSeedRejectsDirectLegacyWriterOnCurrentSchema(t *testing.T) {
	for _, version := range []int{178, 0} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			state := openRunSeedBoundaryStore(t, version)
			before := runSeedBoundaryRows(t, state)
			_, _, err := application.NewRunService(legacyRunSeedStore{state}).Create(t.Context(), application.CreateRunRequest{Goal: "forbidden v1 write on current schema", Budget: domain.Budget{MaxTurns: 2}})
			if err == nil || !strings.Contains(err.Error(), "historical Run fixture rejects schema") {
				t.Fatalf("direct historical writer did not reject current schema: %v", err)
			}
			if after := runSeedBoundaryRows(t, state); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected historical writer left partial rows: before=%v after=%v", before, after)
			}
			assertNoForeignKeyViolations(t, state.db)
		})
	}
}

func TestHistoricalRunSeedLateFailureRollsBackBothWriterBranches(t *testing.T) {
	for _, version := range []int{177, 0} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			state := openRunSeedBoundaryStore(t, version)
			// This is the last Thread creation insert, after all Run graph writes.
			if _, err := state.db.ExecContext(t.Context(), `CREATE TRIGGER force_seed_thread_failure BEFORE INSERT ON thread_events
				BEGIN SELECT RAISE(ABORT, 'forced late Thread seed failure'); END`); err != nil {
				t.Fatal(err)
			}
			before := runSeedBoundaryRows(t, state)
			request := application.CreateRunRequest{Goal: "atomic initial graph", Budget: domain.Budget{MaxTurns: 2}}
			_, _, err := newMigrationFixtureRunService(t, state).Create(t.Context(), request)
			if err == nil || !strings.Contains(err.Error(), "forced late Thread seed failure") {
				t.Fatalf("did not exercise final Thread insert failure: %v", err)
			}
			if after := runSeedBoundaryRows(t, state); !reflect.DeepEqual(before, after) {
				t.Fatalf("failed creation retained partial graph rows: before=%v after=%v", before, after)
			}
			if _, err := state.db.ExecContext(t.Context(), `DROP TRIGGER force_seed_thread_failure`); err != nil {
				t.Fatal(err)
			}
			_, run, err := newMigrationFixtureRunService(t, state).Create(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			assertRunSeedPermission(t, state, run, version == 177)
		})
	}
}
