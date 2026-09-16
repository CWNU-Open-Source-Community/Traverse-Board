package store

import "strings"

// A carried preset describes an already selected backend. Its current phase and
// Thread permission are independent of the original first-time Plan preset.
// Preserve all old bindings and allow only an exact adjacent Thread successor.
var threadStandardCodeContinuationStatements = func() []string {
	var statement string
	for _, candidate := range standardCodePresetStatements {
		if strings.Contains(candidate, "CREATE TRIGGER trg_standard_code_preset_operation_update") {
			statement = candidate
			break
		}
	}
	if statement == "" {
		panic("missing Standard Code preset update trigger")
	}
	continuation := `EXISTS (
		SELECT 1 FROM runs next_run
		JOIN thread_runs next_link ON next_link.run_id=next_run.id
		JOIN thread_runs previous_link ON previous_link.run_id=next_link.predecessor_run_id
			AND previous_link.thread_id=next_link.thread_id AND previous_link.ordinal+1=next_link.ordinal
		JOIN runs previous_run ON previous_run.id=previous_link.run_id
		JOIN standard_code_preset_operations previous_preset ON previous_preset.run_id=previous_run.id
		JOIN run_events continuation ON continuation.run_id=next_run.id
		JOIN thread_execution_permission_snapshots preference ON preference.thread_id=next_link.thread_id
		JOIN run_execution_permission_snapshots selected_permission ON selected_permission.id=NEW.permission_snapshot_id
		JOIN run_mode_snapshots previous_mode ON previous_mode.run_id=previous_run.id
		JOIN run_mode_snapshots next_mode ON next_mode.id=NEW.mode_snapshot_id
		WHERE next_run.id=NEW.run_id AND next_run.status='created'
		AND previous_run.status IN ('completed','failed','cancelled')
		AND previous_preset.status='configured'
		AND previous_preset.selected_backend=NEW.selected_backend
		AND previous_preset.backend_intent=NEW.backend_intent
		AND previous_preset.selection_reason=NEW.selection_reason
		AND previous_preset.workspace_id=NEW.workspace_id
		AND previous_preset.drydock_id=NEW.drydock_id
		AND NEW.requested_by='thread_continuation'
		AND continuation.sequence=NEW.event_sequence_end
		AND continuation.type='standard_code.preset_configured'
		AND continuation.source='standard_code_preset' AND continuation.subject_id=NEW.run_id
		AND json_extract(continuation.payload_json,'$.source')='thread_continuation'
		AND json_extract(continuation.payload_json,'$.predecessor_run_id')=previous_run.id
		AND json_extract(continuation.payload_json,'$.predecessor_preset_digest')=previous_preset.operation_key_digest
		AND json_extract(continuation.payload_json,'$.drydock_id')=NEW.drydock_id
		AND json_extract(continuation.payload_json,'$.permission_snapshot_id')=NEW.permission_snapshot_id
		AND json_extract(continuation.payload_json,'$.browser_cdp_snapshot_id')=NEW.browser_cdp_snapshot_id
		AND preference.mode=selected_permission.mode
		AND NOT EXISTS (SELECT 1 FROM thread_execution_permission_snapshots newer WHERE newer.thread_id=preference.thread_id AND newer.revision>preference.revision)
		AND next_mode.phase=previous_mode.phase AND next_mode.surface=previous_mode.surface
		AND NOT EXISTS (SELECT 1 FROM run_mode_snapshots newer WHERE newer.run_id=previous_mode.run_id AND newer.revision>previous_mode.revision)
	)`
	for _, binding := range []string{
		"mode.phase = 'plan'",
		"permission.mode = 'workspace_access' AND permission.network_scope = 'disabled'",
		"cdp.mode = 'restricted'",
	} {
		if strings.Count(statement, binding) != 1 {
			panic("unexpected Standard Code preset continuation binding")
		}
		statement = strings.Replace(statement, binding, "("+binding+" OR "+continuation+")", 1)
	}
	oldOwner := "drydock.run_id = NEW.run_id"
	if strings.Count(statement, oldOwner) != 1 {
		panic("unexpected Standard Code physical owner binding")
	}
	statement = strings.Replace(statement, oldOwner, "("+oldOwner+" OR ("+continuation+` AND EXISTS (
		SELECT 1 FROM run_file_drydock_bindings binding WHERE binding.run_id=NEW.run_id
		AND binding.drydock_id=drydock.id AND binding.mission_id=NEW.mission_id
		AND binding.source_workspace_id=NEW.workspace_id)))`, 1)
	oldTrust := "trust_record.run_id = NEW.run_id"
	if strings.Count(statement, oldTrust) != 1 {
		panic("unexpected Standard Code physical trust binding")
	}
	statement = strings.Replace(statement, oldTrust, "trust_record.run_id = drydock.run_id", 1)
	return []string{"DROP TRIGGER trg_standard_code_preset_operation_update;", statement}
}()
