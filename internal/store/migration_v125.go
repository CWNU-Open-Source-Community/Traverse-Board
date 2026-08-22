package store

var legacyDockerLifecycleCleanupTriggerCompatibilityStatements = []string{
	`DROP TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_insert;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_insert
		BEFORE INSERT ON sandbox_docker_lifecycle_cleanup_receipts
		WHEN NOT EXISTS (
			SELECT 1
			FROM sandbox_docker_lifecycle_intents intent
			JOIN sandbox_docker_lifecycle_leases lease ON lease.intent_id = intent.id
			JOIN sandbox_docker_lifecycle_transitions final
				ON final.intent_id = intent.id
				AND final.transition_fingerprint = NEW.final_transition_fingerprint
			WHERE intent.id = NEW.intent_id
				AND intent.resource_generation = NEW.resource_generation
				AND lease.resource_generation = NEW.resource_generation
				AND lease.lease_id = NEW.lease_id AND lease.owner_id = NEW.owner_id
				AND lease.generation = NEW.lease_generation AND lease.status = 'active'
				AND julianday(lease.expires_at) > julianday('now')
				AND final.resource_generation = NEW.resource_generation
				AND final.state = 'cleaned'
				AND final.ordinal = (SELECT MAX(transition.ordinal)
					FROM sandbox_docker_lifecycle_transitions transition
					WHERE transition.intent_id = NEW.intent_id)
				AND EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_transitions cleaning
					WHERE cleaning.intent_id = NEW.intent_id AND cleaning.state = 'cleaning'
						AND ((NEW.outcome = 'natural_exit' AND cleaning.reason_code = 'natural_exit')
							OR (NEW.outcome = 'timed_out' AND cleaning.reason_code = 'timeout')
							OR (NEW.outcome = 'cancelled' AND cleaning.reason_code = 'cancelled')
							OR (NEW.outcome = 'failed' AND cleaning.reason_code IN
								('restart_recovery', 'cleanup_started'))))
				AND (NEW.container_already_absent = 1 OR EXISTS (
					SELECT 1 FROM sandbox_docker_lifecycle_actions action
					WHERE action.intent_id = NEW.intent_id
						AND action.resource_generation = NEW.resource_generation
						AND action.lease_id = NEW.lease_id
						AND action.owner_id = NEW.owner_id
						AND action.lease_generation = NEW.lease_generation
						AND action.verb = 'delete'))
				AND (NEW.container_id_fingerprint IS NULL OR NOT EXISTS (
					SELECT 1 FROM sandbox_docker_lifecycle_transitions transition
					WHERE transition.intent_id = NEW.intent_id
						AND transition.container_id_fingerprint IS NOT NULL
						AND transition.container_id_fingerprint <> NEW.container_id_fingerprint))
				AND julianday(NEW.completed_at) >= julianday(lease.renewed_at)
				AND julianday(NEW.completed_at) >= julianday(final.recorded_at)
				AND julianday(NEW.completed_at) < julianday(lease.expires_at)
				AND ((NEW.exit_code IS NULL AND NOT EXISTS (
						SELECT 1 FROM sandbox_docker_lifecycle_transitions exited
						WHERE exited.intent_id = NEW.intent_id AND exited.state = 'exited'))
					OR EXISTS (
						SELECT 1 FROM sandbox_docker_lifecycle_transitions exited
						WHERE exited.intent_id = NEW.intent_id AND exited.state = 'exited'
							AND exited.exit_code = NEW.exit_code))
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle cleanup receipt fence or binding is invalid');
		END;`,
}
