package store

var runExecutionInteractionStatements = []string{
	`CREATE TABLE run_execution_interaction_snapshots (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		revision INTEGER NOT NULL,
		protocol_version TEXT NOT NULL,
		mode TEXT NOT NULL,
		surface TEXT NOT NULL,
		execution_profile TEXT NOT NULL,
		execution_profile_revision INTEGER NOT NULL,
		workspace_trust TEXT NOT NULL,
		command_form TEXT NOT NULL,
		persistent_terminal INTEGER NOT NULL,
		user_input_available INTEGER NOT NULL,
		agent_input_default INTEGER NOT NULL,
		network_scope TEXT NOT NULL,
		required_gate TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		operator_confirmed INTEGER NOT NULL,
		process_enabled INTEGER NOT NULL,
		execution_authorized INTEGER NOT NULL,
		capability_grant INTEGER NOT NULL,
		requested_by TEXT NOT NULL,
		reason TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		UNIQUE(run_id, revision),
		CHECK(revision > 0 AND execution_profile_revision > 0),
		CHECK(protocol_version = 'run_execution_interaction.v1'),
		CHECK(policy_version = 'execution_interaction_policy.v1'),
		CHECK(surface IN ('code', 'cyber')),
		CHECK(execution_profile IN ('preview', 'docker', 'local')),
		CHECK(network_scope = 'disabled'),
		CHECK(agent_input_default = 0),
		CHECK(process_enabled = 0 AND execution_authorized = 0 AND capability_grant = 0),
		CHECK(
			(mode = 'preview' AND workspace_trust = 'untrusted' AND command_form = 'none'
				AND persistent_terminal = 0 AND user_input_available = 0
				AND required_gate = 'none' AND operator_confirmed = 0)
			OR (mode = 'controlled' AND surface = 'code' AND execution_profile = 'local'
				AND workspace_trust = 'trusted' AND command_form = 'structured_argv'
				AND persistent_terminal = 0 AND user_input_available = 0
				AND required_gate = 'local_os_sandbox_gate' AND operator_confirmed = 1)
			OR (mode = 'debug' AND surface = 'code' AND execution_profile = 'local'
				AND workspace_trust = 'trusted' AND command_form = 'user_conpty'
				AND persistent_terminal = 1 AND user_input_available = 1
				AND required_gate = 'debug_agent_input_lease' AND operator_confirmed = 1)
			OR (mode = 'cyber' AND surface = 'cyber' AND execution_profile = 'docker'
				AND workspace_trust = 'trusted' AND command_form = 'container_pty'
				AND persistent_terminal = 1 AND user_input_available = 1
				AND required_gate = 'cyber_container_terminal_gate' AND operator_confirmed = 1)
		),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256 AND instr(run_id, char(0)) = 0),
		CHECK(mission_id = trim(mission_id) AND length(mission_id) BETWEEN 1 AND 256
			AND instr(mission_id, char(0)) = 0),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0),
		CHECK(reason = trim(reason) AND length(reason) BETWEEN 1 AND 1024
			AND instr(reason, char(0)) = 0)
	);`,
	`CREATE INDEX idx_run_execution_interaction_snapshots_run_revision
		ON run_execution_interaction_snapshots(run_id, revision DESC);`,
	`CREATE TABLE run_execution_interaction_operations (
		operation_key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		snapshot_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(snapshot_id) REFERENCES run_execution_interaction_snapshots(id)
			ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0)
	) WITHOUT ROWID;`,
	`INSERT INTO run_execution_interaction_snapshots
		(id, run_id, mission_id, revision, protocol_version, mode, surface,
		execution_profile, execution_profile_revision, workspace_trust, command_form,
		persistent_terminal, user_input_available, agent_input_default, network_scope,
		required_gate, policy_version, operator_confirmed, process_enabled,
		execution_authorized, capability_grant, requested_by, reason, created_at)
		SELECT printf('run-interaction-v86-%016x', run.rowid), run.id, run.mission_id, 1,
			'run_execution_interaction.v1', 'preview',
			(SELECT mode.surface FROM run_mode_snapshots mode
				WHERE mode.run_id = run.id ORDER BY mode.revision DESC LIMIT 1),
			(SELECT profile.profile FROM run_execution_profile_snapshots profile
				WHERE profile.run_id = run.id ORDER BY profile.revision DESC LIMIT 1),
			(SELECT profile.revision FROM run_execution_profile_snapshots profile
				WHERE profile.run_id = run.id ORDER BY profile.revision DESC LIMIT 1),
			'untrusted', 'none', 0, 0, 0, 'disabled', 'none',
			'execution_interaction_policy.v1', 0, 0, 0, 0,
			'schema_v86', 'legacy compatibility preview', run.created_at
		FROM runs run;`,
	`CREATE TRIGGER trg_run_execution_interaction_snapshot_insert
		BEFORE INSERT ON run_execution_interaction_snapshots
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND julianday(NEW.created_at) >= julianday(run.created_at)
				AND EXISTS (
					SELECT 1 FROM run_mode_snapshots mode
					WHERE mode.run_id = NEW.run_id AND mode.surface = NEW.surface
						AND NOT EXISTS (
							SELECT 1 FROM run_mode_snapshots newer
							WHERE newer.run_id = mode.run_id AND newer.revision > mode.revision
						)
				)
				AND EXISTS (
					SELECT 1 FROM run_execution_profile_snapshots profile
					WHERE profile.run_id = NEW.run_id
						AND profile.profile = NEW.execution_profile
						AND profile.revision = NEW.execution_profile_revision
						AND NOT EXISTS (
							SELECT 1 FROM run_execution_profile_snapshots newer
							WHERE newer.run_id = profile.run_id
								AND newer.revision > profile.revision
						)
				)
				AND (
					(NEW.revision = 1 AND NEW.mode = 'preview'
						AND run.status = 'created' AND NOT EXISTS (
							SELECT 1 FROM run_execution_interaction_snapshots existing
							WHERE existing.run_id = NEW.run_id
						))
					OR
					(NEW.revision > 1 AND run.status IN ('created', 'paused')
						AND NOT EXISTS (
							SELECT 1 FROM run_execution_leases lease
							WHERE lease.run_id = NEW.run_id AND lease.status = 'active'
								AND julianday(lease.expires_at) > julianday('now')
						)
						AND EXISTS (
							SELECT 1 FROM run_execution_interaction_snapshots previous
							WHERE previous.run_id = NEW.run_id
								AND previous.revision = NEW.revision - 1
								AND previous.protocol_version = NEW.protocol_version
								AND previous.policy_version = NEW.policy_version
								AND julianday(NEW.created_at) >= julianday(previous.created_at)
						))
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Run execution interaction binding or transition is invalid');
		END;`,
	`CREATE TRIGGER trg_run_execution_interaction_operation_insert
		BEFORE INSERT ON run_execution_interaction_operations
		WHEN NOT EXISTS (
			SELECT 1 FROM run_execution_interaction_snapshots snapshot
			WHERE snapshot.id = NEW.snapshot_id AND snapshot.run_id = NEW.run_id
				AND snapshot.requested_by = NEW.requested_by
				AND snapshot.created_at = NEW.created_at AND snapshot.revision > 1
		)
		BEGIN
			SELECT RAISE(ABORT, 'Run execution interaction operation binding is invalid');
		END;`,
	`CREATE TRIGGER trg_run_execution_interaction_snapshot_update_immutable
		BEFORE UPDATE ON run_execution_interaction_snapshots BEGIN
			SELECT RAISE(ABORT, 'Run execution interaction snapshot cannot be updated');
		END;`,
	`CREATE TRIGGER trg_run_execution_interaction_snapshot_delete_immutable
		BEFORE DELETE ON run_execution_interaction_snapshots BEGIN
			SELECT RAISE(ABORT, 'Run execution interaction snapshot cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_run_execution_interaction_operation_update_immutable
		BEFORE UPDATE ON run_execution_interaction_operations BEGIN
			SELECT RAISE(ABORT, 'Run execution interaction operation cannot be updated');
		END;`,
	`CREATE TRIGGER trg_run_execution_interaction_operation_delete_immutable
		BEFORE DELETE ON run_execution_interaction_operations BEGIN
			SELECT RAISE(ABORT, 'Run execution interaction operation cannot be deleted');
		END;`,
}
