package store

import "strings"

func builtinSkillReadToolCallCreate(tableName string) string {
	statement := agentBrowserSupervisorToolCallCreate(tableName)
	const before = "CHECK(tool_name IN ('history_search', 'history_read',"
	if strings.Count(statement, before) != 1 {
		panic("embedded Skill tool allowlist unavailable")
	}
	return strings.Replace(statement, before, "CHECK(tool_name IN ('skill_read', 'history_search', 'history_read',", 1)
}

// Preserve all call bytes and rowids (history cursor identities), while adding
// one root-scoped read action. Child actor FKs keep the final name.
var builtinSkillReadStatements = func() []string {
	const next = "run_supervisor_tool_calls_v173"
	const previous = "run_supervisor_tool_calls_v172"
	statements := []string{`PRAGMA legacy_alter_table=ON;`, `DROP TRIGGER trg_risk_escalation_supervisor_authority_insert;`, `DROP TRIGGER trg_host_command_supervisor_envelope_immutable;`}
	rebuild := rebuildRiskEscalationSupervisorToolCalls(builtinSkillReadToolCallCreate(next), next, previous)
	replaced := false
	for i, statement := range rebuild {
		if statement == `INSERT INTO `+next+` SELECT * FROM `+previous+`;` {
			const columns = `run_id,turn,attempt_id,round,position,model_attempt,call_id,tool_name,payload_json,authority_json,status,result_json,error_code,created_at,completed_at,stream_response_id,stream_item_id,stream_call_id`
			rebuild[i] = `INSERT INTO ` + next + `(rowid,` + columns + `) SELECT rowid,` + columns + ` FROM ` + previous + `;`
			replaced = true
		}
	}
	if !replaced {
		panic("Embedded skill Supervisor legacy copy unavailable")
	}
	statements = append(statements, rebuild...)
	return append(statements, requireMigrationTrigger("trg_risk_escalation_supervisor_authority_insert", riskEscalationStatements), requireMigrationTrigger("trg_host_command_supervisor_envelope_immutable", riskEscalationStatements), `PRAGMA legacy_alter_table=OFF;`)
}()
