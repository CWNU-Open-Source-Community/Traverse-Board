package store

var controlledCommandProposalStatements = []string{
	`CREATE TABLE controlled_command_proposals (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		root_agent_id TEXT NOT NULL,
		interaction_snapshot_id TEXT NOT NULL,
		interaction_revision INTEGER NOT NULL,
		execution_profile_revision INTEGER NOT NULL,
		permission_snapshot_id TEXT NOT NULL,
		permission_revision INTEGER NOT NULL,
		permission_mode TEXT NOT NULL,
		plan_id TEXT NOT NULL UNIQUE,
		plan_fingerprint TEXT NOT NULL UNIQUE,
		kind TEXT NOT NULL,
		relative_path TEXT NOT NULL,
		timeout_millis INTEGER NOT NULL,
		purpose TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		instruction_authorized INTEGER NOT NULL,
		execution_authorized INTEGER NOT NULL,
		capability_grant INTEGER NOT NULL,
		proposal_fingerprint TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(root_agent_id) REFERENCES agent_nodes(id) ON DELETE RESTRICT,
		FOREIGN KEY(interaction_snapshot_id)
			REFERENCES run_execution_interaction_snapshots(id) ON DELETE RESTRICT,
		FOREIGN KEY(permission_snapshot_id)
			REFERENCES run_execution_permission_snapshots(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'controlled_command_proposal.v1'),
		CHECK(policy_version = 'controlled_command_proposal_policy.v1'),
		CHECK(interaction_revision > 0 AND execution_profile_revision > 0
			AND permission_revision > 0),
		CHECK(permission_mode IN ('conservative', 'approval', 'full_access', 'debug')),
		CHECK(kind IN ('git-status', 'git-diff-check', 'go-version',
			'powershell-workspace-list')),
		CHECK(timeout_millis BETWEEN 1 AND 120000),
		CHECK(length(relative_path) <= 512 AND instr(relative_path, char(0)) = 0),
		CHECK(purpose = trim(purpose) AND length(purpose) BETWEEN 1 AND 1200
			AND instr(purpose, char(0)) = 0),
		CHECK(requested_by = 'run_supervisor'),
		CHECK(instruction_authorized = 0 AND execution_authorized = 0
			AND capability_grant = 0),
		CHECK(length(plan_fingerprint) = 64
			AND plan_fingerprint = lower(plan_fingerprint)
			AND plan_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(proposal_fingerprint) = 64
			AND proposal_fingerprint = lower(proposal_fingerprint)
			AND proposal_fingerprint NOT GLOB '*[^0-9a-f]*')
	);`,
	`CREATE INDEX idx_controlled_command_proposals_run_created
		ON controlled_command_proposals(run_id, created_at DESC, id DESC);`,
	`CREATE TABLE controlled_command_proposal_operations (
		operation_key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		invocation_id TEXT NOT NULL UNIQUE,
		proposal_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		root_agent_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(invocation_id) REFERENCES run_tool_calls(id) ON DELETE RESTRICT,
		FOREIGN KEY(proposal_id)
			REFERENCES controlled_command_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(root_agent_id) REFERENCES agent_nodes(id) ON DELETE RESTRICT,
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(requested_by = 'run_supervisor')
	) WITHOUT ROWID;`,
	`CREATE TABLE controlled_command_proposal_reviews (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		proposal_id TEXT NOT NULL UNIQUE,
		proposal_fingerprint TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		decision TEXT NOT NULL,
		reviewed_by TEXT NOT NULL,
		reason TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		single_use_execution_authorized INTEGER NOT NULL,
		capability_grant INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(proposal_id)
			REFERENCES controlled_command_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'controlled_command_proposal_review.v1'),
		CHECK(policy_version = 'controlled_command_proposal_policy.v1'),
		CHECK(decision IN ('approve', 'deny')),
		CHECK(single_use_execution_authorized =
			CASE WHEN decision = 'approve' THEN 1 ELSE 0 END),
		CHECK(capability_grant = 0),
		CHECK(reviewed_by = trim(reviewed_by)
			AND length(reviewed_by) BETWEEN 1 AND 256
			AND instr(reviewed_by, char(0)) = 0
			AND lower(reviewed_by) NOT IN
				('agent', 'llm', 'model', 'repository', 'repo', 'skill',
				'supervisor', 'run_supervisor')),
		CHECK(reason = trim(reason) AND length(reason) BETWEEN 1 AND 1024
			AND instr(reason, char(0)) = 0),
		CHECK(length(proposal_fingerprint) = 64
			AND proposal_fingerprint = lower(proposal_fingerprint)
			AND proposal_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*')
	);`,
	`CREATE INDEX idx_controlled_command_reviews_run_created
		ON controlled_command_proposal_reviews(run_id, created_at DESC, id DESC);`,
	`CREATE TABLE controlled_command_proposal_results (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		proposal_id TEXT NOT NULL UNIQUE,
		proposal_fingerprint TEXT NOT NULL,
		review_id TEXT NOT NULL UNIQUE,
		request_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		session_message_id INTEGER NOT NULL UNIQUE,
		status TEXT NOT NULL,
		source_kind TEXT NOT NULL,
		source_ref TEXT NOT NULL,
		content_sha256 TEXT NOT NULL,
		instruction_authorized INTEGER NOT NULL,
		raw_output_persisted INTEGER NOT NULL,
		automatic_retry_allowed INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(proposal_id)
			REFERENCES controlled_command_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(review_id)
			REFERENCES controlled_command_proposal_reviews(id) ON DELETE RESTRICT,
		FOREIGN KEY(request_id)
			REFERENCES controlled_command_execution_receipts(request_id)
				ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_message_id)
			REFERENCES session_messages(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'controlled_command_proposal_result.v1'),
		CHECK(policy_version = 'controlled_command_proposal_policy.v1'),
		CHECK(status IN ('completed', 'failed')),
		CHECK(source_kind = 'go_command_result'),
		CHECK(source_ref = trim(source_ref) AND length(source_ref) BETWEEN 1 AND 512
			AND instr(source_ref, char(0)) = 0),
		CHECK(length(proposal_fingerprint) = 64
			AND proposal_fingerprint = lower(proposal_fingerprint)
			AND proposal_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(content_sha256) = 64
			AND content_sha256 = lower(content_sha256)
			AND content_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(instruction_authorized = 0 AND raw_output_persisted = 0
			AND automatic_retry_allowed = 0)
	);`,
	`CREATE TRIGGER trg_controlled_command_proposal_insert_binding
		BEFORE INSERT ON controlled_command_proposals
		WHEN NOT EXISTS (
			SELECT 1
			FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN agent_nodes root ON root.id = NEW.root_agent_id
			JOIN run_execution_interaction_snapshots interaction
				ON interaction.id = NEW.interaction_snapshot_id
			JOIN run_execution_permission_snapshots permission
				ON permission.id = NEW.permission_snapshot_id
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND run.session_id = NEW.session_id AND run.status = 'running'
				AND mission.workspace_id = NEW.workspace_id
				AND root.run_id = NEW.run_id AND root.role = 'root'
				AND root.parent_id = '' AND root.status = 'running'
				AND interaction.run_id = NEW.run_id
				AND interaction.revision = NEW.interaction_revision
				AND interaction.execution_profile_revision =
					NEW.execution_profile_revision
				AND interaction.mode = 'controlled'
				AND permission.run_id = NEW.run_id
				AND permission.mission_id = NEW.mission_id
				AND permission.revision = NEW.permission_revision
				AND permission.mode = NEW.permission_mode
		)
		BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal binding is invalid');
		END;`,
	`CREATE TRIGGER trg_controlled_command_proposal_operation_insert_binding
		BEFORE INSERT ON controlled_command_proposal_operations
		WHEN NOT EXISTS (
			SELECT 1
			FROM controlled_command_proposals proposal
			JOIN run_tool_calls invocation ON invocation.id = NEW.invocation_id
			WHERE proposal.id = NEW.proposal_id
				AND proposal.run_id = NEW.run_id
				AND proposal.session_id = NEW.session_id
				AND proposal.workspace_id = NEW.workspace_id
				AND proposal.root_agent_id = NEW.root_agent_id
				AND proposal.requested_by = NEW.requested_by
				AND invocation.run_id = NEW.run_id
				AND invocation.session_id = NEW.session_id
				AND invocation.workspace_id = NEW.workspace_id
				AND invocation.tool_name = 'controlled_command_propose'
				AND invocation.action_class = 'agent_proposal'
		)
		BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal operation binding is invalid');
		END;`,
	`CREATE TRIGGER trg_controlled_command_review_insert_binding
		BEFORE INSERT ON controlled_command_proposal_reviews
		WHEN NOT EXISTS (
			SELECT 1
			FROM controlled_command_proposals proposal
			JOIN runs run ON run.id = proposal.run_id
			WHERE proposal.id = NEW.proposal_id
				AND proposal.proposal_fingerprint = NEW.proposal_fingerprint
				AND proposal.run_id = NEW.run_id
				AND proposal.mission_id = NEW.mission_id
				AND proposal.session_id = NEW.session_id
				AND proposal.workspace_id = NEW.workspace_id
				AND run.status IN ('created', 'paused')
				AND NOT EXISTS (
					SELECT 1 FROM run_execution_leases lease
					WHERE lease.run_id = NEW.run_id AND lease.status = 'active'
						AND julianday(lease.expires_at) > julianday('now')
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal review binding is invalid');
		END;`,
	`CREATE TRIGGER trg_controlled_command_result_insert_binding
		BEFORE INSERT ON controlled_command_proposal_results
		WHEN NOT EXISTS (
			SELECT 1
			FROM controlled_command_proposals proposal
			JOIN controlled_command_proposal_reviews review
				ON review.id = NEW.review_id
			JOIN controlled_command_execution_receipts receipt
				ON receipt.request_id = NEW.request_id
			JOIN session_messages message
				ON message.id = NEW.session_message_id
			WHERE proposal.id = NEW.proposal_id
				AND proposal.proposal_fingerprint = NEW.proposal_fingerprint
				AND proposal.run_id = NEW.run_id
				AND proposal.mission_id = NEW.mission_id
				AND proposal.session_id = NEW.session_id
				AND proposal.workspace_id = NEW.workspace_id
				AND review.proposal_id = proposal.id
				AND review.decision = 'approve'
				AND review.single_use_execution_authorized = 1
				AND message.session_id = NEW.session_id
				AND message.source_kind = NEW.source_kind
				AND message.source_ref = NEW.source_ref
				AND message.content_sha256 = NEW.content_sha256
				AND message.instruction_authorized = 0
		)
		BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal result binding is invalid');
		END;`,
	`CREATE TRIGGER trg_controlled_command_proposal_update_immutable
		BEFORE UPDATE ON controlled_command_proposals BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal cannot be updated');
		END;`,
	`CREATE TRIGGER trg_controlled_command_proposal_delete_immutable
		BEFORE DELETE ON controlled_command_proposals BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_controlled_command_proposal_operation_update_immutable
		BEFORE UPDATE ON controlled_command_proposal_operations BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal operation cannot be updated');
		END;`,
	`CREATE TRIGGER trg_controlled_command_proposal_operation_delete_immutable
		BEFORE DELETE ON controlled_command_proposal_operations BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal operation cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_controlled_command_review_update_immutable
		BEFORE UPDATE ON controlled_command_proposal_reviews BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal review cannot be updated');
		END;`,
	`CREATE TRIGGER trg_controlled_command_review_delete_immutable
		BEFORE DELETE ON controlled_command_proposal_reviews BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal review cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_controlled_command_result_update_immutable
		BEFORE UPDATE ON controlled_command_proposal_results BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal result cannot be updated');
		END;`,
	`CREATE TRIGGER trg_controlled_command_result_delete_immutable
		BEFORE DELETE ON controlled_command_proposal_results BEGIN
			SELECT RAISE(ABORT, 'controlled command proposal result cannot be deleted');
		END;`,
}
