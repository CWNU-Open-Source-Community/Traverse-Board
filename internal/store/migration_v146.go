package store

// threadPermissionDeferredEffectStatements extends the immutable Thread
// permission operation ledger with an explicit deferred effect. A deferred
// row binds the exact active running/waiting Run and its current permission
// snapshot, but deliberately does not claim that the new Thread preference was
// applied to that Run.
var threadPermissionDeferredEffectStatements = []string{
	`DROP TRIGGER trg_thread_execution_permission_operation_delete_immutable;`,
	`DROP TRIGGER trg_thread_execution_permission_operation_update_immutable;`,
	`DROP TRIGGER trg_thread_execution_permission_operation_insert;`,
	`ALTER TABLE thread_execution_permission_operations
		RENAME TO thread_execution_permission_operations_v145;`,
	`CREATE TABLE thread_execution_permission_operations (
		operation_key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		snapshot_id TEXT NOT NULL UNIQUE,
		thread_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		current_run_id TEXT,
		current_run_effect TEXT NOT NULL,
		current_run_permission_snapshot_id TEXT,
		created_at TEXT NOT NULL,
		FOREIGN KEY(snapshot_id) REFERENCES thread_execution_permission_snapshots(id)
			ON DELETE RESTRICT,
		FOREIGN KEY(thread_id) REFERENCES threads(id) ON DELETE RESTRICT,
		FOREIGN KEY(current_run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(current_run_permission_snapshot_id)
			REFERENCES run_execution_permission_snapshots(id) ON DELETE RESTRICT,
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0),
		CHECK(
			(current_run_effect = 'no_active_run' AND current_run_id IS NULL
				AND current_run_permission_snapshot_id IS NULL)
			OR (current_run_effect IN ('applied', 'paused_and_applied', 'deferred')
				AND current_run_id IS NOT NULL
				AND current_run_permission_snapshot_id IS NOT NULL)
		)
	) WITHOUT ROWID;`,
	`INSERT INTO thread_execution_permission_operations
		(operation_key_digest, request_fingerprint, snapshot_id, thread_id,
		requested_by, current_run_id, current_run_effect,
		current_run_permission_snapshot_id, created_at)
		SELECT operation_key_digest, request_fingerprint, snapshot_id, thread_id,
			requested_by, current_run_id, current_run_effect,
			current_run_permission_snapshot_id, created_at
		FROM thread_execution_permission_operations_v145;`,
	`DROP TABLE thread_execution_permission_operations_v145;`,
	`CREATE TRIGGER trg_thread_execution_permission_operation_insert
		BEFORE INSERT ON thread_execution_permission_operations
		WHEN NOT EXISTS (
			SELECT 1 FROM thread_execution_permission_snapshots snapshot
			WHERE snapshot.id = NEW.snapshot_id AND snapshot.thread_id = NEW.thread_id
				AND snapshot.requested_by = NEW.requested_by
				AND snapshot.created_at = NEW.created_at AND snapshot.revision > 1
				AND (
					(NEW.current_run_effect = 'no_active_run'
						AND NEW.current_run_id IS NULL
						AND NEW.current_run_permission_snapshot_id IS NULL)
					OR
					(NEW.current_run_effect IN ('applied', 'paused_and_applied')
						AND EXISTS (
							SELECT 1 FROM thread_runs binding
							JOIN run_execution_permission_snapshots run_permission
								ON run_permission.id = NEW.current_run_permission_snapshot_id
							WHERE binding.thread_id = NEW.thread_id
								AND binding.run_id = NEW.current_run_id
								AND run_permission.run_id = NEW.current_run_id
								AND run_permission.mode = snapshot.mode
								AND run_permission.approval_policy = snapshot.approval_policy
								AND run_permission.command_scope = snapshot.command_scope
								AND run_permission.filesystem_scope = snapshot.filesystem_scope
								AND run_permission.network_scope = snapshot.network_scope
								AND run_permission.persistent_terminal = snapshot.persistent_terminal
								AND run_permission.background_process = snapshot.background_process
								AND run_permission.agent_terminal_input = snapshot.agent_terminal_input
								AND run_permission.risk_tier = snapshot.risk_tier
								AND run_permission.required_gate = snapshot.required_gate
								AND run_permission.policy_version = snapshot.policy_version
								AND run_permission.process_enabled = 0
								AND run_permission.execution_authorized = 0
								AND run_permission.capability_grant = 0
						))
					OR
					(NEW.current_run_effect = 'deferred'
						AND EXISTS (
							SELECT 1 FROM threads thread_record
							JOIN thread_runs binding ON binding.thread_id = thread_record.id
							JOIN runs run ON run.id = binding.run_id
							JOIN run_execution_permission_snapshots run_permission
								ON run_permission.id = NEW.current_run_permission_snapshot_id
							WHERE thread_record.id = NEW.thread_id
								AND thread_record.active_run_id = NEW.current_run_id
								AND binding.run_id = NEW.current_run_id
								AND run.status IN ('running', 'waiting_approval')
								AND run_permission.run_id = NEW.current_run_id
								AND run_permission.revision = (
									SELECT MAX(latest.revision)
									FROM run_execution_permission_snapshots latest
									WHERE latest.run_id = NEW.current_run_id)
						))
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Thread execution permission operation binding is invalid');
		END;`,
	`CREATE TRIGGER trg_thread_execution_permission_operation_update_immutable
		BEFORE UPDATE ON thread_execution_permission_operations BEGIN
			SELECT RAISE(ABORT, 'Thread execution permission operation cannot be updated');
		END;`,
	`CREATE TRIGGER trg_thread_execution_permission_operation_delete_immutable
		BEFORE DELETE ON thread_execution_permission_operations BEGIN
			SELECT RAISE(ABORT, 'Thread execution permission operation cannot be deleted');
		END;`,
}
