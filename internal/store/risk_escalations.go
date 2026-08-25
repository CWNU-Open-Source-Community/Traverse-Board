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
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
)

type riskEscalationOperationRecord struct {
	RequestFingerprint   string
	ProposalID           string
	RunID                string
	SessionID            string
	WorkspaceID          string
	RootAgentID          string
	SupervisorTurn       int
	SupervisorToolCallID string
	RequestedBy          string
}

func (s *SQLiteStore) CreateRiskEscalationProposal(ctx context.Context,
	operation runner.RiskEscalationOperation, proposal runner.RiskEscalationProposal,
) (runner.RiskEscalationProposal, bool, error) {
	if operation.Validate() != nil || proposal.Validate() != nil ||
		operation.ProposalID != proposal.ID || operation.RunID != proposal.RunID ||
		operation.SessionID != proposal.SessionID ||
		operation.WorkspaceID != proposal.WorkspaceID ||
		operation.RootAgentID != proposal.RootAgentID ||
		operation.SupervisorTurn != proposal.SupervisorTurn ||
		operation.SupervisorToolCallID != proposal.SupervisorToolCallID ||
		operation.InvocationID != proposal.ToolInvocationID ||
		operation.RequestedBy != proposal.RequestedBy ||
		operation.RequestFingerprint != runner.RiskEscalationProposalRequestFingerprint(proposal) {
		return runner.RiskEscalationProposal{}, false, apperror.New(
			apperror.CodeInvalidArgument, "risk escalation operation does not match its proposal")
	}
	payload, err := marshalHostCommandRecord(proposal)
	if err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireStructuredMutationWriteLockTx(ctx, tx, operation.RunID); err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	if err := requireRunExecutionLeaseTx(ctx, tx, operation.RunID,
		operation.LeaseID, operation.LeaseGeneration); err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	existingOperation, found, err := getRiskEscalationOperationTx(ctx, tx, operation.KeyDigest)
	if err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	if found {
		if existingOperation.RequestFingerprint != operation.RequestFingerprint ||
			existingOperation.RunID != operation.RunID ||
			existingOperation.SessionID != operation.SessionID ||
			existingOperation.WorkspaceID != operation.WorkspaceID ||
			existingOperation.RootAgentID != operation.RootAgentID ||
			existingOperation.SupervisorTurn != operation.SupervisorTurn ||
			existingOperation.SupervisorToolCallID != operation.SupervisorToolCallID ||
			existingOperation.RequestedBy != operation.RequestedBy {
			return runner.RiskEscalationProposal{}, false, apperror.New(
				apperror.CodeConflict, "risk escalation operation key was reused for different intent")
		}
		stored, err := getRiskEscalationProposal(ctx, tx, existingOperation.ProposalID)
		if err != nil {
			return runner.RiskEscalationProposal{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return runner.RiskEscalationProposal{}, false, err
		}
		return stored, true, nil
	}
	run, err := requireRiskEscalationProposalBindingTx(ctx, tx, operation, proposal)
	if err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO risk_escalation_proposals
		(id, run_id, mission_id, session_id, workspace_id, root_agent_id,
		supervisor_turn, supervisor_tool_call_id, tool_invocation_id,
		mode_snapshot_id, mode_revision, interaction_snapshot_id, interaction_revision,
		execution_profile_snapshot_id, execution_profile_revision,
		permission_snapshot_id, permission_revision, permission_mode,
		workspace_root_fingerprint, capability_generation, spec_fingerprint,
		scope_fingerprint, proposal_fingerprint, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		proposal.ID, proposal.RunID, proposal.MissionID, proposal.SessionID,
		proposal.WorkspaceID, proposal.RootAgentID, proposal.SupervisorTurn,
		proposal.SupervisorToolCallID, proposal.ToolInvocationID, proposal.ModeSnapshotID,
		proposal.ModeRevision, proposal.InteractionSnapshotID, proposal.InteractionRevision,
		proposal.ExecutionProfileSnapshotID, proposal.ExecutionProfileRevision,
		proposal.PermissionSnapshotID, proposal.PermissionRevision, proposal.PermissionMode,
		proposal.WorkspaceRootFingerprint, proposal.CapabilityGeneration,
		proposal.Spec.Fingerprint, proposal.Scope.Fingerprint, proposal.Fingerprint,
		payload, ts(proposal.CreatedAt)); err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO risk_escalation_operations
		(operation_key_digest, request_fingerprint, proposal_id, run_id, session_id,
		workspace_id, root_agent_id, supervisor_turn, supervisor_tool_call_id,
		requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.KeyDigest, operation.RequestFingerprint, proposal.ID, proposal.RunID,
		proposal.SessionID, proposal.WorkspaceID, proposal.RootAgentID,
		proposal.SupervisorTurn, proposal.SupervisorToolCallID, proposal.RequestedBy,
		ts(operation.CreatedAt)); err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	approvalRecord, _, err := ensureApprovalTx(ctx, tx, approval.Proposal{
		IdempotencyKey: approval.ProposalIdempotencyKey(
			"host_command_propose", proposal.ID),
		ProposalID: proposal.ID, SessionID: proposal.SessionID,
		WorkspaceID: proposal.WorkspaceID, ToolName: "host_command_propose",
		ActionClass: "risk_escalation", Mode: "per_call", Status: approval.StatusPending,
		RequestFingerprint: proposal.Fingerprint, RequestedBy: proposal.RequestedBy,
		CreatedAt: proposal.CreatedAt, UpdatedAt: proposal.CreatedAt,
	})
	if err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run, events.RiskEscalationProposedEvent,
		"risk_escalation", proposal.ID, map[string]any{
			"protocol": proposal.ProtocolVersion, "approval_id": approvalRecord.ID,
			"supervisor_turn":          proposal.SupervisorTurn,
			"supervisor_tool_call_id":  proposal.SupervisorToolCallID,
			"tool_invocation_id":       proposal.ToolInvocationID,
			"spec_fingerprint":         proposal.Spec.Fingerprint,
			"scope_fingerprint":        proposal.Scope.Fingerprint,
			"operator_review_required": true, "execution_authorized": false,
		}); err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	if err := transitionSupervisorRunTx(ctx, tx, &run, domain.RunWaitingApproval,
		"exact risk escalation "+proposal.ID+" is awaiting operator approval",
		proposal.CreatedAt); err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runner.RiskEscalationProposal{}, false, err
	}
	return proposal, false, nil
}

