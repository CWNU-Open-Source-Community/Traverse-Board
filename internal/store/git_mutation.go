package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/gitmutation"
)

// Mutation record domain type lives in the gitmutation package so store and
// application can share it without import cycles.
type GitMutationRecord = gitmutation.Record

// CreateGitMutationOperation records the operation intent. Replaying the
// same operation key returns the stored record without duplicating work.
func (s *SQLiteStore) CreateGitMutationOperation(ctx context.Context, record GitMutationRecord) (GitMutationRecord, bool, error) {
	if record.ProtocolVersion != gitmutation.ProtocolVersion ||
		!record.Operation.Valid() || strings.TrimSpace(record.ID) == "" || len(record.ID) > 256 ||
		len(record.OperationKeyDigest) != 64 || len(record.RequestFingerprint) != 64 ||
		!json.Valid([]byte(record.SpecJSON)) || len(record.SpecJSON) > 32768 {
		return GitMutationRecord{}, false, apperror.New(apperror.CodeInvalidArgument, "git mutation record is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return GitMutationRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM git_mutation_operations WHERE operation_key_digest = ?`,
		record.OperationKeyDigest).Scan(&existingID)
	if err == nil {
		stored, found, err := getGitMutationRecord(ctx, tx, existingID)
		if err != nil {
			return GitMutationRecord{}, false, err
		}
		if !found || stored.RequestFingerprint != record.RequestFingerprint {
			return GitMutationRecord{}, false, apperror.New(apperror.CodeConflict,
				"git mutation operation key was already used for different intent")
		}
		if err := tx.Commit(); err != nil {
			return GitMutationRecord{}, false, err
		}
		return stored, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return GitMutationRecord{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO git_mutation_operations
		(id, protocol_version, operation_key_digest, request_fingerprint, run_id,
		workspace_id, operation, spec_json, pre_head, created_at)
		VALUES (?, 'repository_mutation.v1', ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.OperationKeyDigest, record.RequestFingerprint, record.RunID,
		record.WorkspaceID, record.Operation, record.SpecJSON, record.PreHead, ts(record.CreatedAt)); err != nil {
		return GitMutationRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return GitMutationRecord{}, false, err
	}
	stored, _, err := getGitMutationRecord(ctx, s.db, record.ID)
	return stored, false, err
}

// CompleteGitMutationOperation fills the receipt fields and appends the
// metadata-only run event. It is idempotent per operation.
func (s *SQLiteStore) CompleteGitMutationOperation(ctx context.Context, id string,
	record GitMutationRecord, completedAt time.Time,
) (GitMutationRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return GitMutationRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, found, err := getGitMutationRecord(ctx, tx, id)
	if err != nil {
		return GitMutationRecord{}, false, err
	}
	if !found {
		return GitMutationRecord{}, false, apperror.New(apperror.CodeNotFound, "git mutation operation was not found")
	}
	if stored.CompletedAt != nil {
		if err := tx.Commit(); err != nil {
			return GitMutationRecord{}, false, err
		}
		return stored, true, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE git_mutation_operations SET post_head = ?,
		branch = ?, commit_id = ?, conflicted = ?, clean = ?, stderr_prefix = ?, completed_at = ?
		WHERE id = ?`, record.PostHead, record.Branch, record.CommitID, boolInt(record.Conflicted),
		boolInt(record.Clean), record.StderrPrefix, ts(completedAt), id); err != nil {
		return GitMutationRecord{}, false, err
	}
	var missionID string
	if err := tx.QueryRowContext(ctx, `SELECT mission_id FROM runs WHERE id = ?`,
		stored.RunID).Scan(&missionID); err != nil {
		return GitMutationRecord{}, false, err
	}
	event, err := events.New(stored.RunID, missionID, events.GitMutationCompletedEvent,
		"git_mutation_runner", stored.RunID, map[string]any{
			"operation": stored.Operation, "workspace_id": stored.WorkspaceID,
			"pre_head": stored.PreHead, "post_head": record.PostHead,
			"commit_id": record.CommitID, "branch": record.Branch,
			"conflicted": record.Conflicted, "clean": record.Clean,
		})
	if err != nil {
		return GitMutationRecord{}, false, err
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return GitMutationRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return GitMutationRecord{}, false, err
	}
	updated, _, err := getGitMutationRecord(ctx, s.db, id)
	return updated, false, err
}

func (s *SQLiteStore) GetGitMutationRecord(ctx context.Context, id string) (GitMutationRecord, bool, error) {
	return getGitMutationRecord(ctx, s.db, id)
}

func getGitMutationRecord(ctx context.Context, queryer skillPackageQueryer, id string) (GitMutationRecord, bool, error) {
	row := queryer.QueryRowContext(ctx, `SELECT id, protocol_version, operation_key_digest,
		request_fingerprint, run_id, workspace_id, operation, spec_json, pre_head, post_head,
		branch, commit_id, conflicted, clean, stderr_prefix, completed_at, created_at
		FROM git_mutation_operations WHERE id = ?`, id)
	var record GitMutationRecord
	var conflicted, clean int
	var completedAt, created sql.NullString
	err := row.Scan(&record.ID, &record.ProtocolVersion, &record.OperationKeyDigest,
		&record.RequestFingerprint, &record.RunID, &record.WorkspaceID, &record.Operation,
		&record.SpecJSON, &record.PreHead, &record.PostHead, &record.Branch, &record.CommitID,
		&conflicted, &clean, &record.StderrPrefix, &completedAt, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return GitMutationRecord{}, false, nil
	}
	if err != nil {
		return GitMutationRecord{}, false, err
	}
	record.Conflicted = conflicted == 1
	record.Clean = clean == 1
	record.CreatedAt = parseTS(created.String)
	if completedAt.Valid {
		if parsed := parseTS(completedAt.String); !parsed.IsZero() {
			record.CompletedAt = &parsed
		}
	}
	return record, true, nil
}
