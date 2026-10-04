package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"cyberagent-workbench/internal/runner"
)

// Proposal domain types live in the runner package so the application layer
// can depend on them without a store import cycle.
type OnceCommandProposal = runner.OnceCommandProposal
type OnceCommandProposalOperation = runner.OnceCommandProposalOperation

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

// MarkOnceCommandProposalExecuted transitions approved → executed with the
// exact execution request fingerprint.

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
