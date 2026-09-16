package store

import (
	"context"
	"database/sql"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

// PendingOperatorInstructions reads a single queue snapshot for the exact
// active attempt. Reading later instructions does not commit their deliveries.
func (s *SQLiteStore) PendingOperatorInstructions(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint,
) ([]domain.OperatorSteeringMessage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	run, _, err := requireActiveSupervisorAttemptTx(ctx, tx, checkpoint)
	if err != nil {
		return nil, err
	}
	var sequence int64
	err = tx.QueryRowContext(ctx, `SELECT message.sequence
		FROM operator_steering_messages message
		JOIN operator_steering_deliveries delivery ON delivery.message_id = message.id
		WHERE delivery.run_id = ? AND delivery.attempt_id = ?
			AND delivery.status = 'prepared' AND message.run_id = ?
			AND message.session_id = ? AND message.status = 'pending'`,
		run.ID, checkpoint.AttemptID, run.ID, run.SessionID).Scan(&sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, operatorSteeringSelect+`
		WHERE message.run_id = ? AND message.session_id = ?
			AND message.status = 'pending' AND message.sequence > ?
		ORDER BY message.sequence LIMIT ?`,
		run.ID, run.SessionID, sequence, domain.MaxPendingOperatorSteering+1)
	if err != nil {
		return nil, err
	}
	var messages []domain.OperatorSteeringMessage
	for rows.Next() {
		message, err := getOperatorSteeringMessageRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		messages = append(messages, message)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if len(messages) > domain.MaxPendingOperatorSteering {
		return nil, apperror.New(apperror.CodeResourceExhausted,
			"Pending user instructions exceed the supported queue boundary")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return messages, nil
}
