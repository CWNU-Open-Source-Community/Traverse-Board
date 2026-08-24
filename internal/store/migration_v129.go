package store

var itemStreamToolIdentityStatements = []string{
	`ALTER TABLE run_supervisor_tool_calls
		ADD COLUMN stream_response_id TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE run_supervisor_tool_calls
		ADD COLUMN stream_item_id TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE run_supervisor_tool_calls
		ADD COLUMN stream_call_id TEXT NOT NULL DEFAULT '';`,
	`CREATE UNIQUE INDEX idx_supervisor_tool_stream_item_identity
		ON run_supervisor_tool_calls(stream_response_id, stream_item_id)
		WHERE stream_response_id <> '' AND stream_item_id <> '';`,
	`CREATE UNIQUE INDEX idx_supervisor_tool_stream_call_identity
		ON run_supervisor_tool_calls(stream_response_id, stream_call_id)
		WHERE stream_response_id <> '' AND stream_call_id <> '';`,
	`CREATE TRIGGER trg_supervisor_tool_stream_identity_insert
		BEFORE INSERT ON run_supervisor_tool_calls
		WHEN NOT (
			(NEW.stream_response_id = '' AND NEW.stream_item_id = '' AND NEW.stream_call_id = '')
			OR
			(NEW.stream_response_id = trim(NEW.stream_response_id)
				AND NEW.stream_item_id = trim(NEW.stream_item_id)
				AND NEW.stream_call_id = trim(NEW.stream_call_id)
				AND length(NEW.stream_response_id) BETWEEN 1 AND 256
				AND length(NEW.stream_item_id) BETWEEN 1 AND 256
				AND length(NEW.stream_call_id) BETWEEN 1 AND 256)
		)
		BEGIN
			SELECT RAISE(ABORT, 'supervisor tool stream identity is incomplete');
		END;`,
	`CREATE TRIGGER trg_supervisor_tool_stream_identity_immutable
		BEFORE UPDATE OF stream_response_id, stream_item_id, stream_call_id
		ON run_supervisor_tool_calls
		WHEN NEW.stream_response_id <> OLD.stream_response_id
			OR NEW.stream_item_id <> OLD.stream_item_id
			OR NEW.stream_call_id <> OLD.stream_call_id
		BEGIN
			SELECT RAISE(ABORT, 'supervisor tool stream identity is immutable');
		END;`,
}
