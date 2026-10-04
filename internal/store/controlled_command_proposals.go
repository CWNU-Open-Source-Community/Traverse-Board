package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

const controlledCommandProposalSelect = `SELECT id, protocol_version,
	policy_version, run_id, mission_id, session_id, workspace_id, root_agent_id,
	interaction_snapshot_id, interaction_revision, execution_profile_revision,
	permission_snapshot_id, permission_revision, permission_mode, plan_id,
	plan_fingerprint, kind, relative_path, timeout_millis, purpose, requested_by,
	instruction_authorized, execution_authorized, capability_grant,
	proposal_fingerprint, created_at FROM controlled_command_proposals`

const controlledCommandProposalReviewSelect = `SELECT id, protocol_version,
	policy_version, proposal_id, proposal_fingerprint, run_id, mission_id,
	session_id, workspace_id, decision, reviewed_by, reason,
	operation_key_digest, request_fingerprint,
	single_use_execution_authorized, capability_grant, created_at
	FROM controlled_command_proposal_reviews`

const controlledCommandProposalResultSelect = `SELECT id, protocol_version,
	policy_version, proposal_id, proposal_fingerprint, review_id, request_id,
	run_id, mission_id, session_id, workspace_id, session_message_id, status,
	source_kind, source_ref, content_sha256, instruction_authorized,
	raw_output_persisted, automatic_retry_allowed, created_at
	FROM controlled_command_proposal_results`

type controlledCommandProposalQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *SQLiteStore) GetControlledCommandProposal(
	ctx context.Context,
	id string,
) (runner.ControlledCommandProposal, error) {
	id = strings.TrimSpace(id)
	if !domain.ValidAgentID(id) || strings.ContainsRune(id, 0) {
		return runner.ControlledCommandProposal{}, apperror.New(
			apperror.CodeInvalidArgument,
			"controlled command proposal id is invalid")
	}
	return getControlledCommandProposal(ctx, s.db, id)
}

func (s *SQLiteStore) ListControlledCommandProposals(
	ctx context.Context,
	runID string,
	limit int,
) ([]runner.ControlledCommandProposal, error) {
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || strings.ContainsRune(runID, 0) ||
		limit <= 0 || limit > 100 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"controlled command proposal list requires a valid Run and limit from 1 to 100")
	}
	rows, err := s.db.QueryContext(ctx, controlledCommandProposalSelect+
		` WHERE run_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`,
		runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]runner.ControlledCommandProposal, 0)
	for rows.Next() {
		proposal, err := scanControlledCommandProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, proposal)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetControlledCommandProposalReview(
	ctx context.Context,
	proposalID string,
) (runner.ControlledCommandProposalReview, bool, error) {
	return getControlledCommandProposalReviewByProposal(
		ctx, s.db, strings.TrimSpace(proposalID))
}

func (s *SQLiteStore) GetControlledCommandProposalResult(
	ctx context.Context,
	proposalID string,
) (runner.ControlledCommandProposalResult, bool, error) {
	return getControlledCommandProposalResult(
		ctx, s.db, strings.TrimSpace(proposalID))
}

func getControlledCommandProposal(
	ctx context.Context,
	queryer controlledCommandProposalQueryer,
	id string,
) (runner.ControlledCommandProposal, error) {
	return scanControlledCommandProposal(queryer.QueryRowContext(
		ctx, controlledCommandProposalSelect+` WHERE id = ?`, id))
}

