package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/sandbox"
)

// DockerContainerIOStore is the durable boundary of the container I/O
// contract: input projections, log capture receipts, staging receipts, and
// atomic output commit receipts. Raw container bytes never reach this store.
type DockerContainerIOStore interface {
	InsertDockerInputProjection(ctx context.Context,
		projection sandbox.DockerInputProjection) (bool, error)
	InsertDockerLogCaptureReceipt(ctx context.Context,
		receipt sandbox.DockerLogCaptureReceipt) (bool, error)
	GetDockerLogCaptureReceiptByAttempt(ctx context.Context,
		attemptID string) (sandbox.DockerLogCaptureReceipt, bool, error)
	InsertDockerOutputStagingReceipt(ctx context.Context,
		receipt sandbox.DockerOutputStagingReceipt) (bool, error)
	GetDockerOutputStagingReceiptByAttempt(ctx context.Context,
		attemptID string) (sandbox.DockerOutputStagingReceipt, bool, error)
	CommitDockerOutputs(ctx context.Context, request sandbox.DockerOutputCommitRequest,
		receipt sandbox.DockerOutputCommitReceipt) (bool, error)
	GetDockerOutputCommitReceiptByAttempt(ctx context.Context,
		attemptID string) (sandbox.DockerOutputCommitReceipt, bool, error)
}

const insertDockerInputProjectionSQL = "INSERT INTO sandbox_docker_input_projections (id, attempt_id, generation, plan_id, observation_id, run_id, mission_id, workspace_id, protocol_version, input_artifact_digest, spec_fingerprint, authority_fingerprint, mount_target, mount_read_only, entry_count, total_bytes, projection_fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)"

func (s *SQLiteStore) InsertDockerInputProjection(ctx context.Context,
	projection sandbox.DockerInputProjection,
) (bool, error) {
	if projection.Validate() != nil {
		return false, apperror.New(apperror.CodeInvalidArgument, "docker input projection is invalid")
	}
	result, err := s.db.ExecContext(ctx, insertDockerInputProjectionSQL,
		projection.ID, projection.AttemptID, projection.Generation, projection.PlanID,
		projection.ObservationID, projection.RunID, projection.MissionID,
		projection.WorkspaceID, projection.ProtocolVersion, projection.InputArtifactDigest,
		projection.SpecFingerprint, projection.AuthorityFingerprint, projection.MountTarget,
		projection.EntryCount, projection.TotalBytes, projection.ProjectionFingerprint,
		ts(projection.CreatedAt))
	if err != nil {
		if isUniqueConstraintError(err) {
			return false, nil
		}
		return false, err
	}
	count, err := result.RowsAffected()
	return err == nil && count == 1, err
}

const insertDockerLogCaptureReceiptSQL = "INSERT INTO sandbox_docker_log_capture_receipts (id, attempt_id, generation, run_id, container_id_fingerprint, protocol_version, status, stdout_bytes, stdout_lines, stdout_truncated_bytes, stdout_truncated_lines, stdout_truncated_deadline, stdout_utf8_violations, stdout_redacted_segments, stdout_content_digest, stderr_bytes, stderr_lines, stderr_truncated_bytes, stderr_truncated_lines, stderr_truncated_deadline, stderr_utf8_violations, stderr_redacted_segments, stderr_content_digest, capture_max_bytes, capture_max_lines, capture_fingerprint, receipt_fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

