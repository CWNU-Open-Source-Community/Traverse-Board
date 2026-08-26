package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/standardcodedelivery"
)

const standardCodeDeliverySelect = `SELECT payload_json FROM standard_code_deliveries`

func (s *SQLiteStore) CreateStandardCodeDelivery(ctx context.Context,
	report standardcodedelivery.Report,
) (standardcodedelivery.Report, bool, error) {
	if report.ProtocolVersion != standardcodedelivery.ProtocolVersion ||
		report.EventSequence != 0 || report.ReceiptSHA256 != "" ||
		report.Status != report.ReceiptStatus || report.Observation != (standardcodedelivery.Observation{}) ||
		report.ID == "" || report.OperationKeySHA256 == "" || report.RequestFingerprint == "" {
		return standardcodedelivery.Report{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Standard Code delivery draft is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return standardcodedelivery.Report{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, getErr := getStandardCodeDeliveryByOperation(ctx, tx,
		report.OperationKeySHA256); getErr != nil {
		return standardcodedelivery.Report{}, false, getErr
	} else if found {
		if existing.RequestFingerprint != report.RequestFingerprint ||
			existing.Binding.RunID != report.Binding.RunID {
			return standardcodedelivery.Report{}, false, apperror.New(
				apperror.CodeConflict, "Standard Code delivery operation key was reused")
		}
		if err := tx.Commit(); err != nil {
			return standardcodedelivery.Report{}, false, err
		}
		return existing, true, nil
	}
	event, err := events.New(report.Binding.RunID, report.Binding.MissionID,
		events.StandardCodeDeliveryRecordedEvent, "standard_code_delivery", report.ID,
		map[string]any{"protocol_version": report.ProtocolVersion,
			"status": report.ReceiptStatus, "drydock_id": report.Binding.DrydockID,
			"drydock_generation":  report.Binding.DrydockGeneration,
			"final_checkpoint_id": report.FinalCheckpoint.ID,
			"revision_sha256":     report.FinalCheckpoint.RevisionSHA256,
			"diff_sha256":         report.Diff.SHA256,
			"verification_count":  len(report.Verifications),
			"declaration":         report.Declaration,
			"verified":            report.ReceiptStatus == standardcodedelivery.StatusPassed,
			"automatic_commit":    false, "automatic_push": false,
			"automatic_merge": false, "source_overwrite": false})
	if err != nil {
		return standardcodedelivery.Report{}, false, err
	}
	event.CreatedAt = report.CreatedAt.UTC()
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		return standardcodedelivery.Report{}, false, err
	}
	report, err = report.Seal(event.Sequence)
	if err != nil {
		return standardcodedelivery.Report{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "Standard Code delivery report is invalid", err)
	}
	payload, err := json.Marshal(report)
	if err != nil || len(payload) > standardcodedelivery.MaxPayloadBytes {
		if err == nil {
			err = errors.New("Standard Code delivery payload exceeded its bound")
		}
		return standardcodedelivery.Report{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO standard_code_deliveries
		(id, operation_key_digest, request_fingerprint, protocol_version, run_id,
		 mission_id, session_id, source_workspace_id, drydock_workspace_id, drydock_id,
		 drydock_generation, receipt_status, final_checkpoint_id, revision_sha256,
		 diff_sha256, receipt_sha256, payload_json, event_sequence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		report.ID, report.OperationKeySHA256, report.RequestFingerprint,
		report.ProtocolVersion, report.Binding.RunID, report.Binding.MissionID,
		report.Binding.SessionID, report.Binding.SourceWorkspaceID,
		report.Binding.DrydockWorkspaceID, report.Binding.DrydockID,
		report.Binding.DrydockGeneration, report.ReceiptStatus,
		report.FinalCheckpoint.ID, report.FinalCheckpoint.RevisionSHA256,
		report.Diff.SHA256, report.ReceiptSHA256, string(payload),
		report.EventSequence, ts(report.CreatedAt))
	if err != nil {
		return standardcodedelivery.Report{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return standardcodedelivery.Report{}, false, err
	}
	return report, false, nil
}

func (s *SQLiteStore) GetStandardCodeDelivery(ctx context.Context,
	id string,
) (standardcodedelivery.Report, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return standardcodedelivery.Report{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Standard Code delivery id is invalid")
	}
	value, err := scanStandardCodeDelivery(s.db.QueryRowContext(ctx,
		standardCodeDeliverySelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return standardcodedelivery.Report{}, false, nil
	}
	return value, err == nil, err
}

func (s *SQLiteStore) GetLatestStandardCodeDelivery(ctx context.Context,
	runID string,
) (standardcodedelivery.Report, bool, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return standardcodedelivery.Report{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Standard Code delivery Run id is invalid")
	}
	value, err := scanStandardCodeDelivery(s.db.QueryRowContext(ctx,
		standardCodeDeliverySelect+` WHERE run_id = ? ORDER BY event_sequence DESC LIMIT 1`,
		runID))
	if errors.Is(err, sql.ErrNoRows) {
		return standardcodedelivery.Report{}, false, nil
	}
	return value, err == nil, err
}

func getStandardCodeDeliveryByOperation(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, digest string) (standardcodedelivery.Report, bool, error) {
	value, err := scanStandardCodeDelivery(queryer.QueryRowContext(ctx,
		standardCodeDeliverySelect+` WHERE operation_key_digest = ?`, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return standardcodedelivery.Report{}, false, nil
	}
	return value, err == nil, err
}

func scanStandardCodeDelivery(row scanner) (standardcodedelivery.Report, error) {
	var payload string
	if err := row.Scan(&payload); err != nil {
		return standardcodedelivery.Report{}, err
	}
	var report standardcodedelivery.Report
	if err := json.Unmarshal([]byte(payload), &report); err != nil {
		return standardcodedelivery.Report{}, err
	}
	if err := report.Validate(); err != nil {
		return standardcodedelivery.Report{}, err
	}
	if report.Observation != (standardcodedelivery.Observation{}) ||
		report.Status != report.ReceiptStatus {
		return standardcodedelivery.Report{}, errors.New(
			"stored Standard Code delivery contains a mutable observation")
	}
	return report, nil
}
