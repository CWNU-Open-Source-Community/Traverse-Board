package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

func newToolRequestRepairTest(t *testing.T, marked bool) (*SQLiteStore, string, domain.SupervisorTurn, domain.RunExecutionLease) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tool-request-repair.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, run := createStructuredToolTestRun(t, ctx, st, "repair a rejected tool request")
	if _, err := application.NewRunService(st).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, st, run.ID)
	turn, err := st.BeginSupervisorTurn(ctx, lease, "original pending input")
	if err != nil {
		t.Fatal(err)
	}
	a := startToolRepairModel(t, st, turn.Checkpoint, 0, 0)
	reason := "invalid lifecycle JSON"
	response := llm.ChatResponse{Text: "invalid JSON", Usage: llm.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}}
	if marked {
		reason, err = domain.NewSupervisorToolRequestRepairReason(0, "unknown tool argument field")
		if err != nil {
			t.Fatal(err)
		}
		response.ToolCalls = []llm.ToolCall{{ID: "original-rejected-provider-call", Name: "note_create", Arguments: json.RawMessage(`{"unknown":true}`)}}
	}
	turn.Checkpoint, err = st.RecordSupervisorProtocolFailure(ctx, turn.Checkpoint, a, response, reason, true)
	if err != nil || turn.Checkpoint.RepairPhase != domain.ProtocolRepairPending {
		t.Fatalf("request repair: %+v %v", turn.Checkpoint, err)
	}
	rounds, err := st.ListSupervisorToolRounds(ctx, turn.Checkpoint)
	if err != nil || len(rounds) != 0 {
		t.Fatalf("rejected batch was persisted: %+v %v", rounds, err)
	}
	return st, path, turn, lease
}

func startToolRepairModel(t *testing.T, st *SQLiteStore, cp domain.SupervisorCheckpoint, repair, round int) llm.ModelAttempt {
	t.Helper()
	n, transport, err := st.NextSupervisorModelAttempt(context.Background(), cp, repair, round)
	if err != nil {
		t.Fatal(err)
	}
	a := llm.ModelAttempt{Number: n, SupervisorAttemptID: cp.AttemptID, TransportAttempt: transport, MaxAttempts: 1,
		ProtocolRepair: repair, ToolRound: round, Provider: "test", Model: "model"}
	if inserted, err := st.RecordSupervisorModelStarted(context.Background(), cp, a); err != nil || !inserted {
		t.Fatalf("start: %t %v", inserted, err)
	}
	return a
}

func toolRepairNote(t *testing.T, cp domain.SupervisorCheckpoint) llm.ToolCall {
	t.Helper()
	payload, err := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool,
		json.RawMessage(`{"title":"Correction","content":"This is the corrected tool request."}`))
	if err != nil {
		t.Fatal(err)
	}
	key := runmutation.SupervisorToolOperationKey(cp.RunID, cp.NextTurn, "note_create", string(payload))
	id, err := runmutation.SupervisorToolCallID(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	return llm.ToolCall{ID: id, Name: "note_create", Arguments: payload}
}

