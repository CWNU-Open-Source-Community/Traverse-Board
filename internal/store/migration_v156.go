package store

import "strings"

// Keep the original proposal bytes and request receipts intact. Only the number
// of meaningful alternatives and the operator's manual acceptance policy change.
var planDeliverySimplificationStatements = buildPlanDeliverySimplificationStatements()

func buildPlanDeliverySimplificationStatements() []string {
	proposal := requireMigrationStatement("CREATE TABLE plan_delivery_proposals (", planDeliveryStatements)
	proposal = threadPlanReplaceOnce(proposal, "CHECK(direction_count = 3)", "CHECK(direction_count BETWEEN 1 AND 3)")
	proposal = strings.Replace(proposal, "CREATE TABLE plan_delivery_proposals (", "CREATE TABLE plan_delivery_proposals_v156 (", 1)
	statements := []string{
		`PRAGMA legacy_alter_table = ON;`,
		`DROP TRIGGER trg_plan_delivery_proposal_insert;`,
		`DROP TRIGGER trg_plan_delivery_proposal_update_immutable;`,
		`DROP TRIGGER trg_plan_delivery_proposal_delete_immutable;`,
		proposal,
		`INSERT INTO plan_delivery_proposals_v156 SELECT * FROM plan_delivery_proposals;`,
		`DROP TABLE plan_delivery_proposals;`,
		`ALTER TABLE plan_delivery_proposals_v156 RENAME TO plan_delivery_proposals;`,
		requireMigrationStatement("CREATE INDEX idx_plan_delivery_proposals_run_created", planDeliveryStatements),
		threadPlanExistingTrigger(threadPlanContinuationStatements, "trg_plan_delivery_proposal_insert"),
		threadPlanExistingTrigger(planDeliveryStatements, "trg_plan_delivery_proposal_update_immutable"),
		threadPlanExistingTrigger(planDeliveryStatements, "trg_plan_delivery_proposal_delete_immutable"),
		`PRAGMA legacy_alter_table = OFF;`,
		`ALTER TABLE plan_delivery_selections ADD COLUMN manual_acceptance TEXT NOT NULL DEFAULT 'required'
			CHECK(manual_acceptance IN ('required', 'on_demand'));`,
	}
	statements = append(statements, planDeliveryOptionalCheckpointStatements...)
	return append(statements, threadPlanOnDemandContinuationStatements...)
}

