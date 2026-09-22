package store

import "strings"

func agentBrowserSupervisorToolCallCreate(tableName string) string {
	statement := sourceSearchSupervisorToolCallCreate(tableName)
	const before = "'browser_screenshot')"
	const after = "'browser_screenshot', 'browser_scroll', 'browser_key')"
	if strings.Count(statement, before) != 3 {
		panic("Agent browser Supervisor authority constraints unavailable")
	}
	return strings.ReplaceAll(statement, before, after)
}

// Preserve all call bytes and rowids (history cursor identities), while adding
// exactly two authority-bound v2 actions. Child actor FKs keep the final name.
var agentBrowserSupervisorLedgerStatements = func() []string {
	const next = "run_supervisor_tool_calls_v169"
	const previous = "run_supervisor_tool_calls_v168"
	statements := []string{`PRAGMA legacy_alter_table=ON;`, `DROP TRIGGER trg_risk_escalation_supervisor_authority_insert;`, `DROP TRIGGER trg_host_command_supervisor_envelope_immutable;`}
	rebuild := rebuildRiskEscalationSupervisorToolCalls(agentBrowserSupervisorToolCallCreate(next), next, previous)
	replaced := false
	for i, statement := range rebuild {
		if statement == `INSERT INTO `+next+` SELECT * FROM `+previous+`;` {
			const columns = `run_id,turn,attempt_id,round,position,model_attempt,call_id,tool_name,payload_json,authority_json,status,result_json,error_code,created_at,completed_at,stream_response_id,stream_item_id,stream_call_id`
			rebuild[i] = `INSERT INTO ` + next + `(rowid,` + columns + `) SELECT rowid,` + columns + ` FROM ` + previous + `;`
			replaced = true
		}
	}
	if !replaced {
		panic("Agent browser Supervisor legacy copy unavailable")
	}
	statements = append(statements, rebuild...)
	return append(statements, requireMigrationTrigger("trg_risk_escalation_supervisor_authority_insert", riskEscalationStatements), requireMigrationTrigger("trg_host_command_supervisor_envelope_immutable", riskEscalationStatements), `PRAGMA legacy_alter_table=OFF;`)
}()
