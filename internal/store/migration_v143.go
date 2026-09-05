package store

// immediateExecutionPermissionDowngradeStatements lets a Thread revoke a
// high-risk current Run permission without waiting for a stale execution
// lease or durable execution surface to settle. Escalations and lateral
// high-risk changes still require a created/paused, lease-free Run.
var immediateExecutionPermissionDowngradeStatements = []string{
	`DROP TRIGGER trg_run_execution_permission_snapshot_insert;`,
	`CREATE TRIGGER trg_run_execution_permission_snapshot_insert
		BEFORE INSERT ON run_execution_permission_snapshots
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND julianday(NEW.created_at) >= julianday(run.created_at)
				AND (
					(NEW.revision = 1 AND NEW.mode = 'conservative'
						AND run.status = 'created' AND NOT EXISTS (
							SELECT 1 FROM run_execution_permission_snapshots existing
							WHERE existing.run_id = NEW.run_id
						))
					OR
					(NEW.revision > 1 AND EXISTS (
						SELECT 1 FROM run_execution_permission_snapshots previous
						WHERE previous.run_id = NEW.run_id
							AND previous.revision = NEW.revision - 1
							AND previous.protocol_version = NEW.protocol_version
							AND previous.policy_version = NEW.policy_version
							AND julianday(NEW.created_at) >= julianday(previous.created_at)
							AND (
								(((previous.mode = 'debug' AND NEW.mode <> 'debug')
									OR (previous.mode = 'full_access'
										AND NEW.mode NOT IN ('full_access', 'debug')))
									AND run.status NOT IN ('completed', 'failed', 'cancelled'))
								OR
								(run.status IN ('created', 'paused') AND NOT EXISTS (
									SELECT 1 FROM run_execution_leases lease
									WHERE lease.run_id = NEW.run_id
										AND lease.status = 'active'
										AND julianday(lease.expires_at) > julianday('now')
								))
							)
					))
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Run execution permission binding or transition is invalid');
		END;`,
}
