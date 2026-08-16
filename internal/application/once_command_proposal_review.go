package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/runner"
)

// OnceCommandProposalReviewService owns the operator review and the
// proposal-bound execution path. Approved parameters are immutable: execution
// re-derives the spec from the stored proposal and requires the approval
// fingerprint to match.
type OnceCommandProposalReviewService struct {
	store        OneShotCommandProposalStore
	executor     OnceCommandExecutor
	capabilities domain.ExecutionPermissionRuntimeCapabilities
}

func NewOnceCommandProposalReviewService(store OneShotCommandProposalStore,
	executor OnceCommandExecutor,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) *OnceCommandProposalReviewService {
	return &OnceCommandProposalReviewService{store: store, executor: executor, capabilities: capabilities}
}

func (s *OnceCommandProposalReviewService) List(ctx context.Context, runID string, limit int) ([]runner.OnceCommandProposal, error) {
	if s == nil || s.store == nil {
		return nil, apperror.New(apperror.CodeFailedPrecondition, "once command review store is required")
	}
	values, err := s.store.ListOnceCommandProposals(ctx, strings.TrimSpace(runID), limit)
	return values, apperror.Normalize(err)
}

func (s *OnceCommandProposalReviewService) Review(ctx context.Context, proposalID, decision,
	reviewer, reason string,
) (runner.OnceCommandProposal, error) {
	if s == nil || s.store == nil {
		return runner.OnceCommandProposal{}, apperror.New(apperror.CodeFailedPrecondition, "once command review store is required")
	}
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		reviewer = "cli_operator"
	}
	proposal, found, err := s.store.GetOnceCommandProposal(ctx, proposalID)
	if err != nil {
		return runner.OnceCommandProposal{}, apperror.Normalize(err)
	}
	if !found {
		return runner.OnceCommandProposal{}, apperror.New(apperror.CodeNotFound, "once command proposal was not found")
	}
	// Approving is the tier decision itself: the operator-approved stateless
	// evaluation must pass. Denying is always allowed.
	if decision == "approve" {
		run, err := s.store.GetRun(ctx, proposal.RunID)
		if err != nil {
			return runner.OnceCommandProposal{}, apperror.Normalize(err)
		}
		permission, err := s.store.GetRunExecutionPermission(ctx, run.ID)
		if err != nil {
			return runner.OnceCommandProposal{}, apperror.Normalize(err)
		}
		decision, err := executionauth.EvaluateExecutionPermission(permission, s.capabilities,
			executionauth.PermissionRequest{
				Kind: executionauth.PermissionOperationStatelessCommand, HostFilesystem: true,
				Network: true, OperatorApproved: true,
			})
		if err != nil {
			return runner.OnceCommandProposal{}, apperror.Wrap(
				apperror.CodeInvalidArgument, "once command approval evaluation failed", err)
		}
		if !decision.Allowed {
			return runner.OnceCommandProposal{}, apperror.New(apperror.CodePolicyDenied,
				"once command approval denied: "+decision.Reason)
		}
	}
	approvalFingerprint := ""
	if decision == "approve" {
		approvalFingerprint = runner.OnceCommandApprovalFingerprint(proposal.RequestFingerprint, proposal.ID)
	}
	updated, _, err := s.store.ReviewOnceCommandProposal(ctx, proposalID, decision, reviewer,
		reason, approvalFingerprint, time.Now().UTC())
	return updated, apperror.Normalize(err)
}

// Execute runs an approved proposal with the exact stored parameters.
func (s *OnceCommandProposalReviewService) Execute(ctx context.Context, proposalID,
	requestedBy string, environment []string,
) (OnceCommandRunResult, error) {
	if s == nil || s.store == nil || s.executor == nil || !s.executor.Available() {
		return OnceCommandRunResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"once command execution requires a store and an available executor")
	}
	proposal, found, err := s.store.GetOnceCommandProposal(ctx, proposalID)
	if err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	if !found {
		return OnceCommandRunResult{}, apperror.New(apperror.CodeNotFound, "once command proposal was not found")
	}
	if proposal.Status != "approved" {
		return OnceCommandRunResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"once command proposal must be approved before execution")
	}
	run, err := s.store.GetRun(ctx, proposal.RunID)
	if err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	workspace, err := s.store.GetWorkspaceByID(ctx, proposal.WorkspaceID)
	if err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	permission, err := s.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	envKeys := make([]string, len(environment))
	for index, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		envKeys[index] = key
	}
	sort.Strings(envKeys)
	envDigest := sha256.Sum256([]byte(strings.Join(envKeys, "\x00")))
	if hex.EncodeToString(envDigest[:]) != proposal.EnvironmentSHA256 ||
		!slices.Equal(envKeys, proposal.EnvironmentKeys) {
		return OnceCommandRunResult{}, apperror.New(apperror.CodeInvalidArgument,
			"once command environment does not match the approved proposal")
	}
	spec := runner.OnceCommandSpec{
		ProtocolVersion: runner.OnceCommandProtocolVersion,
		ExecutablePath:  proposal.ExecutablePath, Argv: proposal.Argv,
		WorkingDirectory: proposal.WorkingDirectory, Environment: environment,
		TimeoutMilliseconds: proposal.TimeoutMilliseconds, Purpose: proposal.Purpose,
	}
	if err := runner.ValidateOnceCommandSpec(spec, workspace.RootPath); err != nil {
		return OnceCommandRunResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"once command proposal no longer satisfies the boundary", err)
	}
	// The approval fingerprint must bind this exact proposal: parameters are
	// immutable after approval.
	if runner.OnceCommandApprovalFingerprint(proposal.RequestFingerprint, proposal.ID) !=
		proposal.ApprovalFingerprint {
		return OnceCommandRunResult{}, apperror.New(apperror.CodeInternal,
			"once command proposal approval binding is invalid")
	}
	decision, err := executionauth.EvaluateExecutionPermission(permission, s.capabilities,
		executionauth.PermissionRequest{
			Kind: executionauth.PermissionOperationStatelessCommand, HostFilesystem: true,
			Network: true, OperatorApproved: true,
		})
	if err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	if !decision.Allowed {
		return OnceCommandRunResult{}, apperror.New(apperror.CodePolicyDenied,
			"once command proposal execution denied: "+decision.Reason)
	}
	requestedBy = strings.TrimSpace(requestedBy)
	if requestedBy == "" {
		requestedBy = "cli_operator"
	}
	service := NewOnceCommandService(s.store.(OnceCommandStore), s.executor, s.capabilities)
	result, err := service.Execute(ctx, OnceCommandRunRequest{
		RunID: proposal.RunID, ExecutablePath: proposal.ExecutablePath, Argv: proposal.Argv,
		WorkingDirectory: proposal.WorkingDirectory, Environment: environment,
		TimeoutMilliseconds: proposal.TimeoutMilliseconds, Purpose: proposal.Purpose,
		RequestedBy: requestedBy, OperatorApproved: true,
	})
	if err != nil {
		return OnceCommandRunResult{}, err
	}
	if err := s.store.MarkOnceCommandProposalExecuted(ctx, proposal.ID, result.RequestFingerprint); err != nil {
		return OnceCommandRunResult{}, apperror.Normalize(err)
	}
	return result, nil
}
