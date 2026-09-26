package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/session"
)

// ClaimSupervisorMidTurnSteering freezes every accepted correction for the
// exact active attempt before it is placed in a model request. The returned
// sequence is a durable fence for model start, tool dispatch, and completion.
func (s *SQLiteStore) ClaimSupervisorMidTurnSteering(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint,
) ([]domain.OperatorSteeringMessage, int64, error) {
	if err := checkpoint.Validate(); err != nil {
		return nil, 0, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	run, current, err := requireActiveSupervisorAttemptTx(ctx, tx, checkpoint)
	if err != nil {
		return nil, 0, err
	}
	if err := lockRunningRunForSteeringTx(ctx, tx, run.ID); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, operatorSteeringSelect+`
		WHERE message.run_id=? AND message.session_id=? AND message.delivery_mode='steer'
		AND message.target_attempt_id=? AND message.status='pending'
		ORDER BY message.sequence`, run.ID, run.SessionID, current.AttemptID)
	if err != nil {
		return nil, 0, err
	}
	messages := make([]domain.OperatorSteeringMessage, 0)
	for rows.Next() {
		message, scanErr := getOperatorSteeringMessageRow(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, 0, scanErr
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, err
	}
	_ = rows.Close()
	if len(messages) > domain.MaxPendingOperatorSteering {
		return nil, 0, apperror.New(apperror.CodeResourceExhausted,
			"Current-turn corrections exceed the supported boundary")
	}
	for index, message := range messages {
		if message.Prepared {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operator_steering_midturn_claims
			(message_id,run_id,attempt_id,claimed_at) VALUES(?,?,?,?)`,
			message.ID, run.ID, current.AttemptID, ts(time.Now().UTC())); err != nil {
			return nil, 0, err
		}
		messages[index].Prepared = true
	}
	sequence, err := midturnSteeringSequenceTx(ctx, tx, run.ID, current.AttemptID)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return messages, sequence, nil
}

func midturnSteeringSequenceTx(ctx context.Context, tx *sql.Tx, runID, attemptID string) (int64, error) {
	var sequence int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM operator_steering_messages
		WHERE run_id=? AND delivery_mode='steer' AND target_attempt_id=?`,
		runID, attemptID).Scan(&sequence)
	return sequence, err
}

func (s *SQLiteStore) CurrentSupervisorMidTurnSequence(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint,
) (int64, error) {
	if err := checkpoint.Validate(); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	_, current, err := requireActiveSupervisorAttemptTx(ctx, tx, checkpoint)
	if err != nil {
		return 0, err
	}
	sequence, err := midturnSteeringSequenceTx(ctx, tx, checkpoint.RunID, current.AttemptID)
	if err != nil {
		return 0, err
	}
	return sequence, tx.Commit()
}

// PriorSupervisorMidTurnSequences enumerates possible unsent reservation
// identities for the same model attempt number after a correction. Released
// and absent reservations are harmless idempotent release targets.
func (s *SQLiteStore) PriorSupervisorMidTurnSequences(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint, current int64,
) ([]int64, error) {
	if err := checkpoint.Validate(); err != nil || current < 0 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"Supervisor correction reservation identity is invalid")
	}
	if current == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence FROM operator_steering_messages
		WHERE run_id=? AND delivery_mode='steer' AND target_attempt_id=? AND sequence<?
		ORDER BY sequence`, checkpoint.RunID, checkpoint.AttemptID, current)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sequences := []int64{0}
	for rows.Next() {
		var sequence int64
		if err := rows.Scan(&sequence); err != nil {
			return nil, err
		}
		if sequence <= 0 || sequence >= current {
			return nil, apperror.New(apperror.CodeFailedPrecondition,
				"Supervisor correction reservation sequence changed")
		}
		sequences = append(sequences, sequence)
	}
	return sequences, rows.Err()
}

func requireLatestSupervisorModelSteeringCurrentTx(ctx context.Context, tx *sql.Tx,
	checkpoint domain.SupervisorCheckpoint,
) error {
	var payloadJSON string
	err := tx.QueryRowContext(ctx, `SELECT started.payload_json FROM run_events completed
		JOIN run_events started ON started.run_id=completed.run_id
			AND started.subject_id=completed.subject_id
			AND started.type=? AND started.source='model_gateway'
		WHERE completed.run_id=? AND completed.type=? AND completed.source='model_gateway'
			AND completed.subject_id LIKE ? ORDER BY completed.sequence DESC LIMIT 1`,
		events.ModelStartedEvent, checkpoint.RunID, events.ModelCompletedEvent,
		supervisorModelSubjectPrefix(checkpoint)+"%").Scan(&payloadJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return apperror.New(apperror.CodeFailedPrecondition,
			"No completed model response can close this Supervisor turn")
	}
	if err != nil {
		return err
	}
	payload, err := parseSupervisorModelStartedPayload(payloadJSON)
	if err != nil {
		return err
	}
	sequence, err := midturnSteeringSequenceTx(ctx, tx, checkpoint.RunID, checkpoint.AttemptID)
	if err != nil {
		return err
	}
	if payload.SteeringSequence != sequence {
		return apperror.New(apperror.CodeConflict,
			"Current-turn correction arrived before final answer commit; request a new model response")
	}
	return nil
}

// Commit each correction as its own authorized user message. This runs after
// the original input and before tool evidence or the assistant reply.
func commitMidTurnSteeringTx(ctx context.Context, tx *sql.Tx, run domain.Run,
	checkpoint domain.SupervisorCheckpoint, failed bool,
) (int, error) {
	rows, err := tx.QueryContext(ctx, operatorSteeringSelect+`
		WHERE message.run_id=? AND message.session_id=? AND message.delivery_mode='steer'
			AND message.target_attempt_id=? AND message.status='pending'
		ORDER BY message.sequence`, run.ID, run.SessionID, checkpoint.AttemptID)
	if err != nil {
		return 0, err
	}
	messages := make([]domain.OperatorSteeringMessage, 0)
	for rows.Next() {
		message, scanErr := getOperatorSteeringMessageRow(rows)
		if scanErr != nil {
			_ = rows.Close()
			return 0, scanErr
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()
	for _, message := range messages {
		if !failed && !message.Prepared {
			return 0, apperror.New(apperror.CodeConflict,
				"Unclaimed correction cannot be reported as applied to the final answer")
		}
		user, err := saveSessionMessageTx(ctx, tx,
			session.NewMessage(run.SessionID, "user", message.Content))
		if err != nil {
			return 0, err
		}
		changed, err := tx.ExecContext(ctx, `UPDATE operator_steering_messages
			SET status='committed',session_message_id=?,committed_at=?
			WHERE id=? AND status='pending' AND delivery_mode='steer'
				AND target_attempt_id=?`, user.ID, ts(time.Now().UTC()),
			message.ID, checkpoint.AttemptID)
		if err != nil {
			return 0, err
		}
		count, err := changed.RowsAffected()
		if err != nil || count != 1 {
			return 0, apperror.New(apperror.CodeConflict,
				"Current-turn correction changed before history commit")
		}
		if err := appendSupervisorEventTx(ctx, tx, run,
			events.OperatorSteeringCommittedEvent, "run_supervisor", message.ID,
			map[string]any{"message_id": message.ID, "sequence": message.Sequence,
				"attempt_id": checkpoint.AttemptID, "turn": checkpoint.NextTurn,
				"session_message_id": user.ID, "current_turn": true,
				"applied_to_model": message.Prepared}); err != nil {
			return 0, err
		}
	}
	return len(messages), nil
}
