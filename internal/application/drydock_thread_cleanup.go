package application

import (
	"context"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/gitadvanced"
)

type drydockCleanupExecutor interface {
	PlanRemove(context.Context, string, string, string) (gitadvanced.Preview, error)
	ExecuteRemove(context.Context, string, gitadvanced.Preview) (gitadvanced.Receipt, error)
}

func (s *DrydockService) replayDrydockCleanup(ctx context.Context, request DrydockCleanupRequest, digest string) (DrydockCleanupResult, bool, error) {
	receipt, found, err := s.store.GetDrydockReceiptByOperation(ctx, digest)
	if err != nil || !found {
		return DrydockCleanupResult{}, false, apperror.Normalize(err)
	}
	workspace, found, err := s.cleanupWorkspace(ctx, request.RunID)
	if err != nil {
		return DrydockCleanupResult{}, false, apperror.Normalize(err)
	}
	if !found {
		return DrydockCleanupResult{}, false, apperror.New(apperror.CodeNotFound, "Drydock cleanup receipt has no bound working directory")
	}
	if receipt.RunID != request.RunID || receipt.Operation != drydock.OperationCleanup ||
		!drydockReceiptMatchesRequest(receipt, workspace.ID, request.ExpectedGeneration,
			drydockCleanupRequestFingerprint(workspace.ID, request)) {
		return DrydockCleanupResult{}, false, apperror.New(apperror.CodeConflict,
			"Drydock cleanup operation key was reused for different intent")
	}
	return DrydockCleanupResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: workspace, Receipt: receipt, Preserved: receipt.Outcome == drydock.OutcomePreserved, Replayed: true}, true, nil
}

func (s *DrydockService) completeDrydockCleanup(ctx context.Context, request DrydockCleanupRequest,
	digest string, workspace drydock.Workspace, summary, bindingAfter, gitReceiptID string,
) (DrydockCleanupResult, error) {
	beforeGeneration := workspace.Generation
	now := s.now().UTC()
	workspace.State, workspace.RecoveryReason = drydock.StateCleaned, ""
	workspace.Generation++
	workspace.UpdatedAt, workspace.CleanedAt = now, &now
	receipt := s.transitionReceipt(workspace, beforeGeneration, drydock.OperationCleanup, digest,
		drydockCleanupRequestFingerprint(workspace.ID, request), drydock.OutcomeSucceeded, "", summary,
		workspace.ExpectedBindingFingerprint, bindingAfter, gitReceiptID, "", "")
	receipt.RunID = request.RunID
	workspace, replayed, err := s.advanceDrydockTransition(ctx, workspace, beforeGeneration, receipt)
	if err != nil {
		return DrydockCleanupResult{}, apperror.Normalize(err)
	}
	return DrydockCleanupResult{ProtocolVersion: DrydockAPIProtocolVersion,
		Workspace: workspace, Receipt: receipt, Replayed: replayed}, nil
}

func (s *DrydockService) confirmDrydockCleanupFailure(ctx context.Context, request DrydockCleanupRequest,
	digest, gitReceiptID string, cause error,
) (DrydockCleanupResult, error) {
	// A failed Git receipt does not prove that removal did not happen. It may
	// describe a failed post-operation observation or a concurrent same-key
	// caller's already completed removal. Re-read exact ownership and presence.
	workspace, _, observed, inspectErr := s.loadDrydockForCleanup(ctx, request.RunID, request.ExpectedGeneration)
	if inspectErr == nil && !observed.Found && !observed.Present {
		return s.completeDrydockCleanup(ctx, request, digest, workspace,
			"cleanup reported an error; a fresh exact inspection confirmed the directory and Git registration are absent; the Git receipt is not reclassified as successful",
			"", gitReceiptID)
	}
	// Presence is only an observation, not proof that another caller is not
	// still removing this exact directory. Do not release the reservation by
	// sealing a misleading preserved receipt. The original key remains usable.
	return DrydockCleanupResult{}, errors.Join(apperror.Normalize(cause), inspectErr,
		apperror.New(apperror.CodeConflict, "Drydock removal is not confirmed; retry the same cleanup request to inspect its outcome"))
}

type threadDrydockCleanupStore interface {
	GetRunFileDrydock(context.Context, string) (drydock.Workspace, bool, error)
	GetDrydockHolder(context.Context, string) (domain.ThreadDrydockBinding, bool, error)
}

type drydockCleanupReservationStore interface {
	BeginThreadDrydockCleanup(context.Context, string, string, int64, string, string, time.Time) error
	CompleteThreadDrydockCleanup(context.Context, drydock.Workspace, int64, drydock.Receipt) (drydock.Workspace, bool, error)
}

func (s *DrydockService) beginDrydockCleanup(ctx context.Context, request DrydockCleanupRequest, digest string) error {
	store, ok := s.store.(drydockCleanupReservationStore)
	if !ok {
		return nil
	}
	workspace, found, err := s.cleanupWorkspace(ctx, request.RunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if !found {
		return apperror.New(apperror.CodeNotFound, "Drydock was not found for this Run")
	}
	return apperror.Normalize(store.BeginThreadDrydockCleanup(ctx, request.RunID, workspace.ID,
		request.ExpectedGeneration, digest, drydockCleanupRequestFingerprint(workspace.ID, request), s.now().UTC()))
}

func (s *DrydockService) advanceDrydockTransition(ctx context.Context, workspace drydock.Workspace,
	generation int64, receipt drydock.Receipt,
) (drydock.Workspace, bool, error) {
	if receipt.Operation == drydock.OperationCleanup {
		if store, ok := s.store.(drydockCleanupReservationStore); ok {
			return store.CompleteThreadDrydockCleanup(ctx, workspace, generation, receipt)
		}
	}
	return s.store.AdvanceDrydock(ctx, workspace, generation, receipt)
}

func (s *DrydockService) cleanupWorkspace(ctx context.Context, runID string) (drydock.Workspace, bool, error) {
	if store, ok := s.store.(threadDrydockCleanupStore); ok {
		return store.GetRunFileDrydock(ctx, runID)
	}
	return s.store.GetDrydockByRun(ctx, runID)
}

func (s *DrydockService) threadRetainsDrydock(ctx context.Context, workspace drydock.Workspace) (bool, error) {
	store, ok := s.store.(threadDrydockCleanupStore)
	if !ok {
		return false, nil
	}
	holder, found, err := store.GetDrydockHolder(ctx, workspace.ID)
	return found && holder.ThreadID != "", err
}

func (s *DrydockService) requireCurrentDrydockCleanupHolder(ctx context.Context, runID string, workspace drydock.Workspace) error {
	store, ok := s.store.(threadDrydockCleanupStore)
	if !ok {
		return nil
	}
	holder, found, err := store.GetDrydockHolder(ctx, workspace.ID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if !found || holder.RunID != runID {
		return apperror.New(apperror.CodeConflict, "This working directory is retained by a later conversation turn; an earlier execution cannot clean it up")
	}
	return nil
}