func (s *SQLiteStore) InsertDockerLogCaptureReceipt(ctx context.Context,
	receipt sandbox.DockerLogCaptureReceipt,
) (bool, error) {
	if receipt.Validate() != nil || len(receipt.Streams) != 2 {
		return false, apperror.New(apperror.CodeInvalidArgument, "docker log capture receipt is invalid")
	}
	stdout := receipt.Streams[0]
	stderr := receipt.Streams[1]
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireSandboxManifestWriteLock(ctx, tx, receipt.RunID); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, insertDockerLogCaptureReceiptSQL,
		receipt.ID, receipt.AttemptID, receipt.Generation, receipt.RunID,
		receipt.ContainerIDFingerprint, receipt.ProtocolVersion, receipt.Status,
		stdout.ByteCount, stdout.LineCount, storeBool(stdout.TruncatedBytes),
		storeBool(stdout.TruncatedLines), storeBool(stdout.TruncatedDeadline),
		stdout.UTF8Violations, stdout.RedactedSegments, stdout.ContentDigest,
		stderr.ByteCount, stderr.LineCount, storeBool(stderr.TruncatedBytes),
		storeBool(stderr.TruncatedLines), storeBool(stderr.TruncatedDeadline),
		stderr.UTF8Violations, stderr.RedactedSegments, stderr.ContentDigest,
		receipt.CaptureMaxBytes, receipt.CaptureMaxLines, receipt.CaptureFingerprint,
		receipt.ReceiptFingerprint, ts(receipt.CreatedAt))
	if err != nil {
		if isUniqueConstraintError(err) {
			return false, nil
		}
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, apperror.New(apperror.CodeFailedPrecondition,
			"docker log capture receipt was not durably inserted")
	}
	truncated := stdout.TruncatedBytes || stdout.TruncatedLines || stdout.TruncatedDeadline ||
		stderr.TruncatedBytes || stderr.TruncatedLines || stderr.TruncatedDeadline
	if err := appendDockerContainerIOEvent(ctx, tx, receipt.RunID, receipt.ID,
		events.SandboxDockerLogCaptureCompletedEvent, receipt.CreatedAt, map[string]any{
			"status": receipt.Status, "stream_count": receipt.StreamCount,
			"total_bytes": receipt.TotalBytes, "total_lines": receipt.TotalLines,
			"truncated": truncated, "utf8_violation_count": receipt.UTF8Violations,
			"redacted_segment_count": receipt.RedactedSegments,
		}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

const selectDockerLogCaptureReceiptByAttemptSQL = `SELECT id, attempt_id, generation,
	run_id, container_id_fingerprint, protocol_version, status,
	stdout_bytes, stdout_lines, stdout_truncated_bytes, stdout_truncated_lines,
	stdout_truncated_deadline, stdout_utf8_violations, stdout_redacted_segments,
	stdout_content_digest, stderr_bytes, stderr_lines, stderr_truncated_bytes,
	stderr_truncated_lines, stderr_truncated_deadline, stderr_utf8_violations,
	stderr_redacted_segments, stderr_content_digest, capture_max_bytes,
	capture_max_lines, capture_fingerprint, receipt_fingerprint, created_at
	FROM sandbox_docker_log_capture_receipts WHERE attempt_id = ? ORDER BY id LIMIT 2`

func (s *SQLiteStore) GetDockerLogCaptureReceiptByAttempt(ctx context.Context,
	attemptID string,
) (sandbox.DockerLogCaptureReceipt, bool, error) {
	if strings.TrimSpace(attemptID) == "" {
		return sandbox.DockerLogCaptureReceipt{}, false,
			apperror.New(apperror.CodeInvalidArgument, "docker log capture attempt id is required")
	}
	rows, err := s.db.QueryContext(ctx, selectDockerLogCaptureReceiptByAttemptSQL, attemptID)
	if err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	defer rows.Close()
	var result sandbox.DockerLogCaptureReceipt
	found := false
	for rows.Next() {
		if found {
			return sandbox.DockerLogCaptureReceipt{}, false, apperror.New(
				apperror.CodeFailedPrecondition,
				"docker log capture attempt has multiple durable receipts")
		}
		var stdout, stderr sandbox.DockerLogStreamRecord
		stdout.Stream, stderr.Stream = "stdout", "stderr"
		var stdoutTruncatedBytes, stdoutTruncatedLines, stdoutTruncatedDeadline int
		var stderrTruncatedBytes, stderrTruncatedLines, stderrTruncatedDeadline int
		var createdAt string
		if err := rows.Scan(&result.ID, &result.AttemptID, &result.Generation,
			&result.RunID, &result.ContainerIDFingerprint, &result.ProtocolVersion,
			&result.Status, &stdout.ByteCount, &stdout.LineCount,
			&stdoutTruncatedBytes, &stdoutTruncatedLines, &stdoutTruncatedDeadline,
			&stdout.UTF8Violations, &stdout.RedactedSegments, &stdout.ContentDigest,
			&stderr.ByteCount, &stderr.LineCount, &stderrTruncatedBytes,
			&stderrTruncatedLines, &stderrTruncatedDeadline,
			&stderr.UTF8Violations, &stderr.RedactedSegments, &stderr.ContentDigest,
			&result.CaptureMaxBytes, &result.CaptureMaxLines,
			&result.CaptureFingerprint, &result.ReceiptFingerprint,
			&createdAt); err != nil {
			return sandbox.DockerLogCaptureReceipt{}, false, err
		}
		if !validStoreBools(stdoutTruncatedBytes, stdoutTruncatedLines,
			stdoutTruncatedDeadline, stderrTruncatedBytes, stderrTruncatedLines,
			stderrTruncatedDeadline) {
			return sandbox.DockerLogCaptureReceipt{}, false, apperror.New(
				apperror.CodeFailedPrecondition, "docker log capture receipt flags are invalid")
		}
		stdout.TruncatedBytes, stdout.TruncatedLines, stdout.TruncatedDeadline =
			stdoutTruncatedBytes != 0, stdoutTruncatedLines != 0, stdoutTruncatedDeadline != 0
		stderr.TruncatedBytes, stderr.TruncatedLines, stderr.TruncatedDeadline =
			stderrTruncatedBytes != 0, stderrTruncatedLines != 0, stderrTruncatedDeadline != 0
		result.Streams = []sandbox.DockerLogStreamRecord{stdout, stderr}
		result.StreamCount = len(result.Streams)
		result.TotalBytes = stdout.ByteCount + stderr.ByteCount
		result.TotalLines = stdout.LineCount + stderr.LineCount
		result.UTF8Violations = stdout.UTF8Violations + stderr.UTF8Violations
		result.RedactedSegments = stdout.RedactedSegments + stderr.RedactedSegments
		result.CreatedAt = parseTS(createdAt)
		found = true
	}
	if err := rows.Err(); err != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, err
	}
	if !found {
		return sandbox.DockerLogCaptureReceipt{}, false, nil
	}
	if result.Validate() != nil {
		return sandbox.DockerLogCaptureReceipt{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "durable docker log capture receipt is invalid")
	}
	return result, true, nil
}

