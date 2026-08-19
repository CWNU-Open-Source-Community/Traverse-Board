package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

// GitMutationStore is the bounded store surface for the typed Git workflow.
type GitMutationStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetWorkspaceByID(context.Context, string) (session.WorkspaceRecord, error)
	CreateGitMutationOperation(context.Context, repository.MutationRecord) (repository.MutationRecord, bool, error)
	CompleteGitMutationOperation(context.Context, string, repository.MutationRecord, time.Time) (repository.MutationRecord, bool, error)
	GetGitMutationRecord(context.Context, string) (repository.MutationRecord, bool, error)
}

// GitMutationService owns the review-then-execute flow. Every execution is
// bound to a Run, a Workspace, and the exact reviewed repository state.
type GitMutationService struct {
	store       GitMutationStore
	executor    *repository.MutationExecutor
	checkpoints *WorkspaceCheckpointService
}

func NewGitMutationService(store GitMutationStore, executor *repository.MutationExecutor,
	checkpoints ...*WorkspaceCheckpointService,
) *GitMutationService {
	checkpointService := embeddedWorkspaceCheckpointService(store,
		domain.ExecutionPermissionRuntimeCapabilities{})
	if len(checkpoints) != 0 {
		checkpointService = checkpoints[0]
	}
	return &GitMutationService{store: store, executor: executor,
		checkpoints: checkpointService}
}

type GitMutationRequest struct {
	RunID        string
	OperationKey string
	Spec         repository.MutationSpec
	RequestedBy  string
}

type GitMutationReviewResult struct {
	Review      repository.MutationReview
	RunID       string
	WorkspaceID string
}

type GitMutationExecuteResult struct {
	Record   repository.MutationRecord
	Receipt  repository.MutationReceipt
	Replayed bool
}

// Review resolves the Workspace root from the Run and produces the pre-
// execution evidence: bound state, file list, and bounded diff stat.
func (s *GitMutationService) Review(ctx context.Context, runID string, spec repository.MutationSpec) (GitMutationReviewResult, error) {
	if s == nil || s.store == nil || s.executor == nil || !s.executor.Available() {
		return GitMutationReviewResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"git mutation service requires a store and an available executor")
	}
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return GitMutationReviewResult{}, apperror.Normalize(err)
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return GitMutationReviewResult{}, apperror.Normalize(err)
	}
	if strings.TrimSpace(mission.WorkspaceID) == "" {
		return GitMutationReviewResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"git mutation requires a registered Workspace")
	}
	workspace, err := s.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return GitMutationReviewResult{}, apperror.Normalize(err)
	}
	review, err := s.executor.Review(ctx, workspace.RootPath, spec)
	if err != nil {
		return GitMutationReviewResult{}, apperror.Normalize(err)
	}
	return GitMutationReviewResult{Review: review, RunID: run.ID, WorkspaceID: workspace.ID}, nil
}

// Execute re-verifies the reviewed binding, runs the typed operation, and
// records the receipt plus the metadata-only run event. The operation key
// makes retries idempotent.
func (s *GitMutationService) Execute(ctx context.Context, request GitMutationRequest,
	binding repository.MutationBinding,
) (GitMutationExecuteResult, error) {
	if s == nil || s.store == nil || s.executor == nil || !s.executor.Available() {
		return GitMutationExecuteResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"git mutation service requires a store and an available executor")
	}
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	if request.RequestedBy == "" {
		request.RequestedBy = "cli_operator"
	}
	run, err := s.store.GetRun(ctx, request.RunID)
	if err != nil {
		return GitMutationExecuteResult{}, apperror.Normalize(err)
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return GitMutationExecuteResult{}, apperror.Normalize(err)
	}
	workspace, err := s.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return GitMutationExecuteResult{}, apperror.Normalize(err)
	}
	specJSON, err := json.Marshal(request.Spec)
	if err != nil {
		return GitMutationExecuteResult{}, err
	}
	keyDigest := runmutation.OperationKeyDigest("git_mutation_operation.v1", run.ID, request.OperationKey)
	requestFingerprint := runmutation.Fingerprint("git_mutation_request.v1", run.ID,
		workspace.ID, binding.Head, binding.IndexSHA256, binding.StatusFingerprint, string(specJSON))
	record := repository.MutationRecord{
		ID: idgen.New("git-mutation"), ProtocolVersion: repository.MutationProtocolVersion,
		OperationKeyDigest: keyDigest, RequestFingerprint: requestFingerprint,
		RunID: run.ID, WorkspaceID: workspace.ID, Operation: request.Spec.Operation,
		SpecJSON: string(specJSON), PreHead: binding.Head, CreatedAt: time.Now().UTC(),
	}
	boundaryRequest := WorkspaceMutationBoundaryRequest{RunID: run.ID,
		Kind: workspacecheckpoint.TransactionGitMutation, OperationKey: keyDigest,
		TriggerReceiptID: keyDigest}
	if s.checkpoints != nil {
		if _, err := s.checkpoints.BeginBoundary(ctx, boundaryRequest); err != nil {
			return GitMutationExecuteResult{}, err
		}
	}
	completeBoundary := func(cause error) error {
		if s.checkpoints == nil {
			return nil
		}
		completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx),
			30*time.Second)
		defer cancel()
		_, err := s.checkpoints.CompleteBoundary(completionCtx, boundaryRequest, cause)
		return err
	}
	created, replayed, err := s.store.CreateGitMutationOperation(ctx, record)
	if err != nil {
		operationErr := apperror.Normalize(err)
		return GitMutationExecuteResult{}, errors.Join(operationErr,
			completeBoundary(operationErr))
	}
	if replayed {
		if created.CompletedAt == nil {
			operationErr := apperror.New(
				apperror.CodeFailedPrecondition,
				"previous git mutation did not record a terminal receipt; reconcile repository state before using a new operation key")
			return GitMutationExecuteResult{Record: created, Replayed: true},
				errors.Join(operationErr, completeBoundary(operationErr))
		}
		return GitMutationExecuteResult{Record: created, Replayed: true},
			completeBoundary(nil)
	}
	receipt, err := s.executor.Execute(ctx, workspace.RootPath, request.Spec, binding)
	if err != nil {
		return GitMutationExecuteResult{}, errors.Join(err, completeBoundary(err))
	}
	completed, _, err := s.store.CompleteGitMutationOperation(ctx, created.ID, repository.MutationRecord{
		PostHead: receipt.PostHead, Branch: receipt.Branch, CommitID: receipt.CommitID,
		Conflicted: receipt.Conflicted, Clean: receipt.Clean, StderrPrefix: receipt.StderrPrefix,
	}, time.Now().UTC())
	if err != nil {
		operationErr := apperror.Normalize(err)
		return GitMutationExecuteResult{}, errors.Join(operationErr,
			completeBoundary(operationErr))
	}
	result := GitMutationExecuteResult{Record: completed, Receipt: receipt}
	return result, completeBoundary(nil)
}
