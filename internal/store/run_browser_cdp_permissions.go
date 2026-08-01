package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
)

const runBrowserCDPPermissionSnapshotSelect = `SELECT id, run_id, mission_id,
	revision, protocol_version, mode, navigate_allowed, dom_snapshot_allowed,
	screenshot_allowed, request_capture_allowed, request_mutation_allowed,
	request_replay_allowed, cookie_access_allowed, arbitrary_method_allowed,
	risk_tier, required_gate, policy_version, operator_confirmed,
	transport_enabled, browser_start_authorized, runtime_authorized,
	capability_grant, requested_by, reason, created_at
	FROM run_browser_cdp_permission_snapshots`

type runBrowserCDPPermissionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLiteStore) GetRunBrowserCDPPermission(ctx context.Context,
	runID string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || strings.ContainsRune(runID, 0) {
		return domain.RunBrowserCDPPermissionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument, "Run browser CDP permission Run id is invalid")
	}
	return getCurrentRunBrowserCDPPermissionSnapshot(ctx, s.db, runID)
}

func (s *SQLiteStore) GetRunBrowserCDPPermissionSnapshot(ctx context.Context,
	id string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	id = strings.TrimSpace(id)
	if !domain.ValidAgentID(id) || strings.ContainsRune(id, 0) {
		return domain.RunBrowserCDPPermissionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument,
			"Run browser CDP permission snapshot id is invalid")
	}
	return getRunBrowserCDPPermissionSnapshot(ctx, s.db, id)
}

func (s *SQLiteStore) GetRunBrowserCDPPermissionOperation(ctx context.Context,
	keyDigest string,
) (domain.RunBrowserCDPPermissionOperation, bool, error) {
	keyDigest = strings.TrimSpace(keyDigest)
	if !validStoreDigest(keyDigest) {
		return domain.RunBrowserCDPPermissionOperation{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"Run browser CDP permission operation digest is invalid")
	}
	return getRunBrowserCDPPermissionOperation(ctx, s.db, keyDigest)
}

func (s *SQLiteStore) TransitionRunBrowserCDPPermission(ctx context.Context,
	snapshot domain.RunBrowserCDPPermissionSnapshot,
	operation domain.RunBrowserCDPPermissionOperation, event events.Event,
) (domain.RunBrowserCDPPermissionSnapshot, bool, error) {
	if err := validateRunBrowserCDPPermissionMutation(snapshot, operation, event); err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunBrowserCDPPermissionWriteLockTx(ctx, tx, snapshot.RunID); err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	if existing, found, err := getRunBrowserCDPPermissionOperation(
		ctx, tx, operation.KeyDigest); err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	} else if found {
		if err := validateRunBrowserCDPPermissionReplay(existing, operation); err != nil {
			return domain.RunBrowserCDPPermissionSnapshot{}, false, err
		}
		stored, err := getRunBrowserCDPPermissionSnapshot(ctx, tx, existing.SnapshotID)
		if err != nil {
			return domain.RunBrowserCDPPermissionSnapshot{}, false, err
		}
		if err := validateRunBrowserCDPPermissionOperationBinding(existing, stored); err != nil {
			return domain.RunBrowserCDPPermissionSnapshot{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return domain.RunBrowserCDPPermissionSnapshot{}, false, err
		}
		return stored, true, nil
	}
	current, err := getCurrentRunBrowserCDPPermissionSnapshot(ctx, tx, snapshot.RunID)
	if err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	run, mission, err := getCoordinatorRunTx(ctx, tx, snapshot.RunID)
	if err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	if !domain.CanChangeRunBrowserCDPPermission(run.Status) {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, apperror.New(
			apperror.CodeFailedPrecondition,
			fmt.Sprintf(
				"Run browser CDP permission can only change while created or paused; Run is %s",
				run.Status))
	}
	var activeLeaseCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_execution_leases
		WHERE run_id = ? AND status = 'active' AND julianday(expires_at) > julianday('now')`,
		run.ID).Scan(&activeLeaseCount); err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	if activeLeaseCount != 0 {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run browser CDP permission cannot change while an execution lease is active")
	}
	if snapshot.MissionID != run.MissionID || snapshot.MissionID != mission.ID ||
		snapshot.Revision != current.Revision+1 || snapshot.Mode == current.Mode ||
		snapshot.ProtocolVersion != current.ProtocolVersion ||
		snapshot.PolicyVersion != current.PolicyVersion ||
		snapshot.CreatedAt.Before(current.CreatedAt) {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, apperror.New(
			apperror.CodeConflict,
			"Run browser CDP permission changed concurrently or attempted to change immutable policy")
	}
	if err := insertRunBrowserCDPPermissionSnapshotTx(ctx, tx, snapshot); err != nil {
		_ = tx.Rollback()
		return s.recoverRunBrowserCDPPermissionTransition(ctx, operation, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_browser_cdp_permission_operations
		(operation_key_digest, request_fingerprint, snapshot_id, run_id,
		requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`, operation.KeyDigest,
		operation.RequestFingerprint, operation.SnapshotID, operation.RunID,
		operation.RequestedBy, ts(operation.CreatedAt)); err != nil {
		_ = tx.Rollback()
		return s.recoverRunBrowserCDPPermissionTransition(ctx, operation, err)
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	return snapshot, false, nil
}

