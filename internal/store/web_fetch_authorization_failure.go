package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
)

// GetWebFetchContinuationModelAttempt proves the model origin of this exact
// resumed tool batch. The returned ordinal is cumulative within the logical
// turn; it does not mean the approval continuation made a new provider request.
func (s *SQLiteStore) GetWebFetchContinuationModelAttempt(ctx context.Context,
	value domain.WebFetchAuthorization, attemptID string,
) (int, error) {
	if !domain.ValidAgentID(value.ID) || !domain.ValidAgentID(attemptID) {
		return 0, apperror.New(apperror.CodeInvalidArgument, "Web fetch model provenance identity is invalid")
	}
	var number int
	err := s.db.QueryRowContext(ctx, `SELECT call.model_attempt
		FROM web_fetch_authorizations authorization
		JOIN run_supervisor_tool_calls call ON call.run_id=authorization.run_id
		AND call.turn=authorization.supervisor_turn AND call.call_id=authorization.supervisor_tool_call_id
		JOIN run_supervisor_tool_rounds round ON round.run_id=call.run_id AND round.turn=call.turn
		AND round.attempt_id=call.attempt_id AND round.round=call.round AND round.model_attempt=call.model_attempt
		JOIN run_events event ON event.run_id=call.run_id AND event.type=? AND event.source='model_gateway'
		AND event.subject_id=call.attempt_id || '/model/' || call.model_attempt
		AND json_extract(event.payload_json,'$.attempt_id')=call.attempt_id
		AND json_extract(event.payload_json,'$.turn')=call.turn
		AND json_extract(event.payload_json,'$.model_attempt')=call.model_attempt
		AND json_extract(event.payload_json,'$.tool_round')=call.round-1
		AND json_extract(event.payload_json,'$.tool_call_count')>0
		WHERE authorization.id=? AND authorization.run_id=? AND authorization.session_id=?
		AND authorization.supervisor_turn=? AND authorization.supervisor_tool_call_id=?
		AND call.attempt_id=? AND call.tool_name='web_fetch' AND call.model_attempt>0`,
		events.ModelCompletedEvent, value.ID, value.RunID, value.SessionID, value.SupervisorTurn,
		value.SupervisorToolCallID, attemptID).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, apperror.New(apperror.CodeFailedPrecondition,
			"Web fetch continuation has no matching completed model attempt")
	}
	return number, err
}

