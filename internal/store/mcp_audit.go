package store

import (
	"context"
	"database/sql"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
)

// RecordMCPAudit appends one bounded, metadata-only MCP audit event to the
// Run event stream. Payloads are projected from a closed field set and
// never carry tool output bodies, secrets, or raw client input.
func (s *SQLiteStore) RecordMCPAudit(ctx context.Context, runID, eventType string,
	payload map[string]any,
) error {
	runID = strings.TrimSpace(runID)
	eventType = strings.TrimSpace(eventType)
	switch eventType {
	case "mcp.initialized", "mcp.resource_read", "mcp.tool_denied", "mcp.tool_completed":
	default:
		return apperror.New(apperror.CodeInvalidArgument, "MCP audit event type is invalid")
	}
	var budgetJSON, status, missionID string
	if err := s.db.QueryRowContext(ctx, `SELECT budget_json, status, mission_id FROM runs
		WHERE id = ?`, runID).Scan(&budgetJSON, &status, &missionID); err != nil {
		return err
	}
	_ = budgetJSON
	_ = status
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
	event, err := events.New(runID, missionID, eventType, "mcp_server", runID, payload)
	if err != nil {
		return err
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}
