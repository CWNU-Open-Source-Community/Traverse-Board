package store

// agentCodeToolStatements adds operation-aware FileEdit state and widens the
// Supervisor ledger to the versioned agent-code-tools.v1 registry. Existing
// replacement edits and apply receipts are copied as operation_kind=replace.
var agentCodeToolStatements = []string{
	`ALTER TABLE file_edits ADD COLUMN operation_kind TEXT NOT NULL DEFAULT 'replace'
		CHECK(operation_kind IN ('replace', 'create', 'move', 'delete'));`,
	`ALTER TABLE file_edits ADD COLUMN destination_path TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE file_edits ADD COLUMN destination_original_hash TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE file_edits ADD COLUMN destination_proposed_hash TEXT NOT NULL DEFAULT '';`,

	`DROP TRIGGER trg_file_edit_apply_result_insert;`,
	`DROP TRIGGER trg_file_edit_apply_result_update_immutable;`,
	`DROP TRIGGER trg_file_edit_apply_result_delete_immutable;`,
	`DROP TRIGGER trg_file_edit_apply_operation_insert;`,
	`DROP TRIGGER trg_file_edit_apply_operation_update_immutable;`,
	`DROP TRIGGER trg_file_edit_apply_operation_delete_immutable;`,
	`ALTER TABLE file_edit_apply_results RENAME TO file_edit_apply_results_v114;`,
	`DROP INDEX idx_file_edit_apply_operations_run_created;`,
	`ALTER TABLE file_edit_apply_operations RENAME TO file_edit_apply_operations_v114;`,
	`CREATE TABLE file_edit_apply_operations (
		operation_key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		edit_id TEXT NOT NULL UNIQUE,
		operation_kind TEXT NOT NULL,
		path TEXT NOT NULL,
		destination_path TEXT NOT NULL,
		original_hash TEXT NOT NULL,
		proposed_hash TEXT NOT NULL,
		observed_hash TEXT NOT NULL,
		destination_original_hash TEXT NOT NULL,
		destination_proposed_hash TEXT NOT NULL,
		destination_observed_hash TEXT NOT NULL,
		applied_by TEXT NOT NULL,
		event_sequence INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(edit_id) REFERENCES file_edits(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'file_edit_apply.v1'),
		CHECK(operation_kind IN ('replace', 'create', 'move', 'delete')),
		CHECK(length(operation_key_digest) = 64 AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(original_hash = 'missing' OR (length(original_hash) = 64
			AND original_hash = lower(original_hash) AND original_hash NOT GLOB '*[^0-9a-f]*')),
		CHECK(proposed_hash = 'missing' OR (length(proposed_hash) = 64
			AND proposed_hash = lower(proposed_hash) AND proposed_hash NOT GLOB '*[^0-9a-f]*')),
		CHECK(observed_hash IN (original_hash, proposed_hash)),
		CHECK((operation_kind = 'move' AND length(destination_path) BETWEEN 1 AND 512
			AND destination_original_hash = 'missing'
			AND length(destination_proposed_hash) = 64
			AND destination_proposed_hash = lower(destination_proposed_hash)
			AND destination_proposed_hash NOT GLOB '*[^0-9a-f]*'
			AND destination_observed_hash IN (destination_original_hash, destination_proposed_hash))
			OR (operation_kind != 'move' AND destination_path = ''
				AND destination_original_hash = '' AND destination_proposed_hash = ''
				AND destination_observed_hash = '')),
		CHECK(event_sequence > 0),
		CHECK(path = trim(path) AND length(path) BETWEEN 1 AND 512 AND instr(path, char(0)) = 0),
		CHECK(applied_by = trim(applied_by) AND length(applied_by) BETWEEN 1 AND 256
			AND instr(applied_by, char(0)) = 0)
	) WITHOUT ROWID;`,
	`INSERT INTO file_edit_apply_operations
		(operation_key_digest, request_fingerprint, protocol_version, run_id, session_id,
		 workspace_id, edit_id, operation_kind, path, destination_path, original_hash,
		 proposed_hash, observed_hash, destination_original_hash,
		 destination_proposed_hash, destination_observed_hash, applied_by, event_sequence,
		 created_at)
		SELECT operation_key_digest, request_fingerprint, protocol_version, run_id, session_id,
		 workspace_id, edit_id, 'replace', path, '', original_hash, proposed_hash,
		 observed_hash, '', '', '', applied_by, event_sequence, created_at
		FROM file_edit_apply_operations_v114;`,
	`CREATE INDEX idx_file_edit_apply_operations_run_created
		ON file_edit_apply_operations(run_id, created_at);`,
	`CREATE TABLE file_edit_apply_results (
		operation_key_digest TEXT PRIMARY KEY,
		status TEXT NOT NULL,
		reason_code TEXT NOT NULL,
		event_sequence INTEGER NOT NULL,
		completed_at TEXT NOT NULL,
		FOREIGN KEY(operation_key_digest) REFERENCES file_edit_apply_operations(operation_key_digest)
			ON DELETE RESTRICT,
		CHECK(status IN ('applied', 'failed')),
		CHECK((status = 'applied' AND reason_code = '') OR
			(status = 'failed' AND length(reason_code) BETWEEN 1 AND 64)),
		CHECK(reason_code = trim(reason_code) AND instr(reason_code, char(0)) = 0),
		CHECK(event_sequence > 0)
	) WITHOUT ROWID;`,
	`INSERT INTO file_edit_apply_results
		(operation_key_digest, status, reason_code, event_sequence, completed_at)
		SELECT operation_key_digest, status, reason_code, event_sequence, completed_at
		FROM file_edit_apply_results_v114;`,
	`DROP TABLE file_edit_apply_results_v114;`,
	`DROP TABLE file_edit_apply_operations_v114;`,
	`CREATE TRIGGER trg_file_edit_apply_operation_insert
		BEFORE INSERT ON file_edit_apply_operations
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN sessions session_record ON session_record.id = run.session_id
			JOIN file_edits edit ON edit.id = NEW.edit_id
			JOIN tool_approvals approval ON approval.proposal_id = edit.id
			JOIN run_events event ON event.run_id = run.id AND event.sequence = NEW.event_sequence
			WHERE run.id = NEW.run_id AND run.session_id = NEW.session_id
				AND run.status = 'running' AND session_record.status = 'active'
				AND mission.workspace_id = NEW.workspace_id
				AND edit.session_id = NEW.session_id AND edit.workspace_id = NEW.workspace_id
				AND edit.status = 'approved' AND edit.operation_kind = NEW.operation_kind
				AND edit.path = NEW.path AND edit.destination_path = NEW.destination_path
				AND edit.original_hash = NEW.original_hash
				AND edit.proposed_hash = NEW.proposed_hash
				AND edit.destination_original_hash = NEW.destination_original_hash
				AND edit.destination_proposed_hash = NEW.destination_proposed_hash
				AND NEW.observed_hash IN (edit.original_hash, edit.proposed_hash)
				AND ((edit.operation_kind = 'move' AND NEW.destination_observed_hash IN
					(edit.destination_original_hash, edit.destination_proposed_hash))
					OR (edit.operation_kind != 'move' AND NEW.destination_observed_hash = ''))
				AND approval.run_id = run.id AND approval.session_id = NEW.session_id
				AND approval.workspace_id = NEW.workspace_id
				AND approval.tool_name = CASE edit.operation_kind
					WHEN 'create' THEN 'create_file' WHEN 'move' THEN 'move_file'
					WHEN 'delete' THEN 'delete_file' ELSE 'replace_file' END
				AND approval.action_class = 'workspace_write'
				AND approval.status = 'approved'
				AND event.type = 'file_edit.apply_requested'
				AND event.source = 'file_edit_apply' AND event.subject_id = edit.id
				AND event.created_at = NEW.created_at
				AND json_extract(event.payload_json, '$.operation_key_digest') =
					NEW.operation_key_digest
				AND json_extract(event.payload_json, '$.operation') = NEW.operation_kind
				AND json_extract(event.payload_json, '$.observed_hash') = NEW.observed_hash
				AND json_extract(event.payload_json, '$.proposed_hash') = NEW.proposed_hash
				AND COALESCE(json_extract(event.payload_json, '$.destination_observed_hash'), '') =
					NEW.destination_observed_hash
				AND COALESCE(json_extract(event.payload_json, '$.destination_proposed_hash'), '') =
					NEW.destination_proposed_hash
				AND json_extract(event.payload_json, '$.policy_rechecked') = 1
		)
		BEGIN SELECT RAISE(ABORT, 'FileEdit apply operation binding is invalid'); END;`,
	`CREATE TRIGGER trg_file_edit_apply_operation_update_immutable
		BEFORE UPDATE ON file_edit_apply_operations BEGIN
			SELECT RAISE(ABORT, 'FileEdit apply operation cannot be updated');
		END;`,
	`CREATE TRIGGER trg_file_edit_apply_operation_delete_immutable
		BEFORE DELETE ON file_edit_apply_operations BEGIN
			SELECT RAISE(ABORT, 'FileEdit apply operation cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_file_edit_apply_result_insert
		BEFORE INSERT ON file_edit_apply_results
		WHEN NOT EXISTS (
			SELECT 1 FROM file_edit_apply_operations operation
			JOIN file_edits edit ON edit.id = operation.edit_id
			JOIN run_events event ON event.run_id = operation.run_id
				AND event.sequence = NEW.event_sequence
			WHERE operation.operation_key_digest = NEW.operation_key_digest
				AND ((NEW.status = 'applied' AND edit.status = 'applied'
					AND edit.proposed_hash = operation.proposed_hash AND NEW.reason_code = '')
					OR (NEW.status = 'failed' AND edit.status = 'failed'
						AND length(NEW.reason_code) BETWEEN 1 AND 64))
				AND event.type = 'file_edit.apply_completed'
				AND event.source = 'file_edit_apply' AND event.subject_id = edit.id
				AND event.created_at = NEW.completed_at
				AND json_extract(event.payload_json, '$.operation_key_digest') =
					NEW.operation_key_digest
				AND json_extract(event.payload_json, '$.status') = NEW.status
				AND json_extract(event.payload_json, '$.reason_code') = NEW.reason_code
		)
		BEGIN SELECT RAISE(ABORT, 'FileEdit apply result binding is invalid'); END;`,
	`CREATE TRIGGER trg_file_edit_apply_result_update_immutable
		BEFORE UPDATE ON file_edit_apply_results BEGIN
			SELECT RAISE(ABORT, 'FileEdit apply result cannot be updated');
		END;`,
	`CREATE TRIGGER trg_file_edit_apply_result_delete_immutable
		BEFORE DELETE ON file_edit_apply_results BEGIN
			SELECT RAISE(ABORT, 'FileEdit apply result cannot be deleted');
		END;`,

	`DROP TRIGGER trg_supervisor_tool_call_model_attempt;`,
	`DROP TRIGGER trg_supervisor_tool_round_completion;`,
	`DROP INDEX idx_run_supervisor_tool_calls_pending;`,
	`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v114;`,
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
		authority_json TEXT NOT NULL DEFAULT '',
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
			'specialist_delegation_propose', 'child_task_propose',
			'plan_delivery_propose', 'controlled_command_propose',
			'one_shot_command_propose', 'host_command_propose',
			'sandbox_docker_run_propose', 'skill_candidate_propose', 'debug_terminal',
			'workspace_list', 'workspace_read', 'workspace_glob', 'workspace_grep',
			'workspace_change', 'workspace_apply', 'workspace_delete')),
		CHECK((tool_name IN ('workspace_list', 'workspace_read', 'workspace_glob',
			'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete')
			AND length(authority_json) BETWEEN 2 AND 4096 AND json_valid(authority_json) = 1)
			OR (tool_name NOT IN ('workspace_list', 'workspace_read', 'workspace_glob',
				'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete')
				AND authority_json = '')),
		CHECK(status IN ('pending', 'completed', 'denied', 'failed')),
		CHECK((status = 'pending' AND result_json = '' AND error_code = '' AND completed_at IS NULL)
			OR (status = 'completed' AND length(result_json) > 0 AND error_code = '' AND completed_at IS NOT NULL)
			OR (status IN ('denied', 'failed') AND length(result_json) > 0 AND length(error_code) > 0
				AND completed_at IS NOT NULL))
	);`,
	`INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		payload_json, authority_json, status, result_json, error_code, created_at, completed_at)
		SELECT run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		payload_json, '', status, result_json, error_code, created_at, completed_at
		FROM run_supervisor_tool_calls_v114;`,
	`DROP TABLE run_supervisor_tool_calls_v114;`,
	`CREATE INDEX idx_run_supervisor_tool_calls_pending
		ON run_supervisor_tool_calls(run_id, turn, attempt_id, status, round, position);`,
	`CREATE TRIGGER trg_supervisor_tool_call_model_attempt
		BEFORE INSERT ON run_supervisor_tool_calls
		WHEN NOT EXISTS (
			SELECT 1 FROM run_supervisor_tool_rounds
			WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
				AND round = NEW.round AND model_attempt = NEW.model_attempt
		)
		BEGIN SELECT RAISE(ABORT, 'supervisor tool call model attempt mismatch'); END;`,
	`CREATE TRIGGER trg_supervisor_tool_round_completion
		BEFORE UPDATE OF completed_at ON run_supervisor_tool_rounds
		WHEN NEW.completed_at IS NOT NULL AND EXISTS (
			SELECT 1 FROM run_supervisor_tool_calls
			WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
				AND round = NEW.round AND status = 'pending'
		)
		BEGIN SELECT RAISE(ABORT, 'supervisor tool round still has pending calls'); END;`,
}
