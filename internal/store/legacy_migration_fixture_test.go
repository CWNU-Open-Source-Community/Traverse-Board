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
	statements := []string{`PRAGMA foreign_keys=OFF;`, `PRAGMA legacy_alter_table=ON;`,
		`DROP TRIGGER trg_risk_escalation_supervisor_authority_insert;`,
		`DROP TRIGGER trg_host_command_supervisor_envelope_immutable;`}
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
	return append(statements, `DELETE FROM schema_migrations WHERE version BETWEEN 157 AND 161;`,
		`PRAGMA legacy_alter_table=OFF;`, `PRAGMA foreign_keys=ON;`)
}
