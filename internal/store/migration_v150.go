package store

import "strings"

const legacyUnboundSupervisorMCPAuthority = `{"version":0,"legacy_unbound":true}`

func browserActionSupervisorToolCallCreate(tableName string) string {
	statement := riskEscalationSupervisorToolCallCreate(tableName)
	legacyAuthoritySet := "CHECK((tool_name IN ('host_command_propose', 'workspace_list'"
	mcpAuthoritySet := "CHECK((tool_name IN ('host_command_propose', 'mcp_tool_call', 'workspace_list'"
	legacyNonAuthoritySet := "OR (tool_name NOT IN ('workspace_list'"
	mcpNonAuthoritySet := "OR (tool_name NOT IN ('mcp_tool_call', 'workspace_list'"
	if strings.Count(statement, legacyAuthoritySet) != 1 ||
		strings.Count(statement, legacyNonAuthoritySet) != 1 {
		panic("current Supervisor MCP authority constraints are unavailable")
	}
	statement = strings.Replace(statement, legacyAuthoritySet, mcpAuthoritySet, 1)
	statement = strings.Replace(statement, legacyNonAuthoritySet, mcpNonAuthoritySet, 1)
	codeIntelTail := "'code_type_hierarchy')"
	browserActionTail := "'code_type_hierarchy',\n" +
		"\t\t\t'browser_status', 'browser_navigate', 'browser_snapshot',\n" +
		"\t\t\t'browser_click', 'browser_type', 'browser_screenshot')"
	if strings.Count(statement, codeIntelTail) != 3 {
		panic("current Supervisor browser-action authority constraints are unavailable")
	}
	return strings.ReplaceAll(statement, codeIntelTail, browserActionTail)
}

// browserActionSupervisorLedgerStatements admits the six advertised browser
// actions and closes the MCP persistence gap in the durable Supervisor call
// ledger. Both tool families join both sides of the authority constraint, so
// every new stored call requires bounded JSON authority while the legacy
// dual-mode host proposal remains unchanged. Historical MCP rows predate the
// authority envelope; migration marks them explicitly unbound so they remain
// readable but fail the shared codec and can never resume execution.
var browserActionSupervisorLedgerStatements = func() []string {
	const tableName = "run_supervisor_tool_calls_v150"
	const backupName = "run_supervisor_tool_calls_v149"
	statements := []string{
		`DROP TRIGGER trg_risk_escalation_supervisor_authority_insert;`,
		`DROP TRIGGER trg_host_command_supervisor_envelope_immutable;`,
	}
	rebuild := rebuildRiskEscalationSupervisorToolCalls(
		browserActionSupervisorToolCallCreate(tableName), tableName, backupName)
	legacyCopy := `INSERT INTO ` + tableName + ` SELECT * FROM ` + backupName + `;`
	boundedCopy := `INSERT INTO ` + tableName + `
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		 payload_json, authority_json, status, result_json, error_code, created_at, completed_at,
		 stream_response_id, stream_item_id, stream_call_id)
		SELECT run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
			payload_json,
			CASE WHEN tool_name = 'mcp_tool_call' AND authority_json = ''
				THEN '` + legacyUnboundSupervisorMCPAuthority + `'
				ELSE authority_json END,
			status, result_json, error_code, created_at, completed_at,
			stream_response_id, stream_item_id, stream_call_id
		FROM ` + backupName + `;`
	replaced := false
	for index := range rebuild {
		if rebuild[index] == legacyCopy {
			rebuild[index] = boundedCopy
			replaced = true
		}
	}
	if !replaced {
		panic("current Supervisor MCP legacy copy statement is unavailable")
	}
	statements = append(statements, rebuild...)
	return append(statements,
		requireMigrationTrigger("trg_risk_escalation_supervisor_authority_insert",
			riskEscalationStatements),
		requireMigrationTrigger("trg_host_command_supervisor_envelope_immutable",
			riskEscalationStatements))
}()
