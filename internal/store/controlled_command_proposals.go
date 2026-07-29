package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
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

func (s *SQLiteStore) CreateControlledCommandProposal(
	ctx context.Context,
	operation runner.ControlledCommandProposalOperation,
	proposal runner.ControlledCommandProposal,
) (runner.ControlledCommandProposal, bool, error) {
	if err := operation.Validate(); err != nil {
		return runner.ControlledCommandProposal{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument,
				"controlled command proposal operation is invalid", err)
	}
	if err := proposal.Validate(); err != nil ||
		redact.String(proposal.Purpose) != proposal.Purpose {
		return runner.ControlledCommandProposal{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument,
				"controlled command proposal is invalid", err)
	}
	if operation.ProposalID != proposal.ID ||
		operation.RunID != proposal.RunID ||
		operation.SessionID != proposal.SessionID ||
		operation.WorkspaceID != proposal.WorkspaceID ||
		operation.RootAgentID != proposal.RootAgentID ||
		operation.RequestedBy != proposal.RequestedBy ||
		operation.RequestFingerprint !=
			runner.ControlledCommandProposalRequestFingerprint(proposal) {
		return runner.ControlledCommandProposal{}, false,
			apperror.New(apperror.CodeInvalidArgument,
				"controlled command proposal operation does not match its proposal")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.ControlledCommandProposal{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireStructuredMutationWriteLockTx(ctx, tx,
		operation.RunID); err != nil {
		return runner.ControlledCommandProposal{}, false, err
	}
	if err := requireRunExecutionLeaseTx(ctx, tx, operation.RunID,
		operation.LeaseID, operation.LeaseGeneration); err != nil {
		return runner.ControlledCommandProposal{}, false, err
	}
	existingOperation, found, err := getControlledCommandProposalOperation(
		ctx, tx, operation.KeyDigest)
	if err != nil {
		return runner.ControlledCommandProposal{}, false, err
	}
	if found {
		if existingOperation.RequestFingerprint != operation.RequestFingerprint ||
			existingOperation.RunID != operation.RunID ||
			existingOperation.SessionID != operation.SessionID ||
			existingOperation.WorkspaceID != operation.WorkspaceID ||
			existingOperation.RootAgentID != operation.RootAgentID ||
			existingOperation.RequestedBy != operation.RequestedBy {
			return runner.ControlledCommandProposal{}, false,
				apperror.New(apperror.CodeConflict,
					"controlled command proposal operation key was reused for different intent")
		}
		stored, err := getControlledCommandProposal(
			ctx, tx, existingOperation.ProposalID)
		if err != nil {
			return runner.ControlledCommandProposal{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return runner.ControlledCommandProposal{}, false, err
		}
		return stored, true, nil
	}
	run, mission, err := requireControlledCommandProposalBindingTx(
		ctx, tx, operation, proposal)
	if err != nil {
		return runner.ControlledCommandProposal{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO controlled_command_proposals
		(id, protocol_version, policy_version, run_id, mission_id, session_id,
		workspace_id, root_agent_id, interaction_snapshot_id,
		interaction_revision, execution_profile_revision, permission_snapshot_id,
		permission_revision, permission_mode, plan_id, plan_fingerprint, kind,
		relative_path, timeout_millis, purpose, requested_by,
		instruction_authorized, execution_authorized, capability_grant,
		proposal_fingerprint, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?)`,
		proposal.ID, proposal.ProtocolVersion, proposal.PolicyVersion,
		proposal.RunID, proposal.MissionID, proposal.SessionID,
		proposal.WorkspaceID, proposal.RootAgentID,
		proposal.InteractionSnapshotID, proposal.InteractionRevision,
		proposal.ExecutionProfileRevision, proposal.PermissionSnapshotID,
		proposal.PermissionRevision, proposal.PermissionMode, proposal.PlanID,
		proposal.PlanFingerprint, proposal.Kind, proposal.RelativePath,
		proposal.TimeoutMilliseconds, proposal.Purpose, proposal.RequestedBy,
		proposal.InstructionAuthorized, proposal.ExecutionAuthorized,
		proposal.CapabilityGrant, proposal.Fingerprint,
		ts(proposal.CreatedAt)); err != nil {
		return runner.ControlledCommandProposal{}, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO controlled_command_proposal_operations
		(operation_key_digest, request_fingerprint, invocation_id, proposal_id,
		run_id, session_id, workspace_id, root_agent_id, requested_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.KeyDigest, operation.RequestFingerprint,
		operation.InvocationID, proposal.ID, operation.RunID,
		operation.SessionID, operation.WorkspaceID, operation.RootAgentID,
		operation.RequestedBy, ts(operation.CreatedAt)); err != nil {
		return runner.ControlledCommandProposal{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.ControlledCommandProposedEvent, "controlled_command_proposal",
		proposal.ID, map[string]any{
			"protocol":                 proposal.ProtocolVersion,
			"kind":                     proposal.Kind,
			"root_agent_id":            proposal.RootAgentID,
			"permission_revision":      proposal.PermissionRevision,
			"operator_review_required": true,
			"instruction_authorized":   false,
			"execution_authorized":     false,
			"capability_grant":         false,
		}); err != nil {
		return runner.ControlledCommandProposal{}, false, err
	}
	_ = mission
	if err := tx.Commit(); err != nil {
		return runner.ControlledCommandProposal{}, false, err
	}
	return proposal, false, nil
}

func requireControlledCommandProposalBindingTx(
	ctx context.Context,
	tx *sql.Tx,
	operation runner.ControlledCommandProposalOperation,
	proposal runner.ControlledCommandProposal,
) (domain.Run, domain.Mission, error) {
	run, mission, err := getCoordinatorRunTx(ctx, tx, operation.RunID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, err
	}
	if run.Status != domain.RunRunning || run.SessionID != proposal.SessionID ||
		run.MissionID != proposal.MissionID ||
		mission.WorkspaceID != proposal.WorkspaceID {
		return domain.Run{}, domain.Mission{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"controlled command proposal requires the current running Run")
	}
	root, err := scanAgentNode(tx.QueryRowContext(ctx,
		agentNodeSelect+` WHERE id = ?`, operation.RootAgentID))
	if err != nil {
		return domain.Run{}, domain.Mission{}, err
	}
	if root.RunID != run.ID || root.Role != domain.AgentRoleRoot ||
		root.ParentID != "" || root.Status != domain.AgentRunning ||
		root.ActiveAttemptID == "" {
		return domain.Run{}, domain.Mission{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"controlled command proposal requires the active root Agent")
	}
	var checkpointCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM run_supervisor_checkpoints
		WHERE run_id = ? AND phase = 'turn_started' AND attempt_id = ?
			AND lease_id = ? AND lease_generation = ?`,
		run.ID, root.ActiveAttemptID, operation.LeaseID,
		operation.LeaseGeneration).Scan(&checkpointCount); err != nil {
		return domain.Run{}, domain.Mission{}, err
	}
	if checkpointCount != 1 {
		return domain.Run{}, domain.Mission{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"controlled command proposal is not bound to the active root turn")
	}
	var invocationCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_tool_calls
		WHERE id = ? AND run_id = ? AND session_id = ? AND workspace_id = ?
			AND tool_name = 'controlled_command_propose'
			AND action_class = 'agent_proposal'`,
		operation.InvocationID, operation.RunID, operation.SessionID,
		operation.WorkspaceID).Scan(&invocationCount); err != nil {
		return domain.Run{}, domain.Mission{}, err
	}
	if invocationCount != 1 {
		return domain.Run{}, domain.Mission{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"controlled command proposal is not backed by the Run tool budget ledger")
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, err
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, err
	}
	permission, err := getCurrentRunExecutionPermissionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, err
	}
	mode, err := getCurrentRunModeSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, err
	}
	if interaction.ID != proposal.InteractionSnapshotID ||
		interaction.Revision != proposal.InteractionRevision ||
		interaction.Mode != domain.RunExecutionInteractionControlled ||
		interaction.ExecutionProfileRevision !=
			proposal.ExecutionProfileRevision ||
		profile.Revision != proposal.ExecutionProfileRevision ||
		profile.Profile != domain.RunExecutionProfileLocal ||
		permission.ID != proposal.PermissionSnapshotID ||
		permission.Revision != proposal.PermissionRevision ||
		permission.Mode != proposal.PermissionMode ||
		mode.Surface != domain.ExecutionSurfaceCode {
		return domain.Run{}, domain.Mission{}, apperror.New(
			apperror.CodeConflict,
			"controlled command proposal durable binding is stale")
	}
	return run, mission, nil
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

func (s *SQLiteStore) ReviewControlledCommandProposal(
	ctx context.Context,
	review runner.ControlledCommandProposalReview,
) (runner.ControlledCommandProposalReview, bool, error) {
	if err := review.Validate(); err != nil ||
		redact.String(review.Reason) != review.Reason {
		return runner.ControlledCommandProposalReview{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument,
				"controlled command proposal review is invalid", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunExecutionInteractionWriteLockTx(ctx, tx,
		review.RunID); err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	existing, found, err := getControlledCommandProposalReviewByProposal(
		ctx, tx, review.ProposalID)
	if err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	if found {
		if !controlledCommandProposalReviewsEqual(existing, review) {
			return runner.ControlledCommandProposalReview{}, false,
				apperror.New(apperror.CodeConflict,
					"controlled command proposal already has a different review")
		}
		if err := tx.Commit(); err != nil {
			return runner.ControlledCommandProposalReview{}, false, err
		}
		return existing, true, nil
	}
	if other, found, err := getControlledCommandProposalReviewByOperation(
		ctx, tx, review.OperationKeyDigest); err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	} else if found {
		return runner.ControlledCommandProposalReview{}, false,
			apperror.New(apperror.CodeConflict, fmt.Sprintf(
				"review operation key already belongs to proposal %s", other.ProposalID))
	}
	proposal, err := getControlledCommandProposal(ctx, tx, review.ProposalID)
	if err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	if proposal.Fingerprint != review.ProposalFingerprint ||
		proposal.RunID != review.RunID ||
		proposal.MissionID != review.MissionID ||
		proposal.SessionID != review.SessionID ||
		proposal.WorkspaceID != review.WorkspaceID {
		return runner.ControlledCommandProposalReview{}, false,
			apperror.New(apperror.CodeConflict,
				"controlled command proposal review binding is stale")
	}
	run, _, err := getCoordinatorRunTx(ctx, tx, review.RunID)
	if err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	if run.Status != domain.RunCreated && run.Status != domain.RunPaused {
		return runner.ControlledCommandProposalReview{}, false,
			apperror.New(apperror.CodeFailedPrecondition,
				"controlled command proposal review requires a created or paused Run")
	}
	if err := requireNoActiveRunControlLeaseTx(ctx, tx, run.ID,
		review.CreatedAt); err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, run.ID)
	if err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	permission, err := getCurrentRunExecutionPermissionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	if interaction.ID != proposal.InteractionSnapshotID ||
		interaction.Revision != proposal.InteractionRevision ||
		profile.Revision != proposal.ExecutionProfileRevision ||
		permission.ID != proposal.PermissionSnapshotID ||
		permission.Revision != proposal.PermissionRevision ||
		permission.Mode != proposal.PermissionMode {
		return runner.ControlledCommandProposalReview{}, false,
			apperror.New(apperror.CodeConflict,
				"controlled command proposal cannot be reviewed after its bindings changed")
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO controlled_command_proposal_reviews
		(id, protocol_version, policy_version, proposal_id,
		proposal_fingerprint, run_id, mission_id, session_id, workspace_id,
		decision, reviewed_by, reason, operation_key_digest,
		request_fingerprint, single_use_execution_authorized, capability_grant,
		created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		review.ID, review.ProtocolVersion, review.PolicyVersion,
		review.ProposalID, review.ProposalFingerprint, review.RunID,
		review.MissionID, review.SessionID, review.WorkspaceID,
		review.Decision, review.ReviewedBy, review.Reason,
		review.OperationKeyDigest, review.RequestFingerprint,
		review.SingleUseExecutionAuthorized, review.CapabilityGrant,
		ts(review.CreatedAt)); err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.ControlledCommandProposalReviewedEvent,
		"controlled_command_proposal", review.ProposalID, map[string]any{
			"protocol":                        review.ProtocolVersion,
			"review_id":                       review.ID,
			"decision":                        review.Decision,
			"reviewed_by":                     review.ReviewedBy,
			"single_use_execution_authorized": review.SingleUseExecutionAuthorized,
			"capability_grant":                false,
		}); err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runner.ControlledCommandProposalReview{}, false, err
	}
	return review, false, nil
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

func (s *SQLiteStore) RecordControlledCommandProposalResult(
	ctx context.Context,
	proposalID string,
	reviewID string,
	resultID string,
	execution runner.ControlledExecutionResult,
	evidence session.Message,
	createdAt time.Time,
) (ControlledExecutionReceipt, runner.ControlledCommandProposalResult, bool, error) {
	proposalID = strings.TrimSpace(proposalID)
	reviewID = strings.TrimSpace(reviewID)
	resultID = strings.TrimSpace(resultID)
	createdAt = createdAt.UTC()
	if err := execution.Validate(); err != nil || createdAt.IsZero() {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument,
				"controlled command proposal execution result is invalid", err)
	}
	preparedEvidence, err := session.PrepareMessageForStorage(evidence)
	if err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument,
				"controlled command proposal evidence is invalid", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := getControlledCommandProposalResult(
		ctx, tx, proposalID)
	if err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	if found {
		if existing.ReviewID != reviewID ||
			existing.RequestID != execution.RequestID ||
			existing.ID != resultID {
			return ControlledExecutionReceipt{},
				runner.ControlledCommandProposalResult{}, false,
				apperror.New(apperror.CodeConflict,
					"controlled command proposal result conflicts with its durable record")
		}
		receipt, receiptFound, err := getControlledExecutionReceipt(
			ctx, tx, existing.RequestID)
		if err != nil {
			return ControlledExecutionReceipt{},
				runner.ControlledCommandProposalResult{}, false, err
		}
		if !receiptFound {
			return ControlledExecutionReceipt{},
				runner.ControlledCommandProposalResult{}, false,
				apperror.New(apperror.CodeInternal,
					"controlled command proposal result has no execution receipt")
		}
		if err := tx.Commit(); err != nil {
			return ControlledExecutionReceipt{},
				runner.ControlledCommandProposalResult{}, false, err
		}
		return receipt, existing, true, nil
	}
	proposal, err := getControlledCommandProposal(ctx, tx, proposalID)
	if err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	review, reviewFound, err := getControlledCommandProposalReviewByProposal(
		ctx, tx, proposalID)
	if err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	if !reviewFound || review.ID != reviewID ||
		review.Decision != runner.ControlledCommandReviewApprove ||
		!review.SingleUseExecutionAuthorized ||
		execution.PlanID != proposal.PlanID ||
		execution.PlanFingerprint != proposal.PlanFingerprint ||
		execution.RunID != proposal.RunID ||
		execution.WorkspaceID != proposal.WorkspaceID ||
		execution.InteractionSnapshotID != proposal.InteractionSnapshotID ||
		execution.InteractionRevision != proposal.InteractionRevision ||
		execution.ExecutionProfileRevision != proposal.ExecutionProfileRevision ||
		execution.Kind != proposal.Kind ||
		preparedEvidence.SessionID != proposal.SessionID ||
		preparedEvidence.Provenance.SourceKind !=
			session.SourceGoCommandResult ||
		preparedEvidence.Provenance.SourceRef !=
			"controlled-command-proposal:"+proposal.ID ||
		preparedEvidence.Provenance.InstructionAuthorized {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false,
			apperror.New(apperror.CodeConflict,
				"controlled command proposal result binding is stale or unauthorized")
	}
	receipt, _, err := recordControlledExecutionResultTx(ctx, tx, execution)
	if err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	savedEvidence, err := saveSessionMessageTx(ctx, tx, preparedEvidence)
	if err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	proposalResult, err := runner.NewControlledCommandProposalResult(
		resultID, proposal, review, execution, savedEvidence.ID,
		savedEvidence.Provenance.SourceKind,
		savedEvidence.Provenance.SourceRef,
		savedEvidence.Provenance.ContentSHA256, createdAt)
	if err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO controlled_command_proposal_results
		(id, protocol_version, policy_version, proposal_id,
		proposal_fingerprint, review_id, request_id, run_id, mission_id,
		session_id, workspace_id, session_message_id, status, source_kind,
		source_ref, content_sha256, instruction_authorized,
		raw_output_persisted, automatic_retry_allowed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		proposalResult.ID, proposalResult.ProtocolVersion,
		proposalResult.PolicyVersion, proposalResult.ProposalID,
		proposalResult.ProposalFingerprint, proposalResult.ReviewID,
		proposalResult.RequestID, proposalResult.RunID,
		proposalResult.MissionID, proposalResult.SessionID,
		proposalResult.WorkspaceID, proposalResult.SessionMessageID,
		proposalResult.Status, proposalResult.SourceKind,
		proposalResult.SourceRef, proposalResult.ContentSHA256,
		proposalResult.InstructionAuthorized,
		proposalResult.RawOutputPersisted,
		proposalResult.AutomaticRetryAllowed,
		ts(proposalResult.CreatedAt)); err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	run, _, err := getCoordinatorRunTx(ctx, tx, proposal.RunID)
	if err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.ControlledCommandProposalResultRecordedEvent,
		"controlled_command_proposal", proposal.ID, map[string]any{
			"protocol":                proposalResult.ProtocolVersion,
			"result_id":               proposalResult.ID,
			"review_id":               proposalResult.ReviewID,
			"request_id":              proposalResult.RequestID,
			"status":                  proposalResult.Status,
			"session_message_id":      proposalResult.SessionMessageID,
			"instruction_authorized":  false,
			"raw_output_persisted":    false,
			"automatic_retry_allowed": false,
		}); err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ControlledExecutionReceipt{},
			runner.ControlledCommandProposalResult{}, false, err
	}
	return receipt, proposalResult, false, nil
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

func getControlledCommandProposalOperation(
	ctx context.Context,
	queryer controlledCommandProposalQueryer,
	keyDigest string,
) (runner.ControlledCommandProposalOperation, bool, error) {
	var operation runner.ControlledCommandProposalOperation
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT operation_key_digest,
		request_fingerprint, invocation_id, proposal_id, run_id, session_id,
		workspace_id, root_agent_id, requested_by, created_at
		FROM controlled_command_proposal_operations
		WHERE operation_key_digest = ?`, keyDigest).Scan(
		&operation.KeyDigest, &operation.RequestFingerprint,
		&operation.InvocationID, &operation.ProposalID, &operation.RunID,
		&operation.SessionID, &operation.WorkspaceID, &operation.RootAgentID,
		&operation.RequestedBy, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.ControlledCommandProposalOperation{}, false, nil
	}
	if err != nil {
		return runner.ControlledCommandProposalOperation{}, false, err
	}
	operation.CreatedAt = parseTS(createdAt)
	if !validStoreDigest(operation.KeyDigest) ||
		!validStoreDigest(operation.RequestFingerprint) ||
		!domain.ValidAgentID(operation.InvocationID) ||
		!domain.ValidAgentID(operation.ProposalID) ||
		!domain.ValidAgentID(operation.RunID) ||
		!domain.ValidAgentID(operation.SessionID) ||
		!domain.ValidAgentID(operation.WorkspaceID) ||
		!domain.ValidAgentID(operation.RootAgentID) ||
		operation.RequestedBy != "run_supervisor" ||
		operation.CreatedAt.IsZero() {
		return runner.ControlledCommandProposalOperation{}, false,
			errors.New("stored controlled command proposal operation is invalid")
	}
	return operation, true, nil
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

func getControlledCommandProposalReviewByOperation(
	ctx context.Context,
	queryer controlledCommandProposalQueryer,
	operationDigest string,
) (runner.ControlledCommandProposalReview, bool, error) {
	return scanControlledCommandProposalReview(queryer.QueryRowContext(
		ctx, controlledCommandProposalReviewSelect+
			` WHERE operation_key_digest = ?`, operationDigest))
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

func controlledCommandProposalReviewsEqual(
	left runner.ControlledCommandProposalReview,
	right runner.ControlledCommandProposalReview,
) bool {
	return left.ID == right.ID &&
		left.ProtocolVersion == right.ProtocolVersion &&
		left.PolicyVersion == right.PolicyVersion &&
		left.ProposalID == right.ProposalID &&
		left.ProposalFingerprint == right.ProposalFingerprint &&
		left.RunID == right.RunID && left.MissionID == right.MissionID &&
		left.SessionID == right.SessionID &&
		left.WorkspaceID == right.WorkspaceID &&
		left.Decision == right.Decision &&
		left.ReviewedBy == right.ReviewedBy &&
		left.Reason == right.Reason &&
		left.OperationKeyDigest == right.OperationKeyDigest &&
		left.RequestFingerprint == right.RequestFingerprint &&
		left.SingleUseExecutionAuthorized ==
			right.SingleUseExecutionAuthorized &&
		left.CapabilityGrant == right.CapabilityGrant
}
