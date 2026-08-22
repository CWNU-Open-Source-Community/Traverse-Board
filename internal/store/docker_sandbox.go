package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/sandbox"
)

const dockerSandboxAdmissionSelect = `SELECT id, protocol_version,
	operation_key_digest, request_fingerprint, lifecycle_operation_digest,
	run_id, mission_id, workspace_id, plan_id, candidate_id, preparation_id,
	manifest_json, manifest_fingerprint, plan_fingerprint, spec_fingerprint,
	authority_fingerprint, readiness_fingerprint, readiness_expires_at,
	runtime_epoch_fingerprint, profile_snapshot_id, profile_revision,
	permission_snapshot_id, permission_revision, permission_mode, approval_id,
	approval_version, policy_fingerprint, network_mode, network_target_count,
	cpu_quota_millis, memory_bytes, pids, disk_bytes, wall_clock_seconds,
	log_bytes, log_lines, tool_calls_remaining, decision, reason_code,
	remediation_code, product_entry_enabled, execution_authorized,
	artifact_commit_authorized, requested_by, created_at, admission_fingerprint
	FROM sandbox_docker_product_admissions`

type dockerSandboxQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// DockerSandboxIOReceiptIDs is restart-safe metadata for post-exit I/O replay.
// It deliberately contains no captured log or artifact content.
type DockerSandboxIOReceiptIDs struct {
	LogReceiptID           string
	OutputStagingReceiptID string
	OutputCommitReceiptID  string
}

const dockerSandboxDenialEventIDPrefix = "sandbox-docker-product-denial-"

// RecordDockerSandboxDenial appends one deterministic metadata-only denial
// event. The event ID is derived from the already-redacted operation digest,
// so exact retries replay while a changed reason for the same operation fails
// closed. No persisted denial can grant launch or cleanup authority.
func (s *SQLiteStore) RecordDockerSandboxDenial(ctx context.Context,
	value domain.DockerSandboxDenial,
) (bool, error) {
	if value.Validate() != nil {
		return false, apperror.New(apperror.CodeInvalidArgument,
			"Docker Sandbox denial is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireSandboxManifestWriteLock(ctx, tx, value.RunID); err != nil {
		return false, err
	}
	var bound int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM sandbox_docker_container_plans plan
		WHERE plan.id = ? AND plan.run_id = ? AND plan.mission_id = ?
			AND plan.workspace_id = ? AND plan.requested_by = ?
			AND plan.network_mode = 'disabled' AND plan.network_target_count = 0
	)`, value.PlanID, value.RunID, value.MissionID, value.WorkspaceID,
		value.RequestedBy).Scan(&bound); err != nil {
		return false, err
	}
	if bound != 1 {
		return false, apperror.New(apperror.CodeConflict,
			"Docker Sandbox denial does not bind the exact plan")
	}
	if _, found, lookupErr := getDockerSandboxAdmissionByOperation(ctx, tx,
		value.OperationKeyDigest); lookupErr != nil {
		return false, lookupErr
	} else if found {
		return false, apperror.New(apperror.CodeConflict,
			"Docker Sandbox operation is already authorized")
	}
	eventID := dockerSandboxDenialEventID(value.OperationKeyDigest)
	if existing, found, lookupErr := getDockerSandboxRunEventByEventID(ctx, tx, eventID); lookupErr != nil {
		return false, lookupErr
	} else if found {
		if !dockerSandboxDenialEventMatches(existing, value) {
			return false, apperror.New(apperror.CodeConflict,
				"Docker Sandbox operation already has a different denial")
		}
		return false, nil
	}
	event, err := newDockerSandboxDenialEvent(value)
	if err != nil {
		return false, err
	}
	if _, err = insertRunEventTx(ctx, tx, event); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) GetDockerSandboxDenialByOperation(ctx context.Context,
	operationDigest string,
) (string, string, bool, error) {
	operationDigest = strings.TrimSpace(operationDigest)
	if !validStoreDigest(operationDigest) {
		return "", "", false, apperror.New(apperror.CodeInvalidArgument,
			"Docker Sandbox denial operation digest is invalid")
	}
	event, found, err := getDockerSandboxRunEventByEventID(ctx, s.db,
		dockerSandboxDenialEventID(operationDigest))
	if err != nil || !found {
		return "", "", found, err
	}
	var payload struct {
		Decision        string `json:"decision"`
		ReasonCode      string `json:"reason_code"`
		RemediationCode string `json:"remediation_code"`
		NetworkMode     string `json:"network_mode"`
	}
	if event.Type != events.SandboxDockerProductAdmissionDeniedEvent ||
		event.Source != "sandbox_docker_product" ||
		json.Unmarshal([]byte(event.PayloadJSON), &payload) != nil ||
		payload.Decision != domain.DockerSandboxAdmissionDenied ||
		payload.NetworkMode != "disabled" {
		return "", "", false, apperror.New(apperror.CodeConflict,
			"Docker Sandbox denial event is inconsistent")
	}
	var workspaceID, requestedBy string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_id, requested_by
		FROM sandbox_docker_container_plans WHERE id = ? AND run_id = ? AND mission_id = ?`,
		event.SubjectID, event.RunID, event.MissionID).Scan(&workspaceID, &requestedBy); err != nil {
		return "", "", false, apperror.New(apperror.CodeConflict,
			"Docker Sandbox denial plan binding is inconsistent")
	}
	denial := domain.DockerSandboxDenial{
		ProtocolVersion:    domain.DockerSandboxDenialProtocolVersion,
		OperationKeyDigest: operationDigest, RunID: event.RunID,
		MissionID: event.MissionID, WorkspaceID: workspaceID,
		PlanID: event.SubjectID, RequestedBy: requestedBy,
		ReasonCode: payload.ReasonCode, RemediationCode: payload.RemediationCode,
		NetworkMode: payload.NetworkMode, CreatedAt: event.CreatedAt,
	}
	denial.DenialFingerprint = domain.DockerSandboxDenialFingerprint(denial)
	if denial.Validate() != nil {
		return "", "", false, apperror.New(apperror.CodeConflict,
			"Docker Sandbox denial facts are invalid")
	}
	return payload.ReasonCode, payload.RemediationCode, true, nil
}

