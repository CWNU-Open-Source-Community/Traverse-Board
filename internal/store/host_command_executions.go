package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/redact"
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

func (s *SQLiteStore) PrepareHostExecutionIntent(
	ctx context.Context,
	intent runner.HostExecutionIntent,
) (bool, error) {
	if err := intent.Validate(); err != nil {
		return false, apperror.Wrap(apperror.CodeInvalidArgument,
			"host command execution intent is invalid", err)
	}
	argvJSON, environmentKeysJSON, err := encodeHostExecutionSpec(intent.Spec)
	if err != nil {
		return false, err
	}
	if redact.String(argvJSON) != argvJSON ||
		redact.String(environmentKeysJSON) != environmentKeysJSON ||
		redact.String(intent.Spec.ExecutablePath) !=
			intent.Spec.ExecutablePath ||
		redact.String(intent.Spec.WorkingDirectory) !=
			intent.Spec.WorkingDirectory {
		return false, apperror.New(apperror.CodeInvalidArgument,
			"host command execution intent contains secret-like data")
	}
	requestFingerprint := runner.HostExecutionIntentFingerprint(intent)
	if requestFingerprint == "" {
		return false, apperror.New(apperror.CodeInvalidArgument,
			"host command execution request fingerprint is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunExecutionInteractionWriteLockTx(
		ctx, tx, intent.RunID); err != nil {
		return false, err
	}
	existing, found, err := getHostExecutionIntent(
		ctx, tx, intent.RequestID)
	if err != nil {
		return false, err
	}
	if found {
		if !hostExecutionIntentsEqual(existing, intent) {
			return false, apperror.New(apperror.CodeConflict,
				"host command execution intent conflicts with its durable record")
		}
		operation, operationFound, err := getHostExecutionOperation(
			ctx, tx, intent.OperationKeyDigest)
		if err != nil {
			return false, err
		}
		if !operationFound ||
			operation.RequestID != intent.RequestID ||
			operation.RequestFingerprint != requestFingerprint {
			return false, apperror.New(apperror.CodeConflict,
				"host command execution operation record is inconsistent")
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	operation, found, err := getHostExecutionOperation(
		ctx, tx, intent.OperationKeyDigest)
	if err != nil {
		return false, err
	}
	if found {
		if operation.RequestID != intent.RequestID ||
			operation.RequestFingerprint != requestFingerprint ||
			operation.RunID != intent.RunID ||
			operation.RequestedBy != intent.RequestedBy {
			return false, apperror.New(apperror.CodeConflict,
				"host command execution operation key was reused for different intent")
		}
		return false, apperror.New(apperror.CodeConflict,
			"host command operation exists without its immutable intent")
	}

	runRecord, mission, err := getCoordinatorRunTx(ctx, tx, intent.RunID)
	if err != nil {
		return false, err
	}
	if (runRecord.Status != domain.RunCreated &&
		runRecord.Status != domain.RunPaused) ||
		runRecord.MissionID != intent.MissionID ||
		runRecord.SessionID != intent.SessionID ||
		mission.WorkspaceID != intent.WorkspaceID {
		return false, apperror.New(apperror.CodeFailedPrecondition,
			"host command execution requires the current created or paused Run")
	}
	if err := requireNoActiveRunControlLeaseTx(
		ctx, tx, runRecord.ID, intent.CreatedAt); err != nil {
		return false, err
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(
		ctx, tx, runRecord.ID)
	if err != nil {
		return false, err
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(
		ctx, tx, runRecord.ID)
	if err != nil {
		return false, err
	}
	permission, err := getCurrentRunExecutionPermissionSnapshot(
		ctx, tx, runRecord.ID)
	if err != nil {
		return false, err
	}
	mode, err := getCurrentRunModeSnapshot(ctx, tx, runRecord.ID)
	if err != nil {
		return false, err
	}
	if interaction.ID != intent.InteractionSnapshotID ||
		interaction.Revision != intent.InteractionRevision ||
		interaction.Mode != domain.RunExecutionInteractionControlled ||
		interaction.ExecutionProfileRevision !=
			intent.ExecutionProfileRevision ||
		profile.Revision != intent.ExecutionProfileRevision ||
		profile.Profile != domain.RunExecutionProfileLocal ||
		permission.ID != intent.PermissionSnapshotID ||
		permission.Revision != intent.PermissionRevision ||
		permission.Mode != intent.PermissionMode ||
		!permission.Mode.IncludesFullAccess() ||
		mode.Surface != domain.ExecutionSurfaceCode {
		return false, apperror.New(apperror.CodeConflict,
			"host command execution durable binding is stale")
	}
	spec := intent.Spec
	if _, err := tx.ExecContext(ctx, `INSERT INTO
		host_command_execution_intents
		(request_id, protocol_version, policy_version, operation_key_digest,
		run_id, mission_id, session_id, workspace_id, interaction_snapshot_id,
		interaction_revision, execution_profile_revision,
		permission_snapshot_id, permission_revision, permission_mode,
		spec_protocol_version, spec_policy_version, executable_path,
		executable_sha256, argv_json, working_directory, environment_policy,
		environment_keys_json, environment_sha256, network_intent,
		timeout_millis, purpose, spec_fingerprint, requested_by,
		non_sandboxed, automatic_retry_allowed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		intent.RequestID, intent.ProtocolVersion, intent.PolicyVersion,
		intent.OperationKeyDigest, intent.RunID, intent.MissionID,
		intent.SessionID, intent.WorkspaceID, intent.InteractionSnapshotID,
		intent.InteractionRevision, intent.ExecutionProfileRevision,
		intent.PermissionSnapshotID, intent.PermissionRevision,
		intent.PermissionMode, spec.ProtocolVersion, spec.PolicyVersion,
		spec.ExecutablePath, spec.ExecutableSHA256, argvJSON,
		spec.WorkingDirectory, spec.EnvironmentPolicy, environmentKeysJSON,
		spec.EnvironmentSHA256, spec.NetworkIntent,
		spec.TimeoutMilliseconds, spec.Purpose, spec.Fingerprint,
		intent.RequestedBy, intent.NonSandboxed,
		intent.AutomaticRetryAllowed, ts(intent.CreatedAt)); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO
		host_command_execution_operations
		(operation_key_digest, request_fingerprint, request_id, run_id,
		requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		intent.OperationKeyDigest, requestFingerprint, intent.RequestID,
		intent.RunID, intent.RequestedBy, ts(intent.CreatedAt)); err != nil {
		return false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, runRecord,
		events.HostCommandExecutionPreparedEvent,
		"host_command_execution", intent.RequestID, map[string]any{
			"protocol":                     intent.ProtocolVersion,
			"permission_mode":              string(intent.PermissionMode),
			"non_sandboxed":                true,
			"automatic_retry_allowed":      false,
			"environment_values_persisted": false,
			"raw_output_persisted":         false,
		}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
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

func encodeHostExecutionSpec(
	spec runner.HostCommandSpec,
) (string, string, error) {
	argv, err := json.Marshal(spec.Argv)
	if err != nil {
		return "", "", err
	}
	keys, err := json.Marshal(spec.EnvironmentKeys)
	if err != nil {
		return "", "", err
	}
	return string(argv), string(keys), nil
}

func hostExecutionIntentsEqual(
	left runner.HostExecutionIntent,
	right runner.HostExecutionIntent,
) bool {
	left.CreatedAt = time.Time{}
	right.CreatedAt = time.Time{}
	return reflect.DeepEqual(left, right)
}
