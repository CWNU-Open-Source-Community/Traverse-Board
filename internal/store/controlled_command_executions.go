package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/redact"
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

type ControlledExecutionReceipt struct {
	RequestID               string
	ProtocolVersion         string
	PolicyVersion           string
	Backend                 string
	ExitCode                int
	StdoutObservedBytes     int64
	StdoutCapturedBytes     int
	StdoutPrefixSHA256      string
	StdoutTruncated         bool
	StderrObservedBytes     int64
	StderrCapturedBytes     int
	StderrPrefixSHA256      string
	StderrTruncated         bool
	StartedAt               time.Time
	CompletedAt             time.Time
	TimedOut                bool
	Cancelled               bool
	OutputLimitExceeded     bool
	TreeReaped              bool
	RestrictedToken         bool
	LowIntegrityToken       bool
	JobAssignedAtCreation   bool
	KillOnJobClose          bool
	ActiveProcessLimit      int
	ProcessMemoryLimit      int64
	StdinClosed             bool
	EnvironmentInherited    bool
	NetworkRequested        bool
	PersistentProcess       bool
	ProductExecutionEnabled bool
}

func (r ControlledExecutionReceipt) Validate() error {
	expectedStdoutCapture := r.StdoutObservedBytes
	if expectedStdoutCapture > runner.MaxControlledOutputCaptureBytes {
		expectedStdoutCapture = runner.MaxControlledOutputCaptureBytes
	}
	expectedStderrCapture := r.StderrObservedBytes
	if expectedStderrCapture > runner.MaxControlledOutputCaptureBytes {
		expectedStderrCapture = runner.MaxControlledOutputCaptureBytes
	}
	if !validControlledExecutionStoreIdentity(r.RequestID) ||
		r.ProtocolVersion != runner.ControlledExecutionProtocolVersion ||
		r.PolicyVersion != runner.ControlledExecutionPolicyVersion ||
		!validControlledExecutionStoreIdentity(r.Backend) ||
		r.StdoutObservedBytes < 0 ||
		r.StdoutObservedBytes > runner.MaxControlledOutputObservedBytes ||
		r.StdoutCapturedBytes < 0 ||
		r.StdoutCapturedBytes > runner.MaxControlledOutputCaptureBytes ||
		int64(r.StdoutCapturedBytes) > r.StdoutObservedBytes ||
		int64(r.StdoutCapturedBytes) != expectedStdoutCapture ||
		!validStoreDigest(r.StdoutPrefixSHA256) ||
		r.StdoutTruncated !=
			(r.StdoutObservedBytes > int64(r.StdoutCapturedBytes)) ||
		r.StderrObservedBytes < 0 ||
		r.StderrObservedBytes > runner.MaxControlledOutputObservedBytes ||
		r.StderrCapturedBytes < 0 ||
		r.StderrCapturedBytes > runner.MaxControlledOutputCaptureBytes ||
		int64(r.StderrCapturedBytes) > r.StderrObservedBytes ||
		int64(r.StderrCapturedBytes) != expectedStderrCapture ||
		!validStoreDigest(r.StderrPrefixSHA256) ||
		r.StderrTruncated !=
			(r.StderrObservedBytes > int64(r.StderrCapturedBytes)) ||
		r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) ||
		(r.TimedOut && r.Cancelled) || !r.TreeReaped ||
		(r.OutputLimitExceeded &&
			r.StdoutObservedBytes != runner.MaxControlledOutputObservedBytes &&
			r.StderrObservedBytes != runner.MaxControlledOutputObservedBytes) ||
		!r.RestrictedToken || !r.LowIntegrityToken ||
		!r.JobAssignedAtCreation || !r.KillOnJobClose ||
		r.ActiveProcessLimit != 1 ||
		r.ProcessMemoryLimit != runner.MaxControlledProcessMemoryBytes ||
		!r.StdinClosed || r.EnvironmentInherited || r.NetworkRequested ||
		r.PersistentProcess || !r.ProductExecutionEnabled {
		return runner.ErrControlledExecutionBoundary
	}
	return nil
}

