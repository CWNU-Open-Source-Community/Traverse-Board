package domain

import (
	"testing"
	"time"
)

func TestSupervisorToolRoundValidationTracksTerminalCalls(t *testing.T) {
	now := time.Now().UTC()
	pending := SupervisorToolCall{
		RunID: "run-1", Turn: 1, AttemptID: "attempt-1", Round: 1, Position: 1, ModelAttempt: 1,
		CallID: "toolu_0123456789abcdef01234567", ToolName: "work_item_create",
		PayloadJSON: `{"title":"Plan"}`, Status: SupervisorToolPending, CreatedAt: now,
	}
	round := SupervisorToolRound{
		RunID: "run-1", Turn: 1, AttemptID: "attempt-1", Round: 1, ModelAttempt: 1,
		Calls: []SupervisorToolCall{pending}, CreatedAt: now,
	}
	if err := round.Validate(); err != nil || round.Complete() {
		t.Fatalf("pending supervisor tool round is invalid: %#v err=%v", round, err)
	}
	completed := now.Add(time.Second)
	round.Calls[0].Status = SupervisorToolCompleted
	round.Calls[0].ResultJSON = `{"status":"completed"}`
	round.Calls[0].CompletedAt = &completed
	round.CompletedAt = &completed
	if err := round.Validate(); err != nil || !round.Complete() {
		t.Fatalf("completed supervisor tool round is invalid: %#v err=%v", round, err)
	}
	round.Calls[0].ErrorCode = "unexpected"
	if err := round.Validate(); err == nil {
		t.Fatal("completed supervisor tool call accepted an error code")
	}
}

func TestSupervisorToolResultRejectsUnboundedOrPendingState(t *testing.T) {
	now := time.Now().UTC()
	valid := SupervisorToolResult{
		CallID: "toolu_0123456789abcdef01234567", Status: SupervisorToolDenied,
		ResultJSON: `{"status":"denied"}`, ErrorCode: "POLICY_DENIED", CompletedAt: now,
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	valid.Status = SupervisorToolPending
	if err := valid.Validate(); err == nil {
		t.Fatal("pending supervisor tool result was accepted")
	}
}

func TestSupervisorToolCallAcceptsEveryDurableToolName(t *testing.T) {
	now := time.Now().UTC()
	for _, toolName := range []string{
		"work_item_create",
		"note_create",
		"specialist_delegation_propose",
		"plan_delivery_propose",
		"controlled_command_propose",
		"host_command_propose",
	} {
		t.Run(toolName, func(t *testing.T) {
			call := SupervisorToolCall{
				RunID: "run-1", Turn: 1, AttemptID: "attempt-1", Round: 1,
				Position: 1, ModelAttempt: 1, CallID: "toolu_0123456789abcdef01234567",
				ToolName: toolName, PayloadJSON: `{}`, Status: SupervisorToolPending,
				CreatedAt: now,
			}
			if err := call.Validate(); err != nil {
				t.Fatalf("durable tool %q was rejected: %v", toolName, err)
			}
		})
	}
}
