package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

func TestSupervisorToolLedgerAllowsEveryAdvertisedDefinition(t *testing.T) {
	st, err := Open(t.TempDir() + "/supervisor-tool-registry.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var tableSQL string
	if err := st.db.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'run_supervisor_tool_calls'`).Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	for _, definition := range toolgateway.AllSupervisorToolDefinitions() {
		if !strings.Contains(tableSQL, "'"+string(definition.Name)+"'") {
			t.Fatalf("advertised Supervisor tool %q is absent from the durable ledger constraint: %s",
				definition.Name, tableSQL)
		}
	}
}

func addCurrentSupervisorToolStreamColumns(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	// Historical fixtures intentionally exercise the current writer before
	// reopening through Open. Supply only the columns the current writer needs;
	// the historical ledger rebuilds then discard them and v129 recreates the
	// complete indexed/guarded contract during the real upgrade.
	for _, statement := range itemStreamToolIdentityStatements[:3] {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("add current item-stream compatibility column: %v", err)
		}
	}
}

func TestSchemaV113PreservesCallsAndAdmitsDebugTerminal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v112-supervisor-tools.db")
	db, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	);`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	legacy := &SQLiteStore{db: db, home: filepath.Dir(path)}
	for _, item := range migrationPlan()[:112] {
		if err := legacy.applyMigration(ctx, item); err != nil {
			_ = legacy.Close()
			t.Fatalf("apply v112 migration %d: %v", item.Version, err)
		}
	}
	// The current writer always supplies the Go-issued authority column. Add it
	// only while constructing a representative legacy call; migration v115
	// deliberately ignores this compatibility column when it rebuilds the
	// authoritative ledger, just as it does for an untouched v112/v113 store.
	if _, err := db.ExecContext(ctx, `ALTER TABLE run_supervisor_tool_calls
		ADD COLUMN authority_json TEXT NOT NULL DEFAULT '';`); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	addCurrentSupervisorToolStreamColumns(t, ctx, db)

	_, run, err := application.NewRunService(legacy).Create(ctx, application.CreateRunRequest{
		Goal: "preserve v112 Supervisor tools", Profile: "code",
		Budget: domain.Budget{MaxTurns: 5, MaxToolCalls: 20},
	})
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if _, err := application.NewRunService(legacy).Start(ctx, run.ID); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	turn, err := legacy.BeginSupervisorTurn(ctx,
		acquireTestRunExecutionLease(t, ctx, legacy, run.ID), "persist a v112 call")
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	payload, err := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool,
		json.RawMessage(`{"title":"v112","content":"preserve me"}`))
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	operationKey := runmutation.SupervisorToolOperationKey(run.ID, turn.Checkpoint.NextTurn,
		string(toolgateway.NoteCreateTool), string(payload))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{
		Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "test", Model: "model",
	}
	if inserted, err := legacy.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil || !inserted {
		_ = legacy.Close()
		t.Fatalf("record v112 model start: inserted=%t err=%v", inserted, err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := legacy.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt, llm.ChatResponse{
		Provider: "test", Model: "model",
		Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		ToolCalls: []llm.ToolCall{{
			ID: callID, Name: string(toolgateway.NoteCreateTool), Arguments: payload,
		}},
	})
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	rounds, err := upgraded.ListSupervisorToolRounds(ctx, checkpoint)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 ||
		rounds[0].Calls[0].CallID != callID || rounds[0].Calls[0].ToolName != string(toolgateway.NoteCreateTool) {
		t.Fatalf("v112 Supervisor call was not preserved: %#v err=%v", rounds, err)
	}
	if _, err := upgraded.db.ExecContext(ctx, `INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name, payload_json,
		 status, result_json, error_code, created_at, completed_at)
		VALUES (?, ?, ?, 1, 2, 1, ?, ?, ?, ?, '', '', ?, NULL)`,
		checkpoint.RunID, checkpoint.NextTurn, checkpoint.AttemptID, "debug-terminal-v113",
		string(toolgateway.DebugTerminalTool), `{"version":"debug_terminal.v1","action":"read","cursor":0,"max_bytes":1024,"wait_milliseconds":0}`,
		domain.SupervisorToolPending, ts(time.Now().UTC())); err != nil {
		t.Fatalf("insert debug_terminal after v113 migration: %v", err)
	}
	var count int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_supervisor_tool_calls
		WHERE run_id = ? AND tool_name = ?`, checkpoint.RunID, toolgateway.DebugTerminalTool).Scan(&count); err != nil || count != 1 {
		t.Fatalf("debug_terminal durable call count=%d err=%v", count, err)
	}
	var violation string
	if err := upgraded.db.QueryRowContext(ctx, `PRAGMA foreign_key_check;`).Scan(&violation); err != sql.ErrNoRows {
		t.Fatalf("foreign key violation=%q err=%v", violation, err)
	}
}

func TestSchemaV116AndV120PreserveAuthorityAndAdmitRuntimeTools(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v115-supervisor-tools.db")
	db, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	);`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	legacy := &SQLiteStore{db: db, home: filepath.Dir(path)}
	for _, item := range migrationPlan()[:115] {
		if err := legacy.applyMigration(ctx, item); err != nil {
			_ = legacy.Close()
			t.Fatalf("apply v115 migration %d: %v", item.Version, err)
		}
	}
	addCurrentSupervisorToolStreamColumns(t, ctx, db)
	_, run, err := application.NewRunService(legacy).Create(ctx, application.CreateRunRequest{
		Goal: "preserve v115 workspace authority", Profile: "code",
		Budget: domain.Budget{MaxTurns: 5, MaxToolCalls: 20},
	})
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if _, err := application.NewRunService(legacy).Start(ctx, run.ID); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	turn, err := legacy.BeginSupervisorTurn(ctx,
		acquireTestRunExecutionLease(t, ctx, legacy, run.ID), "persist workspace authority")
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	permission, err := legacy.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	authorityWorkspaceID := turn.Mission.WorkspaceID
	if authorityWorkspaceID == "" {
		authorityWorkspaceID = "workspace-v115"
	}
	authority, err := toolgateway.NewAgentCodeCallAuthority(toolgateway.AgentCodeCapabilityContext{
		RunID: run.ID, MissionID: turn.Mission.ID, RootAgentID: turn.Agent.ID,
		WorkspaceID: authorityWorkspaceID, RootFingerprint: strings.Repeat("a", 64),
		Surface: turn.Mode.Surface, Phase: turn.Mode.Phase, Role: turn.Agent.Role,
		Profile: turn.Mode.Profile, PermissionMode: permission.Mode,
		ModeRevision: turn.Mode.Revision, PermissionRevision: permission.Revision,
	}, run.SessionID)
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	encodedAuthority, err := toolgateway.EncodeAgentCodeCallAuthority(authority)
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	payload, err := toolgateway.NormalizeSupervisorToolPayload(toolgateway.WorkspaceListTool,
		json.RawMessage(`{"version":"agent-code-tools.v1","path":"","limit":10}`))
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	operationKey := runmutation.SupervisorToolOperationKey(run.ID, turn.Checkpoint.NextTurn,
		string(toolgateway.WorkspaceListTool), string(payload))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{
		Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "test", Model: "model",
	}
	if inserted, err := legacy.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil || !inserted {
		_ = legacy.Close()
		t.Fatalf("record v115 model start: inserted=%t err=%v", inserted, err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := legacy.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt,
		llm.ChatResponse{Provider: "test", Model: "model",
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID, Name: string(toolgateway.WorkspaceListTool),
				Arguments: payload, Authority: encodedAuthority}}})
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
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
	if _, err := upgraded.db.ExecContext(ctx, `INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		 payload_json, authority_json, status, result_json, error_code, created_at, completed_at)
		VALUES (?, ?, ?, 1, 2, 1, ?, ?, ?, '', ?, '', '', ?, NULL)`, checkpoint.RunID,
		checkpoint.NextTurn, checkpoint.AttemptID, "command-runtime-v116",
		string(toolgateway.CommandRuntimeTool),
		`{"version":"command-runtime.v2","action":"list"}`,
		domain.SupervisorToolPending, ts(time.Now().UTC())); err != nil {
		t.Fatalf("insert command_runtime after v116 migration: %v", err)
	}
	if _, err := upgraded.db.ExecContext(ctx, `INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		 payload_json, authority_json, status, result_json, error_code, created_at, completed_at)
		VALUES (?, ?, ?, 1, 3, 1, ?, ?, ?, '', ?, '', '', ?, NULL)`, checkpoint.RunID,
		checkpoint.NextTurn, checkpoint.AttemptID, "mcp-tool-v120",
		string(toolgateway.MCPToolCallTool),
		`{"version":"mcp-client.v1","server_id":"fixture","tool_name":"lookup","capability_fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","arguments":{}}`,
		domain.SupervisorToolPending, ts(time.Now().UTC())); err != nil {
		t.Fatalf("insert mcp_tool_call after v120 migration: %v", err)
	}
	rounds, err := upgraded.ListSupervisorToolRounds(ctx, checkpoint)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 3 {
		t.Fatalf("v116/v120 Supervisor calls=%#v err=%v", rounds, err)
	}
	if rounds[0].Calls[0].AuthorityJSON != string(encodedAuthority) ||
		rounds[0].Calls[1].ToolName != string(toolgateway.CommandRuntimeTool) ||
		rounds[0].Calls[1].AuthorityJSON != "" ||
		rounds[0].Calls[2].ToolName != string(toolgateway.MCPToolCallTool) ||
		rounds[0].Calls[2].AuthorityJSON != "" {
		t.Fatalf("v116/v120 authority preservation failed: %#v", rounds[0].Calls)
	}
}
