package store

// Frozen v180 admission columns. Do not use the mutable current writer list:
// a later column addition must not change this migration or its checksum.
const dockerAdmissionV180Columns = `id, protocol_version,
	operation_key_digest, request_fingerprint, lifecycle_operation_digest,
	run_id, mission_id, workspace_id, plan_id, candidate_id, preparation_id,
	manifest_json, manifest_fingerprint, plan_fingerprint, spec_fingerprint,
	authority_fingerprint, readiness_fingerprint, readiness_expires_at,
	runtime_epoch_fingerprint, profile_snapshot_id, profile_revision,
	permission_snapshot_id, permission_revision, permission_mode, approval_id,
	approval_version, policy_fingerprint, network_mode, network_target_count,
	cpu_quota_millis, memory_bytes, pids, disk_bytes, wall_clock_seconds,
	log_bytes, log_lines, tool_calls_remaining, decision, reason_code,
	remediation_code, product_entry_enabled, execution_authorized,
	artifact_commit_authorized, requested_by, created_at, admission_fingerprint`

// The native Docker protocol continues to require an exact approved manifest,
// current permission/profile revisions, policy, budget and candidate lease.
// Approval preference is not a substitute for any of that native authority.
// Rebuild the same parent ledger, retaining rowids and every lifecycle child.
func dockerOperationApprovalStatements() []string {
	create := requireMigrationStatement("CREATE TABLE sandbox_docker_product_admissions_v128 (",
		standardCodeDockerWorkspaceAccessStatements)
	create = replaceCommandRuntimeMigrationFragment(create,
		"CREATE TABLE sandbox_docker_product_admissions_v128 (",
		"CREATE TABLE sandbox_docker_product_admissions_v181 (")
	create = replaceCommandRuntimeMigrationFragment(create,
		"CHECK(permission_mode IN ('workspace_access', 'approval', 'full_access', 'debug'))",
		"CHECK(permission_mode IN ('workspace_access', 'approval', 'full_access', 'debug', 'ask', 'auto', 'full'))")
	// v131 is the last admission trigger revision. In particular, restoring v99
	// here would lose the active candidate's exact lease binding.
	insert := requireMigrationTrigger("trg_sandbox_docker_product_admission_insert",
		commandRuntimeAdapterStatements)
	insert = replaceCommandRuntimeMigrationFragment(insert,
		"AND permission.operator_confirmed = 1",
		`AND ((permission.protocol_version = 'run_execution_permission.v1'
					AND permission.mode IN ('workspace_access','approval','full_access','debug')
					AND permission.operator_confirmed = 1)
				 OR (permission.protocol_version = 'run_execution_permission.v2'
					AND permission.policy_version = 'execution_permission_policy.v2'
					AND ((permission.mode IN ('ask','auto') AND permission.operator_confirmed = 0)
					 OR (permission.mode = 'full' AND permission.operator_confirmed = 1))))`)
	result := []string{
		`DROP TRIGGER trg_sandbox_docker_product_admission_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_cancellation_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_start_request_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_launch_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_receipt_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_admission_update_immutable;`,
		`DROP TRIGGER trg_sandbox_docker_product_admission_delete_immutable;`,
		create,
		`INSERT INTO sandbox_docker_product_admissions_v181 (rowid, ` + dockerAdmissionV180Columns + `)
		 SELECT rowid, ` + dockerAdmissionV180Columns + ` FROM sandbox_docker_product_admissions;`,
		`DROP TABLE sandbox_docker_product_admissions;`,
		`ALTER TABLE sandbox_docker_product_admissions_v181 RENAME TO sandbox_docker_product_admissions;`,
		requireMigrationStatement("CREATE INDEX idx_sandbox_docker_product_admissions_run_created",
			sandboxDockerProductAdmissionStatements),
		insert,
	}
	for _, name := range []string{
		"trg_sandbox_docker_product_cancellation_insert",
		"trg_sandbox_docker_product_start_request_insert",
		"trg_sandbox_docker_product_launch_insert",
		"trg_sandbox_docker_product_receipt_insert",
		"trg_sandbox_docker_product_admission_update_immutable",
		"trg_sandbox_docker_product_admission_delete_immutable",
	} {
		result = append(result, requireMigrationTrigger(name, sandboxDockerProductAdmissionStatements))
	}
	return result
}
