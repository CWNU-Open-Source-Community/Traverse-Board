package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

// Classify only from the exact attempt's last model terminal event. Earlier
// failures, provider prose and tool output do not become a product status.
// Historical events without this structured field retain their generic label.
func recordedThreadModelFailureStage(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID, attemptID string) (string, error) {
	var eventType, payload string
	err := q.QueryRowContext(ctx, `SELECT type,payload_json FROM run_events
		WHERE run_id=? AND source='model_gateway' AND type IN (?,?)
		AND json_extract(payload_json,'$.attempt_id')=? ORDER BY sequence DESC LIMIT 1`,
		runID, events.ModelFailedEvent, events.ModelCompletedEvent, attemptID).Scan(&eventType, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if eventType != events.ModelFailedEvent {
		return "", nil
	}
	var value struct {
		Stage string `json:"failure_stage"`
	}
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return "", err
	}
	switch value.Stage {
	case domain.ThreadFailureToolRequestRejected, domain.ThreadFailureEmptyModelResponse,
		domain.ThreadFailureInvalidModelResponse:
		return value.Stage, nil
	default:
		return "", nil
	}
}