// PrepareWebFetchAuthorizationHandoff uses the ordinary handoff journal but
// selects only the exact input already waiting on this approved fetch. It
// cannot select a later queued message or replace an earlier handoff result.
func (s *SQLiteStore) PrepareWebFetchAuthorizationHandoff(ctx context.Context, authorizationID,
	attemptID string, phase domain.SupervisorPhase,
) (domain.RunExecutionHandoff, bool, error) {
	var empty domain.RunExecutionHandoff
	if !domain.ValidAgentID(attemptID) || (phase != domain.SupervisorTurnStarted && phase != domain.SupervisorTurnFailed) {
		return empty, false, apperror.New(apperror.CodeInvalidArgument, "Web fetch continuation boundary is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, false, err
	}
	defer func() { _ = tx.Rollback() }()
	value, err := getWebFetchAuthorizationTx(ctx, tx, authorizationID)
	if err != nil {
		return empty, false, err
	}
	if err := lockRunControlTx(ctx, tx, value.RunID); err != nil {
		return empty, false, err
	}
	// This is an internal fingerprint domain, not a new public payload. Keep
	// its literal stable so already-recorded approval continuations replay.
	operationKey := "web-fetch-continuation-" + runmutation.Fingerprint(
		"web_fetch_continuation.v1", value.ID, attemptID, string(phase))
	keyDigest := runmutation.RunExecutionHandoffOperationDigest(value.RunID, operationKey)
	if existing, found, err := getRunExecutionHandoffByKey(ctx, tx, keyDigest); err != nil {
		return empty, false, err
	} else if found {
		return existing, true, tx.Commit()
	}
	if err := requireNoActiveRunControlLeaseTx(ctx, tx, value.RunID, time.Now().UTC()); err != nil {
		return empty, false, err
	}
	checkpoint, found, err := getSupervisorCheckpointTx(ctx, tx, value.RunID)
	if err != nil {
		return empty, false, err
	}
	if !found || value.Status == domain.WebFetchAuthorizationPending || checkpoint.NextTurn != value.SupervisorTurn ||
		checkpoint.AttemptID != attemptID || checkpoint.Phase != phase {
		return empty, false, nil
	}
	if phase == domain.SupervisorTurnFailed {
		lease, found, err := getRunExecutionLeaseTx(ctx, tx, value.RunID)
		if err != nil {
			return empty, false, err
		}
		if !found || lease.Status != domain.RunExecutionLeaseReleased || lease.LeaseID != checkpoint.LeaseID ||
			lease.Generation != checkpoint.LeaseGeneration {
			return empty, false, apperror.New(apperror.CodeFailedPrecondition, "Historical web fetch failure requires its exact released lease")
		}
		if err := requireThreadToolEffectsSettledTx(ctx, tx, value.RunID); err != nil {
			return empty, false, err
		}
	}
	run, err := getRunControlRunTx(ctx, tx, value.RunID)
	if err != nil {
		return empty, false, err
	}
	if run.Status != domain.RunRunning || run.SessionID != value.SessionID {
		return empty, false, nil
	}
	var messageID string
	err = tx.QueryRowContext(ctx, `SELECT delivery.message_id
		FROM operator_steering_deliveries delivery
		JOIN operator_steering_messages message ON message.id=delivery.message_id
		JOIN threads thread ON thread.active_run_id=message.run_id
		WHERE delivery.run_id=? AND delivery.attempt_id=? AND delivery.status='prepared'
		AND message.status='pending' AND message.session_id=? AND thread.id=?
		AND EXISTS (SELECT 1 FROM run_execution_handoff_items item WHERE item.message_id=message.id)`,
		run.ID, checkpoint.AttemptID, run.SessionID, value.ThreadID).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		// Non-Thread Supervisor turns have no operator delivery to seal.
		return empty, false, nil
	}
	if err != nil {
		return empty, false, err
	}
	message, err := getOperatorSteeringMessageTx(ctx, tx, messageID)
	if err != nil {
		return empty, false, err
	}
	if message.Content != checkpoint.PendingInput {
		return empty, false, apperror.New(apperror.CodeConflict, "Web fetch continuation no longer matches its prepared input")
	}
	var payload string
	if err := tx.QueryRowContext(ctx, `SELECT payload_json FROM run_supervisor_tool_calls
		WHERE run_id=? AND attempt_id=? AND turn=? AND call_id=? AND tool_name='web_fetch'`,
		run.ID, checkpoint.AttemptID, checkpoint.NextTurn, value.SupervisorToolCallID).Scan(&payload); err != nil {
		return empty, false, err
	}
	if err := validateWebFetchAuthorizationPayloadBinding(ctx, tx, value, payload); err != nil {
		return empty, false, err
	}
	operation := domain.RunExecutionHandoffOperation{ID: idgen.New("run-handoff"),
		ProtocolVersion: domain.RunExecutionHandoffProtocolVersion, KeyDigest: keyDigest,
		RequestFingerprint: runmutation.RunExecutionHandoffRequestFingerprint(run.ID, "web_fetch_authorization", 1),
		RunID:              run.ID, SessionID: run.SessionID, RequestedBy: "web_fetch_authorization", MaxSteps: 1,
		CreatedAt: time.Now().UTC()}
	handoff, err := insertRunExecutionHandoffTx(ctx, tx, run, operation, []domain.RunExecutionHandoffItem{{
		OperationID: operation.ID, Ordinal: 1, MessageID: message.ID, MessageSequence: message.Sequence, Prepared: true,
	}})
	if err != nil {
		return empty, false, err
	}
	return handoff, true, tx.Commit()
}

// Older approval continuations could fail after their initial handoff had
// completed at waiting_approval. Identify that exact durable failure before a
// new Thread input supersedes its attempt and loses model-visible tool history.
func (s *SQLiteStore) GetUnsealedWebFetchThreadFailure(ctx context.Context, threadID string) (
	domain.WebFetchAuthorization, domain.SupervisorCheckpoint, bool, error,
) {
	var empty domain.WebFetchAuthorization
	var checkpoint domain.SupervisorCheckpoint
	var authorizationID, runID string
	err := s.db.QueryRowContext(ctx, `SELECT authorization.id,checkpoint.run_id
		FROM threads thread JOIN run_supervisor_checkpoints checkpoint ON checkpoint.run_id=thread.active_run_id
		JOIN web_fetch_authorizations authorization ON authorization.run_id=checkpoint.run_id AND authorization.thread_id=thread.id
		JOIN run_supervisor_tool_calls call ON call.run_id=checkpoint.run_id AND call.attempt_id=checkpoint.attempt_id
		AND call.call_id=authorization.supervisor_tool_call_id AND call.turn=authorization.supervisor_turn
		WHERE thread.id=? AND checkpoint.phase='turn_failed' AND checkpoint.next_turn=authorization.supervisor_turn
		AND authorization.status IN ('approved','consumed','denied') AND call.tool_name='web_fetch'
		AND EXISTS (SELECT 1 FROM run_events event WHERE event.run_id=checkpoint.run_id
		AND event.type=? AND event.source='run_supervisor' AND event.subject_id=checkpoint.attempt_id
		AND json_extract(event.payload_json,'$.turn')=checkpoint.next_turn
		AND json_extract(event.payload_json,'$.error')=checkpoint.last_error)
		ORDER BY authorization.decided_at DESC,authorization.id DESC LIMIT 1`, threadID, events.AgentTurnFailedEvent).
		Scan(&authorizationID, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, checkpoint, false, nil
	}
	if err != nil {
		return empty, checkpoint, false, err
	}
	value, err := s.GetWebFetchAuthorization(ctx, authorizationID)
	if err != nil {
		return empty, checkpoint, false, err
	}
	checkpoint, found, err := s.GetSupervisorCheckpoint(ctx, runID)
	return value, checkpoint, found, err
}
