package store

var analyzerExecutionCapabilityStatements = []string{
	`CREATE TABLE analyzer_execution_capabilities (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		request_id TEXT NOT NULL,
		request_sha256 TEXT NOT NULL,
		candidate_sha256 TEXT NOT NULL,
		module_sha256 TEXT NOT NULL,
		bearer_token_sha256 TEXT NOT NULL UNIQUE,
		fingerprint TEXT NOT NULL UNIQUE,
		event_sequence INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		issued_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		UNIQUE(run_id, event_sequence),
		CHECK(event_sequence > 0),
		CHECK(length(request_sha256) = 64 AND request_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(candidate_sha256) = 64 AND candidate_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(module_sha256) = 64 AND module_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(bearer_token_sha256) = 64 AND bearer_token_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(julianday(issued_at) IS NOT NULL AND julianday(expires_at) > julianday(issued_at)),
		CHECK(json_valid(payload_json)),
		CHECK(COALESCE(json_extract(payload_json, '$.protocol_version'), '') =
			'analyzer_execution_capability.v1'),
		CHECK(COALESCE(json_extract(payload_json, '$.id'), '') = id),
		CHECK(COALESCE(json_extract(payload_json, '$.run_id'), '') = run_id),
		CHECK(COALESCE(json_extract(payload_json, '$.workspace_id'), '') = workspace_id),
		CHECK(COALESCE(json_extract(payload_json, '$.request_id'), '') = request_id),
		CHECK(COALESCE(json_extract(payload_json, '$.request_sha256'), '') = request_sha256),
		CHECK(COALESCE(json_extract(payload_json, '$.candidate_sha256'), '') = candidate_sha256),
		CHECK(COALESCE(json_extract(payload_json, '$.module_sha256'), '') = module_sha256),
		CHECK(COALESCE(json_extract(payload_json, '$.bearer_token_sha256'), '') = bearer_token_sha256),
		CHECK(COALESCE(json_extract(payload_json, '$.fingerprint'), '') = fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.issued_at'), '') = issued_at),
		CHECK(COALESCE(json_extract(payload_json, '$.expires_at'), '') = expires_at),
		CHECK(COALESCE(json_extract(payload_json, '$.exact_run_bound'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.exact_workspace_bound'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.exact_request_bound'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.exact_module_bound'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.one_shot'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.metadata_only'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.filesystem_authorized'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.network_authorized'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.subprocess_authorized'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.host_process_authorized'), 1) = 0)
	);`,
	`CREATE INDEX idx_analyzer_execution_capabilities_run_issued
		ON analyzer_execution_capabilities(run_id, issued_at, id);`,
	`CREATE TABLE analyzer_execution_consumptions (
		id TEXT PRIMARY KEY,
		capability_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		request_id TEXT NOT NULL,
		fingerprint TEXT NOT NULL UNIQUE,
		event_sequence INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		consumed_at TEXT NOT NULL,
		FOREIGN KEY(capability_id) REFERENCES analyzer_execution_capabilities(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		UNIQUE(run_id, event_sequence),
		CHECK(event_sequence > 0),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(julianday(consumed_at) IS NOT NULL),
		CHECK(json_valid(payload_json)),
		CHECK(COALESCE(json_extract(payload_json, '$.protocol_version'), '') =
			'analyzer_execution_consumption.v1'),
		CHECK(COALESCE(json_extract(payload_json, '$.id'), '') = id),
		CHECK(COALESCE(json_extract(payload_json, '$.capability_id'), '') = capability_id),
		CHECK(COALESCE(json_extract(payload_json, '$.run_id'), '') = run_id),
		CHECK(COALESCE(json_extract(payload_json, '$.workspace_id'), '') = workspace_id),
		CHECK(COALESCE(json_extract(payload_json, '$.request_id'), '') = request_id),
		CHECK(COALESCE(json_extract(payload_json, '$.fingerprint'), '') = fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.consumed_at'), '') = consumed_at),
		CHECK(COALESCE(json_extract(payload_json, '$.atomic'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.replay_guard_enforced'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.bearer_token_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.raw_request_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.raw_result_included'), 1) = 0)
	);`,
	`CREATE TRIGGER trg_analyzer_execution_capability_insert
		BEFORE INSERT ON analyzer_execution_capabilities
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN run_events event ON event.run_id = NEW.run_id
				AND event.sequence = NEW.event_sequence
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND mission.workspace_id = NEW.workspace_id
				AND run.status NOT IN ('completed', 'failed', 'cancelled')
				AND event.mission_id = NEW.mission_id
				AND event.type = 'analyzer.execution_capability_issued'
				AND event.subject_id = NEW.id
				AND COALESCE(json_extract(event.payload_json, '$.capability_fingerprint'), '') = NEW.fingerprint
				AND COALESCE(json_extract(event.payload_json, '$.redacted'), 0) = 1
		)
		BEGIN
			SELECT RAISE(ABORT, 'Analyzer execution capability binding is invalid');
		END;`,
	`CREATE TRIGGER trg_analyzer_execution_consumption_insert
		BEFORE INSERT ON analyzer_execution_consumptions
		WHEN NOT EXISTS (
			SELECT 1 FROM analyzer_execution_capabilities capability
			JOIN runs run ON run.id = NEW.run_id
			JOIN run_events event ON event.run_id = NEW.run_id
				AND event.sequence = NEW.event_sequence
			WHERE capability.id = NEW.capability_id
				AND capability.run_id = NEW.run_id
				AND capability.workspace_id = NEW.workspace_id
				AND capability.request_id = NEW.request_id
				AND run.status NOT IN ('completed', 'failed', 'cancelled')
				AND julianday(NEW.consumed_at) >= julianday(capability.issued_at)
				AND julianday(NEW.consumed_at) < julianday(capability.expires_at)
				AND event.type = 'analyzer.execution_capability_consumed'
				AND event.subject_id = NEW.id
				AND COALESCE(json_extract(event.payload_json, '$.consumption_fingerprint'), '') = NEW.fingerprint
				AND COALESCE(json_extract(event.payload_json, '$.redacted'), 0) = 1
		)
		BEGIN
			SELECT RAISE(ABORT, 'Analyzer execution consumption binding is invalid');
		END;`,
	`CREATE TRIGGER trg_analyzer_execution_capability_update_immutable
		BEFORE UPDATE ON analyzer_execution_capabilities BEGIN
			SELECT RAISE(ABORT, 'Analyzer execution capability cannot be updated');
		END;`,
	`CREATE TRIGGER trg_analyzer_execution_capability_delete_immutable
		BEFORE DELETE ON analyzer_execution_capabilities BEGIN
			SELECT RAISE(ABORT, 'Analyzer execution capability cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_analyzer_execution_consumption_update_immutable
		BEFORE UPDATE ON analyzer_execution_consumptions BEGIN
			SELECT RAISE(ABORT, 'Analyzer execution consumption cannot be updated');
		END;`,
	`CREATE TRIGGER trg_analyzer_execution_consumption_delete_immutable
		BEFORE DELETE ON analyzer_execution_consumptions BEGIN
			SELECT RAISE(ABORT, 'Analyzer execution consumption cannot be deleted');
		END;`,
}
