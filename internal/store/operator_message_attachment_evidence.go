package store

import (
	"context"
	"database/sql"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

// commitOperatorMessageAttachmentEvidenceTx makes queued attachments visible in
// durable Session history only after their exact operator message is committed.
// The narrow receipt table makes retries idempotent without treating an upload
// or a later queued binding as model-visible evidence.
func commitOperatorMessageAttachmentEvidenceTx(ctx context.Context, tx *sql.Tx,
	message domain.OperatorSteeringMessage,
) error {
	imageRows, err := tx.QueryContext(ctx, `SELECT b.image_id,i.sha256 FROM thread_message_images b
		JOIN workspace_image_attachments i ON i.id=b.image_id
		WHERE b.message_id=? ORDER BY b.ordinal`, message.ID)
	if err != nil {
		return err
	}
	images := make([]domain.ImageReference, 0, message.ImageCount)
	for imageRows.Next() {
		var ref domain.ImageReference
		if err := imageRows.Scan(&ref.ID, &ref.SHA256); err != nil {
			imageRows.Close()
			return err
		}
		images = append(images, ref)
	}
	if err := imageRows.Err(); err != nil {
		imageRows.Close()
		return err
	}
	imageRows.Close()
	if len(images) != message.ImageCount {
		return apperror.New(apperror.CodeConflict, "operator message image evidence binding is incomplete")
	}
	for _, ref := range images {
		found, err := operatorMessageAttachmentEvidenceExistsTx(ctx, tx, message.ID, "image", ref.ID)
		if err != nil {
			return err
		}
		if found {
			continue
		}
		saved, err := saveImageEvidenceTx(ctx, tx, message.RunID, message.SessionID,
			message.ID, []domain.ImageReference{ref})
		if err != nil {
			return err
		}
		if len(saved) != 1 {
			return apperror.New(apperror.CodeConflict, "operator message image evidence was not sealed")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operator_message_attachment_evidence
			(message_id,kind,attachment_id,session_message_id) VALUES(?,?,?,?)`,
			message.ID, "image", ref.ID, saved[0].ID); err != nil {
			return err
		}
	}

	fileRows, err := tx.QueryContext(ctx, `SELECT b.attachment_id,f.workspace_id,f.sha256,f.byte_size
		FROM thread_message_attachments b JOIN workspace_file_attachments f ON f.id=b.attachment_id
		WHERE b.message_id=? ORDER BY b.ordinal`, message.ID)
	if err != nil {
		return err
	}
	files := make([]domain.FileAttachmentReference, 0, message.AttachmentCount)
	for fileRows.Next() {
		var ref domain.FileAttachmentReference
		if err := fileRows.Scan(&ref.ID, &ref.WorkspaceID, &ref.SHA256, &ref.ByteSize); err != nil {
			fileRows.Close()
			return err
		}
		files = append(files, ref)
	}
	if err := fileRows.Err(); err != nil {
		fileRows.Close()
		return err
	}
	fileRows.Close()
	if len(files) != message.AttachmentCount {
		return apperror.New(apperror.CodeConflict, "operator message file evidence binding is incomplete")
	}
	for _, ref := range files {
		found, err := operatorMessageAttachmentEvidenceExistsTx(ctx, tx, message.ID, "file", ref.ID)
		if err != nil {
			return err
		}
		if found {
			continue
		}
		saved, err := saveFileAttachmentEvidenceTx(ctx, tx, message.SessionID,
			message.ID, []domain.FileAttachmentReference{ref})
		if err != nil {
			return err
		}
		if len(saved) != 1 {
			return apperror.New(apperror.CodeConflict, "operator message file evidence was not sealed")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operator_message_attachment_evidence
			(message_id,kind,attachment_id,session_message_id) VALUES(?,?,?,?)`,
			message.ID, "file", ref.ID, saved[0].ID); err != nil {
			return err
		}
	}
	return nil
}

func operatorMessageAttachmentEvidenceExistsTx(ctx context.Context, tx *sql.Tx,
	messageID, kind, attachmentID string,
) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM operator_message_attachment_evidence
		WHERE message_id=? AND kind=? AND attachment_id=?`, messageID, kind, attachmentID).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}
