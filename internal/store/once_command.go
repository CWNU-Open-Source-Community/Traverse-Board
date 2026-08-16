package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"cyberagent-workbench/internal/events"
)

// RecordOnceCommandExecution appends one metadata-only audit event for an
// executed one-shot command. Raw output, environment values, and executable
// arguments never enter the event stream; only bounded digests and counts do.
func (s *SQLiteStore) RecordOnceCommandExecution(ctx context.Context, runID string,
	payload map[string]any,
) error {
	runID = strings.TrimSpace(runID)
	if runID == "" || len(runID) > 256 || strings.ContainsRune(runID, 0) {
		return errors.New("once command execution run id is invalid")
	}
	for key, value := range payload {
		if len([]byte(key)) > 64 {
			delete(payload, key)
			continue
		}
		if text, ok := value.(string); ok && len([]byte(text)) > 256 {
			payload[key] = "[bounded]"
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var missionID string
	if err := tx.QueryRowContext(ctx, `SELECT mission_id FROM runs WHERE id = ?`,
		runID).Scan(&missionID); err != nil {
		return err
	}
	event, err := events.New(runID, missionID, events.OnceCommandExecutedEvent,
		"once_command_runner", runID, payload)
	if err != nil {
		return err
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}