func requireRiskEscalationProposalBindingTx(ctx context.Context, tx *sql.Tx,
	operation runner.RiskEscalationOperation, proposal runner.RiskEscalationProposal,
) (domain.Run, error) {
	run, mission, err := getCoordinatorRunTx(ctx, tx, proposal.RunID)
	if err != nil {
		return domain.Run{}, err
	}
	if run.Status != domain.RunRunning || run.MissionID != proposal.MissionID ||
		run.SessionID != proposal.SessionID || mission.WorkspaceID != proposal.WorkspaceID {
		return domain.Run{}, apperror.New(apperror.CodeFailedPrecondition,
			"risk escalation requires the current running Run")
	}
	root, err := scanAgentNode(tx.QueryRowContext(ctx, agentNodeSelect+` WHERE id = ?`,
		proposal.RootAgentID))
	if err != nil {
		return domain.Run{}, err
	}
	if root.RunID != run.ID || root.Role != domain.AgentRoleRoot || root.ParentID != "" ||
		root.Status != domain.AgentRunning || root.ActiveAttemptID == "" {
		return domain.Run{}, apperror.New(apperror.CodeFailedPrecondition,
			"risk escalation requires the active root Agent")
	}
	var callCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM run_supervisor_tool_calls call
		JOIN run_supervisor_checkpoints checkpoint
			ON checkpoint.run_id = call.run_id AND checkpoint.attempt_id = call.attempt_id
		WHERE call.run_id = ? AND call.turn = ? AND call.call_id = ?
			AND call.tool_name = 'host_command_propose' AND call.status = 'pending'
			AND call.attempt_id = ? AND checkpoint.phase = 'turn_started'
			AND checkpoint.lease_id = ? AND checkpoint.lease_generation = ?`,
		proposal.RunID, proposal.SupervisorTurn, proposal.SupervisorToolCallID,
		root.ActiveAttemptID, operation.LeaseID, operation.LeaseGeneration).
		Scan(&callCount); err != nil {
		return domain.Run{}, err
	}
	if callCount != 1 {
		return domain.Run{}, apperror.New(apperror.CodeFailedPrecondition,
			"risk escalation is not bound to the exact pending Supervisor tool call")
	}
	var invocationCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_tool_calls
		WHERE id = ? AND run_id = ? AND session_id = ? AND workspace_id = ?
		AND tool_name = 'host_command_propose' AND action_class = 'agent_proposal'`,
		proposal.ToolInvocationID, proposal.RunID, proposal.SessionID,
		proposal.WorkspaceID).Scan(&invocationCount); err != nil {
		return domain.Run{}, err
	}
	if invocationCount != 1 {
		return domain.Run{}, apperror.New(apperror.CodeFailedPrecondition,
			"risk escalation is not backed by the Run tool budget ledger")
	}
	mode, err := getCurrentRunModeSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, err
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
	if mode.ID != proposal.ModeSnapshotID || mode.Revision != proposal.ModeRevision ||
		mode.Surface != domain.ExecutionSurfaceCode ||
		interaction.ID != proposal.InteractionSnapshotID ||
		interaction.Revision != proposal.InteractionRevision ||
		interaction.Mode != domain.RunExecutionInteractionControlled ||
		interaction.ExecutionProfileRevision != proposal.ExecutionProfileRevision ||
		profile.ID != proposal.ExecutionProfileSnapshotID ||
		profile.Revision != proposal.ExecutionProfileRevision ||
		permission.ID != proposal.PermissionSnapshotID ||
		permission.Revision != proposal.PermissionRevision ||
		permission.Mode != domain.RunExecutionPermissionWorkspaceAccess {
		return domain.Run{}, apperror.New(apperror.CodeConflict,
			"risk escalation durable capability binding is stale")
	}
	return run, nil
}

