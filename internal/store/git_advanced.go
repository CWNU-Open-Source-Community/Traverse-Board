package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/gitadvanced"
)

func (s *SQLiteStore) CreateGitAdvancedOperation(ctx context.Context,
	record gitadvanced.OperationRecord,
) (gitadvanced.OperationRecord, bool, error) {
	if err := validateGitAdvancedOperation(record, true); err != nil {
		return gitadvanced.OperationRecord{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument, "Git advanced operation is invalid", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := getGitAdvancedOperation(ctx, tx, "", record.OperationKeySHA256)
	if err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	if found {
		if existing.RequestFingerprint != record.RequestFingerprint ||
			existing.PreviewID != record.PreviewID || existing.RunID != record.RunID ||
			existing.WorkspaceID != record.WorkspaceID || existing.Operation != record.Operation {
			return gitadvanced.OperationRecord{}, false, apperror.New(apperror.CodeConflict,
				"Git advanced operation key was reused for different intent")
		}
		if err := tx.Commit(); err != nil {
			return gitadvanced.OperationRecord{}, false, err
		}
		return existing, true, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO git_advanced_operations
		(id, protocol_version, operation_key_sha256, request_fingerprint, preview_id,
		approval_fingerprint, run_id, session_id, workspace_id, operation, spec_json,
		preview_json, repository_sha256, common_dir_sha256, permission_snapshot_id, permission_revision,
		capability_generation, lease_id, lease_generation, status, receipt_json,
		error_code, created_at)
		VALUES (?, 'git-advanced.v1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		'proposed', '{}', '', ?)`, record.ID, record.OperationKeySHA256,
		record.RequestFingerprint, record.PreviewID, record.ApprovalFingerprint,
		record.RunID, record.SessionID, record.WorkspaceID, record.Operation,
		record.SpecJSON, record.PreviewJSON, record.RepositorySHA256, record.CommonDirSHA256,
		record.PermissionSnapshotID, record.PermissionRevision,
		record.CapabilityGeneration, record.LeaseID, record.LeaseGeneration,
		ts(record.CreatedAt))
	if err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	if err := appendGitAdvancedEventTx(ctx, tx, record.RunID,
		events.GitAdvancedProposedEvent, record.ID, map[string]any{
			"operation": record.Operation, "preview_id": record.PreviewID,
			"workspace_id":      record.WorkspaceID,
			"repository_sha256": record.RepositorySHA256,
		}); err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	stored, _, err := getGitAdvancedOperation(ctx, s.db, record.ID, "")
	return stored, false, err
}

func (s *SQLiteStore) StartGitAdvancedOperation(ctx context.Context, id,
	approvalID, approvalFingerprint string, startedAt time.Time,
) (gitadvanced.OperationRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, found, err := getGitAdvancedOperation(ctx, tx, id, "")
	if err != nil || !found {
		if err == nil {
			err = apperror.New(apperror.CodeNotFound, "Git advanced operation was not found")
		}
		return gitadvanced.OperationRecord{}, false, err
	}
	if record.Status != gitadvanced.OperationProposed {
		if record.ApprovalID != approvalID || record.ApprovalFingerprint != approvalFingerprint {
			return gitadvanced.OperationRecord{}, false, apperror.New(apperror.CodeConflict,
				"Git advanced approval binding changed")
		}
		if err := tx.Commit(); err != nil {
			return gitadvanced.OperationRecord{}, false, err
		}
		return record, true, nil
	}
	var proposalID, requestFingerprint, status string
	if err := tx.QueryRowContext(ctx, `SELECT proposal_id, request_fingerprint, status
		FROM tool_approvals WHERE id = ?`, strings.TrimSpace(approvalID)).Scan(
		&proposalID, &requestFingerprint, &status); err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	if proposalID != record.ID || requestFingerprint != approvalFingerprint || status != "approved" {
		return gitadvanced.OperationRecord{}, false, apperror.New(apperror.CodeFailedPrecondition,
			"Git advanced operation requires the exact approved preview")
	}
	result, err := tx.ExecContext(ctx, `UPDATE git_advanced_operations SET approval_id = ?,
		status = 'running', started_at = ? WHERE id = ? AND status = 'proposed'`,
		approvalID, ts(startedAt), id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return gitadvanced.OperationRecord{}, false, apperror.New(apperror.CodeConflict,
				"another Git advanced operation owns this repository")
		}
		return gitadvanced.OperationRecord{}, false, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return gitadvanced.OperationRecord{}, false,
			apperror.New(apperror.CodeConflict, "Git advanced operation changed concurrently")
	}
	if err := appendGitAdvancedEventTx(ctx, tx, record.RunID,
		events.GitAdvancedStartedEvent, record.ID, map[string]any{
			"operation": record.Operation, "approval_id": approvalID,
			"lease_generation": record.LeaseGeneration,
		}); err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	stored, _, err := getGitAdvancedOperation(ctx, s.db, id, "")
	return stored, false, err
}

func (s *SQLiteStore) CompleteGitAdvancedOperation(ctx context.Context, id string,
	receipt gitadvanced.Receipt, completedAt time.Time,
) (gitadvanced.OperationRecord, bool, error) {
	receiptJSON, err := json.Marshal(receipt)
	if err != nil || receipt.Validate() != nil || len(receiptJSON) > 2*1024*1024 ||
		receipt.CheckpointID == "" {
		return gitadvanced.OperationRecord{}, false,
			apperror.New(apperror.CodeInvalidArgument, "Git advanced receipt is invalid")
	}
	status := gitadvanced.OperationFailed
	switch receipt.Status {
	case gitadvanced.ReceiptSucceeded:
		status = gitadvanced.OperationSucceeded
	case gitadvanced.ReceiptConflicted:
		status = gitadvanced.OperationConflicted
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, found, err := getGitAdvancedOperation(ctx, tx, id, "")
	if err != nil || !found {
		if err == nil {
			err = apperror.New(apperror.CodeNotFound, "Git advanced operation was not found")
		}
		return gitadvanced.OperationRecord{}, false, err
	}
	if record.Status.Terminal() {
		if record.ReceiptJSON != string(receiptJSON) {
			return gitadvanced.OperationRecord{}, false, apperror.New(apperror.CodeConflict,
				"terminal Git advanced receipt differs from replay")
		}
		if err := tx.Commit(); err != nil {
			return gitadvanced.OperationRecord{}, false, err
		}
		return record, true, nil
	}
	if record.Status != gitadvanced.OperationRunning || receipt.PreviewID != record.PreviewID ||
		receipt.Operation != record.Operation {
		return gitadvanced.OperationRecord{}, false, apperror.New(apperror.CodeConflict,
			"Git advanced receipt does not match the running operation")
	}
	result, err := tx.ExecContext(ctx, `UPDATE git_advanced_operations SET status = ?, receipt_json = ?,
		error_code = ?, completed_at = ? WHERE id = ? AND status = 'running'`, status,
		string(receiptJSON), receipt.ErrorCode, ts(completedAt), id)
	if err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return gitadvanced.OperationRecord{}, false,
			apperror.New(apperror.CodeConflict, "Git advanced operation changed concurrently")
	}
	if err := appendGitAdvancedEventTx(ctx, tx, record.RunID,
		events.GitAdvancedCompletedEvent, record.ID, map[string]any{
			"operation": record.Operation, "status": status,
			"error_code": receipt.ErrorCode, "conflict_count": len(receipt.Conflict.Files),
			"checkpoint_id": receipt.CheckpointID, "sequence_id": receipt.SequenceID,
			"worktree_id": receipt.WorktreeID,
		}); err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return gitadvanced.OperationRecord{}, false, err
	}
	stored, _, err := getGitAdvancedOperation(ctx, s.db, id, "")
	return stored, false, err
}

func (s *SQLiteStore) GetGitAdvancedOperation(ctx context.Context,
	id string,
) (gitadvanced.OperationRecord, bool, error) {
	return getGitAdvancedOperation(ctx, s.db, strings.TrimSpace(id), "")
}

func (s *SQLiteStore) ListGitAdvancedOperations(ctx context.Context,
	filter gitadvanced.OperationListFilter,
) ([]gitadvanced.OperationRecord, error) {
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	if filter.Limit < 1 || filter.Limit > 500 ||
		(filter.Status != "" && !filter.Status.Valid()) {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"Git advanced operation list filter is invalid")
	}
	query := gitAdvancedOperationSelect + ` WHERE 1=1`
	var args []any
	for _, item := range []struct {
		column string
		value  string
	}{{"run_id", strings.TrimSpace(filter.RunID)},
		{"workspace_id", strings.TrimSpace(filter.WorkspaceID)},
		{"repository_sha256", strings.TrimSpace(filter.RepositorySHA256)}} {
		if item.value != "" {
			query += " AND " + item.column + " = ?"
			args = append(args, item.value)
		}
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []gitadvanced.OperationRecord
	for rows.Next() {
		record, err := scanGitAdvancedOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func validateGitAdvancedOperation(record gitadvanced.OperationRecord,
	creating bool,
) error {
	if record.ProtocolVersion != gitadvanced.ProtocolVersion || !record.Operation.Valid() ||
		record.ID == "" || len(record.ID) > 256 ||
		!gitadvanced.ValidDigest(record.OperationKeySHA256) ||
		!gitadvanced.ValidDigest(record.RequestFingerprint) ||
		!gitadvanced.ValidDigest(record.ApprovalFingerprint) ||
		!gitadvanced.ValidDigest(record.RepositorySHA256) ||
		!gitadvanced.ValidDigest(record.CommonDirSHA256) ||
		!gitadvanced.ValidDigest(record.CapabilityGeneration) ||
		record.RunID == "" || record.SessionID == "" || record.WorkspaceID == "" ||
		record.PermissionSnapshotID == "" || record.PermissionRevision <= 0 ||
		record.LeaseID == "" || record.LeaseGeneration <= 0 || record.CreatedAt.IsZero() ||
		!json.Valid([]byte(record.SpecJSON)) || len(record.SpecJSON) > gitadvanced.MaxSpecJSONBytes ||
		!json.Valid([]byte(record.PreviewJSON)) || len(record.PreviewJSON) > 2*1024*1024 {
		return errors.New("Git advanced operation identity or evidence is invalid")
	}
	var spec gitadvanced.Spec
	var preview gitadvanced.Preview
	if json.Unmarshal([]byte(record.SpecJSON), &spec) != nil || spec.Validate() != nil ||
		json.Unmarshal([]byte(record.PreviewJSON), &preview) != nil ||
		preview.Spec.Validate() != nil || preview.Capability.Validate() != nil ||
		preview.ProtocolVersion != gitadvanced.PreviewProtocolVersion ||
		preview.ID != record.PreviewID || preview.Operation != record.Operation ||
		preview.Spec.Operation != record.Operation ||
		preview.ApprovalFingerprint != record.ApprovalFingerprint ||
		preview.Binding.RepositorySHA256 != record.RepositorySHA256 ||
		preview.Binding.CommonDirSHA256 != record.CommonDirSHA256 ||
		preview.Capability.Generation != record.CapabilityGeneration ||
		preview.PermissionSnapshotID != record.PermissionSnapshotID ||
		preview.PermissionRevision != record.PermissionRevision ||
		preview.LeaseGeneration != record.LeaseGeneration {
		return errors.New("Git advanced stored preview binding is invalid")
	}
	normalizedSpecJSON, specErr := json.Marshal(spec)
	previewSpecJSON, previewSpecErr := json.Marshal(preview.Spec)
	if specErr != nil || previewSpecErr != nil ||
		!bytes.Equal(normalizedSpecJSON, previewSpecJSON) {
		return errors.New("Git advanced stored preview spec does not match its audit record")
	}
	if creating && (record.Status != gitadvanced.OperationProposed || record.ApprovalID != "" ||
		record.StartedAt != nil || record.CompletedAt != nil) {
		return errors.New("new Git advanced operation must be proposed")
	}
	return nil
}

const gitAdvancedOperationSelect = `SELECT id, protocol_version,
	operation_key_sha256, request_fingerprint, preview_id, approval_fingerprint,
	COALESCE(approval_id, ''), run_id, session_id, workspace_id, operation, spec_json,
	preview_json, repository_sha256, common_dir_sha256, permission_snapshot_id,
	permission_revision,
	capability_generation, lease_id, lease_generation, status, receipt_json,
	error_code, created_at, started_at, completed_at FROM git_advanced_operations`

func getGitAdvancedOperation(ctx context.Context, queryer skillPackageQueryer,
	id, operationKey string,
) (gitadvanced.OperationRecord, bool, error) {
	query := gitAdvancedOperationSelect
	argument := id
	if id != "" {
		query += ` WHERE id = ?`
	} else {
		query += ` WHERE operation_key_sha256 = ?`
		argument = operationKey
	}
	record, err := scanGitAdvancedOperation(queryer.QueryRowContext(ctx, query, argument))
	if errors.Is(err, sql.ErrNoRows) {
		return gitadvanced.OperationRecord{}, false, nil
	}
	return record, err == nil, err
}

func scanGitAdvancedOperation(row scanner) (gitadvanced.OperationRecord, error) {
	var record gitadvanced.OperationRecord
	var createdAt string
	var startedAt, completedAt sql.NullString
	if err := row.Scan(&record.ID, &record.ProtocolVersion, &record.OperationKeySHA256,
		&record.RequestFingerprint, &record.PreviewID, &record.ApprovalFingerprint,
		&record.ApprovalID, &record.RunID, &record.SessionID, &record.WorkspaceID,
		&record.Operation, &record.SpecJSON, &record.PreviewJSON,
		&record.RepositorySHA256, &record.CommonDirSHA256,
		&record.PermissionSnapshotID, &record.PermissionRevision,
		&record.CapabilityGeneration, &record.LeaseID, &record.LeaseGeneration,
		&record.Status, &record.ReceiptJSON, &record.ErrorCode, &createdAt,
		&startedAt, &completedAt); err != nil {
		return gitadvanced.OperationRecord{}, err
	}
	record.CreatedAt = parseTS(createdAt)
	if startedAt.Valid {
		value := parseTS(startedAt.String)
		record.StartedAt = &value
	}
	if completedAt.Valid {
		value := parseTS(completedAt.String)
		record.CompletedAt = &value
	}
	return record, nil
}

func appendGitAdvancedEventTx(ctx context.Context, tx *sql.Tx, runID,
	eventType, subjectID string, payload map[string]any,
) error {
	var missionID string
	if err := tx.QueryRowContext(ctx, `SELECT mission_id FROM runs WHERE id = ?`,
		runID).Scan(&missionID); err != nil {
		return err
	}
	event, err := events.New(runID, missionID, eventType, "git_advanced_store",
		subjectID, payload)
	if err != nil {
		return err
	}
	_, err = insertRunEventTx(ctx, tx, event)
	return err
}

// CreateGitAdvancedSequence persists the observed sequencer even when the
// start operation ended in a conflict. A unique active-repository index fences
// parallel sequences after crashes and restarts.
func (s *SQLiteStore) CreateGitAdvancedSequence(ctx context.Context,
	sequence gitadvanced.Sequence,
) (gitadvanced.Sequence, bool, error) {
	if err := validateGitAdvancedSequence(sequence); err != nil {
		return gitadvanced.Sequence{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument, "Git advanced sequence is invalid", err)
	}
	if sequence.Generation != 1 {
		return gitadvanced.Sequence{}, false,
			apperror.New(apperror.CodeInvalidArgument, "new Git advanced sequence generation must be one")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO git_advanced_sequences
		(id, protocol_version, run_id, workspace_id, kind, status, repository_sha256,
		original_head, original_branch, target_json, sequencer_sha256, current_head,
		conflict_json, generation, started_operation_id, last_operation_id,
		created_at, updated_at, completed_at)
		VALUES (?, 'git-advanced-sequence.v1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1,
		?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, sequence.ID, sequence.RunID,
		sequence.WorkspaceID, sequence.Kind, sequence.Status, sequence.RepositorySHA256,
		sequence.OriginalHead, sequence.OriginalBranch, sequence.TargetJSON,
		sequence.SequencerSHA256, sequence.CurrentHead, sequence.ConflictJSON,
		sequence.StartedOperationID, sequence.LastOperationID, ts(sequence.CreatedAt),
		ts(sequence.UpdatedAt), optionalTS(sequence.CompletedAt))
	if err != nil {
		return gitadvanced.Sequence{}, false, err
	}
	rows, _ := result.RowsAffected()
	stored, found, err := s.GetGitAdvancedSequence(ctx, sequence.ID)
	if err != nil || !found {
		return gitadvanced.Sequence{}, false, err
	}
	if rows == 0 && (stored.StartedOperationID != sequence.StartedOperationID ||
		stored.RepositorySHA256 != sequence.RepositorySHA256) {
		return gitadvanced.Sequence{}, false, apperror.New(apperror.CodeConflict,
			"Git advanced sequence identity was reused")
	}
	return stored, rows == 0, nil
}

func (s *SQLiteStore) AdvanceGitAdvancedSequence(ctx context.Context,
	sequence gitadvanced.Sequence, expectedGeneration int64,
) (gitadvanced.Sequence, bool, error) {
	if err := validateGitAdvancedSequence(sequence); err != nil ||
		sequence.Generation != expectedGeneration+1 {
		return gitadvanced.Sequence{}, false,
			apperror.New(apperror.CodeInvalidArgument, "Git advanced sequence transition is invalid")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE git_advanced_sequences SET status = ?,
		sequencer_sha256 = ?, current_head = ?, conflict_json = ?, generation = ?,
		last_operation_id = ?, updated_at = ?, completed_at = ?
		WHERE id = ? AND generation = ?`, sequence.Status, sequence.SequencerSHA256,
		sequence.CurrentHead, sequence.ConflictJSON, sequence.Generation,
		sequence.LastOperationID, ts(sequence.UpdatedAt), optionalTS(sequence.CompletedAt),
		sequence.ID, expectedGeneration)
	if err != nil {
		return gitadvanced.Sequence{}, false, err
	}
	rows, _ := result.RowsAffected()
	stored, found, err := s.GetGitAdvancedSequence(ctx, sequence.ID)
	if err != nil || !found {
		return gitadvanced.Sequence{}, false, err
	}
	if rows == 0 {
		if stored.Generation == sequence.Generation && stored.LastOperationID == sequence.LastOperationID {
			return stored, true, nil
		}
		return gitadvanced.Sequence{}, false, apperror.New(apperror.CodeConflict,
			"Git advanced sequence changed concurrently")
	}
	return stored, false, nil
}

func (s *SQLiteStore) GetGitAdvancedSequence(ctx context.Context,
	id string,
) (gitadvanced.Sequence, bool, error) {
	return getGitAdvancedSequenceRow(s.db.QueryRowContext(ctx, gitAdvancedSequenceSelect+
		` WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) GetActiveGitAdvancedSequence(ctx context.Context,
	repositorySHA256 string,
) (gitadvanced.Sequence, bool, error) {
	return getGitAdvancedSequenceRow(s.db.QueryRowContext(ctx, gitAdvancedSequenceSelect+
		` WHERE repository_sha256 = ? AND status IN ('active','conflicted')`,
		strings.TrimSpace(repositorySHA256)))
}

const gitAdvancedSequenceSelect = `SELECT id, protocol_version, run_id, workspace_id,
	kind, status, repository_sha256, original_head, original_branch, target_json,
	sequencer_sha256, current_head, conflict_json, generation, started_operation_id,
	last_operation_id, created_at, updated_at, completed_at FROM git_advanced_sequences`

func getGitAdvancedSequenceRow(row scanner) (gitadvanced.Sequence, bool, error) {
	var value gitadvanced.Sequence
	var createdAt, updatedAt string
	var completedAt sql.NullString
	err := row.Scan(&value.ID, &value.ProtocolVersion, &value.RunID, &value.WorkspaceID,
		&value.Kind, &value.Status, &value.RepositorySHA256, &value.OriginalHead,
		&value.OriginalBranch, &value.TargetJSON, &value.SequencerSHA256,
		&value.CurrentHead, &value.ConflictJSON, &value.Generation,
		&value.StartedOperationID, &value.LastOperationID, &createdAt, &updatedAt,
		&completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return gitadvanced.Sequence{}, false, nil
	}
	if err != nil {
		return gitadvanced.Sequence{}, false, err
	}
	value.CreatedAt, value.UpdatedAt = parseTS(createdAt), parseTS(updatedAt)
	if completedAt.Valid {
		parsed := parseTS(completedAt.String)
		value.CompletedAt = &parsed
	}
	return value, true, nil
}

func validateGitAdvancedSequence(value gitadvanced.Sequence) error {
	if value.ProtocolVersion != gitadvanced.SequenceProtocolVersion || value.ID == "" ||
		value.RunID == "" || value.WorkspaceID == "" ||
		(value.Kind != gitadvanced.SequenceRebase && value.Kind != gitadvanced.SequenceCherryPick &&
			value.Kind != gitadvanced.SequenceBisect) ||
		!gitadvanced.ValidDigest(value.RepositorySHA256) ||
		!gitadvanced.ValidObjectID(value.OriginalHead) ||
		!gitadvanced.ValidObjectID(value.CurrentHead) ||
		!json.Valid([]byte(value.TargetJSON)) || !json.Valid([]byte(value.ConflictJSON)) ||
		!gitadvanced.ValidDigest(value.SequencerSHA256) || value.Generation <= 0 ||
		value.StartedOperationID == "" || value.LastOperationID == "" ||
		value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return errors.New("Git advanced sequence fields are invalid")
	}
	if value.Status.Terminal() != (value.CompletedAt != nil) {
		return errors.New("Git advanced sequence terminal state is invalid")
	}
	return nil
}

func (s *SQLiteStore) CreateManagedGitWorktree(ctx context.Context,
	value gitadvanced.ManagedWorktree,
) (gitadvanced.ManagedWorktree, bool, error) {
	if err := validateManagedGitWorktree(value); err != nil || value.Generation != 1 || !value.Present {
		return gitadvanced.ManagedWorktree{}, false,
			apperror.New(apperror.CodeInvalidArgument, "managed Git worktree is invalid")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO git_managed_worktrees
		(id, protocol_version, run_id, workspace_id, repository_sha256,
		common_dir_sha256, name, path, path_sha256, branch, head, locked, lock_reason,
		present, generation, created_operation_id, last_operation_id, created_at,
		updated_at, removed_at) VALUES (?, 'git-managed-worktree.v1', ?, ?, ?, ?, ?, ?,
		?, ?, ?, ?, ?, 1, 1, ?, ?, ?, ?, NULL) ON CONFLICT(id) DO NOTHING`,
		value.ID, value.RunID, value.WorkspaceID, value.RepositorySHA256,
		value.CommonDirSHA256, value.Name, value.Path, value.PathSHA256, value.Branch,
		value.Head, boolInt(value.Locked), value.LockReason, value.CreatedOperationID,
		value.LastOperationID, ts(value.CreatedAt), ts(value.UpdatedAt))
	if err != nil {
		return gitadvanced.ManagedWorktree{}, false, err
	}
	rows, _ := result.RowsAffected()
	stored, found, err := s.GetManagedGitWorktree(ctx, value.ID)
	if err != nil || !found {
		return gitadvanced.ManagedWorktree{}, false, err
	}
	if rows == 0 && (stored.PathSHA256 != value.PathSHA256 ||
		stored.CreatedOperationID != value.CreatedOperationID) {
		return gitadvanced.ManagedWorktree{}, false, apperror.New(apperror.CodeConflict,
			"managed Git worktree identity was reused")
	}
	return stored, rows == 0, nil
}

func (s *SQLiteStore) AdvanceManagedGitWorktree(ctx context.Context,
	value gitadvanced.ManagedWorktree, expectedGeneration int64,
) (gitadvanced.ManagedWorktree, bool, error) {
	if err := validateManagedGitWorktree(value); err != nil ||
		value.Generation != expectedGeneration+1 {
		return gitadvanced.ManagedWorktree{}, false,
			apperror.New(apperror.CodeInvalidArgument, "managed Git worktree transition is invalid")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE git_managed_worktrees SET head = ?,
		locked = ?, lock_reason = ?, present = ?, generation = ?, last_operation_id = ?,
		updated_at = ?, removed_at = ? WHERE id = ? AND generation = ?`, value.Head,
		boolInt(value.Locked), value.LockReason, boolInt(value.Present), value.Generation,
		value.LastOperationID, ts(value.UpdatedAt), optionalTS(value.RemovedAt), value.ID,
		expectedGeneration)
	if err != nil {
		return gitadvanced.ManagedWorktree{}, false, err
	}
	rows, _ := result.RowsAffected()
	stored, found, err := s.GetManagedGitWorktree(ctx, value.ID)
	if err != nil || !found {
		return gitadvanced.ManagedWorktree{}, false, err
	}
	if rows == 0 {
		if stored.Generation == value.Generation && stored.LastOperationID == value.LastOperationID {
			return stored, true, nil
		}
		return gitadvanced.ManagedWorktree{}, false, apperror.New(apperror.CodeConflict,
			"managed Git worktree changed concurrently")
	}
	return stored, false, nil
}

func (s *SQLiteStore) GetManagedGitWorktree(ctx context.Context,
	id string,
) (gitadvanced.ManagedWorktree, bool, error) {
	return getManagedGitWorktreeRow(s.db.QueryRowContext(ctx, managedGitWorktreeSelect+
		` WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) GetManagedGitWorktreeByName(ctx context.Context,
	commonDirSHA256, name string,
) (gitadvanced.ManagedWorktree, bool, error) {
	return getManagedGitWorktreeRow(s.db.QueryRowContext(ctx, managedGitWorktreeSelect+
		` WHERE common_dir_sha256 = ? AND name = ?`, strings.TrimSpace(commonDirSHA256),
		strings.TrimSpace(name)))
}

func (s *SQLiteStore) ListManagedGitWorktrees(ctx context.Context, runID,
	repositorySHA256 string, includeRemoved bool, limit int,
) ([]gitadvanced.ManagedWorktree, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"managed Git worktree list limit is invalid")
	}
	query := managedGitWorktreeSelect + ` WHERE 1=1`
	var args []any
	if runID = strings.TrimSpace(runID); runID != "" {
		query += ` AND run_id = ?`
		args = append(args, runID)
	}
	if repositorySHA256 = strings.TrimSpace(repositorySHA256); repositorySHA256 != "" {
		query += ` AND repository_sha256 = ?`
		args = append(args, repositorySHA256)
	}
	if !includeRemoved {
		query += ` AND present = 1`
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []gitadvanced.ManagedWorktree
	for rows.Next() {
		value, _, err := getManagedGitWorktreeRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

const managedGitWorktreeSelect = `SELECT id, protocol_version, run_id,
	workspace_id, repository_sha256, common_dir_sha256, name, path, path_sha256,
	branch, head, locked, lock_reason, present, generation, created_operation_id,
	last_operation_id, created_at, updated_at, removed_at FROM git_managed_worktrees`

func getManagedGitWorktreeRow(row scanner) (gitadvanced.ManagedWorktree, bool, error) {
	var value gitadvanced.ManagedWorktree
	var locked, present int
	var createdAt, updatedAt string
	var removedAt sql.NullString
	err := row.Scan(&value.ID, &value.ProtocolVersion, &value.RunID, &value.WorkspaceID,
		&value.RepositorySHA256, &value.CommonDirSHA256, &value.Name, &value.Path,
		&value.PathSHA256, &value.Branch, &value.Head, &locked, &value.LockReason,
		&present, &value.Generation, &value.CreatedOperationID, &value.LastOperationID,
		&createdAt, &updatedAt, &removedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return gitadvanced.ManagedWorktree{}, false, nil
	}
	if err != nil {
		return gitadvanced.ManagedWorktree{}, false, err
	}
	value.Locked, value.Present = locked == 1, present == 1
	value.CreatedAt, value.UpdatedAt = parseTS(createdAt), parseTS(updatedAt)
	if removedAt.Valid {
		parsed := parseTS(removedAt.String)
		value.RemovedAt = &parsed
	}
	return value, true, nil
}

func validateManagedGitWorktree(value gitadvanced.ManagedWorktree) error {
	if value.ProtocolVersion != gitadvanced.WorktreeProtocolVersion || value.ID == "" ||
		value.RunID == "" || value.WorkspaceID == "" || value.Name == "" || value.Path == "" ||
		!gitadvanced.ValidDigest(value.RepositorySHA256) ||
		!gitadvanced.ValidDigest(value.CommonDirSHA256) ||
		!gitadvanced.ValidDigest(value.PathSHA256) || !gitadvanced.ValidObjectID(value.Head) ||
		value.Branch == "" || value.Generation <= 0 || value.CreatedOperationID == "" ||
		value.LastOperationID == "" || value.CreatedAt.IsZero() ||
		value.UpdatedAt.Before(value.CreatedAt) || len(value.LockReason) > 4096 {
		return errors.New("managed Git worktree fields are invalid")
	}
	if value.Present == (value.RemovedAt != nil) || (!value.Present && value.Locked) {
		return errors.New("managed Git worktree lifecycle is invalid")
	}
	return nil
}
