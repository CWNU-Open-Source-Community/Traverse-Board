package store

// The existing steering operation requires an already queued message and is
// Run-scoped. This small intent binding covers the earlier file preparation
// boundary and survives successor creation and process restarts.
var threadMessageIntentStatements = []string{
	`CREATE TABLE thread_message_intents (
		operation_key_digest TEXT PRIMARY KEY,
		thread_id TEXT NOT NULL REFERENCES threads(id),
		request_fingerprint TEXT NOT NULL,
		files_json TEXT NOT NULL CHECK(json_valid(files_json) AND json_type(files_json) = 'array' AND json_array_length(files_json) <= 4),
		run_id TEXT REFERENCES runs(id),
		message_id TEXT UNIQUE REFERENCES operator_steering_messages(id),
		rejected INTEGER NOT NULL DEFAULT 0 CHECK(rejected IN (0, 1)),
		created_at TEXT NOT NULL,
		CHECK(length(operation_key_digest) = 64 AND length(request_fingerprint) = 64),
		CHECK(message_id IS NULL OR (run_id IS NOT NULL AND rejected = 0))
	);`,
	`CREATE INDEX idx_thread_message_intents_thread ON thread_message_intents(thread_id);`,
	`CREATE TRIGGER trg_thread_message_intent_immutable BEFORE UPDATE ON thread_message_intents
		WHEN NEW.operation_key_digest <> OLD.operation_key_digest OR NEW.thread_id <> OLD.thread_id
		OR NEW.request_fingerprint <> OLD.request_fingerprint OR NEW.files_json <> OLD.files_json
		OR NEW.created_at <> OLD.created_at
		OR (OLD.run_id IS NOT NULL AND NEW.run_id IS NOT OLD.run_id)
		OR (OLD.message_id IS NOT NULL AND NEW.message_id IS NOT OLD.message_id)
		OR NEW.rejected < OLD.rejected
		BEGIN SELECT RAISE(ABORT, 'Thread message intent binding is immutable'); END;`,
	`CREATE TRIGGER trg_thread_message_intent_binding BEFORE UPDATE ON thread_message_intents
		WHEN (NEW.run_id IS NOT NULL AND NOT EXISTS
			(SELECT 1 FROM thread_runs WHERE thread_id = NEW.thread_id AND run_id = NEW.run_id))
		OR (NEW.message_id IS NOT NULL AND NOT EXISTS
			(SELECT 1 FROM operator_steering_messages WHERE id = NEW.message_id AND run_id = NEW.run_id))
		BEGIN SELECT RAISE(ABORT, 'Thread message intent Run or message binding is invalid'); END;`,
}
