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
	return func() {
		t.Helper()
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

// Reverse the entire post-v156 schema before cumulative legacy fixtures remove
// Thread tables. In particular, the v157 handoff guard must no longer reference
// threads, and image/attachment constraints must not survive in older schemas.
func removeSchemaV157ForTestStatements() []string {
	const next = "run_supervisor_tool_calls_v160_restore"
	const previous = "run_supervisor_tool_calls_v161_fixture"
	statements := []string{`PRAGMA foreign_keys=OFF;`, `PRAGMA legacy_alter_table=ON;`}
	statements = append(statements, removeSchemaV168QueueForTestStatements()...)
	statements = append(statements,
		`DROP TRIGGER trg_scheduled_job_observation_consent_insert;`,
		`DROP TRIGGER trg_scheduled_job_observation_consent_update_immutable;`,
		`DROP TRIGGER trg_scheduled_job_observation_consent_delete_immutable;`,
		`DROP INDEX idx_scheduled_job_observation_consents_run;`,
		`DROP TABLE scheduled_job_observation_consents;`)
	statements = append(statements, removeSchemaV162ForTestStatements()...)
	statements = append(statements,
		`DROP TRIGGER trg_risk_escalation_supervisor_authority_insert;`,
		`DROP TRIGGER trg_host_command_supervisor_envelope_immutable;`)
	rebuild := rebuildRiskEscalationSupervisorToolCalls(browserActionSupervisorToolCallCreate(next), next, previous)
	for i, statement := range rebuild {
		if statement == `INSERT INTO `+next+` SELECT * FROM `+previous+`;` {
			const columns = `run_id,turn,attempt_id,round,position,model_attempt,call_id,tool_name,payload_json,authority_json,status,result_json,error_code,created_at,completed_at,stream_response_id,stream_item_id,stream_call_id`
			rebuild[i] = `INSERT INTO ` + next + `(rowid,` + columns + `) SELECT rowid,` + columns + ` FROM ` + previous + `;`
		}
	}
	statements = append(statements, rebuild...)
	statements = append(statements,
		requireMigrationTrigger("trg_risk_escalation_supervisor_authority_insert", riskEscalationStatements),
		requireMigrationTrigger("trg_host_command_supervisor_envelope_immutable", riskEscalationStatements),
		`DROP TABLE thread_message_attachments;`, `DROP TABLE workspace_file_attachments;`,
		`DROP TRIGGER trg_thread_message_attachments_immutable;`,
		`ALTER TABLE thread_message_intents DROP COLUMN attachments_json;`,
		`ALTER TABLE run_supervisor_checkpoints DROP COLUMN pending_attachment_count;`,
		`DROP TRIGGER trg_git_remote_started_once;`, `DROP TRIGGER trg_git_mutation_started_once;`,
		`ALTER TABLE git_remote_operations DROP COLUMN started_at;`,
		`ALTER TABLE git_mutation_operations DROP COLUMN started_at;`,
		`DROP TABLE thread_message_images;`, `DROP TABLE workspace_image_attachments;`,
		`DROP TRIGGER trg_thread_message_images_immutable;`,
		`ALTER TABLE thread_message_intents DROP COLUMN images_json;`,
		`ALTER TABLE run_supervisor_checkpoints DROP COLUMN pending_image_count;`,
		`ALTER TABLE operator_steering_messages RENAME TO operator_steering_messages_v160_fixture;`,
		operatorSteeringStatements[0],
		`INSERT INTO operator_steering_messages (id,run_id,session_id,sequence,status,content,content_sha256,requested_by,session_message_id,created_at,committed_at,cancelled_at) SELECT id,run_id,session_id,sequence,status,content,content_sha256,requested_by,session_message_id,created_at,committed_at,cancelled_at FROM operator_steering_messages_v160_fixture;`,
		`DROP TABLE operator_steering_messages_v160_fixture;`, operatorSteeringStatements[1])
	for _, name := range []string{"trg_operator_steering_insert_binding", "trg_operator_steering_update_monotonic", "trg_operator_steering_commit_binding", "trg_operator_steering_delete_immutable"} {
		statements = append(statements, migrationTriggerBeforeForTest(name, 158))
	}
	for _, name := range []string{"trg_session_message_provenance_insert", "trg_run_execution_handoff_item_insert"} {
		statements = append(statements, "DROP TRIGGER "+name, migrationTriggerBeforeForTest(name, 157))
	}
	return append(statements, `DELETE FROM schema_migrations WHERE version BETWEEN 157 AND 169;`,
		`PRAGMA legacy_alter_table=OFF;`, `PRAGMA foreign_keys=ON;`)
}

// These inverse fixtures represent pre-v168 text-only messages. Reject modern
// revision/evidence data before dropping any of it. The caller's existing
// steering-table rebuild removes the four added columns and restores the old
// monotonic trigger; its Supervisor rebuild also removes v169 tool additions.
func removeSchemaV168QueueForTestStatements() []string {
	return []string{
		`CREATE TEMP TABLE legacy_fixture_empty_queue_history (n INTEGER CHECK(n=0));`,
		`INSERT INTO legacy_fixture_empty_queue_history SELECT count(*) FROM operator_steering_revisions;`,
		`INSERT INTO legacy_fixture_empty_queue_history SELECT count(*) FROM operator_message_attachment_evidence;`,
		`INSERT INTO legacy_fixture_empty_queue_history SELECT count(*) FROM operator_steering_messages
			WHERE revision<>0 OR edited_at IS NOT NULL OR original_content<>content
				OR original_content_sha256<>content_sha256;`,
		`DROP TABLE legacy_fixture_empty_queue_history;`,
		`DROP TRIGGER trg_operator_steering_update_monotonic;`,
		`DROP TABLE operator_steering_revisions;`,
		`DROP TABLE operator_message_attachment_evidence;`,
	}
}

// This is a fixture-only inverse to v161. The caller already disables foreign
// keys and rename propagation. It restores every changed contract, not just the
// triggers that happen to prevent an older fixture from renaming its tables.
// The caller's existing Supervisor rebuild also removes v165's source_search.
func removeSchemaV162ForTestStatements() []string {
	statements := []string{
		`DROP TRIGGER trg_web_fetch_failure_observation_handoff_item_insert;`,
		`DROP TRIGGER trg_run_execution_handoff_operation_insert;`,
		migrationTriggerBeforeForTest("trg_run_execution_handoff_operation_insert", 166),
	}
	for _, name := range []string{"trg_file_edit_auto_authorization_insert", "trg_file_edit_auto_authorization_update", "trg_file_edit_auto_authorization_delete", "trg_file_edit_auto_content_update", "trg_file_edit_auto_approval_insert", "trg_file_edit_auto_approval_update", "trg_file_edit_auto_apply_insert"} {
		statements = append(statements, "DROP TRIGGER "+name+";")
	}
	// Reject incompatible modern data rather than silently discarding authority.
	statements = append(statements,
		`CREATE TEMP TABLE legacy_fixture_empty_authority (n INTEGER CHECK(n=0));`,
		`INSERT INTO legacy_fixture_empty_authority SELECT count(*) FROM file_edit_auto_authorizations;`,
		`DROP TABLE legacy_fixture_empty_authority;`,
		`DROP TABLE file_edit_auto_authorizations;`)
	for _, name := range []string{"trg_command_runtime_job_insert_scope", "trg_command_runtime_job_insert_limit", "trg_command_runtime_job_update_transition", "trg_command_runtime_job_delete_immutable"} {
		statements = append(statements, "DROP TRIGGER "+name+";")
	}
	const jobs = "command_runtime_jobs_v162_restore"
	createJobs := strings.Replace(requireMigrationStatement("CREATE TABLE command_runtime_jobs_v142 (", debugFullAccessInheritanceStatements), "command_runtime_jobs_v142", jobs, 1)
	columns := strings.TrimSuffix(commandRuntimeJobColumns, ",\n\tpermission_runtime_epoch, permission_generation")
	if columns == commandRuntimeJobColumns {
		panic("legacy command runtime columns changed")
	}
	columns = "rowid,protocol_version," + columns
	statements = append(statements,
		`CREATE TEMP TABLE legacy_fixture_empty_grant (n INTEGER CHECK(n=0));`,
		`INSERT INTO legacy_fixture_empty_grant SELECT count(*) FROM command_runtime_jobs WHERE permission_runtime_epoch<>'' OR permission_generation<>0;`,
		`DROP TABLE legacy_fixture_empty_grant;`,
		createJobs,
		"INSERT INTO "+jobs+" ("+columns+") SELECT "+columns+" FROM command_runtime_jobs;",
		`DROP TABLE command_runtime_jobs;`,
		"ALTER TABLE "+jobs+" RENAME TO command_runtime_jobs;",
		requireMigrationStatement("CREATE INDEX idx_command_runtime_jobs_run_created", commandRuntimeStatements),
		requireMigrationStatement("CREATE INDEX idx_command_runtime_jobs_active", commandRuntimeStatements))
	for _, name := range []string{"trg_command_runtime_job_insert_scope", "trg_command_runtime_job_insert_limit", "trg_command_runtime_job_update_transition", "trg_command_runtime_job_delete_immutable"} {
		statements = append(statements, migrationTriggerBeforeForTest(name, 163))
	}
	const operations = "web_evidence_operations_v164_restore"
	createOperations := strings.Replace(requireMigrationStatement("CREATE TABLE web_evidence_operations (", webEvidenceStatements), "web_evidence_operations", operations, 1)
	const operationColumns = "rowid,key_digest,protocol_version,request_fingerprint,run_id,tool_name,response_json,created_at"
	statements = append(statements, createOperations,
		"INSERT INTO "+operations+" ("+operationColumns+") SELECT "+operationColumns+" FROM web_evidence_operations;",
		`DROP TABLE web_evidence_operations;`,
		"ALTER TABLE "+operations+" RENAME TO web_evidence_operations;",
		requireMigrationStatement("CREATE INDEX idx_web_evidence_operations_run", webEvidenceStatements),
		requireMigrationTrigger("trg_web_evidence_operation_immutable", webEvidenceStatements),
		requireMigrationTrigger("trg_web_evidence_operation_delete_immutable", webEvidenceStatements))
	return statements
}

// Current writers may seed only historical values. Both helpers restore the
// exact schema and ledger before the migration under test is allowed to run.
func addCurrentCommandGrantColumnsForLegacySeed(t testing.TB, state *SQLiteStore) func() {
	t.Helper()
	return withLegacySeedSchema(t, state, []string{
		`ALTER TABLE command_runtime_jobs ADD COLUMN permission_runtime_epoch TEXT NOT NULL DEFAULT '' CHECK(permission_runtime_epoch='');`,
		`ALTER TABLE command_runtime_jobs ADD COLUMN permission_generation INTEGER NOT NULL DEFAULT 0 CHECK(permission_generation=0);`,
	}, []string{
		`ALTER TABLE command_runtime_jobs DROP COLUMN permission_runtime_epoch;`,
		`ALTER TABLE command_runtime_jobs DROP COLUMN permission_generation;`,
	})
}

func addEmptyCurrentAutoAuthorizationForLegacySeed(t testing.TB, state *SQLiteStore) func() {
	t.Helper()
	return withLegacySeedSchema(t, state, []string{
		requireMigrationStatement("CREATE TABLE file_edit_auto_authorizations (", automaticFileEditMoveAuthorizationStatements),
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
