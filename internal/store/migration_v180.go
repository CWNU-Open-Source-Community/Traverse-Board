package store

import "strings"

// Frozen historical column list: future writer columns must not change this migration.
const commandRuntimeV179Columns = `id, operation_digest, request_fingerprint,
	invocation_id, run_id, mission_id, session_id, workspace_id, root_agent_id,
	workspace_root_sha256, mode_snapshot_id, mode_revision, profile_snapshot_id,
	profile_revision, permission_snapshot_id, permission_revision, permission_mode,
	lease_id, lease_generation, lease_owner_id, owner_id, owner_generation,
	owner_renewed_at, owner_expires_at, intent_json,
	spec_fingerprint, profile, executable_path, executable_sha256,
	environment_sha256, working_directory, stdin_policy, network, credentials,
	timeout_milliseconds, inline_limit_bytes, artifact_limit_bytes, state, pid,
	process_group, stdout, stderr, stdout_observed_bytes, stderr_observed_bytes,
	output_cursor, output_base_cursor, output_frames_json, stdout_sha256,
	stderr_sha256, truncation_reason, exit_code, timed_out, cancelled, killed,
	tree_reaped, job_assigned_at_creation, stdin_closed, stdin_write_count,
	version, created_at, started_at, completed_at, updated_at,
	adapter_kind, adapter_backend, adapter_backend_identity, adapter_generation,
	adapter_isolation_grade, adapter_network_policy, adapter_credential_policy,
	permission_runtime_epoch, permission_generation`

// Extend the existing Job ledger. Preserve old rows, rowids, actor references,
// state transitions and immutable owner receipts; no new execution database.
func commandOperationApprovalStatements() []string {
	create := requireMigrationStatement("CREATE TABLE command_runtime_jobs_v163 (", commandRuntimeHostNetworkStatements)
	create = replaceCommandRuntimeMigrationFragment(create, "CREATE TABLE command_runtime_jobs_v163 (", "CREATE TABLE command_runtime_jobs_v180 (")
	create = strings.ReplaceAll(create, "permission_mode = 'workspace_access'", "permission_mode IN ('workspace_access','ask','auto','full')")
	// Keep legacy_unbound restricted to its historical modes.
	create = strings.ReplaceAll(create, "adapter_kind = 'host_unsandboxed' AND permission_mode IN ('full_access', 'debug')", "adapter_kind = 'host_unsandboxed' AND permission_mode IN ('full_access','debug','ask','auto','full')")
	create = strings.ReplaceAll(create, "AND permission_mode IN ('full_access', 'debug') AND adapter_network_policy", "AND permission_mode IN ('full_access','debug','ask','auto','full') AND adapter_network_policy")
	create = replaceCommandRuntimeMigrationFragment(create, "\t\tpermission_generation INTEGER NOT NULL DEFAULT 0,", "\t\tpermission_generation INTEGER NOT NULL DEFAULT 0,\n\t\trun_authorization_fence INTEGER NOT NULL DEFAULT 0 CHECK(run_authorization_fence >= 0),")
	const oldBinding = "CHECK((permission_runtime_epoch = '' AND permission_generation = 0) OR (length(permission_runtime_epoch) BETWEEN 1 AND 256 AND permission_runtime_epoch = trim(permission_runtime_epoch) AND instr(permission_runtime_epoch, char(0)) = 0 AND permission_generation > 0))"
	const newBinding = `CHECK((permission_mode NOT IN ('ask','auto','full') AND run_authorization_fence=0 AND
			((permission_runtime_epoch='' AND permission_generation=0) OR
			(length(permission_runtime_epoch) BETWEEN 1 AND 256 AND permission_generation>0))) OR
			(permission_mode IN ('ask','auto','full') AND
			 ((permission_mode IN ('ask','auto') AND permission_generation=0) OR
			  (permission_mode='full' AND permission_generation>0 AND length(permission_runtime_epoch)>0)) AND
			 ((permission_runtime_epoch='' AND run_authorization_fence=0) OR
			  (length(permission_runtime_epoch) BETWEEN 1 AND 256 AND run_authorization_fence>0)))),
		CHECK(permission_runtime_epoch=trim(permission_runtime_epoch) AND instr(permission_runtime_epoch,char(0))=0)`
	create = replaceCommandRuntimeMigrationFragment(create, oldBinding, newBinding)
	insert := requireMigrationTrigger("trg_command_runtime_job_insert_scope", debugFullAccessInheritanceStatements)
	insert = strings.ReplaceAll(insert, "permission.mode IN ('full_access', 'debug')", "permission.mode IN ('full_access','debug','ask','auto','full')")
	insert = strings.ReplaceAll(insert, "permission.mode = 'workspace_access'", "permission.mode IN ('workspace_access','ask','auto','full')")
	transition := requireMigrationTrigger("trg_command_runtime_job_update_transition", commandRuntimeHostNetworkStatements)
	transition = replaceCommandRuntimeMigrationFragment(transition, "OR NEW.permission_generation != OLD.permission_generation", "OR NEW.permission_generation != OLD.permission_generation OR NEW.run_authorization_fence != OLD.run_authorization_fence")
	return []string{
		create,
		`INSERT INTO command_runtime_jobs_v180 (rowid, protocol_version, ` + commandRuntimeV179Columns + `, run_authorization_fence)
		 SELECT rowid, protocol_version, ` + commandRuntimeV179Columns + `, 0 FROM command_runtime_jobs;`,
		`DROP TABLE command_runtime_jobs;`,
		`ALTER TABLE command_runtime_jobs_v180 RENAME TO command_runtime_jobs;`,
		requireMigrationStatement("CREATE INDEX idx_command_runtime_jobs_run_created", commandRuntimeStatements),
		requireMigrationStatement("CREATE INDEX idx_command_runtime_jobs_active", commandRuntimeStatements),
		insert,
		requireMigrationTrigger("trg_command_runtime_job_insert_limit", commandRuntimeAdapterStatements),
		transition,
		requireMigrationTrigger("trg_command_runtime_job_delete_immutable", commandRuntimeAdapterStatements),
	}
}