// RequestDockerSandboxCancellation appends one exact, idempotent cancellation
// request and a metadata-only event. It cannot grant start authority and never
// mutates the admission or lifecycle intent.
func (s *SQLiteStore) RequestDockerSandboxCancellation(ctx context.Context,
	value domain.DockerSandboxCancellation,
) (domain.DockerSandboxCancellation, bool, error) {
	if value.Validate() != nil {
		return domain.DockerSandboxCancellation{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox cancellation is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.DockerSandboxCancellation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	admission, err := getDockerSandboxAdmission(ctx, tx, value.AdmissionID)
	if err != nil {
		return domain.DockerSandboxCancellation{}, false, err
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, admission.RunID); err != nil {
		return domain.DockerSandboxCancellation{}, false, err
	}
	if existing, found, err := getDockerSandboxCancellationByOperation(ctx, tx,
		value.OperationKeyDigest); err != nil {
		return domain.DockerSandboxCancellation{}, false, err
	} else if found {
		if existing.ID != value.ID ||
			existing.CancellationFingerprint != value.CancellationFingerprint {
			return domain.DockerSandboxCancellation{}, false, apperror.New(
				apperror.CodeConflict,
				"Docker Sandbox cancellation operation was used for a different request")
		}
		if err := tx.Commit(); err != nil {
			return domain.DockerSandboxCancellation{}, false, err
		}
		return existing, true, nil
	}
	if _, found, err := getDockerSandboxCancellation(ctx, tx,
		value.AdmissionID); err != nil {
		return domain.DockerSandboxCancellation{}, false, err
	} else if found {
		return domain.DockerSandboxCancellation{}, false, apperror.New(
			apperror.CodeConflict,
			"Docker Sandbox admission already has a different cancellation request")
	}
	if value.RequestedAt.After(time.Now().UTC()) {
		return domain.DockerSandboxCancellation{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"Docker Sandbox cancellation timestamp is in the future")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_product_cancellations
		(id, admission_id, protocol_version, run_id, requested_by,
		operation_key_digest, reason_code, cancellation_fingerprint, requested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, value.ID, value.AdmissionID,
		value.ProtocolVersion, value.RunID, value.RequestedBy,
		value.OperationKeyDigest, value.ReasonCode, value.CancellationFingerprint,
		ts(value.RequestedAt)); err != nil {
		return domain.DockerSandboxCancellation{}, false, dockerSandboxConstraintError(err,
			"Docker Sandbox cancellation does not match the active product admission")
	}
	if err := appendDockerSandboxEvent(ctx, tx, admission,
		events.SandboxDockerProductCancelRequestedEvent, value.RequestedAt,
		map[string]any{"reason_code": value.ReasonCode, "status": "requested"}); err != nil {
		return domain.DockerSandboxCancellation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.DockerSandboxCancellation{}, false, err
	}
	return value, false, nil
}

func (s *SQLiteStore) GetDockerSandboxCancellation(ctx context.Context,
	admissionID string,
) (domain.DockerSandboxCancellation, bool, error) {
	admissionID = strings.TrimSpace(admissionID)
	if !validDockerSandboxIdentity(admissionID) {
		return domain.DockerSandboxCancellation{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox admission id is invalid")
	}
	return getDockerSandboxCancellation(ctx, s.db, admissionID)
}

// BeginDockerSandboxStart durably records the independent product Start
// operation before any lifecycle or daemon write. The WAL row and its Run
// event are committed together and contain metadata only.
func (s *SQLiteStore) BeginDockerSandboxStart(ctx context.Context,
	value domain.DockerSandboxStartIntent,
) (domain.DockerSandboxStartIntent, bool, error) {
	if value.Validate() != nil {
		return domain.DockerSandboxStartIntent{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox start request is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.DockerSandboxStartIntent{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	admission, err := getDockerSandboxAdmission(ctx, tx, value.AdmissionID)
	if err != nil {
		return domain.DockerSandboxStartIntent{}, false, err
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, admission.RunID); err != nil {
		return domain.DockerSandboxStartIntent{}, false, err
	}
	if existing, found, lookupErr := getDockerSandboxStartByOperation(ctx, tx,
		value.OperationKeyDigest); lookupErr != nil {
		return domain.DockerSandboxStartIntent{}, false, lookupErr
	} else if found {
		if existing.AdmissionID != value.AdmissionID ||
			existing.StartFingerprint != value.StartFingerprint {
			return domain.DockerSandboxStartIntent{}, false, apperror.New(
				apperror.CodeConflict,
				"Docker Sandbox start operation was used for a different request")
		}
		if err := tx.Commit(); err != nil {
			return domain.DockerSandboxStartIntent{}, false, err
		}
		return existing, true, nil
	}
	if _, found, lookupErr := getDockerSandboxStart(ctx, tx,
		value.AdmissionID); lookupErr != nil {
		return domain.DockerSandboxStartIntent{}, false, lookupErr
	} else if found {
		return domain.DockerSandboxStartIntent{}, false, apperror.New(
			apperror.CodeConflict,
			"Docker Sandbox admission already has a different start request")
	}
	if value.CreatedAt.After(time.Now().UTC()) {
		return domain.DockerSandboxStartIntent{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"Docker Sandbox start timestamp is in the future")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_product_start_requests
		(admission_id, protocol_version, operation_key_digest, request_fingerprint,
		runtime_epoch_fingerprint, run_id, requested_by, created_at, start_fingerprint)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, value.AdmissionID,
		value.ProtocolVersion, value.OperationKeyDigest, value.RequestFingerprint,
		value.RuntimeEpochFingerprint, value.RunID, value.RequestedBy,
		ts(value.CreatedAt), value.StartFingerprint); err != nil {
		return domain.DockerSandboxStartIntent{}, false, dockerSandboxConstraintError(err,
			"Docker Sandbox start request no longer matches current authority")
	}
	if err := appendDockerSandboxEvent(ctx, tx, admission,
		events.SandboxDockerProductStartRequestedEvent, value.CreatedAt,
		map[string]any{"status": "requested"}); err != nil {
		return domain.DockerSandboxStartIntent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.DockerSandboxStartIntent{}, false, err
	}
	return value, false, nil
}

func (s *SQLiteStore) GetDockerSandboxStart(ctx context.Context,
	admissionID string,
) (domain.DockerSandboxStartIntent, bool, error) {
	admissionID = strings.TrimSpace(admissionID)
	if !validDockerSandboxIdentity(admissionID) {
		return domain.DockerSandboxStartIntent{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox admission id is invalid")
	}
	return getDockerSandboxStart(ctx, s.db, admissionID)
}

// CreateDockerSandboxAdmission atomically records one exact, currently valid
// product admission and a metadata-only Run event. The admission remains audit
// evidence only; callers must still match the process-local runtime epoch and
// bind a fresh launch before performing a Docker start write.
func (s *SQLiteStore) CreateDockerSandboxAdmission(ctx context.Context,
	admission domain.DockerSandboxAdmission,
) (domain.DockerSandboxRecord, bool, error) {
	if admission.Validate() != nil ||
		admission.Decision != domain.DockerSandboxAdmissionAuthorized {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox product admission is invalid")
	}
	if err := ctx.Err(); err != nil {
		return domain.DockerSandboxRecord{}, false, apperror.Normalize(err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireSandboxManifestWriteLock(ctx, tx, admission.RunID); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	if existing, found, err := getDockerSandboxAdmissionByOperation(ctx, tx,
		admission.OperationKeyDigest); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	} else if found {
		if existing.ID != admission.ID ||
			existing.AdmissionFingerprint != admission.AdmissionFingerprint {
			return domain.DockerSandboxRecord{}, false, apperror.New(
				apperror.CodeConflict,
				"Docker Sandbox admission operation was used for a different request")
		}
		if err := tx.Commit(); err != nil {
			return domain.DockerSandboxRecord{}, false, err
		}
		record, err := getDockerSandboxRecord(ctx, s.db, existing.ID)
		if err != nil {
			return domain.DockerSandboxRecord{}, false, err
		}
		record.Replayed = true
		return record, true, record.Validate()
	}
	if _, denied, err := getDockerSandboxRunEventByEventID(ctx, tx,
		dockerSandboxDenialEventID(admission.OperationKeyDigest)); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	} else if denied {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeConflict,
			"Docker Sandbox operation was already denied")
	}
	if _, found, err := getDockerSandboxAdmissionByLifecycleOperation(ctx, tx,
		admission.LifecycleOperationDigest); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	} else if found {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeConflict,
			"Docker Sandbox lifecycle operation already has a product admission")
	}
	if _, err := getDockerSandboxAdmission(ctx, tx, admission.ID); err == nil {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeConflict, "Docker Sandbox admission id already exists")
	} else if apperror.CodeOf(err) != apperror.CodeNotFound {
		return domain.DockerSandboxRecord{}, false, err
	}
	now := time.Now().UTC()
	if admission.CreatedAt.After(now) {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"Docker Sandbox admission timestamp is in the future")
	}
	if !admission.ReadinessExpiresAt.After(now) {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeFailedPrecondition,
			"Docker Sandbox readiness evidence expired before admission")
	}
	if err := insertDockerSandboxAdmissionTx(ctx, tx, admission); err != nil {
		return domain.DockerSandboxRecord{}, false, dockerSandboxConstraintError(err,
			"Docker Sandbox admission no longer matches current authority")
	}
	if err := appendDockerSandboxEvent(ctx, tx, admission,
		events.SandboxDockerProductAdmittedEvent, admission.CreatedAt,
		map[string]any{
			"decision":                   admission.Decision,
			"reason_code":                admission.ReasonCode,
			"remediation_code":           admission.RemediationCode,
			"network_mode":               admission.NetworkMode,
			"product_entry_enabled":      true,
			"execution_authorized":       true,
			"artifact_commit_authorized": true,
		}); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	record := domain.DockerSandboxRecord{Admission: admission}
	return record, false, record.Validate()
}

func (s *SQLiteStore) GetDockerSandboxAdmission(ctx context.Context,
	id string,
) (domain.DockerSandboxAdmission, error) {
	id = strings.TrimSpace(id)
	if !validDockerSandboxIdentity(id) {
		return domain.DockerSandboxAdmission{}, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox admission id is invalid")
	}
	return getDockerSandboxAdmission(ctx, s.db, id)
}

func (s *SQLiteStore) GetDockerSandboxAdmissionByOperation(ctx context.Context,
	operationDigest string,
) (domain.DockerSandboxAdmission, bool, error) {
	operationDigest = strings.TrimSpace(operationDigest)
	if !validStoreDigest(operationDigest) {
		return domain.DockerSandboxAdmission{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"Docker Sandbox admission operation digest is invalid")
	}
	return getDockerSandboxAdmissionByOperation(ctx, s.db, operationDigest)
}

func (s *SQLiteStore) GetDockerSandboxAdmissionByLifecycleOperation(ctx context.Context,
	lifecycleOperationDigest string,
) (domain.DockerSandboxAdmission, bool, error) {
	lifecycleOperationDigest = strings.TrimSpace(lifecycleOperationDigest)
	if !validStoreDigest(lifecycleOperationDigest) {
		return domain.DockerSandboxAdmission{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"Docker Sandbox lifecycle operation digest is invalid")
	}
	return getDockerSandboxAdmissionByLifecycleOperation(ctx, s.db,
		lifecycleOperationDigest)
}

// BindDockerSandboxLaunch turns the v97 non-authorizing lifecycle intent into
// a product launch binding only while every persisted authority input is still
// current. It never changes the v97 authority flags.
func (s *SQLiteStore) BindDockerSandboxLaunch(ctx context.Context,
	launch domain.DockerSandboxLaunch,
) (domain.DockerSandboxRecord, bool, error) {
	if launch.Validate() != nil {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox launch binding is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	admission, err := getDockerSandboxAdmission(ctx, tx, launch.AdmissionID)
	if err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, admission.RunID); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	if existing, found, err := getDockerSandboxLaunch(ctx, tx, launch.AdmissionID); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	} else if found {
		if existing.LaunchFingerprint != launch.LaunchFingerprint {
			return domain.DockerSandboxRecord{}, false, apperror.New(
				apperror.CodeConflict,
				"Docker Sandbox admission was bound to a different launch")
		}
		if err := tx.Commit(); err != nil {
			return domain.DockerSandboxRecord{}, false, err
		}
		record, err := getDockerSandboxRecord(ctx, s.db, launch.AdmissionID)
		if err != nil {
			return domain.DockerSandboxRecord{}, false, err
		}
		record.Replayed = true
		return record, true, record.Validate()
	}
	if launch.CreatedAt.After(time.Now().UTC()) {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox launch timestamp is in the future")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_product_launches
		(admission_id, protocol_version, start_operation_key_digest,
		lifecycle_intent_id, attempt_id, run_id,
		lifecycle_request_fingerprint, launch_fingerprint, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		launch.AdmissionID, launch.ProtocolVersion,
		nullableText(launch.StartOperationKeyDigest), launch.LifecycleIntentID,
		launch.AttemptID, launch.RunID, launch.LifecycleRequestFingerprint,
		launch.LaunchFingerprint,
		ts(launch.CreatedAt)); err != nil {
		return domain.DockerSandboxRecord{}, false, dockerSandboxConstraintError(err,
			"Docker Sandbox launch no longer matches current authority")
	}
	if err := appendDockerSandboxEvent(ctx, tx, admission,
		events.SandboxDockerProductLaunchBoundEvent, launch.CreatedAt,
		map[string]any{
			"status":              "bound",
			"lifecycle_intent_id": launch.LifecycleIntentID,
			"attempt_id":          launch.AttemptID,
		}); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	record, err := getDockerSandboxRecord(ctx, s.db, launch.AdmissionID)
	return record, false, err
}

// CompleteDockerSandbox atomically binds the product terminal receipt to the
// v97 cleanup receipt and any v98 I/O receipts. Raw log and artifact content is
// never copied into this ledger or its Run event.
func (s *SQLiteStore) CompleteDockerSandbox(ctx context.Context,
	receipt domain.DockerSandboxReceipt,
) (domain.DockerSandboxRecord, bool, error) {
	if receipt.Validate() != nil {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox completion receipt is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	admission, err := getDockerSandboxAdmission(ctx, tx, receipt.AdmissionID)
	if err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, admission.RunID); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	if existing, found, err := getDockerSandboxReceipt(ctx, tx,
		receipt.AdmissionID); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	} else if found {
		if existing.ReceiptFingerprint != receipt.ReceiptFingerprint {
			return domain.DockerSandboxRecord{}, false, apperror.New(
				apperror.CodeConflict,
				"Docker Sandbox admission already has a different completion receipt")
		}
		if err := tx.Commit(); err != nil {
			return domain.DockerSandboxRecord{}, false, err
		}
		record, err := getDockerSandboxRecord(ctx, s.db, receipt.AdmissionID)
		if err != nil {
			return domain.DockerSandboxRecord{}, false, err
		}
		record.Replayed = true
		return record, true, record.Validate()
	}
	if receipt.CompletedAt.After(time.Now().UTC()) {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"Docker Sandbox completion timestamp is in the future")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_product_receipts
		(id, admission_id, protocol_version, lifecycle_intent_id, attempt_id, run_id,
		workspace_id, outcome, reason_code, exit_code, log_receipt_id,
		output_staging_receipt_id, output_commit_receipt_id, artifact_count,
		cleanup_complete, artifact_commit_authorized, receipt_fingerprint, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		receipt.ID, receipt.AdmissionID, receipt.ProtocolVersion,
		receipt.LifecycleIntentID, receipt.AttemptID, receipt.RunID,
		receipt.WorkspaceID, receipt.Outcome, receipt.ReasonCode,
		nullableInt(receipt.ExitCode), nullableText(receipt.LogReceiptID),
		nullableText(receipt.OutputStagingReceiptID),
		nullableText(receipt.OutputCommitReceiptID), receipt.ArtifactCount,
		boolInt(receipt.CleanupComplete), boolInt(receipt.ArtifactCommitAuthorized),
		receipt.ReceiptFingerprint, ts(receipt.CompletedAt)); err != nil {
		return domain.DockerSandboxRecord{}, false, dockerSandboxConstraintError(err,
			"Docker Sandbox completion does not match lifecycle cleanup and I/O receipts")
	}
	if err := appendDockerSandboxEvent(ctx, tx, admission,
		events.SandboxDockerProductCompletedEvent, receipt.CompletedAt,
		map[string]any{
			"outcome":                    receipt.Outcome,
			"reason_code":                receipt.ReasonCode,
			"cleanup_complete":           receipt.CleanupComplete,
			"artifact_count":             receipt.ArtifactCount,
			"artifact_commit_authorized": receipt.ArtifactCommitAuthorized,
		}); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	record, err := getDockerSandboxRecord(ctx, s.db, receipt.AdmissionID)
	return record, false, err
}

func (s *SQLiteStore) GetDockerSandboxRecord(ctx context.Context,
	admissionID string,
) (domain.DockerSandboxRecord, error) {
	admissionID = strings.TrimSpace(admissionID)
	if !validDockerSandboxIdentity(admissionID) {
		return domain.DockerSandboxRecord{}, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox admission id is invalid")
	}
	return getDockerSandboxRecord(ctx, s.db, admissionID)
}

func (s *SQLiteStore) GetDockerSandboxRecordByLifecycleIntent(ctx context.Context,
	lifecycleIntentID string,
) (domain.DockerSandboxRecord, bool, error) {
	lifecycleIntentID = strings.TrimSpace(lifecycleIntentID)
	if !validDockerSandboxIdentity(lifecycleIntentID) {
		return domain.DockerSandboxRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"Docker Sandbox lifecycle intent id is invalid")
	}
	var admissionID string
	err := s.db.QueryRowContext(ctx, `SELECT admission_id
		FROM sandbox_docker_product_launches WHERE lifecycle_intent_id = ?`,
		lifecycleIntentID).Scan(&admissionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxRecord{}, false, nil
	}
	if err != nil {
		return domain.DockerSandboxRecord{}, false, err
	}
	record, err := getDockerSandboxRecord(ctx, s.db, admissionID)
	return record, err == nil, err
}

