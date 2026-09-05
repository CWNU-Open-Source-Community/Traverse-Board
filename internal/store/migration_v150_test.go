package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

var supervisorToolCallSchemaObjectsV150 = []struct {
	kind string
	name string
}{
	{"index", "idx_run_supervisor_tool_calls_pending"},
	{"index", "idx_supervisor_tool_stream_item_identity"},
	{"index", "idx_supervisor_tool_stream_call_identity"},
	{"trigger", "trg_standard_code_supervisor_ledger_insert"},
	{"trigger", "trg_supervisor_tool_call_model_attempt"},
	{"trigger", "trg_supervisor_tool_round_completion"},
	{"trigger", "trg_supervisor_tool_stream_identity_insert"},
	{"trigger", "trg_supervisor_tool_stream_identity_immutable"},
	{"trigger", "trg_risk_escalation_supervisor_authority_insert"},
	{"trigger", "trg_host_command_supervisor_envelope_immutable"},
}

// removeSchemaV150ForTestStatements restores the v149 Supervisor tool-call
// ledger at the head of the cumulative historical downgrade fixture chain.
// Merely deleting the migration row is not sufficient: v150 changes the table
// constraints, and exact older-schema tests must not observe browser actions
// or authority-bound MCP calls before replaying v150.
func removeSchemaV150ForTestStatements() []string {
	const tableName = "run_supervisor_tool_calls_v149_restore"
	const backupName = "run_supervisor_tool_calls_v150_fixture"
	statements := append(removeSchemaV151ForTestStatements(), []string{
		`DROP TRIGGER trg_risk_escalation_supervisor_authority_insert;`,
		`DROP TRIGGER trg_host_command_supervisor_envelope_immutable;`,
	}...)
	rebuild := rebuildRiskEscalationSupervisorToolCalls(
		riskEscalationSupervisorToolCallCreate(tableName), tableName, backupName)
	legacyCopy := `INSERT INTO ` + tableName + ` SELECT * FROM ` + backupName + `;`
	v149Copy := `INSERT INTO ` + tableName + `
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		 payload_json, authority_json, status, result_json, error_code, created_at, completed_at,
		 stream_response_id, stream_item_id, stream_call_id)
		SELECT run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
			payload_json,
			CASE WHEN tool_name = 'mcp_tool_call' THEN '' ELSE authority_json END,
			status, result_json, error_code, created_at, completed_at,
			stream_response_id, stream_item_id, stream_call_id
		FROM ` + backupName + `
		WHERE tool_name NOT IN ('browser_status', 'browser_navigate', 'browser_snapshot',
			'browser_click', 'browser_type', 'browser_screenshot');`
	replaced := false
	for index := range rebuild {
		if rebuild[index] == legacyCopy {
			rebuild[index] = v149Copy
			replaced = true
		}
	}
	if !replaced {
		panic("current Supervisor v150 fixture copy statement is unavailable")
	}
	statements = append(statements, rebuild...)
	return append(statements,
		requireMigrationTrigger("trg_risk_escalation_supervisor_authority_insert",
			riskEscalationStatements),
		requireMigrationTrigger("trg_host_command_supervisor_envelope_immutable",
			riskEscalationStatements),
		`DELETE FROM schema_migrations WHERE version = 150`)
}

// removeSchemaV151ForTestStatements restores the v150 schema before older
// cumulative downgrade fixtures rebuild either underlying ledger.
func removeSchemaV151ForTestStatements() []string {
	return []string{
		`DROP TRIGGER trg_command_runtime_job_agent_immutable`,
		`DROP INDEX idx_command_runtime_job_agents_actor`,
		`DROP TABLE command_runtime_job_agents`,
		`DROP TRIGGER trg_supervisor_tool_call_agent_immutable`,
		`DROP INDEX idx_supervisor_tool_call_agents_actor`,
		`DROP TABLE run_supervisor_tool_call_agents`,
		`DELETE FROM schema_migrations WHERE version = 151`,
	}
}

func assertSupervisorToolCallSchemaV150(t *testing.T, state *SQLiteStore) {
	t.Helper()
	var tableSQL string
	if err := state.db.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'run_supervisor_tool_calls'`).Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	for _, name := range toolgateway.BrowserActionToolNames() {
		if count := strings.Count(tableSQL, "'"+string(name)+"'"); count != 3 {
			t.Fatalf("browser action %q appears %d times in ledger constraints, want registry plus both authority sets: %s",
				name, count, tableSQL)
		}
	}
	if count := strings.Count(tableSQL, "'mcp_tool_call'"); count != 3 {
		t.Fatalf("MCP tool appears %d times in ledger constraints, want registry plus both authority sets: %s",
			count, tableSQL)
	}
	for _, object := range supervisorToolCallSchemaObjectsV150 {
		var count int
		if err := state.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master
			WHERE type = ? AND name = ?`, object.kind, object.name).Scan(&count); err != nil || count != 1 {
			t.Fatalf("schema object %s %s count=%d err=%v", object.kind, object.name, count, err)
		}
	}
}