func TestSupervisorToolRequestRepairReopensAndCompletesOneRepairAcrossToolRounds(t *testing.T) {
	st, path, turn, lease := newToolRequestRepairTest(t, true)
	ctx := context.Background()
	oldCP := turn.Checkpoint
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	expireTestRunExecutionLease(t, ctx, reopened, lease)
	replacement, err := reopened.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: oldCP.RunID, OwnerID: "reopened-tool-repair", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	turn, err = reopened.BeginSupervisorTurn(ctx, replacement.Lease, oldCP.PendingInput)
	if err != nil || !turn.Recovered || turn.Checkpoint.RepairPhase != domain.ProtocolRepairPending || turn.Checkpoint.RepairReason != oldCP.RepairReason {
		t.Fatalf("pending repair lost on reopen: %+v %v", turn, err)
	}
	cp := turn.Checkpoint
	a := startToolRepairModel(t, reopened, cp, 1, 0)
	a.Outcome = llm.OutcomeSuccess
	response := llm.ChatResponse{Usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, ToolCalls: []llm.ToolCall{toolRepairNote(t, cp)}}
	if _, err := reopened.RecordSupervisorModelCompleted(ctx, oldCP, a, response); err == nil {
		t.Fatal("old lease accepted corrected tools")
	}
	for _, bad := range []llm.ToolCall{
		{ID: response.ToolCalls[0].ID, Name: "note_create", Arguments: json.RawMessage(`{"title":"x","content":"y","unknown":true}`)},
		{ID: response.ToolCalls[0].ID, Name: "workspace_list", Arguments: json.RawMessage(`{"version":"agent-code-tools.v1","path":".","limit":20}`)},
	} {
		invalid := response
		invalid.ToolCalls = []llm.ToolCall{response.ToolCalls[0], bad}
		invalid.ToolCalls[1].ID = "other-provider-call"
		if _, err := reopened.RecordSupervisorModelCompleted(ctx, cp, a, invalid); err == nil {
			t.Fatal("repair bypassed payload or authority validation")
		}
	}
	updated, err := reopened.RecordSupervisorModelCompleted(ctx, cp, a, response)
	if err != nil || updated.RepairPhase != domain.ProtocolRepairPending || updated.RepairReason != cp.RepairReason || updated.TotalTokens != 8 {
		t.Fatalf("corrected tools: %+v %v", updated, err)
	}
	replay, err := reopened.RecordSupervisorModelCompleted(ctx, cp, a, response)
	if err != nil || replay != updated {
		t.Fatalf("completion replay changed state: %+v %v", replay, err)
	}
	rounds, err := reopened.ListSupervisorToolRounds(ctx, updated)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 || rounds[0].Calls[0].Status != domain.SupervisorToolPending {
		t.Fatalf("corrected ledger: %+v %v", rounds, err)
	}
	// A follow-up model attempt is still part of this repair. Its text cannot
	// settle the turn until the corrected batch has an actual durable result.
	followup := startToolRepairModel(t, reopened, updated, 1, 1)
	followup.Outcome = llm.OutcomeSuccess
	answer := llm.ChatResponse{Text: "corrected tool completed", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
	if _, err := reopened.RecordSupervisorModelCompleted(ctx, updated, followup, answer); err == nil {
		t.Fatal("pending corrected tool was replaced with text success")
	}
	call := rounds[0].Calls[0]
	if inserted, err := reopened.RecordSupervisorToolExecutionStarted(ctx, updated, call.CallID); err != nil || !inserted {
		t.Fatalf("tool start %t %v", inserted, err)
	}
	result := domain.SupervisorToolResult{CallID: call.CallID, Status: domain.SupervisorToolCompleted, ResultJSON: `{"status":"completed"}`, CompletedAt: time.Now().UTC()}
	if _, duplicate, err := reopened.RecordSupervisorToolResult(ctx, updated, result); err != nil || duplicate {
		t.Fatalf("tool result %t %v", duplicate, err)
	}
	if _, duplicate, err := reopened.RecordSupervisorToolResult(ctx, updated, result); err != nil || !duplicate {
		t.Fatalf("tool result replay %t %v", duplicate, err)
	}
	finalCP, err := reopened.RecordSupervisorModelCompleted(ctx, updated, followup, answer)
	if err != nil || finalCP.RepairPhase != domain.ProtocolRepairPending || finalCP.TotalTokens != 10 {
		t.Fatalf("followup: %+v %v", finalCP, err)
	}
	_, settled, _, err := reopened.CompleteSupervisorTurn(ctx, finalCP, answer, domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionContinue, Message: answer.Text}, policy.Decision{Allowed: true}, 0)
	if err != nil || settled.RepairPhase != domain.ProtocolRepairNone || settled.RepairReason != "" {
		t.Fatalf("settle: %+v %v", settled, err)
	}
	eventList, err := reopened.ListRunEvents(ctx, cp.RunID)
	if err != nil || countRunEventType(eventList, events.ProtocolRepairRequestedEvent) != 1 || countRunEventType(eventList, events.ProtocolRepairStartedEvent) != 1 || countRunEventType(eventList, events.ProtocolRepairCompletedEvent) != 1 || countRunEventType(eventList, events.SupervisorToolBatchEvent) != 1 {
		t.Fatalf("repair/batch events duplicated: %v", err)
	}
	next, err := reopened.BeginSupervisorTurn(ctx, replacement.Lease, "a later input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.RecordSupervisorModelCompleted(ctx, finalCP, followup, answer); err == nil {
		t.Fatal("old repair completion acquired a later turn")
	}
	after, _, err := reopened.GetSupervisorCheckpoint(ctx, cp.RunID)
	if err != nil || after != next.Checkpoint {
		t.Fatal("old repair changed successor checkpoint")
	}
}

