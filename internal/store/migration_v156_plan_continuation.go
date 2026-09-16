package store

// Both views read existing immutable events. The completion event is normalized
// to its original Run on each handoff; no checkpoint or replacement event is
// manufactured for a completed on-demand item.
var threadPlanOnDemandContinuationStatements = buildThreadPlanOnDemandContinuationStatements()

const planOnDemandCompletionEventsSQL = `CREATE VIEW plan_on_demand_completion_events AS
 SELECT work.run_id, selection.id AS selection_id, work.id AS work_item_id,
   event.event_id AS completion_event_id, work.completed_at AS origin_completed_at,
   selection.proposal_id, selection.direction_ordinal, selected.module_ordinal
 FROM work_items work
 JOIN runs run ON run.id=work.run_id
 JOIN plan_delivery_selection_items selected ON selected.work_item_id=work.id
 JOIN plan_delivery_selections selection ON selection.id=selected.selection_id
   AND selection.run_id=work.run_id AND selection.manual_acceptance='on_demand'
 JOIN run_events event ON event.run_id=work.run_id AND event.mission_id=run.mission_id
   AND event.subject_id=work.id AND event.type='work_item.changed'
   AND event.source IN ('work_item_service','plan_delivery_control')
   AND json_type(event.payload_json,'$.version')='integer'
   AND json_extract(event.payload_json,'$.version')=work.version
   AND json_extract(event.payload_json,'$.from') IN ('pending','in_progress')
   AND json_extract(event.payload_json,'$.to')='completed'
 JOIN run_mode_snapshots mode ON mode.run_id=work.run_id AND mode.phase='deliver'
   AND mode.created_at<=event.created_at
   AND NOT EXISTS (SELECT 1 FROM run_mode_snapshots later WHERE later.run_id=work.run_id
     AND later.revision>mode.revision AND later.created_at<=event.created_at)
 JOIN plan_delivery_modules module ON module.proposal_id=selection.proposal_id
   AND module.direction_ordinal=selection.direction_ordinal AND module.ordinal=selected.module_ordinal
 WHERE work.status='completed' AND work.version>1 AND work.completed_at IS NOT NULL
   AND work.title=module.title AND work.description=module.objective
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
         WHERE dependency.selection_id=selection.id AND dependency.work_item_id=edge.depends_on_id));`

