package store

import "strings"

func sourceSearchSupervisorToolCallCreate(tableName string) string {
	statement := historyRecallSupervisorToolCallCreate(tableName)
	const before = "'web_search', 'web_fetch', 'web_citation'"
	const after = "'web_search', 'source_search', 'web_fetch', 'web_citation'"
	if strings.Count(statement, before) != 3 {
		panic("Supervisor source-search authority constraints are unavailable")
	}
	return strings.ReplaceAll(statement, before, after)
}

// sourceSearchEvidenceStatements admits the public source discovery tool to
// both durable ledgers without weakening their authority or immutability
// constraints. Historical rows retain their rowids so issued history cursors
// remain valid across the upgrade.
var sourceSearchEvidenceStatements = func() []string {
	const next = "run_supervisor_tool_calls_v165"
	const previous = "run_supervisor_tool_calls_v164"
	statements := []string{
		`PRAGMA legacy_alter_table=ON;`,
		`DROP TRIGGER trg_risk_escalation_supervisor_authority_insert;`,
		`DROP TRIGGER trg_host_command_supervisor_envelope_immutable;`,
	}
	rebuild := rebuildRiskEscalationSupervisorToolCalls(
		sourceSearchSupervisorToolCallCreate(next), next, previous)
	for i, statement := range rebuild {
		if statement == `INSERT INTO `+next+` SELECT * FROM `+previous+`;` {
			const columns = `run_id,turn,attempt_id,round,position,model_attempt,call_id,tool_name,payload_json,authority_json,status,result_json,error_code,created_at,completed_at,stream_response_id,stream_item_id,stream_call_id`
			rebuild[i] = `INSERT INTO ` + next + `(rowid,` + columns + `) SELECT rowid,` + columns + ` FROM ` + previous + `;`
		}
	}
	statements = append(statements, rebuild...)
	statements = append(statements,
		requireMigrationTrigger("trg_risk_escalation_supervisor_authority_insert",
			riskEscalationStatements),
		requireMigrationTrigger("trg_host_command_supervisor_envelope_immutable",
			riskEscalationStatements),
		`DROP TRIGGER trg_web_evidence_operation_immutable;`,
		`DROP TRIGGER trg_web_evidence_operation_delete_immutable;`,
		`DROP INDEX idx_web_evidence_operations_run;`,
		`ALTER TABLE web_evidence_operations RENAME TO web_evidence_operations_v164;`,
		`CREATE TABLE web_evidence_operations_v165 (
			key_digest TEXT PRIMARY KEY,
			protocol_version TEXT NOT NULL,
			request_fingerprint TEXT NOT NULL,
			run_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			response_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
			CHECK(protocol_version = 'web_evidence_operation.v1'),
			CHECK(length(key_digest) = 64 AND length(request_fingerprint) = 64),
			CHECK(tool_name IN ('web_search', 'source_search', 'web_fetch', 'web_citation')),
			CHECK(json_valid(response_json) = 1)
		);`,
		`INSERT INTO web_evidence_operations_v165 SELECT * FROM web_evidence_operations_v164;`,
		`DROP TABLE web_evidence_operations_v164;`,
		`ALTER TABLE web_evidence_operations_v165 RENAME TO web_evidence_operations;`,
		`CREATE INDEX idx_web_evidence_operations_run
			ON web_evidence_operations(run_id, created_at DESC, key_digest);`,
		`CREATE TRIGGER trg_web_evidence_operation_immutable
			BEFORE UPDATE ON web_evidence_operations
			BEGIN SELECT RAISE(ABORT, 'web evidence operation is immutable'); END;`,
		`CREATE TRIGGER trg_web_evidence_operation_delete_immutable
			BEFORE DELETE ON web_evidence_operations
			BEGIN SELECT RAISE(ABORT, 'web evidence operation cannot be deleted'); END;`,
		`PRAGMA legacy_alter_table=OFF;`)
	return statements
}()