func (s *SQLiteStore) GetRiskEscalationProposal(ctx context.Context,
	id string,
) (runner.RiskEscalationProposal, error) {
	id = strings.TrimSpace(id)
	if !domain.ValidAgentID(id) {
		return runner.RiskEscalationProposal{}, apperror.New(
			apperror.CodeInvalidArgument, "risk escalation proposal id is invalid")
	}
	return getRiskEscalationProposal(ctx, s.db, id)
}

func (s *SQLiteStore) ListRiskEscalationProposals(ctx context.Context,
	runID string, limit int,
) ([]runner.RiskEscalationProposal, error) {
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || limit <= 0 || limit > 100 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"risk escalation list requires a valid Run and limit from 1 to 100")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json
		FROM risk_escalation_proposals WHERE run_id = ?
		ORDER BY created_at DESC, id DESC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]runner.RiskEscalationProposal, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		proposal, err := decodeRiskEscalationProposal(payload)
		if err != nil {
			return nil, err
		}
		result = append(result, proposal)
	}
	return result, rows.Err()
}

func getRiskEscalationProposal(ctx context.Context, queryer hostCommandProposalQueryer,
	id string,
) (runner.RiskEscalationProposal, error) {
	var payload string
	if err := queryer.QueryRowContext(ctx, `SELECT payload_json
		FROM risk_escalation_proposals WHERE id = ?`, id).Scan(&payload); err != nil {
		return runner.RiskEscalationProposal{}, err
	}
	return decodeRiskEscalationProposal(payload)
}

func decodeRiskEscalationProposal(payload string) (runner.RiskEscalationProposal, error) {
	var proposal runner.RiskEscalationProposal
	if err := json.Unmarshal([]byte(payload), &proposal); err != nil {
		return runner.RiskEscalationProposal{}, err
	}
	if err := proposal.Validate(); err != nil {
		return runner.RiskEscalationProposal{}, fmt.Errorf(
			"stored risk escalation proposal is invalid: %w", err)
	}
	return proposal, nil
}