func (s *SQLiteStore) ListRecoverableDockerSandboxes(ctx context.Context,
	limit int,
) ([]domain.DockerSandboxRecord, error) {
	if limit < 1 || limit > 100 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"Docker Sandbox recovery limit must be between 1 and 100")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT admission.id
		FROM sandbox_docker_product_admissions admission
		LEFT JOIN sandbox_docker_product_receipts receipt
			ON receipt.admission_id = admission.id
		WHERE receipt.admission_id IS NULL
		ORDER BY admission.created_at, admission.id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]domain.DockerSandboxRecord, 0, len(ids))
	for _, id := range ids {
		value, err := getDockerSandboxRecord(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// ListCompletedStandardCodeDockerSandboxes returns only bounded terminal
// records that carry the fixed Standard Code runner protocol. The application
// still performs the full exact-manifest parse before using a record. This
// narrow query closes the crash window between the Docker receipt and the
// Drydock checkpoint without allowing recovery to turn an admission into a
// new start.
func (s *SQLiteStore) ListCompletedStandardCodeDockerSandboxes(ctx context.Context,
	limit int,
) ([]domain.DockerSandboxRecord, error) {
	if limit < 1 || limit > 100 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"Standard Code Docker recovery limit must be between 1 and 100")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT admission.id
		FROM sandbox_docker_product_admissions admission
		JOIN sandbox_docker_product_receipts receipt
			ON receipt.admission_id = admission.id
		WHERE json_extract(admission.manifest_json, '$.command.executable') = ?
			AND json_extract(admission.manifest_json, '$.command.arguments[0]') = ?
		ORDER BY receipt.completed_at DESC, admission.id DESC LIMIT ?`,
		sandbox.DockerStandardCodeRunnerExecutable,
		sandbox.DockerStandardCodeRunnerProtocolVersion, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]domain.DockerSandboxRecord, 0, len(ids))
	for _, id := range ids {
		value, err := getDockerSandboxRecord(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// GetDockerSandboxIOReceiptIDsByAttempt lets crash recovery converge on v98
// receipts without exposing or recapturing their content.
func (s *SQLiteStore) GetDockerSandboxIOReceiptIDsByAttempt(ctx context.Context,
	attemptID string,
) (DockerSandboxIOReceiptIDs, error) {
	attemptID = strings.TrimSpace(attemptID)
	if !validDockerSandboxIdentity(attemptID) {
		return DockerSandboxIOReceiptIDs{}, apperror.New(
			apperror.CodeInvalidArgument, "Docker Sandbox attempt id is invalid")
	}
	var logID, stagingID, commitID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT id FROM sandbox_docker_log_capture_receipts
			WHERE attempt_id = ? ORDER BY created_at DESC, id DESC LIMIT 1),
		(SELECT id FROM sandbox_docker_output_staging_receipts
			WHERE attempt_id = ? ORDER BY created_at DESC, id DESC LIMIT 1),
		(SELECT id FROM sandbox_docker_output_commit_receipts
			WHERE attempt_id = ? ORDER BY created_at DESC, id DESC LIMIT 1)`,
		attemptID, attemptID, attemptID).Scan(&logID, &stagingID, &commitID)
	if err != nil {
		return DockerSandboxIOReceiptIDs{}, err
	}
	return DockerSandboxIOReceiptIDs{LogReceiptID: logID.String,
		OutputStagingReceiptID: stagingID.String,
		OutputCommitReceiptID:  commitID.String}, nil
}

