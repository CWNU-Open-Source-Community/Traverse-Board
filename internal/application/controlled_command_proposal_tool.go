package application

import (
	"context"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

type ControlledCommandProposalMutationStore interface {
	GetRun(ctx context.Context, id string) (domain.Run, error)
	GetMission(ctx context.Context, id string) (domain.Mission, error)
	GetWorkspaceByID(ctx context.Context, id string) (session.WorkspaceRecord, error)
	GetRunExecutionInteraction(
		ctx context.Context, runID string,
	) (domain.RunExecutionInteractionSnapshot, error)
	GetRunExecutionProfile(
		ctx context.Context, runID string,
	) (domain.RunExecutionProfileSnapshot, error)
	GetRunExecutionPermission(
		ctx context.Context, runID string,
	) (domain.RunExecutionPermissionSnapshot, error)
	GetRunMode(ctx context.Context, runID string) (domain.RunModeSnapshot, error)
	CreateControlledCommandProposal(
		ctx context.Context,
		operation runner.ControlledCommandProposalOperation,
		proposal runner.ControlledCommandProposal,
	) (runner.ControlledCommandProposal, bool, error)
}

type ControlledCommandProposalToolExecutor struct {
	store ControlledCommandProposalMutationStore
}

func NewControlledCommandProposalToolExecutor(
	store ControlledCommandProposalMutationStore,
) *ControlledCommandProposalToolExecutor {
	return &ControlledCommandProposalToolExecutor{store: store}
}

func (e *ControlledCommandProposalToolExecutor) ProposeControlledCommand(
	ctx context.Context,
	scope toolgateway.ControlledCommandProposalContext,
	spec toolgateway.ControlledCommandProposalSpec,
) (toolgateway.ControlledCommandProposalResult, error) {
	if e == nil || e.store == nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.New(apperror.CodeFailedPrecondition,
				"controlled command proposal mutation store is required")
	}
	if err := scope.Validate(); err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	if err := spec.Validate(); err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	run, err := e.store.GetRun(ctx, scope.RunID)
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Normalize(err)
	}
	mission, err := e.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Normalize(err)
	}
	if mission.WorkspaceID == "" || mission.WorkspaceID != scope.WorkspaceID {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.New(apperror.CodeFailedPrecondition,
				"controlled command proposal requires the current Workspace")
	}
	workspace, err := e.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Normalize(err)
	}
	interaction, err := e.store.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Normalize(err)
	}
	profile, err := e.store.GetRunExecutionProfile(ctx, run.ID)
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Normalize(err)
	}
	permission, err := e.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Normalize(err)
	}
	mode, err := e.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Normalize(err)
	}
	operationDigest := runmutation.OperationKeyDigest(
		string(toolgateway.ControlledCommandProposeTool),
		scope.RunID, scope.OperationKey)
	plan, err := runner.PlanControlledCommand(
		runner.ControlledCommandPlanRequest{
			ID:            "controlled-command-plan-" + operationDigest[:24],
			WorkspaceID:   mission.WorkspaceID,
			WorkspaceRoot: workspace.RootPath,
			Interaction:   interaction, CurrentProfile: profile,
			CurrentSurface: mode.Surface, Kind: spec.Kind,
			RelativePath: spec.RelativePath,
			Timeout:      time.Duration(spec.TimeoutMS) * time.Millisecond,
		})
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Wrap(apperror.CodeFailedPrecondition,
				"controlled command proposal cannot bind a current fixed plan", err)
	}
	now := time.Now().UTC()
	proposal, err := runner.NewControlledCommandProposal(
		runner.ControlledCommandProposalRequest{
			ID:   "controlled-command-proposal-" + operationDigest[:24],
			Plan: plan, MissionID: mission.ID, SessionID: scope.SessionID,
			RootAgentID: scope.RootAgentID, Permission: permission,
			Purpose: spec.Purpose, RequestedBy: scope.RequestedBy,
			CreatedAt: now,
		})
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Wrap(apperror.CodeInvalidArgument,
				"controlled command proposal is invalid", err)
	}
	operation := runner.ControlledCommandProposalOperation{
		KeyDigest: operationDigest,
		RequestFingerprint: runner.ControlledCommandProposalRequestFingerprint(
			proposal),
		InvocationID: scope.InvocationID, ProposalID: proposal.ID,
		RunID: scope.RunID, SessionID: scope.SessionID,
		WorkspaceID: scope.WorkspaceID, RootAgentID: scope.RootAgentID,
		LeaseID: scope.LeaseID, LeaseGeneration: scope.LeaseGeneration,
		RequestedBy: scope.RequestedBy, CreatedAt: now,
	}
	stored, replayed, err := e.store.CreateControlledCommandProposal(
		ctx, operation, proposal)
	if err != nil {
		return toolgateway.ControlledCommandProposalResult{},
			apperror.Normalize(err)
	}
	return toolgateway.ControlledCommandProposalResult{
		ProposalID: stored.ID, Kind: stored.Kind, Replayed: replayed,
	}, nil
}
