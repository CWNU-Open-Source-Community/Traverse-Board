package store

// These ledgers are private model protocol state, never conversation or tool
// authority. Existing tool rows and their identities are not rebuilt.
var supervisorProviderReplayStatements = []string{
	`CREATE TABLE run_supervisor_provider_replay (
		run_id TEXT NOT NULL, turn INTEGER NOT NULL CHECK(turn > 0),
		attempt_id TEXT NOT NULL, round INTEGER NOT NULL CHECK(round BETWEEN 1 AND 4),
		model_attempt INTEGER NOT NULL CHECK(model_attempt > 0),
		provider TEXT NOT NULL CHECK(length(provider) > 0),
		model TEXT NOT NULL CHECK(length(model) > 0),
		replay_blob BLOB NOT NULL CHECK(length(replay_blob) BETWEEN 1 AND 8388608),
		replay_sha256 TEXT NOT NULL CHECK(length(replay_sha256) = 64),
		created_at TEXT NOT NULL,
		PRIMARY KEY(run_id, turn, attempt_id, round),
		UNIQUE(run_id, turn, attempt_id, model_attempt),
		FOREIGN KEY(run_id, turn, attempt_id, round)
			REFERENCES run_supervisor_tool_rounds(run_id, turn, attempt_id, round) ON DELETE CASCADE
	);`,
	`CREATE TRIGGER trg_supervisor_provider_replay_source BEFORE INSERT ON run_supervisor_provider_replay
	WHEN NOT EXISTS (SELECT 1 FROM run_supervisor_tool_rounds r JOIN run_events e ON e.run_id=r.run_id
		WHERE r.run_id=NEW.run_id AND r.turn=NEW.turn AND r.attempt_id=NEW.attempt_id
		AND r.round=NEW.round AND r.model_attempt=NEW.model_attempt
		AND e.type='model.completed' AND e.source='model_gateway'
		AND e.subject_id=NEW.attempt_id||'/model/'||NEW.model_attempt
		AND json_extract(e.payload_json,'$.provider')=NEW.provider
		AND json_extract(e.payload_json,'$.model')=NEW.model
		AND json_extract(e.payload_json,'$.outcome')='success')
	BEGIN SELECT RAISE(ABORT, 'provider replay requires its successful tool round'); END;`,
	`CREATE TRIGGER trg_supervisor_provider_replay_immutable BEFORE UPDATE ON run_supervisor_provider_replay
	BEGIN SELECT RAISE(ABORT, 'provider replay is immutable'); END;`,
	`CREATE TABLE run_supervisor_context_recoveries (
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		turn INTEGER NOT NULL CHECK(turn > 0), attempt_id TEXT NOT NULL,
		tool_round INTEGER NOT NULL CHECK(tool_round BETWEEN 0 AND 4),
		protocol_repair INTEGER NOT NULL CHECK(protocol_repair BETWEEN 0 AND 1),
		model_attempt INTEGER NOT NULL CHECK(model_attempt > 0),
		original_input_tokens INTEGER NOT NULL CHECK(original_input_tokens > 0),
		provider TEXT NOT NULL CHECK(length(provider) > 0),
		model TEXT NOT NULL CHECK(length(model) > 0), created_at TEXT NOT NULL,
		PRIMARY KEY(run_id, turn, attempt_id, tool_round, protocol_repair)
	);`,
	`CREATE TRIGGER trg_supervisor_context_recovery_source BEFORE INSERT ON run_supervisor_context_recoveries
	WHEN NOT EXISTS (SELECT 1 FROM run_events e WHERE e.run_id=NEW.run_id
		AND e.type='model.failed' AND e.source='model_gateway'
		AND e.subject_id=NEW.attempt_id||'/model/'||NEW.model_attempt
		AND json_extract(e.payload_json,'$.turn')=NEW.turn
		AND json_extract(e.payload_json,'$.attempt_id')=NEW.attempt_id
		AND json_extract(e.payload_json,'$.tool_round')=NEW.tool_round
		AND json_extract(e.payload_json,'$.protocol_repair')=NEW.protocol_repair
		AND json_extract(e.payload_json,'$.provider')=NEW.provider
		AND json_extract(e.payload_json,'$.model')=NEW.model
		AND json_extract(e.payload_json,'$.outcome')='permanent'
		AND json_extract(e.payload_json,'$.failure_reason')='context_limit')
	OR NOT EXISTS (SELECT 1 FROM run_events e WHERE e.run_id=NEW.run_id
		AND e.type='model.started' AND e.source='model_gateway'
		AND e.subject_id=NEW.attempt_id||'/model/'||NEW.model_attempt
		AND json_extract(e.payload_json,'$.input_estimate')=NEW.original_input_tokens)
	BEGIN SELECT RAISE(ABORT, 'context recovery requires a typed context limit failure'); END;`,
	`CREATE TRIGGER trg_supervisor_context_recovery_immutable BEFORE UPDATE ON run_supervisor_context_recoveries
	BEGIN SELECT RAISE(ABORT, 'context recovery claim is immutable'); END;`,
}