func TestSchemaV150RebuildsAuthorityBoundBrowserAndMCPSupervisorLedger(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v149-browser-actions.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 149); err != nil {
		t.Fatal(err)
	}
	_, run := createStructuredToolTestRun(t, ctx, state, "preserve v149 Supervisor call")
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := state.BeginSupervisorTurn(ctx,
		acquireTestRunExecutionLease(t, ctx, state, run.ID), "persist before v150")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool,
		json.RawMessage(`{"title":"v149","content":"preserve me"}`))
	if err != nil {
		t.Fatal(err)
	}
	operationKey := runmutation.SupervisorToolOperationKey(run.ID, turn.Checkpoint.NextTurn,
		string(toolgateway.NoteCreateTool), string(payload))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1,
		Provider: "test", Model: "model"}
	if inserted, err := state.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil || !inserted {
		t.Fatalf("record model start: inserted=%t err=%v", inserted, err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt,
		llm.ChatResponse{Provider: "test", Model: "model",
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID, Name: string(toolgateway.NoteCreateTool),
				Arguments: payload}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		 payload_json, authority_json, status, result_json, error_code, created_at, completed_at)
		VALUES (?, ?, ?, 1, 2, 1, 'mcp-v149-unbound', 'mcp_tool_call',
		 '{"version":"mcp-client.v1","server_id":"docs","tool_name":"lookup","capability_fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","arguments":{}}',
		 '', 'pending', '', '', ?, NULL)`, checkpoint.RunID, checkpoint.NextTurn,
		checkpoint.AttemptID, ts(time.Now().UTC())); err != nil {
		t.Fatalf("create legacy unbound MCP call: %v", err)
	}
	if err := state.applyMigration(ctx, plan[149]); err != nil {
		t.Fatal(err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 150 {
		t.Fatalf("schema version=%d want=150 err=%v", version, err)
	}
	assertSupervisorToolCallSchemaV150(t, state)
	rounds, err := state.ListSupervisorToolRounds(ctx, checkpoint)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 2 ||
		rounds[0].Calls[0].CallID != callID ||
		rounds[0].Calls[1].AuthorityJSON != legacyUnboundSupervisorMCPAuthority {
		t.Fatalf("v149 Supervisor call was not preserved: %#v err=%v", rounds, err)
	}
	if _, err := mcp.DecodeSupervisorCallAuthority(
		json.RawMessage(rounds[0].Calls[1].AuthorityJSON)); err == nil {
		t.Fatal("legacy unbound MCP marker became executable authority")
	}

	now := ts(time.Now().UTC())
	if _, err := state.db.ExecContext(ctx, `INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		 payload_json, authority_json, status, result_json, error_code, created_at, completed_at)
		VALUES (?, ?, ?, 1, 3, 1, 'browser-v150-authorized', 'browser_status',
		 '{"version":"browser_status.v1"}', '{}', 'pending', '', '', ?, NULL)`,
		checkpoint.RunID, checkpoint.NextTurn, checkpoint.AttemptID, now); err != nil {
		t.Fatalf("authority-bound browser action was rejected by v150 ledger: %v", err)
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		 payload_json, authority_json, status, result_json, error_code, created_at, completed_at)
		VALUES (?, ?, ?, 1, 4, 1, 'browser-v150-unbound', 'browser_status',
		 '{"version":"browser_status.v1"}', '', 'pending', '', '', ?, NULL)`,
		checkpoint.RunID, checkpoint.NextTurn, checkpoint.AttemptID, now); err == nil {
		t.Fatal("v150 SQLite ledger accepted a browser action without authority")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE run_supervisor_tool_calls SET stream_call_id = 'changed'
		WHERE run_id = ? AND call_id = ?`, checkpoint.RunID, callID); err == nil {
		t.Fatal("v150 rebuild lost the stream identity immutability trigger")
	}
	assertNoForeignKeyViolations(t, state.db)
}

func TestCleanInstallV150IncludesBrowserAndMCPSupervisorLedger(t *testing.T) {
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "clean-v150-browser-actions.db"))
	defer state.Close()
	used, err := state.tryCleanInstallBaseline(t.Context(), migrationPlan())
	if err != nil || !used {
		t.Fatalf("v150 clean-install baseline used=%t err=%v", used, err)
	}
	if version, err := state.SchemaVersion(t.Context()); err != nil || version != LatestSchemaVersion {
		t.Fatalf("clean schema version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	assertSupervisorToolCallSchemaV150(t, state)
	assertNoForeignKeyViolations(t, state.db)
}
