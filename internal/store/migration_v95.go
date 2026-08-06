package store

var analyzerExecutionCommitStatements = []string{
	`CREATE TABLE analyzer_executions (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		capability_id TEXT NOT NULL UNIQUE,
		consumption_id TEXT NOT NULL UNIQUE,
		requested_by TEXT NOT NULL,
		request_id TEXT NOT NULL,
		request_sha256 TEXT NOT NULL,
		candidate_sha256 TEXT NOT NULL,
		module_sha256 TEXT NOT NULL,
		execution_fingerprint TEXT NOT NULL UNIQUE,
		result_sha256 TEXT NOT NULL,
		result_bytes INTEGER NOT NULL,
		artifact_id TEXT NOT NULL UNIQUE,
		fingerprint TEXT NOT NULL UNIQUE,
		event_sequence INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(capability_id) REFERENCES analyzer_execution_capabilities(id) ON DELETE RESTRICT,
		FOREIGN KEY(consumption_id) REFERENCES analyzer_execution_consumptions(id) ON DELETE RESTRICT,
		FOREIGN KEY(artifact_id) REFERENCES run_artifacts(id) ON DELETE RESTRICT,
		UNIQUE(run_id, event_sequence),
		CHECK(event_sequence > 0),
		CHECK(result_bytes > 0 AND result_bytes <= 16384),
		CHECK(length(request_sha256) = 64 AND request_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(candidate_sha256) = 64 AND candidate_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(module_sha256) = 64 AND module_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(execution_fingerprint) = 64 AND execution_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(result_sha256) = 64 AND result_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(julianday(created_at) IS NOT NULL),
		CHECK(json_valid(payload_json)),
		CHECK(COALESCE(json_extract(payload_json, '$.protocol_version'), '') =
			'analyzer_execution_record.v1'),
		CHECK(COALESCE(json_extract(payload_json, '$.id'), '') = id),
		CHECK(COALESCE(json_extract(payload_json, '$.run_id'), '') = run_id),
		CHECK(COALESCE(json_extract(payload_json, '$.session_id'), '') = session_id),
		CHECK(COALESCE(json_extract(payload_json, '$.workspace_id'), '') = workspace_id),
		CHECK(COALESCE(json_extract(payload_json, '$.capability_id'), '') = capability_id),
		CHECK(COALESCE(json_extract(payload_json, '$.consumption_id'), '') = consumption_id),
		CHECK(COALESCE(json_extract(payload_json, '$.requested_by'), '') = requested_by),
		CHECK(COALESCE(json_extract(payload_json, '$.request_id'), '') = request_id),
		CHECK(COALESCE(json_extract(payload_json, '$.request_sha256'), '') = request_sha256),
		CHECK(COALESCE(json_extract(payload_json, '$.candidate_sha256'), '') = candidate_sha256),
		CHECK(COALESCE(json_extract(payload_json, '$.module_sha256'), '') = module_sha256),
		CHECK(COALESCE(json_extract(payload_json, '$.execution.fingerprint'), '') = execution_fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.result_sha256'), '') = result_sha256),
		CHECK(COALESCE(json_extract(payload_json, '$.result_bytes'), 0) = result_bytes),
		CHECK(COALESCE(json_extract(payload_json, '$.artifact_id'), '') = artifact_id),
		CHECK(COALESCE(json_extract(payload_json, '$.fingerprint'), '') = fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.created_at'), '') = created_at),
		CHECK(COALESCE(json_extract(payload_json, '$.capability_consumed'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.artifact_atomic'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.raw_request_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.bearer_token_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.filesystem_mounted'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.network_enabled'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.subprocess_enabled'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.host_process_authorized'), 1) = 0)
	);`,
	`CREATE INDEX idx_analyzer_executions_run_created
		ON analyzer_executions(run_id, created_at, id);`,
	`CREATE TRIGGER trg_analyzer_execution_insert
		BEFORE INSERT ON analyzer_executions
		WHEN NOT EXISTS (
			SELECT 1
			FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN analyzer_execution_capabilities capability ON capability.id = NEW.capability_id
			JOIN analyzer_execution_consumptions consumption ON consumption.id = NEW.consumption_id
			JOIN run_artifacts artifact ON artifact.id = NEW.artifact_id
			JOIN run_events event ON event.run_id = NEW.run_id
				AND event.sequence = NEW.event_sequence
			WHERE run.id = NEW.run_id
				AND run.session_id = NEW.session_id
				AND run.status NOT IN ('completed', 'failed', 'cancelled')
				AND mission.workspace_id = NEW.workspace_id
				AND capability.run_id = NEW.run_id
				AND capability.workspace_id = NEW.workspace_id
				AND capability.request_id = NEW.request_id
				AND capability.request_sha256 = NEW.request_sha256
				AND capability.candidate_sha256 = NEW.candidate_sha256
				AND capability.module_sha256 = NEW.module_sha256
				AND consumption.capability_id = capability.id
				AND consumption.run_id = NEW.run_id
				AND consumption.workspace_id = NEW.workspace_id
				AND consumption.request_id = NEW.request_id
				AND artifact.run_id = NEW.run_id
				AND artifact.session_id = NEW.session_id
				AND artifact.workspace_id = NEW.workspace_id
				AND artifact.source_id = NEW.id
				AND artifact.tool_name = 'embedded_analyzer'
				AND artifact.stream = 'stdout'
				AND artifact.sha256 = NEW.result_sha256
				AND artifact.size_bytes = NEW.result_bytes
				AND event.type = 'analyzer.execution_completed'
				AND event.subject_id = NEW.id
				AND COALESCE(json_extract(event.payload_json, '$.execution_fingerprint'), '') = NEW.fingerprint
				AND COALESCE(json_extract(event.payload_json, '$.artifact_id'), '') = NEW.artifact_id
				AND COALESCE(json_extract(event.payload_json, '$.redacted'), 0) = 1
		)
		BEGIN
			SELECT RAISE(ABORT, 'Analyzer execution binding is invalid');
		END;`,
	`CREATE TRIGGER trg_analyzer_execution_update_immutable
		BEFORE UPDATE ON analyzer_executions BEGIN
			SELECT RAISE(ABORT, 'Analyzer execution cannot be updated');
		END;`,
	`CREATE TRIGGER trg_analyzer_execution_delete_immutable
		BEFORE DELETE ON analyzer_executions BEGIN
			SELECT RAISE(ABORT, 'Analyzer execution cannot be deleted');
		END;`,
}
