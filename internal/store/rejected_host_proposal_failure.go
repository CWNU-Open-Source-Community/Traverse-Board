package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

// Older supervisors returned a proposal's policy rejection before recording
// its failed tool result. Settle only that already-failed, never-created intent
// while M closes its exact user turn. Nothing is proposed or executed here.
func settleRejectedHostProposalFailureTx(ctx context.Context, tx *sql.Tx, run domain.Run,
	checkpoint domain.SupervisorCheckpoint, handoff domain.RunExecutionHandoff,
) error {
	if handoff.Result == nil || handoff.Result.Status != domain.RunExecutionHandoffFailed ||
		strings.ToUpper(handoff.Result.ErrorCode) != string(apperror.CodePolicyDenied) ||
		handoff.Result.LeaseID != checkpoint.LeaseID || handoff.Result.LeaseGeneration != checkpoint.LeaseGeneration {
		return nil
	}
	lease, found, err := getRunExecutionLeaseTx(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	if !found || lease.Status != domain.RunExecutionLeaseReleased || lease.LeaseID != checkpoint.LeaseID ||
		lease.Generation != checkpoint.LeaseGeneration || lease.ReleasedAt == nil || lease.ReleasedAt.Before(handoff.Result.CompletedAt) {
		return nil
	}
	var pending int
	var callID string
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(call_id),'') FROM run_supervisor_tool_calls
		WHERE run_id=? AND status='pending'`, run.ID).Scan(&pending, &callID); err != nil {
		return err
	}
	if pending != 1 {
		return nil
	}
	call, err := getSupervisorToolCallTx(ctx, tx, checkpoint, callID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if call.ToolName != string(toolgateway.HostCommandProposeTool) || call.ResultJSON != "" || call.ErrorCode != "" || len(call.AuthorityJSON) != 0 {
		return nil
	}
	spec, normalized, err := toolgateway.NormalizeHostCommandProposalPayload(json.RawMessage(call.PayloadJSON))
	if err != nil || spec.Version != runner.HostCommandProposalProtocolVersion || string(normalized) != call.PayloadJSON {
		return nil
	}
	operationKey := runmutation.SupervisorToolOperationKey(run.ID, call.Turn, call.ToolName, call.PayloadJSON)
	expectedCallID, err := runmutation.SupervisorToolCallID(operationKey, call.Round)
	if err != nil || expectedCallID != call.CallID {
		return nil
	}
	digest := runmutation.OperationKeyDigest(call.ToolName, run.ID, operationKey)
	proposalID := "host-command-proposal-" + digest[:24]
	// The ordinary proposal and its execution lineage must never have been
	// created. An uncertain/missing execution result is not a policy rejection.
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM host_command_proposal_operations WHERE operation_key_digest=?) +
		(SELECT COUNT(*) FROM host_command_proposals WHERE id=?) +
		(SELECT COUNT(*) FROM host_command_proposal_execution_intents WHERE proposal_id=?) +
		(SELECT COUNT(*) FROM host_command_proposal_results WHERE proposal_id=?)`,
		digest, proposalID, proposalID, proposalID).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return nil
	}
	var started int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_events
		WHERE run_id=? AND source='run_supervisor' AND type=? AND subject_id=?
		AND json_extract(payload_json,'$.attempt_id')=? AND json_extract(payload_json,'$.turn')=?
		AND sequence>? AND sequence<?`, run.ID, events.SupervisorToolExecutionStartedEvent, call.CallID,
		checkpoint.AttemptID, checkpoint.NextTurn, handoff.Operation.EventSequence, handoff.Result.CompletionEventSequence).Scan(&started); err != nil {
		return err
	}
	if started != 1 {
		return nil
	}
	// Use the recorded machine code, but do not invent the original policy
	// message (old supervisors did not persist it on this path).
	encoded, err := json.Marshal(map[string]any{
		"version": "supervisor_tool_result.v1", "tool": call.ToolName, "status": "failed", "code": "POLICY_DENIED",
		"message":  "The recorded handoff failed with POLICY_DENIED while this host command proposal was pending. No proposal or execution was created. The original detailed policy reason was not retained; no tool was replayed while settling this failure.",
		"metadata": map[string]string{"observed_failed_handoff": handoff.Operation.ID, "original_error_detail_available": "false", "proposal_created": "false", "execution_started": "false"},
	})
	if err != nil {
		return err
	}
	_, err = recordSupervisorToolResultTx(ctx, tx, run, checkpoint, call, domain.SupervisorToolResult{
		CallID: call.CallID, Status: domain.SupervisorToolFailed, ResultJSON: string(encoded),
		ErrorCode: string(apperror.CodePolicyDenied), CompletedAt: time.Now().UTC(),
	})
	return err
}
