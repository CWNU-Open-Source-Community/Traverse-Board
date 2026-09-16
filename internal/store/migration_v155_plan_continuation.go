package store

import "strings"

// These views project immutable provenance already recorded in run_events. They
// are not a new grant or a copy of a prior verification receipt.
var threadPlanContinuationStatements = buildThreadPlanContinuationStatements()

const threadPlanContinuationEvent = "thread.plan_continued"

const threadPlanSourceViewSQL = `CREATE VIEW thread_plan_continuation_sources AS
 SELECT event.run_id, event.subject_id AS proposal_id,
   json_extract(event.payload_json,'$.selection_id') AS selection_id,
   json_extract(event.payload_json,'$.source_proposal_id') AS source_proposal_id,
   json_extract(event.payload_json,'$.source_selection_id') AS source_selection_id,
   json_extract(event.payload_json,'$.predecessor_run_id') AS predecessor_run_id,
   json_extract(item.value,'$.work_item_id') AS work_item_id,
   json_extract(item.value,'$.source_work_item_id') AS source_work_item_id,
   json_extract(item.value,'$.source_version') AS source_version,
   json_extract(item.value,'$.checkpoint_id') AS checkpoint_id,
   event.created_at
 FROM run_events event, json_each(event.payload_json,'$.items') item
 WHERE event.type = 'thread.plan_continued' AND event.source = 'thread_plan_continuation';`

// A completed projection can only refer to an original, valid manual checkpoint.
// Each new event is checked against its immediate predecessor, so repeated
// continuation is bounded and resolves straight to that original receipt.
const threadPlanCompletedViewSQL = `CREATE VIEW thread_plan_completed_sources AS
 SELECT source.*, checkpoint.run_id AS origin_run_id,
   checkpoint.work_item_id AS origin_work_item_id, checkpoint.handoff_note_id,
   origin.completed_at AS origin_completed_at
 FROM thread_plan_continuation_sources source
 JOIN thread_runs current ON current.run_id=source.run_id
 JOIN thread_runs previous ON previous.run_id=source.predecessor_run_id
   AND previous.thread_id=current.thread_id AND previous.ordinal+1=current.ordinal
   AND current.predecessor_run_id=previous.run_id
 JOIN plan_delivery_selections selection ON selection.id=source.selection_id
   AND selection.run_id=source.run_id AND selection.proposal_id=source.proposal_id
 JOIN plan_delivery_selection_items selected ON selected.selection_id=selection.id
   AND selected.work_item_id=source.work_item_id
 JOIN work_items work ON work.id=source.work_item_id AND work.run_id=source.run_id
   AND work.status='completed' AND work.version=1
 JOIN plan_delivery_selections prior_selection ON prior_selection.id=source.source_selection_id
   AND prior_selection.run_id=previous.run_id AND prior_selection.proposal_id=source.source_proposal_id
   AND prior_selection.direction_ordinal=selection.direction_ordinal
 JOIN plan_delivery_selection_items prior_selected ON prior_selected.selection_id=prior_selection.id
   AND prior_selected.work_item_id=source.source_work_item_id
   AND prior_selected.module_ordinal=selected.module_ordinal
 JOIN work_items prior_work ON prior_work.id=source.source_work_item_id
   AND prior_work.run_id=previous.run_id AND prior_work.status='completed'
   AND prior_work.version=source.source_version
 JOIN delivery_checkpoints checkpoint ON checkpoint.id=source.checkpoint_id
 JOIN delivery_checkpoint_operations operation ON operation.checkpoint_id=checkpoint.id
   AND operation.run_id=checkpoint.run_id AND operation.work_item_id=checkpoint.work_item_id
 JOIN work_items origin ON origin.id=checkpoint.work_item_id AND origin.run_id=checkpoint.run_id
   AND origin.status='completed' AND origin.version=checkpoint.work_item_version+1
 JOIN run_mode_snapshots mode ON mode.id=checkpoint.mode_snapshot_id
   AND mode.run_id=checkpoint.run_id AND mode.revision=checkpoint.mode_revision AND mode.phase='deliver'
 JOIN thread_runs origin_binding ON origin_binding.run_id=checkpoint.run_id
   AND origin_binding.thread_id=current.thread_id AND origin_binding.ordinal<=previous.ordinal
 JOIN plan_delivery_modules module ON module.proposal_id=selection.proposal_id
   AND module.direction_ordinal=selection.direction_ordinal AND module.ordinal=selected.module_ordinal
 JOIN plan_delivery_modules original_module ON original_module.proposal_id=checkpoint.proposal_id
   AND original_module.direction_ordinal=checkpoint.direction_ordinal
   AND original_module.ordinal=checkpoint.module_ordinal
 WHERE module.title=original_module.title AND module.objective=original_module.objective
   AND module.acceptance_json=original_module.acceptance_json
   AND module.dependencies_json=original_module.dependencies_json
   AND work.title=module.title AND work.description=module.objective
   AND prior_work.title=module.title AND prior_work.description=module.objective
   AND origin.title=module.title AND origin.description=module.objective
   AND work.acceptance_json=prior_work.acceptance_json AND work.acceptance_json=origin.acceptance_json
   AND NOT EXISTS (SELECT value FROM json_each(work.acceptance_json)
     EXCEPT SELECT value FROM json_each(module.acceptance_json))
   AND NOT EXISTS (SELECT value FROM json_each(module.acceptance_json)
     EXCEPT SELECT value FROM json_each(work.acceptance_json))
   AND NOT EXISTS (
     SELECT 1 FROM json_each(module.dependencies_json) expected
     WHERE NOT EXISTS (SELECT 1 FROM plan_delivery_selection_items dependency
       JOIN work_item_dependencies edge ON edge.depends_on_id=dependency.work_item_id
         AND edge.work_item_id=work.id AND edge.run_id=work.run_id
       WHERE dependency.selection_id=selection.id AND dependency.module_ordinal=expected.value))
   AND NOT EXISTS (
     SELECT 1 FROM work_item_dependencies edge WHERE edge.work_item_id=work.id
       AND NOT EXISTS (SELECT 1 FROM plan_delivery_selection_items dependency
         JOIN json_each(module.dependencies_json) expected ON expected.value=dependency.module_ordinal
         WHERE dependency.selection_id=selection.id AND dependency.work_item_id=edge.depends_on_id))
   AND (checkpoint.work_item_id=prior_work.id OR EXISTS (
     SELECT 1 FROM thread_plan_continuation_sources previous_source
     WHERE previous_source.run_id=previous.run_id
       AND previous_source.selection_id=prior_selection.id
       AND previous_source.work_item_id=prior_work.id
       AND previous_source.checkpoint_id=checkpoint.id));`