func getRiskEscalationOperationTx(ctx context.Context, tx *sql.Tx,
	key string,
) (riskEscalationOperationRecord, bool, error) {
	var value riskEscalationOperationRecord
	err := tx.QueryRowContext(ctx, `SELECT request_fingerprint, proposal_id,
		run_id, session_id, workspace_id, root_agent_id, supervisor_turn,
		supervisor_tool_call_id, requested_by FROM risk_escalation_operations
		WHERE operation_key_digest = ?`, key).Scan(&value.RequestFingerprint,
		&value.ProposalID, &value.RunID, &value.SessionID, &value.WorkspaceID,
		&value.RootAgentID, &value.SupervisorTurn, &value.SupervisorToolCallID,
		&value.RequestedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return riskEscalationOperationRecord{}, false, nil
	}
	return value, err == nil, err
}

func (s *SQLiteStore) PrepareRiskEscalationExecutionIntent(ctx context.Context,
	intent runner.HostExecutionIntent, authorization runner.RiskEscalationAuthorization,
) (bool, error) {
	if intent.Validate() != nil || authorization.Validate() != nil ||
		intent.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess ||
		intent.AuthorizationProposalID != authorization.ProposalID ||
		intent.AuthorizationProposalFingerprint != authorization.ProposalFingerprint ||
		intent.AuthorizationReviewID != authorization.ApprovalID ||
		intent.AuthorizationReviewFingerprint !=
			runner.RiskEscalationAuthorizationFingerprint(authorization) {
		return false, apperror.New(apperror.CodeInvalidArgument,
			"risk escalation execution intent is invalid")
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
	if existing, found, err := getRiskEscalationIntent(ctx, tx, intent.RequestID); err != nil {
		return false, err
	} else if found {
		if runner.HostExecutionIntentFingerprint(existing) != fingerprint {
			return false, apperror.New(apperror.CodeConflict,
				"risk escalation execution intent conflicts with its durable record")
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	proposal, err := getRiskEscalationProposal(ctx, tx, authorization.ProposalID)
	if err != nil {
		return false, err
	}
	record, err := getApprovalTx(ctx, tx, "", proposal.ID)
	if err != nil {
		return false, err
	}
	expectedApprovalReviewer := authorization.ReviewedBy
	if authorization.GrantID != "" {
		expectedApprovalReviewer = "session_grant"
	}
	if record.ID != authorization.ApprovalID ||
		record.Status != approval.StatusApproved ||
		record.Version != authorization.ApprovalVersion ||
		approval.RecordFingerprint(record) != authorization.ApprovalFingerprint ||
		record.ReviewedBy != expectedApprovalReviewer ||
		proposal.Fingerprint != authorization.ProposalFingerprint ||
		proposal.Scope.Fingerprint != authorization.ScopeFingerprint ||
		proposal.Spec.Fingerprint != intent.Spec.Fingerprint {
		return false, apperror.New(apperror.CodeConflict,
			"risk escalation intent is not bound to the exact approved proposal")
	}
	if authorization.GrantID != "" {
		grant, grantErr := getSessionGrantTx(ctx, tx, authorization.GrantID)
		if grantErr != nil {
			return false, grantErr
		}
		if grant.GrantedBy != authorization.ReviewedBy ||
			grant.Generation != authorization.GrantGeneration {
			return false, apperror.New(apperror.CodeConflict,
				"risk escalation grant operator or generation binding is stale")
		}
		consumption, found, err := getGrantConsumptionByProposalTx(ctx, tx, proposal.ID)
		if err != nil {
			return false, err
		}
		if !found || consumption.ID != authorization.GrantConsumptionID ||
			consumption.GrantID != authorization.GrantID ||
			consumption.GrantGeneration != authorization.GrantGeneration ||
			consumption.ScopeFingerprint != authorization.ScopeFingerprint {
			return false, apperror.New(apperror.CodeConflict,
				"risk escalation grant consumption binding is stale")
		}
	}
	if _, found, err := getRiskEscalationInvalidationTx(ctx, tx, proposal.ID); err != nil {
		return false, err
	} else if found {
		return false, apperror.New(apperror.CodeFailedPrecondition,
			"risk escalation proposal is invalidated")
	}
	var run domain.Run
	run, err = scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id, session_id,
		status, config_json, budget_json, started_at, finished_at, created_at, updated_at
		FROM runs WHERE id = ?`, proposal.RunID))
	if err != nil {
		return false, err
	}
	if run.Status != domain.RunWaitingApproval {
		return false, apperror.New(apperror.CodeFailedPrecondition,
			"risk escalation execution requires a waiting Run")
	}
	if err := requireNoActiveRunControlLeaseTx(ctx, tx, run.ID, intent.CreatedAt); err != nil {
		return false, err
	}
	grantID := any(nil)
	consumptionID := any(nil)
	if authorization.GrantID != "" {
		grantID = authorization.GrantID
		consumptionID = authorization.GrantConsumptionID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO risk_escalation_execution_intents
		(request_id, proposal_id, approval_id, grant_id, grant_consumption_id,
		authorization_fingerprint, intent_fingerprint, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, intent.RequestID, proposal.ID,
		authorization.ApprovalID, grantID, consumptionID,
		runner.RiskEscalationAuthorizationFingerprint(authorization), fingerprint,
		payload, ts(intent.CreatedAt)); err != nil {
		return false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.RiskEscalationExecutionPreparedEvent, "risk_escalation", proposal.ID,
		map[string]any{"request_id": intent.RequestID,
			"approval_id": authorization.ApprovalID, "grant_id": authorization.GrantID,
			"grant_consumption_id":    authorization.GrantConsumptionID,
			"automatic_retry_allowed": false, "environment_values_persisted": false,
			"raw_output_persisted": false}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *SQLiteStore) GetRiskEscalationExecutionIntent(ctx context.Context,
	requestID string,
) (runner.HostExecutionIntent, bool, error) {
	return getRiskEscalationIntent(ctx, s.db, strings.TrimSpace(requestID))
}

func (s *SQLiteStore) GetRiskEscalationExecutionIntentByProposal(ctx context.Context,
	proposalID string,
) (runner.HostExecutionIntent, bool, error) {
	var requestID string
	err := s.db.QueryRowContext(ctx, `SELECT request_id
		FROM risk_escalation_execution_intents WHERE proposal_id = ?`,
		strings.TrimSpace(proposalID)).Scan(&requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.HostExecutionIntent{}, false, nil
	}
	if err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	return getRiskEscalationIntent(ctx, s.db, requestID)
}

func getRiskEscalationIntent(ctx context.Context, queryer hostCommandProposalQueryer,
	requestID string,
) (runner.HostExecutionIntent, bool, error) {
	var payload string
	err := queryer.QueryRowContext(ctx, `SELECT payload_json
		FROM risk_escalation_execution_intents WHERE request_id = ?`, requestID).
		Scan(&payload)
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
		intent.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess {
		return runner.HostExecutionIntent{}, false, fmt.Errorf(
			"stored risk escalation execution intent is invalid: %w", err)
	}
	return intent, true, nil
}

func (s *SQLiteStore) RecordRiskEscalationResult(ctx context.Context,
	proposalID string, authorization runner.RiskEscalationAuthorization,
	resultID string, execution runner.HostExecutionResult, evidence session.Message,
	createdAt time.Time,
) (runner.HostExecutionReceipt, runner.RiskEscalationResult, bool, error) {
	if execution.Validate() != nil || authorization.Validate() != nil ||
		execution.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false,
			apperror.New(apperror.CodeInvalidArgument,
				"risk escalation execution result is invalid")
	}
	preparedEvidence, err := session.PrepareMessageForStorage(evidence)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	receipt, err := runner.ProjectHostExecutionReceipt(execution)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, existingReceipt, found, err := getRiskEscalationResult(
		ctx, tx, proposalID); err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	} else if found {
		if existing.ID != resultID || existing.RequestID != execution.RequestID ||
			existingReceipt != receipt {
			return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false,
				apperror.New(apperror.CodeConflict,
					"risk escalation result conflicts with its durable record")
		}
		if err := tx.Commit(); err != nil {
			return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
		}
		return existingReceipt, existing, true, nil
	}
	proposal, err := getRiskEscalationProposal(ctx, tx, proposalID)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	intent, found, err := getRiskEscalationIntent(ctx, tx, execution.RequestID)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	authorizationFingerprint := runner.RiskEscalationAuthorizationFingerprint(authorization)
	if !found || authorization.ProposalID != proposal.ID ||
		authorization.ProposalFingerprint != proposal.Fingerprint ||
		authorization.ScopeFingerprint != proposal.Scope.Fingerprint ||
		intent.AuthorizationProposalID != proposal.ID ||
		intent.AuthorizationProposalFingerprint != proposal.Fingerprint ||
		intent.AuthorizationReviewID != authorization.ApprovalID ||
		intent.AuthorizationReviewFingerprint != authorizationFingerprint ||
		intent.Spec.Fingerprint != proposal.Spec.Fingerprint ||
		execution.AuthorizationProposalID != proposal.ID ||
		execution.AuthorizationProposalFingerprint != proposal.Fingerprint ||
		execution.AuthorizationReviewID != authorization.ApprovalID ||
		execution.AuthorizationReviewFingerprint != authorizationFingerprint ||
		execution.OperationKeyDigest != intent.OperationKeyDigest ||
		execution.RunID != intent.RunID || execution.MissionID != intent.MissionID ||
		execution.SessionID != intent.SessionID ||
		execution.WorkspaceID != intent.WorkspaceID ||
		execution.InteractionSnapshotID != intent.InteractionSnapshotID ||
		execution.InteractionRevision != intent.InteractionRevision ||
		execution.ExecutionProfileRevision != intent.ExecutionProfileRevision ||
		execution.PermissionSnapshotID != intent.PermissionSnapshotID ||
		execution.PermissionRevision != intent.PermissionRevision ||
		execution.PermissionMode != intent.PermissionMode ||
		execution.SpecFingerprint != proposal.Spec.Fingerprint ||
		preparedEvidence.SessionID != proposal.SessionID ||
		preparedEvidence.Provenance.SourceKind != session.SourceGoCommandResult ||
		preparedEvidence.Provenance.SourceRef != "risk-escalation:"+proposal.ID ||
		preparedEvidence.Provenance.InstructionAuthorized {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false,
			apperror.New(apperror.CodeConflict,
				"risk escalation result binding is stale or unauthorized")
	}
	savedEvidence, err := saveSessionMessageTx(ctx, tx, preparedEvidence)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	status := "completed"
	errorCode := ""
	if execution.ExitCode != 0 || execution.TimedOut || execution.Cancelled ||
		execution.OutputLimitExceeded {
		status = "failed"
		errorCode = "execution_failed"
	}
	result, err := runner.NewRiskEscalationResult(resultID, proposal, authorization,
		execution.RequestID, status, errorCode, savedEvidence.Provenance.SourceKind,
		savedEvidence.Provenance.SourceRef, savedEvidence.Provenance.ContentSHA256,
		false, createdAt)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	resultPayload, err := marshalHostCommandRecord(result)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	receiptPayload, err := marshalHostCommandRecord(receipt)
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO risk_escalation_results
		(id, proposal_id, approval_id, request_id, run_id, session_id,
		session_message_id, status, error_code, result_fingerprint,
		receipt_fingerprint, result_json, receipt_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, result.ID,
		proposal.ID, authorization.ApprovalID, execution.RequestID, proposal.RunID,
		proposal.SessionID, savedEvidence.ID, result.Status, result.ErrorCode,
		result.Fingerprint, hostCommandRecordFingerprint(receipt), resultPayload,
		receiptPayload, ts(result.CreatedAt)); err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id, session_id,
		status, config_json, budget_json, started_at, finished_at, created_at, updated_at
		FROM runs WHERE id = ?`, proposal.RunID))
	if err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run,
		events.RiskEscalationExecutionCompletedEvent, "risk_escalation", proposal.ID,
		map[string]any{"result_id": result.ID, "request_id": execution.RequestID,
			"approval_id":          authorization.ApprovalID,
			"grant_id":             authorization.GrantID,
			"grant_consumption_id": authorization.GrantConsumptionID,
			"status":               status, "exit_code": receipt.ExitCode,
			"raw_output_persisted": false, "instruction_authorized": false,
			"automatic_retry_allowed": false}); err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runner.HostExecutionReceipt{}, runner.RiskEscalationResult{}, false, err
	}
	return receipt, result, false, nil
}

