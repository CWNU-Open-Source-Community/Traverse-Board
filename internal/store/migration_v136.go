package store

import "strings"

func riskEscalationSupervisorToolCallCreate(tableName string) string {
	statement := standardCodeSupervisorToolCallCreate(tableName, true)
	legacyAuthority := "CHECK((tool_name IN ('workspace_list'"
	riskAuthority := "CHECK((tool_name IN ('host_command_propose', 'workspace_list'"
	if strings.Count(statement, legacyAuthority) != 1 {
		panic("current Supervisor host-command authority constraint is unavailable")
	}
	return strings.Replace(statement, legacyAuthority, riskAuthority, 1)
}

func rebuildRiskEscalationSupervisorToolCalls(createCalls string, tableName string,
	backupName string,
) []string {
	return []string{
		`DROP TRIGGER trg_standard_code_supervisor_ledger_insert;`,
		`DROP TRIGGER trg_supervisor_tool_call_model_attempt;`,
		`DROP TRIGGER trg_supervisor_tool_round_completion;`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_immutable;`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_insert;`,
		`DROP INDEX idx_run_supervisor_tool_calls_pending;`,
		`DROP INDEX idx_supervisor_tool_stream_call_identity;`,
		`DROP INDEX idx_supervisor_tool_stream_item_identity;`,
		`ALTER TABLE run_supervisor_tool_calls RENAME TO ` + backupName + `;`,
		createCalls,
		`INSERT INTO ` + tableName + ` SELECT * FROM ` + backupName + `;`,
		`DROP TABLE ` + backupName + `;`,
		`ALTER TABLE ` + tableName + ` RENAME TO run_supervisor_tool_calls;`,
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
		requireMigrationTrigger("trg_standard_code_supervisor_ledger_insert",
			standardCodeSupervisorStatements),
	}
}

