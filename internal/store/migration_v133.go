package store

import "strings"

// standardCodePresetStatements widens only the controlled interaction's
// backend binding (Local LPAC or the fixed Docker network-none adapter) and
// adds the durable, non-authorizing Standard Code write-ahead receipt.
var standardCodePresetStatements = func() []string {
	createSnapshots := requireMigrationStatement(
		"CREATE TABLE run_execution_interaction_snapshots (",
		runExecutionInteractionStatements)
	oldControlled := `(mode = 'controlled' AND surface = 'code' AND execution_profile = 'local'
				AND workspace_trust = 'trusted' AND command_form = 'structured_argv'
				AND persistent_terminal = 0 AND user_input_available = 0
				AND required_gate = 'local_os_sandbox_gate' AND operator_confirmed = 1)`
	newControlled := `(mode = 'controlled' AND surface = 'code'
				AND ((execution_profile = 'local' AND required_gate = 'local_os_sandbox_gate')
					OR (execution_profile = 'docker' AND required_gate = 'docker_sandbox_gate'))
				AND workspace_trust = 'trusted' AND command_form = 'structured_argv'
				AND persistent_terminal = 0 AND user_input_available = 0
				AND operator_confirmed = 1)`
	if !strings.Contains(createSnapshots, oldControlled) {
		panic("Standard Code migration source controlled interaction constraint is missing")
	}
	createSnapshots = strings.Replace(createSnapshots, oldControlled, newControlled, 1)
	createSnapshots = strings.Replace(createSnapshots,
		"CREATE TABLE run_execution_interaction_snapshots (",
		"CREATE TABLE run_execution_interaction_snapshots_v133 (", 1)

	createOperations := requireMigrationStatement(
		"CREATE TABLE run_execution_interaction_operations (",
		runExecutionInteractionStatements)
	createOperations = strings.Replace(createOperations,
		"CREATE TABLE run_execution_interaction_operations (",
		"CREATE TABLE run_execution_interaction_operations_v133 (", 1)
	index := requireMigrationStatement(
		"CREATE INDEX idx_run_execution_interaction_snapshots_run_revision",
		runExecutionInteractionStatements)
	interactionTriggers := make([]string, 0, 6)
	for _, name := range []string{
		"trg_run_execution_interaction_snapshot_insert",
		"trg_run_execution_interaction_operation_insert",
		"trg_run_execution_interaction_snapshot_update_immutable",
		"trg_run_execution_interaction_snapshot_delete_immutable",
		"trg_run_execution_interaction_operation_update_immutable",
		"trg_run_execution_interaction_operation_delete_immutable",
	} {
		interactionTriggers = append(interactionTriggers,
			requireMigrationTrigger(name, runExecutionInteractionStatements))
	}

	statements := []string{
		`PRAGMA legacy_alter_table = ON;`,
		`DROP TRIGGER trg_run_execution_interaction_snapshot_insert;`,
		`DROP TRIGGER trg_run_execution_interaction_operation_insert;`,
		`DROP TRIGGER trg_run_execution_interaction_snapshot_update_immutable;`,
		`DROP TRIGGER trg_run_execution_interaction_snapshot_delete_immutable;`,
		`DROP TRIGGER trg_run_execution_interaction_operation_update_immutable;`,
		`DROP TRIGGER trg_run_execution_interaction_operation_delete_immutable;`,
		`DROP INDEX idx_run_execution_interaction_snapshots_run_revision;`,
		createSnapshots,
		`INSERT INTO run_execution_interaction_snapshots_v133
			SELECT * FROM run_execution_interaction_snapshots;`,
		createOperations,
		`INSERT INTO run_execution_interaction_operations_v133
			SELECT * FROM run_execution_interaction_operations;`,
		`DROP TABLE run_execution_interaction_operations;`,
		`DROP TABLE run_execution_interaction_snapshots;`,
		`ALTER TABLE run_execution_interaction_snapshots_v133
			RENAME TO run_execution_interaction_snapshots;`,
		`ALTER TABLE run_execution_interaction_operations_v133
			RENAME TO run_execution_interaction_operations;`,
		index,
	}
	statements = append(statements, interactionTriggers...)
	statements = append(statements, []string{
		`PRAGMA legacy_alter_table = OFF;`,
		`CREATE TABLE standard_code_preset_operations (
			operation_key_digest TEXT PRIMARY KEY,
			request_fingerprint TEXT NOT NULL,
			protocol_version TEXT NOT NULL,
			requested_run_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			mission_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			action TEXT NOT NULL,
			backend_intent TEXT NOT NULL,
			selected_backend TEXT NOT NULL,
			selection_reason TEXT NOT NULL,
			status TEXT NOT NULL,
			drydock_id TEXT,
			drydock_generation INTEGER,
			drydock_checkpoint_id TEXT,
			mode_snapshot_id TEXT,
			profile_snapshot_id TEXT,
			interaction_snapshot_id TEXT,
			permission_snapshot_id TEXT,
			browser_cdp_snapshot_id TEXT,
			event_sequence_start INTEGER NOT NULL,
			event_sequence_end INTEGER NOT NULL,
			requested_by TEXT NOT NULL,
			capability_grant INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
			FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
			FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
			FOREIGN KEY(drydock_id) REFERENCES drydock_workspaces(id) ON DELETE RESTRICT,
			FOREIGN KEY(mode_snapshot_id) REFERENCES run_mode_snapshots(id) ON DELETE RESTRICT,
			FOREIGN KEY(profile_snapshot_id) REFERENCES run_execution_profile_snapshots(id) ON DELETE RESTRICT,
			FOREIGN KEY(interaction_snapshot_id) REFERENCES run_execution_interaction_snapshots(id) ON DELETE RESTRICT,
			FOREIGN KEY(permission_snapshot_id) REFERENCES run_execution_permission_snapshots(id) ON DELETE RESTRICT,
			FOREIGN KEY(browser_cdp_snapshot_id) REFERENCES run_browser_cdp_permission_snapshots(id) ON DELETE RESTRICT,
			CHECK(protocol_version = 'standard_code_preset.v1'),
			CHECK(length(operation_key_digest) = 64 AND operation_key_digest = lower(operation_key_digest)
				AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
			CHECK(length(request_fingerprint) = 64 AND request_fingerprint = lower(request_fingerprint)
				AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
			CHECK(requested_run_id = trim(requested_run_id) AND length(requested_run_id) <= 256
				AND instr(requested_run_id, char(0)) = 0),
			CHECK(action IN ('configure', 'pause_and_configure')),
			CHECK(backend_intent IN ('auto', 'local', 'docker')),
			CHECK(selected_backend IN ('local', 'docker')),
			CHECK(selection_reason IN ('auto_local_ready', 'explicit_local', 'explicit_docker')),
			CHECK((backend_intent = 'auto' AND selected_backend = 'local' AND selection_reason = 'auto_local_ready')
				OR (backend_intent = 'local' AND selected_backend = 'local' AND selection_reason = 'explicit_local')
				OR (backend_intent = 'docker' AND selected_backend = 'docker' AND selection_reason = 'explicit_docker')),
			CHECK(status IN ('preparing', 'waiting_for_pause', 'configured')),
			CHECK(event_sequence_start > 0 AND event_sequence_end >= event_sequence_start),
			CHECK(status = 'configured' OR event_sequence_end = event_sequence_start),
			CHECK(capability_grant = 0),
			CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256 AND instr(run_id, char(0)) = 0),
			CHECK(mission_id = trim(mission_id) AND length(mission_id) BETWEEN 1 AND 256 AND instr(mission_id, char(0)) = 0),
			CHECK(workspace_id = trim(workspace_id) AND length(workspace_id) BETWEEN 1 AND 256 AND instr(workspace_id, char(0)) = 0),
			CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256 AND instr(requested_by, char(0)) = 0),
			CHECK((status IN ('preparing', 'waiting_for_pause')
				AND drydock_id IS NULL AND drydock_generation IS NULL AND drydock_checkpoint_id IS NULL
				AND mode_snapshot_id IS NULL AND profile_snapshot_id IS NULL
				AND interaction_snapshot_id IS NULL AND permission_snapshot_id IS NULL
				AND browser_cdp_snapshot_id IS NULL)
				OR (status = 'configured' AND drydock_id IS NOT NULL AND drydock_generation > 0
					AND drydock_checkpoint_id IS NOT NULL AND mode_snapshot_id IS NOT NULL
					AND profile_snapshot_id IS NOT NULL AND interaction_snapshot_id IS NOT NULL
					AND permission_snapshot_id IS NOT NULL AND browser_cdp_snapshot_id IS NOT NULL))
		) WITHOUT ROWID;`,
		`CREATE INDEX idx_standard_code_preset_operations_run_created
			ON standard_code_preset_operations(run_id, created_at);`,
		`CREATE TRIGGER trg_standard_code_preset_operation_insert
			BEFORE INSERT ON standard_code_preset_operations
			WHEN NOT EXISTS (
				SELECT 1 FROM runs run JOIN missions mission ON mission.id = run.mission_id
				WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
					AND mission.workspace_id = NEW.workspace_id
					AND ((NEW.status = 'preparing' AND run.status IN ('created', 'paused'))
						OR (NEW.status = 'waiting_for_pause' AND NEW.action = 'pause_and_configure'
							AND run.status = 'running'))
					AND EXISTS (SELECT 1 FROM run_events event
						WHERE event.run_id = NEW.run_id AND event.mission_id = NEW.mission_id
							AND event.sequence = NEW.event_sequence_start
							AND event.type = 'standard_code.preset_intent_recorded'
							AND event.source = 'standard_code_preset'
							AND event.subject_id = NEW.run_id)
			)
			BEGIN
				SELECT RAISE(ABORT, 'Standard Code preset intent binding is invalid');
			END;`,
		`CREATE TRIGGER trg_standard_code_preset_operation_update
			BEFORE UPDATE ON standard_code_preset_operations
			WHEN NOT (
				OLD.status IN ('preparing', 'waiting_for_pause') AND NEW.status = 'configured'
				AND OLD.operation_key_digest = NEW.operation_key_digest
				AND OLD.request_fingerprint = NEW.request_fingerprint
				AND OLD.protocol_version = NEW.protocol_version
				AND OLD.requested_run_id = NEW.requested_run_id
				AND OLD.run_id = NEW.run_id AND OLD.mission_id = NEW.mission_id
				AND OLD.workspace_id = NEW.workspace_id AND OLD.action = NEW.action
				AND OLD.backend_intent = NEW.backend_intent
				AND OLD.selected_backend = NEW.selected_backend
				AND OLD.selection_reason = NEW.selection_reason
				AND OLD.event_sequence_start = NEW.event_sequence_start
				AND OLD.requested_by = NEW.requested_by
				AND OLD.capability_grant = NEW.capability_grant
				AND OLD.created_at = NEW.created_at
				AND julianday(NEW.updated_at) >= julianday(OLD.updated_at)
				AND EXISTS (SELECT 1 FROM run_events event
					WHERE event.run_id = NEW.run_id AND event.mission_id = NEW.mission_id
						AND event.sequence = NEW.event_sequence_end
						AND event.type = 'standard_code.preset_configured'
						AND event.source = 'standard_code_preset'
						AND event.subject_id = NEW.run_id)
				AND EXISTS (SELECT 1 FROM drydock_workspaces drydock
					JOIN drydock_workspace_trust trust_record ON trust_record.id = drydock.trust_id
					WHERE drydock.id = NEW.drydock_id AND drydock.run_id = NEW.run_id
						AND drydock.mission_id = NEW.mission_id
						AND drydock.source_workspace_id = NEW.workspace_id
						AND drydock.generation = NEW.drydock_generation
						AND drydock.last_checkpoint_id = NEW.drydock_checkpoint_id
						AND drydock.state IN ('ready', 'delivered')
						AND trust_record.run_id = NEW.run_id
						AND trust_record.workspace_id = NEW.workspace_id
						AND trust_record.grants_process_authority = 0)
				AND EXISTS (SELECT 1 FROM run_mode_snapshots mode
					WHERE mode.id = NEW.mode_snapshot_id AND mode.run_id = NEW.run_id
						AND mode.surface = 'code' AND mode.phase = 'plan'
						AND NOT EXISTS (SELECT 1 FROM run_mode_snapshots newer
							WHERE newer.run_id = mode.run_id AND newer.revision > mode.revision))
				AND EXISTS (SELECT 1 FROM run_execution_profile_snapshots profile
					WHERE profile.id = NEW.profile_snapshot_id AND profile.run_id = NEW.run_id
						AND profile.profile = NEW.selected_backend
						AND NOT EXISTS (SELECT 1 FROM run_execution_profile_snapshots newer
							WHERE newer.run_id = profile.run_id AND newer.revision > profile.revision))
				AND EXISTS (SELECT 1 FROM run_execution_interaction_snapshots interaction
					JOIN run_execution_profile_snapshots profile ON profile.id = NEW.profile_snapshot_id
					WHERE interaction.id = NEW.interaction_snapshot_id AND interaction.run_id = NEW.run_id
						AND interaction.mode = 'controlled' AND interaction.surface = 'code'
						AND interaction.workspace_trust = 'trusted'
						AND interaction.execution_profile = profile.profile
						AND interaction.execution_profile_revision = profile.revision
						AND interaction.network_scope = 'disabled'
						AND interaction.process_enabled = 0 AND interaction.execution_authorized = 0
						AND interaction.capability_grant = 0
						AND NOT EXISTS (SELECT 1 FROM run_execution_interaction_snapshots newer
							WHERE newer.run_id = interaction.run_id AND newer.revision > interaction.revision))
				AND EXISTS (SELECT 1 FROM run_execution_permission_snapshots permission
					WHERE permission.id = NEW.permission_snapshot_id AND permission.run_id = NEW.run_id
						AND permission.mode = 'workspace_access' AND permission.network_scope = 'disabled'
						AND permission.capability_grant = 0 AND permission.process_enabled = 0
						AND permission.execution_authorized = 0
						AND NOT EXISTS (SELECT 1 FROM run_execution_permission_snapshots newer
							WHERE newer.run_id = permission.run_id AND newer.revision > permission.revision))
				AND EXISTS (SELECT 1 FROM run_browser_cdp_permission_snapshots cdp
					WHERE cdp.id = NEW.browser_cdp_snapshot_id AND cdp.run_id = NEW.run_id
						AND cdp.mode = 'restricted' AND cdp.capability_grant = 0
						AND cdp.transport_enabled = 0 AND cdp.browser_start_authorized = 0
						AND cdp.runtime_authorized = 0
						AND NOT EXISTS (SELECT 1 FROM run_browser_cdp_permission_snapshots newer
							WHERE newer.run_id = cdp.run_id AND newer.revision > cdp.revision))
			)
			BEGIN
				SELECT RAISE(ABORT, 'Standard Code preset completion binding is invalid');
			END;`,
		`CREATE TRIGGER trg_standard_code_preset_operation_delete_immutable
			BEFORE DELETE ON standard_code_preset_operations BEGIN
				SELECT RAISE(ABORT, 'Standard Code preset operation cannot be deleted');
			END;`,
	}...)
	return statements
}()
