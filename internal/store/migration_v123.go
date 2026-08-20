package store

var gitAdvancedStatements = []string{
	`CREATE TABLE git_advanced_operations (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_sha256 TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		preview_id TEXT NOT NULL,
		approval_fingerprint TEXT NOT NULL,
		approval_id TEXT,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		operation TEXT NOT NULL,
		spec_json TEXT NOT NULL,
		preview_json TEXT NOT NULL,
		repository_sha256 TEXT NOT NULL,
		common_dir_sha256 TEXT NOT NULL,
		permission_snapshot_id TEXT NOT NULL,
		permission_revision INTEGER NOT NULL,
		capability_generation TEXT NOT NULL,
		lease_id TEXT NOT NULL,
		lease_generation INTEGER NOT NULL,
		status TEXT NOT NULL,
		receipt_json TEXT NOT NULL DEFAULT '{}',
		error_code TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		started_at TEXT,
		completed_at TEXT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(approval_id) REFERENCES tool_approvals(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'git-advanced.v1'),
		CHECK(length(operation_key_sha256) = 64 AND operation_key_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(approval_fingerprint) = 64 AND approval_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(repository_sha256) = 64 AND repository_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(common_dir_sha256) = 64 AND common_dir_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(capability_generation) = 64 AND capability_generation NOT GLOB '*[^0-9a-f]*'),
		CHECK(operation IN ('hunk_stage','hunk_unstage','hunk_revert',
			'stash_create','stash_apply','stash_pop','stash_drop',
			'rebase_start','rebase_continue','rebase_skip','rebase_abort',
			'cherry_pick_start','cherry_pick_continue','cherry_pick_skip','cherry_pick_abort',
			'bisect_start','bisect_good','bisect_bad','bisect_skip','bisect_run','bisect_reset',
			'worktree_create','worktree_lock','worktree_unlock','worktree_remove','worktree_prune')),
		CHECK(json_valid(spec_json) AND length(spec_json) BETWEEN 2 AND 65536),
		CHECK(json_valid(preview_json) AND length(preview_json) BETWEEN 2 AND 2097152),
		CHECK(json_valid(receipt_json) AND length(receipt_json) BETWEEN 2 AND 2097152),
		CHECK(permission_revision > 0 AND lease_generation > 0),
		CHECK(length(permission_snapshot_id) BETWEEN 1 AND 256
			AND length(lease_id) BETWEEN 1 AND 256),
		CHECK(status IN ('proposed','running','succeeded','conflicted','failed')),
		CHECK(length(error_code) <= 64),
		CHECK(julianday(created_at) IS NOT NULL),
		CHECK((status = 'proposed' AND approval_id IS NULL AND started_at IS NULL AND completed_at IS NULL)
			OR (status = 'running' AND approval_id IS NOT NULL AND julianday(started_at) IS NOT NULL
				AND completed_at IS NULL)
			OR (status IN ('succeeded','conflicted','failed') AND approval_id IS NOT NULL
				AND julianday(started_at) IS NOT NULL AND julianday(completed_at) IS NOT NULL
				AND julianday(completed_at) >= julianday(started_at)))
	);`,
	`CREATE INDEX idx_git_advanced_operations_run
		ON git_advanced_operations(run_id, created_at DESC, id DESC);`,
	`CREATE INDEX idx_git_advanced_operations_sequence_recovery
		ON git_advanced_operations(repository_sha256, status, created_at DESC);`,
	`CREATE UNIQUE INDEX idx_git_advanced_operations_running_common_dir
		ON git_advanced_operations(common_dir_sha256) WHERE status = 'running';`,
	`CREATE TRIGGER trg_git_advanced_operation_identity_immutable
		BEFORE UPDATE ON git_advanced_operations
		WHEN OLD.id <> NEW.id OR OLD.protocol_version <> NEW.protocol_version
			OR OLD.operation_key_sha256 <> NEW.operation_key_sha256
			OR OLD.request_fingerprint <> NEW.request_fingerprint
			OR OLD.preview_id <> NEW.preview_id
			OR OLD.approval_fingerprint <> NEW.approval_fingerprint
			OR OLD.run_id <> NEW.run_id OR OLD.session_id <> NEW.session_id
			OR OLD.workspace_id <> NEW.workspace_id OR OLD.operation <> NEW.operation
			OR OLD.spec_json <> NEW.spec_json OR OLD.preview_json <> NEW.preview_json
			OR OLD.repository_sha256 <> NEW.repository_sha256
			OR OLD.common_dir_sha256 <> NEW.common_dir_sha256
			OR OLD.permission_snapshot_id <> NEW.permission_snapshot_id
			OR OLD.permission_revision <> NEW.permission_revision
			OR OLD.capability_generation <> NEW.capability_generation
			OR OLD.lease_id <> NEW.lease_id OR OLD.lease_generation <> NEW.lease_generation
			OR OLD.created_at <> NEW.created_at
		BEGIN SELECT RAISE(ABORT, 'Git advanced operation identity is immutable'); END;`,
	`CREATE TRIGGER trg_git_advanced_operation_terminal_immutable
		BEFORE UPDATE ON git_advanced_operations
		WHEN OLD.status IN ('succeeded','conflicted','failed')
		BEGIN SELECT RAISE(ABORT, 'terminal Git advanced operation is immutable'); END;`,
	`CREATE TRIGGER trg_git_advanced_operation_delete_immutable
		BEFORE DELETE ON git_advanced_operations
		BEGIN SELECT RAISE(ABORT, 'Git advanced audit cannot be deleted'); END;`,

	`CREATE TABLE git_advanced_sequences (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		status TEXT NOT NULL,
		repository_sha256 TEXT NOT NULL,
		original_head TEXT NOT NULL,
		original_branch TEXT NOT NULL,
		target_json TEXT NOT NULL,
		sequencer_sha256 TEXT NOT NULL,
		current_head TEXT NOT NULL,
		conflict_json TEXT NOT NULL,
		generation INTEGER NOT NULL,
		started_operation_id TEXT NOT NULL UNIQUE,
		last_operation_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		completed_at TEXT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(started_operation_id) REFERENCES git_advanced_operations(id) ON DELETE RESTRICT,
		FOREIGN KEY(last_operation_id) REFERENCES git_advanced_operations(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'git-advanced-sequence.v1'),
		CHECK(kind IN ('rebase','cherry_pick','bisect')),
		CHECK(status IN ('active','conflicted','completed','aborted','failed')),
		CHECK(length(repository_sha256) = 64 AND repository_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(original_head) IN (40,64) AND length(current_head) IN (40,64)),
		CHECK(json_valid(target_json) AND length(target_json) BETWEEN 2 AND 65536),
		CHECK(json_valid(conflict_json) AND length(conflict_json) BETWEEN 2 AND 262144),
		CHECK(length(sequencer_sha256) = 64 AND sequencer_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(generation > 0 AND julianday(created_at) IS NOT NULL
			AND julianday(updated_at) IS NOT NULL AND julianday(updated_at) >= julianday(created_at)),
		CHECK((status IN ('active','conflicted') AND completed_at IS NULL)
			OR (status IN ('completed','aborted','failed') AND julianday(completed_at) IS NOT NULL))
	);`,
	`CREATE UNIQUE INDEX idx_git_advanced_sequence_active_repo
		ON git_advanced_sequences(repository_sha256) WHERE status IN ('active','conflicted');`,
	`CREATE INDEX idx_git_advanced_sequences_run
		ON git_advanced_sequences(run_id, created_at DESC, id DESC);`,
	`CREATE TRIGGER trg_git_advanced_sequence_identity_immutable
		BEFORE UPDATE ON git_advanced_sequences
		WHEN OLD.id <> NEW.id OR OLD.protocol_version <> NEW.protocol_version
			OR OLD.run_id <> NEW.run_id OR OLD.workspace_id <> NEW.workspace_id
			OR OLD.kind <> NEW.kind OR OLD.repository_sha256 <> NEW.repository_sha256
			OR OLD.original_head <> NEW.original_head OR OLD.original_branch <> NEW.original_branch
			OR OLD.target_json <> NEW.target_json OR OLD.started_operation_id <> NEW.started_operation_id
			OR OLD.created_at <> NEW.created_at
		BEGIN SELECT RAISE(ABORT, 'Git advanced sequence identity is immutable'); END;`,
	`CREATE TRIGGER trg_git_advanced_sequence_terminal_immutable
		BEFORE UPDATE ON git_advanced_sequences
		WHEN OLD.status IN ('completed','aborted','failed')
		BEGIN SELECT RAISE(ABORT, 'terminal Git advanced sequence is immutable'); END;`,
	`CREATE TRIGGER trg_git_advanced_sequence_delete_immutable
		BEFORE DELETE ON git_advanced_sequences
		BEGIN SELECT RAISE(ABORT, 'Git advanced sequence audit cannot be deleted'); END;`,

	`CREATE TABLE git_managed_worktrees (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		repository_sha256 TEXT NOT NULL,
		common_dir_sha256 TEXT NOT NULL,
		name TEXT NOT NULL,
		path TEXT NOT NULL,
		path_sha256 TEXT NOT NULL,
		branch TEXT NOT NULL,
		head TEXT NOT NULL,
		locked INTEGER NOT NULL,
		lock_reason TEXT NOT NULL DEFAULT '',
		present INTEGER NOT NULL,
		generation INTEGER NOT NULL,
		created_operation_id TEXT NOT NULL UNIQUE,
		last_operation_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		removed_at TEXT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(created_operation_id) REFERENCES git_advanced_operations(id) ON DELETE RESTRICT,
		FOREIGN KEY(last_operation_id) REFERENCES git_advanced_operations(id) ON DELETE RESTRICT,
		UNIQUE(common_dir_sha256, name),
		UNIQUE(path_sha256),
		CHECK(protocol_version = 'git-managed-worktree.v1'),
		CHECK(length(repository_sha256) = 64 AND repository_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(common_dir_sha256) = 64 AND common_dir_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(path_sha256) = 64 AND path_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(name) BETWEEN 1 AND 80 AND length(path) BETWEEN 1 AND 4096
			AND length(branch) BETWEEN 1 AND 255 AND length(head) IN (40,64)),
		CHECK(locked IN (0,1) AND present IN (0,1) AND generation > 0),
		CHECK(length(lock_reason) <= 4096),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at)),
		CHECK((present = 1 AND removed_at IS NULL)
			OR (present = 0 AND locked = 0 AND julianday(removed_at) IS NOT NULL))
	);`,
	`CREATE INDEX idx_git_managed_worktrees_repo
		ON git_managed_worktrees(repository_sha256, present, created_at DESC);`,
	`CREATE INDEX idx_git_managed_worktrees_run
		ON git_managed_worktrees(run_id, created_at DESC, id DESC);`,
	`CREATE TRIGGER trg_git_managed_worktree_identity_immutable
		BEFORE UPDATE ON git_managed_worktrees
		WHEN OLD.id <> NEW.id OR OLD.protocol_version <> NEW.protocol_version
			OR OLD.run_id <> NEW.run_id OR OLD.workspace_id <> NEW.workspace_id
			OR OLD.repository_sha256 <> NEW.repository_sha256
			OR OLD.common_dir_sha256 <> NEW.common_dir_sha256 OR OLD.name <> NEW.name
			OR OLD.path <> NEW.path OR OLD.path_sha256 <> NEW.path_sha256
			OR OLD.branch <> NEW.branch OR OLD.created_operation_id <> NEW.created_operation_id
			OR OLD.created_at <> NEW.created_at
		BEGIN SELECT RAISE(ABORT, 'managed Git worktree identity is immutable'); END;`,
	`CREATE TRIGGER trg_git_managed_worktree_removed_immutable
		BEFORE UPDATE ON git_managed_worktrees WHEN OLD.present = 0
		BEGIN SELECT RAISE(ABORT, 'removed managed Git worktree is immutable'); END;`,
	`CREATE TRIGGER trg_git_managed_worktree_delete_immutable
		BEFORE DELETE ON git_managed_worktrees
		BEGIN SELECT RAISE(ABORT, 'managed Git worktree audit cannot be deleted'); END;`,
}
