package httpapi

import (
	"context"
	"cyberagent-workbench/internal/application"
)

type approvalContinuationController interface {
	ResumeApproval(context.Context, application.ApprovalContinuationRequest) application.ApprovalContinuationResult
}

func (a *API) resumeReviewedProposal(ctx context.Context, runID, kind, proposalID string) *application.ApprovalContinuationResult {
	if !a.runExecutionEnabled {
		return nil
	}
	controller, ok := a.threadTurnController.(approvalContinuationController)
	if !ok {
		return nil
	}
	result := controller.ResumeApproval(ctx, application.ApprovalContinuationRequest{RunID: runID, Kind: kind, ProposalID: proposalID})
	return &result
}
