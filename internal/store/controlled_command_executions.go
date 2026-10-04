package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

const controlledExecutionIntentSelect = `SELECT protocol_version, policy_version,
	request_id, plan_id, plan_fingerprint, run_id, workspace_id,
	interaction_snapshot_id, interaction_revision, execution_profile_revision,
	kind, requested_by, created_at
	FROM controlled_command_execution_intents WHERE request_id = ?`

const controlledExecutionIntentByPlanSelect = `SELECT protocol_version,
	policy_version, request_id, plan_id, plan_fingerprint, run_id, workspace_id,
	interaction_snapshot_id, interaction_revision, execution_profile_revision,
	kind, requested_by, created_at
	FROM controlled_command_execution_intents WHERE plan_id = ?`

const controlledExecutionReceiptSelect = `SELECT request_id, protocol_version,
	policy_version, backend, exit_code, stdout_observed_bytes,
	stdout_captured_bytes, stdout_prefix_sha256, stdout_truncated,
	stderr_observed_bytes, stderr_captured_bytes, stderr_prefix_sha256,
	stderr_truncated, started_at, completed_at, timed_out, cancelled,
	output_limit_exceeded, tree_reaped, restricted_token, low_integrity_token,
	job_assigned_at_creation, kill_on_job_close, active_process_limit,
	process_memory_limit, stdin_closed, environment_inherited, network_requested,
	persistent_process, product_execution_enabled
	FROM controlled_command_execution_receipts WHERE request_id = ?`

type ControlledExecutionReceipt = runner.ControlledExecutionReceipt

// GetControlledExecutionIntentByPlanID reads the retired CLI operation namespace
// before any current runtime or permission checks. It never creates an intent.
func (s *SQLiteStore) GetControlledExecutionIntentByPlanID(ctx context.Context, planID string) (runner.ControlledExecutionIntent, bool, error) {
	return getControlledExecutionIntentByPlanID(ctx, s.db, planID)
}

func (s *SQLiteStore) GetControlledExecutionIntent(
	ctx context.Context,
	requestID string,
) (runner.ControlledExecutionIntent, bool, error) {
	requestID = strings.TrimSpace(requestID)
	if !validControlledExecutionStoreIdentity(requestID) {
		return runner.ControlledExecutionIntent{}, false,
			apperror.New(apperror.CodeInvalidArgument,
				"controlled command execution request id is invalid")
	}
	return getControlledExecutionIntent(ctx, s.db, requestID)
}

func (s *SQLiteStore) GetControlledExecutionReceipt(ctx context.Context,
	requestID string,
) (ControlledExecutionReceipt, bool, error) {
	return getControlledExecutionReceipt(ctx, s.db, strings.TrimSpace(requestID))
}

type controlledExecutionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getControlledExecutionIntent(ctx context.Context,
	queryer controlledExecutionQueryer, requestID string,
) (runner.ControlledExecutionIntent, bool, error) {
	return scanControlledExecutionIntent(queryer.QueryRowContext(ctx,
		controlledExecutionIntentSelect, requestID))
}

func getControlledExecutionIntentByPlanID(ctx context.Context,
	queryer controlledExecutionQueryer, planID string,
) (runner.ControlledExecutionIntent, bool, error) {
	return scanControlledExecutionIntent(queryer.QueryRowContext(ctx,
		controlledExecutionIntentByPlanSelect, planID))
}

func scanControlledExecutionIntent(row *sql.Row) (
	runner.ControlledExecutionIntent, bool, error,
) {
	var intent runner.ControlledExecutionIntent
	var kind string
	var created string
	err := row.Scan(&intent.ProtocolVersion, &intent.PolicyVersion,
		&intent.RequestID, &intent.PlanID, &intent.PlanFingerprint,
		&intent.RunID, &intent.WorkspaceID, &intent.InteractionSnapshotID,
		&intent.InteractionRevision, &intent.ExecutionProfileRevision,
		&kind, &intent.RequestedBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.ControlledExecutionIntent{}, false, nil
	}
	if err != nil {
		return runner.ControlledExecutionIntent{}, false, err
	}
	parsed, err := runner.ParseControlledCommandKind(kind)
	if err != nil {
		return runner.ControlledExecutionIntent{}, false, err
	}
	intent.Kind = parsed
	intent.CreatedAt = parseTS(created)
	if err := intent.Validate(); err != nil {
		return runner.ControlledExecutionIntent{}, false, fmt.Errorf(
			"stored controlled command execution intent is invalid: %w", err)
	}
	return intent, true, nil
}

func getControlledExecutionReceipt(ctx context.Context,
	queryer controlledExecutionQueryer, requestID string,
) (ControlledExecutionReceipt, bool, error) {
	if !validControlledExecutionStoreIdentity(requestID) {
		return ControlledExecutionReceipt{}, false,
			apperror.New(apperror.CodeInvalidArgument,
				"controlled command execution request id is invalid")
	}
	var receipt ControlledExecutionReceipt
	var started, completed string
	err := queryer.QueryRowContext(ctx, controlledExecutionReceiptSelect,
		requestID).Scan(&receipt.RequestID, &receipt.ProtocolVersion,
		&receipt.PolicyVersion, &receipt.Backend, &receipt.ExitCode,
		&receipt.StdoutObservedBytes, &receipt.StdoutCapturedBytes,
		&receipt.StdoutPrefixSHA256, &receipt.StdoutTruncated,
		&receipt.StderrObservedBytes, &receipt.StderrCapturedBytes,
		&receipt.StderrPrefixSHA256, &receipt.StderrTruncated,
		&started, &completed, &receipt.TimedOut, &receipt.Cancelled,
		&receipt.OutputLimitExceeded, &receipt.TreeReaped,
		&receipt.RestrictedToken, &receipt.LowIntegrityToken,
		&receipt.JobAssignedAtCreation, &receipt.KillOnJobClose,
		&receipt.ActiveProcessLimit, &receipt.ProcessMemoryLimit,
		&receipt.StdinClosed, &receipt.EnvironmentInherited,
		&receipt.NetworkRequested, &receipt.PersistentProcess,
		&receipt.ProductExecutionEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ControlledExecutionReceipt{}, false, nil
	}
	if err != nil {
		return ControlledExecutionReceipt{}, false, err
	}
	receipt.StartedAt = parseTS(started)
	receipt.CompletedAt = parseTS(completed)
	if err := receipt.Validate(); err != nil {
		return ControlledExecutionReceipt{}, false, fmt.Errorf(
			"stored controlled command execution receipt is invalid: %w", err)
	}
	return receipt, true, nil
}

func validControlledExecutionStoreIdentity(value string) bool {
	return domain.ValidAgentID(value) && !strings.ContainsRune(value, 0)
}
