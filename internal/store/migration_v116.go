package store

// commandRuntimeStatements adds the Run-owned command-runtime.v2 job ledger and
// widens the Supervisor tool-call registry. The database validates every
// launch against the exact current Code/Deliver, local-profile, full-access,
// and generation-lease snapshots; process-local startup capabilities remain a
// separate application check and are deliberately never persisted as grants.
var commandRuntimeStatements = []string{
	`CREATE TABLE command_runtime_jobs (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		invocation_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		root_agent_id TEXT NOT NULL,
		workspace_root_sha256 TEXT NOT NULL,
		mode_snapshot_id TEXT NOT NULL,
		mode_revision INTEGER NOT NULL,
		profile_snapshot_id TEXT NOT NULL,
		profile_revision INTEGER NOT NULL,
		permission_snapshot_id TEXT NOT NULL,
		permission_revision INTEGER NOT NULL,
		permission_mode TEXT NOT NULL,
		lease_id TEXT NOT NULL,
		lease_generation INTEGER NOT NULL,
		lease_owner_id TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		owner_generation INTEGER NOT NULL,
		owner_renewed_at TEXT NOT NULL,
		owner_expires_at TEXT NOT NULL,
		intent_json TEXT NOT NULL,
		spec_fingerprint TEXT NOT NULL,
		profile TEXT NOT NULL,
		executable_path TEXT NOT NULL,
		executable_sha256 TEXT NOT NULL,
		environment_sha256 TEXT NOT NULL,
		working_directory TEXT NOT NULL,
		stdin_policy TEXT NOT NULL,
		network TEXT NOT NULL,
		credentials TEXT NOT NULL,
		timeout_milliseconds INTEGER NOT NULL,
		inline_limit_bytes INTEGER NOT NULL,
		artifact_limit_bytes INTEGER NOT NULL,
		state TEXT NOT NULL,
		pid INTEGER NOT NULL,
		process_group INTEGER NOT NULL,
		stdout TEXT NOT NULL,
		stderr TEXT NOT NULL,
		stdout_observed_bytes INTEGER NOT NULL,
		stderr_observed_bytes INTEGER NOT NULL,
		output_cursor INTEGER NOT NULL,
		output_base_cursor INTEGER NOT NULL,
		output_frames_json TEXT NOT NULL,
		stdout_sha256 TEXT NOT NULL,
		stderr_sha256 TEXT NOT NULL,
		truncation_reason TEXT NOT NULL,
		exit_code INTEGER,
		timed_out INTEGER NOT NULL,
		cancelled INTEGER NOT NULL,
		killed INTEGER NOT NULL,
		tree_reaped INTEGER NOT NULL,
		job_assigned_at_creation INTEGER NOT NULL,
		stdin_closed INTEGER NOT NULL,
		stdin_write_count INTEGER NOT NULL,
		version INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		started_at TEXT,
		completed_at TEXT,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id, root_agent_id) REFERENCES agent_nodes(run_id, id) ON DELETE RESTRICT,
		FOREIGN KEY(mode_snapshot_id) REFERENCES run_mode_snapshots(id) ON DELETE RESTRICT,
		FOREIGN KEY(profile_snapshot_id) REFERENCES run_execution_profile_snapshots(id) ON DELETE RESTRICT,
		FOREIGN KEY(permission_snapshot_id) REFERENCES run_execution_permission_snapshots(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'command-runtime.v2'),
		CHECK(profile IN ('powershell', 'bash', 'process')),
		CHECK(stdin_policy IN ('closed', 'pipe')),
		CHECK(network = 'disabled' AND credentials = 'none'),
		CHECK(permission_mode = 'full_access'),
		CHECK(mode_revision > 0 AND profile_revision > 0 AND permission_revision > 0
			AND lease_generation > 0 AND owner_generation > 0),
		CHECK(timeout_milliseconds BETWEEN 1 AND 1800000),
		CHECK(inline_limit_bytes BETWEEN 4096 AND 524288),
		CHECK(artifact_limit_bytes BETWEEN inline_limit_bytes AND 4194304),
		CHECK(state IN ('prepared', 'running', 'stopping', 'completed', 'failed',
			'timed_out', 'cancelled', 'killed', 'interrupted')),
		CHECK(pid >= 0 AND process_group >= 0),
		CHECK(length(CAST(stdout AS BLOB)) <= artifact_limit_bytes
			AND length(CAST(stderr AS BLOB)) <= artifact_limit_bytes),
		CHECK(stdout_observed_bytes >= 0 AND stderr_observed_bytes >= 0),
		CHECK(output_cursor >= 0 AND output_base_cursor BETWEEN 0 AND output_cursor),
		CHECK(json_valid(output_frames_json)
			AND length(CAST(output_frames_json AS BLOB)) BETWEEN 2 AND 2097152),
		CHECK(length(stdout_sha256) IN (0, 64) AND length(stderr_sha256) IN (0, 64)),
		CHECK(truncation_reason IN ('', 'inline_window', 'artifact_limit')),
		CHECK(timed_out IN (0, 1) AND cancelled IN (0, 1) AND killed IN (0, 1)
			AND tree_reaped IN (0, 1) AND job_assigned_at_creation IN (0, 1)
			AND stdin_closed IN (0, 1)),
		CHECK(stdin_write_count BETWEEN 0 AND 64 AND version > 0),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at)),
		CHECK(julianday(owner_renewed_at) IS NOT NULL
			AND julianday(owner_expires_at) IS NOT NULL
			AND julianday(owner_renewed_at) >= julianday(created_at)
			AND julianday(owner_expires_at) > julianday(owner_renewed_at)
			AND julianday(updated_at) >= julianday(owner_renewed_at)),
		CHECK((state = 'prepared' AND pid = 0 AND process_group = 0
				AND started_at IS NULL AND completed_at IS NULL AND exit_code IS NULL)
			OR (state IN ('running', 'stopping') AND pid > 0 AND process_group > 0
				AND job_assigned_at_creation = 1 AND started_at IS NOT NULL
				AND completed_at IS NULL AND exit_code IS NULL AND tree_reaped = 0)
			OR (state IN ('completed', 'failed', 'timed_out', 'cancelled', 'killed', 'interrupted')
				AND started_at IS NOT NULL AND completed_at IS NOT NULL
				AND exit_code IS NOT NULL AND tree_reaped = 1)),
		CHECK(timed_out = (state = 'timed_out') AND cancelled = (state = 'cancelled')
			AND killed = (state = 'killed')),
		CHECK(length(CAST(intent_json AS BLOB)) BETWEEN 2 AND 262144
			AND json_valid(intent_json)),
		CHECK(length(id) BETWEEN 1 AND 256 AND id = trim(id) AND instr(id, char(0)) = 0),
		CHECK(length(invocation_id) BETWEEN 1 AND 256 AND invocation_id = trim(invocation_id)
			AND instr(invocation_id, char(0)) = 0),
		CHECK(length(run_id) BETWEEN 1 AND 256 AND run_id = trim(run_id) AND instr(run_id, char(0)) = 0),
		CHECK(length(mission_id) BETWEEN 1 AND 256 AND mission_id = trim(mission_id) AND instr(mission_id, char(0)) = 0),
		CHECK(length(session_id) BETWEEN 1 AND 256 AND session_id = trim(session_id) AND instr(session_id, char(0)) = 0),
		CHECK(length(workspace_id) BETWEEN 1 AND 256 AND workspace_id = trim(workspace_id) AND instr(workspace_id, char(0)) = 0),
		CHECK(length(root_agent_id) BETWEEN 1 AND 256 AND root_agent_id = trim(root_agent_id) AND instr(root_agent_id, char(0)) = 0),
		CHECK(length(lease_id) BETWEEN 1 AND 256 AND lease_id = trim(lease_id) AND instr(lease_id, char(0)) = 0),
		CHECK(length(lease_owner_id) BETWEEN 1 AND 256 AND lease_owner_id = trim(lease_owner_id) AND instr(lease_owner_id, char(0)) = 0),
		CHECK(length(owner_id) BETWEEN 1 AND 256 AND owner_id = trim(owner_id) AND instr(owner_id, char(0)) = 0),
		CHECK(length(executable_path) BETWEEN 1 AND 4096 AND executable_path = trim(executable_path)
			AND instr(executable_path, char(0)) = 0),
		CHECK(length(working_directory) BETWEEN 1 AND 4096 AND working_directory = trim(working_directory)
			AND instr(working_directory, char(0)) = 0),
		CHECK(length(operation_digest) = 64 AND operation_digest = lower(operation_digest)
			AND operation_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(workspace_root_sha256) = 64 AND workspace_root_sha256 = lower(workspace_root_sha256)
			AND workspace_root_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(spec_fingerprint) = 64 AND spec_fingerprint = lower(spec_fingerprint)
			AND spec_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(executable_sha256) = 64 AND executable_sha256 = lower(executable_sha256)
			AND executable_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(environment_sha256) = 64 AND environment_sha256 = lower(environment_sha256)
			AND environment_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(stdout_sha256 = '' OR (stdout_sha256 = lower(stdout_sha256)
			AND stdout_sha256 NOT GLOB '*[^0-9a-f]*')),
		CHECK(stderr_sha256 = '' OR (stderr_sha256 = lower(stderr_sha256)
			AND stderr_sha256 NOT GLOB '*[^0-9a-f]*'))
	);`,
	`CREATE INDEX idx_command_runtime_jobs_run_created
		ON command_runtime_jobs(run_id, created_at DESC, id);`,
	`CREATE INDEX idx_command_runtime_jobs_active
		ON command_runtime_jobs(state, run_id, updated_at);`,
	`CREATE TRIGGER trg_command_runtime_job_insert_scope
		BEFORE INSERT ON command_runtime_jobs
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN agent_nodes root ON root.run_id = run.id AND root.id = NEW.root_agent_id
			JOIN run_mode_snapshots mode ON mode.id = NEW.mode_snapshot_id
			JOIN run_execution_profile_snapshots profile ON profile.id = NEW.profile_snapshot_id
			JOIN run_execution_permission_snapshots permission ON permission.id = NEW.permission_snapshot_id
			JOIN run_execution_leases lease ON lease.run_id = run.id
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND run.session_id = NEW.session_id AND mission.workspace_id = NEW.workspace_id
				AND run.status = 'running' AND root.parent_id IS NULL AND root.role = 'root'
				AND mode.run_id = run.id AND mode.mission_id = mission.id
				AND mode.revision = NEW.mode_revision AND mode.surface = 'code'
				AND mode.phase = 'deliver' AND mode.revision = (
					SELECT MAX(current.revision) FROM run_mode_snapshots current
					WHERE current.run_id = run.id)
				AND profile.run_id = run.id AND profile.mission_id = mission.id
				AND profile.revision = NEW.profile_revision AND profile.profile = 'local'
				AND profile.revision = (SELECT MAX(current.revision)
					FROM run_execution_profile_snapshots current WHERE current.run_id = run.id)
				AND permission.run_id = run.id AND permission.mission_id = mission.id
				AND permission.revision = NEW.permission_revision
				AND permission.mode = NEW.permission_mode AND permission.mode = 'full_access'
				AND permission.revision = (SELECT MAX(current.revision)
					FROM run_execution_permission_snapshots current WHERE current.run_id = run.id)
				AND lease.lease_id = NEW.lease_id AND lease.generation = NEW.lease_generation
				AND lease.owner_id = NEW.lease_owner_id AND lease.status = 'active'
				AND julianday(lease.expires_at) > julianday('now')
		)
		BEGIN
			SELECT RAISE(ABORT, 'command runtime scope is stale or unauthorized');
		END;`,
	`CREATE TRIGGER trg_command_runtime_job_insert_limit
		BEFORE INSERT ON command_runtime_jobs
		WHEN (SELECT COUNT(*) FROM command_runtime_jobs
			WHERE run_id = NEW.run_id AND state IN ('prepared', 'running', 'stopping')) >= 32
		BEGIN
			SELECT RAISE(ABORT, 'command runtime active job limit exceeded');
		END;`,
	`CREATE TRIGGER trg_command_runtime_job_update_transition
		BEFORE UPDATE ON command_runtime_jobs
		WHEN NEW.version != OLD.version + 1
			OR NEW.id != OLD.id OR NEW.operation_digest != OLD.operation_digest
			OR NEW.request_fingerprint != OLD.request_fingerprint
			OR NEW.invocation_id != OLD.invocation_id OR NEW.run_id != OLD.run_id
			OR NEW.mission_id != OLD.mission_id OR NEW.session_id != OLD.session_id
			OR NEW.workspace_id != OLD.workspace_id OR NEW.root_agent_id != OLD.root_agent_id
			OR NEW.workspace_root_sha256 != OLD.workspace_root_sha256
			OR NEW.mode_snapshot_id != OLD.mode_snapshot_id OR NEW.mode_revision != OLD.mode_revision
			OR NEW.profile_snapshot_id != OLD.profile_snapshot_id OR NEW.profile_revision != OLD.profile_revision
			OR NEW.permission_snapshot_id != OLD.permission_snapshot_id
			OR NEW.permission_revision != OLD.permission_revision OR NEW.permission_mode != OLD.permission_mode
			OR NEW.lease_id != OLD.lease_id OR NEW.lease_generation != OLD.lease_generation
			OR NEW.lease_owner_id != OLD.lease_owner_id OR NEW.owner_id != OLD.owner_id
			OR NEW.owner_generation != OLD.owner_generation
			OR julianday(NEW.owner_renewed_at) < julianday(OLD.owner_renewed_at)
			OR julianday(NEW.owner_expires_at) < julianday(OLD.owner_expires_at)
			OR NEW.intent_json != OLD.intent_json OR NEW.spec_fingerprint != OLD.spec_fingerprint
			OR NEW.profile != OLD.profile OR NEW.executable_path != OLD.executable_path
			OR NEW.executable_sha256 != OLD.executable_sha256
			OR NEW.environment_sha256 != OLD.environment_sha256
			OR NEW.working_directory != OLD.working_directory
			OR NEW.stdin_policy != OLD.stdin_policy OR NEW.network != OLD.network
			OR NEW.credentials != OLD.credentials OR NEW.timeout_milliseconds != OLD.timeout_milliseconds
			OR NEW.inline_limit_bytes != OLD.inline_limit_bytes
			OR NEW.artifact_limit_bytes != OLD.artifact_limit_bytes OR NEW.created_at != OLD.created_at
			OR NEW.stdout_observed_bytes < OLD.stdout_observed_bytes
			OR NEW.stderr_observed_bytes < OLD.stderr_observed_bytes
			OR NEW.output_cursor < OLD.output_cursor OR NEW.output_base_cursor < OLD.output_base_cursor
			OR NEW.stdin_write_count < OLD.stdin_write_count
			OR (OLD.state = 'prepared' AND NEW.state NOT IN ('running', 'failed', 'interrupted'))
			OR (OLD.state = 'running' AND NEW.state NOT IN ('running', 'stopping', 'completed', 'failed',
				'timed_out', 'cancelled', 'killed', 'interrupted'))
			OR (OLD.state = 'stopping' AND NEW.state NOT IN ('stopping', 'failed', 'timed_out',
				'cancelled', 'killed', 'interrupted'))
			OR (OLD.state IN ('completed', 'failed', 'timed_out', 'cancelled', 'killed', 'interrupted'))
		BEGIN
			SELECT RAISE(ABORT, 'command runtime transition is invalid');
		END;`,
	`CREATE TRIGGER trg_command_runtime_job_delete_immutable
		BEFORE DELETE ON command_runtime_jobs BEGIN
			SELECT RAISE(ABORT, 'command runtime jobs are immutable audit records');
		END;`,
	`DROP TRIGGER trg_supervisor_tool_call_model_attempt;`,
	`DROP TRIGGER trg_supervisor_tool_round_completion;`,
	`DROP INDEX idx_run_supervisor_tool_calls_pending;`,
	`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v115;`,
	`CREATE TABLE run_supervisor_tool_calls (
		run_id TEXT NOT NULL,
		turn INTEGER NOT NULL,
		attempt_id TEXT NOT NULL,
		round INTEGER NOT NULL,
		position INTEGER NOT NULL,
		model_attempt INTEGER NOT NULL,
		call_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		authority_json TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		result_json TEXT NOT NULL DEFAULT '',
		error_code TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		completed_at TEXT,
		PRIMARY KEY(run_id, turn, attempt_id, round, position),
		UNIQUE(run_id, turn, attempt_id, call_id),
		FOREIGN KEY(run_id, turn, attempt_id, round)
			REFERENCES run_supervisor_tool_rounds(run_id, turn, attempt_id, round) ON DELETE CASCADE,
		CHECK(position BETWEEN 1 AND 4),
		CHECK(model_attempt > 0),
		CHECK(tool_name IN ('work_item_create', 'note_create',
			'specialist_delegation_propose', 'child_task_propose',
			'plan_delivery_propose', 'controlled_command_propose',
			'one_shot_command_propose', 'host_command_propose',
			'sandbox_docker_run_propose', 'skill_candidate_propose', 'debug_terminal',
			'workspace_list', 'workspace_read', 'workspace_glob', 'workspace_grep',
			'workspace_change', 'workspace_apply', 'workspace_delete', 'command_runtime')),
		CHECK((tool_name IN ('workspace_list', 'workspace_read', 'workspace_glob',
			'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete')
			AND length(authority_json) BETWEEN 2 AND 4096 AND json_valid(authority_json) = 1)
			OR (tool_name NOT IN ('workspace_list', 'workspace_read', 'workspace_glob',
				'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete')
				AND authority_json = '')),
		CHECK(status IN ('pending', 'completed', 'denied', 'failed')),
		CHECK((status = 'pending' AND result_json = '' AND error_code = '' AND completed_at IS NULL)
			OR (status = 'completed' AND length(result_json) > 0 AND error_code = '' AND completed_at IS NOT NULL)
			OR (status IN ('denied', 'failed') AND length(result_json) > 0 AND length(error_code) > 0
				AND completed_at IS NOT NULL))
	);`,
	`INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		payload_json, authority_json, status, result_json, error_code, created_at, completed_at)
		SELECT run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		payload_json, authority_json, status, result_json, error_code, created_at, completed_at
		FROM run_supervisor_tool_calls_v115;`,
	`DROP TABLE run_supervisor_tool_calls_v115;`,
	`CREATE INDEX idx_run_supervisor_tool_calls_pending
		ON run_supervisor_tool_calls(run_id, turn, attempt_id, status, round, position);`,
	`CREATE TRIGGER trg_supervisor_tool_call_model_attempt
		BEFORE INSERT ON run_supervisor_tool_calls
		WHEN NOT EXISTS (
			SELECT 1 FROM run_supervisor_tool_rounds
			WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
				AND round = NEW.round AND model_attempt = NEW.model_attempt
		)
		BEGIN
			SELECT RAISE(ABORT, 'supervisor tool call model attempt mismatch');
		END;`,
	`CREATE TRIGGER trg_supervisor_tool_round_completion
		BEFORE UPDATE OF completed_at ON run_supervisor_tool_rounds
		WHEN NEW.completed_at IS NOT NULL AND EXISTS (
			SELECT 1 FROM run_supervisor_tool_calls
			WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
				AND round = NEW.round AND status = 'pending'
		)
		BEGIN
			SELECT RAISE(ABORT, 'supervisor tool round still has pending calls');
		END;`,
}
