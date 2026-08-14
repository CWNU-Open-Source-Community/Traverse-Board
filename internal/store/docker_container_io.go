package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"cyberagent-workbench/internal/apperror"
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
	InsertDockerOutputStagingReceipt(ctx context.Context,
		receipt sandbox.DockerOutputStagingReceipt) (bool, error)
	CommitDockerOutputs(ctx context.Context, request sandbox.DockerOutputCommitRequest,
		receipt sandbox.DockerOutputCommitReceipt) (bool, error)
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
	result, err := s.db.ExecContext(ctx, insertDockerLogCaptureReceiptSQL,
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
	return err == nil && count == 1, err
}

const insertDockerOutputStagingReceiptSQL = "INSERT INTO sandbox_docker_output_staging_receipts (id, attempt_id, generation, run_id, container_id_fingerprint, protocol_version, status, file_count, total_bytes, redacted_count, entry_digest_set, export_fingerprint, receipt_fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

func (s *SQLiteStore) InsertDockerOutputStagingReceipt(ctx context.Context,
	receipt sandbox.DockerOutputStagingReceipt,
) (bool, error) {
	if receipt.Validate() != nil {
		return false, apperror.New(apperror.CodeInvalidArgument, "docker output staging receipt is invalid")
	}
	result, err := s.db.ExecContext(ctx, insertDockerOutputStagingReceiptSQL,
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
	return err == nil && count == 1, err
}

const insertDockerOutputCommitReceiptSQL = "INSERT INTO sandbox_docker_output_commit_receipts (id, attempt_id, generation, run_id, workspace_id, operation_key_digest, request_fingerprint, protocol_version, status, committed_count, committed_digest_set, receipt_fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

const insertDockerOutputCommitEntrySQL = "INSERT INTO sandbox_docker_output_commit_entries (receipt_id, ordinal, path, sha256, size_bytes, media_type) VALUES (?, ?, ?, ?, ?, ?)"

const selectDockerOutputCommitByKeySQL = "SELECT id FROM sandbox_docker_output_commit_receipts WHERE operation_key_digest = ?"

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
	defer tx.Rollback()
	var existingID string
	err = tx.QueryRowContext(ctx, selectDockerOutputCommitByKeySQL,
		request.OperationKeyDigest).Scan(&existingID)
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
		return false, err
	}
	for index, entry := range receipt.Entries {
		if _, err := tx.ExecContext(ctx, insertDockerOutputCommitEntrySQL,
			receipt.ID, index+1, entry.Path, entry.SHA256, entry.SizeBytes,
			entry.MediaType); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func storeBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique constraint") ||
		strings.Contains(text, "constraint failed")
}
