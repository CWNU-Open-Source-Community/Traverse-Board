package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

// GetThreadSupervisorToolCall reads one durable Supervisor call only through
// the owning Thread binding. A valid call identity from another Thread is
// intentionally indistinguishable from an unknown identity at this boundary.
func (s *SQLiteStore) GetThreadSupervisorToolCall(ctx context.Context,
	threadID, callID string,
) (domain.SupervisorToolCall, error) {
	threadID = strings.TrimSpace(threadID)
	callID = strings.TrimSpace(callID)
	if s == nil || s.db == nil || !domain.ValidAgentID(threadID) ||
		!domain.ValidAgentID(callID) {
		return domain.SupervisorToolCall{}, apperror.New(
			apperror.CodeInvalidArgument, "Thread activity identity is invalid")
	}
	call, err := scanSupervisorToolCall(s.db.QueryRowContext(ctx, `SELECT
		call.run_id, call.turn, call.attempt_id, call.round, call.position,
		call.model_attempt, call.call_id, call.stream_response_id,
		call.stream_item_id, call.stream_call_id, call.tool_name,
		call.payload_json, call.authority_json, call.status, call.result_json,
		call.error_code, call.created_at, call.completed_at
		FROM thread_runs binding
		JOIN run_supervisor_tool_calls call ON call.run_id = binding.run_id
		WHERE binding.thread_id = ? AND call.call_id = ?
		ORDER BY binding.ordinal DESC LIMIT 1`, threadID, callID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SupervisorToolCall{}, apperror.New(
			apperror.CodeNotFound, "Thread activity was not found")
	}
	if err != nil {
		return domain.SupervisorToolCall{}, err
	}
	if err := s.loadSupervisorToolAgentAttribution(ctx, &call); err != nil {
		return domain.SupervisorToolCall{}, err
	}
	return call, nil
}

// GetThreadCommandRuntimeJob applies the same Thread ownership boundary to a
// command Job before its sanitized durable output can enter a public view.
func (s *SQLiteStore) GetThreadCommandRuntimeJob(ctx context.Context,
	threadID, jobID string,
) (runner.CommandRuntimeJob, error) {
	threadID = strings.TrimSpace(threadID)
	jobID = strings.TrimSpace(jobID)
	if s == nil || s.db == nil || !domain.ValidAgentID(threadID) ||
		!domain.ValidAgentID(jobID) {
		return runner.CommandRuntimeJob{}, apperror.New(
			apperror.CodeInvalidArgument, "Thread command activity identity is invalid")
	}
	job, err := scanCommandRuntimeJob(s.db.QueryRowContext(ctx,
		`SELECT `+commandRuntimeJobColumns+` FROM command_runtime_jobs job
		WHERE job.id = ? AND EXISTS (
			SELECT 1 FROM thread_runs binding
			WHERE binding.thread_id = ? AND binding.run_id = job.run_id
		) LIMIT 1`, jobID, threadID))
	if errors.Is(err, sql.ErrNoRows) {
		return runner.CommandRuntimeJob{}, apperror.New(
			apperror.CodeNotFound, "Thread command activity was not found")
	}
	return job, err
}

// GetThreadCommandRuntimeJobMetadata is the metadata-only companion used by
// transcript summaries. In particular, its SELECT never reads intent_json,
// stdout, stderr, output_frames_json, environment fields or process identity.
func (s *SQLiteStore) GetThreadCommandRuntimeJobMetadata(ctx context.Context,
	threadID, jobID string,
) (runner.CommandRuntimeJobMetadata, error) {
	threadID = strings.TrimSpace(threadID)
	jobID = strings.TrimSpace(jobID)
	if s == nil || s.db == nil || !domain.ValidAgentID(threadID) ||
		!domain.ValidAgentID(jobID) {
		return runner.CommandRuntimeJobMetadata{}, apperror.New(
			apperror.CodeInvalidArgument, "Thread command activity identity is invalid")
	}
	var value runner.CommandRuntimeJobMetadata
	var state string
	var exitCode sql.NullInt64
	var startedAt, completedAt sql.NullString
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT job.id, job.operation_digest, job.run_id,
		job.working_directory, job.state, job.exit_code, job.started_at,
		job.completed_at, job.updated_at
		FROM command_runtime_jobs job
		WHERE job.id = ? AND EXISTS (
			SELECT 1 FROM thread_runs binding
			WHERE binding.thread_id = ? AND binding.run_id = job.run_id
		) LIMIT 1`, jobID, threadID).Scan(&value.ID, &value.OperationDigest,
		&value.RunID, &value.WorkingDirectory, &state, &exitCode, &startedAt,
		&completedAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.CommandRuntimeJobMetadata{}, apperror.New(
			apperror.CodeNotFound, "Thread command activity was not found")
	}
	if err != nil {
		return runner.CommandRuntimeJobMetadata{}, err
	}
	value.State = runner.CommandRuntimeJobState(state)
	if exitCode.Valid {
		projected := int(exitCode.Int64)
		value.ExitCode = &projected
	}
	value.StartedAt = parseNullableTS(startedAt)
	value.CompletedAt = parseNullableTS(completedAt)
	value.UpdatedAt = parseTS(updatedAt)
	if err := value.Validate(); err != nil {
		return runner.CommandRuntimeJobMetadata{}, err
	}
	return value, nil
}
