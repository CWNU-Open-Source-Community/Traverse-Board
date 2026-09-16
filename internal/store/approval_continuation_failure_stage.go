package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

// Seal a model failure category while the exact approval attempt still owns
// its checkpoint. Closing that checkpoint must not erase the cause or cause a
// later attempt's failure to be used as the explanation for this handoff.
func approvalContinuationFailureStageTx(ctx context.Context, tx *sql.Tx, handoff domain.RunExecutionHandoff,
	lease domain.RunExecutionLease,
) (string, string, error) {
	value, err := loadApprovalContinuationTx(ctx, tx, handoff)
	if err != nil {
		return "", "", err
	}
	cp, found, err := getSupervisorCheckpointTx(ctx, tx, handoff.Operation.RunID)
	if err != nil || !found {
		return "", "", err
	}
	if cp.Phase != domain.SupervisorTurnFailed || cp.AttemptID == "" || cp.PendingInput != value.Input ||
		cp.LeaseID != lease.LeaseID || cp.LeaseGeneration != lease.Generation {
		return "", "", nil
	}
	owned, err := approvalContinuationOwnsTurnTx(ctx, tx, value, cp)
	if err != nil || !owned {
		return "", "", err
	}
	stage, err := recordedThreadModelFailureStage(ctx, tx, cp.RunID, cp.AttemptID)
	return stage, cp.AttemptID, err
}

// Historical handoffs without a sealed category stay generic. This reads the
// immutable completion event, never the latest Run error or an error string.
func recordedApprovalContinuationFailureStage(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, handoff domain.RunExecutionHandoff) (string, error) {
	if handoff.Operation.RequestedBy != approvalContinuationActor || handoff.Result == nil ||
		handoff.Result.Status != domain.RunExecutionHandoffFailed || handoff.Result.ErrorCode != "failed_precondition" {
		return "", nil
	}
	var body string
	if err := q.QueryRowContext(ctx, `SELECT payload_json FROM run_events WHERE run_id=? AND sequence=?
	 AND subject_id=? AND type=? AND source='run_execution_handoff'`, handoff.Operation.RunID,
		handoff.Result.CompletionEventSequence, handoff.Operation.ID, events.RunExecutionHandoffCompletedEvent).Scan(&body); err != nil {
		return "", err
	}
	var payload struct {
		Stage     string `json:"failure_stage"`
		AttemptID string `json:"failure_attempt_id"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "", err
	}
	switch payload.Stage {
	case domain.ThreadFailureToolRequestRejected, domain.ThreadFailureEmptyModelResponse, domain.ThreadFailureInvalidModelResponse:
	default:
		return "", nil
	}
	if !domain.ValidAgentID(payload.AttemptID) {
		return "", apperror.New(apperror.CodeConflict, "Approval continuation failure lost its model attempt")
	}
	stage, err := recordedThreadModelFailureStage(ctx, q, handoff.Operation.RunID, payload.AttemptID)
	if err != nil {
		return "", err
	}
	if stage != payload.Stage {
		return "", apperror.New(apperror.CodeConflict, "Approval continuation failure differs from its model outcome")
	}
	return stage, nil
}
