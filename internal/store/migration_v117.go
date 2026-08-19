package store

// workspaceCheckpointStatements adds the workspace-checkpoint.v1 content-
// addressed manifest, mutation transaction, and Run cursor ledgers. Checkpoint
// rows are assembled unsealed inside one SQLite transaction and become visible
// only after the entry count and blob references are complete.
var workspaceCheckpointStatements = []string{
	`CREATE TABLE workspace_checkpoint_blobs (
		sha256 TEXT PRIMARY KEY,
		size_bytes INTEGER NOT NULL,
		content BLOB NOT NULL,
		reference_count INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		CHECK(length(sha256) = 64 AND sha256 = lower(sha256)
			AND sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(size_bytes BETWEEN 0 AND 33554432
			AND length(content) = size_bytes),
		CHECK(reference_count >= 0),
		CHECK(julianday(created_at) IS NOT NULL)
	) WITHOUT ROWID;`,
	`CREATE TRIGGER trg_workspace_checkpoint_blob_store_quota
		BEFORE INSERT ON workspace_checkpoint_blobs
		WHEN NOT EXISTS (SELECT 1 FROM workspace_checkpoint_blobs
			WHERE sha256 = NEW.sha256)
			AND (SELECT COALESCE(SUM(size_bytes), 0) FROM workspace_checkpoint_blobs)
			+ NEW.size_bytes > 2147483648
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint blob store quota exceeded'); END;`,
	`CREATE TABLE workspace_checkpoints (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		attempt_id TEXT NOT NULL,
		capability_generation TEXT NOT NULL,
		trigger_kind TEXT NOT NULL,
		phase TEXT NOT NULL,
		trigger_receipt_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		title TEXT NOT NULL,
		parent_checkpoint_id TEXT NOT NULL,
		root_fingerprint TEXT NOT NULL,
		root_path_sha256 TEXT NOT NULL,
		base_commit TEXT NOT NULL,
		branch TEXT NOT NULL,
		index_sha256 TEXT NOT NULL,
		index_blob_sha256 TEXT NOT NULL,
		manifest_sha256 TEXT NOT NULL,
		recovery_level TEXT NOT NULL,
		incomplete_reasons_json TEXT NOT NULL,
		entry_count INTEGER NOT NULL,
		stored_bytes INTEGER NOT NULL,
		sealed INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'workspace-checkpoint.v1'),
		CHECK(trigger_kind IN ('manual', 'file_tool', 'command_batch', 'git_mutation',
			'agent_merge', 'rewind_preflight', 'rewind_result', 'fork')),
		CHECK(phase IN ('standalone', 'before', 'after', 'preflight')),
		CHECK(recovery_level IN ('complete', 'partial', 'unavailable')),
		CHECK(json_valid(incomplete_reasons_json)
			AND json_type(incomplete_reasons_json) = 'array'
			AND length(CAST(incomplete_reasons_json AS BLOB)) BETWEEN 2 AND 16384),
		CHECK(entry_count BETWEEN 0 AND 20000),
		CHECK(stored_bytes BETWEEN 0 AND 67108864),
		CHECK(sealed IN (0, 1)),
		CHECK(length(id) BETWEEN 1 AND 256 AND id = trim(id) AND instr(id, char(0)) = 0),
		CHECK(length(run_id) BETWEEN 1 AND 256 AND run_id = trim(run_id) AND instr(run_id, char(0)) = 0),
		CHECK(length(mission_id) BETWEEN 1 AND 256 AND mission_id = trim(mission_id) AND instr(mission_id, char(0)) = 0),
		CHECK(length(session_id) BETWEEN 1 AND 256 AND session_id = trim(session_id) AND instr(session_id, char(0)) = 0),
		CHECK(length(workspace_id) BETWEEN 1 AND 256 AND workspace_id = trim(workspace_id) AND instr(workspace_id, char(0)) = 0),
		CHECK(length(attempt_id) <= 256 AND attempt_id = trim(attempt_id) AND instr(attempt_id, char(0)) = 0),
		CHECK((capability_generation = '') OR (length(capability_generation) = 64
			AND capability_generation = lower(capability_generation)
			AND capability_generation NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(trigger_receipt_id) BETWEEN 1 AND 256
			AND trigger_receipt_id = trim(trigger_receipt_id)
			AND instr(trigger_receipt_id, char(0)) = 0),
		CHECK(length(requested_by) <= 256 AND requested_by = trim(requested_by)
			AND instr(requested_by, char(0)) = 0),
		CHECK(length(title) <= 512 AND title = trim(title)
			AND instr(title, char(0)) = 0),
		CHECK(length(parent_checkpoint_id) <= 256
			AND parent_checkpoint_id = trim(parent_checkpoint_id)
			AND instr(parent_checkpoint_id, char(0)) = 0),
		CHECK(length(root_fingerprint) = 64 AND root_fingerprint = lower(root_fingerprint)
			AND root_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(root_path_sha256) = 64 AND root_path_sha256 = lower(root_path_sha256)
			AND root_path_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(base_commit IN ('unborn', 'non-git') OR
			((length(base_commit) = 40 OR length(base_commit) = 64)
			AND base_commit = lower(base_commit)
			AND base_commit NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(branch) <= 255 AND instr(branch, char(0)) = 0),
		CHECK(length(index_sha256) = 64 AND index_sha256 = lower(index_sha256)
			AND index_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK((index_blob_sha256 = '') OR (length(index_blob_sha256) = 64
			AND index_blob_sha256 = lower(index_blob_sha256)
			AND index_blob_sha256 NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(manifest_sha256) = 64 AND manifest_sha256 = lower(manifest_sha256)
			AND manifest_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE TRIGGER trg_workspace_checkpoint_insert_unsealed
		BEFORE INSERT ON workspace_checkpoints WHEN NEW.sealed != 0
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint must be assembled unsealed'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_metadata_quota
		BEFORE INSERT ON workspace_checkpoints
		WHEN (SELECT COUNT(*) FROM workspace_checkpoints) >= 10000
			OR (SELECT COALESCE(SUM(entry_count), 0) FROM workspace_checkpoints)
				+ NEW.entry_count > 2000000
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint metadata quota exceeded'); END;`,
	`CREATE INDEX idx_workspace_checkpoints_run_created
		ON workspace_checkpoints(run_id, created_at DESC, id DESC) WHERE sealed = 1;`,
	`CREATE INDEX idx_workspace_checkpoints_workspace_created
		ON workspace_checkpoints(workspace_id, created_at DESC, id DESC) WHERE sealed = 1;`,
	`CREATE INDEX idx_workspace_checkpoints_receipt
		ON workspace_checkpoints(run_id, trigger_kind, trigger_receipt_id, phase);`,
	`CREATE TRIGGER trg_workspace_checkpoint_parent_binding
		BEFORE INSERT ON workspace_checkpoints WHEN NEW.parent_checkpoint_id != ''
			AND NOT EXISTS (SELECT 1 FROM workspace_checkpoints parent
				WHERE parent.id = NEW.parent_checkpoint_id AND parent.sealed = 1
					AND parent.run_id = NEW.run_id AND parent.workspace_id = NEW.workspace_id)
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint parent binding is invalid'); END;`,
	`CREATE TABLE workspace_checkpoint_entries (
		checkpoint_id TEXT NOT NULL,
		path TEXT NOT NULL,
		kind TEXT NOT NULL,
		worktree_state TEXT NOT NULL,
		storage_policy TEXT NOT NULL,
		mode INTEGER NOT NULL,
		size_bytes INTEGER NOT NULL,
		worktree_sha256 TEXT NOT NULL,
		blob_sha256 TEXT NOT NULL,
		index_oid TEXT NOT NULL,
		index_mode TEXT NOT NULL,
		tracked INTEGER NOT NULL,
		staged INTEGER NOT NULL,
		binary INTEGER NOT NULL,
		line_endings TEXT NOT NULL,
		recoverable INTEGER NOT NULL,
		reason TEXT NOT NULL,
		PRIMARY KEY(checkpoint_id, path),
		FOREIGN KEY(checkpoint_id) REFERENCES workspace_checkpoints(id) ON DELETE RESTRICT,
		CHECK(kind IN ('file', 'directory', 'symlink', 'other')),
		CHECK(worktree_state IN ('present', 'missing', 'ignored', 'generated', 'external')),
		CHECK(storage_policy IN ('stored', 'missing', 'excluded_ignored',
			'excluded_generated', 'excluded_large', 'excluded_sensitive',
			'excluded_link', 'excluded_special', 'unreadable')),
		CHECK(mode BETWEEN 0 AND 4095),
		CHECK(size_bytes >= 0),
		CHECK(worktree_sha256 = 'missing' OR (length(worktree_sha256) = 64
			AND worktree_sha256 = lower(worktree_sha256)
			AND worktree_sha256 NOT GLOB '*[^0-9a-f]*')),
		CHECK((blob_sha256 = '') OR (length(blob_sha256) = 64
			AND blob_sha256 = lower(blob_sha256)
			AND blob_sha256 NOT GLOB '*[^0-9a-f]*')),
		CHECK(tracked IN (0, 1) AND staged IN (0, 1) AND binary IN (0, 1)
			AND recoverable IN (0, 1)),
		CHECK(line_endings IN ('', 'lf', 'crlf', 'mixed', 'none')),
		CHECK(length(path) BETWEEN 1 AND 4096 AND path = trim(path)
			AND instr(path, char(0)) = 0),
		CHECK(length(index_oid) <= 128 AND instr(index_oid, char(0)) = 0),
		CHECK(length(index_mode) <= 16 AND instr(index_mode, char(0)) = 0),
		CHECK(length(reason) <= 2048 AND reason = trim(reason) AND instr(reason, char(0)) = 0),
		CHECK((storage_policy = 'stored' AND kind = 'file' AND worktree_state = 'present'
			AND recoverable = 1 AND blob_sha256 = worktree_sha256 AND size_bytes <= 4194304)
			OR (storage_policy = 'missing' AND worktree_state = 'missing'
				AND worktree_sha256 = 'missing' AND recoverable = 1 AND blob_sha256 = '')
			OR (storage_policy NOT IN ('stored', 'missing')
				AND recoverable = 0 AND blob_sha256 = ''))
	) WITHOUT ROWID;`,
	`CREATE INDEX idx_workspace_checkpoint_entries_blob
		ON workspace_checkpoint_entries(blob_sha256) WHERE blob_sha256 != '';`,
	`CREATE TRIGGER trg_workspace_checkpoint_entry_insert_unsealed
		BEFORE INSERT ON workspace_checkpoint_entries
		WHEN NOT EXISTS (SELECT 1 FROM workspace_checkpoints
			WHERE id = NEW.checkpoint_id AND sealed = 0)
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint entries require an unsealed checkpoint'); END;`,
	`CREATE TABLE workspace_checkpoint_transactions (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		trigger_receipt_id TEXT NOT NULL,
		before_checkpoint_id TEXT NOT NULL,
		after_checkpoint_id TEXT NOT NULL,
		expected_current_checkpoint_id TEXT NOT NULL,
		target_checkpoint_id TEXT NOT NULL,
		fork_workspace_root TEXT NOT NULL,
		fork_branch TEXT NOT NULL,
		status TEXT NOT NULL,
		recovery_level TEXT NOT NULL,
		error_code TEXT NOT NULL,
		conflict_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		completed_at TEXT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(before_checkpoint_id) REFERENCES workspace_checkpoints(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'workspace-checkpoint.v1'),
		CHECK(kind IN ('file_tool', 'command_batch', 'git_mutation', 'agent_merge',
			'rewind', 'undo', 'redo', 'fork')),
		CHECK(status IN ('prepared', 'applying', 'completed', 'failed', 'interrupted')),
		CHECK(recovery_level IN ('complete', 'partial', 'unavailable')),
		CHECK(length(operation_key_digest) = 64 AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(id) BETWEEN 1 AND 256 AND id = trim(id) AND instr(id, char(0)) = 0),
		CHECK(length(run_id) BETWEEN 1 AND 256 AND run_id = trim(run_id) AND instr(run_id, char(0)) = 0),
		CHECK(length(workspace_id) BETWEEN 1 AND 256 AND workspace_id = trim(workspace_id) AND instr(workspace_id, char(0)) = 0),
		CHECK(length(trigger_receipt_id) BETWEEN 1 AND 256
			AND trigger_receipt_id = trim(trigger_receipt_id)
			AND instr(trigger_receipt_id, char(0)) = 0),
		CHECK(length(before_checkpoint_id) BETWEEN 1 AND 256),
		CHECK(length(after_checkpoint_id) <= 256),
		CHECK(length(expected_current_checkpoint_id) <= 256),
		CHECK(length(target_checkpoint_id) <= 256),
		CHECK((kind = 'fork'
			AND length(fork_workspace_root) BETWEEN 1 AND 4096
			AND fork_workspace_root = trim(fork_workspace_root)
			AND instr(fork_workspace_root, char(0)) = 0
			AND length(fork_branch) BETWEEN 1 AND 255
			AND fork_branch = trim(fork_branch) AND instr(fork_branch, char(0)) = 0)
			OR (kind != 'fork' AND fork_workspace_root = '' AND fork_branch = '')),
		CHECK(length(error_code) <= 512 AND error_code = trim(error_code)
			AND instr(error_code, char(0)) = 0),
		CHECK(json_valid(conflict_json) AND json_type(conflict_json) = 'array'
			AND length(CAST(conflict_json AS BLOB)) BETWEEN 2 AND 262144),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at)),
		CHECK(completed_at IS NULL OR (julianday(completed_at) IS NOT NULL
			AND julianday(completed_at) >= julianday(updated_at))),
		CHECK((status IN ('prepared', 'applying') AND after_checkpoint_id = ''
			AND error_code = '' AND completed_at IS NULL)
			OR (status = 'completed' AND after_checkpoint_id != ''
				AND error_code = '' AND completed_at IS NOT NULL)
			OR (status IN ('failed', 'interrupted') AND error_code != ''
				AND completed_at IS NOT NULL))
	);`,
	`CREATE TRIGGER trg_workspace_checkpoint_transaction_quota
		BEFORE INSERT ON workspace_checkpoint_transactions
		WHEN (SELECT COUNT(*) FROM workspace_checkpoint_transactions) >= 20000
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint transaction quota exceeded'); END;`,
	`CREATE INDEX idx_workspace_checkpoint_transactions_run_created
		ON workspace_checkpoint_transactions(run_id, created_at DESC, id DESC);`,
	`CREATE INDEX idx_workspace_checkpoint_transactions_open
		ON workspace_checkpoint_transactions(status, updated_at)
		WHERE status IN ('prepared', 'applying');`,
	`CREATE TRIGGER trg_workspace_checkpoint_transaction_insert_binding
		BEFORE INSERT ON workspace_checkpoint_transactions
		WHEN NOT EXISTS (SELECT 1 FROM workspace_checkpoints checkpoint
				WHERE checkpoint.id = NEW.before_checkpoint_id AND checkpoint.sealed = 1
					AND checkpoint.run_id = NEW.run_id
					AND checkpoint.workspace_id = NEW.workspace_id)
			OR (NEW.expected_current_checkpoint_id != '' AND NOT EXISTS
				(SELECT 1 FROM workspace_checkpoints checkpoint
				 WHERE checkpoint.id = NEW.expected_current_checkpoint_id
					AND checkpoint.sealed = 1 AND checkpoint.run_id = NEW.run_id
					AND checkpoint.workspace_id = NEW.workspace_id))
			OR (NEW.target_checkpoint_id != '' AND NOT EXISTS
				(SELECT 1 FROM workspace_checkpoints checkpoint
				 WHERE checkpoint.id = NEW.target_checkpoint_id
					AND checkpoint.sealed = 1 AND checkpoint.run_id = NEW.run_id
					AND checkpoint.workspace_id = NEW.workspace_id))
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint transaction binding is invalid'); END;`,
	`CREATE TABLE workspace_checkpoint_run_state (
		run_id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		current_checkpoint_id TEXT NOT NULL,
		last_transaction_id TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(length(current_checkpoint_id) BETWEEN 1 AND 256),
		CHECK(length(last_transaction_id) <= 256),
		CHECK(julianday(updated_at) IS NOT NULL)
	) WITHOUT ROWID;`,
	`CREATE TRIGGER trg_workspace_checkpoint_run_state_insert_binding
		BEFORE INSERT ON workspace_checkpoint_run_state
		WHEN NOT EXISTS (SELECT 1 FROM workspace_checkpoints checkpoint
				WHERE checkpoint.id = NEW.current_checkpoint_id AND checkpoint.sealed = 1
					AND checkpoint.run_id = NEW.run_id
					AND checkpoint.workspace_id = NEW.workspace_id)
			OR (NEW.last_transaction_id != '' AND NOT EXISTS
				(SELECT 1 FROM workspace_checkpoint_transactions transaction_record
				 WHERE transaction_record.id = NEW.last_transaction_id
					AND transaction_record.run_id = NEW.run_id
					AND transaction_record.workspace_id = NEW.workspace_id
					AND transaction_record.status IN ('completed', 'failed', 'interrupted')
					AND transaction_record.after_checkpoint_id = NEW.current_checkpoint_id))
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint Run state binding is invalid'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_run_state_update_binding
		BEFORE UPDATE ON workspace_checkpoint_run_state
		WHEN NEW.run_id != OLD.run_id OR NEW.workspace_id != OLD.workspace_id
			OR julianday(NEW.updated_at) < julianday(OLD.updated_at)
			OR NOT EXISTS (SELECT 1 FROM workspace_checkpoints checkpoint
				WHERE checkpoint.id = NEW.current_checkpoint_id AND checkpoint.sealed = 1
					AND checkpoint.run_id = NEW.run_id
					AND checkpoint.workspace_id = NEW.workspace_id)
			OR (NEW.last_transaction_id != '' AND NOT EXISTS
				(SELECT 1 FROM workspace_checkpoint_transactions transaction_record
				 WHERE transaction_record.id = NEW.last_transaction_id
					AND transaction_record.run_id = NEW.run_id
					AND transaction_record.workspace_id = NEW.workspace_id
					AND transaction_record.status IN ('completed', 'failed', 'interrupted')
					AND transaction_record.after_checkpoint_id = NEW.current_checkpoint_id))
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint Run state transition is invalid'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_insert_scope
		BEFORE INSERT ON workspace_checkpoints
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN sessions session_record ON session_record.id = run.session_id
			WHERE run.id = NEW.run_id AND mission.id = NEW.mission_id
				AND session_record.id = NEW.session_id
				AND mission.workspace_id = NEW.workspace_id
				AND session_record.workspace_id = NEW.workspace_id
		)
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint Run binding is invalid'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_blob_reference
		AFTER INSERT ON workspace_checkpoints WHEN NEW.index_blob_sha256 != ''
		BEGIN UPDATE workspace_checkpoint_blobs
			SET reference_count = reference_count + 1
			WHERE sha256 = NEW.index_blob_sha256; END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_entry_blob_reference
		AFTER INSERT ON workspace_checkpoint_entries WHEN NEW.blob_sha256 != ''
		BEGIN UPDATE workspace_checkpoint_blobs
			SET reference_count = reference_count + 1
			WHERE sha256 = NEW.blob_sha256; END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_seal
		BEFORE UPDATE ON workspace_checkpoints
		WHEN OLD.sealed != 0 OR NEW.sealed != 1
			OR NEW.id != OLD.id OR NEW.protocol_version != OLD.protocol_version
			OR NEW.run_id != OLD.run_id OR NEW.mission_id != OLD.mission_id
			OR NEW.session_id != OLD.session_id OR NEW.workspace_id != OLD.workspace_id
			OR NEW.attempt_id != OLD.attempt_id
			OR NEW.capability_generation != OLD.capability_generation
			OR NEW.trigger_kind != OLD.trigger_kind OR NEW.phase != OLD.phase
			OR NEW.trigger_receipt_id != OLD.trigger_receipt_id
			OR NEW.parent_checkpoint_id != OLD.parent_checkpoint_id
			OR NEW.root_fingerprint != OLD.root_fingerprint
			OR NEW.root_path_sha256 != OLD.root_path_sha256
			OR NEW.base_commit != OLD.base_commit OR NEW.branch != OLD.branch
			OR NEW.index_sha256 != OLD.index_sha256
			OR NEW.index_blob_sha256 != OLD.index_blob_sha256
			OR NEW.manifest_sha256 != OLD.manifest_sha256
			OR NEW.recovery_level != OLD.recovery_level
			OR NEW.incomplete_reasons_json != OLD.incomplete_reasons_json
			OR NEW.entry_count != OLD.entry_count OR NEW.stored_bytes != OLD.stored_bytes
			OR NEW.created_at != OLD.created_at
			OR NEW.entry_count != (SELECT COUNT(*) FROM workspace_checkpoint_entries
				WHERE checkpoint_id = NEW.id)
			OR (NEW.index_blob_sha256 != '' AND NOT EXISTS
				(SELECT 1 FROM workspace_checkpoint_blobs
				 WHERE sha256 = NEW.index_blob_sha256))
			OR EXISTS (SELECT 1 FROM workspace_checkpoint_entries entry
				WHERE entry.checkpoint_id = NEW.id AND entry.blob_sha256 != ''
					AND NOT EXISTS (SELECT 1 FROM workspace_checkpoint_blobs blob
						WHERE blob.sha256 = entry.blob_sha256))
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint seal is invalid'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_delete_immutable
		BEFORE DELETE ON workspace_checkpoints
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoints are immutable'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_entry_update_immutable
		BEFORE UPDATE ON workspace_checkpoint_entries
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint entries are immutable'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_entry_delete_immutable
		BEFORE DELETE ON workspace_checkpoint_entries
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint entries are immutable'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_blob_content_immutable
		BEFORE UPDATE OF sha256, size_bytes, content, created_at ON workspace_checkpoint_blobs
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint blob content is immutable'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_blob_delete_referenced
		BEFORE DELETE ON workspace_checkpoint_blobs WHEN OLD.reference_count != 0
		BEGIN SELECT RAISE(ABORT, 'referenced workspace checkpoint blobs cannot be deleted'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_transaction_update
		BEFORE UPDATE ON workspace_checkpoint_transactions
		WHEN NEW.id != OLD.id OR NEW.protocol_version != OLD.protocol_version
			OR NEW.operation_key_digest != OLD.operation_key_digest
			OR NEW.request_fingerprint != OLD.request_fingerprint
			OR NEW.run_id != OLD.run_id OR NEW.workspace_id != OLD.workspace_id
			OR NEW.kind != OLD.kind OR NEW.trigger_receipt_id != OLD.trigger_receipt_id
			OR NEW.before_checkpoint_id != OLD.before_checkpoint_id
			OR NEW.expected_current_checkpoint_id != OLD.expected_current_checkpoint_id
			OR NEW.target_checkpoint_id != OLD.target_checkpoint_id
			OR NEW.fork_workspace_root != OLD.fork_workspace_root
			OR NEW.fork_branch != OLD.fork_branch
			OR NEW.created_at != OLD.created_at OR julianday(NEW.updated_at) < julianday(OLD.updated_at)
			OR (NEW.after_checkpoint_id != '' AND NOT EXISTS
				(SELECT 1 FROM workspace_checkpoints checkpoint
				 WHERE checkpoint.id = NEW.after_checkpoint_id AND checkpoint.sealed = 1
					AND checkpoint.run_id = NEW.run_id
					AND checkpoint.workspace_id = NEW.workspace_id))
			OR OLD.status IN ('completed', 'failed', 'interrupted')
			OR (OLD.status = 'prepared' AND NEW.status NOT IN
				('prepared', 'applying', 'completed', 'failed', 'interrupted'))
			OR (OLD.status = 'applying' AND NEW.status NOT IN
				('applying', 'completed', 'failed', 'interrupted'))
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint transaction transition is invalid'); END;`,
	`CREATE TRIGGER trg_workspace_checkpoint_transaction_delete_immutable
		BEFORE DELETE ON workspace_checkpoint_transactions
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint transactions are immutable'); END;`,
}