func getDockerSandboxRecord(ctx context.Context, queryer dockerSandboxQueryer,
	admissionID string,
) (domain.DockerSandboxRecord, error) {
	admission, err := getDockerSandboxAdmission(ctx, queryer, admissionID)
	if err != nil {
		return domain.DockerSandboxRecord{}, err
	}
	record := domain.DockerSandboxRecord{Admission: admission}
	if start, found, err := getDockerSandboxStart(ctx, queryer,
		admissionID); err != nil {
		return domain.DockerSandboxRecord{}, err
	} else if found {
		record.Start = &start
	}
	if launch, found, err := getDockerSandboxLaunch(ctx, queryer,
		admissionID); err != nil {
		return domain.DockerSandboxRecord{}, err
	} else if found {
		record.Launch = &launch
	}
	if receipt, found, err := getDockerSandboxReceipt(ctx, queryer,
		admissionID); err != nil {
		return domain.DockerSandboxRecord{}, err
	} else if found {
		record.Receipt = &receipt
	}
	return record, record.Validate()
}

func getDockerSandboxCancellation(ctx context.Context, queryer dockerSandboxQueryer,
	admissionID string,
) (domain.DockerSandboxCancellation, bool, error) {
	var value domain.DockerSandboxCancellation
	var requestedAt string
	err := queryer.QueryRowContext(ctx, `SELECT id, admission_id, protocol_version,
		run_id, requested_by, operation_key_digest, reason_code,
		cancellation_fingerprint, requested_at
		FROM sandbox_docker_product_cancellations WHERE admission_id = ?`,
		admissionID).Scan(&value.ID, &value.AdmissionID, &value.ProtocolVersion,
		&value.RunID, &value.RequestedBy, &value.OperationKeyDigest,
		&value.ReasonCode, &value.CancellationFingerprint, &requestedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxCancellation{}, false, nil
	}
	if err != nil {
		return domain.DockerSandboxCancellation{}, false, err
	}
	value.RequestedAt = parseTS(requestedAt)
	return value, true, value.Validate()
}

