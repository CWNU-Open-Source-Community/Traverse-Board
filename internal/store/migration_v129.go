package store

// threadSuccessionStatements adds the stable user-facing Thread identity while
// preserving every existing Mission, Run, Session, message, and audit record.
// Existing data is conservatively projected as one Thread per Run. Only future
// continuation operations append successor Runs to a Thread.
var threadSuccessionStatements = []string{
	// Runs created before Session projection was introduced may legitimately be
	// unbound. Give each one a deterministic recovery Session before Thread
	// bindings become NOT NULL; existing Run/Session bindings are untouched.
	`INSERT INTO sessions (id, workspace_id, title, route, status, created_at, updated_at)
	 SELECT 'thread-session-' || run.id, COALESCE(mission.workspace_id, ''),
		COALESCE(NULLIF(substr(mission.goal, 1, 4096), ''), 'Recovered Run'),
		COALESCE(NULLIF(json_extract(run.config_json, '$.model_route'), ''), 'learn'),
		'active', run.created_at, run.updated_at
	 FROM runs run JOIN missions mission ON mission.id = run.mission_id
	 WHERE run.session_id IS NULL OR run.session_id = '';`,
	`UPDATE runs SET session_id = 'thread-session-' || id
	 WHERE session_id IS NULL OR session_id = '';`,
	`CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		workspace_id TEXT NOT NULL DEFAULT '',
		mission_id TEXT NOT NULL,
		title TEXT NOT NULL,
		status TEXT NOT NULL,
		active_run_id TEXT,
		last_run_id TEXT,
		version INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		archived_at TEXT,
		deleted_at TEXT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(active_run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(last_run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'thread.v1'),
		CHECK(status IN ('active','archived','deleted')),
		CHECK(version >= 0),
		CHECK(length(title) >= 1),
		CHECK(julianday(created_at) IS NOT NULL),
		CHECK(julianday(updated_at) IS NOT NULL),
		CHECK(archived_at IS NULL OR julianday(archived_at) IS NOT NULL),
		CHECK(deleted_at IS NULL OR julianday(deleted_at) IS NOT NULL),
		CHECK(status <> 'archived' OR archived_at IS NOT NULL),
		CHECK(status <> 'deleted' OR deleted_at IS NOT NULL)
	);`,
	`CREATE TABLE thread_runs (
		thread_id TEXT NOT NULL,
		run_id TEXT NOT NULL UNIQUE,
		session_id TEXT NOT NULL UNIQUE,
		ordinal INTEGER NOT NULL,
		predecessor_run_id TEXT,
		created_at TEXT NOT NULL,
		PRIMARY KEY(thread_id, ordinal),
		FOREIGN KEY(thread_id) REFERENCES threads(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(predecessor_run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(ordinal > 0),
		CHECK((ordinal = 1 AND predecessor_run_id IS NULL) OR
			(ordinal > 1 AND predecessor_run_id IS NOT NULL)),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE TABLE thread_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		thread_id TEXT NOT NULL,
		run_id TEXT,
		type TEXT NOT NULL,
		source TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(thread_id) REFERENCES threads(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(json_valid(payload_json)),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE TABLE thread_lifecycle_operations (
		key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		thread_id TEXT NOT NULL,
		action TEXT NOT NULL,
		result_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(thread_id) REFERENCES threads(id) ON DELETE RESTRICT,
		CHECK(length(key_digest) = 64),
		CHECK(length(request_fingerprint) = 64),
		CHECK(action IN ('archive','restore','delete')),
		CHECK(json_valid(result_json)),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`INSERT INTO threads
		(id, protocol_version, workspace_id, mission_id, title, status,
		 active_run_id, last_run_id, version, created_at, updated_at)
	SELECT 'thread-' || run.id, 'thread.v1', COALESCE(mission.workspace_id, ''),
		mission.id, mission.goal, 'active',
		CASE WHEN run.status IN ('completed','failed','cancelled') THEN NULL ELSE run.id END,
		run.id, 1, run.created_at, run.updated_at
	FROM runs run JOIN missions mission ON mission.id = run.mission_id;`,
	`INSERT INTO thread_runs
		(thread_id, run_id, session_id, ordinal, predecessor_run_id, created_at)
	SELECT 'thread-' || run.id, run.id, run.session_id, 1, NULL, run.created_at
	FROM runs run;`,
	`INSERT INTO thread_events (thread_id, run_id, type, source, payload_json, created_at)
	SELECT 'thread-' || run.id, run.id, 'thread.created', 'schema_v129',
		json_object('run_id', run.id, 'backfilled', json('true')), run.created_at
	FROM runs run;`,
	`CREATE INDEX idx_threads_created_at ON threads(created_at DESC, id DESC);`,
	`CREATE INDEX idx_threads_status_updated_at ON threads(status, updated_at DESC);`,
	`CREATE UNIQUE INDEX idx_threads_active_run_unique ON threads(active_run_id)
		WHERE active_run_id IS NOT NULL AND active_run_id <> '';`,
	`CREATE INDEX idx_thread_runs_thread_run ON thread_runs(thread_id, ordinal);`,
	`CREATE INDEX idx_thread_events_thread_id ON thread_events(thread_id, id);`,
	`CREATE TRIGGER trg_thread_runs_scope_insert
		BEFORE INSERT ON thread_runs
		WHEN NOT EXISTS (
			SELECT 1 FROM threads thread_record
			JOIN runs run ON run.id = NEW.run_id
			JOIN missions mission ON mission.id = run.mission_id
			JOIN sessions session_record ON session_record.id = NEW.session_id
			WHERE thread_record.id = NEW.thread_id
				AND run.session_id = NEW.session_id
				AND run.mission_id = thread_record.mission_id
				AND COALESCE(mission.workspace_id, '') = thread_record.workspace_id
				AND COALESCE(session_record.workspace_id, '') = thread_record.workspace_id
		)
		BEGIN SELECT RAISE(ABORT, 'thread Run scope is invalid'); END;`,
	`CREATE TRIGGER trg_thread_runs_single_active_insert
		BEFORE INSERT ON thread_runs
		WHEN (SELECT status FROM runs WHERE id = NEW.run_id)
			NOT IN ('completed','failed','cancelled')
		AND EXISTS (
			SELECT 1 FROM thread_runs binding
			JOIN runs run ON run.id = binding.run_id
			WHERE binding.thread_id = NEW.thread_id
				AND run.status NOT IN ('completed','failed','cancelled')
		)
		BEGIN SELECT RAISE(ABORT, 'thread already has an active Run'); END;`,
	`CREATE TRIGGER trg_thread_runs_insert_projection
		AFTER INSERT ON thread_runs
		BEGIN
			UPDATE threads SET
				last_run_id = NEW.run_id,
				active_run_id = CASE
					WHEN (SELECT status FROM runs WHERE id = NEW.run_id)
						IN ('completed','failed','cancelled') THEN active_run_id
					ELSE NEW.run_id END,
				version = version + 1,
				updated_at = NEW.created_at
			WHERE id = NEW.thread_id;
		END;`,
	`CREATE TRIGGER trg_thread_runs_update_immutable
		BEFORE UPDATE ON thread_runs
		BEGIN SELECT RAISE(ABORT, 'thread Run bindings are immutable'); END;`,
	`CREATE TRIGGER trg_thread_runs_delete_immutable
		BEFORE DELETE ON thread_runs
		BEGIN SELECT RAISE(ABORT, 'thread Run bindings are immutable'); END;`,
	`CREATE TRIGGER trg_runs_thread_terminal_projection
		AFTER UPDATE OF status ON runs
		WHEN NEW.status IN ('completed','failed','cancelled')
			AND OLD.status NOT IN ('completed','failed','cancelled')
		BEGIN
			UPDATE threads SET active_run_id = NULL, last_run_id = NEW.id,
				version = version + 1, updated_at = NEW.updated_at
			WHERE active_run_id = NEW.id;
			INSERT INTO thread_events (thread_id, run_id, type, source, payload_json, created_at)
			SELECT binding.thread_id, NEW.id, 'thread.run_terminal', 'run_projection',
				json_object('run_id', NEW.id, 'status', NEW.status), NEW.updated_at
			FROM thread_runs binding WHERE binding.run_id = NEW.id;
		END;`,
	`CREATE TRIGGER trg_threads_binding_immutable
		BEFORE UPDATE OF protocol_version, workspace_id, mission_id, created_at ON threads
		BEGIN SELECT RAISE(ABORT, 'thread identity binding is immutable'); END;`,
	`CREATE TRIGGER trg_thread_events_update_immutable
		BEFORE UPDATE ON thread_events
		BEGIN SELECT RAISE(ABORT, 'thread events are immutable'); END;`,
	`CREATE TRIGGER trg_thread_events_delete_immutable
		BEFORE DELETE ON thread_events
		BEGIN SELECT RAISE(ABORT, 'thread events are immutable'); END;`,
	`DROP TRIGGER trg_operator_steering_insert_binding;`,
	`CREATE TRIGGER trg_operator_steering_insert_binding
		BEFORE INSERT ON operator_steering_messages
		WHEN NOT EXISTS (SELECT 1 FROM runs run
			WHERE run.id = NEW.run_id AND run.session_id = NEW.session_id
				AND run.status NOT IN ('completed','failed','cancelled'))
		BEGIN
			SELECT RAISE(ABORT, 'operator steering Run binding is invalid');
		END;`,
}