func scanControlledCommandProposal(
	scanner interface{ Scan(...any) error },
) (runner.ControlledCommandProposal, error) {
	var proposal runner.ControlledCommandProposal
	var permissionMode, kind, createdAt string
	if err := scanner.Scan(
		&proposal.ID, &proposal.ProtocolVersion, &proposal.PolicyVersion,
		&proposal.RunID, &proposal.MissionID, &proposal.SessionID,
		&proposal.WorkspaceID, &proposal.RootAgentID,
		&proposal.InteractionSnapshotID, &proposal.InteractionRevision,
		&proposal.ExecutionProfileRevision, &proposal.PermissionSnapshotID,
		&proposal.PermissionRevision, &permissionMode, &proposal.PlanID,
		&proposal.PlanFingerprint, &kind, &proposal.RelativePath,
		&proposal.TimeoutMilliseconds, &proposal.Purpose,
		&proposal.RequestedBy, &proposal.InstructionAuthorized,
		&proposal.ExecutionAuthorized, &proposal.CapabilityGrant,
		&proposal.Fingerprint, &createdAt); err != nil {
		return runner.ControlledCommandProposal{}, err
	}
	parsedMode, err := domain.ParseRunExecutionPermissionMode(permissionMode)
	if err != nil {
		return runner.ControlledCommandProposal{}, err
	}
	parsedKind, err := runner.ParseControlledCommandKind(kind)
	if err != nil {
		return runner.ControlledCommandProposal{}, err
	}
	proposal.PermissionMode = parsedMode
	proposal.Kind = parsedKind
	proposal.CreatedAt = parseTS(createdAt)
	if err := proposal.Validate(); err != nil {
		return runner.ControlledCommandProposal{}, fmt.Errorf(
			"stored controlled command proposal is invalid: %w", err)
	}
	return proposal, nil
}

func getControlledCommandProposalReviewByProposal(
	ctx context.Context,
	queryer controlledCommandProposalQueryer,
	proposalID string,
) (runner.ControlledCommandProposalReview, bool, error) {
	return scanControlledCommandProposalReview(queryer.QueryRowContext(
		ctx, controlledCommandProposalReviewSelect+` WHERE proposal_id = ?`,
		proposalID))
}

func scanControlledCommandProposalReview(
	scanner interface{ Scan(...any) error },
) (runner.ControlledCommandProposalReview, bool, error) {
	var review runner.ControlledCommandProposalReview
	var decision, createdAt string
	err := scanner.Scan(&review.ID, &review.ProtocolVersion,
		&review.PolicyVersion, &review.ProposalID,
		&review.ProposalFingerprint, &review.RunID, &review.MissionID,
		&review.SessionID, &review.WorkspaceID, &decision,
		&review.ReviewedBy, &review.Reason, &review.OperationKeyDigest,
		&review.RequestFingerprint, &review.SingleUseExecutionAuthorized,
		&review.CapabilityGrant, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.ControlledCommandProposalReview{}, false, nil
	}
	if err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	review.Decision = runner.ControlledCommandReviewDecision(decision)
	review.CreatedAt = parseTS(createdAt)
	if err := review.Validate(); err != nil {
		return runner.ControlledCommandProposalReview{}, false, fmt.Errorf(
			"stored controlled command proposal review is invalid: %w", err)
	}
	return review, true, nil
}

func getControlledCommandProposalResult(
	ctx context.Context,
	queryer controlledCommandProposalQueryer,
	proposalID string,
) (runner.ControlledCommandProposalResult, bool, error) {
	var result runner.ControlledCommandProposalResult
	var status, createdAt string
	err := queryer.QueryRowContext(ctx, controlledCommandProposalResultSelect+
		` WHERE proposal_id = ?`, proposalID).Scan(
		&result.ID, &result.ProtocolVersion, &result.PolicyVersion,
		&result.ProposalID, &result.ProposalFingerprint, &result.ReviewID,
		&result.RequestID, &result.RunID, &result.MissionID,
		&result.SessionID, &result.WorkspaceID, &result.SessionMessageID,
		&status, &result.SourceKind, &result.SourceRef,
		&result.ContentSHA256, &result.InstructionAuthorized,
		&result.RawOutputPersisted, &result.AutomaticRetryAllowed, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.ControlledCommandProposalResult{}, false, nil
	}
	if err != nil {
		return runner.ControlledCommandProposalResult{}, false, err
	}
	result.Status = runner.ControlledCommandProposalResultStatus(status)
	result.CreatedAt = parseTS(createdAt)
	if err := result.Validate(); err != nil {
		return runner.ControlledCommandProposalResult{}, false, fmt.Errorf(
			"stored controlled command proposal result is invalid: %w", err)
	}
	return result, true, nil
}