func insertInitialRunBrowserCDPPermissionSnapshotTx(ctx context.Context, tx *sql.Tx,
	snapshot domain.RunBrowserCDPPermissionSnapshot, run domain.Run, mission domain.Mission,
) error {
	if err := snapshot.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"initial Run browser CDP permission is invalid", err)
	}
	if err := requireRedactedRunBrowserCDPPermissionSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Revision != 1 || snapshot.RunID != run.ID ||
		snapshot.MissionID != run.MissionID || snapshot.MissionID != mission.ID ||
		snapshot.Mode != domain.RunBrowserCDPPermissionRestricted ||
		run.Status != domain.RunCreated || snapshot.CreatedAt.Before(run.CreatedAt) {
		return apperror.New(apperror.CodeInvalidArgument,
			"initial Run browser CDP permission does not match its created Run and Mission")
	}
	return insertRunBrowserCDPPermissionSnapshotTx(ctx, tx, snapshot)
}

func insertRunBrowserCDPPermissionSnapshotTx(ctx context.Context, tx *sql.Tx,
	snapshot domain.RunBrowserCDPPermissionSnapshot,
) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO run_browser_cdp_permission_snapshots
		(id, run_id, mission_id, revision, protocol_version, mode,
		navigate_allowed, dom_snapshot_allowed, screenshot_allowed,
		request_capture_allowed, request_mutation_allowed, request_replay_allowed,
		cookie_access_allowed, arbitrary_method_allowed, risk_tier, required_gate,
		policy_version, operator_confirmed, transport_enabled,
		browser_start_authorized, runtime_authorized, capability_grant,
		requested_by, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ID, snapshot.RunID, snapshot.MissionID, snapshot.Revision,
		snapshot.ProtocolVersion, snapshot.Mode, snapshot.NavigateAllowed,
		snapshot.DOMSnapshotAllowed, snapshot.ScreenshotAllowed,
		snapshot.RequestCaptureAllowed, snapshot.RequestMutationAllowed,
		snapshot.RequestReplayAllowed, snapshot.CookieAccessAllowed,
		snapshot.ArbitraryMethodAllowed, snapshot.RiskTier, snapshot.RequiredGate,
		snapshot.PolicyVersion, snapshot.OperatorConfirmed, snapshot.TransportEnabled,
		snapshot.BrowserStartAuthorized, snapshot.RuntimeAuthorized,
		snapshot.CapabilityGrant, snapshot.RequestedBy, snapshot.Reason,
		ts(snapshot.CreatedAt))
	return err
}

func validateRunBrowserCDPPermissionMutation(
	snapshot domain.RunBrowserCDPPermissionSnapshot,
	operation domain.RunBrowserCDPPermissionOperation, event events.Event,
) error {
	if err := snapshot.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Run browser CDP permission snapshot is invalid", err)
	}
	if err := requireRedactedRunBrowserCDPPermissionSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Revision <= 1 {
		return apperror.New(apperror.CodeInvalidArgument,
			"Run browser CDP permission transition revision must exceed one")
	}
	if err := operation.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Run browser CDP permission operation is invalid", err)
	}
	if operation.SnapshotID != snapshot.ID || operation.RunID != snapshot.RunID ||
		operation.RequestedBy != snapshot.RequestedBy ||
		!operation.CreatedAt.Equal(snapshot.CreatedAt) ||
		operation.RequestFingerprint != runBrowserCDPPermissionRequestFingerprint(snapshot) {
		return apperror.New(apperror.CodeInvalidArgument,
			"Run browser CDP permission operation does not match its snapshot")
	}
	if err := validateRunBrowserCDPPermissionSelectedEvent(event, snapshot); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Run browser CDP permission event is invalid", err)
	}
	return nil
}

func requireRedactedRunBrowserCDPPermissionSnapshot(
	snapshot domain.RunBrowserCDPPermissionSnapshot,
) error {
	if redact.String(snapshot.RequestedBy) != snapshot.RequestedBy ||
		redact.String(snapshot.Reason) != snapshot.Reason {
		return apperror.New(apperror.CodeInvalidArgument,
			"Run browser CDP permission requester and reason must be redacted before persistence")
	}
	return nil
}