func buildThreadPlanContinuationStatements() []string {
	out := []string{threadPlanSourceViewSQL, threadPlanCompletedViewSQL}
	// Ordinary model proposal/selection gates remain byte-for-byte intact. Only a
	// source-checked continuation event permits the new Created epoch projection.
	for _, name := range []string{"trg_plan_delivery_proposal_insert", "trg_plan_delivery_selection_insert"} {
		statement := threadPlanExistingTrigger(planDeliveryStatements, name)
		guard := threadPlanProposalContinuationGuard
		if name == "trg_plan_delivery_selection_insert" {
			guard = threadPlanSelectionContinuationGuard
		}
		statement = threadPlanReplaceOnce(statement, "\n\t\tBEGIN", " AND NOT EXISTS ("+guard+")\n\t\tBEGIN")
		out = append(out, "DROP TRIGGER "+name+";", statement)
	}
	item := threadPlanExistingTrigger(planDeliveryStatements, "trg_plan_delivery_selection_item_insert")
	item = threadPlanReplaceOnce(item, "item.status = 'pending'", `(item.status = 'pending' OR (item.status = 'completed' AND EXISTS (
   SELECT 1 FROM thread_plan_continuation_sources source
   JOIN delivery_checkpoints checkpoint ON checkpoint.id=source.checkpoint_id
   WHERE source.selection_id=selection.id AND source.work_item_id=item.id)))`)
	out = append(out, "DROP TRIGGER trg_plan_delivery_selection_item_insert;", item)
	completion := threadPlanExistingTrigger(deliveryCheckpointStatements, "trg_delivery_run_completion_guard")
	completion = threadPlanReplaceOnce(completion, "work.status != 'completed' OR NOT EXISTS (", "work.status != 'completed' OR (NOT EXISTS (")
	completion = threadPlanReplaceOnce(completion, "\n\t\t\t\t\t)\n\t\t\t\t)", `
     ) AND NOT EXISTS (SELECT 1 FROM thread_plan_completed_sources source
       WHERE source.run_id=selection.run_id AND source.selection_id=selection.id
         AND source.work_item_id=work.id))
    )`)
	out = append(out, "DROP TRIGGER trg_delivery_run_completion_guard;", completion)
	return append(out, threadPlanEventInsertGuard, threadPlanDirectionSourceGuard, threadPlanModuleSourceGuard)
}

func threadPlanExistingTrigger(statements []string, name string) string {
	for _, statement := range statements {
		if strings.HasPrefix(statement, "CREATE TRIGGER "+name+"\n") {
			return statement
		}
	}
	panic("missing Plan continuation base trigger: " + name)
}

func threadPlanReplaceOnce(value, old, replacement string) string {
	if strings.Count(value, old) != 1 {
		panic("Plan continuation base trigger changed")
	}
	return strings.Replace(value, old, replacement, 1)
}

