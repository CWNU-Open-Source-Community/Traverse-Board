package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
)

func TestSupervisorToolRejectionPreservedAtProtocolFailure(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	f.response.ToolCalls = []llm.ToolCall{{ID: "wire-rejected", Name: "browser_snapshot", Arguments: json.RawMessage(`{"version":"agent-browser-runtime.v1"}`)}}
	reason, err := domain.NewSupervisorToolRequestRepairReason(0, "browser action protocol version is invalid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordSupervisorProtocolFailure(t.Context(), f.turn.Checkpoint, f.attempt, f.response, reason, true); err != nil {
		t.Fatal(err)
	}
	var diagnostic string
	err = f.store.db.QueryRow(`SELECT diagnostic_json FROM run_supervisor_tool_rejections WHERE run_id=? AND turn=? AND attempt_id=? AND model_attempt=?`,
		f.turn.Run.ID, f.turn.Checkpoint.NextTurn, f.turn.Checkpoint.AttemptID, f.attempt.Number).Scan(&diagnostic)
	if err != nil {
		t.Fatalf("rejected native request was received but has no durable diagnostic: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(diagnostic), &saved); err != nil || saved["version"] != "tool_request_rejection.v1" {
		t.Fatalf("missing structured rejection diagnostic: %s %v", diagnostic, err)
	}
	var executable int
	if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM run_supervisor_tool_calls WHERE run_id=?`, f.turn.Run.ID).Scan(&executable); err != nil || executable != 0 {
		t.Fatalf("rejected diagnostic created executable tool work: %d %v", executable, err)
	}
}

func rejectedTestResponse() llm.ChatResponse {
	calls := []llm.ToolCall{{ID: "received-private-id", Name: "browser_snapshot", Arguments: json.RawMessage(`{"version":"agent-browser-runtime.v1","unknown":"pw7"}`)}}
	return llm.ChatResponse{ToolCalls: calls, Usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, ToolRequestRejection: llm.NewToolRequestRejection(calls, []llm.ToolSpec{{Name: "browser_snapshot", Parameters: json.RawMessage(`{"properties":{"version":{"const":"browser_snapshot.v2"}}}`)}})}
}

func TestSupervisorToolRejectionAtomicIdempotentPrivateAndDurable(t *testing.T) {
	f := newProviderReplayFixture(t)
	f.start(t)
	response := rejectedTestResponse()
	reason, _ := domain.NewSupervisorToolRequestRepairReason(0, "browser action protocol version is invalid")
	if _, err := f.store.db.Exec(`CREATE TRIGGER inject_rejection_failure BEFORE INSERT ON run_supervisor_tool_rejections BEGIN SELECT RAISE(ABORT,'injected'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RecordSupervisorProtocolFailure(t.Context(), f.turn.Checkpoint, f.attempt, response, reason, true); err == nil {
		t.Fatal("rejection write failure was accepted")
	}
	eventList, err := f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil || countRunEventType(eventList, events.ModelFailedEvent) != 0 || countRunEventType(eventList, events.ProtocolRepairRequestedEvent) != 0 {
		t.Fatal("diagnostic failure leaked terminal or repair event", err)
	}
	cp, found, err := f.store.GetSupervisorCheckpoint(t.Context(), f.turn.Run.ID)
	if err != nil || !found || cp.TotalTokens != 0 || cp.RepairPhase != domain.ProtocolRepairNone {
		t.Fatalf("diagnostic failure charged usage/repair: %+v %v", cp, err)
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER inject_rejection_failure`); err != nil {
		t.Fatal(err)
	}
	cp, err = f.store.RecordSupervisorProtocolFailure(t.Context(), f.turn.Checkpoint, f.attempt, response, reason, true)
	if err != nil {
		t.Fatal(err)
	}
	if cp.TotalTokens != 5 || cp.RepairPhase != domain.ProtocolRepairPending {
		t.Fatalf("failure was not charged: %+v", cp)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.store.RecordSupervisorProtocolFailure(t.Context(), cp, f.attempt, response, reason, true); err != nil {
			t.Fatal("exact replay failed", err)
		}
	}
	changed := response
	changed.ToolCalls = append([]llm.ToolCall(nil), response.ToolCalls...)
	changed.ToolCalls[0].Arguments = json.RawMessage(`{"version":"browser_snapshot.v1","unknown":"pw7"}`)
	if _, err := f.store.RecordSupervisorProtocolFailure(t.Context(), cp, f.attempt, changed, reason, true); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeInvalidArgument {
		t.Fatal("detached evidence accepted", err)
	}
	changed.ToolRequestRejection = nil
	if _, err := f.store.RecordSupervisorProtocolFailure(t.Context(), cp, f.attempt, changed, reason, true); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatal("different received batch overwrote original", err)
	}
	changed = response
	changed.ToolRequestRejection = llm.NewToolRequestRejection(response.ToolCalls, []llm.ToolSpec{{Name: "browser_snapshot", Parameters: json.RawMessage(`{"properties":{"version":{"const":"browser_snapshot.v3"}}}`)}})
	if _, err := f.store.RecordSupervisorProtocolFailure(t.Context(), cp, f.attempt, changed, reason, true); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatal("later offered schema replaced original", err)
	}
	var saved string
	if err := f.store.db.QueryRow(`SELECT diagnostic_json FROM run_supervisor_tool_rejections WHERE run_id=?`, f.turn.Run.ID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(saved, "pw7") || strings.Contains(saved, "received-private-id") || !strings.Contains(saved, "agent-browser-runtime.v1") || !strings.Contains(saved, "browser_snapshot.v2") {
		t.Fatal("unsafe or incomplete private evidence", saved)
	}
	eventList, err = f.store.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil || countRunEventType(eventList, events.ModelFailedEvent) != 1 || countRunEventType(eventList, events.ProtocolRepairRequestedEvent) != 1 {
		t.Fatal("duplicate terminal/repair event", err)
	}
	public, _ := json.Marshal(eventList)
	if strings.Contains(string(public), "tool_request_rejection.v1") || strings.Contains(string(public), "agent-browser-runtime.v1") {
		t.Fatal("diagnostic leaked into ordinary history", string(public))
	}
	for _, table := range []string{"run_supervisor_tool_calls", "run_supervisor_tool_rounds", "run_supervisor_provider_replay"} {
		var count int
		if err := f.store.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE run_id=?", f.turn.Run.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rejected request became native work: %s=%d %v", table, count, err)
		}
	}
	if _, err := f.store.db.Exec(`UPDATE run_supervisor_tool_rejections SET diagnostic_json='{}'`); err == nil {
		t.Fatal("immutable diagnostic update accepted")
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, err = Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	var reopened string
	if err := f.store.db.QueryRow(`SELECT diagnostic_json FROM run_supervisor_tool_rejections WHERE run_id=?`, f.turn.Run.ID).Scan(&reopened); err != nil || reopened != saved {
		t.Fatal("reopen lost original evidence", err)
	}
	if _, err := f.store.RecordSupervisorProtocolFailure(t.Context(), cp, f.attempt, response, reason, true); err != nil {
		t.Fatal("reopen broke exact terminal replay", err)
	}
	stale := cp
	stale.LeaseGeneration++
	if _, err := f.store.RecordSupervisorProtocolFailure(t.Context(), stale, f.attempt, response, reason, true); err == nil {
		t.Fatal("stale owner wrote diagnostics")
	}
}

func TestSchemaV174PreservesV173FailureWithoutInventingEvidence(t *testing.T) {
	st := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "rejections-upgrade.db"))
	defer st.Close()
	if err := applyMigrationPrefixForTest(t.Context(), st, migrationPlan(), 173); err != nil {
		t.Fatal(err)
	}
	f := providerReplayFixtureAtStore(t, st)
	f.start(t)
	// Model the already-sealed v173 lifecycle failure; it never had arguments.
	response := llm.ChatResponse{Text: `{"invalid":true}`, Usage: f.response.Usage}
	if _, err := st.RecordSupervisorProtocolFailure(t.Context(), f.turn.Checkpoint, f.attempt, response, "invalid lifecycle", true); err != nil {
		t.Fatal(err)
	}
	before, err := st.ListRunEvents(t.Context(), f.turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.applyMigration(t.Context(), migrationPlan()[173]); err != nil {
		t.Fatal(err)
	}
	after, err := st.ListRunEvents(t.Context(), f.turn.Run.ID)
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if err != nil || string(beforeJSON) != string(afterJSON) {
		t.Fatal("migration rewrote legacy failure", err)
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM run_supervisor_tool_rejections`).Scan(&count); err != nil || count != 0 {
		t.Fatal("migration fabricated historical arguments", count, err)
	}
	if _, err := st.RecordSupervisorProtocolFailure(t.Context(), f.turn.Checkpoint, f.attempt, rejectedTestResponse(), "invalid lifecycle", true); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatal("legacy terminal allowed fresh historical evidence", err)
	}
}
