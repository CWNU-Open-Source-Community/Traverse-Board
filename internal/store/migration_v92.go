package store

var browserRuntimeLifecycleStatements = []string{
	`CREATE TABLE browser_runtime_checkpoints (
		id TEXT PRIMARY KEY,
		runtime_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		attempt_id TEXT NOT NULL,
		attempt_fingerprint TEXT NOT NULL,
		authorization_fingerprint TEXT NOT NULL,
		process_start_spec_fingerprint TEXT NOT NULL,
		profile_ownership_fingerprint TEXT NOT NULL,
		profile_lease_fingerprint TEXT NOT NULL,
		released_profile_fingerprint TEXT NOT NULL,
		previous_checkpoint_fingerprint TEXT NOT NULL,
		generation INTEGER NOT NULL,
		stage TEXT NOT NULL,
		fingerprint TEXT NOT NULL UNIQUE,
		event_sequence INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		recorded_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(attempt_id) REFERENCES browser_launch_attempts(id) ON DELETE RESTRICT,
		UNIQUE(runtime_id, generation),
		UNIQUE(run_id, event_sequence),
		CHECK(generation > 0),
		CHECK(stage IN ('running', 'cdp_closed', 'process_quiescent',
			'network_released', 'profile_released', 'completed', 'failed')),
		CHECK(event_sequence > 0),
		CHECK(length(attempt_fingerprint) = 64
			AND attempt_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(authorization_fingerprint) = 64
			AND authorization_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(process_start_spec_fingerprint) = 64
			AND process_start_spec_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(profile_ownership_fingerprint) = 64
			AND profile_ownership_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(profile_lease_fingerprint) = 64
			AND profile_lease_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(released_profile_fingerprint = '' OR
			(length(released_profile_fingerprint) = 64
				AND released_profile_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(previous_checkpoint_fingerprint = '' OR
			(length(previous_checkpoint_fingerprint) = 64
				AND previous_checkpoint_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json)),
		CHECK(json_extract(payload_json, '$.protocol_version') =
			'browser_runtime_checkpoint.v1'),
		CHECK(json_extract(payload_json, '$.id') = id),
		CHECK(json_extract(payload_json, '$.runtime_id') = runtime_id),
		CHECK(json_extract(payload_json, '$.run_id') = run_id),
		CHECK(json_extract(payload_json, '$.attempt_id') = attempt_id),
		CHECK(json_extract(payload_json, '$.attempt_fingerprint') = attempt_fingerprint),
		CHECK(json_extract(payload_json, '$.authorization_fingerprint') =
			authorization_fingerprint),
		CHECK(json_extract(payload_json, '$.process_start_spec_fingerprint') =
			process_start_spec_fingerprint),
		CHECK(json_extract(payload_json, '$.profile_ownership_fingerprint') =
			profile_ownership_fingerprint),
		CHECK(json_extract(payload_json, '$.profile_lease_fingerprint') =
			profile_lease_fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.released_profile_fingerprint'), '') =
			released_profile_fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.previous_checkpoint_fingerprint'), '') =
			previous_checkpoint_fingerprint),
		CHECK(json_extract(payload_json, '$.generation') = generation),
		CHECK(json_extract(payload_json, '$.stage') = stage),
		CHECK(json_extract(payload_json, '$.fingerprint') = fingerprint),
		CHECK(json_extract(payload_json, '$.raw_output_included') = 0),
		CHECK(json_extract(payload_json, '$.personal_profile_used') = 0),
		CHECK(json_extract(payload_json, '$.full_cdp_used') = 0)
	);`,
	`CREATE INDEX idx_browser_runtime_checkpoints_runtime_generation
		ON browser_runtime_checkpoints(runtime_id, generation DESC);`,
	`CREATE INDEX idx_browser_runtime_checkpoints_run_recorded
		ON browser_runtime_checkpoints(run_id, recorded_at, id);`,
	`CREATE TABLE browser_runtime_receipts (
		id TEXT PRIMARY KEY,
		runtime_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		attempt_fingerprint TEXT NOT NULL,
		authorization_fingerprint TEXT NOT NULL,
		final_checkpoint_fingerprint TEXT NOT NULL UNIQUE,
		process_exit_fingerprint TEXT NOT NULL,
		released_profile_fingerprint TEXT NOT NULL,
		succeeded INTEGER NOT NULL,
		recovery_required INTEGER NOT NULL,
		failure_code TEXT NOT NULL,
		fingerprint TEXT NOT NULL UNIQUE,
		event_sequence INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		started_at TEXT NOT NULL,
		completed_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(final_checkpoint_fingerprint)
			REFERENCES browser_runtime_checkpoints(fingerprint) ON DELETE RESTRICT,
		UNIQUE(run_id, event_sequence),
		CHECK(succeeded IN (0, 1) AND recovery_required IN (0, 1)),
		CHECK(length(attempt_fingerprint) = 64
			AND attempt_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(authorization_fingerprint) = 64
			AND authorization_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(final_checkpoint_fingerprint) = 64
			AND final_checkpoint_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(process_exit_fingerprint = '' OR
			(length(process_exit_fingerprint) = 64
				AND process_exit_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(released_profile_fingerprint = '' OR
			(length(released_profile_fingerprint) = 64
				AND released_profile_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(event_sequence > 0),
		CHECK(json_valid(payload_json)),
		CHECK(json_extract(payload_json, '$.protocol_version') =
			'browser_runtime_receipt.v1'),
		CHECK(json_extract(payload_json, '$.id') = id),
		CHECK(json_extract(payload_json, '$.runtime_id') = runtime_id),
		CHECK(json_extract(payload_json, '$.run_id') = run_id),
		CHECK(json_extract(payload_json, '$.attempt_fingerprint') = attempt_fingerprint),
		CHECK(json_extract(payload_json, '$.authorization_fingerprint') =
			authorization_fingerprint),
		CHECK(json_extract(payload_json, '$.final_checkpoint_fingerprint') =
			final_checkpoint_fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.process_exit_fingerprint'), '') =
			process_exit_fingerprint),
		CHECK(COALESCE(json_extract(payload_json, '$.released_profile_fingerprint'), '') =
			released_profile_fingerprint),
		CHECK(json_extract(payload_json, '$.succeeded') = succeeded),
		CHECK(json_extract(payload_json, '$.recovery_required') = recovery_required),
		CHECK(COALESCE(json_extract(payload_json, '$.failure_code'), '') = failure_code),
		CHECK(json_extract(payload_json, '$.fingerprint') = fingerprint),
		CHECK(json_extract(payload_json, '$.raw_output_included') = 0),
		CHECK(json_extract(payload_json, '$.page_content_included') = 0),
		CHECK(json_extract(payload_json, '$.screenshot_included') = 0),
		CHECK(json_extract(payload_json, '$.personal_profile_used') = 0),
		CHECK(json_extract(payload_json, '$.full_cdp_used') = 0)
	);`,
	`CREATE INDEX idx_browser_runtime_receipts_run_completed
		ON browser_runtime_receipts(run_id, completed_at, id);`,
	`CREATE TRIGGER trg_browser_runtime_checkpoint_insert
		BEFORE INSERT ON browser_runtime_checkpoints
		WHEN NOT EXISTS (
			SELECT 1 FROM browser_launch_attempts attempt
			JOIN runs run ON run.id = attempt.run_id
			JOIN run_events event ON event.run_id = NEW.run_id
				AND event.sequence = NEW.event_sequence
			WHERE attempt.id = NEW.attempt_id AND attempt.run_id = NEW.run_id
				AND attempt.fingerprint = NEW.attempt_fingerprint
				AND event.type = 'browser.runtime_checkpoint_recorded'
				AND event.subject_id = NEW.id
				AND json_extract(event.payload_json, '$.runtime_id') = NEW.runtime_id
				AND json_extract(event.payload_json, '$.checkpoint_fingerprint') = NEW.fingerprint
				AND json_extract(event.payload_json, '$.generation') = NEW.generation
				AND json_extract(event.payload_json, '$.stage') = NEW.stage
				AND json_extract(event.payload_json, '$.redacted') = 1
				AND (
					(NEW.generation = 1 AND NEW.previous_checkpoint_fingerprint = ''
						AND NEW.stage = 'running' AND NOT EXISTS (
							SELECT 1 FROM browser_runtime_checkpoints existing
							WHERE existing.runtime_id = NEW.runtime_id))
					OR
					(NEW.generation > 1 AND EXISTS (
						SELECT 1 FROM browser_runtime_checkpoints previous
						WHERE previous.runtime_id = NEW.runtime_id
							AND previous.run_id = NEW.run_id
							AND previous.attempt_id = NEW.attempt_id
							AND previous.authorization_fingerprint =
								NEW.authorization_fingerprint
							AND previous.generation = NEW.generation - 1
							AND previous.fingerprint = NEW.previous_checkpoint_fingerprint
							AND julianday(NEW.recorded_at) >= julianday(previous.recorded_at)
							AND json_extract(NEW.payload_json, '$.restricted_cdp_closed') >=
								json_extract(previous.payload_json, '$.restricted_cdp_closed')
							AND json_extract(NEW.payload_json, '$.process_tree_quiescent') >=
								json_extract(previous.payload_json, '$.process_tree_quiescent')
							AND json_extract(NEW.payload_json, '$.network_cleanup_verified') >=
								json_extract(previous.payload_json, '$.network_cleanup_verified')
							AND json_extract(NEW.payload_json, '$.profile_released') >=
								json_extract(previous.payload_json, '$.profile_released')
							AND json_extract(NEW.payload_json, '$.profile_cleaned') >=
								json_extract(previous.payload_json, '$.profile_cleaned')
					))
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Browser runtime checkpoint binding or ancestry is invalid');
		END;`,
	`CREATE TRIGGER trg_browser_runtime_receipt_insert
		BEFORE INSERT ON browser_runtime_receipts
		WHEN NOT EXISTS (
			SELECT 1 FROM browser_runtime_checkpoints checkpoint
			JOIN run_events event ON event.run_id = NEW.run_id
				AND event.sequence = NEW.event_sequence
			WHERE checkpoint.runtime_id = NEW.runtime_id
				AND checkpoint.run_id = NEW.run_id
				AND checkpoint.attempt_fingerprint = NEW.attempt_fingerprint
				AND checkpoint.authorization_fingerprint = NEW.authorization_fingerprint
				AND checkpoint.fingerprint = NEW.final_checkpoint_fingerprint
				AND checkpoint.stage = CASE NEW.succeeded
					WHEN 1 THEN 'completed' ELSE 'failed' END
				AND event.type = 'browser.runtime_receipt_recorded'
				AND event.subject_id = NEW.id
				AND json_extract(event.payload_json, '$.runtime_id') = NEW.runtime_id
				AND json_extract(event.payload_json, '$.receipt_fingerprint') = NEW.fingerprint
				AND json_extract(event.payload_json, '$.final_checkpoint_fingerprint') =
					NEW.final_checkpoint_fingerprint
				AND json_extract(event.payload_json, '$.succeeded') = NEW.succeeded
				AND json_extract(event.payload_json, '$.recovery_required') =
					NEW.recovery_required
				AND json_extract(event.payload_json, '$.redacted') = 1
		)
		BEGIN
			SELECT RAISE(ABORT, 'Browser runtime receipt binding is invalid');
		END;`,
	`CREATE TRIGGER trg_browser_runtime_checkpoint_update_immutable
		BEFORE UPDATE ON browser_runtime_checkpoints BEGIN
			SELECT RAISE(ABORT, 'Browser runtime checkpoint cannot be updated');
		END;`,
	`CREATE TRIGGER trg_browser_runtime_checkpoint_delete_immutable
		BEFORE DELETE ON browser_runtime_checkpoints BEGIN
			SELECT RAISE(ABORT, 'Browser runtime checkpoint cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_browser_runtime_receipt_update_immutable
		BEFORE UPDATE ON browser_runtime_receipts BEGIN
			SELECT RAISE(ABORT, 'Browser runtime receipt cannot be updated');
		END;`,
	`CREATE TRIGGER trg_browser_runtime_receipt_delete_immutable
		BEFORE DELETE ON browser_runtime_receipts BEGIN
			SELECT RAISE(ABORT, 'Browser runtime receipt cannot be deleted');
		END;`,
}
