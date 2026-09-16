package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
)

type monetaryModelEvidence struct {
	Sent        bool
	EventType   string
	PayloadJSON string
}

// A legacy number belongs to the first call started after its reservation,
// not whichever later Supervisor turn happens to reuse that number. Bound
// identities additionally authenticate their derived key against the start.
func monetaryModelEvidenceTx(ctx context.Context, tx *sql.Tx, runID, reservationID, scope string, number int64, provider, model string) (monetaryModelEvidence, error) {
	var result monetaryModelEvidence
	if scope != domain.MonetaryScopeRoot {
		// Keep the existing non-root accounting path, while preventing its
		// events from being mistaken for a root Supervisor model call.
		err := tx.QueryRowContext(ctx, `SELECT type,payload_json FROM run_events WHERE run_id=?
			AND source<>'model_gateway' AND type IN ('model.completed','model.failed')
			AND json_extract(payload_json,'$.model_attempt')=?
			AND json_extract(payload_json,'$.provider')=? AND json_extract(payload_json,'$.model')=?
			ORDER BY sequence DESC LIMIT 1`, runID, number, provider, model).Scan(&result.EventType, &result.PayloadJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return result, nil
		}
		return result, err
	}
	var reservedSequence int64
	var createdAt string
	if err := tx.QueryRowContext(ctx, `SELECT created_at FROM run_monetary_reservations WHERE id=? AND run_id=?`, reservationID, runID).Scan(&createdAt); err != nil {
		return result, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(sequence),0) FROM run_events WHERE run_id=? AND source='monetary_budget' AND type=? AND subject_id=?`, runID, events.MonetaryBudgetReservedEvent, reservationID).Scan(&reservedSequence); err != nil {
		return result, err
	}
	var subject, encoded string
	var startedSequence int64
	err := tx.QueryRowContext(ctx, `SELECT subject_id,payload_json,sequence FROM run_events
		WHERE run_id=? AND source='model_gateway' AND type='model.started'
		AND sequence>? AND (?<>0 OR created_at>=?)
		AND json_extract(payload_json,'$.provider')=? AND json_extract(payload_json,'$.model')=?
		AND ((? >= 2305843009213693952 AND json_extract(payload_json,'$.monetary_attempt_number')=?)
		OR (? < 2305843009213693952 AND COALESCE(json_extract(payload_json,'$.purpose'),'')=''
		AND COALESCE(json_extract(payload_json,'$.supervisor_attempt_id'),'')=''
		AND COALESCE(json_extract(payload_json,'$.monetary_attempt_number'),0)=0
		AND json_extract(payload_json,'$.model_attempt')=?)) ORDER BY sequence LIMIT 1`,
		runID, reservedSequence, reservedSequence, createdAt, provider, model, number, number, number, number).Scan(&subject, &encoded, &startedSequence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err == nil {
		result.Sent = true
		start, e := parseSupervisorModelStartedPayload(encoded)
		if e != nil {
			return result, e
		}
		if number >= 1<<61 && (start.MonetaryAttemptNumber != number || subject != fmt.Sprintf("%s/model/%d", start.AttemptID, start.ModelAttempt)) {
			return result, apperror.New(apperror.CodeFailedPrecondition, "monetary reservation does not match its durable model start")
		}
		err = tx.QueryRowContext(ctx, `SELECT type,payload_json FROM run_events WHERE run_id=?
			AND source='model_gateway' AND subject_id=? AND sequence>?
			AND type IN ('model.completed','model.failed') ORDER BY sequence LIMIT 1`, runID, subject, startedSequence).Scan(&result.EventType, &result.PayloadJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		var terminal struct {
			ModelAttempt           int    `json:"model_attempt"`
			Provider               string `json:"provider"`
			Model                  string `json:"model"`
			Purpose                string `json:"purpose"`
			SupervisorAttemptID    string `json:"supervisor_attempt_id"`
			CompactionSourceSHA256 string `json:"compaction_source_sha256"`
			MonetaryAttemptNumber  int64  `json:"monetary_attempt_number"`
		}
		if e := json.Unmarshal([]byte(result.PayloadJSON), &terminal); e != nil {
			return result, e
		}
		if terminal.ModelAttempt != start.ModelAttempt || terminal.Provider != start.Provider || terminal.Model != start.Model || terminal.Purpose != start.Purpose || terminal.SupervisorAttemptID != start.SupervisorAttemptID || terminal.CompactionSourceSHA256 != start.CompactionSourceSHA256 || terminal.MonetaryAttemptNumber != start.MonetaryAttemptNumber {
			return result, apperror.New(apperror.CodeFailedPrecondition, "monetary terminal identity differs from its original start")
		}
		return result, nil
	}
	if number >= 1<<61 {
		return result, nil
	}
	// Older imported terminal-only evidence has no start to follow. Only one
	// unambiguous legacy terminal can settle it; multiple candidates retain
	// exposure rather than assigning another turn's receipt to this row.
	rows, err := tx.QueryContext(ctx, `SELECT type,payload_json FROM run_events WHERE run_id=?
		AND source='model_gateway' AND type IN ('model.completed','model.failed')
		AND sequence>? AND (?<>0 OR created_at>=?)
		AND COALESCE(json_extract(payload_json,'$.purpose'),'')=''
		AND COALESCE(json_extract(payload_json,'$.supervisor_attempt_id'),'')=''
		AND COALESCE(json_extract(payload_json,'$.monetary_attempt_number'),0)=0
		AND json_extract(payload_json,'$.model_attempt')=?
		AND json_extract(payload_json,'$.provider')=? AND json_extract(payload_json,'$.model')=?
		ORDER BY sequence LIMIT 2`, runID, reservedSequence, reservedSequence, createdAt, number, provider, model)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&result.EventType, &result.PayloadJSON); err != nil {
			return result, err
		}
	}
	result.Sent = count > 0
	if count > 1 {
		result.EventType, result.PayloadJSON = "", ""
	}
	return result, rows.Err()
}

func monetaryTerminalUnknown(usage *llm.Usage, declared bool) bool {
	return declared || usage == nil || supervisorModelUsageUnknown(*usage)
}
