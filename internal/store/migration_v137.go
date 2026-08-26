package store

var standardCodeDeliveryStatements = []string{
	`CREATE TABLE standard_code_deliveries (
		id TEXT PRIMARY KEY,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		source_workspace_id TEXT NOT NULL,
		drydock_workspace_id TEXT NOT NULL,
		drydock_id TEXT NOT NULL,
		drydock_generation INTEGER NOT NULL,
		receipt_status TEXT NOT NULL,
		final_checkpoint_id TEXT NOT NULL,
		revision_sha256 TEXT NOT NULL,
		diff_sha256 TEXT NOT NULL,
		receipt_sha256 TEXT NOT NULL UNIQUE,
		payload_json TEXT NOT NULL,
		event_sequence INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(source_workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(drydock_workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(drydock_id) REFERENCES drydock_workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(final_checkpoint_id) REFERENCES workspace_checkpoints(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id, event_sequence) REFERENCES run_events(run_id, sequence) ON DELETE RESTRICT,
		CHECK(protocol_version = 'standard_code_delivery.v1'),
		CHECK(length(operation_key_digest) = 64 AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(drydock_generation > 0),
		CHECK(receipt_status IN ('passed','failed','partial','not_run','blocked','stale')),
		CHECK(length(revision_sha256) = 64 AND revision_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(diff_sha256) = 64 AND diff_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(receipt_sha256) = 64 AND receipt_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json)),
		CHECK(length(CAST(payload_json AS BLOB)) <= 2097152),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_standard_code_deliveries_run_event
		ON standard_code_deliveries(run_id, event_sequence DESC);`,
	`CREATE TRIGGER trg_standard_code_delivery_insert
		BEFORE INSERT ON standard_code_deliveries
		WHEN json_extract(NEW.payload_json, '$.id') <> NEW.id
			OR json_extract(NEW.payload_json, '$.protocol_version') <> NEW.protocol_version
			OR json_extract(NEW.payload_json, '$.operation_key_sha256') <> NEW.operation_key_digest
			OR json_extract(NEW.payload_json, '$.request_fingerprint') <> NEW.request_fingerprint
			OR json_extract(NEW.payload_json, '$.binding.run_id') <> NEW.run_id
			OR json_extract(NEW.payload_json, '$.binding.mission_id') <> NEW.mission_id
			OR json_extract(NEW.payload_json, '$.binding.session_id') <> NEW.session_id
			OR json_extract(NEW.payload_json, '$.binding.source_workspace_id') <> NEW.source_workspace_id
			OR json_extract(NEW.payload_json, '$.binding.drydock_workspace_id') <> NEW.drydock_workspace_id
			OR json_extract(NEW.payload_json, '$.binding.drydock_id') <> NEW.drydock_id
			OR json_extract(NEW.payload_json, '$.binding.drydock_generation') <> NEW.drydock_generation
			OR json_extract(NEW.payload_json, '$.status') <> NEW.receipt_status
			OR json_extract(NEW.payload_json, '$.receipt_status') <> NEW.receipt_status
			OR json_extract(NEW.payload_json, '$.final_checkpoint.id') <> NEW.final_checkpoint_id
			OR json_extract(NEW.payload_json, '$.final_checkpoint.revision_sha256') <> NEW.revision_sha256
			OR json_extract(NEW.payload_json, '$.diff.sha256') <> NEW.diff_sha256
			OR json_extract(NEW.payload_json, '$.receipt_sha256') <> NEW.receipt_sha256
			OR json_extract(NEW.payload_json, '$.event_sequence') <> NEW.event_sequence
			OR json_extract(NEW.payload_json, '$.verified') <> (NEW.receipt_status = 'passed')
			OR COALESCE(json_extract(NEW.payload_json, '$.observation.revision_sha256'), '') <> ''
			OR NOT EXISTS (
				SELECT 1 FROM runs run
				JOIN missions mission ON mission.id = run.mission_id
				JOIN sessions session_record ON session_record.id = run.session_id
				JOIN drydock_workspaces drydock ON drydock.id = NEW.drydock_id
				JOIN workspace_checkpoints checkpoint ON checkpoint.id = NEW.final_checkpoint_id
				JOIN run_events event ON event.run_id = NEW.run_id
					AND event.sequence = NEW.event_sequence
				WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
					AND run.session_id = NEW.session_id
					AND mission.workspace_id = NEW.source_workspace_id
					AND session_record.workspace_id = NEW.source_workspace_id
					AND drydock.run_id = NEW.run_id
					AND drydock.mission_id = NEW.mission_id
					AND drydock.session_id = NEW.session_id
					AND drydock.source_workspace_id = NEW.source_workspace_id
					AND drydock.workspace_id = NEW.drydock_workspace_id
					AND checkpoint.run_id = NEW.run_id
					AND checkpoint.mission_id = NEW.mission_id
					AND checkpoint.session_id = NEW.session_id
					AND checkpoint.workspace_id = NEW.drydock_workspace_id
					AND event.type = 'standard_code.delivery_recorded'
					AND event.source = 'standard_code_delivery'
					AND event.subject_id = NEW.id
			)
		BEGIN SELECT RAISE(ABORT, 'Standard Code delivery binding is invalid'); END;`,
	`CREATE TRIGGER trg_standard_code_delivery_update_immutable
		BEFORE UPDATE ON standard_code_deliveries BEGIN
			SELECT RAISE(ABORT, 'Standard Code delivery receipts are immutable');
		END;`,
	`CREATE TRIGGER trg_standard_code_delivery_delete_immutable
		BEFORE DELETE ON standard_code_deliveries BEGIN
			SELECT RAISE(ABORT, 'Standard Code delivery receipts are immutable');
		END;`,
}
