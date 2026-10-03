package application

import (
	"database/sql"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

// seedRetainedNativePermission uses a private test database's retained v1 row.
// It never calls the retired public writer, and does not prove historical
// migration or three-mode support. The lower-level helper restores the exact
// live insert trigger before the scenario under test runs.
func seedRetainedNativePermission(t testing.TB, database string, state *store.SQLiteStore, runID string, mode domain.RunExecutionPermissionMode) domain.RunExecutionPermissionSnapshot {
	t.Helper()
	if mode.IsApprovalMode() {
		t.Fatal("current modes must use the real public writer")
	}
	current, err := state.GetRunExecutionPermission(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", database+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	return seedRetainedCommandPermission(t, db, current, mode)
}
