package store

// Ordinary answers have no tool round. Keep their successful native candidates
// separate, then bind one candidate to an accepted public session projection in
// the same fenced turn-completion transaction. This migration invents no replay
// state for old history and leaves the v170 tool-round ledger unchanged.
var supervisorAssistantReplayStatements = []string{
	`CREATE TABLE run_supervisor_assistant_replay (
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		turn INTEGER NOT NULL CHECK(turn > 0), attempt_id TEXT NOT NULL CHECK(length(attempt_id) > 0),
		model_attempt INTEGER NOT NULL CHECK(model_attempt > 0),
		tool_round INTEGER NOT NULL CHECK(tool_round BETWEEN 0 AND 4),
		provider TEXT NOT NULL CHECK(length(provider) > 0), model TEXT NOT NULL CHECK(length(model) > 0),
		replay_blob BLOB NOT NULL CHECK(length(replay_blob) BETWEEN 1 AND 1048576),
		replay_sha256 TEXT NOT NULL CHECK(length(replay_sha256)=64 AND replay_sha256 NOT GLOB '*[^0-9a-f]*'),
		created_at TEXT NOT NULL,
		PRIMARY KEY(run_id,turn,attempt_id,model_attempt)
	);`,
	`CREATE TRIGGER trg_supervisor_assistant_replay_source BEFORE INSERT ON run_supervisor_assistant_replay
	WHEN NOT EXISTS (SELECT 1 FROM run_events e WHERE e.run_id=NEW.run_id
		AND e.type='model.completed' AND e.source='model_gateway'
		AND e.subject_id=NEW.attempt_id||'/model/'||NEW.model_attempt
		AND json_extract(e.payload_json,'$.turn')=NEW.turn
		AND json_extract(e.payload_json,'$.attempt_id')=NEW.attempt_id
		AND json_extract(e.payload_json,'$.model_attempt')=NEW.model_attempt
		AND json_extract(e.payload_json,'$.tool_round')=NEW.tool_round
		AND json_extract(e.payload_json,'$.provider')=NEW.provider
		AND json_extract(e.payload_json,'$.model')=NEW.model
		AND json_extract(e.payload_json,'$.outcome')='success'
		AND json_extract(e.payload_json,'$.tool_call_count')=0
		AND COALESCE(json_extract(e.payload_json,'$.purpose'),'')='')
	OR COALESCE((json_valid(CAST(NEW.replay_blob AS TEXT))
		AND json_extract(CAST(NEW.replay_blob AS TEXT),'$.version')=4
		AND json_extract(CAST(NEW.replay_blob AS TEXT),'$.transport')='openai_chat_completions'
		AND json_extract(CAST(NEW.replay_blob AS TEXT),'$.provider')=NEW.provider
		AND json_extract(CAST(NEW.replay_blob AS TEXT),'$.model')=NEW.model
		AND json_type(CAST(NEW.replay_blob AS TEXT),'$.calls')='array'
		AND json_array_length(CAST(NEW.replay_blob AS TEXT),'$.calls')=0),0)=0
	BEGIN SELECT RAISE(ABORT,'ordinary replay requires its successful primary model source'); END;`,
	`CREATE TRIGGER trg_supervisor_assistant_replay_immutable BEFORE UPDATE ON run_supervisor_assistant_replay
	BEGIN SELECT RAISE(ABORT,'ordinary provider replay is immutable'); END;`,
	`CREATE TRIGGER trg_supervisor_assistant_replay_delete BEFORE DELETE ON run_supervisor_assistant_replay
	WHEN EXISTS (SELECT 1 FROM runs WHERE id=OLD.run_id)
	BEGIN SELECT RAISE(ABORT,'ordinary provider replay cannot be removed'); END;`,
	`CREATE TABLE run_supervisor_assistant_replay_bindings (
		session_message_id INTEGER PRIMARY KEY REFERENCES session_messages(id),
		run_id TEXT NOT NULL, turn INTEGER NOT NULL CHECK(turn > 0), attempt_id TEXT NOT NULL,
		model_attempt INTEGER NOT NULL CHECK(model_attempt > 0),
		projected_content_sha256 TEXT NOT NULL CHECK(length(projected_content_sha256)=64 AND projected_content_sha256 NOT GLOB '*[^0-9a-f]*'),
		replay_sha256 TEXT NOT NULL CHECK(length(replay_sha256)=64 AND replay_sha256 NOT GLOB '*[^0-9a-f]*'),
		created_at TEXT NOT NULL, UNIQUE(run_id,turn,attempt_id),
		FOREIGN KEY(run_id,turn,attempt_id,model_attempt)
			REFERENCES run_supervisor_assistant_replay(run_id,turn,attempt_id,model_attempt) ON DELETE CASCADE
	);`,
	`CREATE TRIGGER trg_supervisor_assistant_binding_source BEFORE INSERT ON run_supervisor_assistant_replay_bindings
	WHEN NOT EXISTS (SELECT 1 FROM run_supervisor_assistant_replay p
		JOIN runs r ON r.id=p.run_id JOIN session_messages m ON m.id=NEW.session_message_id
		JOIN run_events e ON e.run_id=p.run_id
		WHERE p.run_id=NEW.run_id AND p.turn=NEW.turn AND p.attempt_id=NEW.attempt_id
		AND p.model_attempt=NEW.model_attempt AND p.replay_sha256=NEW.replay_sha256
		AND m.session_id=r.session_id AND m.role='assistant' AND m.source_kind='model_response'
		AND m.provenance_version='context_provenance.v1' AND m.instruction_authorized=0
		AND m.content_sha256=NEW.projected_content_sha256
		AND e.type='agent.turn_completed' AND e.source='run_supervisor' AND e.subject_id=NEW.attempt_id
		AND json_extract(e.payload_json,'$.turn')=NEW.turn
		AND json_extract(e.payload_json,'$.attempt_id')=NEW.attempt_id
		AND json_extract(e.payload_json,'$.assistant_message_id')=NEW.session_message_id
		AND json_extract(e.payload_json,'$.provider')=p.provider
		AND json_extract(e.payload_json,'$.model')=p.model)
	BEGIN SELECT RAISE(ABORT,'ordinary replay requires its accepted session projection'); END;`,
	`CREATE TRIGGER trg_supervisor_assistant_binding_immutable BEFORE UPDATE ON run_supervisor_assistant_replay_bindings
	BEGIN SELECT RAISE(ABORT,'ordinary replay binding is immutable'); END;`,
	`CREATE TRIGGER trg_supervisor_assistant_binding_delete BEFORE DELETE ON run_supervisor_assistant_replay_bindings
	WHEN EXISTS (SELECT 1 FROM runs WHERE id=OLD.run_id)
	BEGIN SELECT RAISE(ABORT,'ordinary replay binding cannot be removed'); END;`,
}
