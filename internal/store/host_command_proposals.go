package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

type hostCommandProposalQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type hostCommandProposalOperationRecord struct {
	KeyDigest          string
	RequestFingerprint string
	ProposalID         string
	RunID              string
	SessionID          string
	WorkspaceID        string
	RootAgentID        string
	RequestedBy        string
}

func (s *SQLiteStore) CreateHostCommandProposal(ctx context.Context,
	operation runner.HostCommandProposalOperation,
	proposal runner.HostCommandProposal,
) (runner.HostCommandProposal, bool, error) {
	if err := operation.Validate(); err != nil {
		return runner.HostCommandProposal{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "host command proposal operation is invalid", err)
	}
	if err := proposal.Validate(); err != nil {
		return runner.HostCommandProposal{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "host command proposal is invalid", err)
	}
	if err := runner.ValidateHostCommandProposalTransport(proposal.Spec); err != nil {
		return runner.HostCommandProposal{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "host command proposal transport is invalid", err)
	}
	if operation.ProposalID != proposal.ID || operation.RunID != proposal.RunID ||
		operation.SessionID != proposal.SessionID ||
		operation.WorkspaceID != proposal.WorkspaceID ||
		operation.RootAgentID != proposal.RootAgentID ||
		operation.RequestedBy != proposal.RequestedBy ||
		operation.RequestFingerprint != runner.HostCommandProposalRequestFingerprint(proposal) {
		return runner.HostCommandProposal{}, false, apperror.New(
			apperror.CodeInvalidArgument, "host command proposal operation does not match its proposal")
	}
	payload, err := marshalHostCommandRecord(proposal)
	if err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireStructuredMutationWriteLockTx(ctx, tx, operation.RunID); err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	if err := requireRunExecutionLeaseTx(ctx, tx, operation.RunID,
		operation.LeaseID, operation.LeaseGeneration); err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	existingOperation, found, err := getHostCommandProposalOperation(ctx, tx, operation.KeyDigest)
	if err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	if found {
		if existingOperation.RequestFingerprint != operation.RequestFingerprint ||
			existingOperation.RunID != operation.RunID ||
			existingOperation.SessionID != operation.SessionID ||
			existingOperation.WorkspaceID != operation.WorkspaceID ||
			existingOperation.RootAgentID != operation.RootAgentID ||
			existingOperation.RequestedBy != operation.RequestedBy {
			return runner.HostCommandProposal{}, false, apperror.New(
				apperror.CodeConflict, "host command proposal operation key was reused for different intent")
		}
		stored, err := getHostCommandProposal(ctx, tx, existingOperation.ProposalID)
		if err != nil {
			return runner.HostCommandProposal{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return runner.HostCommandProposal{}, false, err
		}
		return stored, true, nil
	}
	run, err := requireHostCommandProposalBindingTx(ctx, tx, operation, proposal)
	if err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO host_command_proposals
		(id, run_id, mission_id, session_id, workspace_id, root_agent_id,
		interaction_snapshot_id, interaction_revision, execution_profile_revision,
		permission_snapshot_id, permission_revision, permission_mode,
		spec_fingerprint, requested_by, instruction_authorized,
		execution_authorized, capability_grant, proposal_fingerprint,
		payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		proposal.ID, proposal.RunID, proposal.MissionID, proposal.SessionID,
		proposal.WorkspaceID, proposal.RootAgentID, proposal.InteractionSnapshotID,
		proposal.InteractionRevision, proposal.ExecutionProfileRevision,
		proposal.PermissionSnapshotID, proposal.PermissionRevision,
		proposal.PermissionMode, proposal.Spec.Fingerprint, proposal.RequestedBy,
		proposal.InstructionAuthorized, proposal.ExecutionAuthorized,
		proposal.CapabilityGrant, proposal.Fingerprint, payload,
		ts(proposal.CreatedAt)); err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO host_command_proposal_operations
		(operation_key_digest, request_fingerprint, invocation_id, proposal_id,
		run_id, session_id, workspace_id, root_agent_id, requested_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, operation.KeyDigest,
		operation.RequestFingerprint, operation.InvocationID, proposal.ID,
		operation.RunID, operation.SessionID, operation.WorkspaceID,
		operation.RootAgentID, operation.RequestedBy, ts(operation.CreatedAt)); err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run, events.HostCommandProposedEvent,
		"host_command_proposal", proposal.ID, map[string]any{
			"protocol": proposal.ProtocolVersion, "spec_fingerprint": proposal.Spec.Fingerprint,
			"operator_review_required": true, "execution_authorized": false,
			"capability_grant": false,
		}); err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runner.HostCommandProposal{}, false, err
	}
	return proposal, false, nil
}