const threadPlanProposalContinuationGuard = `
 SELECT 1 FROM run_events event
 JOIN runs run ON run.id=event.run_id
 JOIN sessions session ON session.id=run.session_id AND session.status='active'
 JOIN missions mission ON mission.id=run.mission_id
 JOIN agent_nodes root ON root.id=NEW.root_agent_id AND root.run_id=run.id
   AND root.session_id=run.session_id AND root.role='root' AND root.parent_id IS NULL
   AND root.status='ready' AND root.active_attempt_id=''
 JOIN run_mode_snapshots mode ON mode.run_id=run.id AND mode.revision=NEW.mode_revision
 JOIN plan_delivery_proposals original ON original.id=json_extract(event.payload_json,'$.source_proposal_id')
 WHERE event.type='thread.plan_continued' AND event.source='thread_plan_continuation'
   AND event.subject_id=NEW.id AND event.run_id=NEW.run_id AND run.status='created'
   AND NEW.session_id=run.session_id AND NEW.workspace_id=mission.workspace_id
   AND NEW.created_at=event.created_at AND NEW.requested_by=original.requested_by
   AND mode.surface='code' AND mode.phase IN ('plan','deliver')
   AND NOT EXISTS (SELECT 1 FROM run_mode_snapshots later WHERE later.run_id=run.id AND later.revision>mode.revision)`

const threadPlanSelectionContinuationGuard = `
 SELECT 1 FROM run_events event
 JOIN runs run ON run.id=event.run_id
 JOIN plan_delivery_proposals proposal ON proposal.id=event.subject_id AND proposal.run_id=run.id
 JOIN plan_delivery_selections original ON original.id=json_extract(event.payload_json,'$.source_selection_id')
 JOIN agent_nodes root ON root.id=NEW.root_agent_id AND root.run_id=run.id
   AND root.session_id=run.session_id AND root.role='root' AND root.parent_id IS NULL
   AND root.status='ready' AND root.active_attempt_id=''
 JOIN notes note ON note.id=NEW.note_id AND note.run_id=run.id AND note.owner_agent_id=root.id
   AND note.status='active' AND note.category='decision' AND note.visibility='run'
 WHERE event.type='thread.plan_continued' AND event.source='thread_plan_continuation'
   AND event.run_id=NEW.run_id AND run.status='created' AND NEW.proposal_id=proposal.id
   AND NEW.id=json_extract(event.payload_json,'$.selection_id') AND NEW.created_at=event.created_at
   AND NEW.direction_ordinal=original.direction_ordinal AND NEW.module_count=original.module_count
   AND NEW.requested_by=original.requested_by
   AND root.id=proposal.root_agent_id AND note.pinned=1 AND note.owner=''
   AND note.version=1 AND note.created_at=NEW.created_at
   AND (SELECT COUNT(*) FROM note_tags WHERE note_id=note.id)=2
   AND (SELECT COUNT(*) FROM note_tags WHERE note_id=note.id AND tag IN ('plan-delivery','selected-direction'))=2
   AND (SELECT COUNT(*) FROM note_sources WHERE note_id=note.id)=1
   AND EXISTS (SELECT 1 FROM note_sources WHERE note_id=note.id AND source_ref='plan_delivery:'||proposal.id)
   AND NOT EXISTS (SELECT 1 FROM note_evidence WHERE note_id=note.id)`

