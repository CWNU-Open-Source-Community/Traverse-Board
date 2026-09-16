package application

import (
	"context"
	"database/sql"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

// supervisorThreadEndTurn identifies only the interactive reply that the Store
// will settle as continue/end-turn. It does not certify or complete any plan,
// work item, delivery report, or Run. The Store still rechecks the exact prepared
// input/approval continuation and execution lease at the completion transaction.
func supervisorThreadEndTurn(ctx context.Context, source any, turn domain.SupervisorTurn) (bool, error) {
	if !turn.Run.Config.Interactive || (!turn.OperatorSteering && !turn.ApprovalContinuation) ||
		turn.Mode.Phase != domain.ExecutionPhaseDeliver || turn.Run.Status != domain.RunRunning ||
		turn.Checkpoint.RunID != turn.Run.ID || turn.Checkpoint.Phase != domain.SupervisorTurnStarted ||
		turn.Checkpoint.AttemptID == "" || turn.Checkpoint.LeaseID == "" || turn.Checkpoint.LeaseGeneration <= 0 {
		return false, nil
	}
	reader, ok := source.(interface {
		GetThreadByRun(context.Context, string) (domain.Thread, error)
	})
	if !ok {
		return false, nil
	}
	thread, err := reader.GetThreadByRun(ctx, turn.Run.ID)
	if errors.Is(err, sql.ErrNoRows) || apperror.CodeOf(err) == apperror.CodeNotFound {
		return false, nil
	}
	if err != nil {
		return false, apperror.Normalize(err)
	}
	return thread.Validate() == nil && thread.Status == domain.ThreadActive &&
		thread.ActiveRunID == turn.Run.ID && thread.MissionID == turn.Run.MissionID &&
		thread.MissionID == turn.Mission.ID && thread.WorkspaceID == turn.Mission.WorkspaceID, nil
}

func supervisorValidationAction(action domain.RootAction, endTurn bool) domain.RootAction {
	if endTurn && action.Kind == domain.RootActionFinish {
		action.Kind = domain.RootActionContinue
		action.Summary, action.Reason = "", ""
	}
	return action
}
