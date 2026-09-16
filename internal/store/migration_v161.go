package store

import "strings"

func historyRecallSupervisorToolCallCreate(tableName string) string {
	statement := browserActionSupervisorToolCallCreate(tableName)
	before := "CHECK(tool_name IN ('work_item_create', 'note_create',"
	if strings.Count(statement, before) != 1 {
		panic("Supervisor history recall tool allowlist is unavailable")
	}
	// These Go-scoped read tools carry no separately granted workspace/browser
	// authority. Preserve both existing sides of that authority CHECK unchanged.
	return strings.Replace(statement, before, "CHECK(tool_name IN ('history_search', 'history_read', 'work_item_create', 'note_create',", 1)
}

var historyRecallSupervisorStatements = func() []string {
	const next = "run_supervisor_tool_calls_v161"
	const previous = "run_supervisor_tool_calls_v160"
	// v151 introduced an attribution FK to this ledger. legacy_alter_table with
	// the migration runner's FK suspension keeps that reference on the canonical
	// table name while this established rebuild preserves every old row/guard.
	statements := []string{`PRAGMA legacy_alter_table=ON;`,
		`DROP TRIGGER trg_risk_escalation_supervisor_authority_insert;`,
		`DROP TRIGGER trg_host_command_supervisor_envelope_immutable;`}
	rebuild := rebuildRiskEscalationSupervisorToolCalls(historyRecallSupervisorToolCallCreate(next), next, previous)
	for i, statement := range rebuild {
		if statement == `INSERT INTO `+next+` SELECT * FROM `+previous+`;` {
			// Preserve rowids too: an already issued read-only history cursor can
			// retain its candidate watermark across this upgrade.
			const columns = `run_id,turn,attempt_id,round,position,model_attempt,call_id,tool_name,payload_json,authority_json,status,result_json,error_code,created_at,completed_at,stream_response_id,stream_item_id,stream_call_id`
			rebuild[i] = `INSERT INTO ` + next + `(rowid,` + columns + `) SELECT rowid,` + columns + ` FROM ` + previous + `;`
		}
	}
	statements = append(statements, rebuild...)
	return append(statements,
		requireMigrationTrigger("trg_risk_escalation_supervisor_authority_insert", riskEscalationStatements),
		requireMigrationTrigger("trg_host_command_supervisor_envelope_immutable", riskEscalationStatements),
		`PRAGMA legacy_alter_table=OFF;`)
}()
