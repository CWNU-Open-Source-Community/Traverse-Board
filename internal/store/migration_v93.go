package store

var analyzerStartControlStatements = []string{
	`CREATE TABLE analyzer_start_requests (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		signed_request_id TEXT NOT NULL,
		nonce_sha256 TEXT NOT NULL UNIQUE,
		fingerprint TEXT NOT NULL UNIQUE,
		adapter TEXT NOT NULL,
		event_sequence INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		registered_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		UNIQUE(run_id, event_sequence),
		CHECK(adapter IN ('disabled', 'fake')),
		CHECK(event_sequence > 0),
		CHECK(length(nonce_sha256) = 64 AND nonce_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json)),
		CHECK(COALESCE(json_extract(payload_json, '$.protocol_version'), '') =
			'analyzer_durable_start_request.v1'),
		CHECK(COALESCE(json_extract(payload_json, '$.id'), '') = id),
		CHECK(COALESCE(json_extract(payload_json, '$.run_id'), '') = run_id),
		CHECK(COALESCE(json_extract(payload_json, '$.workspace_id'), '') = workspace_id),
		CHECK(COALESCE(json_extract(payload_json, '$.signed_request_id'), '') = signed_request_id),
		CHECK(COALESCE(json_extract(payload_json, '$.nonce_sha256'), '') = nonce_sha256),
		CHECK(COALESCE(json_extract(payload_json, '$.fingerprint'), '') = fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.adapter'), '') = adapter),
		CHECK(COALESCE(json_extract(payload_json, '$.registered_at'), '') = registered_at),
		CHECK(COALESCE(json_extract(payload_json, '$.expires_at'), '') = expires_at),
		CHECK(COALESCE(json_extract(payload_json, '$.exact_run_workspace_bound'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.signature_verified'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.clock_validity_verified'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.durable_replay_guard_present'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.atomic_consumption_present'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.capability_issued'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.capability_consumed'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.start_blocked'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.metadata_only'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.path_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.command_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.argv_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.environment_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.input_body_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.process_starter_present'), 1) = 0),
		CHECK(json_type(payload_json, '$.authority') = 'object')
	);`,
	`CREATE INDEX idx_analyzer_start_requests_run_registered
		ON analyzer_start_requests(run_id, registered_at, id);`,
	`CREATE TABLE analyzer_start_intents (
		id TEXT PRIMARY KEY,
		request_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		generation INTEGER NOT NULL,
		state TEXT NOT NULL,
		previous_intent_fingerprint TEXT NOT NULL,
		fingerprint TEXT NOT NULL UNIQUE,
		event_sequence INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		transitioned_at TEXT NOT NULL,
		FOREIGN KEY(request_id) REFERENCES analyzer_start_requests(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		UNIQUE(request_id, generation),
		UNIQUE(run_id, event_sequence),
		CHECK(generation > 0),
		CHECK(state IN ('disabled', 'prepared', 'consumed', 'expired', 'cancelled',
			'fake_succeeded', 'fake_failed', 'recovery_required')),
		CHECK(event_sequence > 0),
		CHECK(previous_intent_fingerprint = '' OR
			(length(previous_intent_fingerprint) = 64 AND
			 previous_intent_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json)),
		CHECK(COALESCE(json_extract(payload_json, '$.protocol_version'), '') =
			'analyzer_start_intent.v1'),
		CHECK(COALESCE(json_extract(payload_json, '$.id'), '') = id),
		CHECK(COALESCE(json_extract(payload_json, '$.request_id'), '') = request_id),
		CHECK(COALESCE(json_extract(payload_json, '$.run_id'), '') = run_id),
		CHECK(COALESCE(json_extract(payload_json, '$.workspace_id'), '') = workspace_id),
		CHECK(COALESCE(json_extract(payload_json, '$.generation'), 0) = generation),
		CHECK(COALESCE(json_extract(payload_json, '$.state'), '') = state),
		CHECK(COALESCE(json_extract(payload_json, '$.previous_intent_fingerprint'), '') =
			previous_intent_fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.fingerprint'), '') = fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.transitioned_at'), '') = transitioned_at),
		CHECK(COALESCE(json_extract(payload_json, '$.write_ahead_recorded'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.atomic_consumption_enforced'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.process_start_authorized'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.process_observed'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.network_authorized'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.artifact_commit_authorized'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.raw_output_included'), 1) = 0),
		CHECK(json_type(payload_json, '$.authority') = 'object')
	);`,
	`CREATE INDEX idx_analyzer_start_intents_request_generation
		ON analyzer_start_intents(request_id, generation DESC);`,
	`CREATE INDEX idx_analyzer_start_intents_recovery
		ON analyzer_start_intents(state, transitioned_at, request_id);`,
	`CREATE TABLE analyzer_start_lifecycle_receipts (
		id TEXT PRIMARY KEY,
		request_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		generation INTEGER NOT NULL,
		state TEXT NOT NULL,
		intent_fingerprint TEXT NOT NULL UNIQUE,
		previous_receipt_fingerprint TEXT NOT NULL,
		fingerprint TEXT NOT NULL UNIQUE,
		event_sequence INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		recorded_at TEXT NOT NULL,
		FOREIGN KEY(request_id) REFERENCES analyzer_start_requests(id) ON DELETE RESTRICT,
		FOREIGN KEY(intent_fingerprint) REFERENCES analyzer_start_intents(fingerprint) ON DELETE RESTRICT,
		UNIQUE(request_id, generation),
		UNIQUE(run_id, event_sequence),
		CHECK(generation > 0),
		CHECK(state IN ('disabled', 'prepared', 'consumed', 'expired', 'cancelled',
			'fake_succeeded', 'fake_failed', 'recovery_required')),
		CHECK(event_sequence > 0),
		CHECK(previous_receipt_fingerprint = '' OR
			(length(previous_receipt_fingerprint) = 64 AND
			 previous_receipt_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json)),
		CHECK(COALESCE(json_extract(payload_json, '$.protocol_version'), '') =
			'analyzer_start_lifecycle_receipt.v1'),
		CHECK(COALESCE(json_extract(payload_json, '$.id'), '') = id),
		CHECK(COALESCE(json_extract(payload_json, '$.request_id'), '') = request_id),
		CHECK(COALESCE(json_extract(payload_json, '$.run_id'), '') = run_id),
		CHECK(COALESCE(json_extract(payload_json, '$.workspace_id'), '') = workspace_id),
		CHECK(COALESCE(json_extract(payload_json, '$.generation'), 0) = generation),
		CHECK(COALESCE(json_extract(payload_json, '$.state'), '') = state),
		CHECK(COALESCE(json_extract(payload_json, '$.intent_fingerprint'), '') =
			intent_fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.previous_receipt_fingerprint'), '') =
			previous_receipt_fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.fingerprint'), '') = fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.recorded_at'), '') = recorded_at),
		CHECK(COALESCE(json_extract(payload_json, '$.redacted'), 0) = 1),
		CHECK(COALESCE(json_extract(payload_json, '$.raw_request_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.raw_output_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.command_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.process_handle_included'), 1) = 0),
		CHECK(COALESCE(json_extract(payload_json, '$.artifact_committed'), 1) = 0),
		CHECK(json_type(payload_json, '$.authority') = 'object')
	);`,
	`CREATE INDEX idx_analyzer_start_receipts_request_generation
		ON analyzer_start_lifecycle_receipts(request_id, generation);`,
	`CREATE TRIGGER trg_analyzer_start_request_insert
		BEFORE INSERT ON analyzer_start_requests
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN workspaces workspace ON workspace.id = NEW.workspace_id
			JOIN run_events event ON event.run_id = NEW.run_id
				AND event.sequence = NEW.event_sequence
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND mission.workspace_id = NEW.workspace_id
				AND run.status NOT IN ('completed', 'failed', 'cancelled')
				AND event.mission_id = NEW.mission_id
				AND event.type = 'analyzer.start_request_registered'
				AND event.subject_id = NEW.id
				AND (SELECT count(*) FROM json_each(NEW.payload_json)) = 36
				AND (SELECT count(*) FROM json_each(NEW.payload_json, '$.authority')) = 11
				AND NOT EXISTS (SELECT 1 FROM json_each(NEW.payload_json, '$.authority')
					WHERE value <> 0)
				AND COALESCE(json_extract(event.payload_json, '$.request_fingerprint'), '') = NEW.fingerprint
				AND COALESCE(json_extract(event.payload_json, '$.adapter'), '') = NEW.adapter
				AND COALESCE(json_extract(event.payload_json, '$.redacted'), 0) = 1
				AND julianday(NEW.registered_at) >=
					julianday(json_extract(NEW.payload_json, '$.issued_at'))
				AND julianday(NEW.registered_at) < julianday(NEW.expires_at)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Analyzer start request binding is invalid');
		END;`,
	`CREATE TRIGGER trg_analyzer_start_intent_insert
		BEFORE INSERT ON analyzer_start_intents
		WHEN NOT EXISTS (
			SELECT 1 FROM analyzer_start_requests request
			JOIN run_events event ON event.run_id = NEW.run_id
				AND event.sequence = NEW.event_sequence
			WHERE request.id = NEW.request_id AND request.run_id = NEW.run_id
				AND request.workspace_id = NEW.workspace_id
				AND COALESCE(json_extract(NEW.payload_json, '$.request_fingerprint'), '') = request.fingerprint
				AND COALESCE(json_extract(NEW.payload_json, '$.nonce_sha256'), '') = request.nonce_sha256
				AND COALESCE(json_extract(NEW.payload_json, '$.adapter'), '') = request.adapter
				AND COALESCE(json_extract(NEW.payload_json, '$.expires_at'), '') = request.expires_at
				AND (SELECT count(*) FROM json_each(NEW.payload_json)) = 28
				AND (SELECT count(*) FROM json_each(NEW.payload_json, '$.authority')) = 11
				AND NOT EXISTS (SELECT 1 FROM json_each(NEW.payload_json, '$.authority')
					WHERE value <> 0)
				AND event.type = 'analyzer.start_intent_recorded'
				AND event.subject_id = NEW.id
				AND COALESCE(json_extract(event.payload_json, '$.intent_fingerprint'), '') = NEW.fingerprint
				AND COALESCE(json_extract(event.payload_json, '$.generation'), 0) = NEW.generation
				AND COALESCE(json_extract(event.payload_json, '$.state'), '') = NEW.state
				AND COALESCE(json_extract(event.payload_json, '$.redacted'), 0) = 1
				AND (
					(NEW.generation = 1 AND NEW.previous_intent_fingerprint = ''
						AND NOT EXISTS (SELECT 1 FROM analyzer_start_intents prior
							WHERE prior.request_id = NEW.request_id)
						AND ((request.adapter = 'disabled' AND NEW.state = 'disabled')
							OR (request.adapter = 'fake' AND NEW.state = 'prepared')))
					OR
					(NEW.generation > 1 AND EXISTS (
						SELECT 1 FROM analyzer_start_intents previous
						WHERE previous.request_id = NEW.request_id
							AND previous.generation = NEW.generation - 1
							AND previous.fingerprint = NEW.previous_intent_fingerprint
							AND NOT EXISTS (SELECT 1 FROM analyzer_start_intents later
								WHERE later.request_id = NEW.request_id
									AND later.generation >= NEW.generation)
							AND julianday(NEW.transitioned_at) >= julianday(previous.transitioned_at)
							AND (
								(previous.state = 'prepared' AND NEW.state = 'consumed'
									AND request.adapter = 'fake'
									AND julianday(NEW.transitioned_at) < julianday(request.expires_at))
								OR (previous.state = 'prepared' AND NEW.state = 'expired'
									AND julianday(NEW.transitioned_at) >= julianday(request.expires_at))
								OR (previous.state = 'prepared' AND NEW.state = 'cancelled')
								OR (previous.state = 'consumed' AND NEW.state IN
									('fake_succeeded', 'fake_failed', 'recovery_required', 'cancelled'))
							)
					))
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Analyzer start intent binding or transition is invalid');
		END;`,
	`CREATE TRIGGER trg_analyzer_start_receipt_insert
		BEFORE INSERT ON analyzer_start_lifecycle_receipts
		WHEN NOT EXISTS (
			SELECT 1 FROM analyzer_start_intents intent
			JOIN run_events event ON event.run_id = NEW.run_id
				AND event.sequence = NEW.event_sequence
			WHERE intent.request_id = NEW.request_id
				AND intent.run_id = NEW.run_id AND intent.workspace_id = NEW.workspace_id
				AND intent.generation = NEW.generation AND intent.state = NEW.state
				AND intent.fingerprint = NEW.intent_fingerprint
				AND (SELECT count(*) FROM json_each(NEW.payload_json)) = 22
				AND (SELECT count(*) FROM json_each(NEW.payload_json, '$.authority')) = 11
				AND NOT EXISTS (SELECT 1 FROM json_each(NEW.payload_json, '$.authority')
					WHERE value <> 0)
				AND event.type = 'analyzer.start_lifecycle_receipt_recorded'
				AND event.subject_id = NEW.id
				AND COALESCE(json_extract(event.payload_json, '$.receipt_fingerprint'), '') = NEW.fingerprint
				AND COALESCE(json_extract(event.payload_json, '$.intent_fingerprint'), '') = NEW.intent_fingerprint
				AND COALESCE(json_extract(event.payload_json, '$.generation'), 0) = NEW.generation
				AND COALESCE(json_extract(event.payload_json, '$.state'), '') = NEW.state
				AND COALESCE(json_extract(event.payload_json, '$.redacted'), 0) = 1
				AND ((NEW.generation = 1 AND NEW.previous_receipt_fingerprint = ''
					AND NOT EXISTS (SELECT 1 FROM analyzer_start_lifecycle_receipts prior
						WHERE prior.request_id = NEW.request_id))
					OR (NEW.generation > 1 AND EXISTS (
						SELECT 1 FROM analyzer_start_lifecycle_receipts previous
						WHERE previous.request_id = NEW.request_id
							AND previous.generation = NEW.generation - 1
							AND previous.fingerprint = NEW.previous_receipt_fingerprint)))
		)
		BEGIN
			SELECT RAISE(ABORT, 'Analyzer start lifecycle receipt binding is invalid');
		END;`,
	`CREATE TRIGGER trg_analyzer_start_request_update_immutable
		BEFORE UPDATE ON analyzer_start_requests BEGIN
			SELECT RAISE(ABORT, 'Analyzer start request cannot be updated');
		END;`,
	`CREATE TRIGGER trg_analyzer_start_request_delete_immutable
		BEFORE DELETE ON analyzer_start_requests BEGIN
			SELECT RAISE(ABORT, 'Analyzer start request cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_analyzer_start_intent_update_immutable
		BEFORE UPDATE ON analyzer_start_intents BEGIN
			SELECT RAISE(ABORT, 'Analyzer start intent cannot be updated');
		END;`,
	`CREATE TRIGGER trg_analyzer_start_intent_delete_immutable
		BEFORE DELETE ON analyzer_start_intents BEGIN
			SELECT RAISE(ABORT, 'Analyzer start intent cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_analyzer_start_receipt_update_immutable
		BEFORE UPDATE ON analyzer_start_lifecycle_receipts BEGIN
			SELECT RAISE(ABORT, 'Analyzer start lifecycle receipt cannot be updated');
		END;`,
	`CREATE TRIGGER trg_analyzer_start_receipt_delete_immutable
		BEFORE DELETE ON analyzer_start_lifecycle_receipts BEGIN
			SELECT RAISE(ABORT, 'Analyzer start lifecycle receipt cannot be deleted');
		END;`,
}