func requireHostCommandProposalBindingTx(ctx context.Context, tx *sql.Tx,
	operation runner.HostCommandProposalOperation,
	proposal runner.HostCommandProposal,
) (domain.Run, error) {
	run, mission, err := getCoordinatorRunTx(ctx, tx, operation.RunID)
	if err != nil {
		return domain.Run{}, err
	}
	if run.Status != domain.RunRunning || run.SessionID != proposal.SessionID ||
		run.MissionID != proposal.MissionID || mission.WorkspaceID != proposal.WorkspaceID {
		return domain.Run{}, apperror.New(apperror.CodeFailedPrecondition,
			"host command proposal requires the current running Run")
	}
	root, err := scanAgentNode(tx.QueryRowContext(ctx, agentNodeSelect+` WHERE id = ?`, operation.RootAgentID))
	if err != nil {
		return domain.Run{}, err
	}
	if root.RunID != run.ID || root.Role != domain.AgentRoleRoot || root.ParentID != "" ||
		root.Status != domain.AgentRunning || root.ActiveAttemptID == "" {
		return domain.Run{}, apperror.New(apperror.CodeFailedPrecondition,
			"host command proposal requires the active root Agent")
	}
	var checkpointCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_supervisor_checkpoints
		WHERE run_id = ? AND phase = 'turn_started' AND attempt_id = ?
		AND lease_id = ? AND lease_generation = ?`, run.ID, root.ActiveAttemptID,
		operation.LeaseID, operation.LeaseGeneration).Scan(&checkpointCount); err != nil {
		return domain.Run{}, err
	}
	if checkpointCount != 1 {
		return domain.Run{}, apperror.New(apperror.CodeFailedPrecondition,
			"host command proposal is not bound to the active root turn")
	}
	var invocationCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_tool_calls
		WHERE id = ? AND run_id = ? AND session_id = ? AND workspace_id = ?
		AND tool_name = 'host_command_propose' AND action_class = 'agent_proposal'`,
		operation.InvocationID, operation.RunID, operation.SessionID,
		operation.WorkspaceID).Scan(&invocationCount); err != nil {
		return domain.Run{}, err
	}
	if invocationCount != 1 {
		return domain.Run{}, apperror.New(apperror.CodeFailedPrecondition,
			"host command proposal is not backed by the Run tool budget ledger")
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, err
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, err
	}
	permission, err := getCurrentRunExecutionPermissionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, err
	}
	mode, err := getCurrentRunModeSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, err
	}
	if interaction.ID != proposal.InteractionSnapshotID ||
		interaction.Revision != proposal.InteractionRevision ||
		interaction.Mode != domain.RunExecutionInteractionControlled ||
		interaction.ExecutionProfileRevision != proposal.ExecutionProfileRevision ||
		profile.Revision != proposal.ExecutionProfileRevision ||
		profile.Profile != domain.RunExecutionProfileLocal ||
		permission.ID != proposal.PermissionSnapshotID ||
		permission.Revision != proposal.PermissionRevision ||
		permission.Mode != domain.RunExecutionPermissionApproval ||
		mode.Surface != domain.ExecutionSurfaceCode {
		return domain.Run{}, apperror.New(apperror.CodeConflict,
			"host command proposal durable binding is stale")
	}
	var triggerBindingCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM runs run
		JOIN missions mission ON mission.id = run.mission_id
		JOIN agent_nodes root ON root.id = ?
		JOIN run_execution_interaction_snapshots interaction ON interaction.id = ?
		JOIN run_execution_permission_snapshots permission ON permission.id = ?
		WHERE run.id = ? AND run.mission_id = ? AND run.session_id = ?
			AND run.status = 'running' AND mission.workspace_id = ?
			AND root.run_id = ? AND root.role = 'root' AND root.parent_id IS NULL
			AND root.status = 'running'
			AND interaction.run_id = ? AND interaction.revision = ?
			AND interaction.execution_profile_revision = ?
			AND interaction.mode = 'controlled'
			AND permission.run_id = ? AND permission.revision = ?
			AND permission.mode = 'approval'`,
		proposal.RootAgentID, proposal.InteractionSnapshotID,
		proposal.PermissionSnapshotID, proposal.RunID, proposal.MissionID,
		proposal.SessionID, proposal.WorkspaceID, proposal.RunID,
		proposal.RunID, proposal.InteractionRevision,
		proposal.ExecutionProfileRevision, proposal.RunID,
		proposal.PermissionRevision).Scan(&triggerBindingCount); err != nil {
		return domain.Run{}, err
	}
	if triggerBindingCount != 1 {
		return domain.Run{}, apperror.New(apperror.CodeConflict,
			"host command proposal database binding is stale")
	}
	return run, nil
}

func (s *SQLiteStore) GetHostCommandProposal(ctx context.Context, id string) (runner.HostCommandProposal, error) {
	id = strings.TrimSpace(id)
	if !domain.ValidAgentID(id) || strings.ContainsRune(id, 0) {
		return runner.HostCommandProposal{}, apperror.New(apperror.CodeInvalidArgument,
			"host command proposal id is invalid")
	}
	return getHostCommandProposal(ctx, s.db, id)
}

func (s *SQLiteStore) ListHostCommandProposals(ctx context.Context, runID string,
	limit int,
) ([]runner.HostCommandProposal, error) {
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || limit <= 0 || limit > 100 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"host command proposal list requires a valid Run and limit from 1 to 100")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM host_command_proposals
		WHERE run_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]runner.HostCommandProposal, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		proposal, err := decodeHostCommandProposal(payload)
		if err != nil {
			return nil, err
		}
		result = append(result, proposal)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ReviewHostCommandProposal(ctx context.Context,
	review runner.HostCommandReview,
) (runner.HostCommandReview, bool, error) {
	if err := review.Validate(); err != nil || redact.String(review.Reason) != review.Reason {
		return runner.HostCommandReview{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "host command proposal review is invalid", err)
	}
	payload, err := marshalHostCommandRecord(review)
	if err != nil {
		return runner.HostCommandReview{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.HostCommandReview{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunExecutionInteractionWriteLockTx(ctx, tx, review.RunID); err != nil {
		return runner.HostCommandReview{}, false, err
	}
	existing, found, err := getHostCommandReviewByProposal(ctx, tx, review.ProposalID)
	if err != nil {
		return runner.HostCommandReview{}, false, err
	}
	if found {
		if existing.Fingerprint != review.Fingerprint {
			return runner.HostCommandReview{}, false, apperror.New(
				apperror.CodeConflict, "host command proposal already has a different review")
		}
		if err := tx.Commit(); err != nil {
			return runner.HostCommandReview{}, false, err
		}
		return existing, true, nil
	}
	proposal, err := getHostCommandProposal(ctx, tx, review.ProposalID)
	if err != nil {
		return runner.HostCommandReview{}, false, err
	}
	if proposal.Fingerprint != review.ProposalFingerprint || proposal.RunID != review.RunID {
		return runner.HostCommandReview{}, false, apperror.New(
			apperror.CodeConflict, "host command proposal review binding is stale")
	}
	run, _, err := getCoordinatorRunTx(ctx, tx, review.RunID)
	if err != nil {
		return runner.HostCommandReview{}, false, err
	}
	if run.Status != domain.RunCreated && run.Status != domain.RunPaused {
		return runner.HostCommandReview{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "host command review requires a created or paused Run")
	}
	if err := requireNoActiveRunControlLeaseTx(ctx, tx, run.ID, review.CreatedAt); err != nil {
		return runner.HostCommandReview{}, false, err
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return runner.HostCommandReview{}, false, err
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, run.ID)
	if err != nil {
		return runner.HostCommandReview{}, false, err
	}
	permission, err := getCurrentRunExecutionPermissionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return runner.HostCommandReview{}, false, err
	}
	if interaction.ID != proposal.InteractionSnapshotID ||
		interaction.Revision != proposal.InteractionRevision ||
		profile.Revision != proposal.ExecutionProfileRevision ||
		permission.ID != proposal.PermissionSnapshotID ||
		permission.Revision != proposal.PermissionRevision ||
		permission.Mode != domain.RunExecutionPermissionApproval {
		return runner.HostCommandReview{}, false, apperror.New(
			apperror.CodeConflict, "host command proposal cannot be reviewed after its bindings changed")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO host_command_proposal_reviews
		(id, proposal_id, proposal_fingerprint, run_id, decision, reviewed_by,
		operation_key_digest, request_fingerprint, single_use_execution_authorized,
		capability_grant, review_fingerprint, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, review.ID,
		review.ProposalID, review.ProposalFingerprint, review.RunID, review.Decision,
		review.ReviewedBy, review.OperationKeyDigest, review.RequestFingerprint,
		review.SingleUseExecutionAuthorized, review.CapabilityGrant,
		review.Fingerprint, payload, ts(review.CreatedAt)); err != nil {
		return runner.HostCommandReview{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.HostCommandProposalReviewedEvent, "host_command_proposal",
		review.ProposalID, map[string]any{
			"review_id": review.ID, "decision": review.Decision,
			"reviewed_by":                     review.ReviewedBy,
			"single_use_execution_authorized": review.SingleUseExecutionAuthorized,
			"capability_grant":                false,
		}); err != nil {
		return runner.HostCommandReview{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runner.HostCommandReview{}, false, err
	}
	return review, false, nil
}

func (s *SQLiteStore) GetHostCommandProposalReview(ctx context.Context,
	proposalID string,
) (runner.HostCommandReview, bool, error) {
	return getHostCommandReviewByProposal(ctx, s.db, strings.TrimSpace(proposalID))
}

func getHostCommandProposal(ctx context.Context, queryer hostCommandProposalQueryer,
	id string,
) (runner.HostCommandProposal, error) {
	var payload string
	if err := queryer.QueryRowContext(ctx,
		`SELECT payload_json FROM host_command_proposals WHERE id = ?`, id).Scan(&payload); err != nil {
		return runner.HostCommandProposal{}, err
	}
	return decodeHostCommandProposal(payload)
}

func decodeHostCommandProposal(payload string) (runner.HostCommandProposal, error) {
	var proposal runner.HostCommandProposal
	if err := json.Unmarshal([]byte(payload), &proposal); err != nil {
		return runner.HostCommandProposal{}, err
	}
	if err := proposal.Validate(); err != nil {
		return runner.HostCommandProposal{}, fmt.Errorf("stored host command proposal is invalid: %w", err)
	}
	if err := runner.ValidateHostCommandProposalTransport(proposal.Spec); err != nil {
		return runner.HostCommandProposal{}, fmt.Errorf("stored host command proposal transport is invalid: %w", err)
	}
	return proposal, nil
}

func getHostCommandProposalOperation(ctx context.Context,
	queryer hostCommandProposalQueryer, key string,
) (hostCommandProposalOperationRecord, bool, error) {
	var record hostCommandProposalOperationRecord
	err := queryer.QueryRowContext(ctx, `SELECT operation_key_digest,
		request_fingerprint, proposal_id, run_id, session_id, workspace_id,
		root_agent_id, requested_by FROM host_command_proposal_operations
		WHERE operation_key_digest = ?`, key).Scan(&record.KeyDigest,
		&record.RequestFingerprint, &record.ProposalID, &record.RunID,
		&record.SessionID, &record.WorkspaceID, &record.RootAgentID,
		&record.RequestedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return hostCommandProposalOperationRecord{}, false, nil
	}
	return record, err == nil, err
}

func getHostCommandReviewByProposal(ctx context.Context,
	queryer hostCommandProposalQueryer, proposalID string,
) (runner.HostCommandReview, bool, error) {
	var payload string
	err := queryer.QueryRowContext(ctx,
		`SELECT payload_json FROM host_command_proposal_reviews WHERE proposal_id = ?`,
		proposalID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.HostCommandReview{}, false, nil
	}
	if err != nil {
		return runner.HostCommandReview{}, false, err
	}
	var review runner.HostCommandReview
	if err := json.Unmarshal([]byte(payload), &review); err != nil {
		return runner.HostCommandReview{}, false, err
	}
	if err := review.Validate(); err != nil {
		return runner.HostCommandReview{}, false, fmt.Errorf("stored host command review is invalid: %w", err)
	}
	return review, true, nil
}

func marshalHostCommandRecord(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	payload := string(encoded)
	if redact.String(payload) != payload {
		return "", apperror.New(apperror.CodeInvalidArgument,
			"host command durable record contains secret-like data")
	}
	return payload, nil
}

func hostCommandRecordFingerprint(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *SQLiteStore) PrepareHostCommandProposalExecutionIntent(ctx context.Context,
	intent runner.HostExecutionIntent,
) (bool, error) {
	if err := intent.Validate(); err != nil ||
		intent.PermissionMode != domain.RunExecutionPermissionApproval {
		return false, apperror.Wrap(apperror.CodeInvalidArgument,
			"approved host command execution intent is invalid", err)
	}
	payload, err := marshalHostCommandRecord(intent)
	if err != nil {
		return false, err
	}
	fingerprint := runner.HostExecutionIntentFingerprint(intent)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunExecutionInteractionWriteLockTx(ctx, tx, intent.RunID); err != nil {
		return false, err
	}
	if existing, found, err := getHostCommandProposalExecutionIntent(ctx, tx,
		intent.RequestID); err != nil {
		return false, err
	} else if found {
		if runner.HostExecutionIntentFingerprint(existing) != fingerprint {
			return false, apperror.New(apperror.CodeConflict,
				"approved host execution intent conflicts with its durable record")
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	proposal, err := getHostCommandProposal(ctx, tx, intent.AuthorizationProposalID)
	if err != nil {
		return false, err
	}
	review, found, err := getHostCommandReviewByProposal(ctx, tx, proposal.ID)
	if err != nil {
		return false, err
	}
	if !found || review.ID != intent.AuthorizationReviewID ||
		review.Fingerprint != intent.AuthorizationReviewFingerprint ||
		review.Decision != runner.HostCommandReviewApprove ||
		proposal.Fingerprint != intent.AuthorizationProposalFingerprint ||
		proposal.Spec.Fingerprint != intent.Spec.Fingerprint {
		return false, apperror.New(apperror.CodeConflict,
			"approved host execution intent is not bound to its proposal and review")
	}
	run, mission, err := getCoordinatorRunTx(ctx, tx, intent.RunID)
	if err != nil {
		return false, err
	}
	if (run.Status != domain.RunCreated && run.Status != domain.RunPaused) ||
		mission.WorkspaceID != intent.WorkspaceID {
		return false, apperror.New(apperror.CodeFailedPrecondition,
			"approved host execution requires a created or paused Run")
	}
	if err := requireNoActiveRunControlLeaseTx(ctx, tx, run.ID, intent.CreatedAt); err != nil {
		return false, err
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return false, err
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, run.ID)
	if err != nil {
		return false, err
	}
	permission, err := getCurrentRunExecutionPermissionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return false, err
	}
	if interaction.ID != intent.InteractionSnapshotID ||
		interaction.Revision != intent.InteractionRevision ||
		profile.Revision != intent.ExecutionProfileRevision ||
		permission.ID != intent.PermissionSnapshotID ||
		permission.Revision != intent.PermissionRevision ||
		permission.Mode != domain.RunExecutionPermissionApproval {
		return false, apperror.New(apperror.CodeConflict,
			"approved host execution durable binding is stale")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO host_command_proposal_execution_intents
		(request_id, proposal_id, review_id, operation_key_digest, run_id,
		session_id, workspace_id, permission_mode, spec_fingerprint,
		intent_fingerprint, non_sandboxed, automatic_retry_allowed,
		payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		intent.RequestID, proposal.ID, review.ID, intent.OperationKeyDigest,
		intent.RunID, intent.SessionID, intent.WorkspaceID, intent.PermissionMode,
		intent.Spec.Fingerprint, fingerprint, intent.NonSandboxed,
		intent.AutomaticRetryAllowed, payload, ts(intent.CreatedAt)); err != nil {
		return false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.HostCommandExecutionPreparedEvent, "host_command_proposal",
		proposal.ID, map[string]any{
			"request_id": intent.RequestID, "permission_mode": "approval",
			"non_sandboxed": true, "automatic_retry_allowed": false,
			"environment_values_persisted": false, "raw_output_persisted": false,
		}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *SQLiteStore) GetHostCommandProposalExecutionIntent(ctx context.Context,
	requestID string,
) (runner.HostExecutionIntent, bool, error) {
	return getHostCommandProposalExecutionIntent(ctx, s.db, strings.TrimSpace(requestID))
}

func getHostCommandProposalExecutionIntent(ctx context.Context,
	queryer hostCommandProposalQueryer, requestID string,
) (runner.HostExecutionIntent, bool, error) {
	var payload string
	err := queryer.QueryRowContext(ctx, `SELECT payload_json
		FROM host_command_proposal_execution_intents WHERE request_id = ?`,
		requestID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.HostExecutionIntent{}, false, nil
	}
	if err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	var intent runner.HostExecutionIntent
	if err := json.Unmarshal([]byte(payload), &intent); err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	if err := intent.Validate(); err != nil ||
		intent.PermissionMode != domain.RunExecutionPermissionApproval {
		return runner.HostExecutionIntent{}, false, fmt.Errorf(
			"stored approved host execution intent is invalid: %w", err)
	}
	return intent, true, nil
}

func (s *SQLiteStore) GetHostCommandProposalResult(ctx context.Context,
	proposalID string,
) (runner.HostCommandProposalResult, bool, error) {
	result, _, found, err := getHostCommandProposalResult(ctx, s.db,
		strings.TrimSpace(proposalID))
	return result, found, err
}

func (s *SQLiteStore) GetHostCommandProposalReceipt(ctx context.Context,
	requestID string,
) (runner.HostExecutionReceipt, bool, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT receipt_json
		FROM host_command_proposal_results WHERE request_id = ?`,
		strings.TrimSpace(requestID)).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.HostExecutionReceipt{}, false, nil
	}
	if err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	var receipt runner.HostExecutionReceipt
	if err := json.Unmarshal([]byte(payload), &receipt); err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	if err := receipt.Validate(); err != nil {
		return runner.HostExecutionReceipt{}, false, err
	}
	return receipt, true, nil
}

