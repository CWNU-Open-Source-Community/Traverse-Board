package store

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/drydock"
	"database/sql"
	"errors"
	"time"
)

// BeginThreadDrydockCleanup fences the physical directory until the same
// request confirms its filesystem outcome. Timeouts cannot release this fence.
func (s *SQLiteStore) BeginThreadDrydockCleanup(ctx context.Context, runID, drydockID string,
	generation int64, digest, fingerprint string, at time.Time,
) error {
	if runID == "" || drydockID == "" || generation < 1 || !drydock.ValidDigest(digest) || !drydock.ValidDigest(fingerprint) || at.IsZero() {
		return apperror.New(apperror.CodeInvalidArgument, "Drydock cleanup identity is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockRunControlTx(ctx, tx, runID); err != nil {
		return err
	}
	var storedRun, storedDrydock, storedFingerprint string
	var storedGeneration int64
	err = tx.QueryRowContext(ctx, `SELECT run_id,drydock_id,expected_generation,request_fingerprint
	 FROM drydock_cleanup_operations WHERE operation_key_sha256=?`, digest).Scan(&storedRun, &storedDrydock, &storedGeneration, &storedFingerprint)
	if err == nil {
		if storedRun != runID || storedDrydock != drydockID || storedGeneration != generation || storedFingerprint != fingerprint {
			return apperror.New(apperror.CodeConflict, "Cleanup request changed while its result was unknown")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := requireCurrentRunDrydockTx(ctx, tx, runID); err != nil {
		return err
	}
	workspace, found, err := getRunFileDrydock(ctx, tx, runID)
	if err != nil {
		return err
	}
	if !found || workspace.ID != drydockID || workspace.Generation != generation || workspace.State == drydock.StateCleaned {
		return apperror.New(apperror.CodeConflict, "Cleanup directory ownership changed")
	}
	var busy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
	 SELECT 1 FROM run_file_drydock_bindings b JOIN run_execution_leases l ON l.run_id=b.run_id
	 WHERE b.drydock_id=? AND l.status='active' AND julianday(l.expires_at)>julianday(?))`, drydockID, ts(at)).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return apperror.New(apperror.CodeConflict, "Stop the current execution before cleaning its working directory")
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
	 SELECT 1 FROM run_file_drydock_bindings b JOIN workspace_checkpoint_transactions m ON m.run_id=b.run_id
	 WHERE b.drydock_id=? AND m.workspace_id=? AND m.status IN ('prepared','applying'))`, drydockID, workspace.WorkspaceID).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return apperror.New(apperror.CodeConflict, "Confirm the unfinished directory operation before cleanup")
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operator_steering_messages WHERE run_id=? AND status='pending')`, runID).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return apperror.New(apperror.CodeConflict, "The conversation still has accepted input to process")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO drydock_cleanup_operations
	 (operation_key_sha256,request_fingerprint,drydock_id,run_id,expected_generation,status,created_at)
	 VALUES(?,?,?,?,?,'prepared',?)`, digest, fingerprint, drydockID, runID, generation, ts(at))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteThreadDrydockCleanup commits actual physical state and receipt
// together, without a synthetic content checkpoint for a removed directory.
func (s *SQLiteStore) CompleteThreadDrydockCleanup(ctx context.Context, workspace drydock.Workspace,
	generation int64, receipt drydock.Receipt,
) (drydock.Workspace, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workspace, false, err
	}
	defer tx.Rollback()
	if err := lockRunControlTx(ctx, tx, receipt.RunID); err != nil {
		return workspace, false, err
	}
	var matches bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM drydock_cleanup_operations
	 WHERE operation_key_sha256=? AND request_fingerprint=? AND drydock_id=? AND run_id=? AND expected_generation=?)`,
		receipt.OperationKeySHA256, receipt.RequestFingerprint, workspace.ID, receipt.RunID, generation).Scan(&matches); err != nil {
		return workspace, false, err
	}
	if !matches || receipt.Operation != drydock.OperationCleanup {
		return workspace, false, apperror.New(apperror.CodeConflict, "Cleanup receipt differs from its reserved directory operation")
	}
	updated, replayed, err := advanceDrydockTx(ctx, tx, workspace, generation, receipt)
	if err != nil {
		return workspace, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drydock_cleanup_operations SET status='completed',receipt_id=?,completed_at=?
	 WHERE operation_key_sha256=? AND status='prepared'`, receipt.ID, ts(receipt.CreatedAt), receipt.OperationKeySHA256); err != nil {
		return workspace, false, err
	}
	return updated, replayed, tx.Commit()
}
