package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

// OneShotCommandProposalStore is the bounded store surface the proposal tool
// and its operator review flow touch.
type OneShotCommandProposalStore interface {
	OnceCommandStore
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetWorkspaceByID(context.Context, string) (session.WorkspaceRecord, error)
	GetRunExecutionPermission(context.Context, string) (domain.RunExecutionPermissionSnapshot, error)
	CreateOnceCommandProposal(context.Context, runner.OnceCommandProposalOperation, runner.OnceCommandProposal) (runner.OnceCommandProposal, bool, error)
	GetOnceCommandProposal(context.Context, string) (runner.OnceCommandProposal, bool, error)
	ListOnceCommandProposals(context.Context, string, int) ([]runner.OnceCommandProposal, error)
	ReviewOnceCommandProposal(context.Context, string, string, string, string, string, time.Time) (runner.OnceCommandProposal, bool, error)
	MarkOnceCommandProposalExecuted(context.Context, string, string) error
}

// OneShotCommandProposalToolExecutor records the structured agent request.
// It never executes anything; execution stays on the operator path.
type OneShotCommandProposalToolExecutor struct {
	store OneShotCommandProposalStore
}

func NewOneShotCommandProposalToolExecutor(store OneShotCommandProposalStore) *OneShotCommandProposalToolExecutor {
	return &OneShotCommandProposalToolExecutor{store: store}
}

func (e *OneShotCommandProposalToolExecutor) ProposeOneShotCommand(ctx context.Context,
	scope toolgateway.OneShotCommandProposalContext,
	spec toolgateway.OneShotCommandProposalSpec,
) (toolgateway.OneShotCommandProposalResult, error) {
	if e == nil || e.store == nil {
		return toolgateway.OneShotCommandProposalResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "one-shot command proposal store is required")
	}
	if err := scope.Validate(); err != nil {
		return toolgateway.OneShotCommandProposalResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	if err := spec.Validate(); err != nil {
		return toolgateway.OneShotCommandProposalResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	run, err := e.store.GetRun(ctx, scope.RunID)
	if err != nil {
		return toolgateway.OneShotCommandProposalResult{}, apperror.Normalize(err)
	}
	mission, err := e.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return toolgateway.OneShotCommandProposalResult{}, apperror.Normalize(err)
	}
	if mission.WorkspaceID == "" || mission.WorkspaceID != scope.WorkspaceID {
		return toolgateway.OneShotCommandProposalResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "one-shot command proposal requires the current Workspace")
	}
	workspace, err := e.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return toolgateway.OneShotCommandProposalResult{}, apperror.Normalize(err)
	}
	runnerSpec := spec.ToRunnerSpec()
	if err := runner.ValidateOnceCommandSpec(runnerSpec, workspace.RootPath); err != nil {
		return toolgateway.OneShotCommandProposalResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "one-shot command boundary is invalid", err)
	}
	// Tier gating belongs to review/execution: a proposal is a request for
	// exactly that gate, mirroring the controlled-command proposal flow.
	operationDigest := runmutation.OperationKeyDigest(string(toolgateway.OneShotCommandProposeTool),
		scope.RunID, scope.OperationKey)
	specFingerprint := runner.OnceCommandSpecFingerprint(runnerSpec)
	requestFingerprint := runner.OnceCommandRequestFingerprint(run.ID, workspace.ID, runnerSpec)
	envKeys := make([]string, len(runnerSpec.Environment))
	for index, entry := range runnerSpec.Environment {
		key, _, _ := strings.Cut(entry, "=")
		envKeys[index] = key
	}
	sort.Strings(envKeys)
	envDigest := sha256.Sum256([]byte(strings.Join(envKeys, "\x00")))
	now := time.Now().UTC()
	proposal := runner.OnceCommandProposal{
		ID: idgen.New("once-proposal"), ProtocolVersion: runner.OnceCommandProtocolVersion,
		OperationKeyDigest: operationDigest, RequestFingerprint: requestFingerprint,
		RunID: run.ID, RootAgentID: scope.RootAgentID, SessionID: scope.SessionID,
		WorkspaceID: workspace.ID, ExecutablePath: runnerSpec.ExecutablePath,
		Argv: runnerSpec.Argv, WorkingDirectory: runnerSpec.WorkingDirectory,
		EnvironmentKeys: envKeys, EnvironmentSHA256: hex.EncodeToString(envDigest[:]),
		TimeoutMilliseconds: runnerSpec.TimeoutMilliseconds, Purpose: runnerSpec.Purpose,
		SpecFingerprint: specFingerprint, Status: "proposed", CreatedAt: now,
	}
	stored, replayed, err := e.store.CreateOnceCommandProposal(ctx,
		runner.OnceCommandProposalOperation{KeyDigest: operationDigest,
			RequestFingerprint: requestFingerprint, ProposalID: proposal.ID}, proposal)
	if err != nil {
		return toolgateway.OneShotCommandProposalResult{}, apperror.Normalize(err)
	}
	return toolgateway.OneShotCommandProposalResult{ProposalID: stored.ID, Replayed: replayed}, nil
}