func (s *SQLiteStore) GetRiskEscalationResult(ctx context.Context,
	proposalID string,
) (runner.RiskEscalationResult, bool, error) {
	result, _, found, err := getRiskEscalationResult(ctx, s.db,
		strings.TrimSpace(proposalID))
	return result, found, err
}

func (s *SQLiteStore) GetRiskEscalationReceipt(ctx context.Context,
	requestID string,
) (runner.HostExecutionReceipt, bool, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT receipt_json FROM risk_escalation_results
		WHERE request_id = ?`, strings.TrimSpace(requestID)).Scan(&payload)
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

func getRiskEscalationResult(ctx context.Context, queryer hostCommandProposalQueryer,
	proposalID string,
) (runner.RiskEscalationResult, runner.HostExecutionReceipt, bool, error) {
	var resultPayload, receiptPayload string
	err := queryer.QueryRowContext(ctx, `SELECT result_json, receipt_json
		FROM risk_escalation_results WHERE proposal_id = ?`, proposalID).
		Scan(&resultPayload, &receiptPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.RiskEscalationResult{}, runner.HostExecutionReceipt{}, false, nil
	}
	if err != nil {
		return runner.RiskEscalationResult{}, runner.HostExecutionReceipt{}, false, err
	}
	var result runner.RiskEscalationResult
	var receipt runner.HostExecutionReceipt
	if err := json.Unmarshal([]byte(resultPayload), &result); err != nil {
		return runner.RiskEscalationResult{}, runner.HostExecutionReceipt{}, false, err
	}
	if err := json.Unmarshal([]byte(receiptPayload), &receipt); err != nil {
		return runner.RiskEscalationResult{}, runner.HostExecutionReceipt{}, false, err
	}
	if err := result.Validate(); err != nil {
		return runner.RiskEscalationResult{}, runner.HostExecutionReceipt{}, false, err
	}
	if err := receipt.Validate(); err != nil || result.RequestID != receipt.RequestID {
		return runner.RiskEscalationResult{}, runner.HostExecutionReceipt{}, false,
			fmt.Errorf("stored risk escalation receipt is invalid: %w", err)
	}
	return result, receipt, true, nil
}

func (s *SQLiteStore) InvalidateRiskEscalation(ctx context.Context,
	invalidation runner.RiskEscalationInvalidation,
) (runner.RiskEscalationInvalidation, bool, error) {
	if err := invalidation.Validate(); err != nil {
		return runner.RiskEscalationInvalidation{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "risk escalation invalidation is invalid", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, err := getRiskEscalationInvalidationTx(
		ctx, tx, invalidation.ProposalID); err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	} else if found {
		if existing.Fingerprint != invalidation.Fingerprint {
			return runner.RiskEscalationInvalidation{}, false, apperror.New(
				apperror.CodeConflict, "risk escalation already has a different invalidation")
		}
		if err := tx.Commit(); err != nil {
			return runner.RiskEscalationInvalidation{}, false, err
		}
		return existing, true, nil
	}
	proposal, err := getRiskEscalationProposal(ctx, tx, invalidation.ProposalID)
	if err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	}
	if invalidation.GrantID != "" {
		grant, grantErr := getSessionGrantTx(ctx, tx, invalidation.GrantID)
		if grantErr != nil {
			return runner.RiskEscalationInvalidation{}, false, grantErr
		}
		if grant.RunID != proposal.RunID ||
			grant.ScopeFingerprint != proposal.Scope.Fingerprint {
			return runner.RiskEscalationInvalidation{}, false, apperror.New(
				apperror.CodeConflict, "risk escalation invalidation grant binding is stale")
		}
		if grant.Status == approval.GrantActive {
			eventType := events.ApprovalGrantInvalidatedEvent
			if invalidation.ReasonCode == "expired" {
				eventType = events.ApprovalGrantExpiredEvent
			}
			if err := endBoundedGrantTx(ctx, tx, &grant, invalidation.ReasonCode,
				invalidation.Detail, "approval_store", invalidation.CreatedAt,
				eventType); err != nil {
				return runner.RiskEscalationInvalidation{}, false, err
			}
		}
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id, session_id,
		status, config_json, budget_json, started_at, finished_at, created_at, updated_at
		FROM runs WHERE id = ?`, proposal.RunID))
	if err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	}
	event, err := events.New(run.ID, run.MissionID,
		events.RiskEscalationInvalidatedEvent, "risk_escalation", proposal.ID,
		map[string]any{"reason_code": invalidation.ReasonCode,
			"detail": invalidation.Detail, "grant_id": invalidation.GrantID})
	if err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	}
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	}
	grantID := any(nil)
	if invalidation.GrantID != "" {
		grantID = invalidation.GrantID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO risk_escalation_invalidations
		(id, proposal_id, grant_id, reason_code, detail, invalidation_fingerprint,
		event_sequence, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, invalidation.ID,
		invalidation.ProposalID, grantID, invalidation.ReasonCode, invalidation.Detail,
		invalidation.Fingerprint, event.Sequence, ts(invalidation.CreatedAt)); err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	}
	return invalidation, false, nil
}

