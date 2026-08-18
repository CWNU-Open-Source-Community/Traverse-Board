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