func getDockerSandboxStart(ctx context.Context, queryer dockerSandboxQueryer,
	admissionID string,
) (domain.DockerSandboxStartIntent, bool, error) {
	var value domain.DockerSandboxStartIntent
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT admission_id, protocol_version,
		operation_key_digest, request_fingerprint, runtime_epoch_fingerprint,
		run_id, requested_by, created_at, start_fingerprint
		FROM sandbox_docker_product_start_requests WHERE admission_id = ?`,
		admissionID).Scan(&value.AdmissionID, &value.ProtocolVersion,
		&value.OperationKeyDigest, &value.RequestFingerprint,
		&value.RuntimeEpochFingerprint, &value.RunID, &value.RequestedBy,
		&createdAt, &value.StartFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxStartIntent{}, false, nil
	}
	if err != nil {
		return domain.DockerSandboxStartIntent{}, false, err
	}
	value.CreatedAt = parseTS(createdAt)
	return value, true, value.Validate()
}

func getDockerSandboxStartByOperation(ctx context.Context,
	queryer dockerSandboxQueryer, operationDigest string,
) (domain.DockerSandboxStartIntent, bool, error) {
	var admissionID string
	err := queryer.QueryRowContext(ctx, `SELECT admission_id
		FROM sandbox_docker_product_start_requests WHERE operation_key_digest = ?`,
		operationDigest).Scan(&admissionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxStartIntent{}, false, nil
	}
	if err != nil {
		return domain.DockerSandboxStartIntent{}, false, err
	}
	return getDockerSandboxStart(ctx, queryer, admissionID)
}

func getDockerSandboxCancellationByOperation(ctx context.Context,
	queryer dockerSandboxQueryer, operationDigest string,
) (domain.DockerSandboxCancellation, bool, error) {
	var admissionID string
	err := queryer.QueryRowContext(ctx, `SELECT admission_id
		FROM sandbox_docker_product_cancellations WHERE operation_key_digest = ?`,
		operationDigest).Scan(&admissionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxCancellation{}, false, nil
	}
	if err != nil {
		return domain.DockerSandboxCancellation{}, false, err
	}
	return getDockerSandboxCancellation(ctx, queryer, admissionID)
}

func getDockerSandboxAdmission(ctx context.Context, queryer dockerSandboxQueryer,
	id string,
) (domain.DockerSandboxAdmission, error) {
	value, err := scanDockerSandboxAdmission(queryer.QueryRowContext(ctx,
		dockerSandboxAdmissionSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxAdmission{}, apperror.New(
			apperror.CodeNotFound, "Docker Sandbox admission not found")
	}
	return value, err
}

func getDockerSandboxAdmissionByOperation(ctx context.Context,
	queryer dockerSandboxQueryer, digest string,
) (domain.DockerSandboxAdmission, bool, error) {
	value, err := scanDockerSandboxAdmission(queryer.QueryRowContext(ctx,
		dockerSandboxAdmissionSelect+` WHERE operation_key_digest = ?`, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxAdmission{}, false, nil
	}
	return value, err == nil, err
}

func getDockerSandboxAdmissionByLifecycleOperation(ctx context.Context,
	queryer dockerSandboxQueryer, digest string,
) (domain.DockerSandboxAdmission, bool, error) {
	value, err := scanDockerSandboxAdmission(queryer.QueryRowContext(ctx,
		dockerSandboxAdmissionSelect+` WHERE lifecycle_operation_digest = ?`, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxAdmission{}, false, nil
	}
	return value, err == nil, err
}

func scanDockerSandboxAdmission(row scanner) (domain.DockerSandboxAdmission, error) {
	var value domain.DockerSandboxAdmission
	var readinessExpiresAt, createdAt string
	var productEntryEnabled, executionAuthorized, artifactCommitAuthorized int
	err := row.Scan(&value.ID, &value.ProtocolVersion, &value.OperationKeyDigest,
		&value.RequestFingerprint, &value.LifecycleOperationDigest, &value.RunID,
		&value.MissionID, &value.WorkspaceID, &value.PlanID, &value.CandidateID,
		&value.PreparationID, &value.ManifestJSON, &value.ManifestFingerprint,
		&value.PlanFingerprint, &value.SpecFingerprint, &value.AuthorityFingerprint,
		&value.ReadinessFingerprint, &readinessExpiresAt,
		&value.RuntimeEpochFingerprint, &value.ProfileSnapshotID,
		&value.ProfileRevision, &value.PermissionSnapshotID,
		&value.PermissionRevision, &value.PermissionMode, &value.ApprovalID,
		&value.ApprovalVersion, &value.PolicyFingerprint, &value.NetworkMode,
		&value.NetworkTargetCount, &value.CPUQuotaMillis, &value.MemoryBytes,
		&value.PIDs, &value.DiskBytes, &value.WallClockSeconds, &value.LogBytes,
		&value.LogLines, &value.ToolCallsRemaining, &value.Decision,
		&value.ReasonCode, &value.RemediationCode, &productEntryEnabled,
		&executionAuthorized, &artifactCommitAuthorized, &value.RequestedBy,
		&createdAt, &value.AdmissionFingerprint)
	if err != nil {
		return domain.DockerSandboxAdmission{}, err
	}
	value.ReadinessExpiresAt = parseTS(readinessExpiresAt)
	value.CreatedAt = parseTS(createdAt)
	value.ProductEntryEnabled = productEntryEnabled != 0
	value.ExecutionAuthorized = executionAuthorized != 0
	value.ArtifactCommitAuthorized = artifactCommitAuthorized != 0
	return value, value.Validate()
}

func getDockerSandboxLaunch(ctx context.Context, queryer dockerSandboxQueryer,
	admissionID string,
) (domain.DockerSandboxLaunch, bool, error) {
	var value domain.DockerSandboxLaunch
	var startOperationKeyDigest sql.NullString
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT admission_id, protocol_version,
		start_operation_key_digest, lifecycle_intent_id, attempt_id, run_id, lifecycle_request_fingerprint,
		launch_fingerprint, created_at
		FROM sandbox_docker_product_launches WHERE admission_id = ?`, admissionID).Scan(
		&value.AdmissionID, &value.ProtocolVersion, &startOperationKeyDigest,
		&value.LifecycleIntentID,
		&value.AttemptID, &value.RunID, &value.LifecycleRequestFingerprint,
		&value.LaunchFingerprint, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxLaunch{}, false, nil
	}
	if err != nil {
		return domain.DockerSandboxLaunch{}, false, err
	}
	value.StartOperationKeyDigest = startOperationKeyDigest.String
	value.CreatedAt = parseTS(createdAt)
	return value, true, value.Validate()
}

