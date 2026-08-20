package store

// uiEvidenceStatements adds the ui-evidence.v1 manifest, attempt, fixed-step,
// and content-addressed artifact ledgers. Manifests and artifacts are
// immutable; only the bounded not_run -> running -> terminal attempt state
// machine can update an attempt row.
var uiEvidenceStatements = []string{
	`CREATE TABLE ui_evidence_attempts (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		manifest_fingerprint TEXT NOT NULL,
		source_commit TEXT NOT NULL,
		dirty_digest TEXT NOT NULL,
		status TEXT NOT NULL,
		failure_stage TEXT NOT NULL,
		artifact_count INTEGER NOT NULL,
		artifact_bytes INTEGER NOT NULL,
		version INTEGER NOT NULL,
		manifest_json TEXT NOT NULL,
		attempt_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		started_at TEXT,
		completed_at TEXT,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'ui-evidence-attempt.v1'),
		CHECK(length(operation_digest) = 64 AND operation_digest = lower(operation_digest)
			AND operation_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(manifest_fingerprint) = 64 AND manifest_fingerprint = lower(manifest_fingerprint)
			AND manifest_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(dirty_digest) = 64 AND dirty_digest = lower(dirty_digest)
			AND dirty_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(status IN ('not_run', 'running', 'passed', 'failed', 'cancelled',
			'timed_out', 'interrupted')),
		CHECK(failure_stage IN ('none', 'build', 'launch', 'readiness', 'navigation',
			'selector', 'assertion', 'console', 'network', 'capture', 'cleanup')),
		CHECK(artifact_count BETWEEN 0 AND 10000),
		CHECK(artifact_bytes BETWEEN 0 AND 134217728),
		CHECK((artifact_count = 0) = (artifact_bytes = 0)),
		CHECK(status != 'passed' OR (artifact_count > 0 AND artifact_bytes > 0)),
		CHECK(version >= 1),
		CHECK((status = 'not_run' AND version = 1) OR
			(status = 'running' AND version = 2) OR
			(status IN ('passed', 'failed', 'cancelled', 'timed_out', 'interrupted')
				AND version = 3)),
		CHECK(json_valid(manifest_json) AND json_valid(attempt_json)),
		CHECK(json_extract(manifest_json, '$.protocol_version') IS 'ui-evidence.v1'),
		CHECK(json_extract(manifest_json, '$.attempt_id') IS id),
		CHECK(json_extract(manifest_json, '$.run_id') IS run_id),
		CHECK(json_extract(manifest_json, '$.mission_id') IS mission_id),
		CHECK(json_extract(manifest_json, '$.session_id') IS session_id),
		CHECK(json_extract(manifest_json, '$.workspace_id') IS workspace_id),
		CHECK(json_extract(manifest_json, '$.fingerprint') IS manifest_fingerprint),
		CHECK(json_extract(manifest_json, '$.created_at') IS created_at),
		CHECK(json_extract(manifest_json, '$.source.commit') IS source_commit),
		CHECK(json_extract(manifest_json, '$.source.dirty_digest') IS dirty_digest),
		CHECK(json_type(manifest_json, '$.steps') IS 'array'
			AND json_array_length(manifest_json, '$.steps') BETWEEN 1 AND 128),
		CHECK(json_extract(manifest_json, '$.steps[0].kind') IS 'navigate'),
		CHECK(json_extract(manifest_json, '$.capture.screenshot') IS 1),
		CHECK(json_extract(manifest_json, '$.capture.dom') IS 1),
		CHECK(json_extract(manifest_json, '$.capture.accessibility') IS 1),
		CHECK(json_extract(manifest_json, '$.capture.console') IS 1),
		CHECK(json_extract(manifest_json, '$.capture.network') IS 1),
		CHECK(json_extract(manifest_json, '$.capture.performance') IS 1),
		CHECK(json_extract(manifest_json, '$.capture.video') IS 0),
		CHECK(json_extract(manifest_json, '$.failure_policy.fail_on_console_error') IS 1),
		CHECK(json_extract(manifest_json, '$.failure_policy.fail_on_page_error') IS 1),
		CHECK(json_extract(manifest_json, '$.failure_policy.fail_on_request_error') IS 1),
		CHECK(json_extract(manifest_json, '$.failure_policy.fail_on_http_status') IS 1),
		CHECK(json_extract(manifest_json, '$.authority.process_start') IS 0),
		CHECK(json_extract(manifest_json, '$.authority.network_access') IS 0),
		CHECK(json_extract(manifest_json, '$.authority.credential_access') IS 0),
		CHECK(json_extract(manifest_json, '$.authority.personal_profile') IS 0),
		CHECK(json_extract(manifest_json, '$.authority.request_mutation') IS 0),
		CHECK(json_extract(manifest_json, '$.authority.verification_pass') IS 0),
		CHECK(json_extract(attempt_json, '$.protocol_version') IS protocol_version),
		CHECK(json_extract(attempt_json, '$.manifest.attempt_id') IS id),
		CHECK(json_extract(attempt_json, '$.manifest.fingerprint') IS manifest_fingerprint),
		CHECK(json_extract(attempt_json, '$.operation_digest') IS operation_digest),
		CHECK(json_extract(attempt_json, '$.request_fingerprint') IS request_fingerprint),
		CHECK(json_extract(attempt_json, '$.status') IS status),
		CHECK(json_extract(attempt_json, '$.failure_stage') IS failure_stage),
		CHECK(json_extract(attempt_json, '$.artifact_count') IS artifact_count),
		CHECK(json_extract(attempt_json, '$.artifact_bytes') IS artifact_bytes),
		CHECK(json_extract(attempt_json, '$.version') IS version),
		CHECK(json_extract(attempt_json, '$.created_at') IS created_at),
		CHECK(json_extract(attempt_json, '$.started_at') IS started_at),
		CHECK(json_extract(attempt_json, '$.completed_at') IS completed_at),
		CHECK(json_extract(attempt_json, '$.updated_at') IS updated_at),
		CHECK(status != 'passed' OR (
			json_extract(attempt_json, '$.cleanup.browser_tree_reaped') IS 1
			AND json_extract(attempt_json, '$.cleanup.application_tree_reaped') IS 1
			AND json_extract(attempt_json, '$.cleanup.profile_removed') IS 1
			AND json_extract(attempt_json, '$.cleanup.network_released') IS 1
			AND json_extract(attempt_json, '$.cleanup.port_released') IS 1
			AND json_extract(attempt_json, '$.diagnostics.console_errors') IS 0
			AND json_extract(attempt_json, '$.diagnostics.page_errors') IS 0
			AND json_extract(attempt_json, '$.diagnostics.failed_requests') IS 0
			AND json_extract(attempt_json, '$.diagnostics.http_failures') IS 0
			AND json_extract(attempt_json, '$.diagnostics.blocked_requests') IS 0)),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL),
		CHECK((status = 'not_run' AND started_at IS NULL AND completed_at IS NULL
			AND failure_stage = 'none') OR
			(status = 'running' AND started_at IS NOT NULL AND completed_at IS NULL
			AND failure_stage = 'none') OR
			(status IN ('passed', 'failed', 'cancelled', 'timed_out', 'interrupted')
				AND started_at IS NOT NULL AND completed_at IS NOT NULL)),
		CHECK(status != 'passed' OR failure_stage = 'none'),
		CHECK(status NOT IN ('failed', 'cancelled', 'timed_out', 'interrupted')
			OR failure_stage != 'none')
	);`,
	`CREATE INDEX idx_ui_evidence_attempts_run_created
		ON ui_evidence_attempts(run_id, created_at DESC, id DESC);`,
	`CREATE INDEX idx_ui_evidence_attempts_status_updated
		ON ui_evidence_attempts(status, updated_at, id);`,
	`CREATE TRIGGER trg_ui_evidence_attempt_run_binding
		BEFORE INSERT ON ui_evidence_attempts
		WHEN NOT EXISTS (SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND run.session_id = NEW.session_id
				AND mission.workspace_id = NEW.workspace_id)
		BEGIN SELECT RAISE(ABORT,
			'UI evidence manifest does not match its exact Run binding'); END;`,
	`CREATE TRIGGER trg_ui_evidence_attempt_identity_immutable
		BEFORE UPDATE ON ui_evidence_attempts
		WHEN NEW.id != OLD.id OR NEW.operation_digest != OLD.operation_digest
			OR NEW.request_fingerprint != OLD.request_fingerprint
			OR NEW.run_id != OLD.run_id OR NEW.mission_id != OLD.mission_id
			OR NEW.session_id != OLD.session_id OR NEW.workspace_id != OLD.workspace_id
			OR NEW.manifest_fingerprint != OLD.manifest_fingerprint
			OR NEW.source_commit != OLD.source_commit OR NEW.dirty_digest != OLD.dirty_digest
			OR NEW.manifest_json != OLD.manifest_json OR NEW.created_at != OLD.created_at
		BEGIN SELECT RAISE(ABORT, 'UI evidence manifest identity is immutable'); END;`,
	`CREATE TRIGGER trg_ui_evidence_attempt_transition
		BEFORE UPDATE ON ui_evidence_attempts
		WHEN NEW.version != OLD.version + 1 OR NOT (
			(OLD.status = 'not_run' AND NEW.status = 'running') OR
			(OLD.status = 'running' AND NEW.status IN
				('passed', 'failed', 'cancelled', 'timed_out', 'interrupted')))
		BEGIN SELECT RAISE(ABORT, 'UI evidence attempt transition is invalid'); END;`,
	`CREATE TRIGGER trg_ui_evidence_attempt_delete_immutable
		BEFORE DELETE ON ui_evidence_attempts
		BEGIN SELECT RAISE(ABORT, 'UI evidence attempt cannot be deleted'); END;`,
	`CREATE TABLE ui_evidence_steps (
		attempt_id TEXT NOT NULL,
		step_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		kind TEXT NOT NULL,
		status TEXT NOT NULL,
		failure_stage TEXT NOT NULL,
		fingerprint TEXT NOT NULL UNIQUE,
		payload_json TEXT NOT NULL,
		started_at TEXT NOT NULL,
		completed_at TEXT NOT NULL,
		PRIMARY KEY(attempt_id, step_id),
		UNIQUE(attempt_id, sequence),
		FOREIGN KEY(attempt_id) REFERENCES ui_evidence_attempts(id) ON DELETE RESTRICT,
		CHECK(sequence BETWEEN 1 AND 128),
		CHECK(kind IN ('navigate', 'click', 'type', 'assert_present', 'assert_absent', 'capture')),
		CHECK(status IN ('passed', 'failed', 'cancelled', 'timed_out')),
		CHECK(failure_stage IN ('none', 'build', 'launch', 'readiness', 'navigation',
			'selector', 'assertion', 'console', 'network', 'capture', 'cleanup')),
		CHECK((status = 'passed') = (failure_stage = 'none')),
		CHECK(length(fingerprint) = 64 AND fingerprint = lower(fingerprint)
			AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json)),
		CHECK(json_extract(payload_json, '$.protocol_version') IS 'ui-evidence-step.v1'),
		CHECK(json_extract(payload_json, '$.attempt_id') IS attempt_id),
		CHECK(json_extract(payload_json, '$.step_id') IS step_id),
		CHECK(json_extract(payload_json, '$.sequence') IS sequence),
		CHECK(json_extract(payload_json, '$.kind') IS kind),
		CHECK(json_extract(payload_json, '$.status') IS status),
		CHECK(json_extract(payload_json, '$.failure_stage') IS failure_stage),
		CHECK(json_extract(payload_json, '$.fingerprint') IS fingerprint),
		CHECK(json_extract(payload_json, '$.started_at') IS started_at),
		CHECK(json_extract(payload_json, '$.completed_at') IS completed_at),
		CHECK(julianday(started_at) IS NOT NULL AND julianday(completed_at) IS NOT NULL
			AND julianday(completed_at) >= julianday(started_at))
	) WITHOUT ROWID;`,
	`CREATE TRIGGER trg_ui_evidence_step_insert_running
		BEFORE INSERT ON ui_evidence_steps
		WHEN NOT EXISTS (SELECT 1 FROM ui_evidence_attempts
			WHERE id = NEW.attempt_id AND status = 'running'
				AND json_extract(manifest_json,
					'$.steps[' || (NEW.sequence - 1) || '].id') IS NEW.step_id
				AND json_extract(manifest_json,
					'$.steps[' || (NEW.sequence - 1) || '].kind') IS NEW.kind
				AND julianday(NEW.started_at) >= julianday(started_at))
		BEGIN SELECT RAISE(ABORT,
			'UI evidence step must match the exact running manifest'); END;`,
	`CREATE TRIGGER trg_ui_evidence_step_update_immutable
		BEFORE UPDATE ON ui_evidence_steps
		BEGIN SELECT RAISE(ABORT, 'UI evidence step cannot be updated'); END;`,
	`CREATE TRIGGER trg_ui_evidence_step_delete_immutable
		BEFORE DELETE ON ui_evidence_steps
		BEGIN SELECT RAISE(ABORT, 'UI evidence step cannot be deleted'); END;`,
	`CREATE TABLE ui_evidence_artifacts (
		id TEXT PRIMARY KEY,
		attempt_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		step_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		mime TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size_bytes INTEGER NOT NULL,
		width INTEGER NOT NULL,
		height INTEGER NOT NULL,
		source_commit TEXT NOT NULL,
		redacted INTEGER NOT NULL,
		fingerprint TEXT NOT NULL UNIQUE,
		metadata_json TEXT NOT NULL,
		content BLOB NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(attempt_id) REFERENCES ui_evidence_attempts(id) ON DELETE RESTRICT,
		FOREIGN KEY(attempt_id, step_id) REFERENCES ui_evidence_steps(attempt_id, step_id)
			ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(kind IN ('screenshot', 'dom', 'accessibility', 'console', 'network',
			'performance')),
		CHECK(size_bytes BETWEEN 1 AND 33554432 AND length(content) = size_bytes),
		CHECK(width BETWEEN 0 AND 7680 AND height BETWEEN 0 AND 4320),
		CHECK((kind = 'screenshot' AND mime = 'image/png' AND width > 0 AND height > 0)
			OR (kind != 'screenshot' AND width = 0 AND height = 0)),
		CHECK(length(sha256) = 64 AND sha256 = lower(sha256)
			AND sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(redacted IN (0, 1)),
		CHECK(length(fingerprint) = 64 AND fingerprint = lower(fingerprint)
			AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(metadata_json)),
		CHECK(json_extract(metadata_json, '$.protocol_version') IS 'ui-evidence-artifact.v1'),
		CHECK(json_extract(metadata_json, '$.id') IS id),
		CHECK(json_extract(metadata_json, '$.attempt_id') IS attempt_id),
		CHECK(json_extract(metadata_json, '$.run_id') IS run_id),
		CHECK(json_extract(metadata_json, '$.step_id') IS step_id),
		CHECK(json_extract(metadata_json, '$.kind') IS kind),
		CHECK(json_extract(metadata_json, '$.mime') IS mime),
		CHECK(json_extract(metadata_json, '$.sha256') IS sha256),
		CHECK(json_extract(metadata_json, '$.bytes') IS size_bytes),
		CHECK(json_extract(metadata_json, '$.source_commit') IS source_commit),
		CHECK(json_extract(metadata_json, '$.retention_policy') IS 'run_history'),
		CHECK(json_extract(metadata_json, '$.redacted') IS redacted),
		CHECK(json_extract(metadata_json, '$.untrusted') IS 1),
		CHECK(json_extract(metadata_json, '$.fingerprint') IS fingerprint),
		CHECK(json_extract(metadata_json, '$.created_at') IS created_at),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_ui_evidence_artifacts_attempt_created
		ON ui_evidence_artifacts(attempt_id, created_at, id);`,
	`CREATE TRIGGER trg_ui_evidence_attempt_artifact_totals
		BEFORE UPDATE ON ui_evidence_attempts
		WHEN NEW.artifact_count != (SELECT COUNT(*) FROM ui_evidence_artifacts
				WHERE attempt_id = NEW.id)
			OR NEW.artifact_bytes != (SELECT COALESCE(SUM(size_bytes), 0)
				FROM ui_evidence_artifacts WHERE attempt_id = NEW.id)
		BEGIN SELECT RAISE(ABORT,
			'UI evidence attempt artifact totals do not match immutable artifacts'); END;`,
	`CREATE TRIGGER trg_ui_evidence_attempt_pass_complete
		BEFORE UPDATE ON ui_evidence_attempts
		WHEN NEW.status = 'passed' AND (
			(SELECT COUNT(*) FROM ui_evidence_steps WHERE attempt_id = NEW.id)
				!= json_array_length(NEW.manifest_json, '$.steps')
			OR EXISTS (SELECT 1 FROM ui_evidence_steps
				WHERE attempt_id = NEW.id AND status != 'passed')
			OR (SELECT COUNT(DISTINCT kind) FROM ui_evidence_artifacts
				WHERE attempt_id = NEW.id AND kind IN ('screenshot', 'dom',
					'accessibility', 'console', 'network', 'performance')) != 6)
		BEGIN SELECT RAISE(ABORT,
			'passed UI evidence requires every manifest step and core artifact'); END;`,
	`CREATE TRIGGER trg_ui_evidence_attempt_terminal_time
		BEFORE UPDATE ON ui_evidence_attempts
		WHEN NEW.status IN ('passed', 'failed', 'cancelled', 'timed_out', 'interrupted')
			AND (EXISTS (SELECT 1 FROM ui_evidence_steps
				WHERE attempt_id = NEW.id
					AND julianday(completed_at) > julianday(NEW.completed_at))
			OR EXISTS (SELECT 1 FROM ui_evidence_artifacts
				WHERE attempt_id = NEW.id
					AND julianday(created_at) > julianday(NEW.completed_at)))
		BEGIN SELECT RAISE(ABORT,
			'UI evidence completion precedes its immutable evidence'); END;`,
	`CREATE TRIGGER trg_ui_evidence_artifact_insert_running
		BEFORE INSERT ON ui_evidence_artifacts
		WHEN NOT EXISTS (SELECT 1 FROM ui_evidence_attempts
			WHERE id = NEW.attempt_id AND run_id = NEW.run_id
				AND source_commit = NEW.source_commit AND status = 'running'
				AND NEW.kind != 'video'
				AND json_extract(NEW.metadata_json, '$.viewport.width')
					IS json_extract(manifest_json, '$.environment.viewport.width')
				AND json_extract(NEW.metadata_json, '$.viewport.height')
					IS json_extract(manifest_json, '$.environment.viewport.height')
				AND json_extract(NEW.metadata_json, '$.viewport.dpr')
					IS json_extract(manifest_json, '$.environment.viewport.dpr')
				AND (NEW.kind != 'screenshot' OR (
					ABS(NEW.width - json_extract(manifest_json,
						'$.environment.viewport.width') * json_extract(manifest_json,
						'$.environment.viewport.dpr')) <= 1
					AND ABS(NEW.height - json_extract(manifest_json,
						'$.environment.viewport.height') * json_extract(manifest_json,
						'$.environment.viewport.dpr')) <= 1))
				AND julianday(NEW.created_at) >= julianday(started_at))
		BEGIN SELECT RAISE(ABORT, 'UI evidence artifacts require the exact running attempt'); END;`,
	`CREATE TRIGGER trg_ui_evidence_artifact_attempt_quota
		BEFORE INSERT ON ui_evidence_artifacts
		WHEN (SELECT COALESCE(SUM(size_bytes), 0) FROM ui_evidence_artifacts
			WHERE attempt_id = NEW.attempt_id) + NEW.size_bytes > 134217728
		BEGIN SELECT RAISE(ABORT, 'UI evidence attempt artifact quota exceeded'); END;`,
	`CREATE TRIGGER trg_ui_evidence_artifact_store_quota
		BEFORE INSERT ON ui_evidence_artifacts
		WHEN (SELECT COALESCE(SUM(size_bytes), 0) FROM ui_evidence_artifacts)
			+ NEW.size_bytes > 2147483648
		BEGIN SELECT RAISE(ABORT, 'UI evidence artifact store quota exceeded'); END;`,
	`CREATE TRIGGER trg_ui_evidence_artifact_update_immutable
		BEFORE UPDATE ON ui_evidence_artifacts
		BEGIN SELECT RAISE(ABORT, 'UI evidence artifact cannot be updated'); END;`,
	`CREATE TRIGGER trg_ui_evidence_artifact_delete_immutable
		BEFORE DELETE ON ui_evidence_artifacts
		BEGIN SELECT RAISE(ABORT, 'UI evidence artifact cannot be deleted'); END;`,
}
