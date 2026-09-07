package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

func TestSchemaV151BackfillsProvableLegacyAgentAttribution(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v150-agent-attribution.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 150); err != nil {
		t.Fatal(err)
	}

	_, run := createStructuredToolTestRun(t, ctx, state,
		"preserve historical Supervisor actor")
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := state.BeginSupervisorTurn(ctx,
		acquireTestRunExecutionLease(t, ctx, state, run.ID), "persist before v151")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := toolgateway.NormalizeStructuredMemoryPayload(
		toolgateway.NoteCreateTool,
		json.RawMessage(`{"title":"v150","content":"preserve actor"}`))
	if err != nil {
		t.Fatal(err)
	}
	operationKey := runmutation.SupervisorToolOperationKey(run.ID,
		turn.Checkpoint.NextTurn, string(toolgateway.NoteCreateTool), string(payload))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1,
		Provider: "test", Model: "model"}
	if inserted, err := state.RecordSupervisorModelStarted(ctx, turn.Checkpoint,
		attempt); err != nil || !inserted {
		t.Fatalf("record model start: inserted=%t err=%v", inserted, err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint,
		attempt, llm.ChatResponse{Provider: "test", Model: "model",
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID,
				Name: string(toolgateway.NoteCreateTool), Arguments: payload}}})
	if err != nil {
		t.Fatal(err)
	}

	job := commandRuntimeMigrationJob(t, state,
		domain.RunExecutionPermissionFullAccess,
		commandruntimeadapter.HostUnsandboxed(testCommandRuntimeDigest("v151-adapter")))
	if _, replayed, err := state.PrepareCommandRuntimeJob(ctx, job); err != nil || replayed {
		t.Fatalf("prepare v150 Command Job replayed=%t err=%v", replayed, err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, job.RunID)
	if err != nil {
		t.Fatal(err)
	}

	if err := state.applyMigration(ctx, plan[150]); err != nil {
		t.Fatal(err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 151 {
		t.Fatalf("schema version=%d want=151 err=%v", version, err)
	}
	rounds, err := state.ListSupervisorToolRounds(ctx, checkpoint)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
		t.Fatalf("legacy Supervisor round=%#v err=%v", rounds, err)
	}
	call := rounds[0].Calls[0]
	if call.AgentID != turn.Agent.ID ||
		call.AgentAttemptID != turn.Checkpoint.AttemptID ||
		call.AgentAttribution != domain.AgentAttributionLegacyRoot {
		t.Fatalf("legacy Supervisor attribution=%#v", call)
	}
	jobAttribution, err := state.GetThreadCommandRuntimeJobAgentAttribution(ctx,
		threadRecord.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobAttribution.AgentID != job.RootAgentID ||
		jobAttribution.AgentAttemptID != "" ||
		jobAttribution.Source != domain.AgentAttributionLegacyRoot {
		t.Fatalf("legacy Command attribution=%#v", jobAttribution)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE command_runtime_job_agents
		SET attribution_source = 'recorded' WHERE job_id = ?`, job.ID); err == nil {
		t.Fatal("v151 allowed historical Command attribution mutation")
	}
	assertNoForeignKeyViolations(t, state.db)
}

func TestCleanInstallV151IncludesAgentAttributionLedgers(t *testing.T) {
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "clean-v151-agent-attribution.db"))
	defer state.Close()
	used, err := state.tryCleanInstallBaseline(t.Context(), migrationPlan())
	if err != nil || !used {
		t.Fatalf("v151 clean-install baseline used=%t err=%v", used, err)
	}
	for _, name := range []string{"run_supervisor_tool_call_agents",
		"command_runtime_job_agents"} {
		var count int
		if err := state.db.QueryRowContext(t.Context(), `SELECT COUNT(*)
			FROM sqlite_master WHERE type = 'table' AND name = ?`, name).
			Scan(&count); err != nil || count != 1 {
			t.Fatalf("clean-install table %s count=%d err=%v", name, count, err)
		}
	}
	assertNoForeignKeyViolations(t, state.db)
}
