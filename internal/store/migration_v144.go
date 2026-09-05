package store

var controlledRunExactNetworkAllowlistStatements = []string{
	`DROP TRIGGER trg_run_creation_operation_insert;`,
	`CREATE TRIGGER trg_run_creation_operation_insert
		BEFORE INSERT ON run_creation_operations
		WHEN NOT EXISTS (
			SELECT 1
			FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN sessions session_record ON session_record.id = run.session_id
			JOIN workspaces workspace ON workspace.id = mission.workspace_id
			JOIN run_mode_snapshots mode ON mode.run_id = run.id AND mode.revision = 1
			JOIN run_execution_profile_snapshots execution_profile
				ON execution_profile.run_id = run.id AND execution_profile.revision = 1
			JOIN agent_nodes root ON root.run_id = run.id AND root.role = 'root'
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND run.session_id = NEW.session_id
				AND mission.workspace_id = NEW.workspace_id
				AND session_record.workspace_id = NEW.workspace_id
				AND run.status = 'created' AND session_record.status = 'active'
				AND json_extract(run.config_json, '$.interactive') = 1
				AND json_extract(run.config_json, '$.model_route') = mission.profile
				AND json_extract(run.budget_json, '$.max_turns') = 100
				AND COALESCE(json_extract(run.budget_json, '$.max_tokens'), 0) = 0
				AND json_extract(run.budget_json, '$.max_tool_calls') = 100
				AND COALESCE(json_extract(run.budget_json, '$.max_cost_usd'), 0) = 0
				AND COALESCE(json_extract(run.budget_json, '$.timeout_seconds'), 0) = 0
				AND json_extract(mission.scope_json, '$.workspace_id') = NEW.workspace_id
				AND (
					(json_extract(mission.scope_json, '$.network_mode') = 'disabled'
						AND COALESCE(json_array_length(mission.scope_json, '$.allowed_targets'), 0) = 0)
					OR (json_extract(mission.scope_json, '$.network_mode') = 'allowlist'
						AND json_array_length(mission.scope_json, '$.allowed_targets') BETWEEN 1 AND 256)
				)
				AND length(CAST(mission.goal AS BLOB)) BETWEEN 1 AND 4096
				AND session_record.route = mission.profile
				AND session_record.title = mission.goal
				AND mission.created_at = NEW.created_at
				AND mission.updated_at = NEW.created_at
				AND session_record.created_at = NEW.created_at
				AND session_record.updated_at = NEW.created_at
				AND mode.mission_id = mission.id AND mode.profile = mission.profile
				AND mode.scope_json = mission.scope_json AND mode.requested_by = NEW.requested_by
				AND mode.created_at = NEW.created_at
				AND execution_profile.mission_id = mission.id
				AND execution_profile.profile = 'preview'
				AND execution_profile.backend = 'noop'
				AND execution_profile.approval_policy = 'none'
				AND execution_profile.filesystem_scope = 'none'
				AND execution_profile.network_scope = 'disabled'
				AND execution_profile.requested_by = NEW.requested_by
				AND execution_profile.process_enabled = 0
				AND execution_profile.execution_authorized = 0
				AND execution_profile.capability_grant = 0
				AND execution_profile.created_at = NEW.created_at
				AND root.parent_id IS NULL AND root.session_id = session_record.id
				AND root.profile = mission.profile AND root.status = 'ready' AND root.depth = 0
				AND root.child_limit = 0 AND root.turn_limit = 100 AND root.token_limit = 0
				AND root.turns_used = 0 AND root.tokens_used = 0
				AND root.active_attempt_id = '' AND root.version = 1
				AND root.created_at = NEW.created_at AND root.updated_at = NEW.created_at
				AND run.created_at = NEW.created_at
				AND run.updated_at = NEW.created_at
				AND (SELECT COUNT(*) FROM run_events event
					WHERE event.run_id = run.id AND event.mission_id = mission.id
						AND event.type = 'run.created') = 1
				AND (SELECT COUNT(*) FROM run_events event
					WHERE event.run_id = run.id AND event.mission_id = mission.id
						AND event.type = 'session.attached') = 1
				AND (SELECT COUNT(*) FROM run_events event
					WHERE event.run_id = run.id AND event.mission_id = mission.id
						AND event.type = 'run.mode_selected') = 1
				AND (SELECT COUNT(*) FROM run_events event
					WHERE event.run_id = run.id AND event.mission_id = mission.id
						AND event.type = 'agent.registered') = 1
				AND (SELECT COUNT(*) FROM run_events event
					WHERE event.run_id = run.id AND event.mission_id = mission.id) = 4
		)
		BEGIN
			SELECT RAISE(ABORT, 'Run creation operation binding is invalid');
		END;`,
}
