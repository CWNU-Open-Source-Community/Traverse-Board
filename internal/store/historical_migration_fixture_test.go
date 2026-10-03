package store

import "testing"

// openHistoricalMigrationFixture starts from the actual immutable migration
// prefix. It does not strip a current database's ledger or reinterpret v2 data
// as older rows. The caller seeds only values supported by that schema and keeps
// its existing reopen, row-preservation, foreign-key and receipt assertions.
func openHistoricalMigrationFixture(t testing.TB, path string, version int) (*SQLiteStore, error) {
	t.Helper()
	state := openHistoricalTestDatabase(t, path, version)
	ledger, err := state.loadAppliedMigrations(t.Context())
	if err != nil {
		_ = state.Close()
		return nil, err
	}
	if err := validateMigrationPlan(migrationPlan(), ledger); err != nil {
		_ = state.Close()
		return nil, err
	}
	actual, err := state.SchemaVersion(t.Context())
	if err != nil || actual != version {
		t.Fatalf("historical prefix=%d want=%d err=%v", actual, version, err)
	}
	return state, nil
}
