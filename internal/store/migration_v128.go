package store

import "strings"

// standardCodeDockerWorkspaceAccessStatements extends the released Docker
// admission ledger with the additive workspace_access permission introduced in
// v126. The parent table is rebuilt with foreign keys temporarily disabled so
// existing immutable admissions and their lifecycle children remain intact.
var standardCodeDockerWorkspaceAccessStatements = func() []string {
	create := requireMigrationStatement(
		"CREATE TABLE sandbox_docker_product_admissions (",
		sandboxDockerProductAdmissionStatements)
	create = strings.Replace(create,
		"CREATE TABLE sandbox_docker_product_admissions (",
		"CREATE TABLE sandbox_docker_product_admissions_v128 (", 1)
	create = strings.Replace(create,
		"CHECK(permission_mode IN ('approval', 'full_access', 'debug'))",
		"CHECK(permission_mode IN ('workspace_access', 'approval', 'full_access', 'debug'))", 1)
	if !strings.Contains(create,
		"CHECK(permission_mode IN ('workspace_access', 'approval', 'full_access', 'debug'))") {
		panic("Docker product admission permission constraint is unavailable")
	}
	return []string{
		`DROP TRIGGER trg_sandbox_docker_product_admission_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_cancellation_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_start_request_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_launch_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_receipt_insert;`,
		`DROP TRIGGER trg_sandbox_docker_product_admission_update_immutable;`,
		`DROP TRIGGER trg_sandbox_docker_product_admission_delete_immutable;`,
		create,
		`INSERT INTO sandbox_docker_product_admissions_v128
			SELECT * FROM sandbox_docker_product_admissions;`,
		`DROP TABLE sandbox_docker_product_admissions;`,
		`ALTER TABLE sandbox_docker_product_admissions_v128
			RENAME TO sandbox_docker_product_admissions;`,
		requireMigrationStatement(
			"CREATE INDEX idx_sandbox_docker_product_admissions_run_created",
			sandboxDockerProductAdmissionStatements),
		requireMigrationTrigger("trg_sandbox_docker_product_admission_insert",
			sandboxDockerProductAdmissionStatements),
		requireMigrationTrigger("trg_sandbox_docker_product_cancellation_insert",
			sandboxDockerProductAdmissionStatements),
		requireMigrationTrigger("trg_sandbox_docker_product_start_request_insert",
			sandboxDockerProductAdmissionStatements),
		requireMigrationTrigger("trg_sandbox_docker_product_launch_insert",
			sandboxDockerProductAdmissionStatements),
		requireMigrationTrigger("trg_sandbox_docker_product_receipt_insert",
			sandboxDockerProductAdmissionStatements),
		requireMigrationTrigger("trg_sandbox_docker_product_admission_update_immutable",
			sandboxDockerProductAdmissionStatements),
		requireMigrationTrigger("trg_sandbox_docker_product_admission_delete_immutable",
			sandboxDockerProductAdmissionStatements),
	}
}()

func requireMigrationStatement(prefix string, statements []string) string {
	for _, statement := range statements {
		if strings.HasPrefix(strings.TrimSpace(statement), prefix) {
			return statement
		}
	}
	panic("required migration statement is unavailable: " + prefix)
}