func buildThreadPlanOnDemandContinuationStatements() []string {
	// Preserve all old columns for required/manual completion readers. Old event
	// payloads have no completion_event_id and remain a valid required source.
	sources := threadPlanReplaceOnce(threadPlanSourceViewSQL, "   event.created_at", "   COALESCE(json_extract(item.value,'$.completion_event_id'),'') AS completion_event_id,\n   event.created_at")
	manual := threadPlanReplaceOnce(threadPlanCompletedViewSQL, " WHERE module.title", " WHERE selection.manual_acceptance='required' AND prior_selection.manual_acceptance='required'\n   AND module.title")
	onDemand := threadPlanReplaceOnce(threadPlanCompletedViewSQL, "CREATE VIEW thread_plan_completed_sources AS", "CREATE VIEW thread_plan_on_demand_completed_sources AS")
	onDemand = threadPlanReplaceOnce(onDemand, ` SELECT source.*, checkpoint.run_id AS origin_run_id,
   checkpoint.work_item_id AS origin_work_item_id, checkpoint.handoff_note_id,
   origin.completed_at AS origin_completed_at`, ` SELECT source.*, completion.run_id AS origin_run_id,
   completion.work_item_id AS origin_work_item_id, completion.origin_completed_at`)
	onDemand = threadPlanReplaceOnce(onDemand, ` JOIN delivery_checkpoints checkpoint ON checkpoint.id=source.checkpoint_id
 JOIN delivery_checkpoint_operations operation ON operation.checkpoint_id=checkpoint.id
   AND operation.run_id=checkpoint.run_id AND operation.work_item_id=checkpoint.work_item_id
 JOIN work_items origin ON origin.id=checkpoint.work_item_id AND origin.run_id=checkpoint.run_id
   AND origin.status='completed' AND origin.version=checkpoint.work_item_version+1
 JOIN run_mode_snapshots mode ON mode.id=checkpoint.mode_snapshot_id
   AND mode.run_id=checkpoint.run_id AND mode.revision=checkpoint.mode_revision AND mode.phase='deliver'
 JOIN thread_runs origin_binding ON origin_binding.run_id=checkpoint.run_id`, ` JOIN plan_on_demand_completion_events completion ON completion.completion_event_id=source.completion_event_id
 JOIN work_items origin ON origin.id=completion.work_item_id AND origin.run_id=completion.run_id
 JOIN thread_runs origin_binding ON origin_binding.run_id=completion.run_id`)
	onDemand = threadPlanReplaceOnce(onDemand, ` JOIN plan_delivery_modules original_module ON original_module.proposal_id=checkpoint.proposal_id
   AND original_module.direction_ordinal=checkpoint.direction_ordinal
   AND original_module.ordinal=checkpoint.module_ordinal`, ` JOIN plan_delivery_modules original_module ON original_module.proposal_id=completion.proposal_id
   AND original_module.direction_ordinal=completion.direction_ordinal
   AND original_module.ordinal=completion.module_ordinal`)
	onDemand = threadPlanReplaceOnce(onDemand, " WHERE module.title", " WHERE selection.manual_acceptance='on_demand' AND prior_selection.manual_acceptance='on_demand'\n   AND source.checkpoint_id='' AND module.title")
	onDemand = threadPlanReplaceOnce(onDemand, "checkpoint.work_item_id=prior_work.id", "completion.work_item_id=prior_work.id")
	onDemand = threadPlanReplaceOnce(onDemand, "previous_source.checkpoint_id=checkpoint.id", "previous_source.completion_event_id=completion.completion_event_id AND previous_source.checkpoint_id=''")
	out := []string{"DROP VIEW thread_plan_completed_sources;", "DROP VIEW thread_plan_continuation_sources;", sources, manual, planOnDemandCompletionEventsSQL, onDemand}

	selection := threadPlanExistingTrigger(threadPlanContinuationStatements, "trg_plan_delivery_selection_insert")
	selection = threadPlanReplaceOnce(selection, "   AND NEW.requested_by=original.requested_by", "   AND NEW.requested_by=original.requested_by AND NEW.manual_acceptance=original.manual_acceptance")
	out = append(out, "DROP TRIGGER trg_plan_delivery_selection_insert;", selection)
	item := threadPlanExistingTrigger(threadPlanContinuationStatements, "trg_plan_delivery_selection_item_insert")
	item = threadPlanReplaceOnce(item, `   JOIN delivery_checkpoints checkpoint ON checkpoint.id=source.checkpoint_id
   WHERE source.selection_id=selection.id AND source.work_item_id=item.id`, `   WHERE source.selection_id=selection.id AND source.work_item_id=item.id AND (
     selection.manual_acceptance='required' AND EXISTS (SELECT 1 FROM delivery_checkpoints checkpoint WHERE checkpoint.id=source.checkpoint_id)
     OR selection.manual_acceptance='on_demand' AND source.checkpoint_id='' AND EXISTS (
       SELECT 1 FROM plan_on_demand_completion_events completion WHERE completion.completion_event_id=source.completion_event_id))`)
	out = append(out, "DROP TRIGGER trg_plan_delivery_selection_item_insert;", item)
	guard := threadPlanReplaceOnce(threadPlanEventInsertGuard, "     WHERE work.id=json_extract(entry.value,'$.source_work_item_id')", "     JOIN plan_delivery_selections selection ON selection.id=item.selection_id\n     WHERE work.id=json_extract(entry.value,'$.source_work_item_id')")
	guard = threadPlanReplaceOnce(guard, "work.status!='completed' AND json_extract(entry.value,'$.checkpoint_id')=''", "work.status!='completed' AND json_extract(entry.value,'$.checkpoint_id')='' AND COALESCE(json_extract(entry.value,'$.completion_event_id'),'')=''")
	guard = threadPlanReplaceOnce(guard, "work.status='completed' AND (EXISTS (", "work.status='completed' AND selection.manual_acceptance='required' AND COALESCE(json_extract(entry.value,'$.completion_event_id'),'')='' AND (EXISTS (")
	guard = threadPlanReplaceOnce(guard, `             AND source.checkpoint_id=json_extract(entry.value,'$.checkpoint_id'))))`, `             AND source.checkpoint_id=json_extract(entry.value,'$.checkpoint_id')))
         OR work.status='completed' AND selection.manual_acceptance='on_demand'
           AND json_extract(entry.value,'$.checkpoint_id')='' AND (
             EXISTS (SELECT 1 FROM plan_on_demand_completion_events completion
               WHERE completion.run_id=work.run_id AND completion.selection_id=selection.id
                 AND completion.work_item_id=work.id
                 AND completion.completion_event_id=json_extract(entry.value,'$.completion_event_id'))
             OR EXISTS (SELECT 1 FROM thread_plan_on_demand_completed_sources source
               WHERE source.run_id=work.run_id AND source.selection_id=selection.id
                 AND source.work_item_id=work.id
                 AND source.completion_event_id=json_extract(entry.value,'$.completion_event_id'))))`)
	return append(out, "DROP TRIGGER trg_thread_plan_continuation_event_insert;", guard)
}