// riskEscalationStatements keeps Approval as the sole decision ledger. The
// new tables persist only the exact proposal, grant consumption, write-ahead
// execution boundary, terminal metadata receipt, and invalidation facts.
var riskEscalationStatements = func() []string {
	rebuild := rebuildRiskEscalationSupervisorToolCalls(
		riskEscalationSupervisorToolCallCreate("run_supervisor_tool_calls_v136"),
		"run_supervisor_tool_calls_v136", "run_supervisor_tool_calls_v135")
	ledger := []string{
		`CREATE TRIGGER trg_risk_escalation_supervisor_authority_insert
		BEFORE INSERT ON run_supervisor_tool_calls
		WHEN NEW.tool_name = 'host_command_propose' AND (
			(COALESCE(json_extract(NEW.payload_json, '$.version'), '') = 'risk_escalation.v1'
				AND NEW.authority_json = '')
			OR (COALESCE(json_extract(NEW.payload_json, '$.version'), '') <> 'risk_escalation.v1'
				AND NEW.authority_json <> ''))
		BEGIN
			SELECT RAISE(ABORT, 'risk escalation Supervisor authority does not match its protocol');
		END;`,
		`CREATE TRIGGER trg_host_command_supervisor_envelope_immutable
		BEFORE UPDATE OF payload_json, authority_json ON run_supervisor_tool_calls
		WHEN NEW.tool_name = 'host_command_propose' AND
			(NEW.payload_json <> OLD.payload_json OR NEW.authority_json <> OLD.authority_json)
		BEGIN
			SELECT RAISE(ABORT, 'host command Supervisor envelope is immutable');
		END;`,
		`ALTER TABLE approval_session_grants ADD COLUMN scope_fingerprint TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE approval_session_grants ADD COLUMN grant_generation INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE approval_session_grants ADD COLUMN max_uses INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE approval_session_grants ADD COLUMN uses_remaining INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE approval_session_grants ADD COLUMN expires_at TEXT;`,
		`ALTER TABLE approval_session_grants ADD COLUMN mode_snapshot_id TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE approval_session_grants ADD COLUMN mode_revision INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE approval_session_grants ADD COLUMN interaction_snapshot_id TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE approval_session_grants ADD COLUMN interaction_revision INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE approval_session_grants ADD COLUMN execution_profile_snapshot_id TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE approval_session_grants ADD COLUMN execution_profile_revision INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE approval_session_grants ADD COLUMN permission_snapshot_id TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE approval_session_grants ADD COLUMN permission_revision INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE approval_session_grants ADD COLUMN permission_mode TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE approval_session_grants ADD COLUMN workspace_root_fingerprint TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE approval_session_grants ADD COLUMN capability_generation TEXT NOT NULL DEFAULT '';`,
		`DROP INDEX idx_approval_session_grants_active_scope;`,
		`CREATE UNIQUE INDEX idx_approval_session_grants_active_scope
		ON approval_session_grants(run_id, session_id, workspace_id, tool_name,
			action_class, scope_fingerprint) WHERE status = 'active';`,
		`CREATE INDEX idx_approval_session_grants_expiry
		ON approval_session_grants(status, expires_at) WHERE expires_at IS NOT NULL;`,
		`CREATE TABLE approval_grant_consumptions (
		id TEXT PRIMARY KEY,
		grant_id TEXT NOT NULL,
		proposal_id TEXT NOT NULL UNIQUE,
		approval_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		scope_fingerprint TEXT NOT NULL,
		grant_generation INTEGER NOT NULL,
		use_ordinal INTEGER NOT NULL,
		consumption_fingerprint TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY(grant_id) REFERENCES approval_session_grants(id) ON DELETE RESTRICT,
		FOREIGN KEY(approval_id) REFERENCES tool_approvals(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(grant_generation > 0 AND use_ordinal > 0),
		CHECK(length(scope_fingerprint) = 64 AND scope_fingerprint = lower(scope_fingerprint)
			AND scope_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(consumption_fingerprint) = 64
			AND consumption_fingerprint = lower(consumption_fingerprint)
			AND consumption_fingerprint NOT GLOB '*[^0-9a-f]*')
	) WITHOUT ROWID;`,
		`CREATE INDEX idx_approval_grant_consumptions_grant_created
		ON approval_grant_consumptions(grant_id, created_at);`,
		`CREATE TABLE risk_escalation_proposals (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		root_agent_id TEXT NOT NULL,
		supervisor_turn INTEGER NOT NULL,
		supervisor_tool_call_id TEXT NOT NULL,
		tool_invocation_id TEXT NOT NULL UNIQUE,
		mode_snapshot_id TEXT NOT NULL,
		mode_revision INTEGER NOT NULL,
		interaction_snapshot_id TEXT NOT NULL,
		interaction_revision INTEGER NOT NULL,
		execution_profile_snapshot_id TEXT NOT NULL,
		execution_profile_revision INTEGER NOT NULL,
		permission_snapshot_id TEXT NOT NULL,
		permission_revision INTEGER NOT NULL,
		permission_mode TEXT NOT NULL,
		workspace_root_fingerprint TEXT NOT NULL,
		capability_generation TEXT NOT NULL,
		spec_fingerprint TEXT NOT NULL,
		scope_fingerprint TEXT NOT NULL,
		proposal_fingerprint TEXT NOT NULL UNIQUE,
		payload_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(root_agent_id) REFERENCES agent_nodes(id) ON DELETE RESTRICT,
		FOREIGN KEY(tool_invocation_id) REFERENCES run_tool_calls(id) ON DELETE RESTRICT,
		FOREIGN KEY(mode_snapshot_id) REFERENCES run_mode_snapshots(id) ON DELETE RESTRICT,
		FOREIGN KEY(interaction_snapshot_id)
			REFERENCES run_execution_interaction_snapshots(id) ON DELETE RESTRICT,
		FOREIGN KEY(execution_profile_snapshot_id)
			REFERENCES run_execution_profile_snapshots(id) ON DELETE RESTRICT,
		FOREIGN KEY(permission_snapshot_id)
			REFERENCES run_execution_permission_snapshots(id) ON DELETE RESTRICT,
		UNIQUE(run_id, supervisor_turn, supervisor_tool_call_id),
		CHECK(supervisor_turn > 0 AND mode_revision > 0 AND interaction_revision > 0
			AND execution_profile_revision > 0 AND permission_revision > 0),
		CHECK(permission_mode = 'workspace_access'),
		CHECK(length(workspace_root_fingerprint) = 64
			AND workspace_root_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(capability_generation) = 64
			AND capability_generation NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(spec_fingerprint) = 64 AND spec_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(scope_fingerprint) = 64 AND scope_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(proposal_fingerprint) = 64
			AND proposal_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json))
	);`,
		`CREATE INDEX idx_risk_escalation_proposals_run_created
		ON risk_escalation_proposals(run_id, created_at DESC, id DESC);`,
		`CREATE TABLE risk_escalation_operations (
		operation_key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		proposal_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		root_agent_id TEXT NOT NULL,
		supervisor_turn INTEGER NOT NULL,
		supervisor_tool_call_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(proposal_id) REFERENCES risk_escalation_proposals(id) ON DELETE RESTRICT,
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(supervisor_turn > 0 AND requested_by = 'run_supervisor')
	) WITHOUT ROWID;`,
		`CREATE TABLE risk_escalation_execution_intents (
		request_id TEXT PRIMARY KEY,
		proposal_id TEXT NOT NULL UNIQUE,
		approval_id TEXT NOT NULL UNIQUE,
		grant_id TEXT,
		grant_consumption_id TEXT,
		authorization_fingerprint TEXT NOT NULL,
		intent_fingerprint TEXT NOT NULL UNIQUE,
		payload_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(proposal_id) REFERENCES risk_escalation_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(approval_id) REFERENCES tool_approvals(id) ON DELETE RESTRICT,
		FOREIGN KEY(grant_id) REFERENCES approval_session_grants(id) ON DELETE RESTRICT,
		FOREIGN KEY(grant_consumption_id) REFERENCES approval_grant_consumptions(id) ON DELETE RESTRICT,
		CHECK((grant_id IS NULL AND grant_consumption_id IS NULL)
			OR (grant_id IS NOT NULL AND grant_consumption_id IS NOT NULL)),
		CHECK(length(authorization_fingerprint) = 64
			AND authorization_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(intent_fingerprint) = 64 AND intent_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(payload_json))
	);`,
		`CREATE TABLE risk_escalation_results (
		id TEXT PRIMARY KEY,
		proposal_id TEXT NOT NULL UNIQUE,
		approval_id TEXT NOT NULL UNIQUE,
		request_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		session_message_id INTEGER NOT NULL UNIQUE,
		status TEXT NOT NULL,
		error_code TEXT NOT NULL,
		result_fingerprint TEXT NOT NULL UNIQUE,
		receipt_fingerprint TEXT NOT NULL UNIQUE,
		result_json TEXT NOT NULL,
		receipt_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(proposal_id) REFERENCES risk_escalation_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(approval_id) REFERENCES tool_approvals(id) ON DELETE RESTRICT,
		FOREIGN KEY(request_id) REFERENCES risk_escalation_execution_intents(request_id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_message_id) REFERENCES session_messages(id) ON DELETE RESTRICT,
		CHECK(status IN ('completed', 'failed')),
		CHECK((status = 'completed' AND error_code = '') OR
			(status = 'failed' AND length(error_code) > 0)),
		CHECK(length(result_fingerprint) = 64
			AND result_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(receipt_fingerprint) = 64
			AND receipt_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(result_json) AND json_valid(receipt_json))
	);`,
		`CREATE TABLE risk_escalation_invalidations (
		id TEXT PRIMARY KEY,
		proposal_id TEXT NOT NULL UNIQUE,
		grant_id TEXT,
		reason_code TEXT NOT NULL,
		detail TEXT NOT NULL,
		invalidation_fingerprint TEXT NOT NULL UNIQUE,
		event_sequence INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(proposal_id) REFERENCES risk_escalation_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(grant_id) REFERENCES approval_session_grants(id) ON DELETE RESTRICT,
		CHECK(reason_code IN ('expired', 'revoked', 'permission_drift', 'profile_drift',
			'mode_drift', 'workspace_drift', 'root_drift', 'capability_drift', 'uses_exhausted',
			'execution_uncertain')),
		CHECK(length(invalidation_fingerprint) = 64
			AND invalidation_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(event_sequence > 0)
	) WITHOUT ROWID;`,
		`CREATE TRIGGER trg_risk_escalation_proposal_update_immutable
		BEFORE UPDATE ON risk_escalation_proposals BEGIN
			SELECT RAISE(ABORT, 'risk escalation proposal cannot be updated');
		END;`,
		`CREATE TRIGGER trg_risk_escalation_proposal_delete_immutable
		BEFORE DELETE ON risk_escalation_proposals BEGIN
			SELECT RAISE(ABORT, 'risk escalation proposal cannot be deleted');
		END;`,
		`CREATE TRIGGER trg_risk_escalation_operation_update_immutable
		BEFORE UPDATE ON risk_escalation_operations BEGIN
			SELECT RAISE(ABORT, 'risk escalation operation cannot be updated');
		END;`,
		`CREATE TRIGGER trg_risk_escalation_operation_delete_immutable
		BEFORE DELETE ON risk_escalation_operations BEGIN
			SELECT RAISE(ABORT, 'risk escalation operation cannot be deleted');
		END;`,
		`CREATE TRIGGER trg_approval_grant_consumption_update_immutable
		BEFORE UPDATE ON approval_grant_consumptions BEGIN
			SELECT RAISE(ABORT, 'approval grant consumption cannot be updated');
		END;`,
		`CREATE TRIGGER trg_approval_grant_consumption_delete_immutable
		BEFORE DELETE ON approval_grant_consumptions BEGIN
			SELECT RAISE(ABORT, 'approval grant consumption cannot be deleted');
		END;`,
		`CREATE TRIGGER trg_risk_escalation_intent_update_immutable
		BEFORE UPDATE ON risk_escalation_execution_intents BEGIN
			SELECT RAISE(ABORT, 'risk escalation execution intent cannot be updated');
		END;`,
		`CREATE TRIGGER trg_risk_escalation_intent_delete_immutable
		BEFORE DELETE ON risk_escalation_execution_intents BEGIN
			SELECT RAISE(ABORT, 'risk escalation execution intent cannot be deleted');
		END;`,
		`CREATE TRIGGER trg_risk_escalation_result_update_immutable
		BEFORE UPDATE ON risk_escalation_results BEGIN
			SELECT RAISE(ABORT, 'risk escalation result cannot be updated');
		END;`,
		`CREATE TRIGGER trg_risk_escalation_result_delete_immutable
		BEFORE DELETE ON risk_escalation_results BEGIN
			SELECT RAISE(ABORT, 'risk escalation result cannot be deleted');
		END;`,
		`CREATE TRIGGER trg_risk_escalation_invalidation_update_immutable
		BEFORE UPDATE ON risk_escalation_invalidations BEGIN
			SELECT RAISE(ABORT, 'risk escalation invalidation cannot be updated');
		END;`,
		`CREATE TRIGGER trg_risk_escalation_invalidation_delete_immutable
		BEFORE DELETE ON risk_escalation_invalidations BEGIN
			SELECT RAISE(ABORT, 'risk escalation invalidation cannot be deleted');
		END;`,
	}
	return append(rebuild, ledger...)
}()
