package store

// Automatic FileEdit authorization has its own immutable provenance. The
// existing per-call operator approval path remains unchanged.
var automaticFileEditAuthorizationStatements = []string{
	`CREATE TABLE file_edit_auto_authorizations (
		edit_id TEXT PRIMARY KEY REFERENCES file_edits(id) ON DELETE RESTRICT,
		operation_key_digest TEXT NOT NULL UNIQUE CHECK(length(operation_key_digest)=64),
		proposal_fingerprint TEXT NOT NULL CHECK(length(proposal_fingerprint)=64),
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
		operation_kind TEXT NOT NULL CHECK(operation_kind IN ('create','replace')),
		path TEXT NOT NULL,
		original_hash TEXT NOT NULL,
		proposed_hash TEXT NOT NULL,
		permission_snapshot_id TEXT NOT NULL REFERENCES run_execution_permission_snapshots(id) ON DELETE RESTRICT,
		permission_revision INTEGER NOT NULL CHECK(permission_revision > 0),
		mode_revision INTEGER NOT NULL CHECK(mode_revision > 0),
		runtime_epoch TEXT NOT NULL CHECK(length(runtime_epoch) BETWEEN 1 AND 256),
		runtime_generation INTEGER NOT NULL CHECK(runtime_generation > 0),
		agent_id TEXT NOT NULL REFERENCES agent_nodes(id) ON DELETE RESTRICT,
		capability_generation TEXT NOT NULL CHECK(length(capability_generation)=64),
		lease_id TEXT NOT NULL CHECK(length(lease_id) BETWEEN 1 AND 256),
		lease_generation INTEGER NOT NULL CHECK(lease_generation > 0),
		created_at TEXT NOT NULL
	);`,
	`CREATE INDEX idx_file_edit_auto_authorizations_run_created
		ON file_edit_auto_authorizations(run_id, created_at);`,
	`CREATE TRIGGER trg_file_edit_auto_authorization_insert
		BEFORE INSERT ON file_edit_auto_authorizations
		WHEN NOT EXISTS (
			SELECT 1 FROM file_edits edit
			JOIN runs run ON run.id=NEW.run_id AND run.session_id=NEW.session_id
			JOIN missions mission ON mission.id=run.mission_id
			JOIN sessions session_record ON session_record.id=run.session_id
			JOIN run_execution_permission_snapshots permission
				ON permission.id=NEW.permission_snapshot_id AND permission.run_id=run.id
			JOIN run_mode_snapshots mode ON mode.run_id=run.id AND mode.revision=NEW.mode_revision
			JOIN agent_nodes agent ON agent.id=NEW.agent_id AND agent.run_id=run.id
			JOIN run_execution_leases lease ON lease.run_id=run.id
			WHERE edit.id=NEW.edit_id AND edit.session_id=NEW.session_id
				AND edit.workspace_id=NEW.workspace_id AND edit.status='approved'
				AND edit.operation_kind=NEW.operation_kind AND edit.path=NEW.path
				AND edit.destination_path='' AND edit.original_hash=NEW.original_hash
				AND edit.proposed_hash=NEW.proposed_hash
				AND edit.destination_original_hash='' AND edit.destination_proposed_hash=''
				AND NOT EXISTS (SELECT 1 FROM tool_approvals prior WHERE prior.proposal_id=edit.id)
				AND run.status='running' AND session_record.status='active'
				AND session_record.workspace_id=mission.workspace_id
				AND permission.mission_id=mission.id AND permission.mode='full_access'
				AND permission.revision=NEW.permission_revision
				AND NOT EXISTS (SELECT 1 FROM run_execution_permission_snapshots later
					WHERE later.run_id=run.id AND later.revision>permission.revision)
				AND mode.mission_id=mission.id AND mode.surface='code' AND mode.phase='deliver'
				AND NOT EXISTS (SELECT 1 FROM run_mode_snapshots later
					WHERE later.run_id=run.id AND later.revision>mode.revision)
				AND agent.session_id=run.session_id AND agent.role='root'
				AND agent.parent_id IS NULL AND agent.depth=0
				AND agent.status IN ('ready','running','waiting')
				AND lease.lease_id=NEW.lease_id AND lease.generation=NEW.lease_generation
				AND lease.status='active' AND julianday(lease.expires_at)>julianday('now')
				AND NEW.created_at=edit.created_at
		)
		BEGIN SELECT RAISE(ABORT, 'automatic FileEdit source binding is invalid'); END;`,
	`CREATE TRIGGER trg_file_edit_auto_authorization_update
		BEFORE UPDATE ON file_edit_auto_authorizations BEGIN
			SELECT RAISE(ABORT, 'automatic FileEdit source is immutable'); END;`,
	`CREATE TRIGGER trg_file_edit_auto_authorization_delete
		BEFORE DELETE ON file_edit_auto_authorizations BEGIN
			SELECT RAISE(ABORT, 'automatic FileEdit source is immutable'); END;`,
	`CREATE TRIGGER trg_file_edit_auto_content_update
		BEFORE UPDATE ON file_edits
		WHEN EXISTS (SELECT 1 FROM file_edit_auto_authorizations source WHERE source.edit_id=OLD.id)
			AND (NEW.id<>OLD.id OR NEW.session_id<>OLD.session_id
			OR NEW.workspace_id<>OLD.workspace_id OR NEW.path<>OLD.path
			OR NEW.operation_kind<>OLD.operation_kind OR NEW.destination_path<>OLD.destination_path
			OR NEW.original_text<>OLD.original_text OR NEW.proposed_text<>OLD.proposed_text
			OR NEW.diff_text<>OLD.diff_text OR NEW.original_hash<>OLD.original_hash
			OR NEW.proposed_hash<>OLD.proposed_hash
			OR NEW.destination_original_hash<>OLD.destination_original_hash
			OR NEW.destination_proposed_hash<>OLD.destination_proposed_hash
			OR NEW.secrets_redacted<>OLD.secrets_redacted OR NEW.created_at<>OLD.created_at
			OR NEW.status NOT IN ('approved','applied','failed')
			OR (OLD.status='applied' AND NEW.status<>'applied')
			OR (OLD.status='failed' AND NEW.status<>'failed'))
		BEGIN SELECT RAISE(ABORT, 'automatic FileEdit proposal is immutable'); END;`,
	`CREATE TRIGGER trg_file_edit_auto_approval_insert
		BEFORE INSERT ON tool_approvals
		WHEN ((NEW.mode='automatic' AND NEW.tool_name IN
			('create_file','replace_file','move_file','delete_file')) OR EXISTS (
			SELECT 1 FROM file_edit_auto_authorizations source WHERE source.edit_id=NEW.proposal_id))
			AND NOT EXISTS (
				SELECT 1 FROM file_edit_auto_authorizations source
				WHERE source.edit_id=NEW.proposal_id AND source.run_id=NEW.run_id
					AND source.session_id=NEW.session_id AND source.workspace_id=NEW.workspace_id
					AND NEW.mode='automatic' AND NEW.status='approved'
					AND NEW.reviewed_by='automatic_policy'
					AND NEW.action_class='workspace_write'
					AND NEW.tool_name=CASE source.operation_kind
						WHEN 'create' THEN 'create_file' ELSE 'replace_file' END)
		BEGIN SELECT RAISE(ABORT, 'automatic FileEdit approval source is invalid'); END;`,
	`CREATE TRIGGER trg_file_edit_auto_approval_update
		BEFORE UPDATE ON tool_approvals
		WHEN EXISTS (SELECT 1 FROM file_edit_auto_authorizations source WHERE source.edit_id=OLD.proposal_id)
			AND (NEW.mode<>'automatic' OR NEW.status<>'approved'
			OR NEW.reviewed_by<>'automatic_policy' OR NEW.proposal_id<>OLD.proposal_id
			OR NEW.run_id<>OLD.run_id OR NEW.session_id<>OLD.session_id
			OR NEW.workspace_id<>OLD.workspace_id OR NEW.tool_name<>OLD.tool_name
			OR NEW.action_class<>OLD.action_class
			OR NEW.request_fingerprint<>OLD.request_fingerprint)
		BEGIN SELECT RAISE(ABORT, 'automatic FileEdit approval source is immutable'); END;`,
	`CREATE TRIGGER trg_file_edit_auto_apply_insert
		BEFORE INSERT ON file_edit_apply_operations
		WHEN (EXISTS (SELECT 1 FROM file_edit_auto_authorizations source
			WHERE source.edit_id=NEW.edit_id)
			OR EXISTS (SELECT 1 FROM tool_approvals approval
			WHERE approval.proposal_id=NEW.edit_id AND approval.mode='automatic'))
			AND NOT EXISTS (
				SELECT 1 FROM file_edit_auto_authorizations source
				JOIN file_edits edit ON edit.id=source.edit_id
				JOIN tool_approvals approval ON approval.proposal_id=edit.id
				JOIN runs run ON run.id=source.run_id
				JOIN run_execution_permission_snapshots permission
					ON permission.id=source.permission_snapshot_id AND permission.run_id=run.id
				JOIN run_mode_snapshots mode
					ON mode.run_id=run.id AND mode.revision=source.mode_revision
				JOIN run_execution_leases lease ON lease.run_id=run.id
				WHERE source.edit_id=NEW.edit_id AND source.run_id=NEW.run_id
					AND source.session_id=NEW.session_id AND source.workspace_id=NEW.workspace_id
					AND source.operation_kind=NEW.operation_kind AND source.path=NEW.path
					AND source.original_hash=NEW.original_hash AND source.proposed_hash=NEW.proposed_hash
					AND NEW.applied_by=source.agent_id
					AND edit.status='approved' AND run.status='running'
					AND approval.run_id=run.id AND approval.mode='automatic'
					AND approval.status='approved' AND approval.reviewed_by='automatic_policy'
					AND permission.mode='full_access'
					AND permission.revision=source.permission_revision
					AND NOT EXISTS (SELECT 1 FROM run_execution_permission_snapshots later
						WHERE later.run_id=run.id AND later.revision>permission.revision)
					AND mode.surface='code' AND mode.phase='deliver'
					AND NOT EXISTS (SELECT 1 FROM run_mode_snapshots later
						WHERE later.run_id=run.id AND later.revision>mode.revision)
					AND lease.status='active' AND julianday(lease.expires_at)>julianday('now')
			)
		BEGIN SELECT RAISE(ABORT, 'automatic FileEdit apply authority is invalid'); END;`,
}
