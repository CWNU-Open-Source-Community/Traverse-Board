package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runmutation"
)

// AdvanceThreadRunForPendingConfiguration atomically supersedes a quiescent
// active Run before an explicit new Thread turn. The next Submit creates the
// successor and materializes the latest Thread model/permission preferences.
func (s *SQLiteStore) AdvanceThreadRunForPendingConfiguration(ctx context.Context,
	threadID, runID, requestedBy, operationKey string,
) (domain.Thread, domain.Run, bool, error) {
	threadID, runID, requestedBy = strings.TrimSpace(threadID), strings.TrimSpace(runID),
		strings.TrimSpace(requestedBy)
	operationKey, keyErr := domain.NormalizeAgentOperationKey(operationKey)
	if !domain.ValidAgentID(threadID) || !domain.ValidAgentID(runID) ||
		!domain.ValidAgentID(requestedBy) || keyErr != nil {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Thread epoch transition identity is invalid")
	}
	keyDigest := runmutation.Fingerprint("thread_epoch_transition_operation.v1", operationKey)
	requestFingerprint := runmutation.Fingerprint("thread_epoch_transition_request.v1",
		threadID, runID, requestedBy)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET updated_at = updated_at WHERE id = ?`,
		runID); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	threadRecord, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id = ?`,
		threadID))
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id, session_id, status,
		config_json, budget_json, started_at, finished_at, created_at, updated_at
		FROM runs WHERE id = ?`, runID))
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	var allEvents, keyedEvents int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN json_extract(payload_json, '$.operation_key_digest') = ?
			THEN 1 ELSE 0 END), 0)
		FROM run_events WHERE run_id = ? AND type = 'run.status_changed'
			AND source = 'thread_epoch_transition'`, keyDigest, run.ID).
		Scan(&allEvents, &keyedEvents); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if allEvents > 1 || keyedEvents > 1 {
		return domain.Thread{}, domain.Run{}, false, errors.New(
			"Thread epoch transition durable event is duplicated")
	}
	if keyedEvents == 1 {
		var storedFingerprint string
		if err := tx.QueryRowContext(ctx, `SELECT
			json_extract(payload_json, '$.request_fingerprint') FROM run_events
			WHERE run_id = ? AND type = 'run.status_changed'
				AND source = 'thread_epoch_transition'
				AND json_extract(payload_json, '$.operation_key_digest') = ?`,
			run.ID, keyDigest).Scan(&storedFingerprint); err != nil {
			return domain.Thread{}, domain.Run{}, false, err
		}
		if storedFingerprint != requestFingerprint {
			return domain.Thread{}, domain.Run{}, false, apperror.New(
				apperror.CodeConflict,
				"Thread epoch transition key was already used for different intent")
		}
		if run.Status != domain.RunCancelled {
			return domain.Thread{}, domain.Run{}, false, errors.New(
				"Thread epoch transition replay is not terminal")
		}
		if err := tx.Commit(); err != nil {
			return domain.Thread{}, domain.Run{}, false, err
		}
		return threadRecord, run, true, nil
	}
	if allEvents != 0 {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeConflict, "Thread Run was already superseded")
	}
	if threadRecord.Status != domain.ThreadActive || threadRecord.ActiveRunID != run.ID ||
		threadRecord.LastRunID != run.ID || run.Terminal() {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeConflict, "Thread epoch transition target is no longer active")
	}
	now := time.Now().UTC()
	if err := requireThreadLifecycleRunQuiescentTx(ctx, tx, run.ID, now); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	expected := run.Status
	if err := run.Transition(domain.RunCancelled, now); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	event, err := events.New(run.ID, run.MissionID, events.RunStatusChangedEvent,
		"thread_epoch_transition", run.ID, map[string]any{
			"from": expected, "to": domain.RunCancelled,
			"reason":       "explicit next Thread turn applies pending configuration",
			"requested_by": requestedBy, "operation_key_digest": keyDigest,
			"request_fingerprint": requestFingerprint,
		})
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if err := transitionRunTx(ctx, tx, run, expected, event,
		"thread_epoch_transition"); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	threadRecord, err = scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id = ?`,
		threadID))
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if threadRecord.ActiveRunID != "" || threadRecord.LastRunID != run.ID {
		return domain.Thread{}, domain.Run{}, false, errors.New(
			"Thread epoch transition did not release the superseded Run")
	}
	if err := tx.Commit(); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	return threadRecord, run, false, nil
}
