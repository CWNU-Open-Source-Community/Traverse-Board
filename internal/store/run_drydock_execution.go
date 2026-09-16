package store

import (
	"context"
	"database/sql"
	"time"

	"cyberagent-workbench/internal/apperror"
)

func hasRunFileDrydockBindings(ctx context.Context, q drydockQueryer) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master
		WHERE type='view' AND name='run_file_drydock_bindings')`).Scan(&exists)
	return exists, err
}

// A historical physical owner remains readable, but cannot regain execution
// authority once its Thread has published a later holder.
func requireCurrentRunDrydockTx(ctx context.Context, tx *sql.Tx, runID string) error {
	exists, err := hasRunFileDrydockBindings(ctx, tx)
	if err != nil || !exists {
		return err
	}
	workspace, found, err := getRunFileDrydock(ctx, tx, runID)
	if err != nil || !found {
		return err
	}
	holder, found, err := getDrydockHolder(ctx, tx, workspace.ID)
	if err != nil {
		return err
	}
	if !found || holder.RunID != runID {
		return apperror.New(apperror.CodeFailedPrecondition, "This execution context no longer holds the Thread working directory")
	}
	return nil
}

// Run leases also serialize all epochs that share one physical directory. A
// durable cleanup journal has no timeout; only exact cleanup recovery releases it.
func requireDrydockExecutionAdmissionTx(ctx context.Context, tx *sql.Tx, runID string, now time.Time) error {
	if err := requireCurrentRunDrydockTx(ctx, tx, runID); err != nil {
		return err
	}
	exists, err := hasRunFileDrydockBindings(ctx, tx)
	if err != nil || !exists {
		return err
	}
	workspace, found, err := getRunFileDrydock(ctx, tx, runID)
	if err != nil {
		return err
	}
	if found && (workspace.State == "cleaned" || workspace.State == "recovery_required") {
		return apperror.New(apperror.CodeFailedPrecondition, "Thread working directory requires recovery before execution")
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM run_file_drydock_bindings current
		JOIN run_file_drydock_bindings other ON other.drydock_id=current.drydock_id
		JOIN run_execution_leases lease ON lease.run_id=other.run_id
		WHERE current.run_id=? AND other.run_id<>? AND lease.status='active'
		AND julianday(lease.expires_at)>julianday(?))`, runID, runID, ts(now)).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return apperror.New(apperror.CodeConflict, "Thread working directory is executing under another context")
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM run_file_drydock_bindings current
		JOIN run_file_drydock_bindings other ON other.drydock_id=current.drydock_id
		JOIN workspace_checkpoint_transactions pending ON pending.run_id=other.run_id
		WHERE current.run_id=? AND pending.workspace_id=current.workspace_id
		AND pending.status IN ('prepared','applying')
		AND other.run_id<>?)`, runID, runID).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return apperror.New(apperror.CodeConflict, "Thread working directory has an unfinished mutation or cleanup")
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM run_file_drydock_bindings binding JOIN drydock_cleanup_operations cleanup
 ON cleanup.drydock_id=binding.drydock_id WHERE binding.run_id=? AND cleanup.status='prepared')`, runID).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return apperror.New(apperror.CodeConflict, "Thread working directory cleanup is not yet confirmed")
	}
	return nil
}
