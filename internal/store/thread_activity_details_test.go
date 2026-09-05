package store

import (
	"context"
	"encoding/json"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

func TestThreadActivityReferenceIsScopedThroughThreadRunBinding(t *testing.T) {
	st := openWorkItemTestStore(t)
	ctx := context.Background()
	_, ownerRun := createStructuredToolTestRun(t, ctx, st, "activity owner")
	_, otherRun := createStructuredToolTestRun(t, ctx, st, "other activity owner")
	ownerThread, err := st.GetThreadByRun(ctx, ownerRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherThread, err := st.GetThreadByRun(ctx, otherRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Start(ctx, ownerRun.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := st.BeginSupervisorTurn(ctx,
		acquireTestRunExecutionLease(t, ctx, st, ownerRun.ID), "persist activity")
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"title":"Activity","content":"safe"}`)
	normalizedArguments, err := toolgateway.NormalizeStructuredMemoryPayload(
		toolgateway.NoteCreateTool, arguments)
	if err != nil {
		t.Fatal(err)
	}
	operationKey := runmutation.SupervisorToolOperationKey(ownerRun.ID,
		turn.Checkpoint.NextTurn, "note_create", string(normalizedArguments))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1,
		Provider: "test", Model: "model"}
	if inserted, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint,
		attempt); err != nil || !inserted {
		t.Fatalf("model start: inserted=%t err=%v", inserted, err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt,
		llm.ChatResponse{Provider: "test", Model: "model",
			Usage:     llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID, Name: "note_create", Arguments: arguments}}})
	if err != nil {
		t.Fatal(err)
	}
	rounds, err := st.ListSupervisorToolRounds(ctx, checkpoint)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
		t.Fatalf("tool rounds = %#v err=%v", rounds, err)
	}
	callID = rounds[0].Calls[0].CallID
	if call, err := st.GetThreadSupervisorToolCall(ctx, ownerThread.ID,
		callID); err != nil || call.CallID != callID || call.RunID != ownerRun.ID {
		t.Fatalf("owner lookup = %#v err=%v", call, err)
	}
	if _, err := st.GetThreadSupervisorToolCall(ctx, otherThread.ID,
		callID); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeNotFound {
		t.Fatalf("cross-Thread lookup error = %v, want not_found", err)
	}
}
