package store

import (
	"context"
	"cyberagent-workbench/internal/domain"
)

// Previous-turn file observations are rehydrated from the immutable ledger,
// including legacy failures whose old text preview cut off the inner status.
// Reads never activate a grant or rewrite historical Session/summary records.
// Tool rows are append-only. Their stable rowid orders attempts within a turn;
// round numbers restart at 1 when a failed turn gets a new attempt.
func (s *SQLiteStore) SupervisorFileEffectCalls(ctx context.Context, checkpoint domain.SupervisorCheckpoint) ([]domain.SupervisorToolCall, error) {
	if err := checkpoint.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id, turn, attempt_id, round, position,
		model_attempt, call_id, stream_response_id, stream_item_id, stream_call_id,
		tool_name, payload_json, authority_json, status, result_json, error_code, created_at, completed_at
		FROM run_supervisor_tool_calls WHERE run_id=? AND turn<? AND status IN ('completed','failed','denied')
		AND tool_name IN ('workspace_change','workspace_apply','workspace_delete')
		ORDER BY turn DESC,rowid DESC LIMIT 7`, checkpoint.RunID, checkpoint.NextTurn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var calls []domain.SupervisorToolCall
	for rows.Next() {
		call, err := scanSupervisorToolCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, rows.Err()
}
