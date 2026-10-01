package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
)

type specialistMonetaryPayload struct {
	AttemptID    string      `json:"agent_attempt_id"`
	Number       int         `json:"model_attempt"`
	MonetaryKey  int64       `json:"monetary_attempt_number"`
	Provider     string      `json:"provider"`
	Model        string      `json:"model"`
	Outcome      llm.Outcome `json:"outcome"`
	Usage        *llm.Usage  `json:"usage"`
	UsageUnknown bool        `json:"usage_unknown"`
	Dispatch     string      `json:"dispatch"`
	StreamEvents int         `json:"stream_events"`
	StreamBytes  int         `json:"stream_bytes"`
	RetryPlanned bool        `json:"retry_planned"`
	RetryAfter   int64       `json:"retry_after_millis"`
}

func requireSpecialistMonetaryStartTx(ctx context.Context, tx *sql.Tx, refRunID, refAttemptID string, attempt llm.ModelAttempt) error {
	var key int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(json_extract(payload_json,'$.monetary_attempt_number'),0)
		FROM run_events WHERE run_id=? AND source='specialist_model_gateway' AND type='model.started' AND subject_id=?`,
		refRunID, specialistModelSubject(refAttemptID, attempt.Number)).Scan(&key); err != nil {
		return err
	}
	if key != specialistMonetaryPayloadNumber(attempt) {
		return apperror.New(apperror.CodeConflict, "Specialist monetary identity differs from its durable start")
	}
	return nil
}

func specialistMonetaryEvidenceTx(ctx context.Context, tx *sql.Tx, runID string, number int64,
	provider, model string,
) (monetaryModelEvidence, error) {
	// Legacy small integers do not identify a child or a turn. Retain their
	// exposure instead of guessing from another child's most recent receipt.
	result := monetaryModelEvidence{Sent: true}
	if number < 1<<61 {
		return result, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT subject_id,payload_json,sequence FROM run_events
		WHERE run_id=? AND source='specialist_model_gateway' AND type='model.started'
		AND json_extract(payload_json,'$.monetary_attempt_number')=? ORDER BY sequence LIMIT 2`, runID, number)
	if err != nil {
		return result, err
	}
	var subject, raw string
	var sequence int64
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&subject, &raw, &sequence); err != nil {
			rows.Close()
			return result, err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || count != 1 {
		return result, err
	}
	var start specialistMonetaryPayload
	if err := json.Unmarshal([]byte(raw), &start); err != nil {
		return result, err
	}
	if start.Provider != provider || start.Model != model ||
		(llm.ModelAttempt{SpecialistAttemptID: start.AttemptID, Number: start.Number}).MonetaryAttemptNumber() != number ||
		subject != specialistModelSubject(start.AttemptID, start.Number) {
		return result, apperror.New(apperror.CodeFailedPrecondition, "Specialist monetary reservation differs from its durable start")
	}
	rows, err = tx.QueryContext(ctx, `SELECT type,payload_json FROM run_events WHERE run_id=?
		AND source='specialist_model_gateway' AND subject_id=? AND sequence>?
		AND type IN ('model.completed','model.failed') ORDER BY sequence LIMIT 2`, runID, subject, sequence)
	if err != nil {
		return result, err
	}
	count = 0
	for rows.Next() {
		count++
		if err := rows.Scan(&result.EventType, &result.PayloadJSON); err != nil {
			rows.Close()
			return result, err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	if count != 1 {
		result.EventType, result.PayloadJSON = "", ""
		return result, nil
	}
	var terminal specialistMonetaryPayload
	if err := json.Unmarshal([]byte(result.PayloadJSON), &terminal); err != nil {
		return result, err
	}
	if terminal.AttemptID != start.AttemptID || terminal.Number != start.Number || terminal.MonetaryKey != number ||
		terminal.Provider != provider || terminal.Model != model {
		return result, apperror.New(apperror.CodeFailedPrecondition, "Specialist monetary terminal differs from its durable start")
	}
	if terminal.Dispatch == "not_sent" {
		if result.EventType != events.ModelFailedEvent || terminal.Outcome != llm.OutcomePermanent ||
			terminal.Usage == nil || *terminal.Usage != (llm.Usage{}) || terminal.UsageUnknown ||
			terminal.StreamEvents != 0 || terminal.StreamBytes != 0 || terminal.RetryPlanned || terminal.RetryAfter != 0 {
			return result, apperror.New(apperror.CodeFailedPrecondition, "invalid not-dispatched Specialist monetary evidence")
		}
		result.Sent, result.NotSent = false, true
	}
	return result, nil
}
