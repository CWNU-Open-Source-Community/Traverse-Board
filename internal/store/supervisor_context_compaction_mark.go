package store

import (
	"context"
	"database/sql"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
)

func markSupervisorContextMessagesCompacted(ctx context.Context, tx *sql.Tx, sessionID string,
	history []contextmgr.Message, removed int,
) error {
	if removed < 0 || removed > len(history) {
		return apperror.New(apperror.CodeConflict, "Supervisor compaction removed message count is invalid")
	}
	for _, message := range history[:removed] {
		updated, err := tx.ExecContext(ctx, `UPDATE session_messages SET compacted=1
			WHERE session_id=? AND id=? AND compacted=0`, sessionID, message.SourceMessageID)
		if err != nil {
			return err
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return apperror.New(apperror.CodeConflict, "Supervisor compaction history changed")
		}
	}
	return nil
}
