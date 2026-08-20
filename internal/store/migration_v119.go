package store

var scheduledJobStatements = []string{
	`CREATE TABLE scheduled_jobs (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		spec_json TEXT NOT NULL,
		target_run_id TEXT NOT NULL,
		owner_run_id TEXT NOT NULL,
		owner_root_agent_id TEXT NOT NULL,
		execution_mode TEXT NOT NULL,
		status TEXT NOT NULL,
		revision INTEGER NOT NULL,
		next_wake_at TEXT,
		pending_occurrence_at TEXT,
		rounds_completed INTEGER NOT NULL,
		model_calls INTEGER NOT NULL,
		consecutive_unchanged INTEGER NOT NULL,
		last_event_sequence INTEGER NOT NULL,
		last_observation_sha256 TEXT NOT NULL DEFAULT '',
		last_result TEXT NOT NULL DEFAULT '',
		last_error_code TEXT NOT NULL DEFAULT '',
		stop_reason TEXT NOT NULL DEFAULT '',
		active_lease_generation INTEGER NOT NULL DEFAULT 0,
		active_lease_owner_sha256 TEXT NOT NULL DEFAULT '',
		active_fence_token_sha256 TEXT NOT NULL DEFAULT '',
		active_lease_expires_at TEXT,
		created_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		completed_at TEXT,
		FOREIGN KEY(target_run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(owner_run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(owner_root_agent_id) REFERENCES agent_nodes(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'scheduled-job.v1'),
		CHECK(json_valid(spec_json) AND length(spec_json) BETWEEN 2 AND 32768),
		CHECK(target_run_id = owner_run_id),
		CHECK(execution_mode IN ('read_only', 'approved_repair')),
		CHECK(status IN ('active', 'paused', 'completed', 'failed', 'cancelled', 'exhausted')),
		CHECK(revision > 0 AND rounds_completed BETWEEN 0 AND 10000
			AND model_calls BETWEEN 0 AND 10000 AND consecutive_unchanged >= 0
			AND last_event_sequence >= 0 AND active_lease_generation >= 0),
		CHECK(length(last_observation_sha256) IN (0, 64)
			AND last_observation_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(last_result) <= 4096 AND length(last_error_code) <= 64
			AND length(stop_reason) <= 64),
		CHECK((active_lease_generation = 0 AND active_lease_owner_sha256 = ''
			AND active_fence_token_sha256 = '' AND active_lease_expires_at IS NULL)
			OR (active_lease_generation > 0 AND length(active_lease_owner_sha256) = 64
				AND length(active_fence_token_sha256) = 64
				AND julianday(active_lease_expires_at) IS NOT NULL
				AND pending_occurrence_at IS NOT NULL)),
		CHECK((status = 'active' AND next_wake_at IS NOT NULL
			AND julianday(next_wake_at) IS NOT NULL AND completed_at IS NULL
			AND stop_reason = '') OR
			(status = 'paused' AND next_wake_at IS NULL AND completed_at IS NULL
			AND stop_reason = '' AND active_lease_generation = 0) OR
			(status IN ('completed', 'failed', 'cancelled', 'exhausted')
				AND next_wake_at IS NULL AND julianday(completed_at) IS NOT NULL
				AND stop_reason <> '' AND active_lease_generation = 0)),
		CHECK(pending_occurrence_at IS NULL OR julianday(pending_occurrence_at) IS NOT NULL),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at)),
		CHECK(length(created_by) BETWEEN 1 AND 256)
	);`,
	`CREATE INDEX idx_scheduled_jobs_due
		ON scheduled_jobs(status, next_wake_at, id);`,
	`CREATE INDEX idx_scheduled_jobs_owner
		ON scheduled_jobs(owner_run_id, created_at DESC, id DESC);`,
	`CREATE TABLE scheduled_job_authorizations (
		job_id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mode_snapshot_id TEXT NOT NULL,
		mode_revision INTEGER NOT NULL,
		permission_snapshot_id TEXT NOT NULL,
		permission_revision INTEGER NOT NULL,
		authorized_by TEXT NOT NULL,
		authorized_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		execution_bypass INTEGER NOT NULL,
		network_bypass INTEGER NOT NULL,
		approval_bypass INTEGER NOT NULL,
		FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'scheduled-job-authorization.v1'),
		CHECK(mode_revision > 0 AND permission_revision > 0),
		CHECK(execution_bypass = 0 AND network_bypass = 0 AND approval_bypass = 0),
		CHECK(julianday(authorized_at) IS NOT NULL AND julianday(expires_at) IS NOT NULL
			AND julianday(expires_at) > julianday(authorized_at))
	);`,
	`CREATE TRIGGER trg_scheduled_job_authorization_update_immutable
		BEFORE UPDATE ON scheduled_job_authorizations BEGIN
			SELECT RAISE(ABORT, 'scheduled job authorization cannot be updated');
		END;`,
	`CREATE TRIGGER trg_scheduled_job_authorization_delete_immutable
		BEFORE DELETE ON scheduled_job_authorizations BEGIN
			SELECT RAISE(ABORT, 'scheduled job authorization cannot be deleted');
		END;`,
	`CREATE TABLE scheduled_job_operations (
		operation_key_sha256 TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		action TEXT NOT NULL,
		job_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		expected_revision INTEGER NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(length(operation_key_sha256) = 64
			AND operation_key_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(protocol_version = 'scheduled-job-control.v1'),
		CHECK(action IN ('create', 'pause', 'resume', 'cancel')),
		CHECK(expected_revision >= 0),
		CHECK(length(requested_by) BETWEEN 1 AND 256),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_scheduled_job_operations_job
		ON scheduled_job_operations(job_id, created_at, operation_key_sha256);`,
	`CREATE TRIGGER trg_scheduled_job_operation_update_immutable
		BEFORE UPDATE ON scheduled_job_operations BEGIN
			SELECT RAISE(ABORT, 'scheduled job operation cannot be updated');
		END;`,
	`CREATE TRIGGER trg_scheduled_job_operation_delete_immutable
		BEFORE DELETE ON scheduled_job_operations BEGIN
			SELECT RAISE(ABORT, 'scheduled job operation cannot be deleted');
		END;`,
	`CREATE TABLE scheduled_job_rounds (
		job_id TEXT NOT NULL,
		occurrence_at TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		attempt INTEGER NOT NULL,
		claim_generation INTEGER NOT NULL,
		fence_token_sha256 TEXT NOT NULL,
		owner_id_sha256 TEXT NOT NULL,
		status TEXT NOT NULL,
		event_sequence INTEGER NOT NULL,
		observation_sha256 TEXT NOT NULL DEFAULT '',
		changed INTEGER NOT NULL,
		model_called INTEGER NOT NULL,
		tool_called INTEGER NOT NULL,
		result TEXT NOT NULL DEFAULT '',
		error_code TEXT NOT NULL DEFAULT '',
		started_at TEXT NOT NULL,
		completed_at TEXT,
		PRIMARY KEY(job_id, occurrence_at),
		UNIQUE(job_id, ordinal),
		FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'scheduled-job-round.v1'),
		CHECK(ordinal BETWEEN 1 AND 10000 AND attempt BETWEEN 1 AND 8
			AND claim_generation > 0 AND event_sequence >= 0),
		CHECK(length(fence_token_sha256) = 64 AND fence_token_sha256 NOT GLOB '*[^0-9a-f]*'
			AND length(owner_id_sha256) = 64 AND owner_id_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(status IN ('claimed', 'retry_wait', 'unchanged', 'completed', 'failed', 'skipped')),
		CHECK(length(observation_sha256) IN (0, 64)
			AND observation_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(changed IN (0, 1) AND model_called IN (0, 1) AND tool_called IN (0, 1)
			AND (tool_called = 0 OR model_called = 1)),
		CHECK(length(result) <= 4096 AND length(error_code) <= 64),
		CHECK(julianday(occurrence_at) IS NOT NULL AND julianday(started_at) IS NOT NULL),
		CHECK((status IN ('claimed', 'retry_wait') AND completed_at IS NULL)
			OR (status IN ('unchanged', 'completed', 'failed', 'skipped')
				AND julianday(completed_at) IS NOT NULL))
	) WITHOUT ROWID;`,
	`CREATE INDEX idx_scheduled_job_rounds_recent
		ON scheduled_job_rounds(job_id, ordinal DESC);`,
	`CREATE TABLE scheduled_job_notifications (
		id TEXT PRIMARY KEY,
		job_id TEXT NOT NULL,
		dedup_key_sha256 TEXT NOT NULL UNIQUE,
		kind TEXT NOT NULL,
		summary TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE RESTRICT,
		CHECK(length(dedup_key_sha256) = 64
			AND dedup_key_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(kind IN ('change', 'failure', 'recovery', 'completed')),
		CHECK(length(summary) BETWEEN 1 AND 4096),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_scheduled_job_notifications_recent
		ON scheduled_job_notifications(job_id, created_at DESC, id DESC);`,
	`CREATE TRIGGER trg_scheduled_job_notification_update_immutable
		BEFORE UPDATE ON scheduled_job_notifications BEGIN
			SELECT RAISE(ABORT, 'scheduled job notification cannot be updated');
		END;`,
	`CREATE TRIGGER trg_scheduled_job_notification_delete_immutable
		BEFORE DELETE ON scheduled_job_notifications BEGIN
			SELECT RAISE(ABORT, 'scheduled job notification cannot be deleted');
		END;`,
}