func getDockerSandboxReceipt(ctx context.Context, queryer dockerSandboxQueryer,
	admissionID string,
) (domain.DockerSandboxReceipt, bool, error) {
	var value domain.DockerSandboxReceipt
	var exitCode sql.NullInt64
	var logID, stagingID, commitID sql.NullString
	var cleanupComplete, artifactCommitAuthorized int
	var completedAt string
	err := queryer.QueryRowContext(ctx, `SELECT id, admission_id, protocol_version,
		lifecycle_intent_id, attempt_id, run_id, workspace_id, outcome, reason_code,
		exit_code, log_receipt_id, output_staging_receipt_id,
		output_commit_receipt_id, artifact_count, cleanup_complete,
		artifact_commit_authorized, receipt_fingerprint, completed_at
		FROM sandbox_docker_product_receipts WHERE admission_id = ?`, admissionID).Scan(
		&value.ID, &value.AdmissionID, &value.ProtocolVersion,
		&value.LifecycleIntentID, &value.AttemptID, &value.RunID,
		&value.WorkspaceID, &value.Outcome, &value.ReasonCode, &exitCode,
		&logID, &stagingID, &commitID, &value.ArtifactCount, &cleanupComplete,
		&artifactCommitAuthorized, &value.ReceiptFingerprint, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DockerSandboxReceipt{}, false, nil
	}
	if err != nil {
		return domain.DockerSandboxReceipt{}, false, err
	}
	if exitCode.Valid {
		current := int(exitCode.Int64)
		value.ExitCode = &current
	}
	value.LogReceiptID = logID.String
	value.OutputStagingReceiptID = stagingID.String
	value.OutputCommitReceiptID = commitID.String
	value.CleanupComplete = cleanupComplete != 0
	value.ArtifactCommitAuthorized = artifactCommitAuthorized != 0
	value.CompletedAt = parseTS(completedAt)
	return value, true, value.Validate()
}

