package store

var hostCommandProposalStatements = []string{
	`DROP TRIGGER trg_controlled_command_proposal_insert_binding;`,
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
				AND root.parent_id IS NULL AND root.status = 'running'
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
	`DROP TRIGGER trg_supervisor_tool_call_model_attempt;`,
	`DROP TRIGGER trg_supervisor_tool_round_completion;`,
	`DROP INDEX idx_run_supervisor_tool_calls_pending;`,
	`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v95;`,
	`CREATE TABLE run_supervisor_tool_calls (
		run_id TEXT NOT NULL,
		turn INTEGER NOT NULL,
		attempt_id TEXT NOT NULL,
		round INTEGER NOT NULL,
		position INTEGER NOT NULL,
		model_attempt INTEGER NOT NULL,
		call_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		status TEXT NOT NULL,
		result_json TEXT NOT NULL DEFAULT '',
		error_code TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		completed_at TEXT,
		PRIMARY KEY(run_id, turn, attempt_id, round, position),
		UNIQUE(run_id, turn, attempt_id, call_id),
		FOREIGN KEY(run_id, turn, attempt_id, round)
			REFERENCES run_supervisor_tool_rounds(run_id, turn, attempt_id, round) ON DELETE CASCADE,
		CHECK(position BETWEEN 1 AND 4),
		CHECK(model_attempt > 0),
		CHECK(tool_name IN ('work_item_create', 'note_create',
			'specialist_delegation_propose', 'plan_delivery_propose',
			'controlled_command_propose', 'host_command_propose')),
		CHECK(status IN ('pending', 'completed', 'denied', 'failed')),
		CHECK((status = 'pending' AND result_json = '' AND error_code = '' AND completed_at IS NULL)
			OR (status = 'completed' AND length(result_json) > 0 AND error_code = '' AND completed_at IS NOT NULL)
			OR (status IN ('denied', 'failed') AND length(result_json) > 0 AND length(error_code) > 0
				AND completed_at IS NOT NULL))
	);`,
	`INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		payload_json, status, result_json, error_code, created_at, completed_at)
		SELECT run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		payload_json, status, result_json, error_code, created_at, completed_at
		FROM run_supervisor_tool_calls_v95;`,
	`DROP TABLE run_supervisor_tool_calls_v95;`,
	`CREATE INDEX idx_run_supervisor_tool_calls_pending
		ON run_supervisor_tool_calls(run_id, turn, attempt_id, status, round, position);`,
	`CREATE TRIGGER trg_supervisor_tool_call_model_attempt
		BEFORE INSERT ON run_supervisor_tool_calls
		WHEN NOT EXISTS (
			SELECT 1 FROM run_supervisor_tool_rounds
			WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
				AND round = NEW.round AND model_attempt = NEW.model_attempt
		)
		BEGIN
			SELECT RAISE(ABORT, 'supervisor tool call model attempt mismatch');
		END;`,
	`CREATE TRIGGER trg_supervisor_tool_round_completion
		BEFORE UPDATE OF completed_at ON run_supervisor_tool_rounds
		WHEN NEW.completed_at IS NOT NULL AND EXISTS (
			SELECT 1 FROM run_supervisor_tool_calls
			WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
				AND round = NEW.round AND status = 'pending'
		)
		BEGIN
			SELECT RAISE(ABORT, 'supervisor tool round still has pending calls');
		END;`,
	`CREATE TABLE host_command_proposals (
		id TEXT PRIMARY KEY,
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
		spec_fingerprint TEXT NOT NULL UNIQUE,
		requested_by TEXT NOT NULL,
		instruction_authorized INTEGER NOT NULL,
		execution_authorized INTEGER NOT NULL,
		capability_grant INTEGER NOT NULL,
		proposal_fingerprint TEXT NOT NULL UNIQUE,
		payload_json TEXT NOT NULL,
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
		CHECK(permission_mode = 'approval'),
		CHECK(interaction_revision > 0 AND execution_profile_revision > 0
			AND permission_revision > 0),
		CHECK(requested_by = 'run_supervisor'),
		CHECK(instruction_authorized = 0 AND execution_authorized = 0
			AND capability_grant = 0),
		CHECK(length(spec_fingerprint) = 64
			AND spec_fingerprint = lower(spec_fingerprint)
			AND spec_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(proposal_fingerprint) = 64
			AND proposal_fingerprint = lower(proposal_fingerprint)
			AND proposal_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json))
	);`,
	`CREATE INDEX idx_host_command_proposals_run_created
		ON host_command_proposals(run_id, created_at DESC, id DESC);`,
	`CREATE TABLE host_command_proposal_operations (
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
		FOREIGN KEY(proposal_id) REFERENCES host_command_proposals(id) ON DELETE RESTRICT,
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
	`CREATE TABLE host_command_proposal_reviews (
		id TEXT PRIMARY KEY,
		proposal_id TEXT NOT NULL UNIQUE,
		proposal_fingerprint TEXT NOT NULL,
		run_id TEXT NOT NULL,
		decision TEXT NOT NULL,
		reviewed_by TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		single_use_execution_authorized INTEGER NOT NULL,
		capability_grant INTEGER NOT NULL,
		review_fingerprint TEXT NOT NULL UNIQUE,
		payload_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(proposal_id) REFERENCES host_command_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
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
		CHECK(length(proposal_fingerprint) = 64
			AND proposal_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(review_fingerprint) = 64
			AND review_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json))
	);`,
	`CREATE INDEX idx_host_command_reviews_run_created
		ON host_command_proposal_reviews(run_id, created_at DESC, id DESC);`,
	`CREATE TABLE host_command_proposal_execution_intents (
		request_id TEXT PRIMARY KEY,
		proposal_id TEXT NOT NULL UNIQUE,
		review_id TEXT NOT NULL UNIQUE,
		operation_key_digest TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		permission_mode TEXT NOT NULL,
		spec_fingerprint TEXT NOT NULL,
		intent_fingerprint TEXT NOT NULL UNIQUE,
		non_sandboxed INTEGER NOT NULL,
		automatic_retry_allowed INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(proposal_id) REFERENCES host_command_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(review_id) REFERENCES host_command_proposal_reviews(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(permission_mode = 'approval'),
		CHECK(non_sandboxed = 1 AND automatic_retry_allowed = 0),
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(spec_fingerprint) = 64
			AND spec_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(intent_fingerprint) = 64
			AND intent_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json))
	);`,
	`CREATE TABLE host_command_proposal_results (
		id TEXT PRIMARY KEY,
		proposal_id TEXT NOT NULL UNIQUE,
		review_id TEXT NOT NULL UNIQUE,
		request_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		session_message_id INTEGER NOT NULL UNIQUE,
		status TEXT NOT NULL,
		result_fingerprint TEXT NOT NULL UNIQUE,
		receipt_fingerprint TEXT NOT NULL UNIQUE,
		result_json TEXT NOT NULL,
		receipt_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(proposal_id) REFERENCES host_command_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(review_id) REFERENCES host_command_proposal_reviews(id) ON DELETE RESTRICT,
		FOREIGN KEY(request_id) REFERENCES host_command_proposal_execution_intents(request_id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_message_id) REFERENCES session_messages(id) ON DELETE RESTRICT,
		CHECK(status IN ('completed', 'failed')),
		CHECK(length(result_fingerprint) = 64
			AND result_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(receipt_fingerprint) = 64
			AND receipt_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(result_json) AND json_valid(receipt_json))
	);`,
	`CREATE TRIGGER trg_host_command_proposal_insert_binding
		BEFORE INSERT ON host_command_proposals
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
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
				AND root.parent_id IS NULL AND root.status = 'running'
				AND interaction.run_id = NEW.run_id
				AND interaction.revision = NEW.interaction_revision
				AND interaction.execution_profile_revision = NEW.execution_profile_revision
				AND interaction.mode = 'controlled'
				AND permission.run_id = NEW.run_id
				AND permission.revision = NEW.permission_revision
				AND permission.mode = 'approval'
		)
		BEGIN SELECT RAISE(ABORT, 'host command proposal binding is invalid'); END;`,
	`CREATE TRIGGER trg_host_command_proposal_operation_insert_binding
		BEFORE INSERT ON host_command_proposal_operations
		WHEN NOT EXISTS (
			SELECT 1 FROM host_command_proposals proposal
			JOIN run_tool_calls invocation ON invocation.id = NEW.invocation_id
			WHERE proposal.id = NEW.proposal_id
				AND proposal.run_id = NEW.run_id
				AND proposal.session_id = NEW.session_id
				AND proposal.workspace_id = NEW.workspace_id
				AND proposal.root_agent_id = NEW.root_agent_id
				AND proposal.requested_by = NEW.requested_by
				AND invocation.run_id = NEW.run_id
				AND invocation.tool_name = 'host_command_propose'
				AND invocation.action_class = 'agent_proposal'
		)
		BEGIN SELECT RAISE(ABORT, 'host command proposal operation binding is invalid'); END;`,
	`CREATE TRIGGER trg_host_command_review_insert_binding
		BEFORE INSERT ON host_command_proposal_reviews
		WHEN NOT EXISTS (
			SELECT 1 FROM host_command_proposals proposal
			JOIN runs run ON run.id = proposal.run_id
			WHERE proposal.id = NEW.proposal_id
				AND proposal.proposal_fingerprint = NEW.proposal_fingerprint
				AND proposal.run_id = NEW.run_id
				AND run.status IN ('created', 'paused')
				AND NOT EXISTS (
					SELECT 1 FROM run_execution_leases lease
					WHERE lease.run_id = NEW.run_id AND lease.status = 'active'
						AND julianday(lease.expires_at) > julianday('now'))
		)
		BEGIN SELECT RAISE(ABORT, 'host command review binding is invalid'); END;`,
	`CREATE TRIGGER trg_host_command_intent_insert_binding
		BEFORE INSERT ON host_command_proposal_execution_intents
		WHEN NOT EXISTS (
			SELECT 1 FROM host_command_proposals proposal
			JOIN host_command_proposal_reviews review ON review.id = NEW.review_id
			WHERE proposal.id = NEW.proposal_id
				AND proposal.run_id = NEW.run_id
				AND proposal.session_id = NEW.session_id
				AND proposal.workspace_id = NEW.workspace_id
				AND proposal.spec_fingerprint = NEW.spec_fingerprint
				AND review.proposal_id = proposal.id
				AND review.decision = 'approve'
				AND review.single_use_execution_authorized = 1
		)
		BEGIN SELECT RAISE(ABORT, 'host command execution intent binding is invalid'); END;`,
	`CREATE TRIGGER trg_host_command_result_insert_binding
		BEFORE INSERT ON host_command_proposal_results
		WHEN NOT EXISTS (
			SELECT 1 FROM host_command_proposals proposal
			JOIN host_command_proposal_reviews review ON review.id = NEW.review_id
			JOIN host_command_proposal_execution_intents intent
				ON intent.request_id = NEW.request_id
			JOIN session_messages message ON message.id = NEW.session_message_id
			WHERE proposal.id = NEW.proposal_id
				AND proposal.run_id = NEW.run_id
				AND proposal.session_id = NEW.session_id
				AND review.proposal_id = proposal.id AND review.decision = 'approve'
				AND intent.proposal_id = proposal.id AND intent.review_id = review.id
				AND message.session_id = NEW.session_id
				AND message.instruction_authorized = 0
		)
		BEGIN SELECT RAISE(ABORT, 'host command result binding is invalid'); END;`,
	`CREATE TRIGGER trg_host_command_proposal_update_immutable BEFORE UPDATE ON host_command_proposals BEGIN SELECT RAISE(ABORT, 'host command proposal cannot be updated'); END;`,
	`CREATE TRIGGER trg_host_command_proposal_delete_immutable BEFORE DELETE ON host_command_proposals BEGIN SELECT RAISE(ABORT, 'host command proposal cannot be deleted'); END;`,
	`CREATE TRIGGER trg_host_command_proposal_operation_update_immutable BEFORE UPDATE ON host_command_proposal_operations BEGIN SELECT RAISE(ABORT, 'host command proposal operation cannot be updated'); END;`,
	`CREATE TRIGGER trg_host_command_proposal_operation_delete_immutable BEFORE DELETE ON host_command_proposal_operations BEGIN SELECT RAISE(ABORT, 'host command proposal operation cannot be deleted'); END;`,
	`CREATE TRIGGER trg_host_command_review_update_immutable BEFORE UPDATE ON host_command_proposal_reviews BEGIN SELECT RAISE(ABORT, 'host command review cannot be updated'); END;`,
	`CREATE TRIGGER trg_host_command_review_delete_immutable BEFORE DELETE ON host_command_proposal_reviews BEGIN SELECT RAISE(ABORT, 'host command review cannot be deleted'); END;`,
	`CREATE TRIGGER trg_host_command_intent_update_immutable BEFORE UPDATE ON host_command_proposal_execution_intents BEGIN SELECT RAISE(ABORT, 'host command execution intent cannot be updated'); END;`,
	`CREATE TRIGGER trg_host_command_intent_delete_immutable BEFORE DELETE ON host_command_proposal_execution_intents BEGIN SELECT RAISE(ABORT, 'host command execution intent cannot be deleted'); END;`,
	`CREATE TRIGGER trg_host_command_result_update_immutable BEFORE UPDATE ON host_command_proposal_results BEGIN SELECT RAISE(ABORT, 'host command result cannot be updated'); END;`,
	`CREATE TRIGGER trg_host_command_result_delete_immutable BEFORE DELETE ON host_command_proposal_results BEGIN SELECT RAISE(ABORT, 'host command result cannot be deleted'); END;`,
}
