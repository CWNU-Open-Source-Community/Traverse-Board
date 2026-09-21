package store

import "strings"

// A failed fetch continuation is observed without resuming its paused Run.
// The public handoff entry point still requires Running. This exception binds
// the internal observer to its old prepared input and released execution lease.
func webFetchFailureObservationProof(messageBinding string) string {
	return `EXISTS (
	 SELECT 1 FROM threads thread
	 JOIN run_supervisor_checkpoints checkpoint ON checkpoint.run_id=thread.active_run_id
	 JOIN web_fetch_authorizations authorization ON authorization.run_id=checkpoint.run_id
	   AND authorization.thread_id=thread.id AND authorization.session_id=run.session_id
	   AND authorization.supervisor_turn=checkpoint.next_turn
	 JOIN run_supervisor_tool_calls call ON call.run_id=checkpoint.run_id
	   AND call.attempt_id=checkpoint.attempt_id AND call.turn=checkpoint.next_turn
	   AND call.call_id=authorization.supervisor_tool_call_id AND call.tool_name='web_fetch'
	 JOIN run_execution_leases lease ON lease.run_id=checkpoint.run_id
	   AND lease.lease_id=checkpoint.lease_id AND lease.generation=checkpoint.lease_generation
	 JOIN operator_steering_deliveries delivery ON delivery.run_id=checkpoint.run_id
	   AND delivery.attempt_id=checkpoint.attempt_id AND delivery.turn=checkpoint.next_turn
	 JOIN operator_steering_messages message ON message.id=delivery.message_id
	   AND message.run_id=run.id AND message.session_id=run.session_id
	 JOIN run_execution_handoff_items original_item ON original_item.message_id=message.id
	   AND original_item.message_sequence=message.sequence
	 JOIN run_execution_handoff_operations original ON original.id=original_item.operation_id
	   AND original.run_id=run.id AND original.session_id=run.session_id
	 JOIN run_execution_handoff_results original_result ON original_result.operation_id=original.id
	 WHERE thread.status='active' AND thread.active_run_id=run.id AND thread.last_run_id=run.id
	   AND checkpoint.phase='turn_failed' AND checkpoint.attempt_id<>''
	   AND authorization.status IN ('approved','consumed','denied')
	   AND call.status IN ('completed','failed','denied') AND lease.status='released'
	   AND delivery.status='prepared' AND message.status='pending'
	   AND message.content=checkpoint.pending_input
	   AND original_result.status='completed' AND original_result.run_status='waiting_approval'
	   AND NOT EXISTS (SELECT 1 FROM run_supervisor_tool_calls pending
	     WHERE pending.run_id=run.id AND pending.status='pending')
	   AND EXISTS (SELECT 1 FROM run_events failure WHERE failure.run_id=run.id
	     AND failure.type='agent.turn_failed' AND failure.source='run_supervisor'
	     AND failure.subject_id=checkpoint.attempt_id
	     AND json_extract(failure.payload_json,'$.turn')=checkpoint.next_turn
	     AND json_extract(failure.payload_json,'$.error')=checkpoint.last_error)
	 ` + messageBinding + `)`
}

var webFetchFailureObservationStatements = func() []string {
	operation := requireMigrationTrigger("trg_run_execution_handoff_operation_insert", controlledRunOperationsStatements)
	const before = "AND run.status = 'running' AND session_record.status = 'active'"
	if strings.Count(operation, before) != 1 {
		panic("Run handoff operation state constraint is unavailable")
	}
	operation = strings.Replace(operation, before,
		`AND (run.status='running' OR (run.status='paused'
		 AND NEW.requested_by='web_fetch_authorization' AND NEW.max_steps=1 AND NEW.selected_count=1
		 AND `+webFetchFailureObservationProof("")+`)) AND session_record.status='active'`, 1)
	return []string{
		`DROP TRIGGER trg_run_execution_handoff_operation_insert;`, operation,
		// Operation insertion precedes item insertion. Bind the actual selected
		// item here, rather than accepting an unrelated queued message.
		`CREATE TRIGGER trg_web_fetch_failure_observation_handoff_item_insert
		 BEFORE INSERT ON run_execution_handoff_items
		 WHEN EXISTS (SELECT 1 FROM run_execution_handoff_operations operation
		   JOIN runs run ON run.id=operation.run_id
		   WHERE operation.id=NEW.operation_id AND run.status='paused')
		 AND NOT EXISTS (SELECT 1 FROM run_execution_handoff_operations operation
		   JOIN runs run ON run.id=operation.run_id
		   WHERE operation.id=NEW.operation_id AND operation.requested_by='web_fetch_authorization'
		   AND operation.max_steps=1 AND operation.selected_count=1
		   AND NEW.ordinal=1 AND NEW.prepared=1 AND ` + webFetchFailureObservationProof(
			`AND message.id=NEW.message_id AND message.sequence=NEW.message_sequence`) + `)
		 BEGIN SELECT RAISE(ABORT, 'Historical web fetch observation item binding is invalid'); END;`,
	}
}()