const threadPlanEventInsertGuard = `CREATE TRIGGER trg_thread_plan_continuation_event_insert
 BEFORE INSERT ON run_events WHEN NEW.type='thread.plan_continued' AND (
 NEW.source!='thread_plan_continuation' OR NOT EXISTS (
   SELECT 1 FROM thread_runs current
   JOIN thread_runs previous ON previous.thread_id=current.thread_id AND previous.ordinal+1=current.ordinal
     AND current.predecessor_run_id=previous.run_id
   JOIN runs run ON run.id=current.run_id AND run.status='created'
   JOIN runs prior ON prior.id=previous.run_id AND prior.status IN ('completed','failed','cancelled')
     AND prior.mission_id=run.mission_id
   JOIN plan_delivery_proposals proposal ON proposal.id=json_extract(NEW.payload_json,'$.source_proposal_id')
     AND proposal.run_id=prior.id AND proposal.session_id=prior.session_id
   WHERE current.run_id=NEW.run_id AND run.mission_id=NEW.mission_id
     AND previous.run_id=json_extract(NEW.payload_json,'$.predecessor_run_id')
     AND json_extract(NEW.payload_json,'$.source_fingerprint')=proposal.proposal_fingerprint
     AND json_type(NEW.payload_json,'$.items')='array'
     AND json_array_length(NEW.payload_json,'$.items')=(SELECT COUNT(DISTINCT json_extract(value,'$.work_item_id')) FROM json_each(NEW.payload_json,'$.items'))
     AND json_array_length(NEW.payload_json,'$.items')=(SELECT COUNT(DISTINCT json_extract(value,'$.source_work_item_id')) FROM json_each(NEW.payload_json,'$.items'))
     AND NOT EXISTS (SELECT 1 FROM run_events existing WHERE existing.run_id=run.id
       AND existing.type='thread.plan_continued')
     AND NOT EXISTS (SELECT 1 FROM run_execution_leases lease WHERE lease.run_id=run.id AND lease.status='active')
     AND (json_extract(NEW.payload_json,'$.source_selection_id')='' OR EXISTS (
       SELECT 1 FROM plan_delivery_selections selection
       WHERE selection.id=json_extract(NEW.payload_json,'$.source_selection_id')
         AND selection.run_id=prior.id AND selection.proposal_id=proposal.id
         AND selection.module_count=json_array_length(NEW.payload_json,'$.items')))
 ) OR EXISTS (
   SELECT 1 FROM json_each(NEW.payload_json,'$.items') entry
   WHERE NOT EXISTS (
     SELECT 1 FROM work_items work
     JOIN plan_delivery_selection_items item ON item.work_item_id=work.id
       AND item.selection_id=json_extract(NEW.payload_json,'$.source_selection_id')
     WHERE work.id=json_extract(entry.value,'$.source_work_item_id')
       AND work.run_id=json_extract(NEW.payload_json,'$.predecessor_run_id')
       AND work.version=json_extract(entry.value,'$.source_version')
       AND item.module_ordinal=json_extract(entry.value,'$.module_ordinal')
       AND (work.status!='completed' AND json_extract(entry.value,'$.checkpoint_id')='' OR
         work.status='completed' AND (EXISTS (
           SELECT 1 FROM delivery_checkpoints checkpoint
           JOIN delivery_checkpoint_operations operation ON operation.checkpoint_id=checkpoint.id
           JOIN run_mode_snapshots mode ON mode.id=checkpoint.mode_snapshot_id
             AND mode.run_id=work.run_id AND mode.phase='deliver' AND mode.revision=checkpoint.mode_revision
           WHERE checkpoint.id=json_extract(entry.value,'$.checkpoint_id')
             AND checkpoint.run_id=work.run_id AND checkpoint.work_item_id=work.id
             AND checkpoint.selection_id=item.selection_id AND checkpoint.work_item_version=work.version-1
         ) OR EXISTS (SELECT 1 FROM thread_plan_completed_sources source
           WHERE source.run_id=work.run_id AND source.work_item_id=work.id
             AND source.checkpoint_id=json_extract(entry.value,'$.checkpoint_id'))))
   )
 )) BEGIN SELECT RAISE(ABORT,'Thread Plan continuation source is invalid'); END;`

const threadPlanDirectionSourceGuard = `CREATE TRIGGER trg_thread_plan_direction_source_insert
 BEFORE INSERT ON plan_delivery_directions
 WHEN EXISTS (SELECT 1 FROM run_events event WHERE event.subject_id=NEW.proposal_id
   AND event.type='thread.plan_continued' AND event.source='thread_plan_continuation')
 AND NOT EXISTS (
   SELECT 1 FROM run_events event JOIN plan_delivery_directions original
     ON original.proposal_id=json_extract(event.payload_json,'$.source_proposal_id')
   WHERE event.subject_id=NEW.proposal_id AND event.type='thread.plan_continued'
     AND event.source='thread_plan_continuation' AND original.ordinal=NEW.ordinal
     AND original.title=NEW.title AND original.summary=NEW.summary
     AND original.tradeoffs_json=NEW.tradeoffs_json AND original.module_count=NEW.module_count
 ) BEGIN SELECT RAISE(ABORT,'Thread Plan direction differs from its source'); END;`

const threadPlanModuleSourceGuard = `CREATE TRIGGER trg_thread_plan_module_source_insert
 BEFORE INSERT ON plan_delivery_modules
 WHEN EXISTS (SELECT 1 FROM run_events event WHERE event.subject_id=NEW.proposal_id
   AND event.type='thread.plan_continued' AND event.source='thread_plan_continuation')
 AND NOT EXISTS (
   SELECT 1 FROM run_events event JOIN plan_delivery_modules original
     ON original.proposal_id=json_extract(event.payload_json,'$.source_proposal_id')
   WHERE event.subject_id=NEW.proposal_id AND event.type='thread.plan_continued'
     AND event.source='thread_plan_continuation' AND original.direction_ordinal=NEW.direction_ordinal
     AND original.ordinal=NEW.ordinal AND original.title=NEW.title AND original.objective=NEW.objective
     AND original.acceptance_json=NEW.acceptance_json AND original.dependencies_json=NEW.dependencies_json
 ) BEGIN SELECT RAISE(ABORT,'Thread Plan module differs from its source'); END;`
