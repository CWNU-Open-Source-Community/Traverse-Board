package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/gitmutation"
)

// StartRemoteOperation returns claimed=true only for the first successful CAS.
// Callers must validate their exact approval and current execution scope first;
// a false result is never permission to repeat a network mutation.
func (s *SQLiteStore) StartRemoteOperation(ctx context.Context, id, fingerprint string, at time.Time) (gitmutation.RemoteRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return gitmutation.RemoteRecord{}, false, err
	}
	defer tx.Rollback()
	record, found, err := getRemoteOperationRecord(ctx, tx, id)
	if err != nil {
		return record, false, err
	}
	if !found {
		return record, false, apperror.New(apperror.CodeNotFound, "Remote operation was not found")
	}
	if record.RequestFingerprint != fingerprint || at.IsZero() || at.Before(record.CreatedAt) {
		return record, false, apperror.New(apperror.CodeConflict, "Remote operation intent or start timestamp changed")
	}
	if record.StartedAt != nil || record.CompletedAt != nil {
		return record, false, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE git_remote_operations SET started_at=? WHERE id=? AND request_fingerprint=? AND started_at IS NULL AND completed_at IS NULL`, ts(at), id, fingerprint)
	if err != nil {
		return record, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return record, false, err
	}
	if n != 1 {
		return record, false, apperror.New(apperror.CodeConflict, "Remote operation start lost its compare-and-swap")
	}
	if err = tx.Commit(); err != nil {
		return record, false, err
	}
	record.StartedAt = &at
	return record, true, nil
}

func validateThreadPullRequestApprovalSourceTx(ctx context.Context, tx *sql.Tx, p approval.Proposal) error {
	record, found, err := getRemoteOperationRecord(ctx, tx, p.ProposalID)
	if err != nil {
		return err
	}
	var intent struct {
		Version             string `json:"version"`
		OperationID         string `json:"operation_id"`
		ThreadID            string `json:"thread_id"`
		RunID               string `json:"run_id"`
		SessionID           string `json:"session_id"`
		WorkspaceID         string `json:"workspace_id"`
		SourceWorkspaceID   string `json:"source_workspace_id"`
		ApprovalFingerprint string `json:"approval_fingerprint"`
		DraftOnly           bool   `json:"draft_only"`
	}
	if !found || json.Unmarshal([]byte(record.SpecJSON), &intent) != nil || intent.Version != "thread_pull_request.v1" || !intent.DraftOnly ||
		intent.OperationID != record.ID || record.Operation != gitmutation.RemoteCreatePR || intent.RunID != record.RunID ||
		intent.WorkspaceID != record.WorkspaceID || record.RequestFingerprint != intent.ApprovalFingerprint ||
		p.SessionID != intent.SessionID || p.WorkspaceID != intent.SourceWorkspaceID || p.ActionClass != "github_pull_request_create" ||
		p.Mode != "per_call" || p.Status != approval.StatusPending || p.RequestFingerprint != intent.ApprovalFingerprint || record.StartedAt != nil || record.CompletedAt != nil {
		return errors.New("Approval does not match the persisted draft pull request intent")
	}
	binding, bound, err := runBindingForSessionTx(ctx, tx, p.SessionID)
	if err != nil {
		return err
	}
	if !bound || binding.RunID != record.RunID || binding.WorkspaceID != intent.SourceWorkspaceID {
		return errors.New("Draft pull request source session or workspace changed")
	}
	var matched int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM thread_runs WHERE thread_id=? AND run_id=? AND session_id=?`, intent.ThreadID, record.RunID, intent.SessionID).Scan(&matched); err != nil {
		return err
	}
	if matched != 1 {
		return errors.New("Draft pull request approval has no exact task origin")
	}
	return requireFileEditWorkspaceTx(ctx, tx, p.SessionID, intent.WorkspaceID, true)
}