func TestSupervisorOrdinaryProtocolRepairCannotAcquireToolPermissionFromCallerCheckpoint(t *testing.T) {
	st, _, turn, _ := newToolRequestRepairTest(t, false)
	cp := turn.Checkpoint
	a := startToolRepairModel(t, st, cp, 1, 0)
	a.Outcome = llm.OutcomeSuccess
	forged := cp
	forged.RepairReason, _ = domain.NewSupervisorToolRequestRepairReason(0, "caller claims tool rejection")
	response := llm.ChatResponse{ToolCalls: []llm.ToolCall{toolRepairNote(t, cp)}, Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
	if _, err := st.RecordSupervisorModelCompleted(context.Background(), forged, a, response); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("ordinary repair accepted tools through caller marker: %v", err)
	}
	actual, _, _ := st.GetSupervisorCheckpoint(context.Background(), cp.RunID)
	rounds, err := st.ListSupervisorToolRounds(context.Background(), cp)
	if err != nil || len(rounds) != 0 || actual != cp {
		t.Fatal("rejected ordinary repair changed ledger or usage")
	}
}

func TestSupervisorToolRequestRepairCannotHideRejectedToolsOrRequestAnotherSlot(t *testing.T) {
	for _, tools := range []bool{false, true} {
		name := "text-only-failure"
		if tools {
			name = "bad-tool-again"
		}
		t.Run(name, func(t *testing.T) {
			st, _, turn, _ := newToolRequestRepairTest(t, true)
			ctx := context.Background()
			cp := turn.Checkpoint
			a := startToolRepairModel(t, st, cp, 1, 0)
			a.Outcome = llm.OutcomeSuccess
			response := llm.ChatResponse{Text: "pretend the task is finished", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
			if _, err := st.RecordSupervisorModelCompleted(ctx, cp, a, response); err == nil {
				t.Fatal("rejected tools became text-only success")
			}
			if tools {
				response.ToolCalls = []llm.ToolCall{{ID: "second-invalid-request", Name: "note_create", Arguments: json.RawMessage(`{"unexpected":true}`)}}
			}
			if _, err := st.RecordSupervisorProtocolFailure(ctx, cp, a, response, "second invalid response", true); err == nil {
				t.Fatal("repair acquired another correction slot")
			}
			exhausted, err := st.RecordSupervisorProtocolFailure(ctx, cp, a, response, "second invalid response", false)
			if err != nil || exhausted.RepairPhase != domain.ProtocolRepairExhausted || !domain.IsSupervisorToolRequestRepair(exhausted.RepairReason) || exhausted.TotalTokens != 5 {
				t.Fatalf("exhaustion: %+v %v", exhausted, err)
			}
			replay, err := st.RecordSupervisorProtocolFailure(ctx, cp, a, response, "second invalid response", false)
			if err != nil || replay != exhausted {
				t.Fatalf("exhaustion replay: %+v %v", replay, err)
			}
			var raw string
			if err := st.db.QueryRowContext(ctx, `SELECT payload_json FROM run_events WHERE run_id=? AND type=? AND subject_id=?`, cp.RunID, events.ModelFailedEvent, supervisorModelSubject(cp, a.Number)).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var payload struct {
				FailureStage string `json:"failure_stage"`
			}
			if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.FailureStage != string(domain.ThreadFailureToolRequestRejected) {
				t.Fatalf("original failure hidden: %s %v", raw, err)
			}
			if _, _, err := st.NextSupervisorModelAttempt(ctx, exhausted, 1, 0); err == nil {
				t.Fatal("exhausted repair started another model call")
			}
		})
	}
}

func TestSupervisorToolRequestRepairMarkerMustMatchOriginalBatch(t *testing.T) {
	st, _, turn, lease := newToolRequestRepairTest(t, true)
	ctx := context.Background()
	if _, err := st.FailSupervisorTurn(ctx, turn.Checkpoint, "end fixture first attempt", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Resume(ctx, turn.Run.ID); err != nil {
		t.Fatal(err)
	}
	next, err := st.BeginSupervisorTurn(ctx, lease, "new independent attempt")
	if err != nil {
		t.Fatal(err)
	}
	a := startToolRepairModel(t, st, next.Checkpoint, 0, 0)
	for _, mismatch := range []bool{false, true} {
		round := 0
		response := llm.ChatResponse{Text: "bad root JSON"}
		if mismatch {
			round = 1
			response.ToolCalls = []llm.ToolCall{toolRepairNote(t, next.Checkpoint)}
		}
		reason, err := domain.NewSupervisorToolRequestRepairReason(round, "wrong source")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.RecordSupervisorProtocolFailure(ctx, next.Checkpoint, a, response, reason, true); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("invalid marker scope accepted: %v", err)
		}
	}
	if _, err := application.NewRunService(st).Cancel(ctx, next.Run.ID); err != nil {
		t.Fatal(err)
	}
	a.Outcome = llm.OutcomeSuccess
	if _, err := st.RecordSupervisorModelCompleted(ctx, next.Checkpoint, a, llm.ChatResponse{ToolCalls: []llm.ToolCall{toolRepairNote(t, next.Checkpoint)}}); err == nil {
		t.Fatal("cancelled run accepted new tool ledger")
	}
}

func TestSupervisorToolRequestRepairAfterCompletedToolsReportsActualFormatFailure(t *testing.T) {
	st, _, turn, _ := newToolRequestRepairTest(t, true)
	ctx := context.Background()
	cp := turn.Checkpoint
	a := startToolRepairModel(t, st, cp, 1, 0)
	a.Outcome = llm.OutcomeSuccess
	cp, err := st.RecordSupervisorModelCompleted(ctx, cp, a, llm.ChatResponse{ToolCalls: []llm.ToolCall{toolRepairNote(t, cp)}})
	if err != nil {
		t.Fatal(err)
	}
	rounds, err := st.ListSupervisorToolRounds(ctx, cp)
	if err != nil || len(rounds) != 1 {
		t.Fatalf("rounds: %+v %v", rounds, err)
	}
	call := rounds[0].Calls[0]
	if _, err := st.RecordSupervisorToolExecutionStarted(ctx, cp, call.CallID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RecordSupervisorToolResult(ctx, cp, domain.SupervisorToolResult{CallID: call.CallID, Status: domain.SupervisorToolCompleted, ResultJSON: `{"status":"completed"}`, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	followup := startToolRepairModel(t, st, cp, 1, 1)
	response := llm.ChatResponse{Text: "invalid root JSON after real tool completion", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
	exhausted, err := st.RecordSupervisorProtocolFailure(ctx, cp, followup, response, "invalid JSON", false)
	if err != nil || exhausted.RepairPhase != domain.ProtocolRepairExhausted {
		t.Fatalf("failure: %+v %v", exhausted, err)
	}
	replayed, err := st.RecordSupervisorProtocolFailure(ctx, cp, followup, response, "invalid JSON", false)
	if err != nil || replayed != exhausted {
		t.Fatalf("failure replay: %+v %v", replayed, err)
	}
	var raw string
	if err := st.db.QueryRowContext(ctx, `SELECT payload_json FROM run_events WHERE run_id=? AND type=? AND subject_id=?`, cp.RunID, events.ModelFailedEvent, supervisorModelSubject(cp, followup.Number)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		FailureStage string `json:"failure_stage"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.FailureStage != string(domain.ThreadFailureInvalidModelResponse) {
		t.Fatalf("completed tools were falsely described as rejected: %s %v", raw, err)
	}
}

func TestSupervisorToolRequestRepairLateAccountingDoesNotRequireCorrectedTools(t *testing.T) {
	for _, terminal := range []string{events.ModelFailedEvent, events.ModelCompletedEvent} {
		t.Run(terminal, func(t *testing.T) {
			st, run, lease := newMonetarySupervisor(t)
			ctx := context.Background()
			turn, err := st.BeginSupervisorTurn(ctx, lease, "repair with late usage")
			if err != nil {
				t.Fatal(err)
			}
			primary := llm.ModelAttempt{SupervisorAttemptID: turn.Checkpoint.AttemptID, Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "mock", Model: "mock-code"}
			reserveSupervisorMoney(t, st, run.ID, primary, 1000)
			if _, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, primary); err != nil {
				t.Fatal(err)
			}
			reason, _ := domain.NewSupervisorToolRequestRepairReason(0, "unknown field")
			cp, err := st.RecordSupervisorProtocolFailure(ctx, turn.Checkpoint, primary, llm.ChatResponse{Usage: llm.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}, ToolCalls: []llm.ToolCall{{ID: "bad", Name: "note_create", Arguments: json.RawMessage(`{"unknown":true}`)}}}, reason, true)
			if err != nil {
				t.Fatal(err)
			}
			repair := primary
			repair.Number = 2
			repair.ProtocolRepair = 1
			reserveSupervisorMoney(t, st, run.ID, repair, 1000)
			if _, err := st.RecordSupervisorModelStarted(ctx, cp, repair); err != nil {
				t.Fatal(err)
			}
			if _, err := application.NewRunService(st).Cancel(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			before, _, err := st.GetSupervisorCheckpoint(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			usage := llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}
			var after domain.SupervisorCheckpoint
			if terminal == events.ModelFailedEvent {
				repair.Outcome, repair.ErrorText = llm.OutcomeCancelled, "cancelled after response"
				after, err = st.RecordSupervisorModelFailedWithUsage(ctx, cp, repair, usage, 0)
			} else {
				// Exercise the terminal primitive's accounting-only invariant as
				// well as the public failed-with-known-usage path above. No public
				// assistant response or tool payload is supplied by this receipt.
				repair.Outcome = llm.OutcomeSuccess
				payload := map[string]any{"turn": cp.NextTurn, "attempt_id": cp.AttemptID, "model_attempt": repair.Number, "transport_attempt": 1, "max_attempts": 1, "protocol_repair": 1, "tool_round": 0, "provider": repair.Provider, "model": repair.Model, "outcome": repair.Outcome, "usage": usage, "tool_call_count": 0, "accounting_only": true, "usage_unknown": false}
				addSupervisorMonetaryIdentity(payload, repair)
				after, err = st.recordSupervisorModelTerminal(ctx, cp, repair, terminal, payload, supervisorModelTerminalOptions{Usage: &usage, AccountingOnly: true})
			}
			if err != nil || after.TotalTokens != 8 || after.Phase != before.Phase || after.PendingInput != before.PendingInput || after.RepairPhase != before.RepairPhase || after.RepairReason != before.RepairReason {
				t.Fatalf("late accounting was blocked or advanced execution: before=%+v after=%+v err=%v", before, after, err)
			}
			money, err := st.GetMonetaryUsage(ctx, run.ID)
			if err != nil || money.SettledMicros != 12 || money.ReleasedMicros != 1988 {
				t.Fatalf("late known usage not charged: %+v %v", money, err)
			}
			actualRun, err := st.GetRun(ctx, run.ID)
			if err != nil || actualRun.Status != domain.RunCancelled {
				t.Fatalf("accounting revived cancelled run: %+v %v", actualRun, err)
			}
			rounds, err := st.ListSupervisorToolRounds(ctx, cp)
			if err != nil || len(rounds) != 0 {
				t.Fatal("accounting published corrected tools")
			}
			messages, err := st.ListSessionMessages(ctx, run.SessionID, true)
			if err != nil || len(messages) != 0 {
				t.Fatalf("accounting published a successful text answer: %+v %v", messages, err)
			}
		})
	}
}
