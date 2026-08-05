package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/analyzer"
	"cyberagent-workbench/internal/events"
)

func (s *SQLiteStore) RegisterAnalyzerStartRequest(ctx context.Context,
	request analyzer.AnalyzerDurableStartRequest,
) (analyzer.AnalyzerDurableStartRequest, bool, error) {
	if s == nil || s.db == nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, errors.New("sqlite store is not open")
	}
	if ctx == nil {
		return analyzer.AnalyzerDurableStartRequest{}, false,
			errors.New("analyzer start request context is required")
	}
	if err := analyzer.ValidateStoredAnalyzerDurableStartRequest(request); err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if existing, found, err := loadAnalyzerStartRequestByIDTx(ctx, tx, request.ID); err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	} else if found {
		if existing.Fingerprint != request.Fingerprint {
			return analyzer.AnalyzerDurableStartRequest{}, false,
				errors.New("analyzer start request ID was reused with another payload")
		}
		return existing, true, nil
	}
	if existing, found, err := loadAnalyzerStartRequestByNonceTx(ctx, tx,
		request.NonceSHA256); err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	} else if found {
		return analyzer.AnalyzerDurableStartRequest{}, false,
			fmt.Errorf("analyzer start nonce was already bound to request %s", existing.ID)
	}
	missionID, boundWorkspaceID, terminal, err := analyzerStartRunBindingTx(ctx, tx,
		request.RunID)
	if err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	}
	if terminal || boundWorkspaceID != request.WorkspaceID {
		return analyzer.AnalyzerDurableStartRequest{}, false,
			errors.New("analyzer start request run/workspace binding is invalid")
	}
	event, err := events.New(request.RunID, missionID,
		events.AnalyzerStartRequestRegisteredEvent, "analyzer_start_control", request.ID,
		map[string]any{
			"request_fingerprint": request.Fingerprint,
			"adapter":             request.Adapter,
			"start_blocked":       true,
			"redacted":            true,
		})
	if err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	}
	event.CreatedAt = request.RegisteredAt
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO analyzer_start_requests
		(id, run_id, mission_id, workspace_id, signed_request_id, nonce_sha256,
		fingerprint, adapter, event_sequence, payload_json, registered_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		request.ID, request.RunID, missionID, request.WorkspaceID,
		request.SignedRequestID, request.NonceSHA256, request.Fingerprint,
		request.Adapter, event.Sequence, string(payload), ts(request.RegisteredAt),
		ts(request.ExpiresAt)); err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false,
			fmt.Errorf("insert analyzer start request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	}
	return request, false, nil
}

func (s *SQLiteStore) PrepareAnalyzerStartIntent(ctx context.Context, requestID string,
	at time.Time,
) (analyzer.AnalyzerStartIntent, error) {
	return s.withAnalyzerStartTx(ctx, func(tx *sql.Tx) (analyzer.AnalyzerStartIntent, error) {
		request, found, err := loadAnalyzerStartRequestByIDTx(ctx, tx, requestID)
		if err != nil {
			return analyzer.AnalyzerStartIntent{}, err
		}
		if !found {
			return analyzer.AnalyzerStartIntent{}, errors.New("analyzer start request not found")
		}
		if existing, found, err := loadLatestAnalyzerStartIntentTx(ctx, tx, requestID); err != nil {
			return analyzer.AnalyzerStartIntent{}, err
		} else if found {
			return existing, nil
		}
		intent, err := analyzer.BuildInitialAnalyzerStartIntent(request, at)
		if err != nil {
			return analyzer.AnalyzerStartIntent{}, err
		}
		if err := insertAnalyzerStartIntentAndReceiptTx(ctx, tx, intent, nil); err != nil {
			return analyzer.AnalyzerStartIntent{}, err
		}
		return intent, nil
	})
}

func (s *SQLiteStore) ConsumeAnalyzerStartIntent(ctx context.Context, requestID,
	expectedFingerprint string, at time.Time,
) (analyzer.AnalyzerStartIntent, error) {
	return s.transitionAnalyzerStartIntent(ctx, requestID, expectedFingerprint,
		analyzer.AnalyzerStartIntentConsumed, at)
}

func (s *SQLiteStore) ExpireAnalyzerStartIntent(ctx context.Context, requestID,
	expectedFingerprint string, at time.Time,
) (analyzer.AnalyzerStartIntent, error) {
	return s.transitionAnalyzerStartIntent(ctx, requestID, expectedFingerprint,
		analyzer.AnalyzerStartIntentExpired, at)
}

