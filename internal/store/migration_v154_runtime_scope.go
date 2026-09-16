package store

// Logical Run identities may share an explicitly bound physical directory.
// These insertion guards preserve historical rows and require the current holder.
var threadDrydockRuntimeScopeStatements = []string{
	`DROP TRIGGER trg_drydock_delivery_insert_scope;`,
	`CREATE TRIGGER trg_drydock_delivery_insert_scope
		BEFORE INSERT ON drydock_delivery_proposals
		WHEN NOT EXISTS (
			SELECT 1 FROM drydock_workspaces drydock
			JOIN workspace_checkpoints checkpoint ON checkpoint.id = NEW.checkpoint_id
			WHERE drydock.id = NEW.drydock_id AND (EXISTS (SELECT 1 FROM run_file_drydock_bindings owner
 JOIN threads thread ON thread.id=owner.thread_id
 WHERE owner.run_id=NEW.run_id AND owner.drydock_id=drydock.id AND thread.last_run_id=owner.run_id)
 OR EXISTS (SELECT 1 FROM run_file_drydock_bindings owner
 WHERE owner.run_id=NEW.run_id AND owner.drydock_id=drydock.id AND owner.thread_id=''))
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
	`DROP TRIGGER trg_drydock_receipt_insert_scope;`,
	`CREATE TRIGGER trg_drydock_receipt_insert_scope
		BEFORE INSERT ON drydock_lifecycle_receipts
		WHEN NOT EXISTS (
			SELECT 1 FROM drydock_workspaces drydock
			WHERE drydock.id = NEW.drydock_id AND (EXISTS (SELECT 1 FROM run_file_drydock_bindings owner
 JOIN threads thread ON thread.id=owner.thread_id
 WHERE owner.run_id=NEW.run_id AND owner.drydock_id=drydock.id AND thread.last_run_id=owner.run_id)
 OR EXISTS (SELECT 1 FROM run_file_drydock_bindings owner
 WHERE owner.run_id=NEW.run_id AND owner.drydock_id=drydock.id AND owner.thread_id=''))
				AND drydock.generation = NEW.generation_after
				AND drydock.source_identity_sha256 = NEW.source_identity_sha256
				AND drydock.root_fingerprint = NEW.root_fingerprint
		) OR (NEW.checkpoint_id <> '' AND NOT EXISTS (
			SELECT 1 FROM workspace_checkpoints checkpoint
			JOIN drydock_workspaces drydock ON drydock.id = NEW.drydock_id
			WHERE checkpoint.id = NEW.checkpoint_id
				AND (checkpoint.run_id = NEW.run_id OR ((NEW.operation='fork' OR
 (NEW.operation IN ('use','recover') AND drydock.last_checkpoint_id=checkpoint.id)) AND EXISTS (
 SELECT 1 FROM run_file_drydock_bindings historical
 WHERE historical.run_id=checkpoint.run_id AND historical.drydock_id=drydock.id
 AND historical.workspace_id=checkpoint.workspace_id)))
				AND checkpoint.workspace_id = drydock.workspace_id
		)) OR (NEW.delivery_id <> '' AND NOT EXISTS (
			SELECT 1 FROM drydock_delivery_proposals proposal
			WHERE proposal.id = NEW.delivery_id
				AND proposal.drydock_id = NEW.drydock_id
				AND proposal.run_id = NEW.run_id
		))
		BEGIN SELECT RAISE(ABORT, 'Drydock lifecycle receipt scope is invalid'); END;`,
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
			SELECT 1 FROM run_file_drydock_bindings owner
 JOIN drydock_workspaces drydock ON drydock.id=owner.drydock_id
 LEFT JOIN threads thread ON thread.id=owner.thread_id
 WHERE owner.run_id=NEW.run_id AND owner.mission_id=NEW.mission_id
 AND owner.session_id=NEW.session_id AND owner.workspace_id=NEW.workspace_id
 AND drydock.state<>'cleaned' AND (owner.thread_id='' OR thread.last_run_id=owner.run_id)
		)
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint Run binding is invalid'); END;`,
	`DROP TRIGGER trg_workspace_checkpoint_parent_binding;`,
	`CREATE TRIGGER trg_workspace_checkpoint_parent_binding
		BEFORE INSERT ON workspace_checkpoints WHEN NEW.parent_checkpoint_id != ''
			AND NOT EXISTS (SELECT 1 FROM workspace_checkpoints parent
				WHERE parent.id = NEW.parent_checkpoint_id AND parent.sealed = 1
					AND (parent.run_id = NEW.run_id OR EXISTS (
 SELECT 1 FROM run_file_drydock_bindings previous JOIN run_file_drydock_bindings current
 ON current.drydock_id=previous.drydock_id
 JOIN drydock_workspaces d ON d.id=current.drydock_id
 WHERE previous.run_id=parent.run_id AND current.run_id=NEW.run_id
 AND previous.workspace_id=parent.workspace_id AND current.workspace_id=NEW.workspace_id
 AND d.last_checkpoint_id=parent.id)) AND parent.workspace_id = NEW.workspace_id)
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint parent binding is invalid'); END;`,
	`DROP TRIGGER trg_workspace_checkpoint_transaction_insert_binding;`,
	`CREATE TRIGGER trg_workspace_checkpoint_transaction_insert_binding
		BEFORE INSERT ON workspace_checkpoint_transactions
		WHEN NOT EXISTS (SELECT 1 FROM workspace_checkpoints checkpoint
				WHERE checkpoint.id = NEW.before_checkpoint_id AND checkpoint.sealed = 1
					AND (checkpoint.run_id = NEW.run_id OR (NEW.kind='fork' AND EXISTS (
 SELECT 1 FROM run_file_drydock_bindings previous JOIN run_file_drydock_bindings current
 ON current.drydock_id=previous.drydock_id JOIN drydock_workspaces d ON d.id=current.drydock_id
 LEFT JOIN threads thread ON thread.id=current.thread_id
 WHERE previous.run_id=checkpoint.run_id AND previous.workspace_id=checkpoint.workspace_id
 AND current.run_id=NEW.run_id AND current.workspace_id=NEW.workspace_id
 AND NEW.expected_current_checkpoint_id=checkpoint.id AND d.last_checkpoint_id=checkpoint.id
 AND (current.thread_id='' OR thread.last_run_id=current.run_id))))
					AND checkpoint.workspace_id = NEW.workspace_id)
			OR (NEW.expected_current_checkpoint_id != '' AND NOT EXISTS
				(SELECT 1 FROM workspace_checkpoints checkpoint
				 WHERE checkpoint.id = NEW.expected_current_checkpoint_id
					AND checkpoint.sealed = 1 AND (checkpoint.run_id = NEW.run_id OR (NEW.kind IN ('file_tool','rewind','undo','redo') AND EXISTS (
 SELECT 1 FROM workspace_checkpoints before_snapshot
 JOIN run_file_drydock_bindings previous ON previous.run_id=checkpoint.run_id
 JOIN run_file_drydock_bindings current ON current.drydock_id=previous.drydock_id
 JOIN drydock_workspaces d ON d.id=current.drydock_id JOIN threads thread ON thread.id=current.thread_id
 WHERE before_snapshot.id=NEW.before_checkpoint_id AND before_snapshot.run_id=NEW.run_id
 AND before_snapshot.workspace_id=NEW.workspace_id AND before_snapshot.parent_checkpoint_id=NEW.expected_current_checkpoint_id
 AND current.run_id=NEW.run_id AND current.workspace_id=NEW.workspace_id
 AND d.last_checkpoint_id=checkpoint.id AND thread.last_run_id=current.run_id)) OR (NEW.kind='fork' AND EXISTS (
 SELECT 1 FROM run_file_drydock_bindings previous JOIN run_file_drydock_bindings current
 ON current.drydock_id=previous.drydock_id JOIN drydock_workspaces d ON d.id=current.drydock_id
 LEFT JOIN threads thread ON thread.id=current.thread_id
 WHERE previous.run_id=checkpoint.run_id AND previous.workspace_id=checkpoint.workspace_id
 AND current.run_id=NEW.run_id AND current.workspace_id=NEW.workspace_id
 AND NEW.before_checkpoint_id=checkpoint.id AND d.last_checkpoint_id=checkpoint.id
 AND (current.thread_id='' OR thread.last_run_id=current.run_id))))
					AND checkpoint.workspace_id = NEW.workspace_id))
			OR (NEW.target_checkpoint_id != '' AND NOT EXISTS
				(SELECT 1 FROM workspace_checkpoints checkpoint
				 WHERE checkpoint.id = NEW.target_checkpoint_id
					AND checkpoint.sealed = 1 AND (checkpoint.run_id = NEW.run_id OR (NEW.kind IN ('rewind','undo','redo','fork') AND EXISTS (
 SELECT 1 FROM run_file_drydock_bindings historical JOIN run_file_drydock_bindings current
 ON current.drydock_id=historical.drydock_id
 LEFT JOIN threads thread ON thread.id=current.thread_id
 WHERE historical.run_id=checkpoint.run_id AND historical.workspace_id=checkpoint.workspace_id
 AND current.run_id=NEW.run_id AND current.workspace_id=NEW.workspace_id
 AND (current.thread_id='' OR thread.last_run_id=current.run_id))))
					AND checkpoint.workspace_id = NEW.workspace_id))
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint transaction binding is invalid'); END;`,
	`DROP TRIGGER trg_workspace_checkpoint_transaction_update;`,
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
					AND (checkpoint.run_id = NEW.run_id OR (NEW.kind='fork'
 AND ((NEW.status='completed' AND checkpoint.id=NEW.target_checkpoint_id)
 OR (NEW.status IN ('failed','interrupted') AND checkpoint.id=NEW.before_checkpoint_id))
 AND EXISTS (SELECT 1 FROM run_file_drydock_bindings historical JOIN run_file_drydock_bindings current
 ON current.drydock_id=historical.drydock_id LEFT JOIN threads thread ON thread.id=current.thread_id
 WHERE historical.run_id=checkpoint.run_id AND historical.workspace_id=checkpoint.workspace_id
 AND current.run_id=NEW.run_id AND current.workspace_id=NEW.workspace_id
 AND (current.thread_id='' OR thread.last_run_id=current.run_id))))
					AND checkpoint.workspace_id = NEW.workspace_id))
			OR OLD.status IN ('completed', 'failed', 'interrupted')
			OR (OLD.status = 'prepared' AND NEW.status NOT IN
				('prepared', 'applying', 'completed', 'failed', 'interrupted'))
			OR (OLD.status = 'applying' AND NEW.status NOT IN
				('applying', 'completed', 'failed', 'interrupted'))
		BEGIN SELECT RAISE(ABORT, 'workspace checkpoint transaction transition is invalid'); END;`,
	`DROP TRIGGER trg_file_edit_apply_operation_insert;`,
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
				AND session_record.workspace_id = mission.workspace_id
				AND ((mission.workspace_id = NEW.workspace_id
					AND NOT EXISTS (SELECT 1 FROM run_file_drydock_bindings d WHERE d.run_id = run.id)
					AND NOT EXISTS (SELECT 1 FROM standard_code_preset_operations p
						WHERE p.run_id = run.id AND p.status = 'configured'))
					OR EXISTS (SELECT 1 FROM run_file_drydock_bindings owner JOIN drydock_workspaces d ON d.id=owner.drydock_id
 LEFT JOIN threads thread ON thread.id=owner.thread_id
 WHERE owner.run_id=run.id AND owner.mission_id=mission.id AND owner.session_id=run.session_id
 AND owner.source_workspace_id=mission.workspace_id AND owner.workspace_id=NEW.workspace_id
 AND d.state IN ('ready','delivered') AND (owner.thread_id='' OR thread.last_run_id=owner.run_id)))
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
				AND json_extract(event.payload_json, '$.operation_key_digest') = NEW.operation_key_digest
				AND json_extract(event.payload_json, '$.operation') = NEW.operation_kind
				AND json_extract(event.payload_json, '$.observed_hash') = NEW.observed_hash
				AND json_extract(event.payload_json, '$.proposed_hash') = NEW.proposed_hash
				AND COALESCE(json_extract(event.payload_json, '$.destination_observed_hash'), '') = NEW.destination_observed_hash
				AND COALESCE(json_extract(event.payload_json, '$.destination_proposed_hash'), '') = NEW.destination_proposed_hash
				AND json_extract(event.payload_json, '$.policy_rechecked') = 1
		)
		BEGIN SELECT RAISE(ABORT, 'FileEdit apply operation binding is invalid'); END;`,
}
