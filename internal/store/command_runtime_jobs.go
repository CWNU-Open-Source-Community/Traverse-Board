package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

const commandRuntimeJobColumns = `id, operation_digest, request_fingerprint,
	invocation_id, run_id, mission_id, session_id, workspace_id, root_agent_id,
	workspace_root_sha256, mode_snapshot_id, mode_revision, profile_snapshot_id,
	profile_revision, permission_snapshot_id, permission_revision, permission_mode,
	lease_id, lease_generation, lease_owner_id, owner_id, owner_generation,
	owner_renewed_at, owner_expires_at, intent_json,
	spec_fingerprint, profile, executable_path, executable_sha256,
	environment_sha256, working_directory, stdin_policy, network, credentials,
	timeout_milliseconds, inline_limit_bytes, artifact_limit_bytes, state, pid,
	process_group, stdout, stderr, stdout_observed_bytes, stderr_observed_bytes,
	output_cursor, output_base_cursor, output_frames_json, stdout_sha256,
	stderr_sha256, truncation_reason, exit_code, timed_out, cancelled, killed,
	tree_reaped, job_assigned_at_creation, stdin_closed, stdin_write_count,
	version, created_at, started_at, completed_at, updated_at,
	adapter_kind, adapter_backend, adapter_backend_identity, adapter_generation,
	adapter_isolation_grade, adapter_network_policy, adapter_credential_policy`

type commandRuntimeJobQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type commandRuntimeJobScanner interface {
	Scan(...any) error
}

