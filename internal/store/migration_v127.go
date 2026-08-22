package store

var drydockWorkspaceTrustStatements = []string{
	`CREATE TABLE drydock_workspace_trust (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL UNIQUE,
		workspace_id TEXT NOT NULL,
		source_identity_sha256 TEXT NOT NULL,
		root_path TEXT NOT NULL,
		root_path_sha256 TEXT NOT NULL,
		root_fingerprint TEXT NOT NULL,
		repository_sha256 TEXT NOT NULL,
		common_dir_sha256 TEXT NOT NULL,
		branch TEXT NOT NULL,
		base_commit TEXT NOT NULL,
		object_format TEXT NOT NULL,
		index_sha256 TEXT NOT NULL,
		worktree_sha256 TEXT NOT NULL,
		status_sha256 TEXT NOT NULL,
		source_captured_at TEXT NOT NULL,
		dirty_tracked INTEGER NOT NULL,
		dirty_untracked INTEGER NOT NULL,
		dirty_ignored INTEGER NOT NULL,
		symlink_entries INTEGER NOT NULL,
		submodule_entries INTEGER NOT NULL,
		confirmed_by TEXT NOT NULL,
		grants_process_authority INTEGER NOT NULL,
		confirmed_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'drydock-workspace-trust.v1'),
		CHECK(length(source_identity_sha256) = 64 AND source_identity_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(root_path) BETWEEN 1 AND 4096),
		CHECK(length(root_path_sha256) = 64 AND root_path_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(root_fingerprint) = 64 AND root_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(repository_sha256) = 64 AND repository_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(common_dir_sha256) = 64 AND common_dir_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(branch) BETWEEN 1 AND 255 AND length(base_commit) IN (40,64)),
		CHECK(object_format IN ('sha1','sha256')),
		CHECK(length(index_sha256) = 64 AND index_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(worktree_sha256) = 64 AND worktree_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(status_sha256) = 64 AND status_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(julianday(source_captured_at) IS NOT NULL),
		CHECK(dirty_tracked IN (0,1) AND dirty_untracked IN (0,1) AND dirty_ignored IN (0,1)),
		CHECK(symlink_entries >= 0 AND submodule_entries >= 0),
		CHECK(length(confirmed_by) BETWEEN 1 AND 256),
		CHECK(grants_process_authority = 0),
		CHECK(julianday(confirmed_at) IS NOT NULL)
	);`,
	`CREATE UNIQUE INDEX idx_drydock_trust_run_identity
		ON drydock_workspace_trust(run_id, source_identity_sha256);`,
	`CREATE TRIGGER trg_drydock_trust_insert_scope
		BEFORE INSERT ON drydock_workspace_trust
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN sessions session_record ON session_record.id = run.session_id
			WHERE run.id = NEW.run_id
				AND mission.workspace_id = NEW.workspace_id
				AND session_record.workspace_id = NEW.workspace_id
		)
		BEGIN SELECT RAISE(ABORT, 'Drydock Trust Run scope is invalid'); END;`,
	`CREATE TRIGGER trg_drydock_trust_update_immutable
		BEFORE UPDATE ON drydock_workspace_trust BEGIN
			SELECT RAISE(ABORT, 'Drydock Workspace Trust receipts are immutable');
		END;`,
	`CREATE TRIGGER trg_drydock_trust_delete_immutable
		BEFORE DELETE ON drydock_workspace_trust BEGIN
			SELECT RAISE(ABORT, 'Drydock Workspace Trust receipts are immutable');
		END;`,

	`CREATE TABLE drydock_workspaces (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL UNIQUE,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		source_workspace_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL UNIQUE,
		trust_id TEXT NOT NULL UNIQUE,
		source_identity_sha256 TEXT NOT NULL,
		root_path TEXT NOT NULL,
		root_path_sha256 TEXT NOT NULL,
		source_root_fingerprint TEXT NOT NULL,
		repository_sha256 TEXT NOT NULL,
		common_dir_sha256 TEXT NOT NULL,
		source_branch TEXT NOT NULL,
		base_commit TEXT NOT NULL,
		object_format TEXT NOT NULL,
		name TEXT NOT NULL,
		path TEXT NOT NULL,
		path_sha256 TEXT NOT NULL UNIQUE,
		branch TEXT NOT NULL,
		root_fingerprint TEXT NOT NULL DEFAULT '',
		expected_head TEXT NOT NULL DEFAULT '',
		expected_binding_fingerprint TEXT NOT NULL DEFAULT '',
		create_preview_id TEXT NOT NULL,
		create_git_receipt_id TEXT NOT NULL DEFAULT '',
		managed_worktree_id TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL,
		generation INTEGER NOT NULL,
		last_checkpoint_id TEXT NOT NULL DEFAULT '',
		last_delivery_id TEXT NOT NULL DEFAULT '',
		recovery_reason TEXT NOT NULL DEFAULT '',
		expires_at TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		cleaned_at TEXT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(source_workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(trust_id) REFERENCES drydock_workspace_trust(id) ON DELETE RESTRICT,
		UNIQUE(common_dir_sha256, name),
		UNIQUE(common_dir_sha256, branch),
		CHECK(protocol_version = 'drydock-workspace.v1'),
		CHECK(length(source_identity_sha256) = 64 AND source_identity_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(root_path) BETWEEN 1 AND 4096),
		CHECK(length(root_path_sha256) = 64 AND root_path_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(source_root_fingerprint) = 64 AND source_root_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(repository_sha256) = 64 AND repository_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(common_dir_sha256) = 64 AND common_dir_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(source_branch) BETWEEN 1 AND 255 AND length(base_commit) IN (40,64)),
		CHECK(object_format IN ('sha1','sha256')),
		CHECK(length(name) BETWEEN 1 AND 80 AND length(path) BETWEEN 1 AND 4096),
		CHECK(length(path_sha256) = 64 AND path_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(branch) BETWEEN 1 AND 255),
		CHECK(state IN ('preparing','ready','recovery_required','delivered','cleaned')),
		CHECK(generation > 0 AND length(create_preview_id) BETWEEN 1 AND 256),
		CHECK(length(create_git_receipt_id) <= 256 AND length(managed_worktree_id) <= 256
			AND length(last_checkpoint_id) <= 256
			AND length(last_delivery_id) <= 256 AND length(recovery_reason) <= 512),
		CHECK((state = 'preparing' AND root_fingerprint = '' AND expected_head = ''
				AND expected_binding_fingerprint = '' AND managed_worktree_id = '')
			OR (state IN ('ready','delivered') AND length(root_fingerprint) = 64
				AND length(expected_head) IN (40,64) AND length(expected_binding_fingerprint) = 64
				AND length(managed_worktree_id) > 0)
			OR (state IN ('recovery_required','cleaned') AND
				((root_fingerprint = '' AND expected_head = '' AND expected_binding_fingerprint = ''
					AND managed_worktree_id = '')
				 OR (length(root_fingerprint) = 64 AND length(expected_head) IN (40,64)
					AND length(expected_binding_fingerprint) = 64 AND length(managed_worktree_id) > 0)))),
		CHECK((state = 'recovery_required' AND length(recovery_reason) > 0)
			OR (state <> 'recovery_required' AND recovery_reason = '')),
		CHECK(julianday(expires_at) IS NOT NULL AND julianday(created_at) IS NOT NULL
			AND julianday(updated_at) IS NOT NULL AND julianday(updated_at) >= julianday(created_at)
			AND julianday(expires_at) >= julianday(created_at)
			AND julianday(expires_at) <= julianday(created_at, '+30 days')),
		CHECK((state = 'cleaned' AND julianday(cleaned_at) IS NOT NULL
				AND julianday(cleaned_at) >= julianday(updated_at))
			OR (state <> 'cleaned' AND cleaned_at IS NULL))
	);`,
	`CREATE INDEX idx_drydock_workspaces_active_repository
		ON drydock_workspaces(common_dir_sha256, state, created_at, id);`,
	`CREATE INDEX idx_drydock_workspaces_expiry
		ON drydock_workspaces(state, expires_at, id);`,
	`CREATE TRIGGER trg_drydock_workspace_insert_scope
		BEFORE INSERT ON drydock_workspaces
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN sessions session_record ON session_record.id = run.session_id
			JOIN drydock_workspace_trust trust_record ON trust_record.id = NEW.trust_id
			JOIN workspaces synthetic_workspace ON synthetic_workspace.id = NEW.workspace_id
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND run.session_id = NEW.session_id
				AND mission.workspace_id = NEW.source_workspace_id
				AND session_record.workspace_id = NEW.source_workspace_id
				AND trust_record.run_id = NEW.run_id
				AND trust_record.workspace_id = NEW.source_workspace_id
				AND trust_record.source_identity_sha256 = NEW.source_identity_sha256
				AND trust_record.root_path = NEW.root_path
				AND trust_record.root_path_sha256 = NEW.root_path_sha256
				AND trust_record.root_fingerprint = NEW.source_root_fingerprint
				AND trust_record.repository_sha256 = NEW.repository_sha256
				AND trust_record.common_dir_sha256 = NEW.common_dir_sha256
				AND trust_record.branch = NEW.source_branch
				AND trust_record.base_commit = NEW.base_commit
				AND trust_record.object_format = NEW.object_format
				AND synthetic_workspace.name = NEW.name
				AND synthetic_workspace.root_path = NEW.path
				AND NOT EXISTS (SELECT 1 FROM missions
					WHERE workspace_id = NEW.workspace_id)
				AND NOT EXISTS (SELECT 1 FROM sessions
					WHERE workspace_id = NEW.workspace_id)
		)
		BEGIN SELECT RAISE(ABORT, 'Drydock Run, Trust, or Workspace scope is invalid'); END;`,
	`CREATE TRIGGER trg_drydock_workspace_insert_capacity
		BEFORE INSERT ON drydock_workspaces
		WHEN (SELECT COUNT(*) FROM drydock_workspaces WHERE state <> 'cleaned') >= 64
			OR (SELECT COUNT(*) FROM drydock_workspaces
				WHERE common_dir_sha256 = NEW.common_dir_sha256 AND state <> 'cleaned') >= 8
		BEGIN SELECT RAISE(ABORT, 'Drydock active capacity is exhausted'); END;`,
	`CREATE TRIGGER trg_drydock_workspace_identity_immutable
		BEFORE UPDATE ON drydock_workspaces
		WHEN OLD.id <> NEW.id OR OLD.protocol_version <> NEW.protocol_version
			OR OLD.run_id <> NEW.run_id OR OLD.mission_id <> NEW.mission_id
			OR OLD.session_id <> NEW.session_id
			OR OLD.source_workspace_id <> NEW.source_workspace_id
			OR OLD.workspace_id <> NEW.workspace_id OR OLD.trust_id <> NEW.trust_id
			OR OLD.source_identity_sha256 <> NEW.source_identity_sha256
			OR OLD.root_path <> NEW.root_path OR OLD.root_path_sha256 <> NEW.root_path_sha256
			OR OLD.source_root_fingerprint <> NEW.source_root_fingerprint
			OR OLD.repository_sha256 <> NEW.repository_sha256
			OR OLD.common_dir_sha256 <> NEW.common_dir_sha256
			OR OLD.source_branch <> NEW.source_branch OR OLD.base_commit <> NEW.base_commit
			OR OLD.object_format <> NEW.object_format OR OLD.name <> NEW.name
			OR OLD.path <> NEW.path OR OLD.path_sha256 <> NEW.path_sha256
			OR OLD.branch <> NEW.branch OR OLD.create_preview_id <> NEW.create_preview_id
			OR OLD.expires_at <> NEW.expires_at OR OLD.created_at <> NEW.created_at
		BEGIN SELECT RAISE(ABORT, 'Drydock identity is immutable'); END;`,
	`CREATE TRIGGER trg_drydock_workspace_generation
		BEFORE UPDATE ON drydock_workspaces
		WHEN NEW.generation <> OLD.generation + 1
			OR julianday(NEW.updated_at) < julianday(OLD.updated_at)
		BEGIN SELECT RAISE(ABORT, 'Drydock update requires the next ownership generation'); END;`,
	`CREATE TRIGGER trg_drydock_workspace_transition
		BEFORE UPDATE ON drydock_workspaces
		WHEN NOT (
			(OLD.state = 'preparing' AND NEW.state IN ('ready','recovery_required','cleaned'))
			OR (OLD.state = 'ready' AND NEW.state IN ('ready','delivered','recovery_required','cleaned'))
			OR (OLD.state = 'delivered' AND NEW.state IN ('delivered','ready','recovery_required','cleaned'))
			OR (OLD.state = 'recovery_required' AND NEW.state IN ('recovery_required','ready','cleaned'))
		)
		BEGIN SELECT RAISE(ABORT, 'invalid Drydock state transition'); END;`,
	`CREATE TRIGGER trg_drydock_workspace_cleaned_immutable
		BEFORE UPDATE ON drydock_workspaces WHEN OLD.state = 'cleaned'
		BEGIN SELECT RAISE(ABORT, 'cleaned Drydock is immutable'); END;`,
	`CREATE TRIGGER trg_drydock_workspace_delete_immutable
		BEFORE DELETE ON drydock_workspaces
		BEGIN SELECT RAISE(ABORT, 'Drydock audit cannot be deleted'); END;`,
	`CREATE TRIGGER trg_drydock_synthetic_workspace_update_immutable
		BEFORE UPDATE ON workspaces
		WHEN EXISTS (SELECT 1 FROM drydock_workspaces WHERE workspace_id = OLD.id)
			AND (NEW.id <> OLD.id OR NEW.name <> OLD.name OR NEW.root_path <> OLD.root_path)
		BEGIN SELECT RAISE(ABORT, 'Drydock synthetic Workspace identity is immutable'); END;`,
	`CREATE TRIGGER trg_drydock_mission_insert_scope
		BEFORE INSERT ON missions
		WHEN NEW.workspace_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM drydock_workspaces WHERE workspace_id = NEW.workspace_id)
		BEGIN SELECT RAISE(ABORT, 'Drydock Workspace cannot be bound to another Mission'); END;`,
	`CREATE TRIGGER trg_drydock_mission_update_scope
		BEFORE UPDATE OF workspace_id ON missions
		WHEN NEW.workspace_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM drydock_workspaces WHERE workspace_id = NEW.workspace_id)
		BEGIN SELECT RAISE(ABORT, 'Drydock Workspace cannot be rebound to a Mission'); END;`,
	`CREATE TRIGGER trg_drydock_session_insert_scope
		BEFORE INSERT ON sessions
		WHEN EXISTS (SELECT 1 FROM drydock_workspaces WHERE workspace_id = NEW.workspace_id)
		BEGIN SELECT RAISE(ABORT, 'Drydock Workspace cannot be bound to another Session'); END;`,
	`CREATE TRIGGER trg_drydock_session_update_scope
		BEFORE UPDATE OF workspace_id ON sessions
		WHEN EXISTS (SELECT 1 FROM drydock_workspaces WHERE workspace_id = NEW.workspace_id)
		BEGIN SELECT RAISE(ABORT, 'Drydock Workspace cannot be rebound to a Session'); END;`,

	`CREATE TABLE drydock_delivery_proposals (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_sha256 TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		drydock_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		generation INTEGER NOT NULL,
		source_identity_sha256 TEXT NOT NULL,
		root_fingerprint TEXT NOT NULL,
		base_commit TEXT NOT NULL,
		head_commit TEXT NOT NULL,
		merge_base_commit TEXT NOT NULL,
		binding_fingerprint TEXT NOT NULL,
		diff_sha256 TEXT NOT NULL,
		diff_bytes INTEGER NOT NULL,
		diff_stat TEXT NOT NULL,
		changed_paths_json TEXT NOT NULL,
		checkpoint_id TEXT NOT NULL,
		created_by TEXT NOT NULL,
		automatic_merge INTEGER NOT NULL,
		push_authorized INTEGER NOT NULL,
		force_authorized INTEGER NOT NULL,
		source_overwrite_allowed INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(drydock_id) REFERENCES drydock_workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(checkpoint_id) REFERENCES workspace_checkpoints(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'drydock-delivery-proposal.v1'),
		CHECK(length(operation_key_sha256) = 64 AND operation_key_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(generation > 0),
		CHECK(length(source_identity_sha256) = 64 AND source_identity_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(root_fingerprint) = 64 AND root_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(base_commit) IN (40,64) AND length(head_commit) IN (40,64)
			AND length(merge_base_commit) IN (40,64)),
		CHECK(length(binding_fingerprint) = 64 AND binding_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(diff_sha256) = 64 AND diff_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(diff_bytes BETWEEN 0 AND 16777216 AND length(diff_stat) <= 4096),
		CHECK(json_valid(changed_paths_json) AND json_type(changed_paths_json) = 'array'
			AND json_array_length(changed_paths_json) <= 3000),
		CHECK(length(created_by) BETWEEN 1 AND 256),
		CHECK(automatic_merge = 0 AND push_authorized = 0 AND force_authorized = 0
			AND source_overwrite_allowed = 0),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_drydock_delivery_run
		ON drydock_delivery_proposals(run_id, created_at DESC, id DESC);`,
	`CREATE TRIGGER trg_drydock_delivery_insert_scope
		BEFORE INSERT ON drydock_delivery_proposals
		WHEN NOT EXISTS (
			SELECT 1 FROM drydock_workspaces drydock
			JOIN workspace_checkpoints checkpoint ON checkpoint.id = NEW.checkpoint_id
			WHERE drydock.id = NEW.drydock_id AND drydock.run_id = NEW.run_id
				AND drydock.state IN ('ready','delivered')
				AND drydock.generation + 1 = NEW.generation
				AND drydock.source_identity_sha256 = NEW.source_identity_sha256
				AND drydock.root_fingerprint = NEW.root_fingerprint
				AND drydock.base_commit = NEW.base_commit
				AND drydock.base_commit = NEW.merge_base_commit
				AND drydock.expected_head = NEW.head_commit
				AND drydock.expected_binding_fingerprint = NEW.binding_fingerprint
				AND checkpoint.run_id = NEW.run_id
				AND checkpoint.workspace_id = drydock.workspace_id
		)
		BEGIN SELECT RAISE(ABORT, 'Drydock delivery scope is invalid'); END;`,
	`CREATE TRIGGER trg_drydock_delivery_update_immutable
		BEFORE UPDATE ON drydock_delivery_proposals BEGIN
			SELECT RAISE(ABORT, 'Drydock delivery proposals are immutable');
		END;`,
	`CREATE TRIGGER trg_drydock_delivery_delete_immutable
		BEFORE DELETE ON drydock_delivery_proposals BEGIN
			SELECT RAISE(ABORT, 'Drydock delivery proposals are immutable');
		END;`,

	`CREATE TABLE drydock_lifecycle_receipts (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_sha256 TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		drydock_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		operation TEXT NOT NULL,
		outcome TEXT NOT NULL,
		generation_before INTEGER NOT NULL,
		generation_after INTEGER NOT NULL,
		source_identity_sha256 TEXT NOT NULL,
		root_fingerprint TEXT NOT NULL DEFAULT '',
		binding_before_sha256 TEXT NOT NULL DEFAULT '',
		binding_after_sha256 TEXT NOT NULL DEFAULT '',
		git_receipt_id TEXT NOT NULL DEFAULT '',
		checkpoint_id TEXT NOT NULL DEFAULT '',
		delivery_id TEXT NOT NULL DEFAULT '',
		reason_code TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL,
		grants_process_authority INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(drydock_id) REFERENCES drydock_workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'drydock-lifecycle-receipt.v1'),
		CHECK(length(operation_key_sha256) = 64 AND operation_key_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(operation IN ('create','use','checkpoint','rewind','undo','fork','deliver','cleanup','recover')),
		CHECK(outcome IN ('succeeded','preserved','failed')),
		CHECK(generation_before > 0 AND generation_after = generation_before + 1),
		CHECK(length(source_identity_sha256) = 64 AND source_identity_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK((root_fingerprint = '' OR length(root_fingerprint) = 64)
			AND (binding_before_sha256 = '' OR length(binding_before_sha256) = 64)
			AND (binding_after_sha256 = '' OR length(binding_after_sha256) = 64)),
		CHECK(length(git_receipt_id) <= 256 AND length(checkpoint_id) <= 256
			AND length(delivery_id) <= 256 AND length(reason_code) <= 512),
		CHECK(length(summary) BETWEEN 1 AND 4096),
		CHECK(grants_process_authority = 0),
		CHECK((outcome = 'succeeded' AND reason_code = '') OR (outcome <> 'succeeded' AND length(reason_code) > 0)),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_drydock_receipts_timeline
		ON drydock_lifecycle_receipts(drydock_id, created_at, id);`,
	`CREATE TRIGGER trg_drydock_receipt_insert_scope
		BEFORE INSERT ON drydock_lifecycle_receipts
		WHEN NOT EXISTS (
			SELECT 1 FROM drydock_workspaces drydock
			WHERE drydock.id = NEW.drydock_id AND drydock.run_id = NEW.run_id
				AND drydock.generation = NEW.generation_after
				AND drydock.source_identity_sha256 = NEW.source_identity_sha256
				AND drydock.root_fingerprint = NEW.root_fingerprint
		) OR (NEW.checkpoint_id <> '' AND NOT EXISTS (
			SELECT 1 FROM workspace_checkpoints checkpoint
			JOIN drydock_workspaces drydock ON drydock.id = NEW.drydock_id
			WHERE checkpoint.id = NEW.checkpoint_id
				AND checkpoint.run_id = NEW.run_id
				AND checkpoint.workspace_id = drydock.workspace_id
		)) OR (NEW.delivery_id <> '' AND NOT EXISTS (
			SELECT 1 FROM drydock_delivery_proposals proposal
			WHERE proposal.id = NEW.delivery_id
				AND proposal.drydock_id = NEW.drydock_id
				AND proposal.run_id = NEW.run_id
		))
		BEGIN SELECT RAISE(ABORT, 'Drydock lifecycle receipt scope is invalid'); END;`,
	`CREATE TRIGGER trg_drydock_receipt_update_immutable
		BEFORE UPDATE ON drydock_lifecycle_receipts BEGIN
			SELECT RAISE(ABORT, 'Drydock lifecycle receipts are immutable');
		END;`,
	`CREATE TRIGGER trg_drydock_receipt_delete_immutable
		BEFORE DELETE ON drydock_lifecycle_receipts BEGIN
			SELECT RAISE(ABORT, 'Drydock lifecycle receipts are immutable');
		END;`,

	// Checkpoints normally bind to the source Workspace carried by the Run. A
	// Drydock has a distinct, synthetic Workspace identity, so extend the old
	// scope guard with an exact Run/Mission/Session/Drydock ownership binding.
	`DROP TRIGGER trg_workspace_checkpoint_insert_scope;`,
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
		) AND NOT EXISTS (
			SELECT 1 FROM drydock_workspaces drydock
			WHERE drydock.run_id = NEW.run_id
				AND drydock.mission_id = NEW.mission_id
				AND drydock.session_id = NEW.session_id
				AND drydock.workspace_id = NEW.workspace_id
				AND drydock.state <> 'cleaned'
		)
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint Run binding is invalid'); END;`,
}