func insertDockerSandboxAdmissionTx(ctx context.Context, tx *sql.Tx,
	value domain.DockerSandboxAdmission,
) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_product_admissions
		(id, protocol_version, operation_key_digest, request_fingerprint,
		lifecycle_operation_digest, run_id, mission_id, workspace_id, plan_id,
		candidate_id, preparation_id, manifest_json, manifest_fingerprint,
		plan_fingerprint, spec_fingerprint, authority_fingerprint,
		readiness_fingerprint, readiness_expires_at, runtime_epoch_fingerprint,
		profile_snapshot_id, profile_revision, permission_snapshot_id,
		permission_revision, permission_mode, approval_id, approval_version,
		policy_fingerprint, network_mode, network_target_count, cpu_quota_millis,
		memory_bytes, pids, disk_bytes, wall_clock_seconds, log_bytes, log_lines,
		tool_calls_remaining, decision, reason_code, remediation_code,
		product_entry_enabled, execution_authorized, artifact_commit_authorized,
		requested_by, created_at, admission_fingerprint)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ID, value.ProtocolVersion, value.OperationKeyDigest,
		value.RequestFingerprint, value.LifecycleOperationDigest, value.RunID,
		value.MissionID, value.WorkspaceID, value.PlanID, value.CandidateID,
		value.PreparationID, value.ManifestJSON, value.ManifestFingerprint,
		value.PlanFingerprint, value.SpecFingerprint, value.AuthorityFingerprint,
		value.ReadinessFingerprint, ts(value.ReadinessExpiresAt),
		value.RuntimeEpochFingerprint, value.ProfileSnapshotID, value.ProfileRevision,
		value.PermissionSnapshotID, value.PermissionRevision, value.PermissionMode,
		value.ApprovalID, value.ApprovalVersion, value.PolicyFingerprint,
		value.NetworkMode, value.NetworkTargetCount, value.CPUQuotaMillis,
		value.MemoryBytes, value.PIDs, value.DiskBytes, value.WallClockSeconds,
		value.LogBytes, value.LogLines, value.ToolCallsRemaining, value.Decision,
		value.ReasonCode, value.RemediationCode, boolInt(value.ProductEntryEnabled),
		boolInt(value.ExecutionAuthorized), boolInt(value.ArtifactCommitAuthorized),
		value.RequestedBy, ts(value.CreatedAt), value.AdmissionFingerprint)
	return err
}

