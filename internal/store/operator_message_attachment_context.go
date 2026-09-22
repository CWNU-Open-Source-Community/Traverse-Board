package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

// ListPreparedOperatorMessageAttachmentEvidence returns a transient projection
// for the exact prepared delivery. It does not save Session evidence: commit of
// that delivery is the only persistence boundary.
func (s *SQLiteStore) ListPreparedOperatorMessageAttachmentEvidence(ctx context.Context,
	cp domain.SupervisorCheckpoint,
) ([]session.Message, error) {
	values := []session.Message{}
	if err := cp.Validate(); err != nil {
		return values, err
	}
	tx, finish, err := s.beginThreadRequestObservation(ctx)
	if err != nil {
		return values, err
	}
	defer finish()
	current, err := scanSupervisorCheckpoint(tx.QueryRowContext(ctx, supervisorCheckpointSelect,
		cp.RunID))
	if err != nil {
		return values, err
	}
	if cp.Phase != domain.SupervisorTurnStarted || current.Phase != cp.Phase ||
		current.AttemptID != cp.AttemptID || current.NextTurn != cp.NextTurn ||
		current.PendingInput != cp.PendingInput ||
		current.PendingAttachmentCount != cp.PendingAttachmentCount ||
		current.LeaseID != cp.LeaseID || current.LeaseGeneration != cp.LeaseGeneration ||
		cp.LeaseID == "" {
		return values, apperror.New(apperror.CodeConflict,
			"Attachment evidence checkpoint changed")
	}
	lease, err := scanRunExecutionLease(tx.QueryRowContext(ctx, runExecutionLeaseSelect,
		cp.RunID))
	if err != nil {
		return values, err
	}
	if lease.LeaseID != cp.LeaseID || lease.Generation != cp.LeaseGeneration ||
		!lease.ActiveAt(time.Now().UTC()) {
		return values, apperror.New(apperror.CodeConflict,
			"Attachment evidence execution lease changed")
	}
	var messageID, sessionID string
	err = tx.QueryRowContext(ctx, `SELECT delivery.message_id,message.session_id
		FROM operator_steering_deliveries delivery JOIN operator_steering_messages message
			ON message.id=delivery.message_id AND message.run_id=delivery.run_id
		WHERE delivery.run_id=? AND delivery.attempt_id=? AND delivery.turn=?
			AND delivery.status='prepared' AND message.status='pending'`, cp.RunID,
		cp.AttemptID, cp.NextTurn).Scan(&messageID, &sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		if cp.PendingAttachmentCount == 0 {
			return values, nil
		}
		return values, apperror.New(apperror.CodeConflict,
			"Prepared attachment evidence delivery is missing")
	}
	if err != nil {
		return values, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT b.ordinal,f.id FROM thread_message_attachments b
		JOIN workspace_file_attachments f ON f.id=b.attachment_id
		WHERE b.message_id=? ORDER BY b.ordinal`, messageID)
	if err != nil {
		return values, err
	}
	ids := make([]string, 0, cp.PendingAttachmentCount)
	for rows.Next() {
		var ordinal int
		var id string
		if err := rows.Scan(&ordinal, &id); err != nil {
			rows.Close()
			return values, err
		}
		if ordinal != len(ids) {
			rows.Close()
			return values, apperror.New(apperror.CodeConflict,
				"Prepared attachment evidence binding is incomplete")
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return values, err
	}
	rows.Close()
	if len(ids) != cp.PendingAttachmentCount {
		return values, apperror.New(apperror.CodeConflict,
			"Prepared attachment evidence count changed")
	}
	for _, id := range ids {
		value, _, text, err := scanWorkspaceFileAttachment(tx.QueryRowContext(ctx,
			workspaceFileAttachmentSelect+` WHERE id=?`, id))
		if err != nil {
			return values, err
		}
		values = append(values, fileAttachmentEvidenceMessage(sessionID, messageID, value, text))
	}
	return values, nil
}
