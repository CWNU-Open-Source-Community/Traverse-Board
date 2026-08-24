package store

import "strings"

// dockerLifecycleStdinActionStatements extends the immutable v97 Docker
// lifecycle WAL with one metadata-only attach_stdin action. The input bytes
// remain process-local; the durable row proves only that the exact running
// container, resource generation, and active lifecycle lease were fenced
// before the daemon attachment was opened.
var dockerLifecycleStdinActionStatements = func() []string {
	createActions := requireMigrationStatement(
		"CREATE TABLE sandbox_docker_lifecycle_actions (",
		sandboxDockerLifecycleStatements)
	createActions = replaceDockerLifecycleStdinMigrationFragment(createActions,
		"CREATE TABLE sandbox_docker_lifecycle_actions (",
		"CREATE TABLE sandbox_docker_lifecycle_actions_v132 (")
	createActions = replaceDockerLifecycleStdinMigrationFragment(createActions,
		"CHECK(verb IN ('create', 'start', 'term', 'kill', 'delete'))",
		"CHECK(verb IN ('create', 'start', 'attach_stdin', 'term', 'kill', 'delete'))")

	insertAction := requireMigrationTrigger(
		"trg_sandbox_docker_lifecycle_action_insert",
		sandboxDockerLifecycleStatements)
	insertAction = replaceDockerLifecycleStdinMigrationFragment(insertAction,
		"\t\t\t\t\tOR (NEW.verb IN ('term', 'kill')",
		"\t\t\t\t\tOR (NEW.verb = 'attach_stdin'\n"+
			"\t\t\t\t\t\tAND EXISTS (SELECT 1\n"+
			"\t\t\t\t\t\t\tFROM sandbox_docker_lifecycle_transitions transition\n"+
			"\t\t\t\t\t\t\tWHERE transition.intent_id = NEW.intent_id\n"+
			"\t\t\t\t\t\t\t\tAND transition.state = 'started')\n"+
			"\t\t\t\t\t\tAND NOT EXISTS (SELECT 1\n"+
			"\t\t\t\t\t\t\tFROM sandbox_docker_lifecycle_transitions transition\n"+
			"\t\t\t\t\t\t\tWHERE transition.intent_id = NEW.intent_id\n"+
			"\t\t\t\t\t\t\t\tAND transition.state IN ('exited', 'cleaning', 'cleaned')))\n"+
			"\t\t\t\t\tOR (NEW.verb IN ('term', 'kill')")

	return []string{
		`DROP TRIGGER trg_sandbox_docker_lifecycle_action_insert`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_action_update_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_action_delete_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_transition_insert`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_insert`,
		createActions,
		`INSERT INTO sandbox_docker_lifecycle_actions_v132
			SELECT * FROM sandbox_docker_lifecycle_actions`,
		`DROP TABLE sandbox_docker_lifecycle_actions`,
		`ALTER TABLE sandbox_docker_lifecycle_actions_v132
			RENAME TO sandbox_docker_lifecycle_actions`,
		insertAction,
		requireMigrationTrigger("trg_sandbox_docker_lifecycle_action_update_immutable",
			sandboxDockerLifecycleStatements),
		requireMigrationTrigger("trg_sandbox_docker_lifecycle_action_delete_immutable",
			sandboxDockerLifecycleStatements),
		requireMigrationTrigger("trg_sandbox_docker_lifecycle_transition_insert",
			sandboxDockerLifecycleStatements),
		requireMigrationTrigger("trg_sandbox_docker_lifecycle_cleanup_receipt_insert",
			legacyDockerLifecycleCleanupTriggerCompatibilityStatements),
	}
}()

func replaceDockerLifecycleStdinMigrationFragment(statement, old, replacement string) string {
	if !strings.Contains(statement, old) {
		panic("Docker lifecycle stdin migration fragment is unavailable: " + old)
	}
	return strings.Replace(statement, old, replacement, 1)
}
