package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Only opt-in fixture setup uses these snapshots. Open/migration contract tests
// and the independent historical schema oracles still build their own databases.
// Snapshots contain closed database bytes, never live connections or identities.
type testDatabaseFixtures struct {
	mu    sync.Mutex
	plans map[string]map[int][]byte
}

var sharedTestDatabaseFixtures testDatabaseFixtures

// Version zero is the current clean-install schema. Positive versions are built
// through the real historical migrations, without using the current baseline.
func (cache *testDatabaseFixtures) snapshot(ctx context.Context, version int) ([]byte, error) {
	plan := migrationPlan()
	if version < 0 || version > len(plan) {
		return nil, fmt.Errorf("invalid fixture schema version %d", version)
	}
	key := migrationPlanDigest(plan)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.plans == nil {
		cache.plans = make(map[string]map[int][]byte)
	}
	versions := cache.plans[key]
	if versions == nil {
		versions = make(map[int][]byte)
		cache.plans[key] = versions
	}
	if data, ok := versions[version]; ok {
		return data, nil
	}
	previous := 0
	for candidate := range versions {
		if candidate > previous && candidate < version {
			previous = candidate
		}
	}
	directory, err := os.MkdirTemp("", "traverse-test-schema-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "schema.db")
	if previous > 0 {
		if err := writeTestDatabaseSnapshot(path, versions[previous]); err != nil {
			return nil, err
		}
	}
	state, err := openTestDatabaseWithoutMigration(path)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	switch {
	case version == 0:
		// Migrate creates schema but does not mint Open's recovery identity.
		err = state.Migrate(ctx)
	case previous == 0:
		err = applyMigrationPrefixForTest(ctx, state, plan, version)
	default:
		for _, item := range plan[previous:version] {
			if err = state.applyMigration(ctx, item); err != nil {
				break
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err := state.Close(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	versions[version] = data
	return data, nil
}

func writeTestDatabaseSnapshot(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func openTestDatabaseWithoutMigration(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db, home: filepath.Dir(path)}, nil
}

func openCurrentTestDatabase(t testing.TB, path string) *SQLiteStore {
	t.Helper()
	copyTestDatabaseFixture(t, path, 0)
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state
}

func openHistoricalTestDatabase(t testing.TB, path string, version int) *SQLiteStore {
	t.Helper()
	if version < 1 {
		t.Fatal("historical fixture requires a positive version")
	}
	copyTestDatabaseFixture(t, path, version)
	state, err := openTestDatabaseWithoutMigration(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state
}

func copyTestDatabaseFixture(t testing.TB, path string, version int) {
	t.Helper()
	data, err := sharedTestDatabaseFixtures.snapshot(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTestDatabaseSnapshot(path, data); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseFixtureCopiesIsolateWritesIdentityAndReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first := openCurrentTestDatabase(t, path)
	second := openCurrentTestDatabase(t, filepath.Join(t.TempDir(), "state.db"))
	id := first.DataStoreID()
	if id == "" || second.DataStoreID() == "" || id == second.DataStoreID() {
		t.Fatal("independent copies must have distinct recovery identities")
	}
	var firstSeed, secondSeed string
	if err := first.db.QueryRow(`SELECT value FROM provider_setting WHERE key=?`, dataStoreIdentitySeedKey).Scan(&firstSeed); err != nil {
		t.Fatal(err)
	}
	if err := second.db.QueryRow(`SELECT value FROM provider_setting WHERE key=?`, dataStoreIdentitySeedKey).Scan(&secondSeed); err != nil || firstSeed == secondSeed {
		t.Fatalf("copies inherited a shared seed: %v", err)
	}
	if err := first.SetProviderSetting(t.Context(), "fixture.isolation", "first"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := second.GetProviderSetting(t.Context(), "fixture.isolation"); err != nil || found {
		t.Fatalf("write escaped its fixture: found=%t err=%v", found, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if value, found, err := reopened.GetProviderSetting(t.Context(), "fixture.isolation"); err != nil || !found || value != "first" || reopened.DataStoreID() != id {
		t.Fatalf("reopen lost durable state or identity: value=%q found=%t err=%v", value, found, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement := openCurrentTestDatabase(t, path)
	if replacement.DataStoreID() == id {
		t.Fatal("replacement reused the prior recovery identity")
	}
	if _, found, err := replacement.GetProviderSetting(t.Context(), "fixture.isolation"); err != nil || found {
		t.Fatalf("replacement inherited old data: found=%t err=%v", found, err)
	}
}

func TestDatabaseFixtureHistoricalCopiesKeepExactPrefixAndIsolateUpgrade(t *testing.T) {
	const version = 128
	var cache testDatabaseFixtures
	if _, err := cache.snapshot(t.Context(), version-1); err != nil {
		t.Fatal(err)
	}
	// Exercise extending an existing prefix, independently of test order.
	data, err := cache.snapshot(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	var copies []*SQLiteStore
	for range 2 {
		path := filepath.Join(t.TempDir(), "state.db")
		if err := writeTestDatabaseSnapshot(path, data); err != nil {
			t.Fatal(err)
		}
		state, err := openTestDatabaseWithoutMigration(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = state.Close() })
		copies = append(copies, state)
	}
	first, second := copies[0], copies[1]
	oracle := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "oracle.db"))
	if err := applyMigrationPrefixForTest(t.Context(), oracle, migrationPlan(), version); err != nil {
		t.Fatal(err)
	}
	want, err := sqliteSchemaDigest(t.Context(), oracle.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []*SQLiteStore{first, second} {
		got, err := sqliteSchemaDigest(t.Context(), state.db)
		if err != nil || got != want {
			t.Fatalf("historical schema differs from raw oracle: %v", err)
		}
		ledger, err := state.loadAppliedMigrations(t.Context())
		if err != nil || len(ledger) != version {
			t.Fatalf("historical ledger entries=%d err=%v", len(ledger), err)
		}
		if err := validateMigrationPlan(migrationPlan(), ledger); err != nil {
			t.Fatal(err)
		}
		assertNoForeignKeyViolations(t, state.db)
	}
	if err := first.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertLatestMigrationLedger(t, first, migrationPlan())
	if got, err := second.SchemaVersion(t.Context()); err != nil || got != version {
		t.Fatalf("upgrade escaped its copy: version=%d err=%v", got, err)
	}
}

func TestDatabaseFixtureConcurrentCopiesUseIndependentFiles(t *testing.T) {
	var cache testDatabaseFixtures
	const count = 4
	directory := t.TempDir()
	results := make(chan error, count)
	start := make(chan struct{})
	for index := range count {
		go func() {
			<-start
			data, err := cache.snapshot(t.Context(), 0)
			if err != nil {
				results <- err
				return
			}
			path := filepath.Join(directory, fmt.Sprintf("copy-%d.db", index))
			if err := writeTestDatabaseSnapshot(path, data); err != nil {
				results <- err
				return
			}
			state, err := Open(path)
			if err == nil {
				err = state.SetProviderSetting(t.Context(), "fixture.writer", fmt.Sprint(index))
				closeErr := state.Close()
				if err == nil {
					err = closeErr
				}
			}
			results <- err
		}()
	}
	close(start)
	for range count {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		return
	}
	for index := range count {
		state, err := Open(filepath.Join(directory, fmt.Sprintf("copy-%d.db", index)))
		if err != nil {
			t.Fatal(err)
		}
		value, found, err := state.GetProviderSetting(t.Context(), "fixture.writer")
		closeErr := state.Close()
		if err != nil || closeErr != nil || !found || value != fmt.Sprint(index) {
			t.Fatalf("concurrent copy %d lost isolation: value=%q found=%t err=%v close=%v", index, value, found, err, closeErr)
		}
	}
}
