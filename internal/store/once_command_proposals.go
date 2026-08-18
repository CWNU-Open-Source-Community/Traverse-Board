package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/runner"
)

// Proposal domain types live in the runner package so the application layer
// can depend on them without a store import cycle.
type OnceCommandProposal = runner.OnceCommandProposal
type OnceCommandProposalOperation = runner.OnceCommandProposalOperation

func (s *SQLiteStore) CreateOnceCommandProposal(ctx context.Context,
	operation OnceCommandProposalOperation, proposal OnceCommandProposal,
) (OnceCommandProposal, bool, error) {
	if len(operation.KeyDigest) != 64 || len(operation.RequestFingerprint) != 64 ||
		strings.TrimSpace(proposal.ID) == "" || len(proposal.ID) > 256 ||
		proposal.Status != "proposed" || proposal.ProtocolVersion != "once_command.v1" ||
		len(proposal.SpecFingerprint) != 64 || len(proposal.EnvironmentSHA256) != 64 {
		return OnceCommandProposal{}, false, apperror.New(apperror.CodeInvalidArgument,
			"once command proposal is invalid")
	}
	argvJSON, err := json.Marshal(proposal.Argv)
	if err != nil {
		return OnceCommandProposal{}, false, err
	}
	keysJSON, err := json.Marshal(proposal.EnvironmentKeys)
	if err != nil {
		return OnceCommandProposal{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return OnceCommandProposal{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT proposal_id FROM once_command_proposal_operations
		WHERE operation_key_digest = ?`, operation.KeyDigest).Scan(&existingID)
	if err == nil {
		stored, found, err := getOnceCommandProposal(ctx, tx, existingID)
		if err != nil {
			return OnceCommandProposal{}, false, err
		}
		if !found || stored.RequestFingerprint != proposal.RequestFingerprint {
			return OnceCommandProposal{}, false, apperror.New(apperror.CodeConflict,
				"once command proposal operation key was already used for different intent")
		}
		if err := tx.Commit(); err != nil {
			return OnceCommandProposal{}, false, err
		}
		return stored, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return OnceCommandProposal{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO once_command_proposals
		(id, protocol_version, operation_key_digest, request_fingerprint, run_id,
		root_agent_id, session_id, workspace_id, executable_path, argv_json,
		working_directory, environment_keys_json, environment_sha256,
		timeout_milliseconds, purpose, spec_fingerprint, status, created_at)
		VALUES (?, 'once_command.v1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'proposed', ?)`,
		proposal.ID, operation.KeyDigest, proposal.RequestFingerprint, proposal.RunID,
		proposal.RootAgentID, proposal.SessionID, proposal.WorkspaceID,
		proposal.ExecutablePath, string(argvJSON), proposal.WorkingDirectory,
		string(keysJSON), proposal.EnvironmentSHA256, proposal.TimeoutMilliseconds,
		proposal.Purpose, proposal.SpecFingerprint, ts(proposal.CreatedAt)); err != nil {
		return OnceCommandProposal{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO once_command_proposal_operations
		(operation_key_digest, request_fingerprint, proposal_id)
		VALUES (?, ?, ?)`, operation.KeyDigest, operation.RequestFingerprint, proposal.ID); err != nil {
		return OnceCommandProposal{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return OnceCommandProposal{}, false, err
	}
	stored, _, err := getOnceCommandProposal(ctx, s.db, proposal.ID)
	return stored, false, err
}

func (s *SQLiteStore) GetOnceCommandProposal(ctx context.Context, id string) (OnceCommandProposal, bool, error) {
	return getOnceCommandProposal(ctx, s.db, id)
}

func (s *SQLiteStore) ListOnceCommandProposals(ctx context.Context, runID string, limit int) ([]OnceCommandProposal, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM once_command_proposals
		WHERE run_id = ? ORDER BY created_at DESC, id LIMIT ?`, strings.TrimSpace(runID), limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// SQLiteStore intentionally uses one connection. Release the result set
	// before loading each complete record or the nested query waits forever
	// for the connection held by rows.
	if err := rows.Close(); err != nil {
		return nil, err
	}
	proposals := make([]OnceCommandProposal, 0, len(ids))
	for _, id := range ids {
		proposal, found, err := getOnceCommandProposal(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		if found {
			proposals = append(proposals, proposal)
		}
	}
	return proposals, nil
}

// ReviewOnceCommandProposal is the immutable operator decision. Approving
// binds the approval fingerprint; the proposal can never be edited again.
func (s *SQLiteStore) ReviewOnceCommandProposal(ctx context.Context, proposalID,
	decision, reviewer, reason, approvalFingerprint string, now time.Time,
) (OnceCommandProposal, bool, error) {
	if decision != "approve" && decision != "deny" {
		return OnceCommandProposal{}, false, apperror.New(apperror.CodeInvalidArgument,
			"once command proposal review decision is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return OnceCommandProposal{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	proposal, found, err := getOnceCommandProposal(ctx, tx, proposalID)
	if err != nil {
		return OnceCommandProposal{}, false, err
	}
	if !found {
		return OnceCommandProposal{}, false, apperror.New(apperror.CodeNotFound,
			"once command proposal was not found")
	}
	if proposal.Status != "proposed" {
		return OnceCommandProposal{}, false, apperror.New(apperror.CodeConflict,
			"once command proposal was already reviewed")
	}
	status := "denied"
	if decision == "approve" {
		status = "approved"
		if len(approvalFingerprint) != 64 {
			return OnceCommandProposal{}, false, apperror.New(apperror.CodeInvalidArgument,
				"once command approval fingerprint is invalid")
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE once_command_proposals SET status = ?, reviewer = ?,
		review_reason = ?, reviewed_at = ?, approval_fingerprint = ? WHERE id = ? AND status = 'proposed'`,
		status, reviewer, reason, ts(now), approvalFingerprint, proposalID); err != nil {
		return OnceCommandProposal{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return OnceCommandProposal{}, false, err
	}
	updated, _, err := getOnceCommandProposal(ctx, s.db, proposalID)
	return updated, err == nil && updated.Status == status, err
}

// MarkOnceCommandProposalExecuted transitions approved → executed with the
// exact execution request fingerprint.
func (s *SQLiteStore) MarkOnceCommandProposalExecuted(ctx context.Context, proposalID,
	executionRequestFingerprint string,
) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE once_command_proposals SET status = 'executed',
		execution_request_fingerprint = ? WHERE id = ? AND status = 'approved'`,
		executionRequestFingerprint, proposalID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return apperror.New(apperror.CodeConflict,
			"once command proposal is not in the approved state")
	}
	return tx.Commit()
}

func getOnceCommandProposal(ctx context.Context, queryer skillPackageQueryer, id string) (OnceCommandProposal, bool, error) {
	row := queryer.QueryRowContext(ctx, `SELECT id, protocol_version, operation_key_digest,
		request_fingerprint, run_id, root_agent_id, session_id, workspace_id,
		executable_path, argv_json, working_directory, environment_keys_json,
		environment_sha256, timeout_milliseconds, purpose, spec_fingerprint, status,
		reviewer, review_reason, reviewed_at, approval_fingerprint,
		execution_request_fingerprint, created_at FROM once_command_proposals WHERE id = ?`, id)
	var proposal OnceCommandProposal
	var argvJSON, keysJSON, reviewedAt sql.NullString
	var created string
	err := row.Scan(&proposal.ID, &proposal.ProtocolVersion, &proposal.OperationKeyDigest,
		&proposal.RequestFingerprint, &proposal.RunID, &proposal.RootAgentID,
		&proposal.SessionID, &proposal.WorkspaceID, &proposal.ExecutablePath, &argvJSON,
		&proposal.WorkingDirectory, &keysJSON, &proposal.EnvironmentSHA256,
		&proposal.TimeoutMilliseconds, &proposal.Purpose, &proposal.SpecFingerprint,
		&proposal.Status, &proposal.Reviewer, &proposal.ReviewReason, &reviewedAt,
		&proposal.ApprovalFingerprint, &proposal.ExecutionRequestFingerprint, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return OnceCommandProposal{}, false, nil
	}
	if err != nil {
		return OnceCommandProposal{}, false, err
	}
	if err := json.Unmarshal([]byte(argvJSON.String), &proposal.Argv); err != nil {
		return OnceCommandProposal{}, false, err
	}
	if err := json.Unmarshal([]byte(keysJSON.String), &proposal.EnvironmentKeys); err != nil {
		return OnceCommandProposal{}, false, err
	}
	if reviewedAt.Valid {
		if parsed := parseTS(reviewedAt.String); !parsed.IsZero() {
			proposal.ReviewedAt = &parsed
		}
	}
	proposal.CreatedAt = parseTS(created)
	return proposal, true, nil
}
