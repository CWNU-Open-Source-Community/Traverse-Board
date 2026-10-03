package application

import (
	"cyberagent-workbench/internal/domain"
	"database/sql"
	"testing"
	"time"
)

// This synthetic retained-row fixture exercises native compatibility guards.
// Genuine historical-schema upgrades are covered separately by the store's
// v178/v180 tests; this helper is not evidence of a historical upgrade.
func seedRetainedCommandPermission(t testing.TB, db *sql.DB, current domain.RunExecutionPermissionSnapshot, mode domain.RunExecutionPermissionMode) domain.RunExecutionPermissionSnapshot {
	t.Helper()
	value, err := current.Next(current.ID+"-retained", mode, mode != domain.RunExecutionPermissionConservative, "historical-operator", "retained native boundary fixture", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const trigger = "trg_run_execution_permission_snapshot_insert"
	var original string
	if err = tx.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, trigger).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(t.Context(), "DROP TRIGGER "+trigger); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(t.Context(), `INSERT INTO run_execution_permission_snapshots
        (id,run_id,mission_id,revision,protocol_version,mode,approval_policy,command_scope,filesystem_scope,network_scope,persistent_terminal,background_process,agent_terminal_input,risk_tier,required_gate,policy_version,operator_confirmed,process_enabled,execution_authorized,capability_grant,requested_by,reason,created_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.RunID, value.MissionID, value.Revision, value.ProtocolVersion, value.Mode, value.ApprovalPolicy, value.CommandScope, value.FilesystemScope, value.NetworkScope, value.PersistentTerminal, value.BackgroundProcess, value.AgentTerminalInput, value.RiskTier, value.RequiredGate, value.PolicyVersion, value.OperatorConfirmed, value.ProcessEnabled, value.ExecutionAuthorized, value.CapabilityGrant, value.RequestedBy, value.Reason, value.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	var restored string
	if err = tx.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, trigger).Scan(&restored); err != nil || original != restored {
		t.Fatal("fixture changed live writer guard", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return value
}
