package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/events"
)

func (s *SQLiteStore) RecordBrowserRuntimeCheckpoint(ctx context.Context,
	checkpoint browserruntime.BrowserRuntimeCheckpoint,
) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is not open")
	}
	if ctx == nil {
		return errors.New("browser runtime checkpoint context is required")
	}
	if err := browserruntime.ValidateStoredBrowserRuntimeCheckpoint(checkpoint); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := loadBrowserRuntimeCheckpointByIDTx(ctx, tx, checkpoint.ID)
	if err != nil {
		return err
	}
	if found {
		if existing.Fingerprint != checkpoint.Fingerprint {
			return errors.New("browser runtime checkpoint ID was reused with another payload")
		}
		return nil
	}
	var previous browserruntime.BrowserRuntimeCheckpoint
	if checkpoint.Generation > 1 {
		previous, found, err = loadBrowserRuntimeCheckpointGenerationTx(ctx, tx,
			checkpoint.RuntimeID, checkpoint.Generation-1)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("browser runtime checkpoint predecessor is missing")
		}
	}
	if err := browserruntime.ValidateStoredBrowserRuntimeCheckpointSuccessor(
		checkpoint, previous); err != nil {
		return err
	}
	missionID, err := browserRuntimeMissionIDTx(ctx, tx, checkpoint.RunID)
	if err != nil {
		return err
	}
	event, err := events.New(checkpoint.RunID, missionID,
		events.BrowserRuntimeCheckpointRecordedEvent, "browser_runtime",
		checkpoint.ID, map[string]any{
			"runtime_id": checkpoint.RuntimeID, "checkpoint_fingerprint": checkpoint.Fingerprint,
			"generation": checkpoint.Generation, "stage": checkpoint.Stage,
			"recovery_required": checkpoint.RecoveryRequired, "redacted": true,
		})
	if err != nil {
		return err
	}
	event.CreatedAt = checkpoint.RecordedAt
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_runtime_checkpoints
		(id, runtime_id, run_id, attempt_id, attempt_fingerprint,
		authorization_fingerprint, process_start_spec_fingerprint,
		profile_ownership_fingerprint, profile_lease_fingerprint,
		released_profile_fingerprint, previous_checkpoint_fingerprint,
		generation, stage, fingerprint, event_sequence, payload_json, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		checkpoint.ID, checkpoint.RuntimeID, checkpoint.RunID, checkpoint.AttemptID,
		checkpoint.AttemptFingerprint, checkpoint.AuthorizationFingerprint,
		checkpoint.ProcessStartSpecFingerprint, checkpoint.ProfileOwnershipFingerprint,
		checkpoint.ProfileLeaseFingerprint, checkpoint.ReleasedProfileFingerprint,
		checkpoint.PreviousCheckpointFingerprint, checkpoint.Generation, checkpoint.Stage,
		checkpoint.Fingerprint, event.Sequence, string(payload), ts(checkpoint.RecordedAt)); err != nil {
		return fmt.Errorf("insert browser runtime checkpoint: %w", err)
	}
	return tx.Commit()
}

