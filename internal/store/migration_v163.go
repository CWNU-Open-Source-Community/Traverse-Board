package store

import "strings"

// commandRuntimeHostNetworkStatements widens only the host adapter's explicit
// network intent. The two optional runtime-grant fields are immutable launch
// provenance; historical jobs receive the empty pair.
var commandRuntimeHostNetworkStatements = func() []string {
	createJobs := requireMigrationStatement(
		"CREATE TABLE command_runtime_jobs_v142 (", debugFullAccessInheritanceStatements)
	createJobs = replaceCommandRuntimeMigrationFragment(createJobs,
		"CREATE TABLE command_runtime_jobs_v142 (",
		"CREATE TABLE command_runtime_jobs_v163 (")
	createJobs = replaceCommandRuntimeMigrationFragment(createJobs,
		"\t\tadapter_credential_policy TEXT NOT NULL,\n\t\tFOREIGN KEY(run_id)",
		"\t\tadapter_credential_policy TEXT NOT NULL,\n"+
			"\t\tpermission_runtime_epoch TEXT NOT NULL DEFAULT '',\n"+
			"\t\tpermission_generation INTEGER NOT NULL DEFAULT 0,\n"+
			"\t\tFOREIGN KEY(run_id)")
	createJobs = replaceCommandRuntimeMigrationFragment(createJobs,
		"CHECK(network = 'disabled' AND credentials = 'none')",
		"CHECK(credentials = 'none' AND (network = 'disabled' OR ("+
			"network = 'host' AND adapter_kind = 'host_unsandboxed' "+
			"AND permission_mode IN ('full_access', 'debug') "+
			"AND adapter_network_policy = 'host_available')))")
	createJobs = replaceCommandRuntimeMigrationFragment(createJobs,
		"CHECK(profile IN ('powershell', 'bash', 'process'))",
		"CHECK((permission_runtime_epoch = '' AND permission_generation = 0) "+
			"OR (length(permission_runtime_epoch) BETWEEN 1 AND 256 "+
			"AND permission_runtime_epoch = trim(permission_runtime_epoch) "+
			"AND instr(permission_runtime_epoch, char(0)) = 0 "+
			"AND permission_generation > 0)),\n"+
			"\t\tCHECK(profile IN ('powershell', 'bash', 'process'))")

	transition := requireMigrationTrigger(
		"trg_command_runtime_job_update_transition", commandRuntimeAdapterStatements)
	transition = replaceCommandRuntimeMigrationFragment(transition,
		"OR NEW.adapter_credential_policy != OLD.adapter_credential_policy",
		"OR NEW.adapter_credential_policy != OLD.adapter_credential_policy\n"+
			"\t\t\tOR NEW.permission_runtime_epoch != OLD.permission_runtime_epoch\n"+
			"\t\t\tOR NEW.permission_generation != OLD.permission_generation")

	columns := strings.Join([]string{
		"id", "protocol_version", "operation_digest", "request_fingerprint",
		"invocation_id", "run_id", "mission_id", "session_id", "workspace_id",
		"root_agent_id", "workspace_root_sha256", "mode_snapshot_id", "mode_revision",
		"profile_snapshot_id", "profile_revision", "permission_snapshot_id",
		"permission_revision", "permission_mode", "lease_id", "lease_generation",
		"lease_owner_id", "owner_id", "owner_generation", "owner_renewed_at",
		"owner_expires_at", "intent_json", "spec_fingerprint", "profile",
		"executable_path", "executable_sha256", "environment_sha256",
		"working_directory", "stdin_policy", "network", "credentials",
		"timeout_milliseconds", "inline_limit_bytes", "artifact_limit_bytes", "state",
		"pid", "process_group", "stdout", "stderr", "stdout_observed_bytes",
		"stderr_observed_bytes", "output_cursor", "output_base_cursor",
		"output_frames_json", "stdout_sha256", "stderr_sha256", "truncation_reason",
		"exit_code", "timed_out", "cancelled", "killed", "tree_reaped",
		"job_assigned_at_creation", "stdin_closed", "stdin_write_count", "version",
		"created_at", "started_at", "completed_at", "updated_at", "adapter_kind",
		"adapter_backend", "adapter_backend_identity", "adapter_generation",
		"adapter_isolation_grade", "adapter_network_policy", "adapter_credential_policy",
	}, ", ")
	return []string{
		`DROP TRIGGER trg_command_runtime_job_insert_scope;`,
		`DROP TRIGGER trg_command_runtime_job_insert_limit;`,
		`DROP TRIGGER trg_command_runtime_job_update_transition;`,
		`DROP TRIGGER trg_command_runtime_job_delete_immutable;`,
		`DROP INDEX idx_command_runtime_jobs_run_created;`,
		`DROP INDEX idx_command_runtime_jobs_active;`,
		createJobs,
		`INSERT INTO command_runtime_jobs_v163 (rowid, ` + columns +
			`, permission_runtime_epoch, permission_generation)
			SELECT rowid, ` + columns + `, '', 0 FROM command_runtime_jobs;`,
		`DROP TABLE command_runtime_jobs;`,
		`ALTER TABLE command_runtime_jobs_v163 RENAME TO command_runtime_jobs;`,
		requireMigrationStatement("CREATE INDEX idx_command_runtime_jobs_run_created",
			commandRuntimeStatements),
		requireMigrationStatement("CREATE INDEX idx_command_runtime_jobs_active",
			commandRuntimeStatements),
		requireMigrationTrigger("trg_command_runtime_job_insert_scope",
			debugFullAccessInheritanceStatements),
		requireMigrationTrigger("trg_command_runtime_job_insert_limit",
			commandRuntimeAdapterStatements),
		transition,
		requireMigrationTrigger("trg_command_runtime_job_delete_immutable",
			commandRuntimeAdapterStatements),
	}
}()
