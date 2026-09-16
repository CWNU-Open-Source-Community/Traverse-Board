package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/gitmutation"
)

func (s *SQLiteStore) GetGitMutationByKey(ctx context.Context, key string) (gitmutation.Record, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM git_mutation_operations WHERE operation_key_digest=?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return gitmutation.Record{}, false, nil
	}
	if err != nil {
		return gitmutation.Record{}, false, err
	}
	return getGitMutationRecord(ctx, s.db, id)
}

func (s *SQLiteStore) GetGitRemoteByKey(ctx context.Context, key string) (gitmutation.RemoteRecord, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM git_remote_operations WHERE operation_key_digest=?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return gitmutation.RemoteRecord{}, false, nil
	}
	if err != nil {
		return gitmutation.RemoteRecord{}, false, err
	}
	return getRemoteOperationRecord(ctx, s.db, id)
}

func (s *SQLiteStore) StartGitMutationOperation(ctx context.Context, id, fingerprint string, at time.Time) (gitmutation.Record, bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE git_mutation_operations SET started_at=? WHERE id=? AND request_fingerprint=? AND started_at IS NULL AND completed_at IS NULL`, ts(at), id, fingerprint)
	if err != nil {
		return gitmutation.Record{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return gitmutation.Record{}, false, err
	}
	record, found, err := getGitMutationRecord(ctx, s.db, id)
	if err != nil {
		return record, false, err
	}
	if !found || record.RequestFingerprint != fingerprint {
		return record, false, apperror.New(apperror.CodeConflict, "Git operation identity changed")
	}
	return record, n == 1, nil
}

// Operator Git uses the same durable lease fence as model execution, but never
// changes the Run state or manufactures a model turn. A lease held by another
// caller, a live process, or unresolved workspace write prevents admission.
func (s *SQLiteStore) CheckThreadGitIdle(ctx context.Context, threadID, runID string, lease *domain.RunExecutionLease) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	thread, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id=?`, threadID))
	if err != nil {
		return err
	}
	if thread.Status != domain.ThreadActive || thread.ActiveRunID != runID {
		return apperror.New(apperror.CodeConflict, "Git requires the current active task execution")
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, runID).Scan(&status); err != nil {
		return err
	}
	if status != "running" && status != "paused" && status != "created" {
		return apperror.New(apperror.CodeFailedPrecondition, "task execution is not idle for Git")
	}
	current, found, err := getRunExecutionLeaseTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if lease == nil {
		if found && current.ActiveAt(time.Now().UTC()) {
			return apperror.New(apperror.CodeConflict, "task has active execution")
		}
	} else if !found || !sameRunExecutionLease(current, *lease) || !current.ActiveAt(time.Now().UTC()) {
		return apperror.New(apperror.CodeConflict, "Git execution lease changed")
	}
	if err = requireNoOpenWorkspaceRestoreTx(ctx, tx, runID); err != nil {
		return err
	}
	if err = requireQuiescentRunPauseTx(ctx, tx, runID); err != nil {
		return err
	}
	var pending int
	err = tx.QueryRowContext(ctx, `SELECT
	 (SELECT count(*) FROM command_runtime_jobs WHERE run_id=? AND (state IN ('prepared','running','stopping') OR tree_reaped<>1)) +
	 (SELECT count(*) FROM run_supervisor_tool_calls WHERE run_id=? AND status IN ('pending','running')) +
	 (SELECT count(*) FROM operator_steering_deliveries WHERE run_id=? AND status='prepared') +
	 (SELECT count(*) FROM terminal_sessions WHERE run_id=? AND state IN ('starting','running'))`, runID, runID, runID, runID).Scan(&pending)
	if err != nil {
		return err
	}
	if pending != 0 {
		return apperror.New(apperror.CodeConflict, "task has active tools, a command job, or an unresolved input")
	}
	return nil
}

func validateThreadGitApprovalSourceTx(ctx context.Context, tx *sql.Tx, p approval.Proposal) error {
	var runID, workspaceID, fingerprint, spec string
	err := tx.QueryRowContext(ctx, `SELECT run_id,workspace_id,request_fingerprint,spec_json FROM git_mutation_operations WHERE id=?
	 UNION ALL SELECT run_id,workspace_id,request_fingerprint,spec_json FROM git_remote_operations WHERE id=?`, p.ProposalID, p.ProposalID).Scan(&runID, &workspaceID, &fingerprint, &spec)
	if err != nil {
		return err
	}
	var metadata struct {
		Version   string `json:"version"`
		SessionID string `json:"session_id"`
		ThreadID  string `json:"thread_id"`
	}
	if json.Unmarshal([]byte(spec), &metadata) != nil || metadata.Version != "thread_git.v1" || metadata.SessionID != p.SessionID || metadata.ThreadID == "" || workspaceID != p.WorkspaceID || fingerprint != p.RequestFingerprint || p.Mode != "per_call" || p.ActionClass != "git_write" || p.Status != approval.StatusPending {
		return errors.New("Git approval differs from its exact stored intent")
	}
	var matched int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM thread_runs WHERE thread_id=? AND run_id=? AND session_id=?`, metadata.ThreadID, runID, metadata.SessionID).Scan(&matched)
	if err != nil {
		return err
	}
	if matched != 1 {
		return errors.New("Git approval has no exact task origin")
	}
	return nil
}
