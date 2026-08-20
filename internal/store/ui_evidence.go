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
	"cyberagent-workbench/internal/uievidence"
)

func (s *SQLiteStore) CreateUIEvidenceAttempt(ctx context.Context,
	attempt uievidence.Attempt,
) (uievidence.Attempt, bool, error) {
	if s == nil || s.db == nil {
		return uievidence.Attempt{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "UI evidence store is unavailable")
	}
	if attempt.Status != uievidence.StatusNotRun || attempt.Version != 1 ||
		attempt.Validate() != nil {
		return uievidence.Attempt{}, false, apperror.New(
			apperror.CodeInvalidArgument, "UI evidence attempt is invalid")
	}
	manifestJSON, err := json.Marshal(attempt.Manifest)
	if err != nil {
		return uievidence.Attempt{}, false, err
	}
	attemptJSON, err := json.Marshal(attempt)
	if err != nil {
		return uievidence.Attempt{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return uievidence.Attempt{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := getUIEvidenceAttemptByOperation(ctx, tx,
		attempt.OperationDigest)
	if err != nil {
		return uievidence.Attempt{}, false, err
	}
	if found {
		if existing.Manifest.AttemptID != attempt.Manifest.AttemptID ||
			existing.RequestFingerprint != attempt.RequestFingerprint {
			return uievidence.Attempt{}, false, apperror.New(
				apperror.CodeConflict, "UI evidence operation key was reused")
		}
		if err := tx.Commit(); err != nil {
			return uievidence.Attempt{}, false, err
		}
		return existing, true, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ui_evidence_attempts (
		id, protocol_version, operation_digest, request_fingerprint, run_id,
		mission_id, session_id, workspace_id, manifest_fingerprint, source_commit,
		dirty_digest, status, failure_stage, artifact_count, artifact_bytes, version,
		manifest_json, attempt_json, created_at, started_at, completed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.Manifest.AttemptID, attempt.ProtocolVersion, attempt.OperationDigest,
		attempt.RequestFingerprint, attempt.Manifest.RunID, attempt.Manifest.MissionID,
		attempt.Manifest.SessionID, attempt.Manifest.WorkspaceID,
		attempt.Manifest.Fingerprint, attempt.Manifest.Source.Commit,
		attempt.Manifest.Source.DirtyDigest, attempt.Status, attempt.FailureStage,
		attempt.ArtifactCount, attempt.ArtifactBytes, attempt.Version,
		string(manifestJSON), string(attemptJSON), ts(attempt.CreatedAt),
		nullableTS(attempt.StartedAt), nullableTS(attempt.CompletedAt), ts(attempt.UpdatedAt))
	if err != nil {
		return uievidence.Attempt{}, false, apperror.Wrap(
			apperror.CodeConflict, "UI evidence manifest was rejected", err)
	}
	stored, err := getUIEvidenceAttempt(ctx, tx, attempt.Manifest.AttemptID)
	if err != nil {
		return uievidence.Attempt{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return uievidence.Attempt{}, false, err
	}
	return stored, false, nil
}

func (s *SQLiteStore) UpdateUIEvidenceAttempt(ctx context.Context,
	attempt uievidence.Attempt, expectedVersion int64,
) (uievidence.Attempt, error) {
	if s == nil || s.db == nil {
		return uievidence.Attempt{}, apperror.New(
			apperror.CodeFailedPrecondition, "UI evidence store is unavailable")
	}
	if expectedVersion < 1 || attempt.Version != expectedVersion+1 ||
		attempt.Validate() != nil {
		return uievidence.Attempt{}, apperror.New(
			apperror.CodeInvalidArgument, "UI evidence transition is invalid")
	}
	attemptJSON, err := json.Marshal(attempt)
	if err != nil {
		return uievidence.Attempt{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE ui_evidence_attempts SET
		status = ?, failure_stage = ?, artifact_count = ?, artifact_bytes = ?,
		version = ?, attempt_json = ?, started_at = ?, completed_at = ?, updated_at = ?
		WHERE id = ? AND version = ?`, attempt.Status, attempt.FailureStage,
		attempt.ArtifactCount, attempt.ArtifactBytes, attempt.Version, string(attemptJSON),
		nullableTS(attempt.StartedAt), nullableTS(attempt.CompletedAt), ts(attempt.UpdatedAt),
		attempt.Manifest.AttemptID, expectedVersion)
	if err != nil {
		return uievidence.Attempt{}, apperror.Wrap(
			apperror.CodeConflict, "UI evidence transition was rejected", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return uievidence.Attempt{}, err
	}
	if changed != 1 {
		if _, getErr := s.GetUIEvidenceAttempt(ctx, attempt.Manifest.AttemptID); getErr != nil {
			return uievidence.Attempt{}, getErr
		}
		return uievidence.Attempt{}, apperror.New(
			apperror.CodeConflict, "UI evidence attempt version changed")
	}
	return s.GetUIEvidenceAttempt(ctx, attempt.Manifest.AttemptID)
}

func (s *SQLiteStore) GetUIEvidenceAttempt(ctx context.Context,
	attemptID string,
) (uievidence.Attempt, error) {
	attemptID = strings.TrimSpace(attemptID)
	if s == nil || s.db == nil || !domain.ValidAgentID(attemptID) {
		return uievidence.Attempt{}, apperror.New(
			apperror.CodeInvalidArgument, "UI evidence attempt id is invalid")
	}
	return getUIEvidenceAttempt(ctx, s.db, attemptID)
}

func (s *SQLiteStore) ListUIEvidenceAttempts(ctx context.Context,
	filter uievidence.ListFilter,
) ([]uievidence.Attempt, error) {
	if s == nil || s.db == nil || filter.Validate() != nil {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"UI evidence list filter is invalid")
	}
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	query := `SELECT attempt_json FROM ui_evidence_attempts WHERE 1 = 1`
	arguments := make([]any, 0, 3)
	if filter.RunID != "" {
		query += ` AND run_id = ?`
		arguments = append(arguments, filter.RunID)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		arguments = append(arguments, filter.Status)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	arguments = append(arguments, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]uievidence.Attempt, 0, filter.Limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		attempt, err := decodeUIEvidenceAttempt(raw)
		if err != nil {
			return nil, err
		}
		values = append(values, attempt)
	}
	return values, rows.Err()
}

func (s *SQLiteStore) AddUIEvidenceStep(ctx context.Context,
	receipt uievidence.StepReceipt,
) error {
	if s == nil || s.db == nil {
		return apperror.New(apperror.CodeFailedPrecondition,
			"UI evidence store is unavailable")
	}
	if err := receipt.Validate(); err != nil {
		return apperror.New(apperror.CodeInvalidArgument,
			"UI evidence step receipt is invalid")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO ui_evidence_steps (
		attempt_id, step_id, sequence, kind, status, failure_stage, fingerprint,
		payload_json, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		receipt.AttemptID, receipt.StepID, receipt.Sequence, receipt.Kind,
		receipt.Status, receipt.FailureStage, receipt.Fingerprint, string(raw),
		ts(receipt.StartedAt), ts(receipt.CompletedAt))
	if err != nil {
		return apperror.Wrap(apperror.CodeConflict,
			"UI evidence step receipt was rejected", err)
	}
	return nil
}

func (s *SQLiteStore) ListUIEvidenceSteps(ctx context.Context,
	attemptID string,
) ([]uievidence.StepReceipt, error) {
	attemptID = strings.TrimSpace(attemptID)
	if s == nil || s.db == nil || !domain.ValidAgentID(attemptID) {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"UI evidence attempt id is invalid")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM ui_evidence_steps
		WHERE attempt_id = ? ORDER BY sequence`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]uievidence.StepReceipt, 0)
	for rows.Next() {
		var raw string
		var receipt uievidence.StepReceipt
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil || receipt.Validate() != nil {
			return nil, fmt.Errorf("invalid persisted UI evidence step for %q", attemptID)
		}
		values = append(values, receipt)
	}
	return values, rows.Err()
}

func (s *SQLiteStore) AddUIEvidenceArtifact(ctx context.Context,
	artifact uievidence.Artifact,
) error {
	if s == nil || s.db == nil {
		return apperror.New(apperror.CodeFailedPrecondition,
			"UI evidence store is unavailable")
	}
	if err := artifact.Validate(); err != nil {
		return apperror.New(apperror.CodeInvalidArgument,
			"UI evidence artifact is invalid")
	}
	metadata := artifact.Metadata
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO ui_evidence_artifacts (
		id, attempt_id, run_id, step_id, kind, mime, sha256, size_bytes, width,
		height, source_commit, redacted, fingerprint, metadata_json, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, metadata.ID,
		metadata.AttemptID, metadata.RunID, metadata.StepID, metadata.Kind,
		metadata.MIME, metadata.SHA256, metadata.Bytes, metadata.Width,
		metadata.Height, metadata.SourceCommit, boolInt(metadata.Redacted),
		metadata.Fingerprint, string(raw), artifact.Content, ts(metadata.CreatedAt))
	if err != nil {
		return apperror.Wrap(apperror.CodeConflict,
			"UI evidence artifact was rejected", err)
	}
	return nil
}

func (s *SQLiteStore) ListUIEvidenceArtifacts(ctx context.Context,
	attemptID string,
) ([]uievidence.ArtifactMetadata, error) {
	attemptID = strings.TrimSpace(attemptID)
	if s == nil || s.db == nil || !domain.ValidAgentID(attemptID) {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"UI evidence attempt id is invalid")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT metadata_json FROM ui_evidence_artifacts
		WHERE attempt_id = ? ORDER BY created_at, id`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]uievidence.ArtifactMetadata, 0)
	for rows.Next() {
		var raw string
		var metadata uievidence.ArtifactMetadata
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata.Validate() != nil {
			return nil, fmt.Errorf("invalid persisted UI evidence artifact for %q", attemptID)
		}
		values = append(values, metadata)
	}
	return values, rows.Err()
}

func (s *SQLiteStore) GetUIEvidenceArtifact(ctx context.Context,
	attemptID, artifactID string,
) (uievidence.Artifact, error) {
	attemptID = strings.TrimSpace(attemptID)
	artifactID = strings.TrimSpace(artifactID)
	if s == nil || s.db == nil || !domain.ValidAgentID(attemptID) ||
		!domain.ValidAgentID(artifactID) {
		return uievidence.Artifact{}, apperror.New(
			apperror.CodeInvalidArgument, "UI evidence artifact identity is invalid")
	}
	var raw string
	var content []byte
	err := s.db.QueryRowContext(ctx, `SELECT metadata_json, content
		FROM ui_evidence_artifacts WHERE attempt_id = ? AND id = ?`,
		attemptID, artifactID).Scan(&raw, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return uievidence.Artifact{}, apperror.New(
			apperror.CodeNotFound, "UI evidence artifact was not found")
	}
	if err != nil {
		return uievidence.Artifact{}, err
	}
	var metadata uievidence.ArtifactMetadata
	artifact := uievidence.Artifact{Metadata: metadata, Content: content}
	if err := json.Unmarshal([]byte(raw), &artifact.Metadata); err != nil ||
		artifact.Validate() != nil {
		return uievidence.Artifact{}, fmt.Errorf(
			"invalid persisted UI evidence artifact %q", artifactID)
	}
	return artifact, nil
}

func (s *SQLiteStore) UIEvidenceArtifactTotals(ctx context.Context,
	attemptID string,
) (int, int64, error) {
	attemptID = strings.TrimSpace(attemptID)
	if s == nil || s.db == nil || !domain.ValidAgentID(attemptID) {
		return 0, 0, apperror.New(apperror.CodeInvalidArgument,
			"UI evidence attempt id is invalid")
	}
	var count int
	var bytes int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
		FROM ui_evidence_artifacts WHERE attempt_id = ?`, attemptID).Scan(&count, &bytes)
	return count, bytes, err
}

func (s *SQLiteStore) ReconcileUIEvidenceAttempts(ctx context.Context,
	now time.Time,
) ([]uievidence.Attempt, error) {
	attempts, err := s.ListUIEvidenceAttempts(ctx,
		uievidence.ListFilter{Status: uievidence.StatusRunning, Limit: 500})
	if err != nil {
		return nil, err
	}
	reconciled := make([]uievidence.Attempt, 0, len(attempts))
	for _, attempt := range attempts {
		count, bytes, err := s.UIEvidenceArtifactTotals(ctx, attempt.Manifest.AttemptID)
		if err != nil {
			return nil, err
		}
		interrupted, err := uievidence.InterruptAttempt(attempt, count, bytes, now)
		if err != nil {
			return nil, err
		}
		stored, err := s.UpdateUIEvidenceAttempt(ctx, interrupted, attempt.Version)
		if err != nil {
			return nil, err
		}
		reconciled = append(reconciled, stored)
	}
	return reconciled, nil
}

type uiEvidenceAttemptQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getUIEvidenceAttempt(ctx context.Context, queryer uiEvidenceAttemptQueryer,
	attemptID string,
) (uievidence.Attempt, error) {
	var raw string
	err := queryer.QueryRowContext(ctx, `SELECT attempt_json FROM ui_evidence_attempts
		WHERE id = ?`, attemptID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return uievidence.Attempt{}, apperror.New(
			apperror.CodeNotFound, "UI evidence attempt was not found")
	}
	if err != nil {
		return uievidence.Attempt{}, err
	}
	return decodeUIEvidenceAttempt(raw)
}

func getUIEvidenceAttemptByOperation(ctx context.Context,
	queryer uiEvidenceAttemptQueryer, digest string,
) (uievidence.Attempt, bool, error) {
	var raw string
	err := queryer.QueryRowContext(ctx, `SELECT attempt_json FROM ui_evidence_attempts
		WHERE operation_digest = ?`, digest).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return uievidence.Attempt{}, false, nil
	}
	if err != nil {
		return uievidence.Attempt{}, false, err
	}
	attempt, err := decodeUIEvidenceAttempt(raw)
	return attempt, err == nil, err
}

func decodeUIEvidenceAttempt(raw string) (uievidence.Attempt, error) {
	var attempt uievidence.Attempt
	if err := json.Unmarshal([]byte(raw), &attempt); err != nil {
		return uievidence.Attempt{}, err
	}
	if err := attempt.Validate(); err != nil {
		return uievidence.Attempt{}, fmt.Errorf(
			"invalid persisted UI evidence attempt %q: %w",
			attempt.Manifest.AttemptID, err)
	}
	return attempt, nil
}