func (s *SQLiteStore) RecordHostCommandProposalResult(ctx context.Context,
	proposalID string, reviewID string, resultID string,
	execution runner.HostExecutionResult, evidence session.Message,
	createdAt time.Time,
) (runner.HostExecutionReceipt, runner.HostCommandProposalResult, bool, error) {
	if err := execution.Validate(); err != nil ||
		execution.PermissionMode != domain.RunExecutionPermissionApproval {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false,
			apperror.Wrap(apperror.CodeInvalidArgument, "approved host execution result is invalid", err)
	}
	preparedEvidence, err := session.PrepareMessageForStorage(evidence)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	receipt, err := runner.ProjectHostExecutionReceipt(execution)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, existingReceipt, found, err := getHostCommandProposalResult(
		ctx, tx, proposalID); err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	} else if found {
		if existing.ID != resultID || existing.ReviewID != reviewID ||
			existing.RequestID != execution.RequestID || existingReceipt != receipt {
			return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false,
				apperror.New(apperror.CodeConflict, "host command result conflicts with its durable record")
		}
		if err := tx.Commit(); err != nil {
			return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
		}
		return existingReceipt, existing, true, nil
	}
	proposal, err := getHostCommandProposal(ctx, tx, proposalID)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	review, found, err := getHostCommandReviewByProposal(ctx, tx, proposalID)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	if !found {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false,
			apperror.New(apperror.CodeFailedPrecondition,
				"host command result requires a durable operator review")
	}
	intent, found, err := getHostCommandProposalExecutionIntent(ctx, tx, execution.RequestID)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	if !found {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false,
			apperror.New(apperror.CodeFailedPrecondition,
				"host command result requires a durable execution intent")
	}
	if review.ID != reviewID || review.Decision != runner.HostCommandReviewApprove ||
		intent.AuthorizationProposalID != proposal.ID ||
		intent.AuthorizationReviewID != review.ID ||
		execution.AuthorizationProposalID != proposal.ID ||
		execution.AuthorizationReviewID != review.ID ||
		execution.SpecFingerprint != proposal.Spec.Fingerprint ||
		preparedEvidence.SessionID != proposal.SessionID ||
		preparedEvidence.Provenance.SourceKind != session.SourceGoCommandResult ||
		preparedEvidence.Provenance.SourceRef != "host-command-proposal:"+proposal.ID ||
		preparedEvidence.Provenance.InstructionAuthorized {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false,
			apperror.New(apperror.CodeConflict, "host command result binding is stale or unauthorized")
	}
	savedEvidence, err := saveSessionMessageTx(ctx, tx, preparedEvidence)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	status := "completed"
	if execution.ExitCode != 0 || execution.TimedOut || execution.Cancelled ||
		execution.OutputLimitExceeded {
		status = "failed"
	}
	proposalResult, err := runner.NewHostCommandProposalResult(resultID, proposal,
		review, execution.RequestID, status, savedEvidence.Provenance.SourceKind,
		savedEvidence.Provenance.SourceRef, savedEvidence.Provenance.ContentSHA256,
		createdAt.UTC())
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	resultPayload, err := marshalHostCommandRecord(proposalResult)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	receiptPayload, err := marshalHostCommandRecord(receipt)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	receiptFingerprint := hostCommandRecordFingerprint(receipt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO host_command_proposal_results
		(id, proposal_id, review_id, request_id, run_id, session_id,
		session_message_id, status, result_fingerprint, receipt_fingerprint,
		result_json, receipt_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, proposalResult.ID,
		proposal.ID, review.ID, execution.RequestID, proposal.RunID,
		proposal.SessionID, savedEvidence.ID, proposalResult.Status,
		proposalResult.Fingerprint, receiptFingerprint, resultPayload,
		receiptPayload, ts(proposalResult.CreatedAt)); err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id,
		session_id, status, config_json, budget_json, started_at, finished_at,
		created_at, updated_at FROM runs WHERE id = ?`, proposal.RunID))
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.HostCommandProposalResultRecordedEvent, "host_command_proposal",
		proposal.ID, map[string]any{
			"result_id": proposalResult.ID, "request_id": execution.RequestID,
			"status": status, "exit_code": receipt.ExitCode,
			"raw_output_persisted": false, "instruction_authorized": false,
			"automatic_retry_allowed": false,
		}); err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runner.HostExecutionReceipt{}, runner.HostCommandProposalResult{}, false, err
	}
	return receipt, proposalResult, false, nil
}

func getHostCommandProposalResult(ctx context.Context,
	queryer hostCommandProposalQueryer, proposalID string,
) (runner.HostCommandProposalResult, runner.HostExecutionReceipt, bool, error) {
	var resultPayload, receiptPayload string
	err := queryer.QueryRowContext(ctx, `SELECT result_json, receipt_json
		FROM host_command_proposal_results WHERE proposal_id = ?`, proposalID).
		Scan(&resultPayload, &receiptPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.HostCommandProposalResult{}, runner.HostExecutionReceipt{}, false, nil
	}
	if err != nil {
		return runner.HostCommandProposalResult{}, runner.HostExecutionReceipt{}, false, err
	}
	var result runner.HostCommandProposalResult
	var receipt runner.HostExecutionReceipt
	if err := json.Unmarshal([]byte(resultPayload), &result); err != nil {
		return runner.HostCommandProposalResult{}, runner.HostExecutionReceipt{}, false, err
	}
	if err := json.Unmarshal([]byte(receiptPayload), &receipt); err != nil {
		return runner.HostCommandProposalResult{}, runner.HostExecutionReceipt{}, false, err
	}
	if err := result.Validate(); err != nil {
		return runner.HostCommandProposalResult{}, runner.HostExecutionReceipt{}, false, err
	}
	if err := receipt.Validate(); err != nil || result.RequestID != receipt.RequestID {
		return runner.HostCommandProposalResult{}, runner.HostExecutionReceipt{}, false,
			fmt.Errorf("stored host command receipt is invalid: %w", err)
	}
	return result, receipt, true, nil
}
