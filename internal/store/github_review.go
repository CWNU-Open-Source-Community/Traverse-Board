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
	"cyberagent-workbench/internal/githubreview"
)

const maxGitHubReviewListJSONBytes = 16 * 1024 * 1024

func (s *SQLiteStore) PutGitHubReviewConnection(ctx context.Context,
	connection githubreview.Connection, expectedGeneration int64,
) (githubreview.Connection, bool, error) {
	connection.Normalize()
	if connection.Validate() != nil || expectedGeneration < 0 {
		return githubreview.Connection{}, false, apperror.New(apperror.CodeInvalidArgument,
			"GitHub review connection is invalid")
	}
	networkJSON, err := json.Marshal(connection.Network)
	if err != nil {
		return githubreview.Connection{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return githubreview.Connection{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := getGitHubReviewConnection(ctx, tx, connection.ID, "")
	if err != nil {
		return githubreview.Connection{}, false, err
	}
	if !found {
		byName, nameFound, queryErr := getGitHubReviewConnection(ctx, tx, "",
			connection.Repository.FullName)
		if queryErr != nil {
			return githubreview.Connection{}, false, queryErr
		}
		if nameFound {
			existing, found = byName, true
		}
	}
	if !found {
		if expectedGeneration != 0 || connection.Generation != 1 {
			return githubreview.Connection{}, false, apperror.New(apperror.CodeConflict,
				"new GitHub review connection requires generation one")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO github_review_connections
			(id, protocol_version, host, owner, repository, full_name, credential_name,
			auth_kind, client_id, network_json, enabled, generation, created_at, updated_at)
			VALUES (?, 'github-review-connection.v1', ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			connection.ID, connection.Repository.Host, connection.Repository.Owner,
			connection.Repository.Name, connection.Repository.FullName,
			connection.Credential.Name, connection.Credential.Kind, connection.ClientID,
			string(networkJSON), boolInt(connection.Enabled), ts(connection.CreatedAt),
			ts(connection.UpdatedAt))
		if err != nil {
			return githubreview.Connection{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return githubreview.Connection{}, false, err
		}
		return connection, false, nil
	}
	if sameGitHubReviewConnection(existing, connection) {
		if err := tx.Commit(); err != nil {
			return githubreview.Connection{}, false, err
		}
		return existing, true, nil
	}
	if existing.ID != connection.ID || expectedGeneration != existing.Generation ||
		connection.Generation != existing.Generation+1 ||
		connection.CreatedAt != existing.CreatedAt ||
		connection.UpdatedAt.Before(existing.UpdatedAt) {
		return githubreview.Connection{}, false, apperror.New(apperror.CodeConflict,
			"GitHub review connection generation or identity changed")
	}
	result, err := tx.ExecContext(ctx, `UPDATE github_review_connections
		SET credential_name = ?, auth_kind = ?, client_id = ?, network_json = ?,
			enabled = ?, generation = ?, updated_at = ?
		WHERE id = ? AND generation = ?`, connection.Credential.Name,
		connection.Credential.Kind, connection.ClientID, string(networkJSON),
		boolInt(connection.Enabled), connection.Generation, ts(connection.UpdatedAt),
		connection.ID, expectedGeneration)
	if err != nil {
		return githubreview.Connection{}, false, err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return githubreview.Connection{}, false, apperror.New(apperror.CodeConflict,
			"GitHub review connection update lost its compare-and-swap")
	}
	if err := tx.Commit(); err != nil {
		return githubreview.Connection{}, false, err
	}
	return connection, false, nil
}

func (s *SQLiteStore) GetGitHubReviewConnection(ctx context.Context,
	id string,
) (githubreview.Connection, bool, error) {
	return getGitHubReviewConnection(ctx, s.db, strings.TrimSpace(id), "")
}

func (s *SQLiteStore) GetGitHubReviewConnectionByRepository(ctx context.Context,
	fullName string,
) (githubreview.Connection, bool, error) {
	return getGitHubReviewConnection(ctx, s.db, "", strings.TrimSpace(fullName))
}

func (s *SQLiteStore) ListGitHubReviewConnections(ctx context.Context,
	enabledOnly bool,
) ([]githubreview.Connection, error) {
	query := githubReviewConnectionSelect
	args := []any{}
	if enabledOnly {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY full_name ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]githubreview.Connection, 0)
	for rows.Next() {
		value, scanErr := scanGitHubReviewConnection(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

const githubReviewConnectionSelect = `SELECT id, protocol_version, host, owner,
	repository, full_name, credential_name, auth_kind, client_id, network_json,
	enabled, generation, created_at, updated_at FROM github_review_connections`

func getGitHubReviewConnection(ctx context.Context, queryer skillPackageQueryer,
	id, fullName string,
) (githubreview.Connection, bool, error) {
	query := githubReviewConnectionSelect
	argument := id
	if id != "" {
		query += ` WHERE id = ?`
	} else if fullName != "" {
		query += ` WHERE full_name = ?`
		argument = fullName
	} else {
		return githubreview.Connection{}, false, nil
	}
	value, err := scanGitHubReviewConnection(queryer.QueryRowContext(ctx, query, argument))
	if errors.Is(err, sql.ErrNoRows) {
		return githubreview.Connection{}, false, nil
	}
	return value, err == nil, err
}

func scanGitHubReviewConnection(row scanner) (githubreview.Connection, error) {
	var value githubreview.Connection
	var host, owner, repository, fullName, networkJSON, createdAt, updatedAt string
	var enabled int
	if err := row.Scan(&value.ID, &value.ProtocolVersion, &host, &owner, &repository,
		&fullName, &value.Credential.Name, &value.Credential.Kind, &value.ClientID,
		&networkJSON, &enabled, &value.Generation, &createdAt, &updatedAt); err != nil {
		return githubreview.Connection{}, err
	}
	value.Repository = githubreview.RepositoryIdentity{Host: host, Owner: owner,
		Name: repository, FullName: fullName}
	if err := json.Unmarshal([]byte(networkJSON), &value.Network); err != nil {
		return githubreview.Connection{}, err
	}
	value.Enabled = enabled == 1
	value.CreatedAt, value.UpdatedAt = parseTS(createdAt), parseTS(updatedAt)
	if err := value.Validate(); err != nil {
		return githubreview.Connection{}, err
	}
	return value, nil
}

func sameGitHubReviewConnection(left, right githubreview.Connection) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func (s *SQLiteStore) SaveGitHubReviewSnapshot(ctx context.Context, connectionID string,
	snapshot githubreview.Snapshot,
) (githubreview.Snapshot, bool, error) {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" || snapshot.Validate() != nil {
		return githubreview.Snapshot{}, false, apperror.New(apperror.CodeInvalidArgument,
			"GitHub review snapshot is invalid")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > githubreview.MaxSnapshotBytes {
		return githubreview.Snapshot{}, false, apperror.New(apperror.CodeResourceExhausted,
			"GitHub review snapshot exceeds its persistence bound")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return githubreview.Snapshot{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	connection, found, err := getGitHubReviewConnection(ctx, tx, connectionID, "")
	if err != nil || !found {
		if err == nil {
			err = apperror.New(apperror.CodeNotFound, "GitHub review connection was not found")
		}
		return githubreview.Snapshot{}, false, err
	}
	if connection.Repository.FullName != snapshot.Identity.Repository.FullName {
		return githubreview.Snapshot{}, false, apperror.New(apperror.CodeConflict,
			"GitHub review snapshot does not belong to its connection")
	}
	existing, found, err := getGitHubReviewSnapshot(ctx, tx, snapshot.ID)
	if err != nil {
		return githubreview.Snapshot{}, false, err
	}
	if found {
		if existing.Fingerprint != snapshot.Fingerprint {
			return githubreview.Snapshot{}, false, apperror.New(apperror.CodeConflict,
				"GitHub review snapshot identity was reused")
		}
		if err := tx.Commit(); err != nil {
			return githubreview.Snapshot{}, false, err
		}
		return existing, true, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO github_review_snapshots
		(id, protocol_version, connection_id, repository_full_name, pr_number,
		base_sha, head_sha, merge_base_sha, fingerprint, state, snapshot_json,
		fetched_at, created_at) VALUES (?, 'github-review-snapshot.v1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ID, connectionID, snapshot.Identity.Repository.FullName,
		snapshot.Identity.Number, snapshot.Identity.BaseSHA, snapshot.Identity.HeadSHA,
		snapshot.Identity.MergeBaseSHA, snapshot.Fingerprint, snapshot.State, string(raw),
		ts(snapshot.FetchedAt), ts(time.Now().UTC()))
	if err != nil {
		return githubreview.Snapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return githubreview.Snapshot{}, false, err
	}
	return snapshot, false, nil
}

func (s *SQLiteStore) GetGitHubReviewSnapshot(ctx context.Context,
	id string,
) (githubreview.Snapshot, bool, error) {
	return getGitHubReviewSnapshot(ctx, s.db, strings.TrimSpace(id))
}

func getGitHubReviewSnapshot(ctx context.Context, queryer skillPackageQueryer,
	id string,
) (githubreview.Snapshot, bool, error) {
	var raw string
	err := queryer.QueryRowContext(ctx, `SELECT snapshot_json FROM github_review_snapshots
		WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return githubreview.Snapshot{}, false, nil
	}
	if err != nil {
		return githubreview.Snapshot{}, false, err
	}
	var snapshot githubreview.Snapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil || snapshot.Validate() != nil {
		return githubreview.Snapshot{}, false, errors.New("stored GitHub review snapshot is invalid")
	}
	return snapshot, true, nil
}

func (s *SQLiteStore) ListGitHubReviewSnapshots(ctx context.Context,
	connectionID string, prNumber int64, limit int,
) ([]githubreview.Snapshot, error) {
	if strings.TrimSpace(connectionID) == "" || prNumber < 0 || limit < 1 || limit > 200 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"GitHub review snapshot list filter is invalid")
	}
	query := `SELECT snapshot_json FROM github_review_snapshots WHERE connection_id = ?`
	args := []any{connectionID}
	if prNumber > 0 {
		query += ` AND pr_number = ?`
		args = append(args, prNumber)
	}
	query += ` ORDER BY fetched_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]githubreview.Snapshot, 0)
	totalJSONBytes := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		totalJSONBytes, err = addGitHubReviewListJSONBytes(totalJSONBytes, len(raw))
		if err != nil {
			return nil, err
		}
		var snapshot githubreview.Snapshot
		if json.Unmarshal([]byte(raw), &snapshot) != nil || snapshot.Validate() != nil {
			return nil, errors.New("stored GitHub review snapshot is invalid")
		}
		result = append(result, snapshot)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) SaveGitHubReviewEvidence(ctx context.Context,
	record githubreview.EvidenceRecord,
) (githubreview.EvidenceRecord, bool, error) {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.RunID) == "" ||
		strings.TrimSpace(record.WorkspaceID) == "" || record.Graph.Validate() != nil ||
		record.ID != "ghg-"+githubreview.Fingerprint("github-review-evidence-record",
			record.RunID, record.WorkspaceID, record.Graph.Fingerprint)[:32] {
		return githubreview.EvidenceRecord{}, false, apperror.New(apperror.CodeInvalidArgument,
			"GitHub review evidence record is invalid")
	}
	raw, err := json.Marshal(record.Graph)
	if err != nil || len(raw) > 16*1024*1024 {
		return githubreview.EvidenceRecord{}, false, apperror.New(apperror.CodeResourceExhausted,
			"GitHub review evidence graph exceeds its persistence bound")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO github_review_evidence_graphs
		(id, protocol_version, run_id, workspace_id, snapshot_id, fingerprint, state,
		graph_json, created_at) VALUES (?, 'github-review-evidence.v1', ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, record.ID, record.RunID, record.WorkspaceID,
		record.Graph.SnapshotID, record.Graph.Fingerprint, record.Graph.State, string(raw),
		ts(record.Graph.CreatedAt))
	if err != nil {
		return githubreview.EvidenceRecord{}, false, err
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		return record, false, nil
	}
	existing, found, err := s.GetGitHubReviewEvidence(ctx, record.ID)
	if err != nil || !found || existing.Graph.Fingerprint != record.Graph.Fingerprint {
		if err == nil {
			err = apperror.New(apperror.CodeConflict, "GitHub review evidence identity was reused")
		}
		return githubreview.EvidenceRecord{}, false, err
	}
	return existing, true, nil
}

func (s *SQLiteStore) GetGitHubReviewEvidence(ctx context.Context,
	id string,
) (githubreview.EvidenceRecord, bool, error) {
	var record githubreview.EvidenceRecord
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT run_id, workspace_id, graph_json
		FROM github_review_evidence_graphs WHERE id = ?`, strings.TrimSpace(id)).
		Scan(&record.RunID, &record.WorkspaceID, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return githubreview.EvidenceRecord{}, false, nil
	}
	if err != nil {
		return githubreview.EvidenceRecord{}, false, err
	}
	record.ID = strings.TrimSpace(id)
	if json.Unmarshal([]byte(raw), &record.Graph) != nil || record.Graph.Validate() != nil {
		return githubreview.EvidenceRecord{}, false, errors.New("stored GitHub review evidence is invalid")
	}
	return record, true, nil
}

func (s *SQLiteStore) ListGitHubReviewEvidence(ctx context.Context,
	runID string, limit int,
) ([]githubreview.EvidenceRecord, error) {
	if strings.TrimSpace(runID) == "" || limit < 1 || limit > 200 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"GitHub review evidence list filter is invalid")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, run_id, workspace_id, graph_json
		FROM github_review_evidence_graphs WHERE run_id = ?
		ORDER BY created_at DESC, id DESC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]githubreview.EvidenceRecord, 0)
	totalJSONBytes := 0
	for rows.Next() {
		var record githubreview.EvidenceRecord
		var raw string
		if err := rows.Scan(&record.ID, &record.RunID, &record.WorkspaceID, &raw); err != nil {
			return nil, err
		}
		totalJSONBytes, err = addGitHubReviewListJSONBytes(totalJSONBytes, len(raw))
		if err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &record.Graph) != nil || record.Graph.Validate() != nil {
			return nil, errors.New("stored GitHub review evidence is invalid")
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) CreateGitHubReviewWrite(ctx context.Context,
	record githubreview.WriteRecord,
) (githubreview.WriteRecord, bool, error) {
	if err := validateGitHubReviewWriteRecord(record, true); err != nil {
		return githubreview.WriteRecord{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument, "GitHub review write is invalid", err)
	}
	specJSON, _ := json.Marshal(record.Spec)
	previewJSON, _ := json.Marshal(record.Preview)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := getGitHubReviewWrite(ctx, tx, "", record.OperationKeySHA256)
	if err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	if found {
		if existing.RequestFingerprint != record.RequestFingerprint ||
			existing.Preview.ID != record.Preview.ID || existing.RunID != record.RunID {
			return githubreview.WriteRecord{}, false, apperror.New(apperror.CodeConflict,
				"GitHub review write operation key was reused for different intent")
		}
		if err := tx.Commit(); err != nil {
			return githubreview.WriteRecord{}, false, err
		}
		return existing, true, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO github_review_write_operations
		(id, protocol_version, operation_key_sha256, request_fingerprint,
		approval_fingerprint, run_id, session_id, workspace_id, connection_id,
		operation, spec_json, preview_json, capability_generation, base_sha, head_sha,
		merge_base_sha, status, receipt_json, error_code, created_at)
		VALUES (?, 'github-review-write.v1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		'proposed', '{}', '', ?)`, record.ID, record.OperationKeySHA256,
		record.RequestFingerprint, record.ApprovalFingerprint, record.RunID,
		record.SessionID, record.WorkspaceID, record.ConnectionID, record.Preview.Operation,
		string(specJSON), string(previewJSON), record.Preview.CapabilityGeneration,
		record.Preview.Identity.BaseSHA, record.Preview.Identity.HeadSHA,
		record.Preview.Identity.MergeBaseSHA, ts(record.CreatedAt))
	if err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	return record, false, nil
}

func (s *SQLiteStore) StartGitHubReviewWrite(ctx context.Context, id, approvalID,
	approvalFingerprint string, startedAt time.Time,
) (githubreview.WriteRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, found, err := getGitHubReviewWrite(ctx, tx, strings.TrimSpace(id), "")
	if err != nil || !found {
		if err == nil {
			err = apperror.New(apperror.CodeNotFound, "GitHub review write was not found")
		}
		return githubreview.WriteRecord{}, false, err
	}
	if record.Status != githubreview.OperationProposed {
		if record.ApprovalID == approvalID && record.ApprovalFingerprint == approvalFingerprint {
			if err := tx.Commit(); err != nil {
				return githubreview.WriteRecord{}, false, err
			}
			return record, true, nil
		}
		return githubreview.WriteRecord{}, false, apperror.New(apperror.CodeConflict,
			"GitHub review write already started with different approval")
	}
	if approvalID == "" || approvalFingerprint != record.ApprovalFingerprint || startedAt.IsZero() {
		return githubreview.WriteRecord{}, false, apperror.New(apperror.CodeInvalidArgument,
			"GitHub review write start binding is invalid")
	}
	result, err := tx.ExecContext(ctx, `UPDATE github_review_write_operations
		SET approval_id = ?, status = 'running', started_at = ?
		WHERE id = ? AND status = 'proposed' AND approval_id IS NULL`,
		approvalID, ts(startedAt), record.ID)
	if err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return githubreview.WriteRecord{}, false, apperror.New(apperror.CodeConflict,
			"GitHub review write start lost its compare-and-swap")
	}
	if err := tx.Commit(); err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	return s.mustGetGitHubReviewWrite(ctx, record.ID)
}

func (s *SQLiteStore) CompleteGitHubReviewWrite(ctx context.Context, id string,
	receipt githubreview.WriteReceipt, completedAt time.Time,
) (githubreview.WriteRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, found, err := getGitHubReviewWrite(ctx, tx, strings.TrimSpace(id), "")
	if err != nil || !found {
		if err == nil {
			err = apperror.New(apperror.CodeNotFound, "GitHub review write was not found")
		}
		return githubreview.WriteRecord{}, false, err
	}
	if record.Status.Terminal() {
		if record.Receipt.ID == receipt.ID {
			if err := tx.Commit(); err != nil {
				return githubreview.WriteRecord{}, false, err
			}
			return record, true, nil
		}
		return githubreview.WriteRecord{}, false, apperror.New(apperror.CodeConflict,
			"terminal GitHub review write receipt cannot change")
	}
	if record.Status != githubreview.OperationRunning || receipt.PreviewID != record.Preview.ID ||
		completedAt.IsZero() || completedAt.Before(record.StartedAt) {
		return githubreview.WriteRecord{}, false, apperror.New(apperror.CodeConflict,
			"GitHub review write completion binding is invalid")
	}
	status := githubreview.OperationFailed
	if receipt.Status == githubreview.ReceiptSucceeded {
		status = githubreview.OperationSucceeded
	} else if receipt.Status == githubreview.ReceiptRecovered {
		status = githubreview.OperationRecovered
	}
	raw, _ := json.Marshal(receipt)
	result, err := tx.ExecContext(ctx, `UPDATE github_review_write_operations
		SET status = ?, receipt_json = ?, error_code = ?, completed_at = ?
		WHERE id = ? AND status = 'running'`, status, string(raw), receipt.ErrorCode,
		ts(completedAt), record.ID)
	if err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return githubreview.WriteRecord{}, false, apperror.New(apperror.CodeConflict,
			"GitHub review write completion lost its compare-and-swap")
	}
	if err := tx.Commit(); err != nil {
		return githubreview.WriteRecord{}, false, err
	}
	return s.mustGetGitHubReviewWrite(ctx, record.ID)
}

func (s *SQLiteStore) GetGitHubReviewWrite(ctx context.Context,
	id string,
) (githubreview.WriteRecord, bool, error) {
	return getGitHubReviewWrite(ctx, s.db, strings.TrimSpace(id), "")
}

func (s *SQLiteStore) ListGitHubReviewWrites(ctx context.Context, runID string,
	status githubreview.OperationStatus, limit int,
) ([]githubreview.WriteRecord, error) {
	if strings.TrimSpace(runID) == "" || limit < 1 || limit > 200 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"GitHub review write list filter is invalid")
	}
	query := githubReviewWriteSelect + ` WHERE run_id = ?`
	args := []any{runID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]githubreview.WriteRecord, 0)
	totalJSONBytes := 0
	for rows.Next() {
		record, jsonBytes, scanErr := scanGitHubReviewWriteSized(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		totalJSONBytes, scanErr = addGitHubReviewListJSONBytes(totalJSONBytes, jsonBytes)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ListRunningGitHubReviewWrites(ctx context.Context,
	limit int,
) ([]githubreview.WriteRecord, error) {
	if limit < 1 || limit > 500 {
		return nil, errors.New("GitHub review recovery limit is invalid")
	}
	rows, err := s.db.QueryContext(ctx, githubReviewWriteSelect+
		` WHERE status = 'running' ORDER BY created_at ASC, id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]githubreview.WriteRecord, 0)
	totalJSONBytes := 0
	for rows.Next() {
		record, jsonBytes, scanErr := scanGitHubReviewWriteSized(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		totalJSONBytes, scanErr = addGitHubReviewListJSONBytes(totalJSONBytes, jsonBytes)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

const githubReviewWriteSelect = `SELECT id, protocol_version,
	operation_key_sha256, request_fingerprint, approval_fingerprint,
	COALESCE(approval_id, ''), run_id, session_id, workspace_id, connection_id,
	operation, spec_json, preview_json, status, receipt_json, error_code,
	created_at, started_at, completed_at FROM github_review_write_operations`

func getGitHubReviewWrite(ctx context.Context, queryer skillPackageQueryer,
	id, operationKey string,
) (githubreview.WriteRecord, bool, error) {
	query := githubReviewWriteSelect
	argument := id
	if id != "" {
		query += ` WHERE id = ?`
	} else {
		query += ` WHERE operation_key_sha256 = ?`
		argument = operationKey
	}
	record, err := scanGitHubReviewWrite(queryer.QueryRowContext(ctx, query, argument))
	if errors.Is(err, sql.ErrNoRows) {
		return githubreview.WriteRecord{}, false, nil
	}
	return record, err == nil, err
}

func scanGitHubReviewWrite(row scanner) (githubreview.WriteRecord, error) {
	record, _, err := scanGitHubReviewWriteSized(row)
	return record, err
}

func scanGitHubReviewWriteSized(row scanner) (githubreview.WriteRecord, int, error) {
	var record githubreview.WriteRecord
	var operation githubreview.WriteOperation
	var specJSON, previewJSON, receiptJSON, createdAt string
	var startedAt, completedAt sql.NullString
	if err := row.Scan(&record.ID, &record.ProtocolVersion, &record.OperationKeySHA256,
		&record.RequestFingerprint, &record.ApprovalFingerprint, &record.ApprovalID,
		&record.RunID, &record.SessionID, &record.WorkspaceID, &record.ConnectionID,
		&operation, &specJSON, &previewJSON, &record.Status, &receiptJSON,
		&record.ErrorCode, &createdAt, &startedAt, &completedAt); err != nil {
		return githubreview.WriteRecord{}, 0, err
	}
	jsonBytes := len(specJSON) + len(previewJSON) + len(receiptJSON)
	if json.Unmarshal([]byte(specJSON), &record.Spec) != nil ||
		json.Unmarshal([]byte(previewJSON), &record.Preview) != nil {
		return githubreview.WriteRecord{}, jsonBytes,
			errors.New("stored GitHub review write JSON is invalid")
	}
	if receiptJSON != "{}" && json.Unmarshal([]byte(receiptJSON), &record.Receipt) != nil {
		return githubreview.WriteRecord{}, jsonBytes,
			errors.New("stored GitHub review receipt JSON is invalid")
	}
	if record.Preview.Operation != operation {
		return githubreview.WriteRecord{}, jsonBytes,
			errors.New("stored GitHub review operation is inconsistent")
	}
	record.CreatedAt = parseTS(createdAt)
	if startedAt.Valid {
		record.StartedAt = parseTS(startedAt.String)
	}
	if completedAt.Valid {
		record.CompletedAt = parseTS(completedAt.String)
	}
	if err := validateGitHubReviewWriteRecord(record, false); err != nil {
		return githubreview.WriteRecord{}, jsonBytes, err
	}
	return record, jsonBytes, nil
}

func addGitHubReviewListJSONBytes(total, next int) (int, error) {
	if total < 0 || next < 0 || total > maxGitHubReviewListJSONBytes-next {
		return 0, apperror.New(apperror.CodeResourceExhausted,
			"GitHub review list exceeds its aggregate JSON bound; request a smaller limit")
	}
	return total + next, nil
}

func validateGitHubReviewWriteRecord(record githubreview.WriteRecord, creating bool) error {
	if strings.TrimSpace(record.ID) == "" || record.ProtocolVersion != githubreview.WriteProtocolVersion ||
		len(record.OperationKeySHA256) != 64 || len(record.RequestFingerprint) != 64 ||
		len(record.ApprovalFingerprint) != 64 || strings.TrimSpace(record.RunID) == "" ||
		strings.TrimSpace(record.SessionID) == "" || strings.TrimSpace(record.WorkspaceID) == "" ||
		strings.TrimSpace(record.ConnectionID) == "" || record.Spec.Validate() != nil ||
		record.Preview.Validate() != nil || record.Spec.Operation != record.Preview.Operation ||
		record.ApprovalFingerprint != record.Preview.ApprovalFingerprint ||
		record.CreatedAt.IsZero() {
		return errors.New("GitHub review write record identity is invalid")
	}
	switch record.Status {
	case githubreview.OperationProposed:
		if record.ApprovalID != "" || !record.StartedAt.IsZero() || !record.CompletedAt.IsZero() {
			return errors.New("proposed GitHub review write has execution metadata")
		}
	case githubreview.OperationRunning:
		if record.ApprovalID == "" || record.StartedAt.IsZero() || !record.CompletedAt.IsZero() {
			return errors.New("running GitHub review write has invalid execution metadata")
		}
	case githubreview.OperationSucceeded, githubreview.OperationRecovered, githubreview.OperationFailed:
		if record.ApprovalID == "" || record.StartedAt.IsZero() || record.CompletedAt.IsZero() ||
			record.Receipt.PreviewID != record.Preview.ID {
			return errors.New("terminal GitHub review write has invalid receipt metadata")
		}
	default:
		return errors.New("GitHub review write status is invalid")
	}
	if creating && record.Status != githubreview.OperationProposed {
		return errors.New("new GitHub review write must be proposed")
	}
	return nil
}

func (s *SQLiteStore) mustGetGitHubReviewWrite(ctx context.Context,
	id string,
) (githubreview.WriteRecord, bool, error) {
	record, found, err := s.GetGitHubReviewWrite(ctx, id)
	if err != nil || !found {
		if err == nil {
			err = fmt.Errorf("GitHub review write %s disappeared", id)
		}
		return githubreview.WriteRecord{}, false, err
	}
	return record, false, nil
}
