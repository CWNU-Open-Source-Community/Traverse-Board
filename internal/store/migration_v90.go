package store

var hostCommandExecutionStatements = []string{
	`CREATE TABLE host_command_execution_intents (
		request_id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		interaction_snapshot_id TEXT NOT NULL,
		interaction_revision INTEGER NOT NULL,
		execution_profile_revision INTEGER NOT NULL,
		permission_snapshot_id TEXT NOT NULL,
		permission_revision INTEGER NOT NULL,
		permission_mode TEXT NOT NULL,
		spec_protocol_version TEXT NOT NULL,
		spec_policy_version TEXT NOT NULL,
		executable_path TEXT NOT NULL,
		executable_sha256 TEXT NOT NULL,
		argv_json TEXT NOT NULL,
		working_directory TEXT NOT NULL,
		environment_policy TEXT NOT NULL,
		environment_keys_json TEXT NOT NULL,
		environment_sha256 TEXT NOT NULL,
		network_intent TEXT NOT NULL,
		timeout_millis INTEGER NOT NULL,
		purpose TEXT NOT NULL,
		spec_fingerprint TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		non_sandboxed INTEGER NOT NULL,
		automatic_retry_allowed INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(interaction_snapshot_id)
			REFERENCES run_execution_interaction_snapshots(id) ON DELETE RESTRICT,
		FOREIGN KEY(permission_snapshot_id)
			REFERENCES run_execution_permission_snapshots(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'host_command_execution_intent.v1'),
		CHECK(policy_version = 'host_command_execution_policy.v1'),
		CHECK(permission_mode = 'full_access'),
		CHECK(spec_protocol_version = 'host_command.v1'),
		CHECK(spec_policy_version = 'host_command_policy.v1'),
		CHECK(environment_policy = 'sanitized_host_environment.v1'),
		CHECK(network_intent = 'host'),
		CHECK(timeout_millis BETWEEN 1 AND 600000),
		CHECK(interaction_revision > 0 AND execution_profile_revision > 0
			AND permission_revision > 0),
		CHECK(length(request_id) BETWEEN 1 AND 256
			AND instr(request_id, char(0)) = 0),
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(executable_path) BETWEEN 1 AND 32767
			AND instr(executable_path, char(0)) = 0),
		CHECK(length(working_directory) BETWEEN 1 AND 32767
			AND instr(working_directory, char(0)) = 0),
		CHECK(length(executable_sha256) = 64
			AND executable_sha256 = lower(executable_sha256)
			AND executable_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(environment_sha256) = 64
			AND environment_sha256 = lower(environment_sha256)
			AND environment_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(spec_fingerprint) = 64
			AND spec_fingerprint = lower(spec_fingerprint)
			AND spec_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(argv_json) AND json_type(argv_json) = 'array'),
		CHECK(json_valid(environment_keys_json)
			AND json_type(environment_keys_json) = 'array'),
		CHECK(purpose = trim(purpose) AND length(purpose) BETWEEN 1 AND 1200
			AND instr(purpose, char(0)) = 0),
		CHECK(non_sandboxed = 1 AND automatic_retry_allowed = 0)
	);`,
	`CREATE INDEX idx_host_command_execution_intents_run_created
		ON host_command_execution_intents(run_id, created_at DESC, request_id DESC);`,
	`CREATE TABLE host_command_execution_operations (
		operation_key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		request_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(request_id)
			REFERENCES host_command_execution_intents(request_id)
				ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*')
	) WITHOUT ROWID;`,
	`CREATE TABLE host_command_execution_receipts (
		request_id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		backend TEXT NOT NULL,
		exit_code INTEGER NOT NULL,
		stdout_observed_bytes INTEGER NOT NULL,
		stdout_captured_bytes INTEGER NOT NULL,
		stdout_prefix_sha256 TEXT NOT NULL,
		stdout_truncated INTEGER NOT NULL,
		stderr_observed_bytes INTEGER NOT NULL,
		stderr_captured_bytes INTEGER NOT NULL,
		stderr_prefix_sha256 TEXT NOT NULL,
		stderr_truncated INTEGER NOT NULL,
		started_at TEXT NOT NULL,
		completed_at TEXT NOT NULL,
		timed_out INTEGER NOT NULL,
		cancelled INTEGER NOT NULL,
		output_limit_exceeded INTEGER NOT NULL,
		tree_reaped INTEGER NOT NULL,
		non_sandboxed INTEGER NOT NULL,
		restricted_token INTEGER NOT NULL,
		low_integrity_token INTEGER NOT NULL,
		job_assigned_at_creation INTEGER NOT NULL,
		kill_on_job_close INTEGER NOT NULL,
		active_process_limit INTEGER NOT NULL,
		job_memory_limit INTEGER NOT NULL,
		stdin_closed INTEGER NOT NULL,
		environment_inherited INTEGER NOT NULL,
		network_requested INTEGER NOT NULL,
		persistent_process INTEGER NOT NULL,
		product_execution_enabled INTEGER NOT NULL,
		FOREIGN KEY(request_id)
			REFERENCES host_command_execution_intents(request_id)
				ON DELETE RESTRICT,
		CHECK(protocol_version = 'host_command_execution_receipt.v1'),
		CHECK(policy_version = 'host_command_execution_policy.v1'),
		CHECK(stdout_observed_bytes BETWEEN 0 AND 67108864
			AND stdout_captured_bytes BETWEEN 0 AND 65536
			AND stdout_captured_bytes <= stdout_observed_bytes),
		CHECK(stderr_observed_bytes BETWEEN 0 AND 67108864
			AND stderr_captured_bytes BETWEEN 0 AND 65536
			AND stderr_captured_bytes <= stderr_observed_bytes),
		CHECK(length(stdout_prefix_sha256) = 64
			AND stdout_prefix_sha256 = lower(stdout_prefix_sha256)
			AND stdout_prefix_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(stderr_prefix_sha256) = 64
			AND stderr_prefix_sha256 = lower(stderr_prefix_sha256)
			AND stderr_prefix_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(NOT (timed_out = 1 AND cancelled = 1)),
		CHECK(tree_reaped = 1 AND non_sandboxed = 1
			AND restricted_token = 0 AND low_integrity_token = 0
			AND job_assigned_at_creation = 1 AND kill_on_job_close = 1
			AND active_process_limit = 32
			AND job_memory_limit = 2147483648
			AND stdin_closed = 1 AND environment_inherited = 0
			AND network_requested = 1 AND persistent_process = 0
			AND product_execution_enabled = 1)
	);`,
	`CREATE TRIGGER trg_host_command_execution_intent_insert_binding
		BEFORE INSERT ON host_command_execution_intents
		WHEN NOT EXISTS (
			SELECT 1
			FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN run_execution_interaction_snapshots interaction
				ON interaction.id = NEW.interaction_snapshot_id
			JOIN run_execution_profile_snapshots profile
				ON profile.run_id = NEW.run_id
				AND profile.revision = NEW.execution_profile_revision
			JOIN run_execution_permission_snapshots permission
				ON permission.id = NEW.permission_snapshot_id
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND run.session_id = NEW.session_id
				AND run.status IN ('created', 'paused')
				AND mission.workspace_id = NEW.workspace_id
				AND interaction.run_id = NEW.run_id
				AND interaction.mission_id = NEW.mission_id
				AND interaction.revision = NEW.interaction_revision
				AND interaction.execution_profile_revision =
					NEW.execution_profile_revision
				AND interaction.mode = 'controlled'
				AND interaction.surface = 'code'
				AND interaction.execution_profile = 'local'
				AND interaction.workspace_trust = 'trusted'
				AND interaction.command_form = 'structured_argv'
				AND profile.profile = 'local'
				AND permission.run_id = NEW.run_id
				AND permission.mission_id = NEW.mission_id
				AND permission.revision = NEW.permission_revision
				AND permission.mode = 'full_access'
				AND NOT EXISTS (
					SELECT 1 FROM run_execution_leases lease
					WHERE lease.run_id = NEW.run_id AND lease.status = 'active'
						AND julianday(lease.expires_at) > julianday('now')
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'host command execution intent binding is invalid');
		END;`,
	`CREATE TRIGGER trg_host_command_execution_operation_insert_binding
		BEFORE INSERT ON host_command_execution_operations
		WHEN NOT EXISTS (
			SELECT 1 FROM host_command_execution_intents intent
			WHERE intent.request_id = NEW.request_id
				AND intent.operation_key_digest = NEW.operation_key_digest
				AND intent.run_id = NEW.run_id
				AND intent.requested_by = NEW.requested_by
		)
		BEGIN
			SELECT RAISE(ABORT, 'host command operation binding is invalid');
		END;`,
	`CREATE TRIGGER trg_host_command_execution_receipt_insert_binding
		BEFORE INSERT ON host_command_execution_receipts
		WHEN NOT EXISTS (
			SELECT 1 FROM host_command_execution_intents intent
			WHERE intent.request_id = NEW.request_id
				AND intent.non_sandboxed = NEW.non_sandboxed
				AND intent.automatic_retry_allowed = 0
		)
		BEGIN
			SELECT RAISE(ABORT, 'host command receipt binding is invalid');
		END;`,
	`CREATE TRIGGER trg_host_command_execution_intent_update_immutable
		BEFORE UPDATE ON host_command_execution_intents BEGIN
			SELECT RAISE(ABORT, 'host command execution intent cannot be updated');
		END;`,
	`CREATE TRIGGER trg_host_command_execution_intent_delete_immutable
		BEFORE DELETE ON host_command_execution_intents BEGIN
			SELECT RAISE(ABORT, 'host command execution intent cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_host_command_execution_operation_update_immutable
		BEFORE UPDATE ON host_command_execution_operations BEGIN
			SELECT RAISE(ABORT, 'host command execution operation cannot be updated');
		END;`,
	`CREATE TRIGGER trg_host_command_execution_operation_delete_immutable
		BEFORE DELETE ON host_command_execution_operations BEGIN
			SELECT RAISE(ABORT, 'host command execution operation cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_host_command_execution_receipt_update_immutable
		BEFORE UPDATE ON host_command_execution_receipts BEGIN
			SELECT RAISE(ABORT, 'host command execution receipt cannot be updated');
		END;`,
	`CREATE TRIGGER trg_host_command_execution_receipt_delete_immutable
		BEFORE DELETE ON host_command_execution_receipts BEGIN
			SELECT RAISE(ABORT, 'host command execution receipt cannot be deleted');
		END;`,
}
