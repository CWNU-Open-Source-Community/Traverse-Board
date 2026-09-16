package store

import (
	"context"
	"database/sql"
	"errors"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

// FailedTurnWebToolEvidence reads only completed web calls bound to an exact
// sealed outcome. It does not infer failure from arbitrary session text.
func (s *SQLiteStore) FailedTurnWebToolEvidence(ctx context.Context, runID string,
	outcomeMessageID int64,
) (domain.ThreadTurnFailure, []domain.SupervisorToolCall, bool, error) {
	var messageID string
	err := s.db.QueryRowContext(ctx, `SELECT subject_id FROM run_events
		WHERE run_id=? AND type=? AND source='thread_turn'
		AND json_extract(payload_json,'$.outcome_message_id')=?
		ORDER BY sequence DESC LIMIT 1`, runID, events.ThreadTurnFailedEvent, outcomeMessageID).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ThreadTurnFailure{}, nil, false, nil
	}
	if err != nil {
		return domain.ThreadTurnFailure{}, nil, false, err
	}
	failure, found, err := s.GetThreadTurnFailure(ctx, runID, messageID)
	if err != nil || !found || failure.OutcomeMessageID != outcomeMessageID {
		return failure, nil, false, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id, turn, attempt_id, round, position,
		model_attempt, call_id, stream_response_id, stream_item_id, stream_call_id,
		tool_name, payload_json, authority_json, status, result_json, error_code, created_at, completed_at
		FROM run_supervisor_tool_calls WHERE run_id=? AND turn=? AND attempt_id=?
		AND status='completed' AND tool_name IN ('web_search','web_fetch')
		ORDER BY round DESC,position LIMIT 6`, runID, failure.Turn, failure.AttemptID)
	if err != nil {
		return failure, nil, false, err
	}
	defer rows.Close()
	var calls []domain.SupervisorToolCall
	for rows.Next() {
		call, err := scanSupervisorToolCall(rows)
		if err != nil {
			return failure, nil, false, err
		}
		calls = append(calls, call)
	}
	return failure, calls, true, rows.Err()
}