const insertDockerOutputStagingReceiptSQL = "INSERT INTO sandbox_docker_output_staging_receipts (id, attempt_id, generation, run_id, container_id_fingerprint, protocol_version, status, file_count, total_bytes, redacted_count, entry_digest_set, export_fingerprint, receipt_fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

const insertDockerOutputStagingEntrySQL = "INSERT INTO sandbox_docker_output_staging_entries (receipt_id, ordinal, path, sha256, size_bytes, media_type, redacted) VALUES (?, ?, ?, ?, ?, ?, ?)"

func (s *SQLiteStore) InsertDockerOutputStagingReceipt(ctx context.Context,
	receipt sandbox.DockerOutputStagingReceipt,
) (bool, error) {
	if receipt.Validate() != nil {
		return false, apperror.New(apperror.CodeInvalidArgument, "docker output staging receipt is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireSandboxManifestWriteLock(ctx, tx, receipt.RunID); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, insertDockerOutputStagingReceiptSQL,
		receipt.ID, receipt.AttemptID, receipt.Generation, receipt.RunID,
		receipt.ContainerIDFingerprint, receipt.ProtocolVersion, receipt.Status,
		receipt.FileCount, receipt.TotalBytes, receipt.RedactedCount,
		receipt.EntryDigestSet, receipt.ExportFingerprint, receipt.ReceiptFingerprint,
		ts(receipt.CreatedAt))
	if err != nil {
		if isUniqueConstraintError(err) {
			return false, nil
		}
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, apperror.New(apperror.CodeFailedPrecondition,
			"docker output staging receipt was not durably inserted")
	}
	for index, entry := range receipt.Entries {
		if _, err := tx.ExecContext(ctx, insertDockerOutputStagingEntrySQL,
			receipt.ID, index+1, entry.Path, entry.SHA256, entry.SizeBytes,
			entry.MediaType, storeBool(entry.Redacted)); err != nil {
			return false, err
		}
	}
	if err := appendDockerContainerIOEvent(ctx, tx, receipt.RunID, receipt.ID,
		events.SandboxDockerOutputStagingCompletedEvent, receipt.CreatedAt, map[string]any{
			"status": receipt.Status, "file_count": receipt.FileCount,
			"total_bytes": receipt.TotalBytes, "redacted_count": receipt.RedactedCount,
			"truncated": receipt.Status == sandbox.DockerOutputStagingStatusTruncated,
		}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

const selectDockerOutputStagingReceiptByAttemptSQL = `SELECT id, attempt_id,
	generation, run_id, container_id_fingerprint, protocol_version, status,
	file_count, total_bytes, redacted_count, entry_digest_set, export_fingerprint,
	receipt_fingerprint, created_at FROM sandbox_docker_output_staging_receipts
	WHERE attempt_id = ? ORDER BY id LIMIT 2`

const selectDockerOutputStagingEntriesSQL = `SELECT ordinal, path, sha256,
	size_bytes, media_type, redacted FROM sandbox_docker_output_staging_entries
	WHERE receipt_id = ? ORDER BY ordinal`

func (s *SQLiteStore) GetDockerOutputStagingReceiptByAttempt(ctx context.Context,
	attemptID string,
) (sandbox.DockerOutputStagingReceipt, bool, error) {
	if strings.TrimSpace(attemptID) == "" {
		return sandbox.DockerOutputStagingReceipt{}, false,
			apperror.New(apperror.CodeInvalidArgument, "docker output staging attempt id is required")
	}
	rows, err := s.db.QueryContext(ctx, selectDockerOutputStagingReceiptByAttemptSQL, attemptID)
	if err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	defer rows.Close()
	var result sandbox.DockerOutputStagingReceipt
	found := false
	var createdAt string
	for rows.Next() {
		if found {
			return sandbox.DockerOutputStagingReceipt{}, false, apperror.New(
				apperror.CodeFailedPrecondition,
				"docker output staging attempt has multiple durable receipts")
		}
		if err := rows.Scan(&result.ID, &result.AttemptID, &result.Generation,
			&result.RunID, &result.ContainerIDFingerprint, &result.ProtocolVersion,
			&result.Status, &result.FileCount, &result.TotalBytes,
			&result.RedactedCount, &result.EntryDigestSet,
			&result.ExportFingerprint, &result.ReceiptFingerprint,
			&createdAt); err != nil {
			return sandbox.DockerOutputStagingReceipt{}, false, err
		}
		found = true
	}
	if err := rows.Err(); err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	if !found {
		return sandbox.DockerOutputStagingReceipt{}, false, nil
	}
	if err := rows.Close(); err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	result.CreatedAt = parseTS(createdAt)
	entryRows, err := s.db.QueryContext(ctx, selectDockerOutputStagingEntriesSQL, result.ID)
	if err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	defer entryRows.Close()
	for entryRows.Next() {
		var ordinal, redacted int
		var entry sandbox.DockerStagedOutputEntry
		if err := entryRows.Scan(&ordinal, &entry.Path, &entry.SHA256,
			&entry.SizeBytes, &entry.MediaType, &redacted); err != nil {
			return sandbox.DockerOutputStagingReceipt{}, false, err
		}
		if ordinal != len(result.Entries)+1 || !validStoreBools(redacted) {
			return sandbox.DockerOutputStagingReceipt{}, false, apperror.New(
				apperror.CodeFailedPrecondition,
				"durable docker output staging entry sequence is invalid")
		}
		entry.Redacted = redacted != 0
		result.Entries = append(result.Entries, entry)
	}
	if err := entryRows.Err(); err != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, err
	}
	if result.Validate() != nil {
		return sandbox.DockerOutputStagingReceipt{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "durable docker output staging receipt is invalid")
	}
	return result, true, nil
}

const insertDockerOutputCommitReceiptSQL = "INSERT INTO sandbox_docker_output_commit_receipts (id, attempt_id, generation, run_id, workspace_id, operation_key_digest, request_fingerprint, protocol_version, status, committed_count, committed_digest_set, receipt_fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

const insertDockerOutputCommitEntrySQL = "INSERT INTO sandbox_docker_output_commit_entries (receipt_id, ordinal, path, sha256, size_bytes, media_type) VALUES (?, ?, ?, ?, ?, ?)"

const selectDockerOutputCommitByKeySQL = "SELECT id FROM sandbox_docker_output_commit_receipts WHERE operation_key_digest = ?"

const selectDockerOutputCommitByAttemptSQL = "SELECT id FROM sandbox_docker_output_commit_receipts WHERE attempt_id = ? LIMIT 1"

// CommitDockerOutputs atomically inserts the commit receipt and every accepted
// entry in one transaction. A conflicting operation key digest replays the
// existing receipt instead; a failure leaves no partial rows.
func (s *SQLiteStore) CommitDockerOutputs(ctx context.Context,
	request sandbox.DockerOutputCommitRequest, receipt sandbox.DockerOutputCommitReceipt,
) (bool, error) {
	if request.Validate() != nil || receipt.Validate() != nil ||
		receipt.RequestFingerprint != request.RequestFingerprint ||
		receipt.OperationKeyDigest != request.OperationKeyDigest ||
		len(receipt.Entries) != len(request.AcceptedEntries) {
		return false, apperror.New(apperror.CodeInvalidArgument, "docker output commit request or receipt is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireSandboxManifestWriteLock(ctx, tx, receipt.RunID); err != nil {
		return false, err
	}
	var existingID string
	err = tx.QueryRowContext(ctx, selectDockerOutputCommitByKeySQL,
		request.OperationKeyDigest).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil {
		return false, nil
	}
	err = tx.QueryRowContext(ctx, selectDockerOutputCommitByAttemptSQL,
		request.AttemptID).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, insertDockerOutputCommitReceiptSQL,
		receipt.ID, receipt.AttemptID, receipt.Generation, receipt.RunID,
		receipt.WorkspaceID, receipt.OperationKeyDigest, receipt.RequestFingerprint,
		receipt.ProtocolVersion, receipt.Status, receipt.CommittedCount,
		receipt.CommittedDigestSet, receipt.ReceiptFingerprint, ts(receipt.CreatedAt)); err != nil {
		if isUniqueConstraintError(err) {
			return false, nil
		}
		return false, err
	}
	for index, entry := range receipt.Entries {
		if _, err := tx.ExecContext(ctx, insertDockerOutputCommitEntrySQL,
			receipt.ID, index+1, entry.Path, entry.SHA256, entry.SizeBytes,
			entry.MediaType); err != nil {
			return false, err
		}
	}
	var totalBytes int64
	for _, entry := range receipt.Entries {
		totalBytes += entry.SizeBytes
	}
	if err := appendDockerContainerIOEvent(ctx, tx, receipt.RunID, receipt.ID,
		events.SandboxDockerOutputCommitCompletedEvent, receipt.CreatedAt, map[string]any{
			"status": receipt.Status, "committed_count": receipt.CommittedCount,
			"total_bytes": totalBytes,
		}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

const selectDockerOutputCommitReceiptByAttemptSQL = `SELECT id, attempt_id,
	generation, run_id, workspace_id, operation_key_digest, request_fingerprint,
	protocol_version, status, committed_count, committed_digest_set,
	receipt_fingerprint, created_at FROM sandbox_docker_output_commit_receipts
	WHERE attempt_id = ? ORDER BY id LIMIT 2`

const selectDockerOutputCommitEntriesSQL = `SELECT ordinal, path, sha256,
	size_bytes, media_type FROM sandbox_docker_output_commit_entries
	WHERE receipt_id = ? ORDER BY ordinal`

func (s *SQLiteStore) GetDockerOutputCommitReceiptByAttempt(ctx context.Context,
	attemptID string,
) (sandbox.DockerOutputCommitReceipt, bool, error) {
	if strings.TrimSpace(attemptID) == "" {
		return sandbox.DockerOutputCommitReceipt{}, false,
			apperror.New(apperror.CodeInvalidArgument, "docker output commit attempt id is required")
	}
	rows, err := s.db.QueryContext(ctx, selectDockerOutputCommitReceiptByAttemptSQL, attemptID)
	if err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	defer rows.Close()
	var result sandbox.DockerOutputCommitReceipt
	found := false
	var createdAt string
	for rows.Next() {
		if found {
			return sandbox.DockerOutputCommitReceipt{}, false, apperror.New(
				apperror.CodeFailedPrecondition,
				"docker output commit attempt has multiple durable receipts")
		}
		if err := rows.Scan(&result.ID, &result.AttemptID, &result.Generation,
			&result.RunID, &result.WorkspaceID, &result.OperationKeyDigest,
			&result.RequestFingerprint, &result.ProtocolVersion, &result.Status,
			&result.CommittedCount, &result.CommittedDigestSet,
			&result.ReceiptFingerprint, &createdAt); err != nil {
			return sandbox.DockerOutputCommitReceipt{}, false, err
		}
		found = true
	}
	if err := rows.Err(); err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	if !found {
		return sandbox.DockerOutputCommitReceipt{}, false, nil
	}
	if err := rows.Close(); err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	result.CreatedAt = parseTS(createdAt)
	entryRows, err := s.db.QueryContext(ctx, selectDockerOutputCommitEntriesSQL, result.ID)
	if err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	defer entryRows.Close()
	for entryRows.Next() {
		var ordinal int
		var entry sandbox.DockerOutputCommitEntry
		if err := entryRows.Scan(&ordinal, &entry.Path, &entry.SHA256,
			&entry.SizeBytes, &entry.MediaType); err != nil {
			return sandbox.DockerOutputCommitReceipt{}, false, err
		}
		if ordinal != len(result.Entries)+1 {
			return sandbox.DockerOutputCommitReceipt{}, false, apperror.New(
				apperror.CodeFailedPrecondition,
				"durable docker output commit entry sequence is invalid")
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := entryRows.Err(); err != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, err
	}
	if result.Validate() != nil {
		return sandbox.DockerOutputCommitReceipt{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "durable docker output commit receipt is invalid")
	}
	return result, true, nil
}

func storeBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

func validStoreBools(values ...int) bool {
	for _, value := range values {
		if value != 0 && value != 1 {
			return false
		}
	}
	return true
}

func appendDockerContainerIOEvent(ctx context.Context, tx *sql.Tx, runID,
	subjectID, eventType string, createdAt time.Time, payload map[string]any,
) error {
	var missionID string
	if err := tx.QueryRowContext(ctx, `SELECT mission_id FROM runs WHERE id = ?`, runID).
		Scan(&missionID); err != nil {
		return err
	}
	event, err := events.New(runID, missionID, eventType,
		"sandbox_docker_container_io", subjectID, payload)
	if err != nil {
		return err
	}
	event.CreatedAt = createdAt
	_, err = insertRunEventTx(ctx, tx, event)
	return err
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique constraint") ||
		strings.Contains(text, "constraint failed")
}