var planDeliveryOptionalCheckpointStatements = []string{
	`DROP TRIGGER trg_delivery_work_item_completion_guard;`,
	`CREATE TRIGGER trg_delivery_work_item_completion_guard
		BEFORE UPDATE OF status ON work_items
		WHEN NEW.status = 'completed' AND OLD.status != 'completed'
			AND EXISTS (SELECT 1 FROM plan_delivery_selection_items selected
				JOIN plan_delivery_selections selection ON selection.id = selected.selection_id
				JOIN delivery_gate_enrollments enrollment
					ON enrollment.run_id = selection.run_id AND enrollment.selection_id = selection.id
				WHERE selected.work_item_id = OLD.id)
			AND (NOT EXISTS (
				SELECT 1 FROM run_mode_snapshots mode WHERE mode.run_id = OLD.run_id AND mode.phase = 'deliver'
					AND NOT EXISTS (SELECT 1 FROM run_mode_snapshots later
						WHERE later.run_id = OLD.run_id AND later.revision > mode.revision)
			) OR NOT EXISTS (
				SELECT 1 FROM plan_delivery_selection_items selected
				JOIN plan_delivery_selections selection ON selection.id = selected.selection_id
				JOIN plan_delivery_modules module ON module.proposal_id = selection.proposal_id
					AND module.direction_ordinal = selection.direction_ordinal AND module.ordinal = selected.module_ordinal
				WHERE selected.work_item_id = NEW.id AND selection.run_id = NEW.run_id
					AND NEW.title = module.title AND NEW.description = module.objective
					AND NOT EXISTS (SELECT value FROM json_each(NEW.acceptance_json)
						EXCEPT SELECT value FROM json_each(module.acceptance_json))
					AND NOT EXISTS (SELECT value FROM json_each(module.acceptance_json)
						EXCEPT SELECT value FROM json_each(NEW.acceptance_json))
					AND NOT EXISTS (
						SELECT 1 FROM json_each(module.dependencies_json) expected
						WHERE NOT EXISTS (SELECT 1 FROM plan_delivery_selection_items dependency
							JOIN work_item_dependencies edge ON edge.depends_on_id = dependency.work_item_id
								AND edge.work_item_id = NEW.id AND edge.run_id = NEW.run_id
							JOIN work_items dependency_work ON dependency_work.id = dependency.work_item_id
								AND dependency_work.run_id = NEW.run_id AND dependency_work.status = 'completed'
							WHERE dependency.selection_id = selection.id AND dependency.module_ordinal = expected.value))
					AND NOT EXISTS (
						SELECT 1 FROM work_item_dependencies edge WHERE edge.work_item_id = NEW.id
							AND NOT EXISTS (SELECT 1 FROM plan_delivery_selection_items dependency
								JOIN json_each(module.dependencies_json) expected ON expected.value = dependency.module_ordinal
								WHERE dependency.selection_id = selection.id AND dependency.work_item_id = edge.depends_on_id))
			) OR (EXISTS (
				SELECT 1 FROM plan_delivery_selection_items selected
				JOIN plan_delivery_selections selection ON selection.id = selected.selection_id
				WHERE selected.work_item_id = OLD.id AND selection.manual_acceptance = 'required'
			) AND NOT EXISTS (
				SELECT 1 FROM delivery_checkpoints checkpoint
				JOIN delivery_checkpoint_operations operation ON operation.checkpoint_id = checkpoint.id
				JOIN run_mode_snapshots mode ON mode.id = checkpoint.mode_snapshot_id
				WHERE checkpoint.run_id = OLD.run_id AND checkpoint.work_item_id = OLD.id
					AND checkpoint.work_item_version = OLD.version
					AND mode.run_id = OLD.run_id AND mode.revision = checkpoint.mode_revision AND mode.phase = 'deliver'
					AND NOT EXISTS (SELECT 1 FROM run_mode_snapshots later
						WHERE later.run_id = OLD.run_id AND later.revision > mode.revision)
			)))
		BEGIN SELECT RAISE(ABORT, 'selected WorkItem requires Deliver phase and its manual acceptance policy'); END;`,
	`DROP TRIGGER trg_delivery_run_completion_guard;`,
	`CREATE TRIGGER trg_delivery_run_completion_guard
		BEFORE UPDATE OF status ON runs
		WHEN NEW.status = 'completed' AND OLD.status != 'completed'
			AND EXISTS (SELECT 1 FROM delivery_gate_enrollments enrollment WHERE enrollment.run_id = NEW.id)
			AND EXISTS (
				SELECT 1 FROM plan_delivery_selection_items selected
				JOIN plan_delivery_selections selection ON selection.id = selected.selection_id
				JOIN work_items work ON work.id = selected.work_item_id
				WHERE selection.run_id = NEW.id AND (work.status != 'completed' OR (
					selection.manual_acceptance = 'required' AND NOT EXISTS (
						SELECT 1 FROM delivery_checkpoints checkpoint
						JOIN delivery_checkpoint_operations operation ON operation.checkpoint_id = checkpoint.id
						JOIN run_mode_snapshots mode ON mode.id = checkpoint.mode_snapshot_id
						WHERE checkpoint.run_id = NEW.id AND checkpoint.selection_id = selection.id
							AND checkpoint.work_item_id = work.id AND checkpoint.work_item_version = work.version - 1
							AND mode.run_id = NEW.id AND mode.revision = checkpoint.mode_revision AND mode.phase = 'deliver'
					) AND NOT EXISTS (
						SELECT 1 FROM thread_plan_completed_sources source WHERE source.run_id = NEW.id
							AND source.selection_id = selection.id AND source.work_item_id = work.id
					)
				))
			)
		BEGIN SELECT RAISE(ABORT, 'Run has incomplete Delivery acceptance gates'); END;`,
}