func (s *SQLiteStore) PrepareControlledExecutionIntent(ctx context.Context,
	intent runner.ControlledExecutionIntent,
) (bool, error) {
	if err := intent.Validate(); err != nil ||
		redact.String(intent.RequestedBy) != intent.RequestedBy {
		return false, apperror.Wrap(apperror.CodeInvalidArgument,
			"controlled command execution intent is invalid", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunExecutionInteractionWriteLockTx(ctx, tx,
		intent.RunID); err != nil {
		return false, err
	}
	existing, found, err := getControlledExecutionIntent(ctx, tx,
		intent.RequestID)
	if err != nil {
		return false, err
	}
	if found {
		if !controlledExecutionIntentMatches(existing, intent) {
			return false, apperror.New(apperror.CodeConflict,
				"controlled command execution intent conflicts with its durable record")
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	existing, found, err = getControlledExecutionIntentByPlanID(ctx, tx,
		intent.PlanID)
	if err != nil {
		return false, err
	}
	if found {
		if !controlledExecutionIntentMatches(existing, intent) {
			return false, apperror.New(apperror.CodeConflict,
				"controlled command execution operation key was already used for different intent")
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	run, mission, err := getCoordinatorRunTx(ctx, tx, intent.RunID)
	if err != nil {
		return false, err
	}
	if run.Terminal() || (run.Status != domain.RunCreated &&
		run.Status != domain.RunPaused) {
		return false, apperror.New(apperror.CodeFailedPrecondition,
			"controlled command execution requires a created or paused Run")
	}
	if mission.WorkspaceID != intent.WorkspaceID {
		return false, apperror.New(apperror.CodeConflict,
			"controlled command execution Workspace binding is stale")
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx,
		run.ID)
	if err != nil {
		return false, err
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, run.ID)
	if err != nil {
		return false, err
	}
	if interaction.ID != intent.InteractionSnapshotID ||
		interaction.Revision != intent.InteractionRevision ||
		interaction.Mode != domain.RunExecutionInteractionControlled ||
		interaction.ExecutionProfileRevision !=
			intent.ExecutionProfileRevision ||
		profile.Revision != intent.ExecutionProfileRevision ||
		profile.Profile != domain.RunExecutionProfileLocal {
		return false, apperror.New(apperror.CodeConflict,
			"controlled command execution durable binding is stale")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO
		controlled_command_execution_intents
		(request_id, protocol_version, policy_version, plan_id, plan_fingerprint,
		run_id, workspace_id, interaction_snapshot_id, interaction_revision,
		execution_profile_revision, kind, requested_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		intent.RequestID, intent.ProtocolVersion, intent.PolicyVersion,
		intent.PlanID, intent.PlanFingerprint, intent.RunID, intent.WorkspaceID,
		intent.InteractionSnapshotID, intent.InteractionRevision,
		intent.ExecutionProfileRevision, intent.Kind, intent.RequestedBy,
		ts(intent.CreatedAt)); err != nil {
		return false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.ControlledCommandExecutionPreparedEvent,
		"controlled_command_execution", intent.RequestID, map[string]any{
			"protocol":                   intent.ProtocolVersion,
			"kind":                       string(intent.Kind),
			"interaction_revision":       intent.InteractionRevision,
			"execution_profile_revision": intent.ExecutionProfileRevision,
			"raw_output_persisted":       false,
		}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *SQLiteStore) RecordControlledExecutionResult(ctx context.Context,
	result runner.ControlledExecutionResult,
) (ControlledExecutionReceipt, bool, error) {
	if err := result.Validate(); err != nil {
		return ControlledExecutionReceipt{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument,
				"controlled command execution result is invalid", err)
	}
	receipt := projectControlledExecutionReceipt(result)
	if err := receipt.Validate(); err != nil {
		return ControlledExecutionReceipt{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return ControlledExecutionReceipt{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	intent, found, err := getControlledExecutionIntent(ctx, tx,
		result.RequestID)
	if err != nil {
		return ControlledExecutionReceipt{}, false, err
	}
	if !found || intent.PlanID != result.PlanID ||
		intent.PlanFingerprint != result.PlanFingerprint ||
		intent.RunID != result.RunID || intent.WorkspaceID != result.WorkspaceID ||
		intent.InteractionSnapshotID != result.InteractionSnapshotID ||
		intent.InteractionRevision != result.InteractionRevision ||
		intent.ExecutionProfileRevision != result.ExecutionProfileRevision ||
		intent.Kind != result.Kind {
		return ControlledExecutionReceipt{}, false, apperror.New(
			apperror.CodeConflict,
			"controlled command execution result is not bound to its intent")
	}
	existing, exists, err := getControlledExecutionReceipt(ctx, tx,
		result.RequestID)
	if err != nil {
		return ControlledExecutionReceipt{}, false, err
	}
	if exists {
		if existing != receipt {
			return ControlledExecutionReceipt{}, false, apperror.New(
				apperror.CodeConflict,
				"controlled command execution receipt conflicts with its durable record")
		}
		if err := tx.Commit(); err != nil {
			return ControlledExecutionReceipt{}, false, err
		}
		return existing, true, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO
		controlled_command_execution_receipts
		(request_id, protocol_version, policy_version, backend, exit_code,
		stdout_observed_bytes, stdout_captured_bytes, stdout_prefix_sha256,
		stdout_truncated, stderr_observed_bytes, stderr_captured_bytes,
		stderr_prefix_sha256, stderr_truncated, started_at, completed_at,
		timed_out, cancelled, output_limit_exceeded, tree_reaped,
		restricted_token, low_integrity_token, job_assigned_at_creation,
		kill_on_job_close, active_process_limit, process_memory_limit,
		stdin_closed, environment_inherited, network_requested,
		persistent_process, product_execution_enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		receipt.RequestID, receipt.ProtocolVersion, receipt.PolicyVersion,
		receipt.Backend, receipt.ExitCode, receipt.StdoutObservedBytes,
		receipt.StdoutCapturedBytes, receipt.StdoutPrefixSHA256,
		receipt.StdoutTruncated, receipt.StderrObservedBytes,
		receipt.StderrCapturedBytes, receipt.StderrPrefixSHA256,
		receipt.StderrTruncated, ts(receipt.StartedAt), ts(receipt.CompletedAt),
		receipt.TimedOut, receipt.Cancelled, receipt.OutputLimitExceeded,
		receipt.TreeReaped, receipt.RestrictedToken,
		receipt.LowIntegrityToken, receipt.JobAssignedAtCreation,
		receipt.KillOnJobClose, receipt.ActiveProcessLimit,
		receipt.ProcessMemoryLimit, receipt.StdinClosed,
		receipt.EnvironmentInherited, receipt.NetworkRequested,
		receipt.PersistentProcess, receipt.ProductExecutionEnabled); err != nil {
		return ControlledExecutionReceipt{}, false, err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id,
		session_id, status, config_json, budget_json, started_at, finished_at,
		created_at, updated_at FROM runs WHERE id = ?`, intent.RunID))
	if err != nil {
		return ControlledExecutionReceipt{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.ControlledCommandExecutionCompletedEvent,
		"controlled_command_execution", receipt.RequestID, map[string]any{
			"protocol":              receipt.ProtocolVersion,
			"exit_code":             receipt.ExitCode,
			"timed_out":             receipt.TimedOut,
			"cancelled":             receipt.Cancelled,
			"output_limit_exceeded": receipt.OutputLimitExceeded,
			"tree_reaped":           receipt.TreeReaped,
			"stdout_observed_bytes": receipt.StdoutObservedBytes,
			"stderr_observed_bytes": receipt.StderrObservedBytes,
			"raw_output_persisted":  false,
		}); err != nil {
		return ControlledExecutionReceipt{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ControlledExecutionReceipt{}, false, err
	}
	return receipt, false, nil
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

func controlledExecutionIntentMatches(
	left runner.ControlledExecutionIntent,
	right runner.ControlledExecutionIntent,
) bool {
	left.CreatedAt = time.Time{}
	right.CreatedAt = time.Time{}
	return left == right
}

func projectControlledExecutionReceipt(
	result runner.ControlledExecutionResult,
) ControlledExecutionReceipt {
	return ControlledExecutionReceipt{
		RequestID: result.RequestID, ProtocolVersion: result.ProtocolVersion,
		PolicyVersion: result.PolicyVersion, Backend: result.Backend,
		ExitCode:            result.ExitCode,
		StdoutObservedBytes: result.Stdout.ObservedBytes,
		StdoutCapturedBytes: result.Stdout.CapturedBytes,
		StdoutPrefixSHA256:  result.Stdout.CapturedPrefixSHA256,
		StdoutTruncated:     result.Stdout.Truncated,
		StderrObservedBytes: result.Stderr.ObservedBytes,
		StderrCapturedBytes: result.Stderr.CapturedBytes,
		StderrPrefixSHA256:  result.Stderr.CapturedPrefixSHA256,
		StderrTruncated:     result.Stderr.Truncated,
		StartedAt:           result.StartedAt, CompletedAt: result.CompletedAt,
		TimedOut: result.TimedOut, Cancelled: result.Cancelled,
		OutputLimitExceeded:     result.OutputLimitExceeded,
		TreeReaped:              result.TreeReaped,
		RestrictedToken:         result.RestrictedToken,
		LowIntegrityToken:       result.LowIntegrityToken,
		JobAssignedAtCreation:   result.JobAssignedAtCreation,
		KillOnJobClose:          result.KillOnJobClose,
		ActiveProcessLimit:      result.ActiveProcessLimit,
		ProcessMemoryLimit:      result.ProcessMemoryLimit,
		StdinClosed:             result.StdinClosed,
		EnvironmentInherited:    result.EnvironmentInherited,
		NetworkRequested:        result.NetworkRequested,
		PersistentProcess:       result.PersistentProcess,
		ProductExecutionEnabled: result.ProductExecutionEnabled,
	}
}
