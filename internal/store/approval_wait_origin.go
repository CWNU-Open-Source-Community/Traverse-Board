package store

import (
	"context"
	"database/sql"
	"errors"
)

// A proposal may precede the final wait by internal tool segments. Follow only
// the same durable operator delivery or exact internal approval handoff; never
// infer ownership merely from adjacent turn numbers or a later generic wait.
func resolveApprovalWaitOriginTx(ctx context.Context, tx *sql.Tx, runID string, call approvalContinuationContext) (approvalContinuationContext, bool, error) {
	var origin approvalContinuationContext
	err := tx.QueryRowContext(ctx, `SELECT json_extract(event.payload_json,'$.turn'),event.subject_id,
	 COALESCE((SELECT MIN(delivery.turn) FROM operator_steering_deliveries delivery JOIN operator_steering_messages message ON message.id=delivery.message_id
	 WHERE delivery.run_id=event.run_id AND message.session_message_id=json_extract(event.payload_json,'$.user_message_id') AND delivery.status IN ('committed','superseded')),
	 (SELECT json_extract(prepared.payload_json,'$.approval_continuation.origin_turn')+1 FROM run_events started
	 JOIN run_execution_handoff_operations operation ON operation.id=json_extract(started.payload_json,'$.approval_continuation_handoff_id') AND operation.run_id=started.run_id
	 JOIN run_events prepared ON prepared.run_id=operation.run_id AND prepared.sequence=operation.event_sequence AND prepared.subject_id=operation.id
	 WHERE started.run_id=event.run_id AND started.subject_id=event.subject_id AND started.type='agent.turn_started' AND started.source='run_supervisor'),?)
	 FROM run_events event WHERE event.run_id=? AND event.type='agent.turn_completed' AND event.source='run_supervisor'
	 AND json_extract(event.payload_json,'$.lifecycle_action')='wait' AND json_extract(event.payload_json,'$.requested_lifecycle_action')='wait'
	 AND json_extract(event.payload_json,'$.turn')>=?
	 AND (event.subject_id=? OR EXISTS (SELECT 1 FROM operator_steering_deliveries original JOIN operator_steering_deliveries final ON final.message_id=original.message_id AND final.run_id=original.run_id
	 WHERE original.run_id=event.run_id AND original.attempt_id=? AND original.turn=? AND original.status='superseded' AND final.attempt_id=event.subject_id AND final.status='committed')
	 OR EXISTS (SELECT 1 FROM run_events original JOIN run_events final ON final.run_id=original.run_id AND final.subject_id=event.subject_id AND final.type='agent.turn_started' AND final.source='run_supervisor'
	 WHERE original.run_id=event.run_id AND original.subject_id=? AND original.type='agent.turn_started' AND original.source='run_supervisor'
	 AND COALESCE(json_extract(original.payload_json,'$.approval_continuation_handoff_id'),'')!=''
	 AND json_extract(original.payload_json,'$.approval_continuation_handoff_id')=json_extract(final.payload_json,'$.approval_continuation_handoff_id')))
	 ORDER BY event.sequence LIMIT 1`, call.OriginTurn, runID, call.OriginTurn, call.OriginAttemptID, call.OriginAttemptID, call.OriginTurn, call.OriginAttemptID).Scan(&origin.OriginTurn, &origin.OriginAttemptID, &origin.BatchStartTurn)
	if errors.Is(err, sql.ErrNoRows) {
		return origin, false, nil
	}
	return origin, err == nil, err
}