func validateRunBrowserCDPPermissionSelectedEvent(event events.Event,
	snapshot domain.RunBrowserCDPPermissionSnapshot,
) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.Type != events.RunBrowserCDPPermissionSelectedEvent ||
		event.Source != "run_browser_cdp_permission" || event.RunID != snapshot.RunID ||
		event.MissionID != snapshot.MissionID || event.SubjectID != snapshot.ID ||
		!event.CreatedAt.Equal(snapshot.CreatedAt) {
		return errors.New("run browser CDP permission event identity does not match its snapshot")
	}
	if err := rejectDuplicateJSONFields(event.PayloadJSON); err != nil {
		return err
	}
	var payload struct {
		Protocol               string                             `json:"protocol"`
		Revision               int64                              `json:"revision"`
		From                   domain.RunBrowserCDPPermissionMode `json:"from"`
		To                     domain.RunBrowserCDPPermissionMode `json:"to"`
		NavigateAllowed        bool                               `json:"navigate_allowed"`
		DOMSnapshotAllowed     bool                               `json:"dom_snapshot_allowed"`
		ScreenshotAllowed      bool                               `json:"screenshot_allowed"`
		RequestCaptureAllowed  bool                               `json:"request_capture_allowed"`
		RequestMutationAllowed bool                               `json:"request_mutation_allowed"`
		RequestReplayAllowed   bool                               `json:"request_replay_allowed"`
		CookieAccessAllowed    bool                               `json:"cookie_access_allowed"`
		ArbitraryMethodAllowed bool                               `json:"arbitrary_method_allowed"`
		RiskTier               domain.ExecutionRiskTier           `json:"risk_tier"`
		RequiredGate           domain.BrowserCDPPermissionGate    `json:"required_gate"`
		PolicyVersion          string                             `json:"policy_version"`
		RequestedBy            string                             `json:"requested_by"`
		Reason                 string                             `json:"reason"`
		TransportEnabled       bool                               `json:"transport_enabled"`
		BrowserStartAuthorized bool                               `json:"browser_start_authorized"`
		RuntimeAuthorized      bool                               `json:"runtime_authorized"`
		CapabilityGrant        bool                               `json:"capability_grant"`
	}
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		return err
	}
	if payload.Protocol != snapshot.ProtocolVersion ||
		payload.Revision != snapshot.Revision || !payload.From.Valid() ||
		payload.From == snapshot.Mode || payload.To != snapshot.Mode ||
		payload.NavigateAllowed != snapshot.NavigateAllowed ||
		payload.DOMSnapshotAllowed != snapshot.DOMSnapshotAllowed ||
		payload.ScreenshotAllowed != snapshot.ScreenshotAllowed ||
		payload.RequestCaptureAllowed != snapshot.RequestCaptureAllowed ||
		payload.RequestMutationAllowed != snapshot.RequestMutationAllowed ||
		payload.RequestReplayAllowed != snapshot.RequestReplayAllowed ||
		payload.CookieAccessAllowed != snapshot.CookieAccessAllowed ||
		payload.ArbitraryMethodAllowed != snapshot.ArbitraryMethodAllowed ||
		payload.RiskTier != snapshot.RiskTier ||
		payload.RequiredGate != snapshot.RequiredGate ||
		payload.PolicyVersion != snapshot.PolicyVersion ||
		payload.RequestedBy != snapshot.RequestedBy || payload.Reason != snapshot.Reason ||
		payload.TransportEnabled || payload.BrowserStartAuthorized ||
		payload.RuntimeAuthorized || payload.CapabilityGrant {
		return errors.New("run browser CDP permission event payload does not match its snapshot")
	}
	return nil
}

