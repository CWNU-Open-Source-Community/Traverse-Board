package store

// An approval continuation references its already committed user input without
// reopening its delivery or creating an additional operator message.
var approvalContinuationStatements = []string{
	`DROP TRIGGER trg_run_execution_handoff_item_insert;`,
	`CREATE TRIGGER trg_run_execution_handoff_item_insert
	BEFORE INSERT ON run_execution_handoff_items
	WHEN NOT EXISTS (
	 SELECT 1 FROM run_execution_handoff_operations operation
	 JOIN operator_steering_messages message ON message.id=NEW.message_id
	 WHERE operation.id=NEW.operation_id AND NEW.ordinal<=operation.selected_count
	 AND message.run_id=operation.run_id AND message.session_id=operation.session_id
	 AND message.sequence=NEW.message_sequence
	 AND ((message.status='pending' AND NEW.prepared=EXISTS (
	   SELECT 1 FROM operator_steering_deliveries delivery WHERE delivery.message_id=message.id AND delivery.status='prepared'))
	 OR (message.status='committed' AND NEW.prepared=0 AND NEW.ordinal=1 AND operation.selected_count=1
	   AND operation.requested_by='approval_continuation' AND operation.max_steps=1
	   AND EXISTS (SELECT 1 FROM run_events prepared
	     JOIN run_events origin ON origin.run_id=prepared.run_id
	       AND origin.subject_id=json_extract(prepared.payload_json,'$.approval_continuation.origin_attempt_id')
	       AND origin.type='agent.turn_completed' AND origin.source='run_supervisor'
	     JOIN threads thread ON thread.id=json_extract(prepared.payload_json,'$.approval_continuation.thread_id')
	     JOIN run_supervisor_checkpoints checkpoint ON checkpoint.run_id=operation.run_id
	     WHERE prepared.run_id=operation.run_id AND prepared.sequence=operation.event_sequence AND prepared.subject_id=operation.id
	     AND prepared.type='run.execution_handoff_requested' AND prepared.source='run_execution_handoff'
	     AND json_extract(prepared.payload_json,'$.approval_continuation.user_message_id')=message.session_message_id
	     AND json_extract(origin.payload_json,'$.user_message_id')=message.session_message_id
	     AND json_extract(origin.payload_json,'$.turn')=json_extract(prepared.payload_json,'$.approval_continuation.origin_turn')
	     AND json_extract(origin.payload_json,'$.lifecycle_action')='wait' AND json_extract(origin.payload_json,'$.requested_lifecycle_action')='wait'
	     AND origin.sequence<prepared.sequence AND thread.status='active' AND thread.active_run_id=operation.run_id AND thread.last_run_id=operation.run_id
	     AND checkpoint.phase='waiting' AND checkpoint.next_turn=json_extract(origin.payload_json,'$.turn')+1
	     AND NOT EXISTS (SELECT 1 FROM operator_steering_messages newer WHERE newer.run_id=message.run_id AND newer.sequence>message.sequence))))
	)
	BEGIN SELECT RAISE(ABORT, 'Run execution handoff item binding is invalid'); END;`,
}
