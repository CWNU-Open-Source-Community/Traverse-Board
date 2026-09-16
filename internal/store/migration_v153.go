package store

// Preserve the historical Apply journal and approval identities. Only the
// insertion guard changes: an owned Drydock is a distinct, exact file target.
var drydockFileEditScopeStatements = []string{
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
					AND NOT EXISTS (SELECT 1 FROM drydock_workspaces d WHERE d.run_id = run.id)
					AND NOT EXISTS (SELECT 1 FROM standard_code_preset_operations p
						WHERE p.run_id = run.id AND p.status = 'configured'))
					OR EXISTS (SELECT 1 FROM drydock_workspaces d
						WHERE d.run_id = run.id AND d.mission_id = mission.id
						AND d.session_id = run.session_id AND d.source_workspace_id = mission.workspace_id
						AND d.workspace_id = NEW.workspace_id AND d.state IN ('ready','delivered')))
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
