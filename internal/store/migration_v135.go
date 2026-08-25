package store

import "strings"

func standardCodeSupervisorToolCallCreate(tableName string, includeCodeIntel bool) string {
	statement := requireMigrationStatement(
		"CREATE TABLE run_supervisor_tool_calls_v134 (", webEvidenceStatements)
	statement = replaceCommandRuntimeMigrationFragment(statement,
		"CREATE TABLE run_supervisor_tool_calls_v134 (", "CREATE TABLE "+tableName+" (")
	if !includeCodeIntel {
		return statement
	}
	webEvidenceTail := "'web_citation')"
	codeIntelTail := "'web_citation',\n" +
		"\t\t\t'code_workspace_symbols', 'code_document_symbols', 'code_definition',\n" +
		"\t\t\t'code_references', 'code_implementation', 'code_hover',\n" +
		"\t\t\t'code_signature_help', 'code_diagnostics', 'code_call_hierarchy',\n" +
		"\t\t\t'code_type_hierarchy')"
	if strings.Count(statement, webEvidenceTail) != 3 {
		panic("current Supervisor tool authority constraints are unavailable")
	}
	return strings.ReplaceAll(statement, webEvidenceTail, codeIntelTail)
}

// standardCodeSupervisorStatements adds one append-only state and budget ledger
// around the existing RunSupervisor. Tool arguments and results stay in the
// existing Supervisor ledger; this table stores only bounded structural facts,
// digests, decisions, and the complete deterministic state projection.
var standardCodeSupervisorStatements = func() []string {
	createCalls := standardCodeSupervisorToolCallCreate(
		"run_supervisor_tool_calls_v135", true)
	rebuild := []string{
		`DROP TRIGGER trg_supervisor_tool_call_model_attempt;`,
		`DROP TRIGGER trg_supervisor_tool_round_completion;`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_immutable;`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_insert;`,
		`DROP INDEX idx_run_supervisor_tool_calls_pending;`,
		`DROP INDEX idx_supervisor_tool_stream_call_identity;`,
		`DROP INDEX idx_supervisor_tool_stream_item_identity;`,
		`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v134;`,
		createCalls,
		`INSERT INTO run_supervisor_tool_calls_v135 SELECT *
			FROM run_supervisor_tool_calls_v134;`,
		`DROP TABLE run_supervisor_tool_calls_v134;`,
		`ALTER TABLE run_supervisor_tool_calls_v135 RENAME TO run_supervisor_tool_calls;`,
		requireMigrationStatement("CREATE INDEX idx_run_supervisor_tool_calls_pending",
			githubReviewStatements),
		requireMigrationStatement("CREATE UNIQUE INDEX idx_supervisor_tool_stream_item_identity",
			itemStreamToolIdentityStatements),
		requireMigrationStatement("CREATE UNIQUE INDEX idx_supervisor_tool_stream_call_identity",
			itemStreamToolIdentityStatements),
		requireMigrationTrigger("trg_supervisor_tool_call_model_attempt",
			githubReviewStatements),
		requireMigrationTrigger("trg_supervisor_tool_round_completion",
			githubReviewStatements),
		requireMigrationTrigger("trg_supervisor_tool_stream_identity_insert",
			itemStreamToolIdentityStatements),
		requireMigrationTrigger("trg_supervisor_tool_stream_identity_immutable",
			itemStreamToolIdentityStatements),
	}
	ledger := []string{
		`CREATE TABLE standard_code_supervisor_ledger (
		id TEXT PRIMARY KEY,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		root_agent_id TEXT NOT NULL,
		preset_operation_key_digest TEXT NOT NULL,
		turn INTEGER NOT NULL,
		attempt_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		decision TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		tool_action TEXT NOT NULL,
		tool_kind TEXT NOT NULL,
		intent_fingerprint TEXT NOT NULL,
		evidence_fingerprint TEXT NOT NULL,
		result_status TEXT NOT NULL,
		error_code TEXT NOT NULL,
		from_state TEXT NOT NULL,
		to_state TEXT NOT NULL,
		reason_code TEXT NOT NULL,
		snapshot_version INTEGER NOT NULL,
		snapshot_json TEXT NOT NULL,
		event_sequence INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(root_agent_id) REFERENCES agent_nodes(id) ON DELETE RESTRICT,
		FOREIGN KEY(preset_operation_key_digest)
			REFERENCES standard_code_preset_operations(operation_key_digest) ON DELETE RESTRICT,
		FOREIGN KEY(run_id, event_sequence) REFERENCES run_events(run_id, sequence) ON DELETE RESTRICT,
		UNIQUE(run_id, snapshot_version),
		UNIQUE(run_id, event_sequence),
		CHECK(protocol_version = 'standard_code_supervisor.v1'),
		CHECK(length(operation_key_digest) = 64 AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(preset_operation_key_digest) = 64
			AND preset_operation_key_digest = lower(preset_operation_key_digest)
			AND preset_operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(intent_fingerprint = '' OR (length(intent_fingerprint) = 64
			AND intent_fingerprint = lower(intent_fingerprint)
			AND intent_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(evidence_fingerprint = '' OR (length(evidence_fingerprint) = 64
			AND evidence_fingerprint = lower(evidence_fingerprint)
			AND evidence_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(turn > 0 AND snapshot_version > 0),
		CHECK(kind IN ('initialized', 'turn_prepared', 'phase_advanced',
			'call_authorized', 'call_denied', 'call_replayed', 'call_observed',
			'round_observed', 'action_recorded', 'stopped')),
		CHECK(decision IN ('recorded', 'allowed', 'denied', 'replayed', 'observed')),
		CHECK(tool_kind IN ('workspace_read', 'code_intel_read', 'plan_proposal',
			'workspace_proposal', 'workspace_mutation', 'command_run', 'command_start',
			'command_list', 'command_read', 'command_wait', 'command_write_stdin',
			'command_cancel', 'command_kill', 'other')),
		CHECK(result_status IN ('', 'completed', 'denied', 'failed')),
		CHECK(from_state IN ('', 'inspect', 'plan', 'checkpoint', 'edit', 'execute',
			'observe', 'diagnose', 'deliver', 'stopped')),
		CHECK(to_state IN ('inspect', 'plan', 'checkpoint', 'edit', 'execute',
			'observe', 'diagnose', 'deliver', 'stopped')),
		CHECK(length(snapshot_json) BETWEEN 2 AND 65536 AND json_valid(snapshot_json)),
		CHECK(length(id) BETWEEN 1 AND 256 AND id = trim(id) AND instr(id, char(0)) = 0),
		CHECK(length(run_id) BETWEEN 1 AND 256 AND run_id = trim(run_id) AND instr(run_id, char(0)) = 0),
		CHECK(length(mission_id) BETWEEN 1 AND 256 AND mission_id = trim(mission_id) AND instr(mission_id, char(0)) = 0),
		CHECK(length(workspace_id) BETWEEN 1 AND 256 AND workspace_id = trim(workspace_id) AND instr(workspace_id, char(0)) = 0),
		CHECK(length(root_agent_id) BETWEEN 1 AND 256 AND root_agent_id = trim(root_agent_id) AND instr(root_agent_id, char(0)) = 0),
		CHECK(length(attempt_id) BETWEEN 1 AND 256 AND attempt_id = trim(attempt_id) AND instr(attempt_id, char(0)) = 0),
		CHECK(length(tool_call_id) <= 256 AND tool_call_id = trim(tool_call_id) AND instr(tool_call_id, char(0)) = 0),
		CHECK(length(tool_name) <= 256 AND tool_name = trim(tool_name) AND instr(tool_name, char(0)) = 0),
		CHECK(length(tool_action) <= 256 AND tool_action = trim(tool_action) AND instr(tool_action, char(0)) = 0),
		CHECK(length(error_code) <= 256 AND error_code = trim(error_code) AND instr(error_code, char(0)) = 0),
		CHECK(length(reason_code) <= 256 AND reason_code = trim(reason_code) AND instr(reason_code, char(0)) = 0),
		CHECK((kind IN ('call_authorized', 'call_denied', 'call_replayed', 'call_observed')
			AND tool_call_id <> '' AND tool_name <> '')
			OR (kind NOT IN ('call_authorized', 'call_denied', 'call_replayed', 'call_observed')
				AND tool_call_id = '' AND tool_name = '')),
		CHECK((kind = 'call_observed' AND result_status <> '')
			OR (kind <> 'call_observed' AND result_status = ''))
	) WITHOUT ROWID;`,
		`CREATE INDEX idx_standard_code_supervisor_ledger_run_event
		ON standard_code_supervisor_ledger(run_id, event_sequence DESC);`,
		`CREATE INDEX idx_standard_code_supervisor_ledger_intent
		ON standard_code_supervisor_ledger(run_id, intent_fingerprint, kind, event_sequence DESC);`,
		`CREATE INDEX idx_standard_code_supervisor_ledger_call
		ON standard_code_supervisor_ledger(run_id, tool_call_id, event_sequence);`,
		`CREATE TRIGGER trg_standard_code_supervisor_ledger_insert
		BEFORE INSERT ON standard_code_supervisor_ledger
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			JOIN agent_nodes root ON root.run_id = run.id AND root.id = NEW.root_agent_id
			JOIN run_supervisor_checkpoints checkpoint ON checkpoint.run_id = run.id
			JOIN run_execution_leases lease ON lease.run_id = run.id
			JOIN standard_code_preset_operations preset
				ON preset.operation_key_digest = NEW.preset_operation_key_digest
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND mission.workspace_id = NEW.workspace_id
				AND root.role = 'root' AND root.status = 'running'
				AND root.active_attempt_id = NEW.attempt_id
				AND checkpoint.next_turn = NEW.turn AND checkpoint.attempt_id = NEW.attempt_id
				AND checkpoint.phase = 'turn_started'
				AND checkpoint.lease_id = lease.lease_id
				AND checkpoint.lease_generation = lease.generation
				AND lease.status = 'active'
				AND preset.run_id = NEW.run_id AND preset.mission_id = NEW.mission_id
				AND preset.workspace_id = NEW.workspace_id AND preset.status = 'configured'
				AND (SELECT COUNT(*) FROM standard_code_supervisor_ledger existing
					WHERE existing.run_id = NEW.run_id) < 512
				AND EXISTS (SELECT 1 FROM run_events event
					WHERE event.run_id = NEW.run_id AND event.sequence = NEW.event_sequence
						AND event.source = 'standard_code_supervisor'
						AND event.subject_id = NEW.id
						AND event.type IN ('standard_code.supervisor_prepared',
							'standard_code.supervisor_authorized',
							'standard_code.supervisor_denied',
							'standard_code.supervisor_replayed',
							'standard_code.supervisor_observed',
							'standard_code.supervisor_stopped'))
				AND ((NEW.kind NOT IN ('call_authorized', 'call_denied', 'call_replayed', 'call_observed'))
					OR EXISTS (SELECT 1 FROM run_supervisor_tool_calls call
						WHERE call.run_id = NEW.run_id AND call.turn = NEW.turn
							AND call.attempt_id = NEW.attempt_id AND call.call_id = NEW.tool_call_id
							AND call.tool_name = NEW.tool_name
							AND ((NEW.kind = 'call_observed' AND call.status IN ('completed', 'denied', 'failed'))
								OR (NEW.kind <> 'call_observed' AND call.status = 'pending'))))
		)
		BEGIN
			SELECT RAISE(ABORT, 'Standard Code Supervisor ledger binding is invalid');
		END;`,
		`CREATE TRIGGER trg_standard_code_supervisor_ledger_update_immutable
		BEFORE UPDATE ON standard_code_supervisor_ledger BEGIN
			SELECT RAISE(ABORT, 'Standard Code Supervisor ledger cannot be updated');
		END;`,
		`CREATE TRIGGER trg_standard_code_supervisor_ledger_delete_immutable
		BEFORE DELETE ON standard_code_supervisor_ledger BEGIN
			SELECT RAISE(ABORT, 'Standard Code Supervisor ledger cannot be deleted');
		END;`,
	}
	return append(rebuild, ledger...)
}()
