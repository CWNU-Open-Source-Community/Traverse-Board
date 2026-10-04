package store

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runner"
	"database/sql"
	"fmt"
)

// Frozen old-schema migration input. This is data construction, not execution.
func seedHistoricalHostExecutionReceipt(
	ctx context.Context, s *SQLiteStore,
	intent runner.HostExecutionIntent, receipt runner.HostExecutionReceipt,
) (runner.HostExecutionReceipt, bool, error) {
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	if version != 141 && version != 142 {
		return runner.HostExecutionReceipt{}, false, fmt.Errorf("historical receipt fixture rejects schema %d", version)
	}

	if err := receipt.Validate(); err != nil {
		return runner.HostExecutionReceipt{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument,
				"historical host command receipt is invalid", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	storedIntent, found, err := getHostExecutionIntent(ctx, tx, receipt.RequestID)
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	if !found || intent.RequestID != receipt.RequestID || !hostExecutionIntentsEqual(storedIntent, intent) {
		return runner.HostExecutionReceipt{}, false, apperror.New(
			apperror.CodeConflict,
			"host command execution result is not bound to its intent")
	}
	existing, exists, err := getHostExecutionReceipt(
		ctx, tx, receipt.RequestID)
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	if exists {
		if existing != receipt {
			return runner.HostExecutionReceipt{}, false, apperror.New(
				apperror.CodeConflict,
				"host command execution receipt conflicts with its durable record")
		}
		if err := tx.Commit(); err != nil {
			return runner.HostExecutionReceipt{}, false, err
		}
		return existing, true, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO
		host_command_execution_receipts
		(request_id, protocol_version, policy_version, backend, exit_code,
		stdout_observed_bytes, stdout_captured_bytes, stdout_prefix_sha256,
		stdout_truncated, stderr_observed_bytes, stderr_captured_bytes,
		stderr_prefix_sha256, stderr_truncated, started_at, completed_at,
		timed_out, cancelled, output_limit_exceeded, tree_reaped,
		non_sandboxed, restricted_token, low_integrity_token,
		job_assigned_at_creation, kill_on_job_close, active_process_limit,
		job_memory_limit, stdin_closed, environment_inherited,
		network_requested, persistent_process, product_execution_enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		receipt.RequestID, receipt.ProtocolVersion, receipt.PolicyVersion,
		receipt.Backend, receipt.ExitCode, receipt.StdoutObservedBytes,
		receipt.StdoutCapturedBytes, receipt.StdoutPrefixSHA256,
		receipt.StdoutTruncated, receipt.StderrObservedBytes,
		receipt.StderrCapturedBytes, receipt.StderrPrefixSHA256,
		receipt.StderrTruncated, ts(receipt.StartedAt),
		ts(receipt.CompletedAt), receipt.TimedOut, receipt.Cancelled,
		receipt.OutputLimitExceeded, receipt.TreeReaped,
		receipt.NonSandboxed, receipt.RestrictedToken,
		receipt.LowIntegrityToken, receipt.JobAssignedAtCreation,
		receipt.KillOnJobClose, receipt.ActiveProcessLimit,
		receipt.JobMemoryLimit, receipt.StdinClosed,
		receipt.EnvironmentInherited, receipt.NetworkRequested,
		receipt.PersistentProcess, receipt.ProductExecutionEnabled); err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	runRecord, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id,
		session_id, status, config_json, budget_json, started_at, finished_at,
		created_at, updated_at FROM runs WHERE id = ?`, intent.RunID))
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, runRecord,
		events.HostCommandExecutionCompletedEvent,
		"host_command_execution", receipt.RequestID, map[string]any{
			"protocol":              receipt.ProtocolVersion,
			"exit_code":             receipt.ExitCode,
			"timed_out":             receipt.TimedOut,
			"cancelled":             receipt.Cancelled,
			"output_limit_exceeded": receipt.OutputLimitExceeded,
			"tree_reaped":           receipt.TreeReaped,
			"non_sandboxed":         receipt.NonSandboxed,
			"stdout_observed_bytes": receipt.StdoutObservedBytes,
			"stderr_observed_bytes": receipt.StderrObservedBytes,
			"raw_output_persisted":  false,
		}); err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	return receipt, false, nil
}
