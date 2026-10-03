package application

import (
	"context"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/toolgateway"
)

func pendingToolApprovalUnavailable(message string) error {
	return apperror.New(apperror.CodeFailedPrecondition, message)
}

// Pending tools resume the same durable model turn under the ordinary Run
// lease. They cannot enter the completed-wait continuation protocol.
func (s *ThreadTurnService) resumePendingToolApproval(ctx context.Context, request ApprovalContinuationRequest) ApprovalContinuationResult {
	st, ok := s.execution.store.(interface {
		GetSupervisorApprovalCall(context.Context, string, string) (domain.SupervisorToolCall, bool, error)
		GetApprovalByProposal(context.Context, string) (approval.Record, error)
	})
	if !ok {
		return approvalContinuationFailed(pendingToolApprovalUnavailable("pending tool approval continuation unavailable"))
	}
	var tool string
	var fingerprint func(domain.SupervisorToolCall) string
	switch request.Kind {
	case "agent_browser":
		tool, fingerprint = toolgateway.AgentBrowserApprovalTool, toolgateway.AgentBrowserApprovalFingerprint
	case "mcp":
		tool, fingerprint = mcp.OperationApprovalTool, mcp.OperationApprovalFingerprint
	case "command_runtime":
		tool, fingerprint = string(toolgateway.CommandRuntimeTool), commandruntimeadapter.OperationApprovalFingerprint
	default:
		return approvalContinuationFailed(pendingToolApprovalUnavailable("unknown pending tool approval kind"))
	}
	record, e := st.GetApprovalByProposal(ctx, request.ProposalID)
	if e != nil {
		return approvalContinuationFailed(e)
	}
	if record.ToolName != tool || record.RunID != request.RunID || record.Status == approval.StatusPending {
		return ApprovalContinuationResult{State: "not_started"}
	}
	call, _, e := st.GetSupervisorApprovalCall(ctx, request.RunID, request.ProposalID)
	if e != nil {
		return approvalContinuationFailed(e)
	}
	if record.RequestFingerprint != fingerprint(call) {
		return approvalContinuationFailed(pendingToolApprovalUnavailable("tool approval continuation source mismatch"))
	}
	if call.Status.Terminal() {
		return ApprovalContinuationResult{State: "completed", Replayed: true}
	}
	var result LifecycleResult
	e = s.execution.supervisor.withRunExecutionLease(ctx, request.RunID, func(c context.Context, lease domain.RunExecutionLease) error {
		checkpoint, found, e := s.execution.store.GetSupervisorCheckpoint(c, request.RunID)
		if e != nil {
			return e
		}
		if !found || checkpoint.Phase != domain.SupervisorTurnStarted || checkpoint.NextTurn != call.Turn || checkpoint.AttemptID != call.AttemptID {
			return pendingToolApprovalUnavailable("tool approval cannot resume a different Supervisor turn")
		}
		result, e = s.execution.supervisor.stepWithLease(c, lease, "")
		return e
	})
	if e != nil {
		failed := approvalContinuationFailed(e)
		failed.ModelCalled = result.ModelAttempts > 0
		failed.ToolCalled = result.ToolCalls > 0
		return failed
	}
	state := "completed"
	if result.RunStatus == domain.RunWaitingApproval {
		state = "waiting_approval"
	}
	return ApprovalContinuationResult{State: state, ModelCalled: result.ModelAttempts > 0, ToolCalled: result.ToolCalls > 0}
}
