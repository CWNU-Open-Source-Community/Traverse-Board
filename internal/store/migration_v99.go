package store

var sandboxDockerProductAdmissionStatements = []string{
	`CREATE UNIQUE INDEX idx_sandbox_docker_log_capture_receipts_attempt_v99
		ON sandbox_docker_log_capture_receipts(attempt_id);`,
	`CREATE UNIQUE INDEX idx_sandbox_docker_output_staging_receipts_attempt_v99
		ON sandbox_docker_output_staging_receipts(attempt_id);`,
	`CREATE UNIQUE INDEX idx_sandbox_docker_output_commit_receipts_attempt_v99
		ON sandbox_docker_output_commit_receipts(attempt_id);`,
	`CREATE TABLE sandbox_docker_output_staging_entries (
		receipt_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		path TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size_bytes INTEGER NOT NULL,
		media_type TEXT NOT NULL,
		redacted INTEGER NOT NULL,
		PRIMARY KEY(receipt_id, ordinal),
		UNIQUE(receipt_id, path),
		FOREIGN KEY(receipt_id) REFERENCES sandbox_docker_output_staging_receipts(id)
			ON DELETE RESTRICT,
		CHECK(ordinal BETWEEN 1 AND 64),
		CHECK(length(path) BETWEEN 1 AND 1024 AND path = trim(path)
			AND instr(path, char(0)) = 0 AND instr(path, char(92)) = 0
			AND path NOT GLOB '/*'
			AND path NOT GLOB '*//*' AND path NOT GLOB '../*'
			AND path NOT GLOB '*/../*' AND path NOT GLOB '*/..'),
		CHECK(length(sha256) = 64 AND sha256 = lower(sha256)
			AND sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(size_bytes BETWEEN 1 AND 4194304),
		CHECK(length(media_type) BETWEEN 1 AND 256 AND media_type = trim(media_type)
			AND instr(media_type, char(0)) = 0),
		CHECK(redacted IN (0, 1))
	) WITHOUT ROWID;`,
	`CREATE TABLE sandbox_docker_product_admissions (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		lifecycle_operation_digest TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		plan_id TEXT NOT NULL UNIQUE,
		candidate_id TEXT NOT NULL,
		preparation_id TEXT NOT NULL,
		manifest_json TEXT NOT NULL,
		manifest_fingerprint TEXT NOT NULL,
		plan_fingerprint TEXT NOT NULL,
		spec_fingerprint TEXT NOT NULL,
		authority_fingerprint TEXT NOT NULL,
		readiness_fingerprint TEXT NOT NULL,
		readiness_expires_at TEXT NOT NULL,
		runtime_epoch_fingerprint TEXT NOT NULL,
		profile_snapshot_id TEXT NOT NULL,
		profile_revision INTEGER NOT NULL,
		permission_snapshot_id TEXT NOT NULL,
		permission_revision INTEGER NOT NULL,
		permission_mode TEXT NOT NULL,
		approval_id TEXT NOT NULL,
		approval_version INTEGER NOT NULL,
		policy_fingerprint TEXT NOT NULL,
		network_mode TEXT NOT NULL,
		network_target_count INTEGER NOT NULL,
		cpu_quota_millis INTEGER NOT NULL,
		memory_bytes INTEGER NOT NULL,
		pids INTEGER NOT NULL,
		disk_bytes INTEGER NOT NULL,
		wall_clock_seconds INTEGER NOT NULL,
		log_bytes INTEGER NOT NULL,
		log_lines INTEGER NOT NULL,
		tool_calls_remaining INTEGER NOT NULL,
		decision TEXT NOT NULL,
		reason_code TEXT NOT NULL,
		remediation_code TEXT NOT NULL,
		product_entry_enabled INTEGER NOT NULL,
		execution_authorized INTEGER NOT NULL,
		artifact_commit_authorized INTEGER NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		admission_fingerprint TEXT NOT NULL UNIQUE,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(plan_id) REFERENCES sandbox_docker_container_plans(id) ON DELETE RESTRICT,
		FOREIGN KEY(candidate_id) REFERENCES sandbox_execution_candidates(id) ON DELETE RESTRICT,
		FOREIGN KEY(preparation_id) REFERENCES sandbox_manifest_preparations(id) ON DELETE RESTRICT,
		FOREIGN KEY(profile_snapshot_id) REFERENCES run_execution_profile_snapshots(id) ON DELETE RESTRICT,
		FOREIGN KEY(permission_snapshot_id) REFERENCES run_execution_permission_snapshots(id) ON DELETE RESTRICT,
		FOREIGN KEY(approval_id) REFERENCES tool_approvals(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'docker_sandbox_admission.v1'),
		CHECK(decision = 'authorized' AND reason_code = 'ready' AND remediation_code = 'none'),
		CHECK(product_entry_enabled = 1 AND execution_authorized = 1
			AND artifact_commit_authorized = 1),
		CHECK(network_mode = 'disabled' AND network_target_count = 0),
		CHECK(permission_mode IN ('approval', 'full_access', 'debug')),
		CHECK(profile_revision >= 1 AND permission_revision >= 1 AND approval_version >= 1),
		CHECK(cpu_quota_millis BETWEEN 1 AND 8000),
		CHECK(memory_bytes BETWEEN 16777216 AND 8589934592),
		CHECK(pids BETWEEN 1 AND 512 AND disk_bytes BETWEEN 1 AND 16777216),
		CHECK(wall_clock_seconds BETWEEN 1 AND 3600),
		CHECK(log_bytes BETWEEN 1 AND 262144 AND log_lines BETWEEN 1 AND 4096),
		CHECK(tool_calls_remaining >= 1),
		CHECK(json_valid(manifest_json) AND length(manifest_json) BETWEEN 1 AND 65536),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(readiness_expires_at) IS NOT NULL
			AND julianday(readiness_expires_at) > julianday(created_at)),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256 AND instr(run_id, char(0)) = 0),
		CHECK(mission_id = trim(mission_id) AND length(mission_id) BETWEEN 1 AND 256 AND instr(mission_id, char(0)) = 0),
		CHECK(workspace_id = trim(workspace_id) AND length(workspace_id) BETWEEN 1 AND 256 AND instr(workspace_id, char(0)) = 0),
		CHECK(plan_id = trim(plan_id) AND length(plan_id) BETWEEN 1 AND 256 AND instr(plan_id, char(0)) = 0),
		CHECK(candidate_id = trim(candidate_id) AND length(candidate_id) BETWEEN 1 AND 256 AND instr(candidate_id, char(0)) = 0),
		CHECK(preparation_id = trim(preparation_id) AND length(preparation_id) BETWEEN 1 AND 256 AND instr(preparation_id, char(0)) = 0),
		CHECK(profile_snapshot_id = trim(profile_snapshot_id) AND length(profile_snapshot_id) BETWEEN 1 AND 256 AND instr(profile_snapshot_id, char(0)) = 0),
		CHECK(permission_snapshot_id = trim(permission_snapshot_id) AND length(permission_snapshot_id) BETWEEN 1 AND 256 AND instr(permission_snapshot_id, char(0)) = 0),
		CHECK(approval_id = trim(approval_id) AND length(approval_id) BETWEEN 1 AND 256 AND instr(approval_id, char(0)) = 0),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256 AND instr(requested_by, char(0)) = 0),
		CHECK(length(operation_key_digest) = 64 AND operation_key_digest = lower(operation_key_digest) AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint = lower(request_fingerprint) AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(lifecycle_operation_digest) = 64 AND lifecycle_operation_digest = lower(lifecycle_operation_digest) AND lifecycle_operation_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(manifest_fingerprint) = 64 AND manifest_fingerprint = lower(manifest_fingerprint) AND manifest_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(plan_fingerprint) = 64 AND plan_fingerprint = lower(plan_fingerprint) AND plan_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(spec_fingerprint) = 64 AND spec_fingerprint = lower(spec_fingerprint) AND spec_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(authority_fingerprint) = 64 AND authority_fingerprint = lower(authority_fingerprint) AND authority_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(readiness_fingerprint) = 64 AND readiness_fingerprint = lower(readiness_fingerprint) AND readiness_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(runtime_epoch_fingerprint) = 64 AND runtime_epoch_fingerprint = lower(runtime_epoch_fingerprint) AND runtime_epoch_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(policy_fingerprint) = 64 AND policy_fingerprint = lower(policy_fingerprint) AND policy_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(admission_fingerprint) = 64 AND admission_fingerprint = lower(admission_fingerprint) AND admission_fingerprint NOT GLOB '*[^0-9a-f]*')
	);`,
	`CREATE INDEX idx_sandbox_docker_product_admissions_run_created
		ON sandbox_docker_product_admissions(run_id, created_at, id);`,
	`CREATE TABLE sandbox_docker_product_cancellations (
		id TEXT PRIMARY KEY,
		admission_id TEXT NOT NULL UNIQUE,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		reason_code TEXT NOT NULL,
		cancellation_fingerprint TEXT NOT NULL UNIQUE,
		requested_at TEXT NOT NULL,
		FOREIGN KEY(admission_id) REFERENCES sandbox_docker_product_admissions(id)
			ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'docker_sandbox_cancellation.v1'),
		CHECK(reason_code = 'cancelled'),
		CHECK(julianday(requested_at) IS NOT NULL),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(admission_id = trim(admission_id) AND length(admission_id) BETWEEN 1 AND 256 AND instr(admission_id, char(0)) = 0),
		CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256 AND instr(run_id, char(0)) = 0),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256 AND instr(requested_by, char(0)) = 0),
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(cancellation_fingerprint) = 64
			AND cancellation_fingerprint = lower(cancellation_fingerprint)
			AND cancellation_fingerprint NOT GLOB '*[^0-9a-f]*')
	) WITHOUT ROWID;`,
	`CREATE TABLE sandbox_docker_product_start_requests (
		admission_id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		runtime_epoch_fingerprint TEXT NOT NULL,
		run_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		start_fingerprint TEXT NOT NULL UNIQUE,
		UNIQUE(admission_id, operation_key_digest),
		FOREIGN KEY(admission_id) REFERENCES sandbox_docker_product_admissions(id)
			ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'docker_sandbox_start.v1'),
		CHECK(julianday(created_at) IS NOT NULL),
		CHECK(admission_id = trim(admission_id) AND length(admission_id) BETWEEN 1 AND 256
			AND instr(admission_id, char(0)) = 0),
		CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256
			AND instr(run_id, char(0)) = 0),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0),
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(runtime_epoch_fingerprint) = 64
			AND runtime_epoch_fingerprint = lower(runtime_epoch_fingerprint)
			AND runtime_epoch_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(start_fingerprint) = 64
			AND start_fingerprint = lower(start_fingerprint)
			AND start_fingerprint NOT GLOB '*[^0-9a-f]*')
	) WITHOUT ROWID;`,
	`CREATE TABLE sandbox_docker_product_launches (
		admission_id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		start_operation_key_digest TEXT,
		lifecycle_intent_id TEXT NOT NULL UNIQUE,
		attempt_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		lifecycle_request_fingerprint TEXT NOT NULL,
		launch_fingerprint TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY(admission_id) REFERENCES sandbox_docker_product_admissions(id) ON DELETE RESTRICT,
		FOREIGN KEY(admission_id, start_operation_key_digest)
			REFERENCES sandbox_docker_product_start_requests(admission_id, operation_key_digest)
			ON DELETE RESTRICT,
		FOREIGN KEY(lifecycle_intent_id) REFERENCES sandbox_docker_lifecycle_intents(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'docker_sandbox_launch.v1'),
		CHECK(julianday(created_at) IS NOT NULL),
		CHECK(admission_id = trim(admission_id) AND length(admission_id) BETWEEN 1 AND 256 AND instr(admission_id, char(0)) = 0),
		CHECK(lifecycle_intent_id = trim(lifecycle_intent_id) AND length(lifecycle_intent_id) BETWEEN 1 AND 256 AND instr(lifecycle_intent_id, char(0)) = 0),
		CHECK(attempt_id = trim(attempt_id) AND length(attempt_id) BETWEEN 1 AND 256 AND instr(attempt_id, char(0)) = 0),
		CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256 AND instr(run_id, char(0)) = 0),
		CHECK(start_operation_key_digest IS NULL OR
			(length(start_operation_key_digest) = 64
				AND start_operation_key_digest = lower(start_operation_key_digest)
				AND start_operation_key_digest NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(lifecycle_request_fingerprint) = 64
			AND lifecycle_request_fingerprint = lower(lifecycle_request_fingerprint)
			AND lifecycle_request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(launch_fingerprint) = 64 AND launch_fingerprint = lower(launch_fingerprint) AND launch_fingerprint NOT GLOB '*[^0-9a-f]*')
	) WITHOUT ROWID;`,
	`CREATE TABLE sandbox_docker_product_receipts (
		id TEXT PRIMARY KEY,
		admission_id TEXT NOT NULL UNIQUE,
		protocol_version TEXT NOT NULL,
		lifecycle_intent_id TEXT NOT NULL UNIQUE,
		attempt_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		outcome TEXT NOT NULL,
		reason_code TEXT NOT NULL,
		exit_code INTEGER,
		log_receipt_id TEXT,
		output_staging_receipt_id TEXT,
		output_commit_receipt_id TEXT,
		artifact_count INTEGER NOT NULL,
		cleanup_complete INTEGER NOT NULL,
		artifact_commit_authorized INTEGER NOT NULL,
		receipt_fingerprint TEXT NOT NULL UNIQUE,
		completed_at TEXT NOT NULL,
		FOREIGN KEY(admission_id) REFERENCES sandbox_docker_product_admissions(id) ON DELETE RESTRICT,
		FOREIGN KEY(lifecycle_intent_id) REFERENCES sandbox_docker_lifecycle_cleanup_receipts(intent_id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(log_receipt_id) REFERENCES sandbox_docker_log_capture_receipts(id) ON DELETE RESTRICT,
		FOREIGN KEY(output_staging_receipt_id) REFERENCES sandbox_docker_output_staging_receipts(id) ON DELETE RESTRICT,
		FOREIGN KEY(output_commit_receipt_id) REFERENCES sandbox_docker_output_commit_receipts(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'docker_sandbox_receipt.v1'),
		CHECK(outcome IN ('succeeded', 'timed_out', 'cancelled', 'failed')),
		CHECK((outcome = 'succeeded' AND reason_code = 'completed')
			OR (outcome = 'timed_out' AND reason_code = 'timed_out')
			OR (outcome = 'cancelled' AND reason_code = 'cancelled')
			OR (outcome = 'failed' AND reason_code NOT IN ('completed', 'timed_out', 'cancelled'))),
		CHECK(exit_code IS NULL OR exit_code BETWEEN 0 AND 255),
		CHECK(artifact_count BETWEEN 0 AND 64),
		CHECK(cleanup_complete = 1 AND artifact_commit_authorized = 1),
		CHECK((output_commit_receipt_id IS NULL AND artifact_count = 0)
			OR (outcome = 'succeeded' AND output_commit_receipt_id IS NOT NULL
				AND output_staging_receipt_id IS NOT NULL AND artifact_count BETWEEN 1 AND 64)),
		CHECK(julianday(completed_at) IS NOT NULL),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(admission_id = trim(admission_id) AND length(admission_id) BETWEEN 1 AND 256 AND instr(admission_id, char(0)) = 0),
		CHECK(lifecycle_intent_id = trim(lifecycle_intent_id) AND length(lifecycle_intent_id) BETWEEN 1 AND 256 AND instr(lifecycle_intent_id, char(0)) = 0),
		CHECK(attempt_id = trim(attempt_id) AND length(attempt_id) BETWEEN 1 AND 256 AND instr(attempt_id, char(0)) = 0),
		CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256 AND instr(run_id, char(0)) = 0),
		CHECK(workspace_id = trim(workspace_id) AND length(workspace_id) BETWEEN 1 AND 256 AND instr(workspace_id, char(0)) = 0),
		CHECK(length(receipt_fingerprint) = 64 AND receipt_fingerprint = lower(receipt_fingerprint) AND receipt_fingerprint NOT GLOB '*[^0-9a-f]*')
	);`,
	`CREATE TRIGGER trg_sandbox_docker_product_admission_insert
		BEFORE INSERT ON sandbox_docker_product_admissions
		WHEN NOT EXISTS (
			SELECT 1
			FROM sandbox_docker_container_plans plan
			JOIN sandbox_execution_candidates candidate ON candidate.id = plan.candidate_id
			JOIN sandbox_manifest_preparations preparation ON preparation.id = plan.preparation_id
			JOIN sandbox_manifest_validations validation ON validation.preparation_id = preparation.id
			JOIN runs run ON run.id = plan.run_id
			JOIN missions mission ON mission.id = plan.mission_id
			JOIN run_execution_profile_snapshots profile ON profile.id = NEW.profile_snapshot_id
			JOIN run_execution_permission_snapshots permission ON permission.id = NEW.permission_snapshot_id
			JOIN tool_approvals approval ON approval.id = NEW.approval_id
			WHERE plan.id = NEW.plan_id AND plan.candidate_id = NEW.candidate_id
				AND plan.preparation_id = NEW.preparation_id
				AND plan.run_id = NEW.run_id AND plan.mission_id = NEW.mission_id
				AND plan.workspace_id = NEW.workspace_id AND run.mission_id = NEW.mission_id
				AND mission.workspace_id = NEW.workspace_id
				AND plan.manifest_fingerprint = NEW.manifest_fingerprint
				AND candidate.manifest_fingerprint = NEW.manifest_fingerprint
				AND preparation.manifest_fingerprint = NEW.manifest_fingerprint
				AND plan.plan_fingerprint = NEW.plan_fingerprint
				AND plan.spec_fingerprint = NEW.spec_fingerprint
				AND plan.authority_fingerprint = NEW.authority_fingerprint
				AND plan.policy_fingerprint = NEW.policy_fingerprint
				AND candidate.policy_fingerprint = NEW.policy_fingerprint
				AND validation.policy_fingerprint = NEW.policy_fingerprint
				AND preparation.backend = 'docker' AND preparation.network_mode = 'disabled'
				AND preparation.allowed_target_count = 0
				AND preparation.environment_count = 0 AND preparation.secret_reference_count = 0
				AND preparation.cpu_quota_millis = NEW.cpu_quota_millis
				AND preparation.memory_bytes = NEW.memory_bytes AND preparation.pids = NEW.pids
				AND preparation.max_output_bytes = NEW.disk_bytes
				AND NEW.wall_clock_seconds <= preparation.timeout_seconds
				AND plan.network_mode = 'disabled' AND plan.network_target_count = 0
				AND plan.environment_count = 0 AND plan.secret_reference_count = 0
				AND plan.nano_cpus = NEW.cpu_quota_millis * 1000000
				AND plan.memory_bytes = NEW.memory_bytes AND plan.pids = NEW.pids
				AND plan.max_output_bytes = NEW.disk_bytes
				AND NEW.wall_clock_seconds <= plan.timeout_seconds
				AND NEW.log_bytes = MIN(plan.max_output_bytes, 262144)
				AND NEW.log_lines = 4096
				AND plan.simulation_only = 1 AND plan.production_submitted = 0
				AND plan.production_verified = 0 AND plan.backend_available = 0
				AND plan.backend_enabled = 0 AND plan.execution_authorized = 0
				AND plan.artifact_commit_authorized = 0
				AND candidate.preparation_id = NEW.preparation_id
				AND candidate.run_id = NEW.run_id AND candidate.mission_id = NEW.mission_id
				AND candidate.workspace_id = NEW.workspace_id
				AND candidate.approval_id = NEW.approval_id
				AND candidate.approval_status = 'approved'
				AND candidate.backend_enabled = 0 AND candidate.execution_authorized = 0
				AND validation.policy_allowed = 1 AND validation.needs_approval = 1
				AND validation.approval_id = '' AND validation.approval_status = 'required'
				AND profile.run_id = NEW.run_id AND profile.mission_id = NEW.mission_id
				AND profile.revision = NEW.profile_revision AND profile.profile = 'docker'
				AND profile.backend = 'docker' AND profile.approval_policy = 'always'
				AND profile.network_scope = 'disabled'
				AND profile.process_enabled = 0 AND profile.execution_authorized = 0
				AND profile.capability_grant = 0
				AND profile.revision = (SELECT MAX(current.revision)
					FROM run_execution_profile_snapshots current WHERE current.run_id = NEW.run_id)
				AND permission.run_id = NEW.run_id AND permission.mission_id = NEW.mission_id
				AND permission.revision = NEW.permission_revision
				AND permission.mode = NEW.permission_mode AND permission.mode <> 'conservative'
				AND permission.operator_confirmed = 1
				AND permission.process_enabled = 0 AND permission.execution_authorized = 0
				AND permission.capability_grant = 0
				AND permission.revision = (SELECT MAX(current.revision)
					FROM run_execution_permission_snapshots current WHERE current.run_id = NEW.run_id)
				AND approval.proposal_id = NEW.preparation_id AND approval.run_id = NEW.run_id
				AND approval.session_id = run.session_id AND approval.workspace_id = NEW.workspace_id
				AND approval.tool_name = 'sandbox.manifest'
				AND approval.action_class = 'sandbox_execute'
				AND approval.mode = 'per_call' AND approval.status = 'approved'
				AND approval.version = NEW.approval_version
				AND approval.request_fingerprint = plan.authorization_fingerprint
				AND approval.requested_by = NEW.requested_by
				AND plan.requested_by = NEW.requested_by
				AND candidate.requested_by = NEW.requested_by
				AND preparation.requested_by = NEW.requested_by
				AND run.status IN ('created', 'preparing', 'running', 'waiting_approval', 'paused')
				AND julianday(NEW.created_at) >= julianday(plan.created_at)
				AND julianday(NEW.created_at) >= julianday(profile.created_at)
				AND julianday(NEW.created_at) >= julianday(permission.created_at)
				AND julianday(NEW.created_at) >= julianday(approval.updated_at)
				AND NOT EXISTS (SELECT 1 FROM run_execution_leases lease
					WHERE lease.run_id = NEW.run_id AND lease.status = 'active'
						AND julianday(lease.expires_at) > julianday('now'))
				AND candidate.tool_calls_used = COALESCE((SELECT usage.consumed
					FROM run_tool_usage usage WHERE usage.run_id = NEW.run_id), 0)
				AND candidate.execution_millis_used =
					COALESCE((SELECT checkpoint.execution_millis
						FROM run_supervisor_checkpoints checkpoint
						WHERE checkpoint.run_id = NEW.run_id), 0) +
					COALESCE((SELECT SUM(call.elapsed_millis)
						FROM specialist_model_calls call WHERE call.run_id = NEW.run_id), 0) +
					COALESCE((SELECT SUM(CASE WHEN call.elapsed_recorded = 1
						THEN call.elapsed_millis ELSE call.reserved_millis END)
						FROM readonly_fanout_model_calls call WHERE call.run_id = NEW.run_id), 0)
				AND NEW.wall_clock_seconds = CASE
					WHEN COALESCE(CAST(json_extract(run.budget_json, '$.timeout_seconds') AS INTEGER), 0) = 0
						THEN plan.timeout_seconds
					ELSE MIN(plan.timeout_seconds,
						(CAST(json_extract(run.budget_json, '$.timeout_seconds') AS INTEGER) * 1000
							- candidate.execution_millis_used) / 1000)
					END
				AND NEW.tool_calls_remaining = CASE
					WHEN COALESCE(CAST(json_extract(run.budget_json, '$.max_tool_calls') AS INTEGER), 0) = 0
						THEN 100
					ELSE MIN(100, CAST(json_extract(run.budget_json, '$.max_tool_calls') AS INTEGER)
						- candidate.tool_calls_used)
					END
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product admission binding is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_cancellation_insert
		BEFORE INSERT ON sandbox_docker_product_cancellations
		WHEN NOT EXISTS (
			SELECT 1 FROM sandbox_docker_product_admissions admission
			WHERE admission.id = NEW.admission_id AND admission.run_id = NEW.run_id
				AND admission.requested_by = NEW.requested_by
				AND julianday(NEW.requested_at) >= julianday(admission.created_at)
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_product_receipts receipt
					WHERE receipt.admission_id = admission.id)
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_product_launches launch
					JOIN sandbox_docker_lifecycle_transitions transition
						ON transition.intent_id = launch.lifecycle_intent_id
					WHERE launch.admission_id = admission.id
						AND transition.state IN ('exited', 'cleaning', 'cleaned'))
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_product_launches launch
					JOIN sandbox_docker_lifecycle_cleanup_receipts cleanup
						ON cleanup.intent_id = launch.lifecycle_intent_id
					WHERE launch.admission_id = admission.id)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product cancellation binding is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_start_request_insert
		BEFORE INSERT ON sandbox_docker_product_start_requests
		WHEN NOT EXISTS (
			SELECT 1 FROM sandbox_docker_product_admissions admission
			WHERE admission.id = NEW.admission_id AND admission.run_id = NEW.run_id
				AND admission.requested_by = NEW.requested_by
				AND admission.runtime_epoch_fingerprint = NEW.runtime_epoch_fingerprint
				AND admission.decision = 'authorized'
				AND admission.product_entry_enabled = 1
				AND admission.execution_authorized = 1
				AND admission.artifact_commit_authorized = 1
				AND julianday(NEW.created_at) >= julianday(admission.created_at)
				AND julianday(NEW.created_at) < julianday(admission.readiness_expires_at)
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_product_cancellations cancellation
					WHERE cancellation.admission_id = admission.id)
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_product_launches launch
					WHERE launch.admission_id = admission.id)
				AND NOT EXISTS (SELECT 1 FROM sandbox_docker_product_receipts receipt
					WHERE receipt.admission_id = admission.id)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product start request binding is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_launch_insert
		BEFORE INSERT ON sandbox_docker_product_launches
		WHEN NOT EXISTS (
			SELECT 1
			FROM sandbox_docker_product_admissions admission
			JOIN sandbox_docker_lifecycle_intents intent ON intent.id = NEW.lifecycle_intent_id
			WHERE admission.id = NEW.admission_id AND admission.run_id = NEW.run_id
				AND intent.attempt_id = NEW.attempt_id AND intent.plan_id = admission.plan_id
				AND intent.run_id = admission.run_id AND intent.mission_id = admission.mission_id
				AND intent.workspace_id = admission.workspace_id
				AND intent.operation_key_digest = admission.lifecycle_operation_digest
				AND ((EXISTS (SELECT 1 FROM sandbox_docker_product_start_requests start
						WHERE start.admission_id = admission.id
							AND start.operation_key_digest = NEW.start_operation_key_digest
							AND julianday(start.created_at) <= julianday(intent.created_at)))
					OR (NEW.start_operation_key_digest IS NULL
						AND NOT EXISTS (SELECT 1 FROM sandbox_docker_product_start_requests start
							WHERE start.admission_id = admission.id)
						AND EXISTS (SELECT 1 FROM sandbox_docker_product_cancellations cancellation
							WHERE cancellation.admission_id = admission.id
								AND julianday(cancellation.requested_at) <= julianday(intent.created_at))))
				AND intent.request_fingerprint = NEW.lifecycle_request_fingerprint
				AND intent.spec_fingerprint = admission.spec_fingerprint
				AND intent.plan_fingerprint = admission.plan_fingerprint
				AND intent.authority_fingerprint = admission.authority_fingerprint
				AND intent.requested_by = admission.requested_by
				AND intent.product_entry_enabled = 0 AND intent.execution_authorized = 0
				AND intent.artifact_commit_authorized = 0
				AND admission.decision = 'authorized' AND admission.product_entry_enabled = 1
				AND admission.execution_authorized = 1 AND admission.artifact_commit_authorized = 1
				AND intent.resource_generation = 1
				AND intent.created_at = NEW.created_at
				AND julianday(intent.created_at) >= julianday(admission.created_at)
				AND julianday(intent.created_at) < julianday(admission.readiness_expires_at)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product launch binding is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_receipt_insert
		BEFORE INSERT ON sandbox_docker_product_receipts
		WHEN NOT EXISTS (
			SELECT 1
			FROM sandbox_docker_product_admissions admission
			JOIN sandbox_docker_product_launches launch ON launch.admission_id = admission.id
			JOIN sandbox_docker_lifecycle_cleanup_receipts cleanup
				ON cleanup.intent_id = launch.lifecycle_intent_id
			WHERE admission.id = NEW.admission_id
				AND launch.lifecycle_intent_id = NEW.lifecycle_intent_id
				AND launch.attempt_id = NEW.attempt_id AND launch.run_id = NEW.run_id
				AND admission.workspace_id = NEW.workspace_id
				AND NEW.cleanup_complete = 1
				AND NEW.artifact_commit_authorized = admission.artifact_commit_authorized
				AND (NOT EXISTS (SELECT 1 FROM sandbox_docker_product_cancellations cancellation
						WHERE cancellation.admission_id = admission.id)
					OR NEW.outcome = 'cancelled')
				AND cleanup.exit_code IS NEW.exit_code
				AND ((NEW.outcome = 'succeeded' AND cleanup.outcome = 'natural_exit'
						AND cleanup.exit_code = 0)
					OR (NEW.outcome = 'timed_out' AND cleanup.outcome = 'timed_out')
					OR (NEW.outcome = 'cancelled' AND cleanup.outcome = 'cancelled')
					OR (NEW.outcome = 'failed'
						AND cleanup.outcome IN ('failed', 'natural_exit')))
				AND (NEW.log_receipt_id IS NULL OR EXISTS (
					SELECT 1 FROM sandbox_docker_log_capture_receipts log
					WHERE log.id = NEW.log_receipt_id AND log.attempt_id = NEW.attempt_id
						AND log.run_id = NEW.run_id
						AND (cleanup.container_id_fingerprint IS NULL
							OR log.container_id_fingerprint = cleanup.container_id_fingerprint)))
				AND (NEW.output_staging_receipt_id IS NULL OR EXISTS (
					SELECT 1 FROM sandbox_docker_output_staging_receipts staging
					WHERE staging.id = NEW.output_staging_receipt_id
						AND staging.attempt_id = NEW.attempt_id AND staging.run_id = NEW.run_id
						AND (cleanup.container_id_fingerprint IS NULL
							OR staging.container_id_fingerprint = cleanup.container_id_fingerprint)
						AND staging.file_count = (SELECT COUNT(*)
							FROM sandbox_docker_output_staging_entries entry
							WHERE entry.receipt_id = staging.id)
						AND staging.total_bytes = COALESCE((SELECT SUM(entry.size_bytes)
							FROM sandbox_docker_output_staging_entries entry
							WHERE entry.receipt_id = staging.id), 0)
						AND staging.redacted_count = COALESCE((SELECT SUM(entry.redacted)
							FROM sandbox_docker_output_staging_entries entry
							WHERE entry.receipt_id = staging.id), 0)))
				AND (NEW.output_commit_receipt_id IS NULL OR EXISTS (
					SELECT 1 FROM sandbox_docker_output_commit_receipts commit_receipt
					WHERE commit_receipt.id = NEW.output_commit_receipt_id
						AND commit_receipt.attempt_id = NEW.attempt_id
						AND commit_receipt.run_id = NEW.run_id
						AND commit_receipt.workspace_id = NEW.workspace_id
						AND commit_receipt.committed_count = NEW.artifact_count))
				AND julianday(NEW.completed_at) >= julianday(launch.created_at)
				AND julianday(NEW.completed_at) >= julianday(cleanup.completed_at)
				AND (NEW.log_receipt_id IS NULL OR julianday(NEW.completed_at) >= julianday(
					(SELECT created_at FROM sandbox_docker_log_capture_receipts WHERE id = NEW.log_receipt_id)))
				AND (NEW.output_staging_receipt_id IS NULL OR julianday(NEW.completed_at) >= julianday(
					(SELECT created_at FROM sandbox_docker_output_staging_receipts WHERE id = NEW.output_staging_receipt_id)))
				AND (NEW.output_commit_receipt_id IS NULL OR julianday(NEW.completed_at) >= julianday(
					(SELECT created_at FROM sandbox_docker_output_commit_receipts WHERE id = NEW.output_commit_receipt_id)))
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product receipt binding is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_admission_update_immutable
		BEFORE UPDATE ON sandbox_docker_product_admissions BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product admission cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_admission_delete_immutable
		BEFORE DELETE ON sandbox_docker_product_admissions BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product admission cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_cancellation_update_immutable
		BEFORE UPDATE ON sandbox_docker_product_cancellations BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product cancellation cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_cancellation_delete_immutable
		BEFORE DELETE ON sandbox_docker_product_cancellations BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product cancellation cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_start_request_update_immutable
		BEFORE UPDATE ON sandbox_docker_product_start_requests BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product start request cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_start_request_delete_immutable
		BEFORE DELETE ON sandbox_docker_product_start_requests BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product start request cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_launch_update_immutable
		BEFORE UPDATE ON sandbox_docker_product_launches BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product launch cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_launch_delete_immutable
		BEFORE DELETE ON sandbox_docker_product_launches BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product launch cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_receipt_update_immutable
		BEFORE UPDATE ON sandbox_docker_product_receipts BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product receipt cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_product_receipt_delete_immutable
		BEFORE DELETE ON sandbox_docker_product_receipts BEGIN
			SELECT RAISE(ABORT, 'Docker Sandbox product receipt cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_output_staging_entry_insert
		BEFORE INSERT ON sandbox_docker_output_staging_entries
		WHEN NOT EXISTS (
			SELECT 1 FROM sandbox_docker_output_staging_receipts receipt
			WHERE receipt.id = NEW.receipt_id AND NEW.ordinal <= receipt.file_count
		) OR EXISTS (
			SELECT 1 FROM sandbox_docker_output_staging_entries previous
			WHERE previous.receipt_id = NEW.receipt_id
				AND ((previous.ordinal < NEW.ordinal AND previous.path >= NEW.path)
					OR (previous.ordinal > NEW.ordinal AND previous.path <= NEW.path))
		)
		BEGIN
			SELECT RAISE(ABORT, 'Docker output staging entry binding is invalid');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_output_staging_entry_update_immutable
		BEFORE UPDATE ON sandbox_docker_output_staging_entries BEGIN
			SELECT RAISE(ABORT, 'Docker output staging entry cannot be updated');
		END;`,
	`CREATE TRIGGER trg_sandbox_docker_output_staging_entry_delete_immutable
		BEFORE DELETE ON sandbox_docker_output_staging_entries BEGIN
			SELECT RAISE(ABORT, 'Docker output staging entry cannot be deleted');
		END;`,
}
