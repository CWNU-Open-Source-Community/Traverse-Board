package store

import (
	"context"
	"database/sql"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

// SupervisorOperatorMessageID identifies the explicit input of one durable
// attempt, including its committed or superseded history. A new kernel turn
// alone is not a new user request, and retrying a delivery keeps its message ID.
func (s *SQLiteStore) SupervisorOperatorMessageID(ctx context.Context, runID, attemptID string, turn int) (string, error) {
	if !domain.ValidAgentID(runID) || !domain.ValidAgentID(attemptID) || turn <= 0 {
		return "", apperror.New(apperror.CodeInvalidArgument, "Supervisor input identity is invalid")
	}
	var messageID string
	err := s.db.QueryRowContext(ctx, `SELECT message.id
		FROM operator_steering_deliveries delivery
		JOIN operator_steering_messages message ON message.id = delivery.message_id
		JOIN runs run ON run.id = delivery.run_id
		WHERE delivery.run_id = ? AND delivery.attempt_id = ? AND delivery.turn = ?
			AND message.run_id = run.id AND message.session_id = run.session_id`,
		runID, attemptID, turn).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return messageID, err
}
