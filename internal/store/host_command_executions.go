package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runner"
)

const hostExecutionIntentSelect = `SELECT protocol_version, policy_version,
	request_id, operation_key_digest, run_id, mission_id, session_id,
	workspace_id, interaction_snapshot_id, interaction_revision,
	execution_profile_revision, permission_snapshot_id, permission_revision,
	permission_mode, spec_protocol_version, spec_policy_version,
	executable_path, executable_sha256, argv_json, working_directory,
	environment_policy, environment_keys_json, environment_sha256,
	network_intent, timeout_millis, purpose, spec_fingerprint, requested_by,
	non_sandboxed, automatic_retry_allowed, created_at
	FROM host_command_execution_intents WHERE request_id = ?`

const hostExecutionReceiptSelect = `SELECT request_id, protocol_version,
	policy_version, backend, exit_code, stdout_observed_bytes,
	stdout_captured_bytes, stdout_prefix_sha256, stdout_truncated,
	stderr_observed_bytes, stderr_captured_bytes, stderr_prefix_sha256,
	stderr_truncated, started_at, completed_at, timed_out, cancelled,
	output_limit_exceeded, tree_reaped, non_sandboxed, restricted_token,
	low_integrity_token, job_assigned_at_creation, kill_on_job_close,
	active_process_limit, job_memory_limit, stdin_closed,
	environment_inherited, network_requested, persistent_process,
	product_execution_enabled
	FROM host_command_execution_receipts WHERE request_id = ?`

type hostExecutionOperation struct {
	OperationKeyDigest string
	RequestFingerprint string
	RequestID          string
	RunID              string
	RequestedBy        string
	CreatedAt          time.Time
}

