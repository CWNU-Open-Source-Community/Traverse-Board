package store

import (
	"context"

	"cyberagent-workbench/internal/events"
)

// ListRunContextDiagnosticEvents reads the latest relevant durable receipts.
// Filtering before the bound keeps unrelated tool traffic from hiding them.
// Results are newest first; this never starts or repairs a compaction attempt.
func (s *SQLiteStore) ListRunContextDiagnosticEvents(ctx context.Context, runID string, limit int) ([]events.Event, error) {
	var err error
	if runID, err = validateReadRunID(runID); err != nil {
		return nil, err
	}
	if err := validateStoreReadPage(0, limit); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, event_id, version, run_id, mission_id, sequence,
		type, source, subject_id, payload_json, created_at FROM run_events WHERE run_id = ? AND
		((source = 'context_manager' AND type = 'session.context_compacted') OR
		(source = 'model_gateway' AND type IN ('model.started', 'model.completed', 'model.failed')
		AND json_extract(payload_json, '$.purpose') = 'context_compaction'))
		ORDER BY sequence DESC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]events.Event, 0, limit)
	for rows.Next() {
		value, err := scanRunEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
