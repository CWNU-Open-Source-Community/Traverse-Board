package store

// browserCDPImmediateDowngradeStatements keeps the immutable browser CDP
// ledger compatible with fail-closed runtime fencing. Raising the ceiling
// still requires a quiescent created/paused Run. Lowering it is allowed for
// every nonterminal Run even while a now-stale lease or execution surface is
// being fenced by the application layer.
var browserCDPImmediateDowngradeStatements = []string{
	`DROP TRIGGER trg_run_browser_cdp_permission_snapshot_insert;`,
	`CREATE TRIGGER trg_run_browser_cdp_permission_snapshot_insert
		BEFORE INSERT ON run_browser_cdp_permission_snapshots
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND julianday(NEW.created_at) >= julianday(run.created_at)
				AND (
					(NEW.revision = 1 AND NEW.mode = 'restricted'
						AND run.status = 'created' AND NOT EXISTS (
							SELECT 1 FROM run_browser_cdp_permission_snapshots existing
							WHERE existing.run_id = NEW.run_id
						))
					OR
					(NEW.revision > 1 AND EXISTS (
							SELECT 1 FROM run_browser_cdp_permission_snapshots previous
							WHERE previous.run_id = NEW.run_id
								AND previous.revision = NEW.revision - 1
								AND previous.protocol_version = NEW.protocol_version
								AND previous.policy_version = NEW.policy_version
								AND julianday(NEW.created_at) >= julianday(previous.created_at)
								AND (
									(NEW.mode = 'restricted'
										AND previous.mode = 'full_debug'
										AND run.status NOT IN ('completed', 'failed', 'cancelled'))
									OR
									(NEW.mode = 'full_debug'
										AND previous.mode = 'restricted'
										AND run.status IN ('created', 'paused')
										AND NOT EXISTS (
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
			SELECT RAISE(ABORT, 'Run browser CDP permission binding or transition is invalid');
		END;`,
}
