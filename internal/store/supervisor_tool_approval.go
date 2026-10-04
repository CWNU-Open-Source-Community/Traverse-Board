package store

import (
	"context"
	"database/sql"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

func getSupervisorApprovalCallTx(ctx context.Context, tx *sql.Tx, runID, callID string) (domain.SupervisorToolCall, bool, error) {
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
func (s *SQLiteStore) GetSupervisorApprovalCall(ctx context.Context, runID, callID string) (domain.SupervisorToolCall, bool, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.SupervisorToolCall{}, false, e
	}
	defer tx.Rollback()
	c, started, e := getSupervisorApprovalCallTx(ctx, tx, runID, callID)
	if e != nil {
		return c, started, e
	}
	return c, started, tx.Commit()
}
