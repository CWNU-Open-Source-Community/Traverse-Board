package application

import (
	"context"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

type unsealedWebFetchThreadFailureStore interface {
	webFetchAuthorizationFailureStore
	threadTurnFailureStore
	GetUnsealedWebFetchThreadFailure(context.Context, string) (domain.WebFetchAuthorization, domain.SupervisorCheckpoint, bool, error)
}

func (s *RunExecutionHandoffService) closeHistoricalWebFetchThreadFailure(ctx context.Context, threadID string) error {
	recorder, ok := s.store.(unsealedWebFetchThreadFailureStore)
	if !ok {
		return nil
	}
	value, checkpoint, found, err := recorder.GetUnsealedWebFetchThreadFailure(ctx, threadID)
	if err != nil || !found {
		return apperror.Normalize(err)
	}
	handoff, bound, err := recorder.PrepareWebFetchAuthorizationHandoff(ctx, value.ID,
		checkpoint.AttemptID, domain.SupervisorTurnFailed)
	if err != nil {
		return apperror.Normalize(err)
	}
	if !bound {
		return apperror.New(apperror.CodeConflict, "The earlier web fetch failure changed before it could be preserved")
	}
	if handoff.Result == nil {
		err = s.supervisor.withRunExecutionLease(ctx, value.RunID, func(leaseCtx context.Context, lease domain.RunExecutionLease) error {
			current, exists, err := s.store.GetSupervisorCheckpoint(leaseCtx, value.RunID)
			if err != nil {
				return apperror.Normalize(err)
			}
			if !exists || current.Phase != domain.SupervisorTurnFailed || current.AttemptID != checkpoint.AttemptID ||
				current.NextTurn != checkpoint.NextTurn || current.LeaseID != checkpoint.LeaseID ||
				current.LeaseGeneration != checkpoint.LeaseGeneration || current.PendingInput != checkpoint.PendingInput {
				return apperror.New(apperror.CodeConflict, "The earlier web fetch failure changed before observation")
			}
			// The old record has a text error, not a machine-readable error code.
			// This precondition describes the observed failed boundary. It does
			// not assign a new cause to the old model call or rerun any work.
			return s.completeWebFetchContinuation(leaseCtx, lease, &handoff, LifecycleResult{},
				apperror.New(apperror.CodeFailedPrecondition, "Previously recorded web fetch continuation failed"),
				"observed_failed_web_fetch")
		})
		if err != nil {
			return apperror.Normalize(err)
		}
	}
	_, closed, err := closeFailedProductTurn(ctx, recorder, threadID, handoff)
	if err != nil {
		return err
	}
	if !closed {
		return apperror.New(apperror.CodeFailedPrecondition, "The earlier web fetch failure has not reached a safely closed Thread boundary")
	}
	return nil
}
