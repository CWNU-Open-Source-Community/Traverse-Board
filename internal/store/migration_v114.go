package store

var contextContinuityStatements = []string{
	`CREATE TABLE context_memories (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		scope TEXT NOT NULL,
		scope_id TEXT NOT NULL,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		content_sha256 TEXT NOT NULL,
		status TEXT NOT NULL,
		source_kind TEXT NOT NULL,
		source_ref TEXT NOT NULL,
		references_json TEXT NOT NULL,
		retention_until TEXT,
		redacted INTEGER NOT NULL,
		created_by TEXT NOT NULL,
		updated_by TEXT NOT NULL,
		version INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		CHECK(protocol_version = 'context_memory.v1'),
		CHECK(scope IN ('user', 'project')),
		CHECK((scope = 'user' AND scope_id = 'local-user') OR
			(scope = 'project' AND length(trim(scope_id)) BETWEEN 1 AND 256)),
		CHECK(length(CAST(title AS BLOB)) BETWEEN 1 AND 256 AND instr(title, char(0)) = 0),
		CHECK(length(CAST(content AS BLOB)) BETWEEN 1 AND 16384 AND instr(content, char(0)) = 0),
		CHECK(length(content_sha256) = 64 AND content_sha256 = lower(content_sha256)
			AND content_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(status IN ('active', 'disabled')),
		CHECK(source_kind IN ('operator_explicit', 'operator_import_explicit')),
		CHECK(length(CAST(source_ref AS BLOB)) <= 512 AND instr(source_ref, char(0)) = 0),
		CHECK(json_valid(references_json) AND json_type(references_json) = 'array'
			AND json_array_length(references_json) <= 32),
		CHECK(retention_until IS NULL OR julianday(retention_until) IS NOT NULL),
		CHECK(redacted IN (0, 1)),
		CHECK(length(trim(created_by)) BETWEEN 1 AND 256
			AND lower(created_by) NOT IN ('agent', 'assistant', 'llm', 'model', 'repository',
				'repo', 'tool', 'supervisor', 'run_supervisor', 'automatic', 'auto', 'system')),
		CHECK(length(trim(updated_by)) BETWEEN 1 AND 256
			AND lower(updated_by) NOT IN ('agent', 'assistant', 'llm', 'model', 'repository',
				'repo', 'tool', 'supervisor', 'run_supervisor', 'automatic', 'auto', 'system')),
		CHECK(version >= 1),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at))
	);`,
	`CREATE INDEX idx_context_memories_scope_status_updated
		ON context_memories(scope, scope_id, status, updated_at DESC, id DESC);`,
	`CREATE TRIGGER trg_context_memories_identity_immutable
		BEFORE UPDATE ON context_memories
		WHEN NEW.id != OLD.id OR NEW.protocol_version != OLD.protocol_version
			OR NEW.scope != OLD.scope OR NEW.scope_id != OLD.scope_id
			OR NEW.source_kind != OLD.source_kind OR NEW.created_by != OLD.created_by
			OR NEW.created_at != OLD.created_at
		BEGIN SELECT RAISE(ABORT, 'long-term memory identity is immutable'); END;`,
	`CREATE TABLE run_instruction_snapshots (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		revision INTEGER NOT NULL,
		target_path TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		snapshot_json TEXT NOT NULL,
		diff_json TEXT NOT NULL,
		confirmed_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE,
		UNIQUE(run_id, revision),
		CHECK(revision >= 1),
		CHECK(length(CAST(target_path AS BLOB)) BETWEEN 1 AND 4096
			AND instr(target_path, char(0)) = 0),
		CHECK(length(fingerprint) = 64 AND fingerprint = lower(fingerprint)
			AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(snapshot_json) AND json_type(snapshot_json) = 'object'
			AND json_extract(snapshot_json, '$.protocol_version') = 'project_instruction_snapshot.v1'
			AND json_extract(snapshot_json, '$.fingerprint') = fingerprint),
		CHECK(json_valid(diff_json) AND json_type(diff_json) = 'object'),
		CHECK(length(trim(confirmed_by)) BETWEEN 1 AND 256
			AND lower(confirmed_by) NOT IN ('agent', 'assistant', 'llm', 'model', 'repository',
				'repo', 'tool', 'supervisor', 'run_supervisor', 'automatic', 'auto', 'system')),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_run_instruction_snapshots_run_revision
		ON run_instruction_snapshots(run_id, revision DESC);`,
	`CREATE TRIGGER trg_run_instruction_snapshots_no_update
		BEFORE UPDATE ON run_instruction_snapshots
		BEGIN SELECT RAISE(ABORT, 'Run instruction snapshots are immutable'); END;`,
	`CREATE TRIGGER trg_run_instruction_snapshots_no_delete
		BEFORE DELETE ON run_instruction_snapshots
		BEGIN SELECT RAISE(ABORT, 'Run instruction snapshots are immutable'); END;`,
	`CREATE TABLE session_continuity_nodes (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		kind TEXT NOT NULL,
		session_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		parent_id TEXT,
		source_node_id TEXT,
		title TEXT NOT NULL,
		summary TEXT NOT NULL,
		snapshot_json TEXT NOT NULL,
		context_sha256 TEXT NOT NULL,
		project_config_fingerprint TEXT NOT NULL,
		project_instructions_fingerprint TEXT NOT NULL,
		git_branch TEXT NOT NULL,
		git_head TEXT NOT NULL,
		created_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE,
		FOREIGN KEY(parent_id) REFERENCES session_continuity_nodes(id) ON DELETE RESTRICT,
		FOREIGN KEY(source_node_id) REFERENCES session_continuity_nodes(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'session_continuity_node.v1'),
		CHECK(kind IN ('root', 'checkpoint', 'fork', 'resume')),
		CHECK(length(trim(title)) BETWEEN 1 AND 256 AND length(CAST(title AS BLOB)) <= 1024),
		CHECK(length(CAST(summary AS BLOB)) <= 4096 AND instr(summary, char(0)) = 0),
		CHECK(json_valid(snapshot_json) AND json_type(snapshot_json) = 'object'
			AND json_extract(snapshot_json, '$.protocol_version') = 'continuity_snapshot.v1'),
		CHECK(length(context_sha256) = 64 AND context_sha256 = lower(context_sha256)
			AND context_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(project_config_fingerprint = '' OR
			(length(project_config_fingerprint) = 64
			AND project_config_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(project_instructions_fingerprint = '' OR
			(length(project_instructions_fingerprint) = 64
			AND project_instructions_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(CAST(git_branch AS BLOB)) <= 512 AND instr(git_branch, char(0)) = 0),
		CHECK(git_head = '' OR (length(git_head) BETWEEN 40 AND 64
			AND git_head = lower(git_head) AND git_head NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(trim(created_by)) BETWEEN 1 AND 256
			AND lower(created_by) NOT IN ('agent', 'assistant', 'llm', 'model', 'repository',
				'repo', 'tool', 'supervisor', 'run_supervisor', 'automatic', 'auto', 'system')),
		CHECK(julianday(created_at) IS NOT NULL),
		CHECK((kind = 'root' AND parent_id IS NULL AND source_node_id IS NULL)
			OR (kind = 'checkpoint' AND parent_id IS NOT NULL AND source_node_id IS NULL)
			OR (kind IN ('fork', 'resume') AND parent_id IS NULL AND source_node_id IS NOT NULL))
	);`,
	`CREATE INDEX idx_session_continuity_nodes_session_created
		ON session_continuity_nodes(session_id, created_at, id);`,
	`CREATE INDEX idx_session_continuity_nodes_run_created
		ON session_continuity_nodes(run_id, created_at, id);`,
	`CREATE TRIGGER trg_session_continuity_nodes_no_update
		BEFORE UPDATE ON session_continuity_nodes
		BEGIN SELECT RAISE(ABORT, 'Session continuity nodes are immutable'); END;`,
	`CREATE TRIGGER trg_session_continuity_nodes_no_delete
		BEFORE DELETE ON session_continuity_nodes
		BEGIN SELECT RAISE(ABORT, 'Session continuity nodes are immutable'); END;`,
}
