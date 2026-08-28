package store

// threadExecutionPermissionStatements add a non-authorizing, immutable Thread
// preference. Existing Threads are backfilled conservatively. A selection is
// atomically synchronized to the current Run at a safe boundary and is also
// materialized into future successor Runs; it never restores any lease,
// approval, process, credential, adapter, or other runtime authority.
var threadExecutionPermissionStatements = []string{
	`CREATE TABLE thread_execution_permission_snapshots (
		id TEXT PRIMARY KEY,
		thread_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		revision INTEGER NOT NULL,
		protocol_version TEXT NOT NULL,
		mode TEXT NOT NULL,
		approval_policy TEXT NOT NULL,
		command_scope TEXT NOT NULL,
		filesystem_scope TEXT NOT NULL,
		network_scope TEXT NOT NULL,
		persistent_terminal INTEGER NOT NULL,
		background_process INTEGER NOT NULL,
		agent_terminal_input INTEGER NOT NULL,
		risk_tier TEXT NOT NULL,
		required_gate TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		operator_confirmed INTEGER NOT NULL,
		process_enabled INTEGER NOT NULL,
		execution_authorized INTEGER NOT NULL,
		capability_grant INTEGER NOT NULL,
		requested_by TEXT NOT NULL,
		reason TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(thread_id) REFERENCES threads(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		UNIQUE(thread_id, revision),
		CHECK(revision > 0),
		CHECK(protocol_version = 'thread_execution_permission.v1'),
		CHECK(policy_version = 'execution_permission_policy.v1'),
		CHECK(process_enabled = 0 AND execution_authorized = 0 AND capability_grant = 0),
		CHECK(
			(mode = 'conservative' AND approval_policy = 'fixed_templates'
				AND command_scope = 'fixed_templates'
				AND filesystem_scope = 'workspace_guarded' AND network_scope = 'disabled'
				AND persistent_terminal = 0 AND background_process = 0
				AND agent_terminal_input = 0 AND risk_tier = 'minimal'
				AND required_gate = 'conservative_control' AND operator_confirmed = 0)
			OR (mode = 'workspace_access' AND approval_policy = 'out_of_scope_exact_once'
				AND command_scope = 'sandboxed_workspace'
				AND filesystem_scope = 'workspace_guarded' AND network_scope = 'disabled'
				AND persistent_terminal = 0 AND background_process = 0
				AND agent_terminal_input = 0 AND risk_tier = 'elevated'
				AND required_gate = 'workspace_sandbox_adapter' AND operator_confirmed = 1)
			OR (mode = 'approval' AND approval_policy = 'per_command'
				AND command_scope = 'arbitrary_stateless'
				AND filesystem_scope = 'host_full' AND network_scope = 'host'
				AND persistent_terminal = 0 AND background_process = 0
				AND agent_terminal_input = 0 AND risk_tier = 'elevated'
				AND required_gate = 'operator_approval' AND operator_confirmed = 1)
			OR (mode = 'full_access' AND approval_policy = 'none'
				AND command_scope = 'arbitrary_stateless'
				AND filesystem_scope = 'host_full' AND network_scope = 'host'
				AND persistent_terminal = 0 AND background_process = 0
				AND agent_terminal_input = 0 AND risk_tier = 'high'
				AND required_gate = 'danger_full_access' AND operator_confirmed = 1)
			OR (mode = 'debug' AND approval_policy = 'none'
				AND command_scope = 'arbitrary_persistent'
				AND filesystem_scope = 'host_full' AND network_scope = 'host'
				AND persistent_terminal = 1 AND background_process = 1
				AND agent_terminal_input = 1 AND risk_tier = 'high'
				AND required_gate = 'debug_maximum_access' AND operator_confirmed = 1)
		),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(thread_id = trim(thread_id) AND length(thread_id) BETWEEN 1 AND 256
			AND instr(thread_id, char(0)) = 0),
		CHECK(mission_id = trim(mission_id) AND length(mission_id) BETWEEN 1 AND 256
			AND instr(mission_id, char(0)) = 0),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0),
		CHECK(reason = trim(reason) AND length(reason) BETWEEN 1 AND 1024
			AND instr(reason, char(0)) = 0)
	);`,
	`CREATE INDEX idx_thread_execution_permission_snapshots_thread_revision
		ON thread_execution_permission_snapshots(thread_id, revision DESC);`,
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
			OR (current_run_effect IN ('applied', 'paused_and_applied')
				AND current_run_id IS NOT NULL
				AND current_run_permission_snapshot_id IS NOT NULL)
		)
	) WITHOUT ROWID;`,
	`INSERT INTO thread_execution_permission_snapshots
		(id, thread_id, mission_id, revision, protocol_version, mode, approval_policy,
		command_scope, filesystem_scope, network_scope, persistent_terminal,
		background_process, agent_terminal_input, risk_tier, required_gate,
		policy_version, operator_confirmed, process_enabled, execution_authorized,
		capability_grant, requested_by, reason, created_at)
		SELECT printf('thread-permission-v139-%016x', thread_record.rowid),
			thread_record.id, thread_record.mission_id, 1,
			'thread_execution_permission.v1', 'conservative', 'fixed_templates',
			'fixed_templates', 'workspace_guarded', 'disabled', 0, 0, 0, 'minimal',
			'conservative_control', 'execution_permission_policy.v1', 0, 0, 0, 0,
			'schema_v139', 'legacy compatibility conservative Thread permission',
			thread_record.created_at
		FROM threads thread_record;`,
	`CREATE TRIGGER trg_thread_execution_permission_snapshot_insert
		BEFORE INSERT ON thread_execution_permission_snapshots
		WHEN NOT EXISTS (
			SELECT 1 FROM threads thread_record
			WHERE thread_record.id = NEW.thread_id
				AND thread_record.mission_id = NEW.mission_id
				AND julianday(NEW.created_at) >= julianday(thread_record.created_at)
				AND (
					(NEW.revision = 1 AND NEW.mode = 'conservative'
						AND thread_record.status = 'active' AND NOT EXISTS (
							SELECT 1 FROM thread_execution_permission_snapshots existing
							WHERE existing.thread_id = NEW.thread_id
						))
					OR
					(NEW.revision > 1 AND thread_record.status = 'active'
						AND EXISTS (
							SELECT 1 FROM thread_execution_permission_snapshots previous
							WHERE previous.thread_id = NEW.thread_id
								AND previous.revision = NEW.revision - 1
								AND previous.protocol_version = NEW.protocol_version
								AND previous.policy_version = NEW.policy_version
								AND julianday(NEW.created_at) >= julianday(previous.created_at)
						))
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Thread execution permission binding or transition is invalid');
		END;`,
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
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Thread execution permission operation binding is invalid');
		END;`,
	`CREATE TRIGGER trg_thread_execution_permission_snapshot_update_immutable
		BEFORE UPDATE ON thread_execution_permission_snapshots BEGIN
			SELECT RAISE(ABORT, 'Thread execution permission snapshot cannot be updated');
		END;`,
	`CREATE TRIGGER trg_thread_execution_permission_snapshot_delete_immutable
		BEFORE DELETE ON thread_execution_permission_snapshots BEGIN
			SELECT RAISE(ABORT, 'Thread execution permission snapshot cannot be deleted');
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
