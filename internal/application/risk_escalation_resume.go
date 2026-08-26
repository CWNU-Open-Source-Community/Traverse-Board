package application

import (
	"context"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

const RiskEscalationResumeProtocolVersion = "risk_escalation_resume.v1"

type ResumeRiskEscalationRequest struct {
	Version    string
	RunID      string
	ProposalID string
}

type ResumeRiskEscalationResult struct {
	Execution LifecycleResult
	Replayed  bool
}

type riskEscalationResumeStore interface {
	GetRiskEscalationProposal(context.Context, string) (runner.RiskEscalationProposal, error)
	GetApprovalByProposal(context.Context, string) (approval.Record, error)
	GetRiskEscalationResult(context.Context, string) (runner.RiskEscalationResult, bool, error)
	GetRiskEscalationInvalidation(context.Context, string) (
		runner.RiskEscalationInvalidation, bool, error)
	ResumeRiskEscalationRun(context.Context, string, string) (domain.Run, bool, error)
}

// ResumeRiskEscalation continues only the Supervisor turn that owns the exact
// durable proposal. A replay after that turn has completed is a no-op; it
// cannot create a new turn or re-execute the external side effect.
func (s *RunExecutionHandoffService) ResumeRiskEscalation(ctx context.Context,
	request ResumeRiskEscalationRequest,
) (ResumeRiskEscalationResult, error) {
	if s == nil || s.store == nil || s.supervisor == nil {
		return ResumeRiskEscalationResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "risk escalation resume dependencies are required")
	}
	request.RunID = strings.TrimSpace(request.RunID)
	request.ProposalID = strings.TrimSpace(request.ProposalID)
	if request.Version != RiskEscalationResumeProtocolVersion ||
		!domain.ValidAgentID(request.RunID) || !domain.ValidAgentID(request.ProposalID) {
		return ResumeRiskEscalationResult{}, apperror.New(
			apperror.CodeInvalidArgument, "risk escalation resume request is invalid")
	}
	store, ok := any(s.store).(riskEscalationResumeStore)
	if !ok {
		return ResumeRiskEscalationResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "risk escalation resume store is unavailable")
	}
	proposal, err := store.GetRiskEscalationProposal(ctx, request.ProposalID)
	if err != nil {
		return ResumeRiskEscalationResult{}, apperror.Normalize(err)
	}
	if proposal.RunID != request.RunID {
		return ResumeRiskEscalationResult{}, apperror.New(
			apperror.CodeNotFound, "risk escalation proposal was not found for this Run")
	}
	record, err := store.GetApprovalByProposal(ctx, proposal.ID)
	if err != nil {
		return ResumeRiskEscalationResult{}, apperror.Normalize(err)
	}
	_, hasResult, err := store.GetRiskEscalationResult(ctx, proposal.ID)
	if err != nil {
		return ResumeRiskEscalationResult{}, apperror.Normalize(err)
	}
	_, invalidated, err := store.GetRiskEscalationInvalidation(ctx, proposal.ID)
	if err != nil {
		return ResumeRiskEscalationResult{}, apperror.Normalize(err)
	}
	if record.Status != approval.StatusDenied && !hasResult && !invalidated {
		return ResumeRiskEscalationResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"risk escalation has no durable denial, result, or invalidation to resume")
	}
	runRecord, err := s.store.GetRun(ctx, proposal.RunID)
	if err != nil {
		return ResumeRiskEscalationResult{}, apperror.Normalize(err)
	}
	if runRecord.Status == domain.RunWaitingApproval {
		if _, _, err := store.ResumeRiskEscalationRun(ctx, proposal.ID,
			"durable risk escalation decision is ready for exact continuation"); err != nil {
			return ResumeRiskEscalationResult{}, apperror.Normalize(err)
		}
	}
	resumable, err := s.riskEscalationTurnResumable(ctx, proposal)
	if err != nil {
		return ResumeRiskEscalationResult{}, err
	}
	if !resumable {
		return ResumeRiskEscalationResult{Replayed: true}, nil
	}
	var execution LifecycleResult
	err = s.supervisor.withRunExecutionLease(ctx, proposal.RunID,
		func(leaseCtx context.Context, lease domain.RunExecutionLease) error {
			stillResumable, checkErr := s.riskEscalationTurnResumable(leaseCtx, proposal)
			if checkErr != nil {
				return checkErr
			}
			if !stillResumable {
				return nil
			}
			var stepErr error
			execution, stepErr = s.supervisor.stepWithLease(leaseCtx, lease, "")
			return stepErr
		})
	if err != nil {
		return ResumeRiskEscalationResult{Execution: execution}, apperror.Normalize(err)
	}
	if execution.Turn == 0 {
		return ResumeRiskEscalationResult{Replayed: true}, nil
	}
	return ResumeRiskEscalationResult{Execution: execution}, nil
}

func (s *RunExecutionHandoffService) riskEscalationTurnResumable(ctx context.Context,
	proposal runner.RiskEscalationProposal,
) (bool, error) {
	runRecord, err := s.store.GetRun(ctx, proposal.RunID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	if runRecord.Status != domain.RunRunning {
		return false, nil
	}
	checkpoint, found, err := s.store.GetSupervisorCheckpoint(ctx, proposal.RunID)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	if !found || checkpoint.Phase != domain.SupervisorTurnStarted ||
		checkpoint.NextTurn != proposal.SupervisorTurn {
		return false, nil
	}
	rounds, err := s.store.ListSupervisorToolRounds(ctx, checkpoint)
	if err != nil {
		return false, apperror.Normalize(err)
	}
	for _, round := range rounds {
		for _, call := range round.Calls {
			if call.CallID == proposal.SupervisorToolCallID &&
				call.ToolName == "host_command_propose" {
				return true, nil
			}
		}
	}
	return false, apperror.New(apperror.CodeConflict,
		"risk escalation lost its exact durable Supervisor tool call")
}
