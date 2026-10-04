package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

type hostCommandProposalQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
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

func (s *SQLiteStore) GetHostCommandProposalExecutionIntentByProposal(ctx context.Context, proposalID string) (runner.HostExecutionIntent, bool, error) {
	var requestID string
	err := s.db.QueryRowContext(ctx, `SELECT request_id FROM host_command_proposal_execution_intents WHERE proposal_id=?`, proposalID).Scan(&requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return runner.HostExecutionIntent{}, false, nil
	}
	if err != nil {
		return runner.HostExecutionIntent{}, false, err
	}
	return getHostCommandProposalExecutionIntent(ctx, s.db, requestID)
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
	if result.SavedOutput != nil {
		if err := result.SavedOutput.ValidateReceipt(receipt); err != nil {
			return runner.HostCommandProposalResult{}, runner.HostExecutionReceipt{}, false, err
		}
	}
	return result, receipt, true, nil
}
