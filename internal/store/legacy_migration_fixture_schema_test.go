package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

// Compare all persisted schema objects, including constraints and triggers.
// SQLite quotes renamed table identifiers and retains formatting differences
// after DROP COLUMN; neither changes the restored contract.
func legacyFixtureSchema(t testing.TB, state *SQLiteStore) map[string]string {
	t.Helper()
	rows, err := state.db.QueryContext(t.Context(), `SELECT type, name, sql FROM sqlite_schema
		WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	var names []string
	for rows.Next() {
		var kind, name, statement string
		if err := rows.Scan(&kind, &name, &statement); err != nil {
			t.Fatal(err)
		}
		result[kind+":"+name] = statement
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for key, statement := range result {
		for _, name := range names {
			statement = strings.ReplaceAll(statement, `"`+name+`"`, name)
		}
		statement = strings.Join(strings.Fields(statement), " ")
		statement = strings.NewReplacer("( ", "(", " )", ")", ", ", ",", " ,", ",").Replace(statement)
		result[key] = statement
	}
	return result
}

func legacyFixtureRows(t testing.TB, state *SQLiteStore, table string) [][]any {
	t.Helper()
	rows, err := state.db.QueryContext(t.Context(), "SELECT rowid,* FROM "+table+" ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLegacyFixtureRestoresExactV156SchemaAndRows(t *testing.T) {
	ctx := t.Context()
	oracle := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "real-v156.db"))
	if err := applyMigrationPrefixForTest(ctx, oracle, migrationPlan(), 156); err != nil {
		t.Fatal(err)
	}
	state, err := Open(filepath.Join(t.TempDir(), "downgraded-v156.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, run := createStructuredToolTestRun(t, ctx, state, "preserve fixture rows")
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := state.BeginSupervisorTurn(ctx, acquireTestRunExecutionLease(t, ctx, state, run.ID), "historical input")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool,
		json.RawMessage(`{"title":"v156","content":"preserve row and actor"}`))
	if err != nil {
		t.Fatal(err)
	}
	operation := runmutation.SupervisorToolOperationKey(run.ID, turn.Checkpoint.NextTurn, string(toolgateway.NoteCreateTool), string(payload))
	callID, err := runmutation.SupervisorToolCallID(operation, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "test", Model: "model"}
	if inserted, err := state.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil || !inserted {
		t.Fatalf("record model start: %t %v", inserted, err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	if _, err := state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt,
		llm.ChatResponse{Provider: "test", Model: "model", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID, Name: string(toolgateway.NoteCreateTool), Arguments: payload}}}); err != nil {
		t.Fatal(err)
	}
	job := commandRuntimeMigrationJob(t, state, domain.RunExecutionPermissionFullAccess,
		commandruntimeadapter.HostUnsandboxed(strings.Repeat("a", 64)))
	if _, replayed, err := state.PrepareCommandRuntimeJob(ctx, job); err != nil || replayed {
		t.Fatalf("prepare job: %t %v", replayed, err)
	}
	for _, statement := range []string{
		`UPDATE run_supervisor_tool_calls SET rowid=23;`,
		`UPDATE command_runtime_jobs SET rowid=47,version=version+1,state='running',pid=123,process_group=123,job_assigned_at_creation=1,started_at=created_at;`,
	} {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO web_evidence_operations
		(rowid,key_digest,protocol_version,request_fingerprint,run_id,tool_name,response_json,created_at)
		VALUES (71,?,'web_evidence_operation.v1',?,?,'web_search','{}','2026-09-21T00:00:00Z')`,
		strings.Repeat("b", 64), strings.Repeat("c", 64), run.ID); err != nil {
		t.Fatal(err)
	}
	tables := []string{"run_supervisor_tool_calls", "run_supervisor_tool_call_agents", "command_runtime_jobs", "command_runtime_job_agents", "web_evidence_operations"}
	before := map[string][][]any{}
	for _, table := range tables {
		before[table] = legacyFixtureRows(t, state, table)
		if len(before[table]) != 1 {
			t.Fatalf("%s fixture is not populated: %d", table, len(before[table]))
		}
	}
	// Only v163's trailing empty grant pair is absent from the old job schema.
	before["command_runtime_jobs"][0] = before["command_runtime_jobs"][0][:len(before["command_runtime_jobs"][0])-2]
	ledgerBefore, err := state.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV157ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("restore v156 with %q: %v", statement, err)
		}
	}
	wantSchema, gotSchema := legacyFixtureSchema(t, oracle), legacyFixtureSchema(t, state)
	for key, want := range wantSchema {
		if got := gotSchema[key]; got != want {
			t.Errorf("v156 schema %s:\n got: %s\nwant: %s", key, got, want)
		}
	}
	for key := range gotSchema {
		if _, exists := wantSchema[key]; !exists {
			t.Errorf("unexpected v156 schema object %s", key)
		}
	}
	ledgerAfter, err := state.loadAppliedMigrations(ctx)
	if err != nil || len(ledgerAfter) != 156 {
		t.Fatalf("v156 migration prefix: count=%d err=%v", len(ledgerAfter), err)
	}
	if err := validateMigrationPlan(migrationPlan(), ledgerAfter); err != nil {
		t.Fatal(err)
	}
	for version, value := range ledgerAfter {
		if !reflect.DeepEqual(value, ledgerBefore[version]) {
			t.Fatalf("migration %d was rewritten", version)
		}
	}
	for _, table := range tables {
		if after := legacyFixtureRows(t, state, table); !reflect.DeepEqual(after, before[table]) {
			t.Errorf("%s data or rowid changed:\n got: %#v\nwant: %#v", table, after, before[table])
		}
	}
	for pragma, want := range map[string]int{"foreign_keys": 1, "legacy_alter_table": 0} {
		var got int
		if err := state.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
			t.Errorf("restored %s=%d want=%d err=%v", pragma, got, want, err)
		}
	}
	assertNoForeignKeyViolations(t, state.db)
}
