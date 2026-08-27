package store

// An intermediate Windows Desktop preview applied v136 before its two final
// Supervisor authority triggers were added. Its exact migration checksum is
// accepted separately; this migration then makes both the legacy and final
// v136 histories converge on the same fail-closed trigger definitions.
var legacyRiskEscalationSupervisorTriggerRepairStatements = []string{
	`DROP TRIGGER IF EXISTS trg_risk_escalation_supervisor_authority_insert;`,
	`DROP TRIGGER IF EXISTS trg_host_command_supervisor_envelope_immutable;`,
	requireMigrationTrigger("trg_risk_escalation_supervisor_authority_insert",
		riskEscalationStatements),
	requireMigrationTrigger("trg_host_command_supervisor_envelope_immutable",
		riskEscalationStatements),
}
