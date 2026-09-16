package store

import (
	"context"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

// AdvanceThreadRunForExhaustedBudget retires only a proven exhausted, idle
// execution epoch. It does not refill its budget or authorize its successor.
func (s *SQLiteStore) AdvanceThreadRunForExhaustedBudget(ctx context.Context, threadID, runID, requestedBy string) (domain.Run, bool, error) {
	if !domain.ValidAgentID(threadID) || !domain.ValidAgentID(runID) || !domain.ValidAgentID(requestedBy) {
		return domain.Run{}, false, apperror.New(apperror.CodeInvalidArgument, "Thread budget transition identity is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Run{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET updated_at=updated_at WHERE id=?`, runID); err != nil {
		return domain.Run{}, false, err
	}
	thread, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id=?`, threadID))
	if err != nil {
		return domain.Run{}, false, err
	}
	run, err := getRunControlRunTx(ctx, tx, runID)
	if err != nil {
		return domain.Run{}, false, err
	}
	if run.Terminal() || thread.ActiveRunID != runID {
		return run, false, nil
	}
	if thread.Status != domain.ThreadActive || thread.MissionID != run.MissionID {
		return run, false, apperror.New(apperror.CodeConflict, "Thread budget epoch is no longer active")
	}
	cp, found, err := getSupervisorCheckpointTx(ctx, tx, runID)
	if err != nil {
		return run, false, err
	}
	if !found || !(cp.NextTurn > run.Budget.MaxTurns || (run.Budget.MaxTokens > 0 && cp.TotalTokens >= run.Budget.MaxTokens) || (run.Budget.TimeoutSeconds > 0 && cp.ExecutionMillis >= run.Budget.TimeoutSeconds*1000)) {
		return run, false, nil
	}
	if err := requireThreadLifecycleRunQuiescentTx(ctx, tx, runID, time.Now().UTC()); err != nil {
		return run, false, err
	}
	if cp.AttemptID != "" || cp.PendingInput != "" {
		return run, false, apperror.New(apperror.CodeFailedPrecondition, "The exhausted Thread turn must settle before continuing")
	}
	if err := requireThreadToolEffectsSettledTx(ctx, tx, runID); err != nil {
		return run, false, err
	}
	expected := run.Status
	if err := run.Transition(domain.RunCancelled, time.Now().UTC()); err != nil {
		return run, false, err
	}
	event, err := events.New(run.ID, run.MissionID, events.RunStatusChangedEvent, "thread_budget_transition", run.ID, map[string]any{
		"from": expected, "to": run.Status, "reason": "explicit next Thread message advances the exhausted execution budget", "requested_by": requestedBy,
		"next_turn": cp.NextTurn, "total_tokens": cp.TotalTokens, "execution_millis": cp.ExecutionMillis,
	})
	if err != nil {
		return run, false, err
	}
	if err := transitionRunTx(ctx, tx, run, expected, event, "thread_budget_transition"); err != nil {
		return run, false, err
	}
	return run, true, tx.Commit()
}
