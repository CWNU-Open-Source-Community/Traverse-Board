package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/gitmutation"
)

type RemoteOperation = gitmutation.RemoteOperation

const (
	RemoteFetch      = gitmutation.RemoteFetch
	RemotePullFF     = gitmutation.RemotePullFF
	RemotePushBranch = gitmutation.RemotePushBranch
	RemoteCreatePR   = gitmutation.RemoteCreatePR
	RemoteUpdatePR   = gitmutation.RemoteUpdatePR
)

// Remote record domain type lives in the gitmutation package.
type RemoteOperationRecord = gitmutation.RemoteRecord

func (s *SQLiteStore) CreateRemoteOperation(ctx context.Context, record RemoteOperationRecord) (RemoteOperationRecord, bool, error) {
	if record.ProtocolVersion != "repository_remote.v1" || !record.Operation.Valid() ||
		strings.TrimSpace(record.ID) == "" || len(record.ID) > 256 ||
		len(record.OperationKeyDigest) != 64 || len(record.RequestFingerprint) != 64 ||
		record.RemoteHost == "" || len(record.RemoteHost) > 253 {
		return RemoteOperationRecord{}, false, apperror.New(apperror.CodeInvalidArgument, "remote operation record is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return RemoteOperationRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM git_remote_operations WHERE operation_key_digest = ?`,
		record.OperationKeyDigest).Scan(&existingID)
	if err == nil {
		stored, found, err := getRemoteOperationRecord(ctx, tx, existingID)
		if err != nil {
			return RemoteOperationRecord{}, false, err
		}
		if !found || stored.RequestFingerprint != record.RequestFingerprint {
			return RemoteOperationRecord{}, false, apperror.New(apperror.CodeConflict,
				"remote operation key was already used for different intent")
		}
		if err := tx.Commit(); err != nil {
			return RemoteOperationRecord{}, false, err
		}
		return stored, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RemoteOperationRecord{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO git_remote_operations
		(id, protocol_version, operation_key_digest, request_fingerprint, run_id, workspace_id,
		operation, spec_json, remote_host, remote_port, protocol, branch, pre_head, created_at)
		VALUES (?, 'repository_remote.v1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.OperationKeyDigest, record.RequestFingerprint, record.RunID, record.WorkspaceID,
		record.Operation, record.SpecJSON, record.RemoteHost, record.RemotePort, record.Protocol,
		record.Branch, record.PreHead, ts(record.CreatedAt)); err != nil {
		return RemoteOperationRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return RemoteOperationRecord{}, false, err
	}
	stored, _, err := getRemoteOperationRecord(ctx, s.db, record.ID)
	return stored, false, err
}

func (s *SQLiteStore) CompleteRemoteOperation(ctx context.Context, id string,
	record RemoteOperationRecord, completedAt time.Time,
) (RemoteOperationRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return RemoteOperationRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, found, err := getRemoteOperationRecord(ctx, tx, id)
	if err != nil {
		return RemoteOperationRecord{}, false, err
	}
	if !found {
		return RemoteOperationRecord{}, false, apperror.New(apperror.CodeNotFound, "remote operation was not found")
	}
	if stored.CompletedAt != nil {
		if err := tx.Commit(); err != nil {
			return RemoteOperationRecord{}, false, err
		}
		return stored, true, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE git_remote_operations SET post_head = ?, commit_id = ?,
		pull_request_url = ?, pull_request_number = ?, stderr_prefix = ?, completed_at = ? WHERE id = ?`,
		record.PostHead, record.CommitID, record.PullRequestURL, record.PullRequestNumber,
		record.StderrPrefix, ts(completedAt), id); err != nil {
		return RemoteOperationRecord{}, false, err
	}
	var missionID string
	if err := tx.QueryRowContext(ctx, `SELECT mission_id FROM runs WHERE id = ?`, stored.RunID).Scan(&missionID); err != nil {
		return RemoteOperationRecord{}, false, err
	}
	event, err := events.New(stored.RunID, missionID, events.GitRemoteCompletedEvent,
		"git_remote_runner", stored.RunID, map[string]any{
			"operation": stored.Operation, "remote_host": stored.RemoteHost,
			"branch": stored.Branch, "commit_id": record.CommitID,
			"pull_request_url": record.PullRequestURL, "pull_request_number": record.PullRequestNumber,
		})
	if err != nil {
		return RemoteOperationRecord{}, false, err
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return RemoteOperationRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return RemoteOperationRecord{}, false, err
	}
	updated, _, err := getRemoteOperationRecord(ctx, s.db, id)
	return updated, false, err
}

func (s *SQLiteStore) GetRemoteOperation(ctx context.Context, id string) (RemoteOperationRecord, bool, error) {
	return getRemoteOperationRecord(ctx, s.db, id)
}

func getRemoteOperationRecord(ctx context.Context, queryer skillPackageQueryer, id string) (RemoteOperationRecord, bool, error) {
	row := queryer.QueryRowContext(ctx, `SELECT id, protocol_version, operation_key_digest,
		request_fingerprint, run_id, workspace_id, operation, spec_json, remote_host, remote_port,
		protocol, branch, pre_head, post_head, commit_id, pull_request_url, pull_request_number,
		stderr_prefix, completed_at, created_at FROM git_remote_operations WHERE id = ?`, id)
	var record RemoteOperationRecord
	var completedAt, created sql.NullString
	err := row.Scan(&record.ID, &record.ProtocolVersion, &record.OperationKeyDigest,
		&record.RequestFingerprint, &record.RunID, &record.WorkspaceID, &record.Operation,
		&record.SpecJSON, &record.RemoteHost, &record.RemotePort, &record.Protocol, &record.Branch,
		&record.PreHead, &record.PostHead, &record.CommitID, &record.PullRequestURL,
		&record.PullRequestNumber, &record.StderrPrefix, &completedAt, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteOperationRecord{}, false, nil
	}
	if err != nil {
		return RemoteOperationRecord{}, false, err
	}
	record.CreatedAt = parseTS(created.String)
	if completedAt.Valid {
		if parsed := parseTS(completedAt.String); !parsed.IsZero() {
			record.CompletedAt = &parsed
		}
	}
	return record, true, nil
}
