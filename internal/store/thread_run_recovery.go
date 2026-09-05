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

// GetThreadRunRecovery returns a recovery candidate only when the latest
// execution handoff of the exact active running Run failed durably.
func (s *SQLiteStore) GetThreadRunRecovery(ctx context.Context,
	threadID string,
) (domain.ThreadRunRecovery, bool, error) {
	threadID = strings.TrimSpace(threadID)
	if !domain.ValidAgentID(threadID) {
		return domain.ThreadRunRecovery{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Thread recovery Thread id is invalid")
	}
	threadRecord, err := s.GetThread(ctx, threadID)
	if err != nil {
		return domain.ThreadRunRecovery{}, false, err
	}
	if threadRecord.ActiveRunID == "" {
		return domain.ThreadRunRecovery{}, false, nil
	}
	run, err := s.GetRun(ctx, threadRecord.ActiveRunID)
	if err != nil {
		return domain.ThreadRunRecovery{}, false, err
	}
	if run.Status != domain.RunRunning {
		return domain.ThreadRunRecovery{}, false, nil
	}
	var operationID string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM run_execution_handoff_operations
		WHERE run_id = ? ORDER BY event_sequence DESC LIMIT 1`, run.ID).Scan(&operationID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ThreadRunRecovery{}, false, nil
	}
	if err != nil {
		return domain.ThreadRunRecovery{}, false, err
	}
	handoff, found, err := getRunExecutionHandoffByID(ctx, s.db, operationID)
	if err != nil || !found {
		return domain.ThreadRunRecovery{}, false, err
	}
	if handoff.Result == nil ||
		handoff.Result.Status != domain.RunExecutionHandoffFailed {
		return domain.ThreadRunRecovery{}, false, nil
	}
	checkpoint, checkpointFound, err := s.GetSupervisorCheckpoint(ctx, run.ID)
	if err != nil {
		return domain.ThreadRunRecovery{}, false, err
	}
	disposition := threadRunFailureDisposition(run, handoff, checkpoint,
		checkpointFound)
	quiescent := true
	if lease, leaseFound, leaseErr := s.GetRunExecutionLease(ctx, run.ID); leaseErr != nil {
		return domain.ThreadRunRecovery{}, false, leaseErr
	} else if leaseFound && lease.ActiveAt(time.Now().UTC()) {
		quiescent = false
	}
	recovery := domain.ThreadRunRecovery{
		ProtocolVersion: domain.ThreadRunRecoveryProtocolVersion,
		ThreadID:        threadRecord.ID, RunID: run.ID, HandoffOperationID: operationID,
		Disposition: disposition,
		ErrorCode:   handoff.Result.ErrorCode, StopReason: handoff.Result.StopReason,
		Detail: "", Quiescent: quiescent, FailedAt: handoff.Result.CompletedAt,
	}
	if err := recovery.Validate(); err != nil {
		return domain.ThreadRunRecovery{}, false, err
	}
	return recovery, true, nil
}

// RecoverThreadRunFromFailedHandoff atomically proves that the supplied
// handoff is still the latest failed durable boundary, fences active execution,
// fails the old Run, and cancels its pending steering. The Thread trigger then
// clears active_run_id so the next submission creates a successor Run.
func (s *SQLiteStore) RecoverThreadRunFromFailedHandoff(ctx context.Context,
	threadID, runID, operationID, requestedBy, operationKey string,
) (domain.Thread, domain.Run, bool, error) {
	return s.recoverThreadRunFromFailedHandoff(ctx, threadID, runID, operationID,
		requestedBy, operationKey, false)
}

// ContinueThreadRunFromFailedHandoff is the product-facing continuation fence.
// A new, explicit user turn is permission to abandon the previous failed turn,
// including a transient failure that could otherwise be retried in place. It
// never copies or replays the old pending message; the caller submits the new
// message only after this transaction has made the old Run terminal.
func (s *SQLiteStore) ContinueThreadRunFromFailedHandoff(ctx context.Context,
	threadID, runID, operationID, requestedBy, operationKey string,
) (domain.Thread, domain.Run, bool, error) {
	return s.recoverThreadRunFromFailedHandoff(ctx, threadID, runID, operationID,
		requestedBy, operationKey, true)
}

func (s *SQLiteStore) recoverThreadRunFromFailedHandoff(ctx context.Context,
	threadID, runID, operationID, requestedBy, operationKey string,
	allowRetryableAbandonment bool,
) (domain.Thread, domain.Run, bool, error) {
	threadID, runID, operationID = strings.TrimSpace(threadID), strings.TrimSpace(runID),
		strings.TrimSpace(operationID)
	requestedBy = strings.TrimSpace(requestedBy)
	operationKey, keyErr := domain.NormalizeAgentOperationKey(operationKey)
	if !domain.ValidAgentID(threadID) || !domain.ValidAgentID(runID) ||
		!domain.ValidAgentID(operationID) || !domain.ValidAgentID(requestedBy) || keyErr != nil {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Thread recovery identity is invalid")
	}
	keyDigest := runmutation.Fingerprint("thread_run_recovery_operation.v1", operationKey)
	requestFingerprint := runmutation.Fingerprint("thread_run_recovery_request.v1",
		threadID, runID, operationID, requestedBy)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Acquire SQLite's writer lock before observing the recovery predicate.
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
	var allRecoveryEvents, keyedRecoveryEvents int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN json_extract(payload_json, '$.operation_key_digest') = ?
			THEN 1 ELSE 0 END), 0)
		FROM run_events WHERE run_id = ? AND type = 'run.status_changed'
			AND source = 'thread_recovery'`, keyDigest, run.ID).
		Scan(&allRecoveryEvents, &keyedRecoveryEvents); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if allRecoveryEvents > 1 || keyedRecoveryEvents > 1 {
		return domain.Thread{}, domain.Run{}, false, errors.New(
			"Thread recovery durable event is duplicated")
	}
	if keyedRecoveryEvents == 1 {
		var storedFingerprint string
		if err := tx.QueryRowContext(ctx, `SELECT
			json_extract(payload_json, '$.request_fingerprint') FROM run_events
			WHERE run_id = ? AND type = 'run.status_changed' AND source = 'thread_recovery'
				AND json_extract(payload_json, '$.operation_key_digest') = ?`,
			run.ID, keyDigest).Scan(&storedFingerprint); err != nil {
			return domain.Thread{}, domain.Run{}, false, err
		}
		if storedFingerprint != requestFingerprint {
			return domain.Thread{}, domain.Run{}, false, apperror.New(
				apperror.CodeConflict,
				"Thread recovery operation key was already used for different intent")
		}
		if run.Status != domain.RunFailed {
			return domain.Thread{}, domain.Run{}, false, errors.New(
				"Thread recovery replay is not terminal")
		}
		if err := tx.Commit(); err != nil {
			return domain.Thread{}, domain.Run{}, false, err
		}
		return threadRecord, run, true, nil
	}
	if allRecoveryEvents != 0 {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeConflict, "Thread Run was already recovered by another operation")
	}
	if threadRecord.Status != domain.ThreadActive || threadRecord.ActiveRunID != run.ID ||
		threadRecord.LastRunID != run.ID || run.Status != domain.RunRunning {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeConflict, "Thread recovery target is no longer the active running Run")
	}
	var latestOperationID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM run_execution_handoff_operations
		WHERE run_id = ? ORDER BY event_sequence DESC LIMIT 1`, run.ID).
		Scan(&latestOperationID); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if latestOperationID != operationID {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeConflict, "Thread recovery handoff is no longer the latest boundary")
	}
	handoff, found, err := getRunExecutionHandoffByID(ctx, tx, operationID)
	if err != nil || !found {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if handoff.Operation.RunID != run.ID || handoff.Result == nil ||
		handoff.Result.Status != domain.RunExecutionHandoffFailed {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeFailedPrecondition,
			"Thread recovery requires the latest failed durable handoff")
	}
	checkpoint, checkpointFound, err := getSupervisorCheckpointTx(ctx, tx, run.ID)
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	disposition := threadRunFailureDisposition(run, handoff, checkpoint,
		checkpointFound)
	if !allowRetryableAbandonment && !disposition.AllowsRunRecovery() {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeFailedPrecondition,
			"The failed Thread turn remains retryable on the current Run")
	}
	now := time.Now().UTC()
	if lease, leaseFound, leaseErr := getRunExecutionLeaseTx(ctx, tx, run.ID); leaseErr != nil {
		return domain.Thread{}, domain.Run{}, false, leaseErr
	} else if leaseFound && lease.ActiveAt(now) {
		return domain.Thread{}, domain.Run{}, false, apperror.New(
			apperror.CodeFailedPrecondition,
			"The old Run is still executing; wait for its current lease to finish and retry recovery")
	}
	expected := run.Status
	if err := run.Transition(domain.RunFailed, now); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	reason := "operator ended old Run after failed durable handoff"
	continuationSource := "operator_recovery"
	if allowRetryableAbandonment {
		reason = "explicit next Thread turn advanced past failed durable handoff"
		continuationSource = "explicit_next_turn"
	}
	event, err := events.New(run.ID, run.MissionID, events.RunStatusChangedEvent,
		"thread_recovery", operationID, map[string]any{
			"from": expected, "to": domain.RunFailed,
			"reason":                  reason,
			"continuation_source":     continuationSource,
			"handoff_operation_id":    operationID,
			"error_code":              handoff.Result.ErrorCode,
			"stop_reason":             handoff.Result.StopReason,
			"failure_disposition":     disposition,
			"pending_steering_copied": false,
			"requested_by":            requestedBy,
			"operation_key_digest":    keyDigest,
			"request_fingerprint":     requestFingerprint,
		})
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if err := transitionRunTx(ctx, tx, run, expected, event, "thread_recovery"); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	threadRecord, err = scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id = ?`,
		threadID))
	if err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	if threadRecord.ActiveRunID != "" || threadRecord.LastRunID != run.ID {
		return domain.Thread{}, domain.Run{}, false, errors.New(
			"Thread recovery did not release the failed active Run")
	}
	if err := tx.Commit(); err != nil {
		return domain.Thread{}, domain.Run{}, false, err
	}
	return threadRecord, run, false, nil
}

