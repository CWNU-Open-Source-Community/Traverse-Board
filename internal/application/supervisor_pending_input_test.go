package application_test

import (
	"fmt"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestSupervisorNextTurnInstructionWaitsForItsOwnTurn(t *testing.T) {
	provider := &retrySequenceProvider{}
	_, st, run, supervisor := newRetrySupervisor(t, provider)
	ctx := t.Context()
	first, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "Edit backend and frontend files",
		OperationKey: "queued-original-0001", RequestedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	correction, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "Correction: only inspect frontend; do not edit any file",
		OperationKey: "queued-correction-0002", RequestedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "Withdrawn instruction must not reach the model",
		OperationKey: "queued-cancelled-0003", RequestedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CancelOperatorSteering(ctx, domain.CancelOperatorSteeringRequest{
		MessageID: cancelled.Message.ID, OperationKey: "cancel-queued-0003",
		RequestedBy: "test_operator", Reason: "operator withdrew this instruction"}); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Step(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("model calls=%d", len(provider.requests))
	}
	request := provider.requests[0]
	input := request.Messages[len(request.Messages)-1]
	if input.Role != "user" || !strings.Contains(input.Content, first.Message.Content) ||
		strings.Contains(input.Content, correction.Message.Content) ||
		strings.Contains(input.Content, cancelled.Message.Content) || request.Metadata["pending_user_instructions"] != "0" {
		t.Fatalf("next-turn queue entered the current model request: role=%s input=%s", input.Role, input.Content)
	}
	stillPending, err := st.GetOperatorSteering(ctx, correction.Message.ID)
	if err != nil || stillPending.Status != domain.OperatorSteeringPending || stillPending.Prepared {
		t.Fatalf("context preview consumed pending correction: %+v err=%v", stillPending, err)
	}
	if _, err := supervisor.Step(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("model calls=%d", len(provider.requests))
	}
	second := provider.requests[1].Messages
	if second[len(second)-1].Content != correction.Message.Content {
		t.Fatalf("correction did not keep its own input: %+v", second[len(second)-1])
	}
}

func TestSupervisorNextTurnQueueDoesNotCrowdCurrentContext(t *testing.T) {
	provider := &retrySequenceProvider{}
	_, st, run, supervisor := newRetrySupervisor(t, provider)
	firstContent := "Instruction 0: " + strings.Repeat("界", 5000)
	for i := 0; i < 13; i++ {
		_, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{
			RunID: run.ID, SessionID: run.SessionID,
			Content:      fmt.Sprintf("Instruction %d: ", i) + strings.Repeat("界", 5000),
			OperationKey: fmt.Sprintf("bounded-pending-input-%04d", i), RequestedBy: "test_operator"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := supervisor.Step(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("model calls=%d", len(provider.requests))
	}
	input := provider.requests[0].Messages[len(provider.requests[0].Messages)-1]
	if input.Role != "user" || !strings.Contains(input.Content, firstContent) ||
		strings.Contains(input.Content, "Instruction 1: ") ||
		provider.requests[0].Metadata["pending_user_instructions"] != "0" {
		t.Fatal("next-turn queue was mixed into or truncated within the current model request")
	}
	queue, err := st.GetOperatorSteeringQueueSummary(t.Context(), run.ID)
	if err != nil || queue.Pending != 12 || queue.Prepared != 0 || queue.Committed != 1 || queue.Cancelled != 0 {
		t.Fatalf("next-turn queue was consumed out of order: %+v err=%v", queue, err)
	}
}
