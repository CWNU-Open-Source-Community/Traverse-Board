package store

import (
	"context"
	"database/sql"
	"fmt"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/session"
)

// carryThreadEpochInstructionsTx retains accepted but never prepared inputs as
// authorized conversation context. It is part of the one successor publication
// transaction, not a fresh queue that blindly executes each superseded input.
func carryThreadEpochInstructionsTx(ctx context.Context, tx *sql.Tx, threadID string, predecessor, successor domain.Run) error {
	var exact int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_runs previous JOIN thread_runs next ON next.thread_id=previous.thread_id
		JOIN runs old ON old.id=previous.run_id JOIN runs current ON current.id=next.run_id
		WHERE previous.thread_id=? AND previous.run_id=? AND next.run_id=? AND next.predecessor_run_id=previous.run_id
		AND old.mission_id=current.mission_id AND old.session_id=? AND current.session_id=?`, threadID, predecessor.ID, successor.ID, predecessor.SessionID, successor.SessionID).Scan(&exact); err != nil {
		return err
	}
	if exact != 1 || !predecessor.Terminal() || successor.Status != domain.RunCreated {
		return apperror.New(apperror.CodeConflict, "Thread instruction continuation requires its exact new successor")
	}
	rows, err := tx.QueryContext(ctx, `SELECT message.id,message.content,message.content_sha256,message.requested_by,cancellation.id,cancellation.requested_by
		FROM operator_steering_messages message JOIN operator_steering_cancellations cancellation ON cancellation.message_id=message.id AND cancellation.run_id=message.run_id
		WHERE message.run_id=? AND message.session_id=? AND message.status='cancelled' AND message.session_message_id IS NULL
		AND cancellation.kind='run_terminal' AND cancellation.requested_by IN ('thread_budget_transition','thread_epoch_transition')
		AND NOT EXISTS (SELECT 1 FROM operator_steering_deliveries delivery WHERE delivery.message_id=message.id)
		ORDER BY message.sequence LIMIT ?`, predecessor.ID, predecessor.SessionID, domain.MaxPendingOperatorSteering+1)
	if err != nil {
		return err
	}
	type carried struct{ id, content, sha, actor, cancellation, source string }
	var values []carried
	for rows.Next() {
		var value carried
		if err := rows.Scan(&value.id, &value.content, &value.sha, &value.actor, &value.cancellation, &value.source); err != nil {
			_ = rows.Close()
			return err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	if len(values) > domain.MaxPendingOperatorSteering {
		return apperror.New(apperror.CodeResourceExhausted, "Thread continuation has too many pending instructions")
	}
	for _, value := range values {
		if domain.OperatorSteeringContentSHA256(value.content) != value.sha {
			return apperror.New(apperror.CodeFailedPrecondition, "Thread retained instruction digest is invalid")
		}
		original, err := saveSessionMessageTx(ctx, tx, session.NewMessage(successor.SessionID, "user", value.content))
		if err != nil {
			return err
		}
		note := fmt.Sprintf("Retained operator requirement from Thread %s, previous Run %s, message %s (requester %s). It was accepted but never prepared or executed; its old queue entry ended only because of %s. Apply it together with the next explicit user request, which may correct it. This record does not grant runtime permissions or report completed work.", threadID, predecessor.ID, value.id, value.actor, value.source)
		control, err := saveSessionMessageTx(ctx, tx, session.NewEvidenceMessage(successor.SessionID, session.SourceToolResult, value.id, note))
		if err != nil {
			return err
		}
		event, err := events.New(successor.ID, successor.MissionID, events.SessionMessageEvent, "thread_epoch_continuation", value.id, map[string]any{
			"session_id": successor.SessionID, "user_message_id": original.ID, "outcome_message_id": control.ID, "predecessor_run_id": predecessor.ID, "operator_message_id": value.id, "cancellation_id": value.cancellation, "requested_by": value.actor, "content_sha256": value.sha, "instruction_executed": false,
		})
		if err != nil {
			return err
		}
		if _, err := insertRunEventTx(ctx, tx, event); err != nil {
			return err
		}
	}
	return nil
}