func (s *SQLiteStore) RecordHostExecutionResult(
	ctx context.Context,
	result runner.HostExecutionResult,
) (runner.HostExecutionReceipt, bool, error) {
	if err := result.Validate(); err != nil {
		return runner.HostExecutionReceipt{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument,
				"host command execution result is invalid", err)
	}
	receipt, err := runner.ProjectHostExecutionReceipt(result)
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	intent, found, err := getHostExecutionIntent(ctx, tx, result.RequestID)
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	if !found || intent.OperationKeyDigest != result.OperationKeyDigest ||
		intent.RunID != result.RunID || intent.MissionID != result.MissionID ||
		intent.SessionID != result.SessionID ||
		intent.WorkspaceID != result.WorkspaceID ||
		intent.InteractionSnapshotID != result.InteractionSnapshotID ||
		intent.InteractionRevision != result.InteractionRevision ||
		intent.ExecutionProfileRevision != result.ExecutionProfileRevision ||
		intent.PermissionSnapshotID != result.PermissionSnapshotID ||
		intent.PermissionRevision != result.PermissionRevision ||
		intent.PermissionMode != result.PermissionMode ||
		intent.Spec.Fingerprint != result.SpecFingerprint {
		return runner.HostExecutionReceipt{}, false, apperror.New(
			apperror.CodeConflict,
			"host command execution result is not bound to its intent")
	}
	existing, exists, err := getHostExecutionReceipt(
		ctx, tx, result.RequestID)
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

func (s *SQLiteStore) GetHostExecutionIntent(
	ctx context.Context,
	requestID string,
) (runner.HostExecutionIntent, bool, error) {
	requestID = strings.TrimSpace(requestID)
	if !domain.ValidAgentID(requestID) || strings.ContainsRune(requestID, 0) {
		return runner.HostExecutionIntent{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"host command execution request id is invalid")
	}
	return getHostExecutionIntent(ctx, s.db, requestID)
}

// GetHostExecutionIntentByOperation is a read-only compatibility barrier. The
// old operation namespace is checked before a CLI request can enter the shared
// Command Runtime; an orphan operation or intent cannot become a new process.
func (s *SQLiteStore) GetHostExecutionIntentByOperation(ctx context.Context, runID, digest string) (runner.HostExecutionIntent, bool, error) {
	if !domain.ValidAgentID(runID) || len(digest) != 64 {
		return runner.HostExecutionIntent{}, false, apperror.New(apperror.CodeInvalidArgument, "host command operation identity is invalid")
	}
	operation, found, err := getHostExecutionOperation(ctx, s.db, digest)
	if err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	if !found {
		var requestID string
		err := s.db.QueryRowContext(ctx, `SELECT request_id FROM host_command_execution_intents WHERE operation_key_digest = ?`, digest).Scan(&requestID)
		if errors.Is(err, sql.ErrNoRows) {
			return runner.HostExecutionIntent{}, false, nil
		}
		if err != nil {
			return runner.HostExecutionIntent{}, false, err
		}
		return runner.HostExecutionIntent{}, false, apperror.New(apperror.CodeConflict, "host intent has no matching operation record; automatic retry is disabled")
	}
	intent, exists, err := getHostExecutionIntent(ctx, s.db, operation.RequestID)
	if err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	if !exists || operation.RunID != runID || intent.RunID != runID || intent.OperationKeyDigest != digest ||
		operation.RequestedBy != intent.RequestedBy || operation.RequestFingerprint != runner.HostExecutionIntentFingerprint(intent) {
		return runner.HostExecutionIntent{}, false, apperror.New(apperror.CodeConflict, "host command operation record is inconsistent; automatic retry is disabled")
	}
	return intent, true, nil
}

func (s *SQLiteStore) GetHostExecutionReceipt(
	ctx context.Context,
	requestID string,
) (runner.HostExecutionReceipt, bool, error) {
	requestID = strings.TrimSpace(requestID)
	if !domain.ValidAgentID(requestID) || strings.ContainsRune(requestID, 0) {
		return runner.HostExecutionReceipt{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"host command execution request id is invalid")
	}
	return getHostExecutionReceipt(ctx, s.db, requestID)
}

type hostExecutionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getHostExecutionIntent(
	ctx context.Context,
	queryer hostExecutionQueryer,
	requestID string,
) (runner.HostExecutionIntent, bool, error) {
	var intent runner.HostExecutionIntent
	var permissionMode string
	var argvJSON, environmentKeysJSON string
	var networkIntent string
	var createdAt string
	err := queryer.QueryRowContext(ctx, hostExecutionIntentSelect, requestID).
		Scan(&intent.ProtocolVersion, &intent.PolicyVersion,
			&intent.RequestID, &intent.OperationKeyDigest,
			&intent.RunID, &intent.MissionID, &intent.SessionID,
			&intent.WorkspaceID, &intent.InteractionSnapshotID,
			&intent.InteractionRevision, &intent.ExecutionProfileRevision,
			&intent.PermissionSnapshotID, &intent.PermissionRevision,
			&permissionMode, &intent.Spec.ProtocolVersion,
			&intent.Spec.PolicyVersion, &intent.Spec.ExecutablePath,
			&intent.Spec.ExecutableSHA256, &argvJSON,
			&intent.Spec.WorkingDirectory, &intent.Spec.EnvironmentPolicy,
			&environmentKeysJSON, &intent.Spec.EnvironmentSHA256,
			&networkIntent, &intent.Spec.TimeoutMilliseconds,
			&intent.Spec.Purpose, &intent.Spec.Fingerprint,
			&intent.RequestedBy, &intent.NonSandboxed,
			&intent.AutomaticRetryAllowed, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.HostExecutionIntent{}, false, nil
	}
	if err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	mode, err := domain.ParseRunExecutionPermissionMode(permissionMode)
	if err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	intent.PermissionMode = mode
	intent.Spec.NetworkIntent = runner.HostNetworkIntent(networkIntent)
	if err := json.Unmarshal([]byte(argvJSON), &intent.Spec.Argv); err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	if err := json.Unmarshal(
		[]byte(environmentKeysJSON),
		&intent.Spec.EnvironmentKeys,
	); err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	intent.CreatedAt = parseTS(createdAt)
	if err := intent.Validate(); err != nil {
		return runner.HostExecutionIntent{}, false, fmt.Errorf(
			"stored host command execution intent is invalid: %w", err)
	}
	return intent, true, nil
}

func getHostExecutionOperation(
	ctx context.Context,
	queryer hostExecutionQueryer,
	keyDigest string,
) (hostExecutionOperation, bool, error) {
	var operation hostExecutionOperation
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT operation_key_digest,
		request_fingerprint, request_id, run_id, requested_by, created_at
		FROM host_command_execution_operations
		WHERE operation_key_digest = ?`, keyDigest).
		Scan(&operation.OperationKeyDigest, &operation.RequestFingerprint,
			&operation.RequestID, &operation.RunID, &operation.RequestedBy,
			&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return hostExecutionOperation{}, false, nil
	}
	if err != nil {
		return hostExecutionOperation{}, false, err
	}
	operation.CreatedAt = parseTS(createdAt)
	if operation.OperationKeyDigest == "" ||
		operation.RequestFingerprint == "" ||
		operation.RequestID == "" || operation.RunID == "" ||
		operation.RequestedBy == "" || operation.CreatedAt.IsZero() {
		return hostExecutionOperation{}, false,
			errors.New("stored host command operation is invalid")
	}
	return operation, true, nil
}

func getHostExecutionReceipt(
	ctx context.Context,
	queryer hostExecutionQueryer,
	requestID string,
) (runner.HostExecutionReceipt, bool, error) {
	var receipt runner.HostExecutionReceipt
	var startedAt, completedAt string
	err := queryer.QueryRowContext(ctx, hostExecutionReceiptSelect, requestID).
		Scan(&receipt.RequestID, &receipt.ProtocolVersion,
			&receipt.PolicyVersion, &receipt.Backend, &receipt.ExitCode,
			&receipt.StdoutObservedBytes, &receipt.StdoutCapturedBytes,
			&receipt.StdoutPrefixSHA256, &receipt.StdoutTruncated,
			&receipt.StderrObservedBytes, &receipt.StderrCapturedBytes,
			&receipt.StderrPrefixSHA256, &receipt.StderrTruncated,
			&startedAt, &completedAt, &receipt.TimedOut,
			&receipt.Cancelled, &receipt.OutputLimitExceeded,
			&receipt.TreeReaped, &receipt.NonSandboxed,
			&receipt.RestrictedToken, &receipt.LowIntegrityToken,
			&receipt.JobAssignedAtCreation, &receipt.KillOnJobClose,
			&receipt.ActiveProcessLimit, &receipt.JobMemoryLimit,
			&receipt.StdinClosed, &receipt.EnvironmentInherited,
			&receipt.NetworkRequested, &receipt.PersistentProcess,
			&receipt.ProductExecutionEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.HostExecutionReceipt{}, false, nil
	}
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	receipt.StartedAt = parseTS(startedAt)
	receipt.CompletedAt = parseTS(completedAt)
	if err := receipt.Validate(); err != nil {
		return runner.HostExecutionReceipt{}, false, fmt.Errorf(
			"stored host command execution receipt is invalid: %w", err)
	}
	return receipt, true, nil
}
