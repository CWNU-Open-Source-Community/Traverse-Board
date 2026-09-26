package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

func TestSchemaV161PreservesToolRowsActorBindingsAndAdmitsHistory(t *testing.T) {
	st := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "history-migration.db"))
	defer st.Close()
	if err := applyMigrationPrefixForTest(t.Context(), st, migrationPlan(), 160); err != nil {
		t.Fatal(err)
	}
	defer addCurrentSteeringForLegacySeed(t, st)()
	_, run := createStructuredToolTestRun(t, t.Context(), st, "Preserve tool history migration")
	if _, err := application.NewRunService(st).Start(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := st.BeginSupervisorTurn(t.Context(), acquireTestRunExecutionLease(t, t.Context(), st, run.ID), "old source")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool, json.RawMessage(`{"title":"before161","content":"preserve exact 中文 source"}`))
	if err != nil {
		t.Fatal(err)
	}
	key := runmutation.SupervisorToolOperationKey(run.ID, turn.Checkpoint.NextTurn, "note_create", string(payload))
	callID, err := runmutation.SupervisorToolCallID(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "test", Model: "model"}
	if _, err := st.RecordSupervisorModelStarted(t.Context(), turn.Checkpoint, attempt); err != nil {
		t.Fatal(err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	cp, err := st.RecordSupervisorModelCompleted(t.Context(), turn.Checkpoint, attempt, llm.ChatResponse{Provider: "test", Model: "model", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, ToolCalls: []llm.ToolCall{{ID: callID, Name: "note_create", Arguments: payload}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordSupervisorToolExecutionStarted(t.Context(), cp, callID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RecordSupervisorToolResult(t.Context(), cp, domain.SupervisorToolResult{CallID: callID, Status: domain.SupervisorToolCompleted, ResultJSON: `{"stdout":"原始已保存结果","stderr":""}`, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	before, err := st.ListSupervisorToolRounds(t.Context(), cp)
	if err != nil {
		t.Fatal(err)
	}
	var rowID int64
	if err := st.db.QueryRow(`SELECT rowid FROM run_supervisor_tool_calls WHERE run_id=? AND call_id=?`, run.ID, callID).Scan(&rowID); err != nil {
		t.Fatal(err)
	}
	var actorBefore string
	if err := st.db.QueryRow(`SELECT json_object('agent_id',agent_id,'attempt',agent_attempt_id,'source',attribution_source,'created_at',created_at) FROM run_supervisor_tool_call_agents WHERE run_id=? AND call_id=?`, run.ID, callID).Scan(&actorBefore); err != nil {
		t.Fatal(err)
	}
	historyPayload, err := toolgateway.NormalizeHistoryRecallPayload(toolgateway.HistorySearchTool, json.RawMessage(`{"query":"原始"}`))
	if err != nil {
		t.Fatal(err)
	}
	historyKey := runmutation.SupervisorToolOperationKey(run.ID, cp.NextTurn, "history_search", string(historyPayload))
	historyCallID, err := runmutation.SupervisorToolCallID(historyKey, 2)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Number, attempt.ToolRound, attempt.Outcome = 2, 1, ""
	if _, err := st.RecordSupervisorModelStarted(t.Context(), cp, attempt); err != nil {
		t.Fatal(err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	response := llm.ChatResponse{Provider: "test", Model: "model", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, ToolCalls: []llm.ToolCall{{ID: historyCallID, Name: "history_search", Arguments: historyPayload}}}
	if _, err := st.RecordSupervisorModelCompleted(t.Context(), cp, attempt, response); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("v160 must reject new tool at actual SQL allowlist: %v", err)
	}
	if err := st.applyMigration(t.Context(), migrationPlan()[160]); err != nil {
		t.Fatal(err)
	}
	after, err := st.ListSupervisorToolRounds(t.Context(), cp)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("old stored tool records/actor changed: %v", err)
	}
	var rowAfter int64
	var actorAfter string
	if err := st.db.QueryRow(`SELECT rowid FROM run_supervisor_tool_calls WHERE run_id=? AND call_id=?`, run.ID, callID).Scan(&rowAfter); err != nil || rowAfter != rowID {
		t.Fatalf("rowid changed: %d/%d %v", rowID, rowAfter, err)
	}
	if err := st.db.QueryRow(`SELECT json_object('agent_id',agent_id,'attempt',agent_attempt_id,'source',attribution_source,'created_at',created_at) FROM run_supervisor_tool_call_agents WHERE run_id=? AND call_id=?`, run.ID, callID).Scan(&actorAfter); err != nil || actorAfter != actorBefore {
		t.Fatalf("attribution row changed: %s/%s %v", actorBefore, actorAfter, err)
	}
	assertSupervisorToolCallSchemaV150(t, st)
	assertNoForeignKeyViolations(t, st.db)
	var actorSchema string
	if err := st.db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='run_supervisor_tool_call_agents'`).Scan(&actorSchema); err != nil || strings.Contains(actorSchema, "_v160") || strings.Contains(actorSchema, "_v161") {
		t.Fatalf("actor FK retargeted: %s %v", actorSchema, err)
	}
	if _, err := st.RecordSupervisorModelCompleted(t.Context(), cp, attempt, response); err != nil {
		t.Fatalf("v161 rejects actual normalized history call: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE run_supervisor_tool_calls SET authority_json='{}' WHERE run_id=? AND call_id=?`, run.ID, historyCallID); err == nil {
		t.Fatal("history gained a fake workspace authority envelope")
	}
	if _, err := st.db.Exec(`UPDATE run_supervisor_tool_calls SET stream_call_id='changed' WHERE run_id=? AND call_id=?`, run.ID, callID); err == nil {
		t.Fatal("original stream identity guard was lost")
	}
	assertNoForeignKeyViolations(t, st.db)
}
