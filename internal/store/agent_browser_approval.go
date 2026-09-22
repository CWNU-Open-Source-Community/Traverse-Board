package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/toolgateway"
)

func getAgentBrowserCallTx(ctx context.Context, tx *sql.Tx, runID, callID string) (domain.SupervisorToolCall, bool, error) {
	c, e := scanSupervisorToolCall(tx.QueryRowContext(ctx, `SELECT run_id,turn,attempt_id,round,position,model_attempt,call_id,stream_response_id,stream_item_id,stream_call_id,tool_name,payload_json,authority_json,status,result_json,error_code,created_at,completed_at FROM run_supervisor_tool_calls WHERE run_id=? AND call_id=?`, runID, callID))
	if e != nil {
		return c, false, e
	}
	e = tx.QueryRowContext(ctx, `SELECT agent_id,agent_attempt_id,attribution_source FROM run_supervisor_tool_call_agents WHERE run_id=? AND turn=? AND attempt_id=? AND call_id=?`, c.RunID, c.Turn, c.AttemptID, c.CallID).Scan(&c.AgentID, &c.AgentAttemptID, &c.AgentAttribution)
	if e != nil {
		return c, false, e
	}
	started, e := supervisorModelEventExistsTx(ctx, tx, runID, events.SupervisorToolExecutionStartedEvent, callID)
	return c, started, e
}
func (s *SQLiteStore) GetAgentBrowserCall(ctx context.Context, runID, callID string) (domain.SupervisorToolCall, bool, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.SupervisorToolCall{}, false, e
	}
	defer tx.Rollback()
	c, started, e := getAgentBrowserCallTx(ctx, tx, runID, callID)
	if e != nil {
		return c, started, e
	}
	return c, started, tx.Commit()
}
func validateAgentBrowserApprovalSourceTx(ctx context.Context, tx *sql.Tx, p approval.Proposal) error {
	binding, bound, e := runBindingForSessionTx(ctx, tx, p.SessionID)
	if e != nil {
		return e
	}
	if !bound {
		return errors.New("browser approval requires a bound Run session")
	}
	c, started, e := getAgentBrowserCallTx(ctx, tx, binding.RunID, p.ProposalID)
	if e != nil {
		return e
	}
	a, e := toolgateway.DecodeAgentBrowserAuthority(json.RawMessage(c.AuthorityJSON))
	if e != nil {
		return e
	}
	canonical, e := toolgateway.NormalizeAgentBrowserPayload(toolgateway.ToolName(c.ToolName), json.RawMessage(c.PayloadJSON))
	if e != nil {
		return e
	}
	var payload toolgateway.AgentBrowserPayload
	_ = json.Unmarshal(canonical, &payload)
	if started || c.Status != domain.SupervisorToolPending || string(canonical) != c.PayloadJSON || a.RunID != c.RunID || a.RootAgentID != c.AgentID || c.AgentAttemptID != c.AttemptID || payload.Sensitive == nil || p.ToolName != toolgateway.AgentBrowserApprovalTool || p.SessionID != a.SessionID || p.WorkspaceID != a.WorkspaceID || p.ActionClass != "browser_"+payload.Sensitive.Effect || p.Mode != "per_call" || p.Status != approval.StatusPending || p.RequestedBy != "run_supervisor" || p.RequestFingerprint != toolgateway.AgentBrowserApprovalFingerprint(c) {
		return errors.New("browser sensitive approval source is not the exact pending undispatched call")
	}
	var missionID, sessionID, workspaceID string
	if e = tx.QueryRowContext(ctx, `SELECT r.mission_id,r.session_id,COALESCE(m.workspace_id,'') FROM runs r JOIN missions m ON m.id=r.mission_id WHERE r.id=?`, c.RunID).Scan(&missionID, &sessionID, &workspaceID); e != nil {
		return e
	}
	if missionID != a.MissionID || sessionID != a.SessionID || workspaceID != a.WorkspaceID {
		return errors.New("browser approval Run identity changed")
	}
	return nil
}

// This narrow exception only settles a proven non-dispatch decision. It does
// not create an execution-started event or permit successful preflight output.
func validateAgentBrowserPreflightResultTx(ctx context.Context, tx *sql.Tx, call domain.SupervisorToolCall, result domain.SupervisorToolResult) (bool, error) {
	if !toolgateway.IsBrowserActionTool(toolgateway.ToolName(call.ToolName)) || !toolgateway.IsAgentBrowserPayload(json.RawMessage(call.PayloadJSON)) {
		return false, nil
	}
	if (result.Status != domain.SupervisorToolDenied || result.ErrorCode != "policy_denied") && (result.Status != domain.SupervisorToolFailed || result.ErrorCode != "browser_authority_expired") {
		return false, nil
	}
	exact, started, e := getAgentBrowserCallTx(ctx, tx, call.RunID, call.CallID)
	if e != nil {
		return false, e
	}
	if started || exact.Status != domain.SupervisorToolPending {
		return false, nil
	}
	a, e := toolgateway.DecodeAgentBrowserAuthority(json.RawMessage(exact.AuthorityJSON))
	if e != nil {
		return false, e
	}
	canonical, e := toolgateway.NormalizeAgentBrowserPayload(toolgateway.ToolName(exact.ToolName), json.RawMessage(exact.PayloadJSON))
	if e != nil {
		return false, e
	}
	var p toolgateway.AgentBrowserPayload
	_ = json.Unmarshal(canonical, &p)
	if p.Sensitive == nil || string(canonical) != exact.PayloadJSON || a.RunID != exact.RunID || a.RootAgentID != exact.AgentID || exact.AgentAttemptID != exact.AttemptID {
		return false, nil
	}
	var envelope struct {
		Version  string            `json:"version"`
		Tool     string            `json:"tool"`
		Status   string            `json:"status"`
		Code     string            `json:"code"`
		Metadata map[string]string `json:"metadata"`
	}
	if json.Unmarshal([]byte(result.ResultJSON), &envelope) != nil || envelope.Version != "supervisor_tool_result.v1" || envelope.Tool != exact.ToolName || envelope.Status != string(result.Status) || envelope.Code != result.ErrorCode || envelope.Metadata["browser_preflight"] != "not_dispatched" || envelope.Metadata["browser_source_fingerprint"] != toolgateway.AgentBrowserApprovalFingerprint(exact) {
		return false, nil
	}
	record, e := getApprovalTx(ctx, tx, "", exact.CallID)
	if errors.Is(e, sql.ErrNoRows) {
		return result.Status == domain.SupervisorToolFailed, nil
	}
	if e != nil {
		return false, e
	}
	if record.RunID != a.RunID || record.SessionID != a.SessionID || record.WorkspaceID != a.WorkspaceID || record.ToolName != toolgateway.AgentBrowserApprovalTool || record.Mode != "per_call" || record.GrantID != "" || record.RequestFingerprint != toolgateway.AgentBrowserApprovalFingerprint(exact) {
		return false, nil
	}
	if result.Status == domain.SupervisorToolDenied {
		return record.Status == approval.StatusDenied, nil
	}
	return record.Status != approval.StatusDenied, nil
}