func (s *SQLiteStore) GetRiskEscalationInvalidation(ctx context.Context,
	proposalID string,
) (runner.RiskEscalationInvalidation, bool, error) {
	return getRiskEscalationInvalidationTx(ctx, s.db, strings.TrimSpace(proposalID))
}

func getRiskEscalationInvalidationTx(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, proposalID string) (runner.RiskEscalationInvalidation, bool, error) {
	var value runner.RiskEscalationInvalidation
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT id, proposal_id, COALESCE(grant_id, ''),
		reason_code, detail, invalidation_fingerprint, created_at
		FROM risk_escalation_invalidations WHERE proposal_id = ?`, proposalID).
		Scan(&value.ID, &value.ProposalID, &value.GrantID, &value.ReasonCode,
			&value.Detail, &value.Fingerprint, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.RiskEscalationInvalidation{}, false, nil
	}
	if err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	}
	value.CreatedAt = parseTS(createdAt)
	if err := value.Validate(); err != nil {
		return runner.RiskEscalationInvalidation{}, false, err
	}
	return value, true, nil
}

func (s *SQLiteStore) ResumeRiskEscalationRun(ctx context.Context,
	proposalID string, reason string,
) (domain.Run, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.Run{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	proposal, err := getRiskEscalationProposal(ctx, tx, strings.TrimSpace(proposalID))
	if err != nil {
		return domain.Run{}, false, err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id, session_id,
		status, config_json, budget_json, started_at, finished_at, created_at, updated_at
		FROM runs WHERE id = ?`, proposal.RunID))
	if err != nil {
		return domain.Run{}, false, err
	}
	if run.Status == domain.RunRunning {
		if err := tx.Commit(); err != nil {
			return domain.Run{}, false, err
		}
		return run, true, nil
	}
	if run.Status != domain.RunWaitingApproval {
		return domain.Run{}, false, apperror.New(apperror.CodeFailedPrecondition,
			"risk escalation Run is not waiting for approval")
	}
	if err := requireNoActiveRunControlLeaseTx(ctx, tx, run.ID, time.Now().UTC()); err != nil {
		return domain.Run{}, false, err
	}
	if err := transitionSupervisorRunTx(ctx, tx, &run, domain.RunRunning,
		strings.TrimSpace(reason), time.Now().UTC()); err != nil {
		return domain.Run{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Run{}, false, err
	}
	return run, false, nil
}
