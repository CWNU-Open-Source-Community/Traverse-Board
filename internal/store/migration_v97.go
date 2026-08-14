package store

var sandboxDockerLifecycleStatements = []string{
	`CREATE TABLE sandbox_docker_lifecycle_intents (
		id TEXT PRIMARY KEY,
		attempt_id TEXT NOT NULL UNIQUE,
		plan_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		resource_generation INTEGER NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		spec_fingerprint TEXT NOT NULL,
		plan_fingerprint TEXT NOT NULL,
		authority_fingerprint TEXT NOT NULL,
		base_label_plan_fingerprint TEXT NOT NULL,
		ownership_label_fingerprint TEXT NOT NULL UNIQUE,
		container_name_fingerprint TEXT NOT NULL,
		endpoint_class TEXT NOT NULL,
		endpoint_fingerprint TEXT NOT NULL,
		intent_fingerprint TEXT NOT NULL UNIQUE,
		product_entry_enabled INTEGER NOT NULL,
		execution_authorized INTEGER NOT NULL,
		artifact_commit_authorized INTEGER NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(plan_id) REFERENCES sandbox_docker_container_plans(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'sandbox_docker_lifecycle_intent.v1'),
		CHECK(resource_generation = 1),
		CHECK(endpoint_class IN ('local_unix', 'local_npipe')),
		CHECK((endpoint_class = 'local_unix'
				AND endpoint_fingerprint = 'abb7b229bcc1ad7267b985ca29db577432c1233f0b2119200259822a4d5e1e73')
			OR (endpoint_class = 'local_npipe'
				AND endpoint_fingerprint = '17864598d1806ec5ebaa50d09e071857ee8ece05ce8febe9e3b0aff8ad37730c')),
		CHECK(product_entry_enabled = 0 AND execution_authorized = 0
			AND artifact_commit_authorized = 0),
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(spec_fingerprint) = 64
			AND spec_fingerprint = lower(spec_fingerprint)
			AND spec_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(plan_fingerprint) = 64
			AND plan_fingerprint = lower(plan_fingerprint)
			AND plan_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(authority_fingerprint) = 64
			AND authority_fingerprint = lower(authority_fingerprint)
			AND authority_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(base_label_plan_fingerprint) = 64
			AND base_label_plan_fingerprint = lower(base_label_plan_fingerprint)
			AND base_label_plan_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(ownership_label_fingerprint) = 64
			AND ownership_label_fingerprint = lower(ownership_label_fingerprint)
			AND ownership_label_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(container_name_fingerprint) = 64
			AND container_name_fingerprint = lower(container_name_fingerprint)
			AND container_name_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(endpoint_fingerprint) = 64
			AND endpoint_fingerprint = lower(endpoint_fingerprint)
			AND endpoint_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(intent_fingerprint) = 64
			AND intent_fingerprint = lower(intent_fingerprint)
			AND intent_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(attempt_id = trim(attempt_id) AND length(attempt_id) BETWEEN 1 AND 256
			AND instr(attempt_id, char(0)) = 0),
		CHECK(plan_id = trim(plan_id) AND length(plan_id) BETWEEN 1 AND 256
			AND instr(plan_id, char(0)) = 0),
		CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256
			AND instr(run_id, char(0)) = 0),
		CHECK(mission_id = trim(mission_id) AND length(mission_id) BETWEEN 1 AND 256
			AND instr(mission_id, char(0)) = 0),
		CHECK(workspace_id = trim(workspace_id) AND length(workspace_id) BETWEEN 1 AND 256
			AND instr(workspace_id, char(0)) = 0),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_sandbox_docker_lifecycle_intents_run_created
		ON sandbox_docker_lifecycle_intents(run_id, created_at, id);`,
	`CREATE TABLE sandbox_docker_lifecycle_leases (
		intent_id TEXT PRIMARY KEY,
		resource_generation INTEGER NOT NULL,
		lease_id TEXT NOT NULL UNIQUE,
		owner_id TEXT NOT NULL,
		generation INTEGER NOT NULL,
		status TEXT NOT NULL,
		acquired_at TEXT NOT NULL,
		renewed_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		released_at TEXT,
		FOREIGN KEY(intent_id) REFERENCES sandbox_docker_lifecycle_intents(id) ON DELETE RESTRICT,
		CHECK(resource_generation = 1 AND generation >= 1),
		CHECK(status IN ('active', 'released')),
		CHECK(julianday(acquired_at) IS NOT NULL AND julianday(renewed_at) IS NOT NULL
			AND julianday(expires_at) IS NOT NULL
			AND julianday(renewed_at) >= julianday(acquired_at)
			AND julianday(expires_at) > julianday(renewed_at)),
		CHECK((status = 'active' AND released_at IS NULL)
			OR (status = 'released' AND julianday(released_at) IS NOT NULL
				AND julianday(released_at) >= julianday(renewed_at)
				AND julianday(released_at) < julianday(expires_at))),
		CHECK(lease_id = trim(lease_id) AND length(lease_id) BETWEEN 1 AND 256
			AND instr(lease_id, char(0)) = 0),
		CHECK(owner_id = trim(owner_id) AND length(owner_id) BETWEEN 1 AND 256
			AND instr(owner_id, char(0)) = 0)
	) WITHOUT ROWID;`,
	`CREATE INDEX idx_sandbox_docker_lifecycle_leases_status_expiry
		ON sandbox_docker_lifecycle_leases(status, expires_at, intent_id);`,
	`CREATE TABLE sandbox_docker_lifecycle_actions (
		intent_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		lease_id TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		lease_generation INTEGER NOT NULL,
		resource_generation INTEGER NOT NULL,
		verb TEXT NOT NULL,
		action_fingerprint TEXT NOT NULL UNIQUE,
		prepared_at TEXT NOT NULL,
		PRIMARY KEY(intent_id, ordinal),
		UNIQUE(intent_id, resource_generation, lease_generation, verb),
		FOREIGN KEY(intent_id) REFERENCES sandbox_docker_lifecycle_intents(id) ON DELETE RESTRICT,
		CHECK(ordinal BETWEEN 1 AND 64),
		CHECK(lease_generation >= 1 AND resource_generation = 1),
		CHECK(verb IN ('create', 'start', 'term', 'kill', 'delete')),
		CHECK(length(action_fingerprint) = 64
			AND action_fingerprint = lower(action_fingerprint)
			AND action_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(lease_id = trim(lease_id) AND length(lease_id) BETWEEN 1 AND 256
			AND instr(lease_id, char(0)) = 0),
		CHECK(owner_id = trim(owner_id) AND length(owner_id) BETWEEN 1 AND 256
			AND instr(owner_id, char(0)) = 0),
		CHECK(julianday(prepared_at) IS NOT NULL)
	) WITHOUT ROWID;`,
	`CREATE TABLE sandbox_docker_lifecycle_transitions (
		intent_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		lease_id TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		lease_generation INTEGER NOT NULL,
		resource_generation INTEGER NOT NULL,
		state TEXT NOT NULL,
		reason_code TEXT NOT NULL,
		exit_code INTEGER,
		container_id_fingerprint TEXT,
		previous_fingerprint TEXT,
		transition_fingerprint TEXT NOT NULL UNIQUE,
		recorded_at TEXT NOT NULL,
		PRIMARY KEY(intent_id, ordinal),
		FOREIGN KEY(intent_id) REFERENCES sandbox_docker_lifecycle_intents(id) ON DELETE RESTRICT,
		CHECK(ordinal BETWEEN 1 AND 64),
		CHECK(lease_generation >= 1 AND resource_generation = 1),
		CHECK(state IN ('created', 'started', 'exited', 'cleaning', 'cleaned', 'failed')),
		CHECK(reason_code IN ('created', 'started', 'natural_exit', 'timeout', 'cancelled',
			'restart_recovery', 'cleanup_started', 'cleanup_completed', 'create_failed',
			'start_failed', 'wait_failed', 'terminate_failed', 'cleanup_failed',
			'transport_disabled', 'transport_unsupported', 'connection_failed',
			'invalid_response', 'configuration_mismatch', 'unsafe_existing_container')),
		CHECK((state = 'created' AND reason_code IN ('created', 'restart_recovery'))
			OR (state = 'started' AND reason_code IN ('started', 'restart_recovery'))
			OR (state = 'exited' AND reason_code IN
				('natural_exit', 'timeout', 'cancelled', 'restart_recovery'))
			OR (state = 'cleaning' AND reason_code IN
				('natural_exit', 'timeout', 'cancelled', 'restart_recovery', 'cleanup_started'))
			OR (state = 'cleaned' AND reason_code IN ('cleanup_completed', 'restart_recovery'))
			OR (state = 'failed' AND reason_code IN ('create_failed', 'start_failed',
				'wait_failed', 'terminate_failed', 'cleanup_failed', 'transport_disabled',
				'transport_unsupported', 'connection_failed', 'invalid_response',
				'configuration_mismatch', 'unsafe_existing_container'))),
		CHECK(exit_code IS NULL OR exit_code BETWEEN 0 AND 255),
		CHECK((state = 'exited' AND exit_code IS NOT NULL)
			OR (state <> 'exited' AND exit_code IS NULL)),
		CHECK((state IN ('created', 'started', 'exited')
				AND container_id_fingerprint IS NOT NULL
				AND length(container_id_fingerprint) = 64
				AND container_id_fingerprint = lower(container_id_fingerprint)
				AND container_id_fingerprint NOT GLOB '*[^0-9a-f]*')
			OR (state NOT IN ('created', 'started', 'exited')
				AND container_id_fingerprint IS NULL)),
		CHECK(previous_fingerprint IS NULL OR
			(length(previous_fingerprint) = 64
				AND previous_fingerprint = lower(previous_fingerprint)
				AND previous_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(transition_fingerprint) = 64
			AND transition_fingerprint = lower(transition_fingerprint)
			AND transition_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(lease_id = trim(lease_id) AND length(lease_id) BETWEEN 1 AND 256
			AND instr(lease_id, char(0)) = 0),
		CHECK(owner_id = trim(owner_id) AND length(owner_id) BETWEEN 1 AND 256
			AND instr(owner_id, char(0)) = 0),
		CHECK(julianday(recorded_at) IS NOT NULL)
	) WITHOUT ROWID;`,
	`CREATE INDEX idx_sandbox_docker_lifecycle_transitions_latest
		ON sandbox_docker_lifecycle_transitions(intent_id, ordinal DESC);`,
	`CREATE UNIQUE INDEX idx_sandbox_docker_lifecycle_transitions_single_checkpoint
		ON sandbox_docker_lifecycle_transitions(intent_id, state)
		WHERE state IN ('created', 'started', 'exited', 'cleaning', 'cleaned');`,
	`CREATE TABLE sandbox_docker_lifecycle_cleanup_receipts (
		intent_id TEXT PRIMARY KEY,
		lease_id TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		lease_generation INTEGER NOT NULL,
		resource_generation INTEGER NOT NULL,
		final_transition_fingerprint TEXT NOT NULL UNIQUE,
		container_id_fingerprint TEXT,
		outcome TEXT NOT NULL,
		exit_code INTEGER,
		container_removed_now INTEGER NOT NULL,
		container_already_absent INTEGER NOT NULL,
		cleanup_fingerprint TEXT NOT NULL UNIQUE,
		completed_at TEXT NOT NULL,
		FOREIGN KEY(intent_id) REFERENCES sandbox_docker_lifecycle_intents(id) ON DELETE RESTRICT,
		FOREIGN KEY(final_transition_fingerprint)
			REFERENCES sandbox_docker_lifecycle_transitions(transition_fingerprint)
			ON DELETE RESTRICT,
		CHECK(lease_generation >= 1 AND resource_generation = 1),
		CHECK(outcome IN ('natural_exit', 'timed_out', 'cancelled', 'failed')),
		CHECK(exit_code IS NULL OR exit_code BETWEEN 0 AND 255),
		CHECK(container_removed_now IN (0, 1) AND container_already_absent IN (0, 1)
			AND container_removed_now + container_already_absent = 1),
		CHECK(length(final_transition_fingerprint) = 64
			AND final_transition_fingerprint = lower(final_transition_fingerprint)
			AND final_transition_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(container_id_fingerprint IS NULL OR
			(length(container_id_fingerprint) = 64
				AND container_id_fingerprint = lower(container_id_fingerprint)
				AND container_id_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(container_removed_now = 0 OR container_id_fingerprint IS NOT NULL),
		CHECK(length(cleanup_fingerprint) = 64
			AND cleanup_fingerprint = lower(cleanup_fingerprint)
			AND cleanup_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(lease_id = trim(lease_id) AND length(lease_id) BETWEEN 1 AND 256
			AND instr(lease_id, char(0)) = 0),
		CHECK(owner_id = trim(owner_id) AND length(owner_id) BETWEEN 1 AND 256
			AND instr(owner_id, char(0)) = 0),
		CHECK(julianday(completed_at) IS NOT NULL)
	) WITHOUT ROWID;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_intent_insert
		BEFORE INSERT ON sandbox_docker_lifecycle_intents
		WHEN NOT EXISTS (
			SELECT 1
			FROM sandbox_docker_container_plans plan
			JOIN runs run ON run.id = plan.run_id
			JOIN missions mission ON mission.id = plan.mission_id
			WHERE plan.id = NEW.plan_id AND plan.run_id = NEW.run_id
				AND plan.mission_id = NEW.mission_id
				AND plan.workspace_id = NEW.workspace_id
				AND run.mission_id = NEW.mission_id
				AND mission.workspace_id = NEW.workspace_id
				AND plan.spec_fingerprint = NEW.spec_fingerprint
				AND plan.plan_fingerprint = NEW.plan_fingerprint
				AND plan.authority_fingerprint = NEW.authority_fingerprint
				AND plan.label_plan_fingerprint = NEW.base_label_plan_fingerprint
				AND plan.container_name_fingerprint = NEW.container_name_fingerprint
				AND plan.network_mode = 'disabled' AND plan.network_target_count = 0
				AND plan.environment_count = 0 AND plan.secret_reference_count = 0
				AND plan.simulation_only = 1 AND plan.production_submitted = 0
				AND plan.production_verified = 0 AND plan.backend_available = 0
				AND plan.backend_enabled = 0 AND plan.execution_authorized = 0
				AND plan.artifact_commit_authorized = 0
				AND plan.requested_by = NEW.requested_by
				AND run.status IN
					('created', 'preparing', 'running', 'waiting_approval', 'paused')
				AND julianday(NEW.created_at) >= julianday(plan.created_at)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle intent binding is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_lease_insert
		BEFORE INSERT ON sandbox_docker_lifecycle_leases
		WHEN NEW.generation <> 1 OR NEW.status <> 'active' OR NEW.released_at IS NOT NULL
			OR NEW.renewed_at <> NEW.acquired_at
			OR julianday(NEW.expires_at) <= julianday('now')
			OR NOT EXISTS (
				SELECT 1 FROM sandbox_docker_lifecycle_intents intent
				WHERE intent.id = NEW.intent_id
					AND intent.resource_generation = NEW.resource_generation
					AND julianday(NEW.acquired_at) >= julianday(intent.created_at)
			)
		BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle initial lease is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_lease_update
		BEFORE UPDATE ON sandbox_docker_lifecycle_leases
		WHEN NOT (
			(OLD.status = 'active' AND NEW.status = 'active'
				AND NEW.intent_id = OLD.intent_id
				AND NEW.resource_generation = OLD.resource_generation
				AND NEW.lease_id = OLD.lease_id AND NEW.owner_id = OLD.owner_id
				AND NEW.generation = OLD.generation AND NEW.acquired_at = OLD.acquired_at
				AND NEW.released_at IS NULL
				AND julianday(OLD.expires_at) > julianday('now')
				AND julianday(NEW.renewed_at) >= julianday(OLD.renewed_at)
				AND julianday(NEW.renewed_at) < julianday(OLD.expires_at)
				AND julianday(NEW.expires_at) >= julianday(OLD.expires_at)
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_cleanup_receipts receipt
					WHERE receipt.intent_id = OLD.intent_id))
			OR (OLD.status = 'active' AND NEW.status = 'released'
				AND NEW.intent_id = OLD.intent_id
				AND NEW.resource_generation = OLD.resource_generation
				AND NEW.lease_id = OLD.lease_id AND NEW.owner_id = OLD.owner_id
				AND NEW.generation = OLD.generation AND NEW.acquired_at = OLD.acquired_at
				AND NEW.renewed_at = OLD.renewed_at AND NEW.expires_at = OLD.expires_at
				AND julianday(OLD.expires_at) > julianday('now')
				AND julianday(NEW.released_at) >= julianday(OLD.renewed_at)
				AND julianday(NEW.released_at) < julianday(OLD.expires_at))
			OR ((OLD.status = 'released' OR julianday(OLD.expires_at) <= julianday('now'))
				AND NEW.status = 'active' AND NEW.intent_id = OLD.intent_id
				AND NEW.resource_generation = OLD.resource_generation
				AND NEW.lease_id <> OLD.lease_id AND NEW.generation = OLD.generation + 1
				AND NEW.renewed_at = NEW.acquired_at AND NEW.released_at IS NULL
				AND julianday(NEW.expires_at) > julianday('now')
				AND ((OLD.status = 'released'
						AND julianday(NEW.acquired_at) >= julianday(OLD.released_at))
					OR (OLD.status = 'active'
						AND julianday(NEW.acquired_at) >= julianday(OLD.expires_at)))
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_cleanup_receipts receipt
					WHERE receipt.intent_id = OLD.intent_id))
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle lease transition is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_action_insert
		BEFORE INSERT ON sandbox_docker_lifecycle_actions
		WHEN NOT (
			(SELECT COUNT(*) FROM sandbox_docker_lifecycle_actions action
				WHERE action.intent_id = NEW.intent_id) < 64
			AND EXISTS (
			SELECT 1
			FROM sandbox_docker_lifecycle_intents intent
			JOIN sandbox_docker_lifecycle_leases lease ON lease.intent_id = intent.id
			WHERE intent.id = NEW.intent_id
				AND intent.resource_generation = NEW.resource_generation
				AND lease.resource_generation = NEW.resource_generation
				AND lease.lease_id = NEW.lease_id AND lease.owner_id = NEW.owner_id
				AND lease.generation = NEW.lease_generation AND lease.status = 'active'
				AND julianday(lease.expires_at) > julianday('now')
				AND julianday(NEW.prepared_at) >= julianday(lease.renewed_at)
				AND julianday(NEW.prepared_at) < julianday(lease.expires_at)
				AND NEW.ordinal = 1 + (SELECT COALESCE(MAX(action.ordinal), 0)
					FROM sandbox_docker_lifecycle_actions action
					WHERE action.intent_id = NEW.intent_id)
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_actions action
					WHERE action.intent_id = NEW.intent_id
						AND julianday(action.prepared_at) > julianday(NEW.prepared_at))
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_transitions transition
					WHERE transition.intent_id = NEW.intent_id
						AND julianday(transition.recorded_at) > julianday(NEW.prepared_at))
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_cleanup_receipts receipt
					WHERE receipt.intent_id = NEW.intent_id)
				AND (
					(NEW.verb = 'create'
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state IN
									('created', 'started', 'exited', 'cleaning', 'cleaned')))
					OR (NEW.verb = 'start'
						AND EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'created')
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state IN ('started', 'cleaning', 'cleaned')))
					OR (NEW.verb IN ('term', 'kill')
						AND EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'started')
						AND EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'cleaning')
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state IN ('exited', 'cleaned'))
						AND (NEW.verb = 'term' OR EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_actions action
							WHERE action.intent_id = NEW.intent_id
								AND action.resource_generation = NEW.resource_generation
								AND action.lease_generation = NEW.lease_generation
								AND action.verb = 'term')))
					OR (NEW.verb = 'delete'
						AND EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'cleaning')
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'cleaned'))
				)
			)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle action fence or sequence is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_transition_insert
		BEFORE INSERT ON sandbox_docker_lifecycle_transitions
		WHEN NOT (
			(SELECT COUNT(*) FROM sandbox_docker_lifecycle_transitions transition
				WHERE transition.intent_id = NEW.intent_id) < 64
			AND EXISTS (
			SELECT 1
			FROM sandbox_docker_lifecycle_intents intent
			JOIN sandbox_docker_lifecycle_leases lease ON lease.intent_id = intent.id
			WHERE intent.id = NEW.intent_id
				AND intent.resource_generation = NEW.resource_generation
				AND lease.resource_generation = NEW.resource_generation
				AND lease.lease_id = NEW.lease_id AND lease.owner_id = NEW.owner_id
				AND lease.generation = NEW.lease_generation AND lease.status = 'active'
				AND julianday(lease.expires_at) > julianday('now')
				AND julianday(NEW.recorded_at) >= julianday(lease.renewed_at)
				AND julianday(NEW.recorded_at) < julianday(lease.expires_at)
				AND NEW.ordinal = 1 + (SELECT COALESCE(MAX(transition.ordinal), 0)
					FROM sandbox_docker_lifecycle_transitions transition
					WHERE transition.intent_id = NEW.intent_id)
				AND ((NEW.ordinal = 1 AND NEW.previous_fingerprint IS NULL)
					OR (NEW.ordinal > 1 AND NEW.previous_fingerprint = (SELECT transition_fingerprint
						FROM sandbox_docker_lifecycle_transitions transition
						WHERE transition.intent_id = NEW.intent_id
						ORDER BY transition.ordinal DESC LIMIT 1)))
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_actions action
					WHERE action.intent_id = NEW.intent_id
						AND julianday(action.prepared_at) > julianday(NEW.recorded_at))
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_transitions transition
					WHERE transition.intent_id = NEW.intent_id
						AND julianday(transition.recorded_at) > julianday(NEW.recorded_at))
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_cleanup_receipts receipt
					WHERE receipt.intent_id = NEW.intent_id)
				AND NOT EXISTS (
					SELECT 1 FROM sandbox_docker_lifecycle_transitions previous
					WHERE previous.intent_id = NEW.intent_id
						AND previous.container_id_fingerprint IS NOT NULL
						AND NEW.container_id_fingerprint IS NOT NULL
						AND previous.container_id_fingerprint <> NEW.container_id_fingerprint)
				AND (
					(NEW.state = 'created'
						AND EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_actions action
							WHERE action.intent_id = NEW.intent_id AND action.verb = 'create'
								AND julianday(action.prepared_at) <= julianday(NEW.recorded_at))
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state IN
									('created', 'started', 'exited', 'cleaning', 'cleaned')))
					OR (NEW.state = 'started'
						AND EXISTS (SELECT 1 FROM sandbox_docker_lifecycle_actions action
							WHERE action.intent_id = NEW.intent_id AND action.verb = 'start'
								AND julianday(action.prepared_at) <= julianday(NEW.recorded_at))
						AND EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'created')
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state IN ('started', 'cleaning', 'cleaned')))
					OR (NEW.state = 'exited'
						AND EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'started')
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state IN ('exited', 'cleaned')))
					OR (NEW.state = 'cleaning'
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state IN ('cleaning', 'cleaned'))
						AND (NEW.ordinal = 1 OR (SELECT state
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
							ORDER BY transition.ordinal DESC LIMIT 1)
								IN ('created', 'started', 'exited', 'failed')))
					OR (NEW.state = 'cleaned'
						AND EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'cleaning')
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'cleaned')
						AND (SELECT state
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
							ORDER BY transition.ordinal DESC LIMIT 1)
								IN ('cleaning', 'exited', 'failed'))
					OR (NEW.state = 'failed'
						AND NOT EXISTS (SELECT 1
							FROM sandbox_docker_lifecycle_transitions transition
							WHERE transition.intent_id = NEW.intent_id
								AND transition.state = 'cleaned'))
				)
			)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle transition fence or sequence is invalid');
		END;`,
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
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_intent_update_immutable
		BEFORE UPDATE ON sandbox_docker_lifecycle_intents BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle intent cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_intent_delete_immutable
		BEFORE DELETE ON sandbox_docker_lifecycle_intents BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle intent cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_lease_delete_immutable
		BEFORE DELETE ON sandbox_docker_lifecycle_leases BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle lease cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_action_update_immutable
		BEFORE UPDATE ON sandbox_docker_lifecycle_actions BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle action cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_action_delete_immutable
		BEFORE DELETE ON sandbox_docker_lifecycle_actions BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle action cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_transition_update_immutable
		BEFORE UPDATE ON sandbox_docker_lifecycle_transitions BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle transition cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_transition_delete_immutable
		BEFORE DELETE ON sandbox_docker_lifecycle_transitions BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle transition cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_update_immutable
		BEFORE UPDATE ON sandbox_docker_lifecycle_cleanup_receipts BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle cleanup receipt cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_delete_immutable
		BEFORE DELETE ON sandbox_docker_lifecycle_cleanup_receipts BEGIN
			SELECT RAISE(ABORT, 'Docker lifecycle cleanup receipt cannot be deleted');
		END;`,
}
