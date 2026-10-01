package store

import (
	"context"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
)

// RecordSupervisorModelNotDispatched is used only for a local Router rejection
// before the Provider method is called. A durable start is an intent, not proof
// of network I/O. Preserve that start and attach an exact terminal receipt so
// monetary recovery can release it even if the process stops before Release.
func (s *SQLiteStore) RecordSupervisorModelNotDispatched(ctx context.Context, checkpoint domain.SupervisorCheckpoint,
	attempt llm.ModelAttempt,
) (domain.SupervisorCheckpoint, error) {
	attempt = sanitizeModelAttempt(attempt)
	if err := attempt.ValidateFailed(); err != nil {
		return domain.SupervisorCheckpoint{}, err
	}
	bound := attempt.SupervisorAttemptID != "" || (attempt.Purpose == llm.ModelPurposeContextCompaction && attempt.MonetaryAttemptNumber() >= 1<<62)
	if !bound || attempt.Outcome != llm.OutcomePermanent || attempt.RetryPlanned ||
		attempt.StreamEvents != 0 || attempt.StreamBytes != 0 {
		return domain.SupervisorCheckpoint{}, apperror.New(apperror.CodeInvalidArgument, "not-dispatched model receipt requires a bound local rejection without stream output")
	}
	elapsed, err := supervisorModelElapsedMillis(attempt.Elapsed)
	if err != nil {
		return domain.SupervisorCheckpoint{}, err
	}
	usage := llm.Usage{}
	payload := map[string]any{
		"turn": checkpoint.NextTurn, "attempt_id": checkpoint.AttemptID,
		"model_attempt": attempt.Number, "transport_attempt": attempt.TransportNumber(),
		"max_attempts": attempt.MaxAttempts, "protocol_repair": attempt.ProtocolRepair, "tool_round": attempt.ToolRound,
		"provider": attempt.Provider, "model": attempt.Model, "outcome": attempt.Outcome,
		"error": attempt.ErrorText, "elapsed_millis": elapsed, "retry_after_millis": 0, "retry_planned": false,
		"stream_events": 0, "stream_bytes": 0, "usage": usage, "tool_call_count": 0,
		"usage_unknown": false, "accounting_only": true, "dispatch": "not_sent",
	}
	addSupervisorCompactionIdentity(payload, attempt)
	addSupervisorMonetaryIdentity(payload, attempt)
	return s.recordSupervisorModelTerminal(ctx, checkpoint, attempt, events.ModelFailedEvent, payload,
		supervisorModelTerminalOptions{Usage: &usage, AccountingOnly: true})
}