func (s *SQLiteStore) RecordBrowserRuntimeReceipt(ctx context.Context,
	receipt browserruntime.BrowserRuntimeReceipt,
) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is not open")
	}
	if ctx == nil {
		return errors.New("browser runtime receipt context is required")
	}
	if err := browserruntime.ValidateStoredBrowserRuntimeReceipt(receipt); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := loadBrowserRuntimeReceiptByIDTx(ctx, tx, receipt.ID)
	if err != nil {
		return err
	}
	if found {
		if existing.Fingerprint != receipt.Fingerprint {
			return errors.New("browser runtime receipt ID was reused with another payload")
		}
		return nil
	}
	checkpoint, found, err := loadBrowserRuntimeCheckpointFingerprintTx(ctx, tx,
		receipt.FinalCheckpointFingerprint)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("browser runtime receipt final checkpoint is missing")
	}
	if err := browserruntime.ValidateStoredBrowserRuntimeReceiptForCheckpoint(
		receipt, checkpoint); err != nil {
		return err
	}
	missionID, err := browserRuntimeMissionIDTx(ctx, tx, receipt.RunID)
	if err != nil {
		return err
	}
	event, err := events.New(receipt.RunID, missionID,
		events.BrowserRuntimeReceiptRecordedEvent, "browser_runtime", receipt.ID,
		map[string]any{
			"runtime_id": receipt.RuntimeID, "receipt_fingerprint": receipt.Fingerprint,
			"final_checkpoint_fingerprint": receipt.FinalCheckpointFingerprint,
			"succeeded":                    receipt.Succeeded, "recovery_required": receipt.RecoveryRequired,
			"failure_code": receipt.FailureCode, "redacted": true,
		})
	if err != nil {
		return err
	}
	event.CreatedAt = receipt.CompletedAt
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_runtime_receipts
		(id, runtime_id, run_id, attempt_fingerprint, authorization_fingerprint,
		final_checkpoint_fingerprint, process_exit_fingerprint,
		released_profile_fingerprint, succeeded, recovery_required, failure_code,
		fingerprint, event_sequence, payload_json, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		receipt.ID, receipt.RuntimeID, receipt.RunID, receipt.AttemptFingerprint,
		receipt.AuthorizationFingerprint, receipt.FinalCheckpointFingerprint,
		receipt.ProcessExitFingerprint, receipt.ReleasedProfileFingerprint,
		receipt.Succeeded, receipt.RecoveryRequired, receipt.FailureCode,
		receipt.Fingerprint, event.Sequence, string(payload), ts(receipt.StartedAt),
		ts(receipt.CompletedAt)); err != nil {
		return fmt.Errorf("insert browser runtime receipt: %w", err)
	}
	return tx.Commit()
}

func (s *SQLiteStore) LoadLatestBrowserRuntimeCheckpoint(ctx context.Context,
	runtimeID string,
) (browserruntime.BrowserRuntimeCheckpoint, bool, error) {
	if s == nil || s.db == nil {
		return browserruntime.BrowserRuntimeCheckpoint{}, false,
			errors.New("sqlite store is not open")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json
		FROM browser_runtime_checkpoints WHERE runtime_id = ?
		ORDER BY generation DESC LIMIT 1`, runtimeID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return browserruntime.BrowserRuntimeCheckpoint{}, false, nil
	}
	if err != nil {
		return browserruntime.BrowserRuntimeCheckpoint{}, false, err
	}
	checkpoint, err := decodeBrowserRuntimeCheckpoint(payload)
	return checkpoint, err == nil, err
}

func (s *SQLiteStore) LoadBrowserRuntimeReceipt(ctx context.Context,
	runtimeID string,
) (browserruntime.BrowserRuntimeReceipt, bool, error) {
	if s == nil || s.db == nil {
		return browserruntime.BrowserRuntimeReceipt{}, false,
			errors.New("sqlite store is not open")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json
		FROM browser_runtime_receipts WHERE runtime_id = ?`, runtimeID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return browserruntime.BrowserRuntimeReceipt{}, false, nil
	}
	if err != nil {
		return browserruntime.BrowserRuntimeReceipt{}, false, err
	}
	receipt, err := decodeBrowserRuntimeReceipt(payload)
	return receipt, err == nil, err
}

func (s *SQLiteStore) ListRecoverableBrowserRuntimeCheckpoints(ctx context.Context,
	limit int,
) ([]browserruntime.BrowserRuntimeCheckpoint, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite store is not open")
	}
	if limit <= 0 || limit > 256 {
		return nil, errors.New("browser runtime recovery limit must be between 1 and 256")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT checkpoint.payload_json
		FROM browser_runtime_checkpoints checkpoint
		LEFT JOIN browser_runtime_checkpoints later
			ON later.runtime_id = checkpoint.runtime_id
			AND later.generation > checkpoint.generation
		LEFT JOIN browser_runtime_receipts receipt
			ON receipt.runtime_id = checkpoint.runtime_id
		WHERE later.id IS NULL AND (receipt.id IS NULL OR receipt.recovery_required = 1)
		ORDER BY checkpoint.recorded_at, checkpoint.runtime_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	checkpoints := make([]browserruntime.BrowserRuntimeCheckpoint, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		checkpoint, err := decodeBrowserRuntimeCheckpoint(payload)
		if err != nil {
			return nil, err
		}
		checkpoints = append(checkpoints, checkpoint)
	}
	return checkpoints, rows.Err()
}