func acquireRunBrowserCDPPermissionWriteLockTx(
	ctx context.Context, tx *sql.Tx, runID string,
) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE runs SET updated_at = updated_at WHERE id = ?`, runID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return apperror.New(apperror.CodeNotFound,
			"Run browser CDP permission Run was not found")
	}
	return nil
}

func (s *SQLiteStore) recoverRunBrowserCDPPermissionTransition(
	ctx context.Context, operation domain.RunBrowserCDPPermissionOperation, original error,
) (domain.RunBrowserCDPPermissionSnapshot, bool, error) {
	existing, found, err := getRunBrowserCDPPermissionOperation(ctx, s.db, operation.KeyDigest)
	if err != nil || !found {
		if err == nil {
			return domain.RunBrowserCDPPermissionSnapshot{}, false, original
		}
		return domain.RunBrowserCDPPermissionSnapshot{}, false, errors.Join(original, err)
	}
	if err := validateRunBrowserCDPPermissionReplay(existing, operation); err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	stored, err := getRunBrowserCDPPermissionSnapshot(ctx, s.db, existing.SnapshotID)
	if err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	if err := validateRunBrowserCDPPermissionOperationBinding(existing, stored); err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, false, err
	}
	return stored, true, nil
}

func validateRunBrowserCDPPermissionReplay(existing,
	request domain.RunBrowserCDPPermissionOperation,
) error {
	if existing.KeyDigest != request.KeyDigest ||
		existing.RequestFingerprint != request.RequestFingerprint ||
		existing.RunID != request.RunID || existing.RequestedBy != request.RequestedBy {
		return apperror.New(apperror.CodeConflict,
			"Run browser CDP permission operation key was already used for different intent")
	}
	return nil
}

func validateRunBrowserCDPPermissionOperationBinding(
	operation domain.RunBrowserCDPPermissionOperation,
	snapshot domain.RunBrowserCDPPermissionSnapshot,
) error {
	if operation.SnapshotID != snapshot.ID || operation.RunID != snapshot.RunID ||
		operation.RequestedBy != snapshot.RequestedBy ||
		!operation.CreatedAt.Equal(snapshot.CreatedAt) ||
		operation.RequestFingerprint != runBrowserCDPPermissionRequestFingerprint(snapshot) {
		return apperror.New(apperror.CodeInternal,
			"stored Run browser CDP permission operation binding is invalid")
	}
	return nil
}

func runBrowserCDPPermissionRequestFingerprint(
	snapshot domain.RunBrowserCDPPermissionSnapshot,
) string {
	return runmutation.Fingerprint("run_browser_cdp_permission_change_request.v1",
		snapshot.RunID, string(snapshot.Mode),
		fmt.Sprintf("%t", snapshot.OperatorConfirmed),
		snapshot.RequestedBy, snapshot.Reason)
}

func getRunBrowserCDPPermissionSnapshot(ctx context.Context,
	queryer runBrowserCDPPermissionQueryer, id string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	return scanRunBrowserCDPPermissionSnapshot(queryer.QueryRowContext(ctx,
		runBrowserCDPPermissionSnapshotSelect+` WHERE id = ?`, id))
}

func getCurrentRunBrowserCDPPermissionSnapshot(ctx context.Context,
	queryer runBrowserCDPPermissionQueryer, runID string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	return scanRunBrowserCDPPermissionSnapshot(queryer.QueryRowContext(ctx,
		runBrowserCDPPermissionSnapshotSelect+
			` WHERE run_id = ? ORDER BY revision DESC LIMIT 1`, runID))
}

func scanRunBrowserCDPPermissionSnapshot(scanner interface{ Scan(...any) error }) (
	domain.RunBrowserCDPPermissionSnapshot, error,
) {
	var snapshot domain.RunBrowserCDPPermissionSnapshot
	var createdAt string
	if err := scanner.Scan(&snapshot.ID, &snapshot.RunID, &snapshot.MissionID,
		&snapshot.Revision, &snapshot.ProtocolVersion, &snapshot.Mode,
		&snapshot.NavigateAllowed, &snapshot.DOMSnapshotAllowed,
		&snapshot.ScreenshotAllowed, &snapshot.RequestCaptureAllowed,
		&snapshot.RequestMutationAllowed, &snapshot.RequestReplayAllowed,
		&snapshot.CookieAccessAllowed, &snapshot.ArbitraryMethodAllowed,
		&snapshot.RiskTier, &snapshot.RequiredGate, &snapshot.PolicyVersion,
		&snapshot.OperatorConfirmed, &snapshot.TransportEnabled,
		&snapshot.BrowserStartAuthorized, &snapshot.RuntimeAuthorized,
		&snapshot.CapabilityGrant, &snapshot.RequestedBy, &snapshot.Reason,
		&createdAt); err != nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, err
	}
	snapshot.CreatedAt = parseTS(createdAt)
	return snapshot, snapshot.Validate()
}

func getRunBrowserCDPPermissionOperation(ctx context.Context,
	queryer runBrowserCDPPermissionQueryer, keyDigest string,
) (domain.RunBrowserCDPPermissionOperation, bool, error) {
	var operation domain.RunBrowserCDPPermissionOperation
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT operation_key_digest,
		request_fingerprint, snapshot_id, run_id, requested_by, created_at
		FROM run_browser_cdp_permission_operations WHERE operation_key_digest = ?`, keyDigest).
		Scan(&operation.KeyDigest, &operation.RequestFingerprint, &operation.SnapshotID,
			&operation.RunID, &operation.RequestedBy, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RunBrowserCDPPermissionOperation{}, false, nil
	}
	if err != nil {
		return domain.RunBrowserCDPPermissionOperation{}, false, err
	}
	operation.CreatedAt = parseTS(createdAt)
	return operation, true, operation.Validate()
}