func (s *SQLiteStore) CancelAnalyzerStartIntent(ctx context.Context, requestID,
	expectedFingerprint string, at time.Time,
) (analyzer.AnalyzerStartIntent, error) {
	return s.transitionAnalyzerStartIntent(ctx, requestID, expectedFingerprint,
		analyzer.AnalyzerStartIntentCancelled, at)
}

func (s *SQLiteStore) CompleteFakeAnalyzerStartIntent(ctx context.Context, requestID,
	expectedFingerprint string, at time.Time,
) (analyzer.AnalyzerStartIntent, error) {
	return s.transitionAnalyzerStartIntent(ctx, requestID, expectedFingerprint,
		analyzer.AnalyzerStartIntentFakeSucceeded, at)
}

func (s *SQLiteStore) FailFakeAnalyzerStartIntent(ctx context.Context, requestID,
	expectedFingerprint string, at time.Time,
) (analyzer.AnalyzerStartIntent, error) {
	return s.transitionAnalyzerStartIntent(ctx, requestID, expectedFingerprint,
		analyzer.AnalyzerStartIntentFakeFailed, at)
}

func (s *SQLiteStore) transitionAnalyzerStartIntent(ctx context.Context, requestID,
	expectedFingerprint string, target analyzer.AnalyzerStartIntentState, at time.Time,
) (analyzer.AnalyzerStartIntent, error) {
	if strings.TrimSpace(expectedFingerprint) == "" {
		return analyzer.AnalyzerStartIntent{},
			errors.New("expected analyzer start intent fingerprint is required")
	}
	return s.withAnalyzerStartTx(ctx, func(tx *sql.Tx) (analyzer.AnalyzerStartIntent, error) {
		latest, found, err := loadLatestAnalyzerStartIntentTx(ctx, tx, requestID)
		if err != nil {
			return analyzer.AnalyzerStartIntent{}, err
		}
		if !found {
			return analyzer.AnalyzerStartIntent{}, errors.New("analyzer start intent not found")
		}
		if latest.Fingerprint != expectedFingerprint {
			if latest.PreviousIntentFingerprint == expectedFingerprint && latest.State == target {
				return latest, nil
			}
			return analyzer.AnalyzerStartIntent{}, errors.New("stale analyzer start intent generation")
		}
		next, err := analyzer.BuildAnalyzerStartIntentSuccessor(latest, target, at)
		if err != nil {
			return analyzer.AnalyzerStartIntent{}, err
		}
		previousReceipt, found, err := loadAnalyzerStartReceiptGenerationTx(ctx, tx,
			requestID, latest.Generation)
		if err != nil {
			return analyzer.AnalyzerStartIntent{}, err
		}
		if !found {
			return analyzer.AnalyzerStartIntent{}, errors.New("analyzer lifecycle receipt predecessor is missing")
		}
		if err := insertAnalyzerStartIntentAndReceiptTx(ctx, tx, next,
			&previousReceipt); err != nil {
			return analyzer.AnalyzerStartIntent{}, err
		}
		return next, nil
	})
}