func appendDockerSandboxEvent(ctx context.Context, tx *sql.Tx,
	admission domain.DockerSandboxAdmission, eventType string, createdAt time.Time,
	payload map[string]any,
) error {
	event, err := events.New(admission.RunID, admission.MissionID, eventType,
		"sandbox_docker_product", admission.ID, payload)
	if err != nil {
		return err
	}
	event.CreatedAt = createdAt
	_, err = insertRunEventTx(ctx, tx, event)
	return err
}

func dockerSandboxDenialEventID(operationDigest string) string {
	return dockerSandboxDenialEventIDPrefix + operationDigest
}

func newDockerSandboxDenialEvent(value domain.DockerSandboxDenial) (events.Event, error) {
	event, err := events.New(value.RunID, value.MissionID,
		events.SandboxDockerProductAdmissionDeniedEvent,
		"sandbox_docker_product", value.PlanID, map[string]any{
			"decision":                   domain.DockerSandboxAdmissionDenied,
			"reason_code":                value.ReasonCode,
			"remediation_code":           value.RemediationCode,
			"network_mode":               value.NetworkMode,
			"product_entry_enabled":      false,
			"execution_authorized":       false,
			"artifact_commit_authorized": false,
		})
	if err != nil {
		return events.Event{}, err
	}
	event.EventID = dockerSandboxDenialEventID(value.OperationKeyDigest)
	event.CreatedAt = value.CreatedAt
	return event, event.Validate()
}

func dockerSandboxDenialEventMatches(event events.Event,
	value domain.DockerSandboxDenial,
) bool {
	expected, err := newDockerSandboxDenialEvent(value)
	if err != nil {
		return false
	}
	return event.EventID == expected.EventID && event.RunID == expected.RunID &&
		event.MissionID == expected.MissionID && event.Type == expected.Type &&
		event.Source == expected.Source && event.SubjectID == expected.SubjectID &&
		event.PayloadJSON == expected.PayloadJSON
}

func getDockerSandboxRunEventByEventID(ctx context.Context, queryer dockerSandboxQueryer,
	eventID string,
) (events.Event, bool, error) {
	event, err := scanRunEvent(queryer.QueryRowContext(ctx, `SELECT id, event_id,
		version, run_id, mission_id, sequence, type, source, subject_id,
		payload_json, created_at FROM run_events WHERE event_id = ?`, eventID))
	if errors.Is(err, sql.ErrNoRows) {
		return events.Event{}, false, nil
	}
	return event, err == nil, err
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func validDockerSandboxIdentity(value string) bool {
	return domain.ValidAgentID(value) && !strings.ContainsRune(value, 0)
}

func dockerSandboxConstraintError(err error, message string) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "constraint") || strings.Contains(lower, "binding is invalid") ||
		strings.Contains(lower, "cannot be updated") || strings.Contains(lower, "cannot be deleted") {
		return apperror.Wrap(apperror.CodeConflict, message, err)
	}
	return err
}
