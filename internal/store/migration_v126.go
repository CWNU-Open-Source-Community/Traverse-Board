package store

import "strings"

// workspaceAccessExecutionPermissionStatements rebuilds only the permission
// snapshot table so SQLite can enforce the additive workspace_access enum.
// Migration execution disables foreign-key enforcement around this standard
// create-copy-drop-rename sequence, then runs foreign_key_check before commit.
var workspaceAccessExecutionPermissionStatements = func() []string {
	statements := []string{
		`DROP TRIGGER trg_run_execution_permission_operation_insert;`,
		`DROP TRIGGER trg_controlled_command_proposal_insert_binding;`,
		`DROP TRIGGER trg_host_command_execution_intent_insert_binding;`,
		`DROP TRIGGER trg_host_command_proposal_insert_binding;`,
		`DROP TRIGGER trg_sandbox_docker_product_admission_insert;`,
		`DROP TRIGGER trg_command_runtime_job_insert_scope;`,
		`CREATE TABLE run_execution_permission_snapshots_v126 (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		revision INTEGER NOT NULL,
		protocol_version TEXT NOT NULL,
		mode TEXT NOT NULL,
		approval_policy TEXT NOT NULL,
		command_scope TEXT NOT NULL,
		filesystem_scope TEXT NOT NULL,
		network_scope TEXT NOT NULL,
		persistent_terminal INTEGER NOT NULL,
		background_process INTEGER NOT NULL,
		agent_terminal_input INTEGER NOT NULL,
		risk_tier TEXT NOT NULL,
		required_gate TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		operator_confirmed INTEGER NOT NULL,
		process_enabled INTEGER NOT NULL,
		execution_authorized INTEGER NOT NULL,
		capability_grant INTEGER NOT NULL,
		requested_by TEXT NOT NULL,
		reason TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		UNIQUE(run_id, revision),
		CHECK(revision > 0),
		CHECK(protocol_version = 'run_execution_permission.v1'),
		CHECK(policy_version = 'execution_permission_policy.v1'),
		CHECK(process_enabled = 0 AND execution_authorized = 0 AND capability_grant = 0),
		CHECK(
			(mode = 'conservative' AND approval_policy = 'fixed_templates'
				AND command_scope = 'fixed_templates'
				AND filesystem_scope = 'workspace_guarded' AND network_scope = 'disabled'
				AND persistent_terminal = 0 AND background_process = 0
				AND agent_terminal_input = 0 AND risk_tier = 'minimal'
				AND required_gate = 'conservative_control' AND operator_confirmed = 0)
			OR (mode = 'workspace_access' AND approval_policy = 'out_of_scope_exact_once'
				AND command_scope = 'sandboxed_workspace'
				AND filesystem_scope = 'workspace_guarded' AND network_scope = 'disabled'
				AND persistent_terminal = 0 AND background_process = 0
				AND agent_terminal_input = 0 AND risk_tier = 'elevated'
				AND required_gate = 'workspace_sandbox_adapter' AND operator_confirmed = 1)
			OR (mode = 'approval' AND approval_policy = 'per_command'
				AND command_scope = 'arbitrary_stateless'
				AND filesystem_scope = 'host_full' AND network_scope = 'host'
				AND persistent_terminal = 0 AND background_process = 0
				AND agent_terminal_input = 0 AND risk_tier = 'elevated'
				AND required_gate = 'operator_approval' AND operator_confirmed = 1)
			OR (mode = 'full_access' AND approval_policy = 'none'
				AND command_scope = 'arbitrary_stateless'
				AND filesystem_scope = 'host_full' AND network_scope = 'host'
				AND persistent_terminal = 0 AND background_process = 0
				AND agent_terminal_input = 0 AND risk_tier = 'high'
				AND required_gate = 'danger_full_access' AND operator_confirmed = 1)
			OR (mode = 'debug' AND approval_policy = 'none'
				AND command_scope = 'arbitrary_persistent'
				AND filesystem_scope = 'host_full' AND network_scope = 'host'
				AND persistent_terminal = 1 AND background_process = 1
				AND agent_terminal_input = 1 AND risk_tier = 'high'
				AND required_gate = 'debug_maximum_access' AND operator_confirmed = 1)
		),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256
			AND instr(run_id, char(0)) = 0),
		CHECK(mission_id = trim(mission_id) AND length(mission_id) BETWEEN 1 AND 256
			AND instr(mission_id, char(0)) = 0),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0),
		CHECK(reason = trim(reason) AND length(reason) BETWEEN 1 AND 1024
			AND instr(reason, char(0)) = 0)
	);`,
		`INSERT INTO run_execution_permission_snapshots_v126
		(id, run_id, mission_id, revision, protocol_version, mode, approval_policy,
		command_scope, filesystem_scope, network_scope, persistent_terminal,
		background_process, agent_terminal_input, risk_tier, required_gate,
		policy_version, operator_confirmed, process_enabled, execution_authorized,
		capability_grant, requested_by, reason, created_at)
		SELECT id, run_id, mission_id, revision, protocol_version, mode, approval_policy,
		command_scope, filesystem_scope, network_scope, persistent_terminal,
		background_process, agent_terminal_input, risk_tier, required_gate,
		policy_version, operator_confirmed, process_enabled, execution_authorized,
		capability_grant, requested_by, reason, created_at
		FROM run_execution_permission_snapshots;`,
		`DROP TABLE run_execution_permission_snapshots;`,
		`ALTER TABLE run_execution_permission_snapshots_v126
		RENAME TO run_execution_permission_snapshots;`,
		`CREATE INDEX idx_run_execution_permission_snapshots_run_revision
		ON run_execution_permission_snapshots(run_id, revision DESC);`,
		`CREATE TRIGGER trg_run_execution_permission_snapshot_insert
		BEFORE INSERT ON run_execution_permission_snapshots
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND julianday(NEW.created_at) >= julianday(run.created_at)
				AND (
					(NEW.revision = 1 AND NEW.mode = 'conservative'
						AND run.status = 'created' AND NOT EXISTS (
							SELECT 1 FROM run_execution_permission_snapshots existing
							WHERE existing.run_id = NEW.run_id
						))
					OR
					(NEW.revision > 1 AND run.status IN ('created', 'paused')
						AND NOT EXISTS (
							SELECT 1 FROM run_execution_leases lease
							WHERE lease.run_id = NEW.run_id AND lease.status = 'active'
								AND julianday(lease.expires_at) > julianday('now')
						)
						AND EXISTS (
							SELECT 1 FROM run_execution_permission_snapshots previous
							WHERE previous.run_id = NEW.run_id
								AND previous.revision = NEW.revision - 1
								AND previous.protocol_version = NEW.protocol_version
								AND previous.policy_version = NEW.policy_version
								AND julianday(NEW.created_at) >= julianday(previous.created_at)
						))
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Run execution permission binding or transition is invalid');
		END;`,
		`CREATE TRIGGER trg_run_execution_permission_snapshot_update_immutable
		BEFORE UPDATE ON run_execution_permission_snapshots BEGIN
			SELECT RAISE(ABORT, 'Run execution permission snapshot cannot be updated');
		END;`,
		`CREATE TRIGGER trg_run_execution_permission_snapshot_delete_immutable
		BEFORE DELETE ON run_execution_permission_snapshots BEGIN
			SELECT RAISE(ABORT, 'Run execution permission snapshot cannot be deleted');
		END;`,
	}
	statements = append(statements,
		requireMigrationTrigger("trg_run_execution_permission_operation_insert",
			runExecutionPermissionStatements),
		requireMigrationTrigger("trg_controlled_command_proposal_insert_binding",
			hostCommandProposalStatements),
		requireMigrationTrigger("trg_host_command_execution_intent_insert_binding",
			hostCommandExecutionStatements),
		requireMigrationTrigger("trg_host_command_proposal_insert_binding",
			hostCommandProposalStatements),
		requireMigrationTrigger("trg_sandbox_docker_product_admission_insert",
			sandboxDockerProductAdmissionStatements),
		requireMigrationTrigger("trg_command_runtime_job_insert_scope",
			commandRuntimeStatements),
	)
	return statements
}()

func requireMigrationTrigger(name string, statements []string) string {
	prefix := "CREATE TRIGGER " + name
	for _, statement := range statements {
		if strings.HasPrefix(strings.TrimSpace(statement), prefix) {
			return statement
		}
	}
	panic("required migration trigger is unavailable: " + name)
}
