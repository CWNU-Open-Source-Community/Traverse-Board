package store

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"database/sql"
	"encoding/json"
)

func addSupervisorMonetaryIdentity(payload map[string]any, attempt llm.ModelAttempt) {
	if attempt.SupervisorAttemptID != "" {
		payload["supervisor_attempt_id"] = attempt.SupervisorAttemptID
		payload["monetary_attempt_number"] = attempt.MonetaryAttemptNumber()
	}
}

// A terminal commit can succeed while its acknowledgement is lost. Recognize
// only the exact usage already saved for this start, without replacing a
// completed response by a failure or attributing another call's checkpoint.
func supervisorAccountingUsageAcknowledgedTx(ctx context.Context, tx *sql.Tx, runID, subject string, attempt llm.ModelAttempt, usage llm.Usage, expected map[string]any) (bool, error) {
	var encoded string
	err := tx.QueryRowContext(ctx, `SELECT payload_json FROM run_events WHERE run_id=? AND source='model_gateway' AND subject_id=? AND type IN ('model.completed','model.failed') ORDER BY sequence LIMIT 1`, runID, subject).Scan(&encoded)
	if err != nil {
		return false, err
	}
	var saved struct {
		Usage               *llm.Usage `json:"usage"`
		ToolCount           int        `json:"tool_call_count"`
		Number              int        `json:"model_attempt"`
		Provider            string     `json:"provider"`
		Model               string     `json:"model"`
		Purpose             string     `json:"purpose"`
		SupervisorAttemptID string     `json:"supervisor_attempt_id"`
		CostKey             int64      `json:"monetary_attempt_number"`
	}
	if err := json.Unmarshal([]byte(encoded), &saved); err != nil {
		return false, err
	}
	count, ok := expected["tool_call_count"].(int)
	if !ok || saved.Usage == nil || *saved.Usage != usage || saved.ToolCount != count {
		return false, nil
	}
	key := int64(0)
	if attempt.SupervisorAttemptID != "" {
		key = attempt.MonetaryAttemptNumber()
	}
	return saved.Number == attempt.Number && saved.Provider == attempt.Provider && saved.Model == attempt.Model && saved.Purpose == attempt.Purpose && saved.SupervisorAttemptID == attempt.SupervisorAttemptID && saved.CostKey == key, nil
}

func supervisorModelUsageUnknown(usage llm.Usage) bool {
	return usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 ||
		(usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0) ||
		usage.TotalTokens-usage.InputTokens > usage.OutputTokens
}

// A failed/aborted response may still carry known billable usage. This path
// cannot publish a response, create tool work or move protocol-repair state.
func (s *SQLiteStore) RecordSupervisorModelFailedWithUsage(ctx context.Context, checkpoint domain.SupervisorCheckpoint,
	attempt llm.ModelAttempt, usage llm.Usage, toolCount int,
) (domain.SupervisorCheckpoint, error) {
	attempt = sanitizeModelAttempt(attempt)
	if err := attempt.ValidateFailed(); err != nil {
		return domain.SupervisorCheckpoint{}, apperror.Wrap(apperror.CodeInvalidArgument, "invalid failed model attempt", err)
	}
	if attempt.Purpose != "" {
		return domain.SupervisorCheckpoint{}, apperror.New(apperror.CodeInvalidArgument, "ordinary usage receipt cannot impersonate auxiliary purpose")
	}
	if toolCount < 0 {
		return domain.SupervisorCheckpoint{}, apperror.New(apperror.CodeInvalidArgument, "invalid recorded model tool count")
	}
	unknown := supervisorModelUsageUnknown(usage)
	if _, _, _, err := supervisorUsage(usage); err != nil {
		usage = llm.Usage{}
		unknown = true
	}
	elapsed, err := supervisorModelElapsedMillis(attempt.Elapsed)
	if err != nil {
		return domain.SupervisorCheckpoint{}, err
	}
	payload := map[string]any{"turn": checkpoint.NextTurn, "attempt_id": checkpoint.AttemptID,
		"model_attempt": attempt.Number, "transport_attempt": attempt.TransportNumber(), "max_attempts": attempt.MaxAttempts,
		"protocol_repair": attempt.ProtocolRepair, "tool_round": attempt.ToolRound, "provider": attempt.Provider, "model": attempt.Model,
		"outcome": attempt.Outcome, "error": attempt.ErrorText, "elapsed_millis": elapsed,
		"retry_after_millis": attempt.RetryAfter.Milliseconds(), "retry_planned": attempt.RetryPlanned,
		"stream_events": attempt.StreamEvents, "stream_bytes": attempt.StreamBytes, "usage": usage, "tool_call_count": toolCount,
		"usage_unknown": unknown, "accounting_only": true}
	addSupervisorMonetaryIdentity(payload, attempt)
	return s.recordSupervisorModelTerminal(ctx, checkpoint, attempt, events.ModelFailedEvent, payload, supervisorModelTerminalOptions{Usage: &usage, AccountingOnly: true})
}