func (s *SQLiteStore) PrepareCommandRuntimeJob(ctx context.Context,
	job runner.CommandRuntimeJob,
) (runner.CommandRuntimeJob, bool, error) {
	if s == nil || s.db == nil {
		return runner.CommandRuntimeJob{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "command runtime store is unavailable")
	}
	if job.State != runner.CommandRuntimeJobPrepared || job.Version != 1 ||
		job.Validate() != nil {
		return runner.CommandRuntimeJob{}, false, apperror.New(
			apperror.CodeInvalidArgument, "command runtime prepared job is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.CommandRuntimeJob{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := getCommandRuntimeJobByOperation(ctx, tx,
		job.OperationDigest)
	if err != nil {
		return runner.CommandRuntimeJob{}, false, err
	}
	if found {
		if existing.ID != job.ID ||
			existing.RequestFingerprint != job.RequestFingerprint ||
			existing.SpecFingerprint != job.SpecFingerprint ||
			existing.RunID != job.RunID || existing.InvocationID != job.InvocationID ||
			existing.Adapter != job.Adapter {
			return runner.CommandRuntimeJob{}, false, apperror.New(
				apperror.CodeConflict, "command runtime operation key was reused")
		}
		if err := tx.Commit(); err != nil {
			return runner.CommandRuntimeJob{}, false, err
		}
		return existing, true, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO command_runtime_jobs (
		id, protocol_version, operation_digest, request_fingerprint, invocation_id,
		run_id, mission_id, session_id, workspace_id, root_agent_id,
		workspace_root_sha256, mode_snapshot_id, mode_revision, profile_snapshot_id,
		profile_revision, permission_snapshot_id, permission_revision, permission_mode,
		lease_id, lease_generation, lease_owner_id, owner_id, owner_generation,
		owner_renewed_at, owner_expires_at, intent_json,
		spec_fingerprint, profile, executable_path, executable_sha256,
		environment_sha256, working_directory, stdin_policy, network, credentials,
		timeout_milliseconds, inline_limit_bytes, artifact_limit_bytes, state, pid,
		process_group, stdout, stderr, stdout_observed_bytes, stderr_observed_bytes,
		output_cursor, output_base_cursor, output_frames_json, stdout_sha256,
		stderr_sha256, truncation_reason, exit_code, timed_out, cancelled, killed,
		tree_reaped, job_assigned_at_creation, stdin_closed, stdin_write_count,
		version, created_at, started_at, completed_at, updated_at,
		adapter_kind, adapter_backend, adapter_backend_identity, adapter_generation,
		adapter_isolation_grade, adapter_network_policy, adapter_credential_policy)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, runner.CommandRuntimeProtocolVersion, job.OperationDigest,
		job.RequestFingerprint, job.InvocationID, job.RunID, job.MissionID,
		job.SessionID, job.WorkspaceID, job.RootAgentID, job.WorkspaceRootSHA256,
		job.ModeSnapshotID, job.ModeRevision, job.ProfileSnapshotID,
		job.ProfileRevision, job.PermissionSnapshotID, job.PermissionRevision,
		job.PermissionMode, job.LeaseID, job.LeaseGeneration, job.LeaseOwnerID,
		job.OwnerID, job.OwnerGeneration, ts(job.OwnerRenewedAt),
		ts(job.OwnerExpiresAt), job.IntentJSON, job.SpecFingerprint, job.Profile,
		job.ExecutablePath, job.ExecutableSHA256, job.EnvironmentSHA256,
		job.WorkingDirectory, job.StdinPolicy, job.Network, job.Credentials,
		job.TimeoutMilliseconds, job.InlineLimitBytes, job.ArtifactLimitBytes,
		job.State, job.PID, job.ProcessGroup, job.Stdout, job.Stderr,
		job.StdoutObservedBytes, job.StderrObservedBytes, job.OutputCursor,
		job.OutputBaseCursor, job.OutputFramesJSON, job.StdoutSHA256,
		job.StderrSHA256, job.TruncationReason, nullableInt(job.ExitCode),
		boolInt(job.TimedOut), boolInt(job.Cancelled), boolInt(job.Killed),
		boolInt(job.TreeReaped), boolInt(job.JobAssignedAtCreation),
		boolInt(job.StdinClosed), job.StdinWriteCount, job.Version,
		ts(job.CreatedAt), nullableTS(job.StartedAt), nullableTS(job.CompletedAt),
		ts(job.UpdatedAt), job.Adapter.Kind, job.Adapter.Backend,
		job.Adapter.BackendIdentity, job.Adapter.Generation,
		job.Adapter.IsolationGrade, job.Adapter.NetworkPolicy,
		job.Adapter.CredentialPolicy)
	if err != nil {
		return runner.CommandRuntimeJob{}, false, apperror.Wrap(
			apperror.CodeConflict, "command runtime launch scope was rejected", err)
	}
	stored, err := getCommandRuntimeJob(ctx, tx, job.ID)
	if err != nil {
		return runner.CommandRuntimeJob{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runner.CommandRuntimeJob{}, false, err
	}
	return stored, false, nil
}

func (s *SQLiteStore) UpdateCommandRuntimeJob(ctx context.Context,
	job runner.CommandRuntimeJob, expectedVersion int64,
) (runner.CommandRuntimeJob, error) {
	if s == nil || s.db == nil {
		return runner.CommandRuntimeJob{}, apperror.New(
			apperror.CodeFailedPrecondition, "command runtime store is unavailable")
	}
	if expectedVersion <= 0 || job.Version != expectedVersion+1 ||
		job.Validate() != nil {
		return runner.CommandRuntimeJob{}, apperror.New(
			apperror.CodeInvalidArgument, "command runtime transition is invalid")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE command_runtime_jobs SET
		state = ?, pid = ?, process_group = ?, stdout = ?, stderr = ?,
		stdout_observed_bytes = ?, stderr_observed_bytes = ?, output_cursor = ?,
		output_base_cursor = ?, output_frames_json = ?, stdout_sha256 = ?,
		stderr_sha256 = ?, truncation_reason = ?, exit_code = ?, timed_out = ?,
		cancelled = ?, killed = ?, tree_reaped = ?, job_assigned_at_creation = ?,
		stdin_closed = ?, stdin_write_count = ?, owner_renewed_at = ?,
		owner_expires_at = ?, version = ?, started_at = ?, completed_at = ?,
		updated_at = ? WHERE id = ? AND version = ?
			AND adapter_kind = ? AND adapter_backend = ?
			AND adapter_backend_identity = ? AND adapter_generation = ?
			AND adapter_isolation_grade = ? AND adapter_network_policy = ?
			AND adapter_credential_policy = ?`,
		job.State, job.PID, job.ProcessGroup, job.Stdout, job.Stderr,
		job.StdoutObservedBytes, job.StderrObservedBytes, job.OutputCursor,
		job.OutputBaseCursor, job.OutputFramesJSON, job.StdoutSHA256,
		job.StderrSHA256, job.TruncationReason, nullableInt(job.ExitCode),
		boolInt(job.TimedOut), boolInt(job.Cancelled), boolInt(job.Killed),
		boolInt(job.TreeReaped), boolInt(job.JobAssignedAtCreation),
		boolInt(job.StdinClosed), job.StdinWriteCount, ts(job.OwnerRenewedAt),
		ts(job.OwnerExpiresAt), job.Version,
		nullableTS(job.StartedAt), nullableTS(job.CompletedAt), ts(job.UpdatedAt),
		job.ID, expectedVersion, job.Adapter.Kind, job.Adapter.Backend,
		job.Adapter.BackendIdentity, job.Adapter.Generation,
		job.Adapter.IsolationGrade, job.Adapter.NetworkPolicy,
		job.Adapter.CredentialPolicy)
	if err != nil {
		return runner.CommandRuntimeJob{}, apperror.Wrap(
			apperror.CodeConflict, "command runtime transition was rejected", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return runner.CommandRuntimeJob{}, err
	}
	if changed != 1 {
		if _, getErr := s.GetCommandRuntimeJob(ctx, job.ID); getErr != nil {
			return runner.CommandRuntimeJob{}, getErr
		}
		return runner.CommandRuntimeJob{}, apperror.New(
			apperror.CodeConflict, "command runtime record version changed")
	}
	return s.GetCommandRuntimeJob(ctx, job.ID)
}

func (s *SQLiteStore) GetCommandRuntimeJob(ctx context.Context,
	jobID string,
) (runner.CommandRuntimeJob, error) {
	jobID = strings.TrimSpace(jobID)
	if s == nil || s.db == nil || !domain.ValidAgentID(jobID) {
		return runner.CommandRuntimeJob{}, apperror.New(
			apperror.CodeInvalidArgument, "command runtime job id is invalid")
	}
	return getCommandRuntimeJob(ctx, s.db, jobID)
}

func (s *SQLiteStore) ListCommandRuntimeJobs(ctx context.Context,
	filter runner.CommandRuntimeListFilter,
) ([]runner.CommandRuntimeJob, error) {
	if s == nil || s.db == nil || filter.Limit < 0 || filter.Limit > 500 ||
		(filter.RunID != "" && (!domain.ValidAgentID(filter.RunID) ||
			strings.TrimSpace(filter.RunID) != filter.RunID)) {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"command runtime job filter is invalid")
	}
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	query := `SELECT ` + commandRuntimeJobColumns + ` FROM command_runtime_jobs WHERE 1 = 1`
	arguments := make([]any, 0, 2)
	if filter.RunID != "" {
		query += ` AND run_id = ?`
		arguments = append(arguments, filter.RunID)
	}
	if filter.ActiveOnly {
		query += ` AND state IN ('prepared', 'running', 'stopping')`
	}
	query += ` ORDER BY created_at DESC, id LIMIT ?`
	arguments = append(arguments, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]runner.CommandRuntimeJob, 0, filter.Limit)
	for rows.Next() {
		job, err := scanCommandRuntimeJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) CommandRuntimeJobOwnershipActive(ctx context.Context,
	job runner.CommandRuntimeJob,
) (bool, error) {
	if s == nil || s.db == nil || job.Validate() != nil {
		return false, apperror.New(apperror.CodeInvalidArgument,
			"command runtime ownership query is invalid")
	}
	if job.Adapter.Kind == commandruntimeadapter.KindLegacyUnbound {
		return false, nil
	}
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM command_runtime_jobs
		WHERE id = ? AND owner_id = ? AND owner_generation = ?
			AND adapter_kind = ? AND adapter_backend = ?
			AND adapter_backend_identity = ? AND adapter_generation = ?
			AND adapter_isolation_grade = ? AND adapter_network_policy = ?
			AND adapter_credential_policy = ?
			AND state IN ('prepared', 'running', 'stopping')
			AND julianday(owner_expires_at) > julianday('now')
	)`, job.ID, job.OwnerID, job.OwnerGeneration, job.Adapter.Kind,
		job.Adapter.Backend, job.Adapter.BackendIdentity, job.Adapter.Generation,
		job.Adapter.IsolationGrade, job.Adapter.NetworkPolicy,
		job.Adapter.CredentialPolicy).Scan(&active)
	if err != nil {
		return false, err
	}
	return active == 1, nil
}

func getCommandRuntimeJob(ctx context.Context, queryer commandRuntimeJobQueryer,
	jobID string,
) (runner.CommandRuntimeJob, error) {
	job, err := scanCommandRuntimeJob(queryer.QueryRowContext(ctx,
		`SELECT `+commandRuntimeJobColumns+` FROM command_runtime_jobs WHERE id = ?`,
		jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return runner.CommandRuntimeJob{}, apperror.New(
			apperror.CodeNotFound, "command runtime job was not found")
	}
	return job, err
}

func getCommandRuntimeJobByOperation(ctx context.Context,
	queryer commandRuntimeJobQueryer, digest string,
) (runner.CommandRuntimeJob, bool, error) {
	job, err := scanCommandRuntimeJob(queryer.QueryRowContext(ctx,
		`SELECT `+commandRuntimeJobColumns+
			` FROM command_runtime_jobs WHERE operation_digest = ?`, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return runner.CommandRuntimeJob{}, false, nil
	}
	return job, err == nil, err
}

func scanCommandRuntimeJob(scanner commandRuntimeJobScanner) (
	runner.CommandRuntimeJob, error,
) {
	var job runner.CommandRuntimeJob
	var permissionMode, profile, stdinPolicy, network, credentials, state string
	var exitCode sql.NullInt64
	var timedOut, cancelled, killed, treeReaped, assigned, stdinClosed int
	var ownerRenewedAt, ownerExpiresAt sql.NullString
	var createdAt, startedAt, completedAt, updatedAt sql.NullString
	var adapterKind, adapterIsolation, adapterNetwork, adapterCredentials string
	err := scanner.Scan(&job.ID, &job.OperationDigest, &job.RequestFingerprint,
		&job.InvocationID, &job.RunID, &job.MissionID, &job.SessionID,
		&job.WorkspaceID, &job.RootAgentID, &job.WorkspaceRootSHA256,
		&job.ModeSnapshotID, &job.ModeRevision, &job.ProfileSnapshotID,
		&job.ProfileRevision, &job.PermissionSnapshotID, &job.PermissionRevision,
		&permissionMode, &job.LeaseID, &job.LeaseGeneration, &job.LeaseOwnerID,
		&job.OwnerID, &job.OwnerGeneration, &ownerRenewedAt,
		&ownerExpiresAt, &job.IntentJSON, &job.SpecFingerprint, &profile,
		&job.ExecutablePath, &job.ExecutableSHA256, &job.EnvironmentSHA256,
		&job.WorkingDirectory, &stdinPolicy, &network, &credentials,
		&job.TimeoutMilliseconds, &job.InlineLimitBytes, &job.ArtifactLimitBytes,
		&state, &job.PID, &job.ProcessGroup, &job.Stdout, &job.Stderr,
		&job.StdoutObservedBytes, &job.StderrObservedBytes, &job.OutputCursor,
		&job.OutputBaseCursor, &job.OutputFramesJSON, &job.StdoutSHA256,
		&job.StderrSHA256, &job.TruncationReason, &exitCode, &timedOut,
		&cancelled, &killed, &treeReaped, &assigned, &stdinClosed,
		&job.StdinWriteCount, &job.Version, &createdAt, &startedAt,
		&completedAt, &updatedAt, &adapterKind, &job.Adapter.Backend,
		&job.Adapter.BackendIdentity, &job.Adapter.Generation, &adapterIsolation,
		&adapterNetwork, &adapterCredentials)
	if err != nil {
		return runner.CommandRuntimeJob{}, err
	}
	job.PermissionMode = domain.RunExecutionPermissionMode(permissionMode)
	job.Profile = runner.CommandRuntimeProfile(profile)
	job.StdinPolicy = runner.CommandRuntimeStdinPolicy(stdinPolicy)
	job.Network = runner.CommandRuntimeNetwork(network)
	job.Credentials = runner.CommandRuntimeCredentialPolicy(credentials)
	job.State = runner.CommandRuntimeJobState(state)
	job.Adapter.Kind = commandruntimeadapter.Kind(adapterKind)
	job.Adapter.IsolationGrade = commandruntimeadapter.IsolationGrade(adapterIsolation)
	job.Adapter.NetworkPolicy = commandruntimeadapter.NetworkPolicy(adapterNetwork)
	job.Adapter.CredentialPolicy = commandruntimeadapter.CredentialPolicy(adapterCredentials)
	if exitCode.Valid {
		value := int(exitCode.Int64)
		job.ExitCode = &value
	}
	job.TimedOut = timedOut == 1
	job.Cancelled = cancelled == 1
	job.Killed = killed == 1
	job.TreeReaped = treeReaped == 1
	job.JobAssignedAtCreation = assigned == 1
	job.StdinClosed = stdinClosed == 1
	job.OwnerRenewedAt = parseTS(ownerRenewedAt.String)
	job.OwnerExpiresAt = parseTS(ownerExpiresAt.String)
	job.CreatedAt = parseTS(createdAt.String)
	job.StartedAt = parseNullableTS(startedAt)
	job.CompletedAt = parseNullableTS(completedAt)
	job.UpdatedAt = parseTS(updatedAt.String)
	if err := job.Validate(); err != nil {
		return runner.CommandRuntimeJob{}, fmt.Errorf(
			"invalid persisted command runtime job %q: %w", job.ID, err)
	}
	return job, nil
}

var _ runner.CommandRuntimeStore = (*SQLiteStore)(nil)
var _ runner.CommandRuntimeOwnershipStore = (*SQLiteStore)(nil)
