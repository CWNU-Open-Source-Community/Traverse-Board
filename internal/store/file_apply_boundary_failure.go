package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

// A source workspace_apply cannot enter its file manager before its durable
// checkpoint transaction exists. An approved edit with no such transaction is
// therefore an unstarted write, not an uncertain completed mutation. Record
// only the failed tool invocation; its approved proposal and prepared apply
// intent remain available if a later model, after reading new input, retries.
func settleFileApplyBeforeBoundaryFailureTx(ctx context.Context, tx *sql.Tx, run domain.Run,
	cp domain.SupervisorCheckpoint, handoff domain.RunExecutionHandoff,
) error {
	if (cp.Phase != domain.SupervisorTurnStarted && cp.Phase != domain.SupervisorTurnFailed) || handoff.Result == nil ||
		handoff.Result.Status != domain.RunExecutionHandoffFailed ||
		strings.ToUpper(handoff.Result.ErrorCode) != string(apperror.CodeInternal) ||
		handoff.Result.LeaseID != cp.LeaseID || handoff.Result.LeaseGeneration != cp.LeaseGeneration {
		return nil
	}
	lease, found, err := getRunExecutionLeaseTx(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	if !found || lease.Status != domain.RunExecutionLeaseReleased || lease.LeaseID != cp.LeaseID ||
		lease.Generation != cp.LeaseGeneration || lease.ReleasedAt == nil || lease.ReleasedAt.Before(handoff.Result.CompletedAt) {
		return nil
	}
	var count int
	var callID string
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MIN(call_id),'') FROM run_supervisor_tool_calls
		WHERE run_id=? AND status='pending'`, run.ID).Scan(&count, &callID); err != nil {
		return err
	}
	if count != 1 {
		return nil
	}
	call, err := getSupervisorToolCallTx(ctx, tx, cp, callID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	input, authority, key, valid := fileApplyCallIdentity(call)
	if !valid || call.ResultJSON != "" || call.ErrorCode != "" || authority.MissionID != run.MissionID || authority.SessionID != run.SessionID {
		return nil
	}
	operation, found, err := getFileEditApplyOperationByEditTx(ctx, tx, input.EditID)
	if err != nil || !found {
		return err
	}
	if !fileApplyOperationMatches(operation, call, input, authority, key) ||
		operation.ObservedHash != operation.OriginalHash || operation.EventSequence <= handoff.Operation.EventSequence ||
		operation.EventSequence >= handoff.Result.CompletionEventSequence {
		return nil
	}
	// Do not infer safety from a failed process or an absent result alone. The
	// production source path always prepares this checkpoint before touching a
	// file. Existing snapshots/transactions or any edit transition disqualify it.
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_edits edit
		JOIN missions mission ON mission.id=? JOIN tool_approvals approval ON approval.proposal_id=edit.id
		WHERE edit.id=? AND edit.session_id=? AND edit.workspace_id=? AND mission.workspace_id=edit.workspace_id
		AND edit.status='approved' AND edit.path=? AND edit.operation_kind=? AND edit.original_hash=? AND edit.proposed_hash=?
		AND approval.run_id=? AND approval.session_id=edit.session_id AND approval.workspace_id=edit.workspace_id
		AND approval.status='approved' AND approval.action_class='workspace_write'
		AND NOT EXISTS(SELECT 1 FROM run_file_drydock_bindings WHERE run_id=?)
		AND NOT EXISTS(SELECT 1 FROM standard_code_preset_operations WHERE run_id=? AND status='configured')
		AND NOT EXISTS(SELECT 1 FROM file_edit_apply_results WHERE operation_key_digest=?)
		AND NOT EXISTS(SELECT 1 FROM workspace_checkpoint_transactions WHERE run_id=? AND trigger_receipt_id=?)
		AND NOT EXISTS(SELECT 1 FROM workspace_checkpoints WHERE run_id=? AND trigger_receipt_id=?)`,
		run.MissionID, input.EditID, run.SessionID, authority.WorkspaceID, operation.Path, operation.Operation,
		input.ExpectedOriginalSHA256, input.ExpectedProposedSHA256, run.ID, run.ID, run.ID, operation.KeyDigest,
		run.ID, input.EditID, run.ID, input.EditID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_events WHERE run_id=? AND type=?
		AND source='run_supervisor' AND subject_id=? AND json_extract(payload_json,'$.attempt_id')=?
		AND json_extract(payload_json,'$.turn')=? AND sequence>? AND sequence<?`, run.ID,
		events.SupervisorToolExecutionStartedEvent, call.CallID, cp.AttemptID, cp.NextTurn,
		handoff.Operation.EventSequence, operation.EventSequence).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return nil
	}
	message := "The recorded invocation failed with INTERNAL before its workspace mutation boundary was created. This invocation did not reach the file write. The original detailed error was not retained."
	detailAvailable := "false"
	if strings.TrimSpace(cp.LastError) != "" {
		detail := []rune(sanitizeSupervisorText(cp.LastError))
		if len(detail) > 1024 {
			detail = detail[:1024]
		}
		message = "The invocation failed with INTERNAL before its workspace mutation boundary was created and did not reach the file write. Recorded error: " + string(detail)
		detailAvailable = "true"
	}
	message += " The approved proposal remains available, but no apply was retried while closing this turn. Read the latest user input before deciding whether to call workspace_apply again with these exact expectations."
	encoded, err := json.Marshal(map[string]any{
		"version": "supervisor_tool_result.v1", "tool": call.ToolName, "status": "failed", "code": "INTERNAL",
		"message": message,
		"metadata": map[string]string{"observed_failed_handoff": handoff.Operation.ID, "file_apply_boundary_started": "false",
			"apply_operation_digest": operation.KeyDigest, "edit_id": input.EditID, "path": operation.Path,
			"expected_action": input.ExpectedAction, "original_sha256": input.ExpectedOriginalSHA256,
			"proposed_sha256": input.ExpectedProposedSHA256, "original_error_detail_available": detailAvailable},
	})
	if err != nil {
		return err
	}
	_, err = recordSupervisorToolResultTx(ctx, tx, run, cp, call, domain.SupervisorToolResult{
		CallID: call.CallID, Status: domain.SupervisorToolFailed, ResultJSON: string(encoded),
		ErrorCode: string(apperror.CodeInternal), CompletedAt: time.Now().UTC(),
	})
	return err
}

func fileApplyCallIdentity(call domain.SupervisorToolCall) (toolgateway.WorkspaceApplyPayload, toolgateway.AgentCodeCallAuthority, string, bool) {
	var input toolgateway.WorkspaceApplyPayload
	if call.ToolName != string(toolgateway.WorkspaceApplyTool) {
		return input, toolgateway.AgentCodeCallAuthority{}, "", false
	}
	normalized, err := toolgateway.NormalizeAgentCodePayload(toolgateway.WorkspaceApplyTool, json.RawMessage(call.PayloadJSON))
	if err != nil || string(normalized) != call.PayloadJSON || json.Unmarshal(normalized, &input) != nil {
		return input, toolgateway.AgentCodeCallAuthority{}, "", false
	}
	authority, err := toolgateway.DecodeAgentCodeCallAuthority(json.RawMessage(call.AuthorityJSON))
	key := runmutation.SupervisorToolOperationKey(call.RunID, call.Turn, call.ToolName, call.PayloadJSON)
	id, keyErr := runmutation.SupervisorToolCallID(key, call.Round)
	return input, authority, key, err == nil && keyErr == nil && id == call.CallID && authority.RunID == call.RunID
}

func fileApplyOperationMatches(operation fileedit.ApplyOperation, call domain.SupervisorToolCall,
	input toolgateway.WorkspaceApplyPayload, authority toolgateway.AgentCodeCallAuthority, key string,
) bool {
	kind := map[string]string{"create": fileedit.OperationCreate, "propose_patch": fileedit.OperationReplace, "move": fileedit.OperationMove}[input.ExpectedAction]
	return operation.RunID == call.RunID && operation.SessionID == authority.SessionID &&
		operation.WorkspaceID == authority.WorkspaceID && operation.AppliedBy == authority.RootAgentID &&
		operation.EditID == input.EditID && operation.Operation == kind &&
		operation.OriginalHash == input.ExpectedOriginalSHA256 && operation.ProposedHash == input.ExpectedProposedSHA256 &&
		operation.KeyDigest == runmutation.FileEditApplyOperationDigest(call.RunID, input.EditID, key) &&
		operation.RequestFingerprint == runmutation.FileEditApplyRequestFingerprint(call.RunID, input.EditID, authority.RootAgentID)
}

// GetFailedFileApplyOperationKey is a read-only bridge for a NEW, explicitly
// requested workspace_apply. It returns only an exact operation whose original
// invocation was sealed by M before any checkpoint boundary existed. It does
// not copy authority: the caller supplies its current invocation and lease to
// FileEditApplyService, which still checks approval, policy and current hashes.
func (s *SQLiteStore) GetFailedFileApplyOperationKey(ctx context.Context, runID, editID, appliedBy string) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()
	operation, found, err := getFileEditApplyOperationByEditTx(ctx, tx, editID)
	if err != nil || !found || operation.RunID != runID || operation.AppliedBy != appliedBy {
		return "", false, err
	}
	var cp domain.SupervisorCheckpoint
	var callID, messageID string
	cp.RunID = runID
	err = tx.QueryRowContext(ctx, `SELECT call.turn,call.attempt_id,call.call_id,delivery.message_id FROM run_supervisor_tool_calls call
		JOIN operator_steering_deliveries delivery ON delivery.run_id=call.run_id AND delivery.attempt_id=call.attempt_id AND delivery.turn=call.turn
		WHERE call.run_id=? AND call.tool_name='workspace_apply' AND call.status='failed' AND call.error_code='INTERNAL'
		AND delivery.status='committed' AND json_valid(call.result_json)
		AND json_extract(call.result_json,'$.metadata.file_apply_boundary_started')='false'
		AND json_extract(call.result_json,'$.metadata.apply_operation_digest')=?
		ORDER BY call.turn LIMIT 1`, runID, operation.KeyDigest).Scan(&cp.NextTurn, &cp.AttemptID, &callID, &messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	call, err := getSupervisorToolCallTx(ctx, tx, cp, callID)
	if err != nil {
		return "", false, err
	}
	input, authority, key, valid := fileApplyCallIdentity(call)
	if !valid || !fileApplyOperationMatches(operation, call, input, authority, key) {
		return "", false, nil
	}
	failure, sealed, err := getThreadTurnFailure(ctx, tx, runID, messageID)
	if err != nil || !sealed || failure.AttemptID != call.AttemptID || failure.Turn != call.Turn || failure.ErrorCode != "INTERNAL" {
		return "", false, err
	}
	var envelope struct {
		Metadata map[string]string `json:"metadata"`
	}
	if json.Unmarshal([]byte(call.ResultJSON), &envelope) != nil || envelope.Metadata["observed_failed_handoff"] != failure.HandoffOperationID {
		return "", false, nil
	}
	if _, complete, err := getFileEditApplyResult(ctx, tx, operation.KeyDigest); err != nil {
		return "", false, err
	} else if complete {
		// A lost tool response after the explicit retry must replay the durable
		// result. FileEditApplyService returns it without writing again.
		return key, true, nil
	}
	// A mutation boundary created after that failure must be handled by its
	// existing recovery path, never adopted through this pre-boundary shortcut.
	var boundaries int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_checkpoint_transactions WHERE run_id=? AND trigger_receipt_id=?`, runID, editID).Scan(&boundaries); err != nil {
		return "", false, err
	}
	if boundaries != 0 {
		return "", false, nil
	}
	return key, true, nil
}

func fileApplyBoundaryFailureEvidence(result string) string {
	var envelope struct {
		Metadata map[string]string `json:"metadata"`
	}
	if json.Unmarshal([]byte(result), &envelope) != nil || envelope.Metadata["file_apply_boundary_started"] != "false" {
		return ""
	}
	facts := make(map[string]string)
	for _, key := range []string{"edit_id", "path", "expected_action", "original_sha256", "proposed_sha256", "apply_operation_digest", "file_apply_boundary_started"} {
		value := envelope.Metadata[key]
		if value == "" || len(value) > 256 {
			return ""
		}
		facts[key] = value
	}
	encoded, _ := json.Marshal(facts)
	return "Recorded unstarted apply expectations (no execution authority): " + string(encoded) + "\n"
}
