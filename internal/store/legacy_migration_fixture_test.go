package store

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Current writers can seed text-only historical fixtures, but the real upgrade
// must start from the historical column set. The returned function verifies that
// none of the temporary input counts carries data, removes them, and checks the
// original columns, exact migration prefix, and foreign keys before upgrading.
func addCurrentInputColumnsForLegacySeed(t testing.TB, state *SQLiteStore) func() {
	t.Helper()
	ctx := t.Context()
	type column struct {
		Name       string
		Type       string
		NotNull    int
		Default    any
		PrimaryKey int
	}
	columns := func(table string) []column {
		t.Helper()
		rows, err := state.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []column
		for rows.Next() {
			var ordinal int
			var c column
			if err := rows.Scan(&ordinal, &c.Name, &c.Type, &c.NotNull, &c.Default, &c.PrimaryKey); err != nil {
				t.Fatal(err)
			}
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	ledger, err := state.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMigrationPlan(migrationPlan(), ledger); err != nil {
		t.Fatal(err)
	}
	tables := []struct {
		name   string
		fields []string
	}{
		{"run_supervisor_checkpoints", []string{"pending_image_count", "pending_attachment_count"}},
		{"operator_steering_messages", []string{"image_count", "attachment_count"}},
	}
	before := make(map[string][]column)
	for _, table := range tables {
		before[table.name] = columns(table.name)
		for _, field := range table.fields {
			if _, err := state.db.ExecContext(ctx, "ALTER TABLE "+table.name+" ADD COLUMN "+field+" INTEGER NOT NULL DEFAULT 0 CHECK("+field+"=0)"); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Current queue writers and readers also inspect v168's revision identity.
	// No backfill here: legacy rows keep an empty original identity, and the
	// historical monotonic trigger must not observe a fixture UPDATE.
	for _, statement := range []string{
		`ALTER TABLE operator_steering_messages ADD COLUMN revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0);`,
		`ALTER TABLE operator_steering_messages ADD COLUMN original_content TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE operator_steering_messages ADD COLUMN original_content_sha256 TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE operator_steering_messages ADD COLUMN edited_at TEXT;`,
	} {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	restoreSteering := addCurrentSteeringForLegacySeed(t, state)
	return func() {
		t.Helper()
		restoreSteering()
		for _, statement := range []string{
			`CREATE TEMP TABLE legacy_fixture_empty_revision_identity (n INTEGER CHECK(n=0));`,
			`INSERT INTO legacy_fixture_empty_revision_identity SELECT count(*) FROM operator_steering_messages
				WHERE revision<>0 OR edited_at IS NOT NULL
					OR (original_content<>'' AND original_content<>content)
					OR (original_content_sha256<>'' AND original_content_sha256<>content_sha256);`,
			`DROP TABLE legacy_fixture_empty_revision_identity;`,
			`ALTER TABLE operator_steering_messages DROP COLUMN revision;`,
			`ALTER TABLE operator_steering_messages DROP COLUMN original_content;`,
			`ALTER TABLE operator_steering_messages DROP COLUMN original_content_sha256;`,
			`ALTER TABLE operator_steering_messages DROP COLUMN edited_at;`,
		} {
			if _, err := state.db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("remove current revision fixture with %q: %v", statement, err)
			}
		}
		for _, table := range tables {
			for _, field := range table.fields {
				var nonzero int
				if err := state.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table.name+" WHERE "+field+"<>0").Scan(&nonzero); err != nil || nonzero != 0 {
					t.Fatalf("legacy %s.%s has new input data: %d %v", table.name, field, nonzero, err)
				}
				if _, err := state.db.ExecContext(ctx, "ALTER TABLE "+table.name+" DROP COLUMN "+field); err != nil {
					t.Fatal(err)
				}
			}
			if got := columns(table.name); !reflect.DeepEqual(got, before[table.name]) {
				t.Fatalf("legacy %s columns changed: %#v -> %#v", table.name, before[table.name], got)
			}
		}
		after, err := state.loadAppliedMigrations(ctx)
		if err != nil || !reflect.DeepEqual(after, ledger) {
			t.Fatalf("seed changed migration prefix: %v", err)
		}
		if err := verifySQLiteForeignKeys(ctx, state.db); err != nil {
			t.Fatal(err)
		}
	}
}

// Current model writers inspect the v171 steering identity even when seeding
// an older, text-only migration fixture. This temporary schema is never
// recorded in the migration ledger and is removed before historical checks.
func addCurrentSteeringForLegacySeed(t testing.TB, state *SQLiteStore) func() {
	t.Helper()
	ctx := t.Context()
	for _, statement := range midTurnSteeringStatements[:3] {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("add current steering fixture with %q: %v", statement, err)
		}
	}
	return func() {
		t.Helper()
		for _, statement := range removeCurrentSteeringForLegacySeedStatements() {
			if _, err := state.db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("remove current steering fixture with %q: %v", statement, err)
			}
		}
	}
}

// Reject committed corrections or claims, then remove the fixture-only v171
// steering identity. Shared by every legacy seed that installs it.
func removeCurrentSteeringForLegacySeedStatements() []string {
	return []string{
		`CREATE TEMP TABLE legacy_fixture_empty_steering (n INTEGER CHECK(n=0));`,
		`INSERT INTO legacy_fixture_empty_steering SELECT count(*) FROM operator_steering_midturn_claims;`,
		`INSERT INTO legacy_fixture_empty_steering SELECT count(*) FROM operator_steering_messages
			WHERE delivery_mode<>'next_turn' OR target_attempt_id<>'';`,
		`DROP TABLE legacy_fixture_empty_steering;`,
		`DROP TABLE operator_steering_midturn_claims;`,
		`ALTER TABLE operator_steering_messages DROP COLUMN delivery_mode;`,
		`ALTER TABLE operator_steering_messages DROP COLUMN target_attempt_id;`,
	}
}

func migrationTriggerBeforeForTest(name string, version int) string {
	var prior string
	for _, migration := range migrationPlan() {
		if migration.Version >= version {
			break
		}
		for _, statement := range migration.Statements {
			fields := strings.Fields(statement)
			if len(fields) >= 3 && fields[0] == "CREATE" && fields[1] == "TRIGGER" && fields[2] == name {
				prior = statement
			}
		}
	}
	if prior == "" {
		panic(fmt.Sprintf("missing pre-v%d trigger %s", version, name))
	}
	return prior
}

// Current writers may seed only historical values. Both helpers restore the
// exact schema and ledger before the migration under test is allowed to run.
func addCurrentCommandGrantColumnsForLegacySeed(t testing.TB, state *SQLiteStore) func() {
	t.Helper()
	return withLegacySeedSchema(t, state, []string{
		`ALTER TABLE command_runtime_jobs ADD COLUMN permission_runtime_epoch TEXT NOT NULL DEFAULT '' CHECK(permission_runtime_epoch='');`,
		`ALTER TABLE command_runtime_jobs ADD COLUMN permission_generation INTEGER NOT NULL DEFAULT 0 CHECK(permission_generation=0);`,
		`ALTER TABLE command_runtime_jobs ADD COLUMN run_authorization_fence INTEGER NOT NULL DEFAULT 0 CHECK(run_authorization_fence=0);`,
	}, []string{
		`ALTER TABLE command_runtime_jobs DROP COLUMN permission_runtime_epoch;`,
		`ALTER TABLE command_runtime_jobs DROP COLUMN permission_generation;`,
		`ALTER TABLE command_runtime_jobs DROP COLUMN run_authorization_fence;`,
	})
}

func addEmptyCurrentAutoAuthorizationForLegacySeed(t testing.TB, state *SQLiteStore) func() {
	t.Helper()
	return withLegacySeedSchema(t, state, []string{
		requireMigrationStatement("CREATE TABLE file_edit_auto_authorizations (", automaticFileEditMoveAuthorizationStatements),
		`ALTER TABLE file_edit_auto_authorizations ADD COLUMN run_authorization_fence INTEGER NOT NULL DEFAULT 0 CHECK(run_authorization_fence=0);`,
		`CREATE TRIGGER legacy_fixture_no_automatic_authority BEFORE INSERT ON file_edit_auto_authorizations BEGIN SELECT RAISE(ABORT, 'historical fixture cannot contain automatic authority'); END;`,
	}, []string{
		`CREATE TEMP TABLE legacy_fixture_empty_authority (n INTEGER CHECK(n=0));`,
		`INSERT INTO legacy_fixture_empty_authority SELECT count(*) FROM file_edit_auto_authorizations;`,
		`DROP TABLE legacy_fixture_empty_authority;`,
		`DROP TABLE file_edit_auto_authorizations;`,
	})
}

func withLegacySeedSchema(t testing.TB, state *SQLiteStore, setup, restore []string) func() {
	t.Helper()
	before := legacyFixtureSchema(t, state)
	ledger, err := state.loadAppliedMigrations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMigrationPlan(migrationPlan(), ledger); err != nil {
		t.Fatal(err)
	}
	execute := func(statements []string) {
		for _, statement := range statements {
			if _, err := state.db.ExecContext(t.Context(), statement); err != nil {
				t.Fatalf("legacy seed schema %q: %v", statement, err)
			}
		}
	}
	execute(setup)
	return func() {
		t.Helper()
		execute(restore)
		if after := legacyFixtureSchema(t, state); !reflect.DeepEqual(after, before) {
			t.Fatal("legacy seed changed the historical schema")
		}
		after, err := state.loadAppliedMigrations(t.Context())
		if err != nil || !reflect.DeepEqual(after, ledger) {
			t.Fatalf("legacy seed changed migration prefix: %v", err)
		}
		if err := verifySQLiteForeignKeys(t.Context(), state.db); err != nil {
			t.Fatal(err)
		}
	}
}
