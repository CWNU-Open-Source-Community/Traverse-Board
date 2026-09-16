package store

import (
	"context"
	"database/sql"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

func requireThreadContinueRepairSourceTx(ctx context.Context, tx *sql.Tx, run domain.Run,
	checkpoint domain.SupervisorCheckpoint, round int,
) error {
	refused := apperror.New(apperror.CodeFailedPrecondition, "Thread continuation correction requires the current interactive Deliver input and completed tool results")
	if !run.Config.Interactive {
		return refused
	}
	thread, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE active_run_id = ? AND status = 'active'`, run.ID))
	if err != nil {
		return err
	}
	mode, err := getCurrentRunModeSnapshot(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	if thread.MissionID != run.MissionID || thread.WorkspaceID != mode.Scope.WorkspaceID || mode.Phase != domain.ExecutionPhaseDeliver {
		return refused
	}
	var prepared int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM operator_steering_deliveries WHERE run_id = ? AND attempt_id = ? AND status = 'prepared'`, run.ID, checkpoint.AttemptID).Scan(&prepared); err != nil {
		return err
	}
	_, approvalContinuation, err := approvalContinuationForCheckpointTx(ctx, tx, checkpoint)
	if err != nil {
		return err
	}
	if prepared > 1 || (prepared != 1 && !approvalContinuation) {
		return refused
	}
	var total, completed int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(completed_at) FROM run_supervisor_tool_rounds WHERE run_id = ? AND turn = ? AND attempt_id = ?`, run.ID, checkpoint.NextTurn, checkpoint.AttemptID).Scan(&total, &completed); err != nil {
		return err
	}
	if total != round || completed != round {
		return refused
	}
	return nil
}
