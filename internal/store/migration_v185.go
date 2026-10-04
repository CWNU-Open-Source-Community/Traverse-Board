package store

import "strings"

// The existing actor ledger is written after the Job in the same transaction.
// This immutable insert discriminator keeps old Jobs under the agent predicate;
// it is not an approval and cannot recreate process-local operator consent.
func operatorCommandInvocationStatements() []string {
	insert := requireMigrationTrigger("trg_command_runtime_job_insert_scope", commandOperationApprovalStatements())
	const old = "AND run.status = 'running' AND root.parent_id IS NULL"
	if strings.Count(insert, old) != 1 {
		panic("operator command migration lost the immutable Run-state predicate")
	}
	insert = strings.Replace(insert, old, "AND (run.status = 'running' OR (NEW.operator_invocation = 1 AND run.status IN ('created', 'paused'))) AND root.parent_id IS NULL", 1)
	return []string{
		`ALTER TABLE command_runtime_jobs ADD COLUMN operator_invocation INTEGER NOT NULL DEFAULT 0 CHECK(operator_invocation IN (0,1));`,
		`DROP TRIGGER trg_command_runtime_job_insert_scope;`, insert,
		`CREATE TRIGGER trg_command_runtime_operator_invocation_immutable
		 BEFORE UPDATE OF operator_invocation ON command_runtime_jobs
		 WHEN NEW.operator_invocation != OLD.operator_invocation
		 BEGIN SELECT RAISE(ABORT, 'Command Runtime invocation source is immutable'); END;`,
		`CREATE TRIGGER trg_command_runtime_operator_actor_binding
		 BEFORE INSERT ON command_runtime_job_agents
		 WHEN (NEW.attribution_source = 'operator_root') !=
		  (SELECT operator_invocation FROM command_runtime_jobs WHERE id = NEW.job_id)
		 BEGIN SELECT RAISE(ABORT, 'Command Runtime invocation source differs from its actor'); END;`,
	}
}