// threadRunFailureDisposition deliberately classifies transient handoff
// failures away from terminal Run recovery. Provider retry exhaustion is a
// turn boundary, not proof that the Run's authority/configuration epoch is
// unusable. Resource/deadline failures require a successor only when durable
// Run budget facts prove that retrying the same Run cannot make progress.
func threadRunFailureDisposition(run domain.Run, handoff domain.RunExecutionHandoff,
	checkpoint domain.SupervisorCheckpoint, checkpointFound bool,
) domain.ThreadRunFailureDisposition {
	if handoff.Result == nil ||
		handoff.Result.Status != domain.RunExecutionHandoffFailed {
		return domain.ThreadRunFailureRecoveryRequired
	}
	budgetExhausted := false
	if checkpointFound {
		budgetExhausted = checkpoint.NextTurn > run.Budget.MaxTurns ||
			(run.Budget.MaxTokens > 0 && checkpoint.TotalTokens >= run.Budget.MaxTokens) ||
			(run.Budget.TimeoutSeconds > 0 &&
				checkpoint.ExecutionMillis >= run.Budget.TimeoutSeconds*1000)
	}
	switch strings.ToLower(strings.TrimSpace(handoff.Result.ErrorCode)) {
	case strings.ToLower(string(apperror.CodeUnavailable)),
		strings.ToLower(string(apperror.CodeCancelled)),
		strings.ToLower(string(apperror.CodeConflict)):
		return domain.ThreadRunFailureRetrySameTurn
	case strings.ToLower(string(apperror.CodeResourceExhausted)),
		strings.ToLower(string(apperror.CodeDeadlineExceeded)):
		if budgetExhausted {
			return domain.ThreadRunFailureRequiresSuccessor
		}
		return domain.ThreadRunFailureRetrySameTurn
	case strings.ToLower(string(apperror.CodeFailedPrecondition)),
		strings.ToLower(string(apperror.CodeInvalidArgument)),
		strings.ToLower(string(apperror.CodeNotFound)),
		strings.ToLower(string(apperror.CodePolicyDenied)):
		return domain.ThreadRunFailureRequiresSuccessor
	default:
		// Unknown/internal/data-loss failures require explicit recovery rather
		// than silently retrying a potentially inconsistent durable boundary.
		return domain.ThreadRunFailureRecoveryRequired
	}
}
