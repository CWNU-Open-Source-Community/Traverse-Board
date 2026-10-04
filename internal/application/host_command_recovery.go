package application

import (
	"context"
	"encoding/json"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

// recoverHistoricalHostTool consumes saved outcomes for the exact old call.
// Retired proposals cannot dispatch commands or create new approval authority.
func (s *RunSupervisor) recoverHistoricalHostTool(ctx context.Context, turn domain.SupervisorTurn,
	call domain.SupervisorToolCall,
) (domain.SupervisorToolResult, error) {
	failed := func(code, message string) (domain.SupervisorToolResult, error) {
		raw, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{
			Version: supervisorToolResultVersion, Tool: call.ToolName,
			Status: string(domain.SupervisorToolFailed), Code: code, Message: message,
		})
		return domain.SupervisorToolResult{CallID: call.CallID, Status: domain.SupervisorToolFailed,
			ErrorCode: code, ResultJSON: string(raw), CompletedAt: time.Now().UTC()}, err
	}
	spec, _, err := toolgateway.NormalizeHostCommandProposalPayload(json.RawMessage(call.PayloadJSON))
	if err != nil {
		return domain.SupervisorToolResult{}, err
	}
	if spec.Version != runner.RiskEscalationProtocolVersion {
		return failed(string(apperror.CodePolicyDenied), "Host command proposals are retired; use Command Runtime for a new command.")
	}
	history, ok := any(s.store).(RiskEscalationHistoryStore)
	if !ok {
		return domain.SupervisorToolResult{}, apperror.New(apperror.CodeFailedPrecondition, "historical command evidence is unavailable")
	}
	key := supervisorToolOperationKey(call.RunID, call.Turn, toolgateway.HostCommandProposeTool, json.RawMessage(call.PayloadJSON))
	digest := runmutation.OperationKeyDigest(string(toolgateway.HostCommandProposeTool), call.RunID, key)
	proposal, err := history.GetRiskEscalationProposal(ctx, "risk-escalation-"+digest[:24])
	if apperror.CodeOf(apperror.Normalize(err)) == apperror.CodeNotFound {
		return failed(string(apperror.CodePolicyDenied), "Risk escalation proposals are retired; use Command Runtime for a new command.")
	}
	if err != nil {
		return domain.SupervisorToolResult{}, err
	}
	if proposal.RunID != call.RunID || proposal.MissionID != turn.Mission.ID ||
		proposal.SessionID != turn.Run.SessionID || proposal.WorkspaceID != turn.Mission.WorkspaceID ||
		proposal.RootAgentID != call.AgentID || proposal.SupervisorTurn != call.Turn ||
		proposal.SupervisorToolCallID != call.CallID {
		return domain.SupervisorToolResult{}, apperror.New(apperror.CodeConflict, "historical command does not match the exact durable Supervisor call")
	}
	result, err := (&HostCommandHistory{riskStore: history}).riskEscalationResult(ctx, proposal, true)
	if err != nil {
		return domain.SupervisorToolResult{}, err
	}
	if result.State == toolgateway.HostCommandProposalWaiting {
		return domain.SupervisorToolResult{}, apperror.New(apperror.CodeFailedPrecondition, "historical command has no saved outcome; execution is retired")
	}
	status := domain.SupervisorToolCompleted
	code := result.ErrorCode
	if result.State == toolgateway.HostCommandProposalDenied {
		status, code = domain.SupervisorToolDenied, string(apperror.CodePolicyDenied)
	} else if result.State == toolgateway.HostCommandProposalFailed {
		status = domain.SupervisorToolFailed
	}
	metadata := map[string]string{"proposal_id": proposal.ID, "spec_fingerprint": proposal.Spec.Fingerprint,
		"approval_id": result.ApprovalID, "proposal_state": string(result.State), "automatic_retry_allowed": "false"}
	if result.Uncertain {
		metadata["execution_result_uncertain"] = "true"
	}
	raw, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{
		Version: supervisorToolResultVersion, Tool: call.ToolName, Status: string(status),
		Code: code, Message: boundedSupervisorToolMessage(result.Message),
		Stdout: truncateUTF8Bytes(sanitizeControlledCommandEvidence([]byte(result.Evidence)), MaxHostCommandEvidenceBytes), Metadata: metadata,
	})
	return domain.SupervisorToolResult{CallID: call.CallID, Status: status, ErrorCode: code,
		ResultJSON: string(raw), CompletedAt: time.Now().UTC()}, err
}
