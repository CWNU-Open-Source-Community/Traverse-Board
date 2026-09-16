package application

import (
	"context"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

type supervisorModelFailureUsageStore interface {
	RecordSupervisorModelFailedWithUsage(context.Context, domain.SupervisorCheckpoint,
		llm.ModelAttempt, llm.Usage, int) (domain.SupervisorCheckpoint, error)
}

func sameModelAccountingEpoch(current, expected domain.SupervisorCheckpoint) bool {
	return current.RunID == expected.RunID && current.AttemptID == expected.AttemptID &&
		current.NextTurn == expected.NextTurn && current.LeaseID == expected.LeaseID &&
		current.LeaseGeneration == expected.LeaseGeneration
}

func (s *RunSupervisor) failReceivedModelOutput(ctx context.Context, result *LifecycleResult,
	turn *domain.SupervisorTurn, attempt llm.ModelAttempt, response llm.ChatResponse, cause error,
) error {
	attempt.Outcome = llm.OutcomeInvalidResponse
	if ctx.Err() != nil || apperror.CodeOf(cause) == apperror.CodeCancelled {
		attempt.Outcome = llm.OutcomeCancelled
	}
	attempt.ErrorText = "model output could not be committed"
	attempt.RetryPlanned, attempt.RetryAfter = false, 0
	updated, accountingErr := s.recordFailedModelAccounting(ctx, turn.Checkpoint,
		attempt, &response.Usage, len(response.ToolCalls))
	elapsed := attempt.Elapsed
	if updated.RunID != "" {
		if !sameModelAccountingEpoch(updated, turn.Checkpoint) {
			// A late receipt may update accounting after the original owner or
			// turn has gone away. It must not fail that newer execution.
			return errors.Join(cause, accountingErr)
		}
		turn.Checkpoint, result.Checkpoint = updated, updated
		elapsed = 0
	}
	return s.recordFailure(ctx, result, errors.Join(cause, accountingErr), elapsed)
}

func (s *RunSupervisor) settleModelAccounting(ctx context.Context, runID string,
	attempt llm.ModelAttempt, usage llm.Usage, toolCount int,
) error {
	eventCtx, cancel := supervisorModelEventContext(ctx)
	defer cancel()
	_, err := s.monetary.SettleModelCall(eventCtx, runID, domain.MonetaryScopeRoot, attempt, usage, toolCount)
	return err
}

// Only the receipt and usage survive a rejected/cancelled response. This path
// cannot dispatch the response's tools or commit it as an assistant message.
func (s *RunSupervisor) recordFailedModelAccounting(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint, attempt llm.ModelAttempt,
	usage *llm.Usage, toolCount int,
) (domain.SupervisorCheckpoint, error) {
	eventCtx, cancel := supervisorModelEventContext(ctx)
	defer cancel()
	var updated domain.SupervisorCheckpoint
	var err error
	if usage != nil {
		if accounting, ok := s.store.(supervisorModelFailureUsageStore); ok {
			updated, err = accounting.RecordSupervisorModelFailedWithUsage(eventCtx, checkpoint,
				attempt, *usage, toolCount)
		} else {
			updated, err = s.store.RecordSupervisorModelFailed(eventCtx, checkpoint, attempt)
		}
	} else {
		updated, err = s.store.RecordSupervisorModelFailed(eventCtx, checkpoint, attempt)
	}
	if err != nil {
		// The sent call has no confirmed terminal receipt. Keep the reservation
		// open so a restart cannot mistake it for a free, repeatable request.
		return updated, err
	}
	var moneyErr error
	if usage == nil {
		_, moneyErr = s.monetary.SettleUnknownModelCall(eventCtx, checkpoint.RunID,
			domain.MonetaryScopeRoot, attempt)
	} else {
		_, moneyErr = s.monetary.SettleModelCall(eventCtx, checkpoint.RunID,
			domain.MonetaryScopeRoot, attempt, *usage, toolCount)
	}
	return updated, errors.Join(err, moneyErr)
}
