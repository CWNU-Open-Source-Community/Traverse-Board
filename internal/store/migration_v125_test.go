package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const migration97CleanupReceiptTriggerPrefix = "CREATE TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_insert"

// removeSchemaV125ForTestStatements restores a v124 database and is the head
// of the cumulative downgrade fixture chain used by historical migration tests.
// Schema v125 deliberately leaves the canonical v97 trigger in place, so only
// its migration-history row must be removed.
func removeSchemaV125ForTestStatements() []string {
	return append(removeSchemaV126ForTestStatements(),
		`DELETE FROM schema_migrations WHERE version = 125`)
}

func TestSchemaV125UpgradesCanonicalV124Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "canonical-v124.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV125ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 124 {
		t.Fatalf("downgraded schema version=%d want=124 err=%v", version, err)
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
		t.Fatalf("schema version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	var storedTrigger string
	if err := upgraded.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`,
		"trg_sandbox_docker_lifecycle_cleanup_receipt_insert").Scan(&storedTrigger); err != nil {
		t.Fatal(err)
	}
	canonicalTrigger := migration97CleanupReceiptTrigger(t, migrationPlan()[96].Statements)
	if normalizeSQLiteDDL(storedTrigger) != normalizeSQLiteDDL(canonicalTrigger) {
		t.Fatal("migration 125 changed the canonical migration 97 trigger")
	}
}

func TestMigration97ReleasedChecksumsArePinned(t *testing.T) {
	item := migrationPlan()[96]
	const canonical = "e279b320761a7ae9ff7af17dbb9df0eceb80702d434f9dca30eca6825892559d"
	if checksum := migrationChecksum(item); checksum != canonical {
		t.Fatalf("released migration 97 changed: checksum=%q", checksum)
	}

	legacyStatements, _ := legacyWindowsPreviewMigration97Statements(t)
	legacyItem := item
	legacyItem.Statements = legacyStatements
	if checksum := migrationChecksum(legacyItem); checksum != legacyWindowsPreviewMigration97Checksum {
		t.Fatalf("legacy migration 97 fixture checksum=%q", checksum)
	}
	if !acceptedMigrationChecksum(item, legacyWindowsPreviewMigration97Checksum) {
		t.Fatal("released Windows preview migration 97 checksum was rejected")
	}
	wrongVersion := item
	wrongVersion.Version = 98
	if acceptedMigrationChecksum(wrongVersion, legacyWindowsPreviewMigration97Checksum) {
		t.Fatal("migration 97 legacy checksum escaped its exact version")
	}
	if acceptedMigrationChecksum(item, strings.Repeat("0", 64)) {
		t.Fatal("unknown migration 97 checksum was accepted")
	}
	if err := validateMigrationPlan(migrationPlan(), map[int]appliedMigration{
		97: {Name: "wrong migration name", Checksum: legacyWindowsPreviewMigration97Checksum},
	}); err == nil {
		t.Fatal("legacy checksum bypassed the exact migration name")
	}

	canonicalTrigger := migration97CleanupReceiptTrigger(t, item.Statements)
	if got := legacyDockerLifecycleCleanupTriggerCompatibilityStatements[1]; got != canonicalTrigger {
		t.Fatal("migration 125 repair trigger drifted from canonical migration 97")
	}
}

func TestSQLiteUpgradesLegacyWindowsPreviewV97AndRepairsCleanupTrigger(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-v97.db")
	db, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		t.Fatal(err)
	}
	legacy := &SQLiteStore{db: db, home: filepath.Dir(path)}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	);`); err != nil {
		t.Fatal(err)
	}
	plan := migrationPlan()
	for _, item := range plan[:97] {
		if err := legacy.applyMigration(ctx, item); err != nil {
			t.Fatalf("apply legacy migration %d: %v", item.Version, err)
		}
	}
	createdAt := time.Date(2026, 8, 14, 3, 30, 0, 0, time.UTC)
	if err := legacy.SaveWorkspace(ctx, WorkspaceRecord{
		ID: "workspace-legacy-v97", Name: "legacy-v97-preview",
		RootPath: `C:\preview`, CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}

	legacyStatements, legacyTrigger := legacyWindowsPreviewMigration97Statements(t)
	legacyItem := plan[96]
	legacyItem.Statements = legacyStatements
	if checksum := migrationChecksum(legacyItem); checksum != legacyWindowsPreviewMigration97Checksum {
		t.Fatalf("legacy migration 97 checksum=%q", checksum)
	}
	if _, err := db.Exec(`DROP TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_insert`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(legacyTrigger); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE schema_migrations SET checksum = ? WHERE version = 97`,
		legacyWindowsPreviewMigration97Checksum); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("legacy v97 database did not upgrade in place: %v", err)
	}
	defer upgraded.Close()
	version, err := upgraded.SchemaVersion(ctx)
	if err != nil || version != LatestSchemaVersion {
		t.Fatalf("version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	workspaces, err := upgraded.ListWorkspaces(ctx)
	if err != nil || len(workspaces) != 1 || workspaces[0].ID != "workspace-legacy-v97" {
		t.Fatalf("legacy workspace was not preserved: %#v err=%v", workspaces, err)
	}

	var recordedChecksum string
	if err := upgraded.db.QueryRowContext(ctx,
		`SELECT checksum FROM schema_migrations WHERE version = 97`).Scan(&recordedChecksum); err != nil {
		t.Fatal(err)
	}
	if recordedChecksum != legacyWindowsPreviewMigration97Checksum {
		t.Fatalf("legacy migration history was rewritten: checksum=%q", recordedChecksum)
	}
	var repairRows int
	if err := upgraded.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 125`).Scan(&repairRows); err != nil {
		t.Fatal(err)
	}
	if repairRows != 1 {
		t.Fatalf("migration 125 rows=%d", repairRows)
	}

	var storedTrigger string
	if err := upgraded.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`,
		"trg_sandbox_docker_lifecycle_cleanup_receipt_insert").Scan(&storedTrigger); err != nil {
		t.Fatal(err)
	}
	canonicalTrigger := migration97CleanupReceiptTrigger(t, plan[96].Statements)
	if normalizeSQLiteDDL(storedTrigger) != normalizeSQLiteDDL(canonicalTrigger) {
		t.Fatal("migration 125 did not restore the canonical cleanup receipt trigger")
	}
	if strings.Contains(storedTrigger, "final.lease_id = NEW.lease_id") ||
		strings.Contains(storedTrigger, "final.lease_generation = NEW.lease_generation") {
		t.Fatal("legacy cleanup receipt trigger predicates remain after migration 125")
	}

	var integrity string
	if err := upgraded.db.QueryRowContext(ctx, `PRAGMA integrity_check;`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("upgraded legacy database integrity=%q", integrity)
	}
}

func legacyWindowsPreviewMigration97Statements(t testing.TB) ([]string, string) {
	t.Helper()
	statements := append([]string(nil), migrationPlan()[96].Statements...)
	canonicalBinding := "\t\t\t\tAND final.resource_generation = NEW.resource_generation\n" +
		"\t\t\t\tAND final.state = 'cleaned'"
	legacyBinding := "\t\t\t\tAND final.resource_generation = NEW.resource_generation\n" +
		"\t\t\t\tAND final.lease_id = NEW.lease_id AND final.owner_id = NEW.owner_id\n" +
		"\t\t\t\tAND final.lease_generation = NEW.lease_generation\n" +
		"\t\t\t\tAND final.state = 'cleaned'"
	for index, statement := range statements {
		if !strings.HasPrefix(strings.TrimSpace(statement), migration97CleanupReceiptTriggerPrefix) {
			continue
		}
		if strings.Count(statement, canonicalBinding) != 1 {
			t.Fatal("canonical migration 97 cleanup trigger binding is unavailable")
		}
		legacyTrigger := strings.Replace(statement, canonicalBinding, legacyBinding, 1)
		statements[index] = legacyTrigger
		return statements, legacyTrigger
	}
	t.Fatal("migration 97 cleanup receipt trigger is unavailable")
	return nil, ""
}

func migration97CleanupReceiptTrigger(t testing.TB, statements []string) string {
	t.Helper()
	for _, statement := range statements {
		if strings.HasPrefix(strings.TrimSpace(statement), migration97CleanupReceiptTriggerPrefix) {
			return statement
		}
	}
	t.Fatal("migration 97 cleanup receipt trigger is unavailable")
	return ""
}

func normalizeSQLiteDDL(statement string) string {
	return strings.TrimSuffix(strings.TrimSpace(statement), ";")
}
