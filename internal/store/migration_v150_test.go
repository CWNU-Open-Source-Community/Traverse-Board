package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
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
	state := openHistoricalTestDatabase(t,
		filepath.Join(t.TempDir(), "schema-v149-browser-actions.db"), 149)
	plan := migrationPlan()
	restoreLegacyInputs := addCurrentInputColumnsForLegacySeed(t, state)
	run := seedV149StructuredToolRun(t, state)
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
	restoreLegacyInputs()
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

// Seed old data under its genuine v149 constraints. The current application
// writer cannot manufacture a five-mode row; no historical trigger is relaxed.
func seedV149StructuredToolRun(t *testing.T, state *SQLiteStore) domain.Run {
	return seedLegacyStructuredToolRun(t, state, "ws-structured", domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionConservative)
}

func seedLegacyStructuredToolRun(t testing.TB, state *SQLiteStore, workspaceID string, phase domain.ExecutionPhase, permissionMode domain.RunExecutionPermissionMode) domain.Run {
	t.Helper()
	ctx, now := t.Context(), time.Now().UTC().Truncate(time.Millisecond)
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`INSERT INTO sessions(id,workspace_id,title,route,status,created_at,updated_at) VALUES('session-v149',?,'v149','code','active',?,?)`,
		`INSERT INTO missions(id,goal,profile,workspace_id,scope_json,created_at,updated_at) VALUES('mission-v149','preserve historical native operations','code',?,'{"network_mode":"disabled","workspace_id":"' || ? || '"}',?,?)`,
		`INSERT INTO runs(id,mission_id,session_id,status,config_json,budget_json,created_at,updated_at) VALUES('run-v149','mission-v149','session-v149','created','{"model_route":"mock/default"}','{"max_turns":5,"max_tokens":1000,"max_tool_calls":20}',?,?)`,
	} {
		args := []any{ts(now), ts(now)}
		if strings.HasPrefix(query, "INSERT INTO sessions") {
			args = append([]any{workspaceID}, args...)
		}
		if strings.HasPrefix(query, "INSERT INTO missions") {
			args = append([]any{workspaceID, workspaceID}, args...)
		}
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	run := domain.Run{ID: "run-v149", MissionID: "mission-v149", SessionID: "session-v149", Status: domain.RunCreated, Budget: domain.Budget{MaxTurns: 5, MaxTokens: 1000, MaxToolCalls: 20}, CreatedAt: now, UpdatedAt: now}
	mission := domain.Mission{ID: run.MissionID, Profile: domain.ProfileCode, WorkspaceID: workspaceID, Scope: domain.Scope{NetworkMode: "disabled", WorkspaceID: workspaceID}, CreatedAt: now, UpdatedAt: now}
	mode, err := domain.NewInitialRunModeSnapshot("mode-v149", run, mission, domain.ExecutionSurfaceCode, phase, "historical-operator", "historical mode", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertInitialRunModeSnapshotTx(ctx, tx, mode, run, mission); err != nil {
		t.Fatal(err)
	}
	profile, err := domain.NewInitialRunExecutionProfileSnapshot("profile-v149", run, mission, "historical-operator", "v149 profile", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertInitialRunExecutionProfileSnapshotTx(ctx, tx, profile, run, mission); err != nil {
		t.Fatal(err)
	}
	interaction, err := domain.NewInitialRunExecutionInteractionSnapshot("interaction-v149", run, mission, mode, profile, "historical-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertInitialRunExecutionInteractionSnapshotTx(ctx, tx, interaction, run, mission, mode, profile); err != nil {
		t.Fatal(err)
	}
	initial, err := domain.NewInitialRunExecutionPermissionSnapshot("unused-v2", run, mission, "historical-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	initialID := "permission-v149"
	if permissionMode != domain.RunExecutionPermissionConservative {
		initialID += "-initial"
	}
	legacy, err := initial.Next(initialID, domain.RunExecutionPermissionConservative, false, "historical-operator", "genuine historical permission tuple", now)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Revision = 1
	if err = insertRunExecutionPermissionSnapshotTx(ctx, tx, legacy); err != nil {
		t.Fatal(err)
	}
	if permissionMode != domain.RunExecutionPermissionConservative {
		legacy, err = legacy.Next("permission-v149", permissionMode, true, "historical-operator", "genuine historical permission selection", now)
		if err != nil {
			t.Fatal(err)
		}
		if err = insertRunExecutionPermissionSnapshotTx(ctx, tx, legacy); err != nil {
			t.Fatal(err)
		}
	}
	browser, err := domain.NewInitialRunBrowserCDPPermissionSnapshot("browser-v149", run, mission, "historical-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertInitialRunBrowserCDPPermissionSnapshotTx(ctx, tx, browser, run, mission); err != nil {
		t.Fatal(err)
	}
	if err = insertInitialRunInstructionSnapshotTx(ctx, tx, run, "historical-operator"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = syncRootAgentTx(ctx, tx, run, mission, rootAgentProjection{Status: domain.AgentReady}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = createAgentGraphSnapshotTx(ctx, tx, run); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stored, err := state.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}
