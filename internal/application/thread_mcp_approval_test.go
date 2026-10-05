package application

import (
	"testing"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
)

func TestThreadTurnStopsAtPendingMCPApprovalAndResumesSameTurn(t *testing.T) {
	f := newMCPRecoveryAcceptanceRuntime(t, domain.RunExecutionPermissionAsk, 1, false)
	request := ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion,
		ThreadID: domain.InitialThreadID(f.run.ID), Content: "Look up the exact reviewed MCP input.",
		OperationKey: "thread-mcp-pending-operation-0001", RequestedBy: "operator"}
	result, err := f.turns.Execute(t.Context(), request)
	if err != nil || result.Execution == nil || result.Execution.Handoff.Result == nil {
		t.Fatalf("Thread did not return its approval boundary: result=%+v err=%v", result, err)
	}
	handoff := result.Execution.Handoff
	if handoff.Result.Status != domain.RunExecutionHandoffCompleted || handoff.Result.StopReason != "root_wait" ||
		result.Submission.Message.Status != domain.OperatorSteeringPending || result.Submission.Run.Status != domain.RunRunning {
		t.Fatalf("approval wait lost its resumable message or Run: %+v", result)
	}
	if f.requests.Load() != 0 || f.wireCalls.Load() != 0 {
		t.Fatal("Thread dispatched the MCP call before operator consent")
	}
	var found bool
	f.pending, found, err = f.st.GetSupervisorCheckpoint(t.Context(), f.run.ID)
	if err != nil || !found || f.pending.Phase != domain.SupervisorTurnStarted {
		t.Fatalf("missing original pending turn: %+v err=%v", f.pending, err)
	}
	rounds, err := f.st.ListSupervisorToolRounds(t.Context(), f.pending)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
		t.Fatalf("missing exact durable MCP intent: %+v err=%v", rounds, err)
	}
	f.calls = rounds[0].Calls
	f.assertApproval(t, 0, approval.StatusPending)
	if retry, err := f.turns.Execute(t.Context(), request); err != nil || !retry.Replayed || retry.Submission.Message.ID != result.Submission.Message.ID {
		t.Fatalf("pending request retry restarted or lost the original message: %+v err=%v", retry, err)
	}
	second := threadTurnExecutionRequest(SubmitThreadMessageRequest{ThreadID: request.ThreadID, OperationKey: request.OperationKey,
		RequestedBy: request.RequestedBy}, f.run.ID, 2)
	if _, found, err := f.st.GetRunExecutionHandoff(t.Context(),
		runmutation.RunExecutionHandoffOperationDigest(f.run.ID, second.OperationKey)); err != nil || found {
		t.Fatalf("Thread created a second handoff while waiting for approval: found=%t err=%v", found, err)
	}
	f.provider.mu.Lock()
	modelRequests := f.provider.requests
	f.provider.mu.Unlock()
	if modelRequests != 1 {
		t.Fatalf("approval wait repeated the initial model request: %d", modelRequests)
	}
	f.decide(t, f.request(t, 0, ApprovalControlApproveOnce, "thread-mcp-consent-operation-0001"))
	if resumed := f.resume(t, 0); resumed.State != "completed" {
		t.Fatalf("exact approved Thread turn did not resume: %+v", resumed)
	}
	f.assertCall(t, 0, domain.SupervisorToolCompleted, "", true)
	f.assertTurnSettled(t, 1, 1)
	message, err := f.st.GetOperatorSteering(t.Context(), result.Submission.Message.ID)
	if err != nil || message.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("approval continuation did not commit the original Thread input: %+v err=%v", message, err)
	}
	if replay, err := f.turns.Execute(t.Context(), request); err != nil || !replay.Replayed || replay.Submission.Message.ID != message.ID {
		t.Fatalf("settled Thread request did not replay: %+v err=%v", replay, err)
	}
	f.assertTurnSettled(t, 1, 1)
}
