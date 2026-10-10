package store

// v188 introduced the distinct SBX profile but left the command Job insert
// fence with only the historical Local and Docker Engine adapter mappings.
// Repair that production wiring gap in a forward migration. The added tuple
// uses the existing sandbox permission, network, credential, Run and lease
// predicates; no historical Job or persisted authorization is rewritten.
func sbxCommandRuntimeScopeStatements() []string {
	insert := requireMigrationTrigger("trg_command_runtime_job_insert_scope", operatorCommandInvocationStatements())
	const existing = "OR (NEW.adapter_backend = 'docker_standard_code' AND profile.profile = 'docker')"
	insert = replaceCommandRuntimeMigrationFragment(insert, existing, existing+
		"\n\t\t\t\t\t\t\tOR (NEW.adapter_backend = 'docker_sandboxes' AND profile.profile = 'sbx')")
	return []string{`DROP TRIGGER trg_command_runtime_job_insert_scope;`, insert}
}