func (s *SQLiteStore) LoadAnalyzerStartRequest(ctx context.Context, id string) (
	analyzer.AnalyzerDurableStartRequest, bool, error,
) {
	if s == nil || s.db == nil {
		return analyzer.AnalyzerDurableStartRequest{}, false,
			errors.New("sqlite store is not open")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM analyzer_start_requests
		WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerDurableStartRequest{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	}
	value, err := analyzer.DecodeStoredAnalyzerDurableStartRequest([]byte(payload))
	return value, err == nil, err
}

func (s *SQLiteStore) LoadLatestAnalyzerStartIntent(ctx context.Context, requestID string) (
	analyzer.AnalyzerStartIntent, bool, error,
) {
	if s == nil || s.db == nil {
		return analyzer.AnalyzerStartIntent{}, false, errors.New("sqlite store is not open")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM analyzer_start_intents
		WHERE request_id = ? ORDER BY generation DESC LIMIT 1`, requestID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerStartIntent{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerStartIntent{}, false, err
	}
	value, err := analyzer.DecodeStoredAnalyzerStartIntent([]byte(payload))
	return value, err == nil, err
}

func (s *SQLiteStore) ListAnalyzerStartLifecycleReceipts(ctx context.Context,
	requestID string,
) ([]analyzer.AnalyzerStartLifecycleReceipt, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite store is not open")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json
		FROM analyzer_start_lifecycle_receipts WHERE request_id = ?
		ORDER BY generation`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []analyzer.AnalyzerStartLifecycleReceipt
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		receipt, err := analyzer.DecodeStoredAnalyzerStartLifecycleReceipt([]byte(payload))
		if err != nil {
			return nil, err
		}
		out = append(out, receipt)
	}
	return out, rows.Err()
}

// ReconcileAnalyzerStartIntents closes only metadata state. It never starts,
// kills, observes, or cleans a process and never commits an Artifact.
func (s *SQLiteStore) ReconcileAnalyzerStartIntents(ctx context.Context, now time.Time,
	limit int,
) ([]analyzer.AnalyzerStartIntent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite store is not open")
	}
	if limit <= 0 || limit > 256 {
		return nil, errors.New("analyzer start reconciliation limit must be between 1 and 256")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT intent.payload_json
		FROM analyzer_start_intents intent
		LEFT JOIN analyzer_start_intents later ON later.request_id = intent.request_id
			AND later.generation > intent.generation
		WHERE later.id IS NULL AND (intent.state = 'consumed' OR
			(intent.state = 'prepared' AND julianday(json_extract(intent.payload_json, '$.expires_at'))
				<= julianday(?)))
		ORDER BY intent.transitioned_at, intent.request_id LIMIT ?`, ts(now), limit)
	if err != nil {
		return nil, err
	}
	var pending []analyzer.AnalyzerStartIntent
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			_ = rows.Close()
			return nil, err
		}
		intent, err := analyzer.DecodeStoredAnalyzerStartIntent([]byte(payload))
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		pending = append(pending, intent)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var reconciled []analyzer.AnalyzerStartIntent
	for _, intent := range pending {
		target := analyzer.AnalyzerStartIntentRecoveryRequired
		if intent.State == analyzer.AnalyzerStartIntentPrepared {
			target = analyzer.AnalyzerStartIntentExpired
		}
		next, err := s.transitionAnalyzerStartIntent(ctx, intent.RequestID,
			intent.Fingerprint, target, now)
		if err != nil {
			return reconciled, err
		}
		reconciled = append(reconciled, next)
	}
	return reconciled, nil
}

func (s *SQLiteStore) withAnalyzerStartTx(ctx context.Context,
	fn func(*sql.Tx) (analyzer.AnalyzerStartIntent, error),
) (analyzer.AnalyzerStartIntent, error) {
	if s == nil || s.db == nil {
		return analyzer.AnalyzerStartIntent{}, errors.New("sqlite store is not open")
	}
	if ctx == nil {
		return analyzer.AnalyzerStartIntent{}, errors.New("analyzer start context is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return analyzer.AnalyzerStartIntent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	value, err := fn(tx)
	if err != nil {
		return analyzer.AnalyzerStartIntent{}, err
	}
	if err := tx.Commit(); err != nil {
		return analyzer.AnalyzerStartIntent{}, err
	}
	return value, nil
}

func insertAnalyzerStartIntentAndReceiptTx(ctx context.Context, tx *sql.Tx,
	intent analyzer.AnalyzerStartIntent, previousReceipt *analyzer.AnalyzerStartLifecycleReceipt,
) error {
	if err := analyzer.ValidateStoredAnalyzerStartIntent(intent); err != nil {
		return err
	}
	missionID, workspaceID, terminal, err := analyzerStartRunBindingTx(ctx, tx, intent.RunID)
	if err != nil {
		return err
	}
	if terminal || workspaceID != intent.WorkspaceID {
		return errors.New("analyzer start intent run/workspace binding is invalid")
	}
	event, err := events.New(intent.RunID, missionID,
		events.AnalyzerStartIntentRecordedEvent, "analyzer_start_control", intent.ID,
		map[string]any{
			"intent_fingerprint": intent.Fingerprint, "generation": intent.Generation,
			"state": intent.State, "recovery_required": intent.RecoveryRequired,
			"start_blocked": true, "redacted": true,
		})
	if err != nil {
		return err
	}
	event.CreatedAt = intent.TransitionedAt
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO analyzer_start_intents
		(id, request_id, run_id, workspace_id, generation, state,
		previous_intent_fingerprint, fingerprint, event_sequence, payload_json, transitioned_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, intent.ID, intent.RequestID,
		intent.RunID, intent.WorkspaceID, intent.Generation, intent.State,
		intent.PreviousIntentFingerprint, intent.Fingerprint, event.Sequence,
		string(payload), ts(intent.TransitionedAt)); err != nil {
		return fmt.Errorf("insert analyzer start intent: %w", err)
	}
	receipt, err := analyzer.BuildAnalyzerStartLifecycleReceipt(intent, previousReceipt)
	if err != nil {
		return err
	}
	receiptEvent, err := events.New(intent.RunID, missionID,
		events.AnalyzerStartLifecycleReceiptRecordedEvent, "analyzer_start_control",
		receipt.ID, map[string]any{
			"receipt_fingerprint": receipt.Fingerprint,
			"intent_fingerprint":  receipt.IntentFingerprint,
			"generation":          receipt.Generation,
			"state":               receipt.State,
			"recovery_required":   receipt.RecoveryRequired,
			"redacted":            true,
		})
	if err != nil {
		return err
	}
	receiptEvent.CreatedAt = receipt.RecordedAt
	receiptEvent, err = insertRunEventTx(ctx, tx, receiptEvent)
	if err != nil {
		return err
	}
	receiptPayload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO analyzer_start_lifecycle_receipts
		(id, request_id, run_id, workspace_id, generation, state, intent_fingerprint,
		previous_receipt_fingerprint, fingerprint, event_sequence, payload_json, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, receipt.ID, receipt.RequestID,
		receipt.RunID, receipt.WorkspaceID, receipt.Generation, receipt.State,
		receipt.IntentFingerprint, receipt.PreviousReceiptFingerprint, receipt.Fingerprint,
		receiptEvent.Sequence, string(receiptPayload), ts(receipt.RecordedAt)); err != nil {
		return fmt.Errorf("insert analyzer start lifecycle receipt: %w", err)
	}
	return nil
}

func analyzerStartRunBindingTx(ctx context.Context, tx *sql.Tx, runID string) (
	missionID, workspaceID string, terminal bool, err error,
) {
	var status string
	err = tx.QueryRowContext(ctx, `SELECT run.mission_id, mission.workspace_id, run.status
		FROM runs run JOIN missions mission ON mission.id = run.mission_id
		WHERE run.id = ?`, runID).Scan(&missionID, &workspaceID, &status)
	terminal = status == "completed" || status == "failed" || status == "cancelled"
	return
}

func loadAnalyzerStartRequestByIDTx(ctx context.Context, tx *sql.Tx, id string) (
	analyzer.AnalyzerDurableStartRequest, bool, error,
) {
	return loadAnalyzerStartRequestQueryTx(ctx, tx,
		`SELECT payload_json FROM analyzer_start_requests WHERE id = ?`, id)
}

func loadAnalyzerStartRequestByNonceTx(ctx context.Context, tx *sql.Tx, nonce string) (
	analyzer.AnalyzerDurableStartRequest, bool, error,
) {
	return loadAnalyzerStartRequestQueryTx(ctx, tx,
		`SELECT payload_json FROM analyzer_start_requests WHERE nonce_sha256 = ?`, nonce)
}

func loadAnalyzerStartRequestQueryTx(ctx context.Context, tx *sql.Tx, query string,
	args ...any,
) (analyzer.AnalyzerDurableStartRequest, bool, error) {
	var payload string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerDurableStartRequest{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerDurableStartRequest{}, false, err
	}
	value, err := analyzer.DecodeStoredAnalyzerDurableStartRequest([]byte(payload))
	return value, err == nil, err
}

func loadLatestAnalyzerStartIntentTx(ctx context.Context, tx *sql.Tx, requestID string) (
	analyzer.AnalyzerStartIntent, bool, error,
) {
	var payload string
	err := tx.QueryRowContext(ctx, `SELECT payload_json FROM analyzer_start_intents
		WHERE request_id = ? ORDER BY generation DESC LIMIT 1`, requestID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerStartIntent{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerStartIntent{}, false, err
	}
	value, err := analyzer.DecodeStoredAnalyzerStartIntent([]byte(payload))
	return value, err == nil, err
}

func loadAnalyzerStartReceiptGenerationTx(ctx context.Context, tx *sql.Tx,
	requestID string, generation uint64,
) (analyzer.AnalyzerStartLifecycleReceipt, bool, error) {
	var payload string
	err := tx.QueryRowContext(ctx, `SELECT payload_json
		FROM analyzer_start_lifecycle_receipts WHERE request_id = ? AND generation = ?`,
		requestID, generation).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerStartLifecycleReceipt{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerStartLifecycleReceipt{}, false, err
	}
	value, err := analyzer.DecodeStoredAnalyzerStartLifecycleReceipt([]byte(payload))
	return value, err == nil, err
}
