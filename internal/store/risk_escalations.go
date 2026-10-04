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
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

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
	// The old execution system is retired. Only durable outcomes can resume,
	// including an intent whose outcome is permanently unknown.
	var settled bool
	if err := tx.QueryRowContext(ctx, `SELECT
  EXISTS(SELECT 1 FROM tool_approvals WHERE proposal_id = ? AND status = 'denied') OR
  EXISTS(SELECT 1 FROM risk_escalation_results WHERE proposal_id = ?) OR
  EXISTS(SELECT 1 FROM risk_escalation_invalidations WHERE proposal_id = ?) OR
  EXISTS(SELECT 1 FROM risk_escalation_execution_intents WHERE proposal_id = ?)`,
		proposal.ID, proposal.ID, proposal.ID, proposal.ID).Scan(&settled); err != nil {
		return domain.Run{}, false, err
	}
	if !settled {
		return domain.Run{}, false, apperror.New(apperror.CodeFailedPrecondition, "historical command has no durable outcome")
	}
	checkpoint, found, err := getSupervisorCheckpointTx(ctx, tx, run.ID)
	if err != nil {
		return domain.Run{}, false, err
	}
	if !found || checkpoint.Phase != domain.SupervisorTurnStarted || checkpoint.NextTurn != proposal.SupervisorTurn {
		return domain.Run{}, false, apperror.New(apperror.CodeConflict, "historical command no longer owns the pending Supervisor turn")
	}
	var ownsCall bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_supervisor_tool_calls c
  JOIN run_supervisor_tool_call_agents a ON a.run_id = c.run_id AND a.turn = c.turn
    AND a.attempt_id = c.attempt_id AND a.call_id = c.call_id
  WHERE c.run_id = ? AND c.turn = ? AND c.call_id = ? AND c.tool_name = 'host_command_propose'
    AND c.attempt_id = ? AND a.agent_id = ? AND a.agent_attempt_id = c.attempt_id)`,
		run.ID, proposal.SupervisorTurn, proposal.SupervisorToolCallID, checkpoint.AttemptID, proposal.RootAgentID).Scan(&ownsCall); err != nil {
		return domain.Run{}, false, err
	}
	if !ownsCall {
		return domain.Run{}, false, apperror.New(apperror.CodeConflict, "historical command lost its exact durable call")
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