func browserRuntimeMissionIDTx(ctx context.Context, tx *sql.Tx, runID string) (string, error) {
	var missionID string
	if err := tx.QueryRowContext(ctx, `SELECT mission_id FROM runs WHERE id = ?`, runID).
		Scan(&missionID); err != nil {
		return "", err
	}
	return missionID, nil
}

func loadBrowserRuntimeCheckpointByIDTx(ctx context.Context, tx *sql.Tx, id string) (
	browserruntime.BrowserRuntimeCheckpoint, bool, error,
) {
	return loadBrowserRuntimeCheckpointQueryTx(ctx, tx,
		`SELECT payload_json FROM browser_runtime_checkpoints WHERE id = ?`, id)
}

func loadBrowserRuntimeCheckpointGenerationTx(ctx context.Context, tx *sql.Tx,
	runtimeID string, generation uint64,
) (browserruntime.BrowserRuntimeCheckpoint, bool, error) {
	return loadBrowserRuntimeCheckpointQueryTx(ctx, tx,
		`SELECT payload_json FROM browser_runtime_checkpoints
		WHERE runtime_id = ? AND generation = ?`, runtimeID, generation)
}

func loadBrowserRuntimeCheckpointFingerprintTx(ctx context.Context, tx *sql.Tx,
	fingerprint string,
) (browserruntime.BrowserRuntimeCheckpoint, bool, error) {
	return loadBrowserRuntimeCheckpointQueryTx(ctx, tx,
		`SELECT payload_json FROM browser_runtime_checkpoints WHERE fingerprint = ?`, fingerprint)
}

func loadBrowserRuntimeCheckpointQueryTx(ctx context.Context, tx *sql.Tx,
	query string, args ...any,
) (browserruntime.BrowserRuntimeCheckpoint, bool, error) {
	var payload string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return browserruntime.BrowserRuntimeCheckpoint{}, false, nil
	}
	if err != nil {
		return browserruntime.BrowserRuntimeCheckpoint{}, false, err
	}
	checkpoint, err := decodeBrowserRuntimeCheckpoint(payload)
	return checkpoint, err == nil, err
}

func loadBrowserRuntimeReceiptByIDTx(ctx context.Context, tx *sql.Tx, id string) (
	browserruntime.BrowserRuntimeReceipt, bool, error,
) {
	var payload string
	err := tx.QueryRowContext(ctx, `SELECT payload_json FROM browser_runtime_receipts
		WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return browserruntime.BrowserRuntimeReceipt{}, false, nil
	}
	if err != nil {
		return browserruntime.BrowserRuntimeReceipt{}, false, err
	}
	receipt, err := decodeBrowserRuntimeReceipt(payload)
	return receipt, err == nil, err
}

func decodeBrowserRuntimeCheckpoint(payload string) (
	browserruntime.BrowserRuntimeCheckpoint, error,
) {
	var checkpoint browserruntime.BrowserRuntimeCheckpoint
	if err := json.Unmarshal([]byte(payload), &checkpoint); err != nil {
		return browserruntime.BrowserRuntimeCheckpoint{}, err
	}
	if err := browserruntime.ValidateStoredBrowserRuntimeCheckpoint(checkpoint); err != nil {
		return browserruntime.BrowserRuntimeCheckpoint{}, err
	}
	return checkpoint, nil
}

func decodeBrowserRuntimeReceipt(payload string) (browserruntime.BrowserRuntimeReceipt, error) {
	var receipt browserruntime.BrowserRuntimeReceipt
	if err := json.Unmarshal([]byte(payload), &receipt); err != nil {
		return browserruntime.BrowserRuntimeReceipt{}, err
	}
	if err := browserruntime.ValidateStoredBrowserRuntimeReceipt(receipt); err != nil {
		return browserruntime.BrowserRuntimeReceipt{}, err
	}
	return receipt, nil
}
