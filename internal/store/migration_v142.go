package store

// debugFullAccessInheritanceStatements aligns the two durable stateless host
// execution ledgers with the execution-policy hierarchy: Debug includes every
// Full Access capability, while retaining its own immutable permission mode.
// The application layer still validates the exact live snapshot/runtime gate;
// these constraints only stop SQLite from incorrectly rejecting that binding.
var debugFullAccessInheritanceStatements = func() []string {
	createJobs := requireMigrationStatement(
		"CREATE TABLE command_runtime_jobs_v131 (", commandRuntimeAdapterStatements)
	createJobs = replaceCommandRuntimeMigrationFragment(createJobs,
		"CREATE TABLE command_runtime_jobs_v131 (",
		"CREATE TABLE command_runtime_jobs_v142 (")
	createJobs = replaceCommandRuntimeMigrationFragment(createJobs,
		"OR (adapter_kind = 'host_unsandboxed' AND permission_mode = 'full_access'",
		"OR (adapter_kind = 'host_unsandboxed' AND permission_mode IN ('full_access', 'debug')")
	createJobs = replaceCommandRuntimeMigrationFragment(createJobs,
		"OR (adapter_kind = 'legacy_unbound' AND permission_mode = 'full_access'",
		"OR (adapter_kind = 'legacy_unbound' AND permission_mode IN ('full_access', 'debug')")

	jobInsertScope := requireMigrationTrigger(
		"trg_command_runtime_job_insert_scope", commandRuntimeAdapterStatements)
	jobInsertScope = replaceCommandRuntimeMigrationFragment(jobInsertScope,
		"NEW.adapter_kind = 'host_unsandboxed' AND permission.mode = 'full_access'",
		"NEW.adapter_kind = 'host_unsandboxed' AND permission.mode IN ('full_access', 'debug')")

	createHostIntents := requireMigrationStatement(
		"CREATE TABLE host_command_execution_intents (", hostCommandExecutionStatements)
	createHostIntents = replaceCommandRuntimeMigrationFragment(createHostIntents,
		"CREATE TABLE host_command_execution_intents (",
		"CREATE TABLE host_command_execution_intents_v142 (")
	createHostIntents = replaceCommandRuntimeMigrationFragment(createHostIntents,
		"CHECK(permission_mode = 'full_access')",
		"CHECK(permission_mode IN ('full_access', 'debug'))")

	hostIntentInsert := requireMigrationTrigger(
		"trg_host_command_execution_intent_insert_binding", hostCommandExecutionStatements)
	hostIntentInsert = replaceCommandRuntimeMigrationFragment(hostIntentInsert,
		"AND permission.mode = 'full_access'",
		"AND permission.mode IN ('full_access', 'debug')")

	return []string{
		createJobs,
		`INSERT INTO command_runtime_jobs_v142 SELECT * FROM command_runtime_jobs;`,
		`DROP TABLE command_runtime_jobs;`,
		`ALTER TABLE command_runtime_jobs_v142 RENAME TO command_runtime_jobs;`,
		requireMigrationStatement("CREATE INDEX idx_command_runtime_jobs_run_created",
			commandRuntimeStatements),
		requireMigrationStatement("CREATE INDEX idx_command_runtime_jobs_active",
			commandRuntimeStatements),
		jobInsertScope,
		requireMigrationTrigger("trg_command_runtime_job_insert_limit",
			commandRuntimeAdapterStatements),
		requireMigrationTrigger("trg_command_runtime_job_update_transition",
			commandRuntimeAdapterStatements),
		requireMigrationTrigger("trg_command_runtime_job_delete_immutable",
			commandRuntimeAdapterStatements),

		`DROP TRIGGER trg_host_command_execution_intent_insert_binding;`,
		`DROP TRIGGER trg_host_command_execution_intent_update_immutable;`,
		`DROP TRIGGER trg_host_command_execution_intent_delete_immutable;`,
		`DROP TRIGGER trg_host_command_execution_operation_insert_binding;`,
		`DROP TRIGGER trg_host_command_execution_operation_update_immutable;`,
		`DROP TRIGGER trg_host_command_execution_operation_delete_immutable;`,
		`DROP TRIGGER trg_host_command_execution_receipt_insert_binding;`,
		`DROP TRIGGER trg_host_command_execution_receipt_update_immutable;`,
		`DROP TRIGGER trg_host_command_execution_receipt_delete_immutable;`,
		createHostIntents,
		`INSERT INTO host_command_execution_intents_v142
			SELECT * FROM host_command_execution_intents;`,
		`DROP TABLE host_command_execution_intents;`,
		`ALTER TABLE host_command_execution_intents_v142
			RENAME TO host_command_execution_intents;`,
		requireMigrationStatement("CREATE INDEX idx_host_command_execution_intents_run_created",
			hostCommandExecutionStatements),
		hostIntentInsert,
		requireMigrationTrigger("trg_host_command_execution_intent_update_immutable",
			hostCommandExecutionStatements),
		requireMigrationTrigger("trg_host_command_execution_intent_delete_immutable",
			hostCommandExecutionStatements),
		requireMigrationTrigger("trg_host_command_execution_operation_insert_binding",
			hostCommandExecutionStatements),
		requireMigrationTrigger("trg_host_command_execution_operation_update_immutable",
			hostCommandExecutionStatements),
		requireMigrationTrigger("trg_host_command_execution_operation_delete_immutable",
			hostCommandExecutionStatements),
		requireMigrationTrigger("trg_host_command_execution_receipt_insert_binding",
			hostCommandExecutionStatements),
		requireMigrationTrigger("trg_host_command_execution_receipt_update_immutable",
			hostCommandExecutionStatements),
		requireMigrationTrigger("trg_host_command_execution_receipt_delete_immutable",
			hostCommandExecutionStatements),
	}
}()
