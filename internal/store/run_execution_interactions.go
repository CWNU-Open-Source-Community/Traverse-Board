package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
)

const runExecutionInteractionSnapshotSelect = `SELECT id, run_id, mission_id,
	revision, protocol_version, mode, surface, execution_profile,
	execution_profile_revision, workspace_trust, command_form, persistent_terminal,
	user_input_available, agent_input_default, network_scope, required_gate,
	policy_version, operator_confirmed, process_enabled, execution_authorized,
	capability_grant, requested_by, reason, created_at
	FROM run_execution_interaction_snapshots`

type runExecutionInteractionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLiteStore) GetRunExecutionInteraction(ctx context.Context,
	runID string,
) (domain.RunExecutionInteractionSnapshot, error) {
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || strings.ContainsRune(runID, 0) {
		return domain.RunExecutionInteractionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument, "Run execution interaction Run id is invalid")
	}
	return getCurrentRunExecutionInteractionSnapshot(ctx, s.db, runID)
}

func (s *SQLiteStore) GetRunExecutionInteractionSnapshot(ctx context.Context,
	id string,
) (domain.RunExecutionInteractionSnapshot, error) {
	id = strings.TrimSpace(id)
	if !domain.ValidAgentID(id) || strings.ContainsRune(id, 0) {
		return domain.RunExecutionInteractionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument,
			"Run execution interaction snapshot id is invalid")
	}
	return getRunExecutionInteractionSnapshot(ctx, s.db, id)
}

func (s *SQLiteStore) GetRunExecutionInteractionOperation(ctx context.Context,
	keyDigest string,
) (domain.RunExecutionInteractionOperation, bool, error) {
	keyDigest = strings.TrimSpace(keyDigest)
	if !validStoreDigest(keyDigest) {
		return domain.RunExecutionInteractionOperation{}, false, apperror.New(
			apperror.CodeInvalidArgument,
			"Run execution interaction operation digest is invalid")
	}
	return getRunExecutionInteractionOperation(ctx, s.db, keyDigest)
}

func (s *SQLiteStore) TransitionRunExecutionInteraction(ctx context.Context,
	snapshot domain.RunExecutionInteractionSnapshot,
	operation domain.RunExecutionInteractionOperation, event events.Event,
) (domain.RunExecutionInteractionSnapshot, bool, error) {
	if err := validateRunExecutionInteractionMutation(snapshot, operation, event); err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunExecutionInteractionWriteLockTx(ctx, tx, snapshot.RunID); err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	if existing, found, err := getRunExecutionInteractionOperation(ctx, tx,
		operation.KeyDigest); err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	} else if found {
		if err := validateRunExecutionInteractionReplay(existing, operation); err != nil {
			return domain.RunExecutionInteractionSnapshot{}, false, err
		}
		stored, err := getRunExecutionInteractionSnapshot(ctx, tx, existing.SnapshotID)
		if err != nil {
			return domain.RunExecutionInteractionSnapshot{}, false, err
		}
		if err := validateRunExecutionInteractionOperationBinding(existing, stored); err != nil {
			return domain.RunExecutionInteractionSnapshot{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return domain.RunExecutionInteractionSnapshot{}, false, err
		}
		return stored, true, nil
	}
	current, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx, snapshot.RunID)
	if err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	run, mission, err := getCoordinatorRunTx(ctx, tx, snapshot.RunID)
	if err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	if !domain.CanChangeRunExecutionInteraction(run.Status) {
		return domain.RunExecutionInteractionSnapshot{}, false, apperror.New(
			apperror.CodeFailedPrecondition,
			fmt.Sprintf(
				"Run execution interaction can only change while created or paused; Run is %s",
				run.Status))
	}
	var activeLeaseCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_execution_leases
		WHERE run_id = ? AND status = 'active'
			AND julianday(expires_at) > julianday('now')`, run.ID).
		Scan(&activeLeaseCount); err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	if activeLeaseCount != 0 {
		return domain.RunExecutionInteractionSnapshot{}, false, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run execution interaction cannot change while an execution lease is active")
	}
	currentMode, err := getCurrentRunModeSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	currentProfile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	if snapshot.MissionID != run.MissionID || snapshot.MissionID != mission.ID ||
		snapshot.Revision != current.Revision+1 ||
		snapshot.ProtocolVersion != current.ProtocolVersion ||
		snapshot.PolicyVersion != current.PolicyVersion ||
		snapshot.Surface != currentMode.Surface ||
		snapshot.ExecutionProfile != currentProfile.Profile ||
		snapshot.ExecutionProfileRevision != currentProfile.Revision ||
		snapshot.CreatedAt.Before(current.CreatedAt) {
		return domain.RunExecutionInteractionSnapshot{}, false, apperror.New(
			apperror.CodeConflict,
			"Run execution interaction changed concurrently or its policy binding is stale")
	}
	if err := insertRunExecutionInteractionSnapshotTx(ctx, tx, snapshot); err != nil {
		_ = tx.Rollback()
		return s.recoverRunExecutionInteractionTransition(ctx, operation, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_execution_interaction_operations
		(operation_key_digest, request_fingerprint, snapshot_id, run_id,
		requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`, operation.KeyDigest,
		operation.RequestFingerprint, operation.SnapshotID, operation.RunID,
		operation.RequestedBy, ts(operation.CreatedAt)); err != nil {
		_ = tx.Rollback()
		return s.recoverRunExecutionInteractionTransition(ctx, operation, err)
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	return snapshot, false, nil
}

func insertInitialRunExecutionInteractionSnapshotTx(ctx context.Context, tx *sql.Tx,
	snapshot domain.RunExecutionInteractionSnapshot, run domain.Run,
	mission domain.Mission, mode domain.RunModeSnapshot,
	profile domain.RunExecutionProfileSnapshot,
) error {
	if err := snapshot.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"initial Run execution interaction is invalid", err)
	}
	if err := requireRedactedRunExecutionInteractionSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Revision != 1 || snapshot.RunID != run.ID ||
		snapshot.MissionID != run.MissionID || snapshot.MissionID != mission.ID ||
		snapshot.Mode != domain.RunExecutionInteractionPreview ||
		snapshot.Surface != mode.Surface ||
		snapshot.ExecutionProfile != profile.Profile ||
		snapshot.ExecutionProfileRevision != profile.Revision ||
		run.Status != domain.RunCreated || snapshot.CreatedAt.Before(run.CreatedAt) {
		return apperror.New(apperror.CodeInvalidArgument,
			"initial Run execution interaction does not match its created Run")
	}
	return insertRunExecutionInteractionSnapshotTx(ctx, tx, snapshot)
}

func insertRunExecutionInteractionSnapshotTx(ctx context.Context, tx *sql.Tx,
	snapshot domain.RunExecutionInteractionSnapshot,
) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO run_execution_interaction_snapshots
		(id, run_id, mission_id, revision, protocol_version, mode, surface,
		execution_profile, execution_profile_revision, workspace_trust, command_form,
		persistent_terminal, user_input_available, agent_input_default, network_scope,
		required_gate, policy_version, operator_confirmed, process_enabled,
		execution_authorized, capability_grant, requested_by, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ID, snapshot.RunID, snapshot.MissionID, snapshot.Revision,
		snapshot.ProtocolVersion, snapshot.Mode, snapshot.Surface,
		snapshot.ExecutionProfile, snapshot.ExecutionProfileRevision,
		snapshot.WorkspaceTrust, snapshot.CommandForm, snapshot.PersistentTerminal,
		snapshot.UserInputAvailable, snapshot.AgentInputDefault, snapshot.NetworkScope,
		snapshot.RequiredGate, snapshot.PolicyVersion, snapshot.OperatorConfirmed,
		snapshot.ProcessEnabled, snapshot.ExecutionAuthorized, snapshot.CapabilityGrant,
		snapshot.RequestedBy, snapshot.Reason, ts(snapshot.CreatedAt))
	return err
}

func validateRunExecutionInteractionMutation(
	snapshot domain.RunExecutionInteractionSnapshot,
	operation domain.RunExecutionInteractionOperation, event events.Event,
) error {
	if err := snapshot.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Run execution interaction snapshot is invalid", err)
	}
	if err := requireRedactedRunExecutionInteractionSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Revision <= 1 {
		return apperror.New(apperror.CodeInvalidArgument,
			"Run execution interaction transition revision must exceed one")
	}
	if err := operation.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Run execution interaction operation is invalid", err)
	}
	if operation.SnapshotID != snapshot.ID || operation.RunID != snapshot.RunID ||
		operation.RequestedBy != snapshot.RequestedBy ||
		!operation.CreatedAt.Equal(snapshot.CreatedAt) ||
		operation.RequestFingerprint != runExecutionInteractionRequestFingerprint(snapshot) {
		return apperror.New(apperror.CodeInvalidArgument,
			"Run execution interaction operation does not match its snapshot")
	}
	if err := validateRunExecutionInteractionChangedEvent(event, snapshot); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Run execution interaction event is invalid", err)
	}
	return nil
}

func requireRedactedRunExecutionInteractionSnapshot(
	snapshot domain.RunExecutionInteractionSnapshot,
) error {
	if redact.String(snapshot.RequestedBy) != snapshot.RequestedBy ||
		redact.String(snapshot.Reason) != snapshot.Reason {
		return apperror.New(apperror.CodeInvalidArgument,
			"Run execution interaction requester and reason must be redacted")
	}
	return nil
}

func validateRunExecutionInteractionChangedEvent(event events.Event,
	snapshot domain.RunExecutionInteractionSnapshot,
) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.Type != events.RunExecutionInteractionSelectedEvent ||
		event.Source != "run_execution_interaction" || event.RunID != snapshot.RunID ||
		event.MissionID != snapshot.MissionID || event.SubjectID != snapshot.ID ||
		!event.CreatedAt.Equal(snapshot.CreatedAt) {
		return errors.New(
			"run execution interaction event identity does not match its snapshot")
	}
	if err := rejectDuplicateJSONFields(event.PayloadJSON); err != nil {
		return err
	}
	var payload struct {
		Protocol                 string                             `json:"protocol"`
		Revision                 int64                              `json:"revision"`
		From                     domain.RunExecutionInteractionMode `json:"from"`
		To                       domain.RunExecutionInteractionMode `json:"to"`
		Surface                  domain.ExecutionSurface            `json:"surface"`
		ExecutionProfile         domain.RunExecutionProfile         `json:"execution_profile"`
		ExecutionProfileRevision int64                              `json:"execution_profile_revision"`
		WorkspaceTrust           domain.WorkspaceTrustLevel         `json:"workspace_trust"`
		CommandForm              domain.ExecutionCommandForm        `json:"command_form"`
		PersistentTerminal       bool                               `json:"persistent_terminal"`
		UserInputAvailable       bool                               `json:"user_input_available"`
		AgentInputDefault        *bool                              `json:"agent_input_default"`
		NetworkScope             domain.ExecutionNetworkScope       `json:"network_scope"`
		RequiredGate             domain.ExecutionInteractionGate    `json:"required_gate"`
		PolicyVersion            string                             `json:"policy_version"`
		OperatorConfirmed        bool                               `json:"operator_confirmed"`
		RequestedBy              string                             `json:"requested_by"`
		Reason                   string                             `json:"reason"`
		ProcessEnabled           *bool                              `json:"process_enabled"`
		ExecutionAuthorized      *bool                              `json:"execution_authorized"`
		CapabilityGrant          *bool                              `json:"capability_grant"`
	}
	decoder := json.NewDecoder(strings.NewReader(event.PayloadJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("run execution interaction event contains trailing data")
	}
	if payload.Protocol != snapshot.ProtocolVersion ||
		payload.Revision != snapshot.Revision || !payload.From.Valid() ||
		payload.From == snapshot.Mode || payload.To != snapshot.Mode ||
		payload.Surface != snapshot.Surface ||
		payload.ExecutionProfile != snapshot.ExecutionProfile ||
		payload.ExecutionProfileRevision != snapshot.ExecutionProfileRevision ||
		payload.WorkspaceTrust != snapshot.WorkspaceTrust ||
		payload.CommandForm != snapshot.CommandForm ||
		payload.PersistentTerminal != snapshot.PersistentTerminal ||
		payload.UserInputAvailable != snapshot.UserInputAvailable ||
		payload.AgentInputDefault == nil || *payload.AgentInputDefault ||
		payload.NetworkScope != snapshot.NetworkScope ||
		payload.RequiredGate != snapshot.RequiredGate ||
		payload.PolicyVersion != snapshot.PolicyVersion ||
		payload.OperatorConfirmed != snapshot.OperatorConfirmed ||
		payload.RequestedBy != snapshot.RequestedBy || payload.Reason != snapshot.Reason ||
		payload.ProcessEnabled == nil || *payload.ProcessEnabled ||
		payload.ExecutionAuthorized == nil || *payload.ExecutionAuthorized ||
		payload.CapabilityGrant == nil || *payload.CapabilityGrant {
		return errors.New(
			"run execution interaction event does not match its closed authority boundary")
	}
	return nil
}

func acquireRunExecutionInteractionWriteLockTx(ctx context.Context, tx *sql.Tx,
	runID string,
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
			"Run execution interaction Run was not found")
	}
	return nil
}

func (s *SQLiteStore) recoverRunExecutionInteractionTransition(ctx context.Context,
	operation domain.RunExecutionInteractionOperation, original error,
) (domain.RunExecutionInteractionSnapshot, bool, error) {
	existing, found, err := getRunExecutionInteractionOperation(ctx, s.db,
		operation.KeyDigest)
	if err != nil || !found {
		if err == nil {
			return domain.RunExecutionInteractionSnapshot{}, false, original
		}
		return domain.RunExecutionInteractionSnapshot{}, false,
			errors.Join(original, err)
	}
	if err := validateRunExecutionInteractionReplay(existing, operation); err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	stored, err := getRunExecutionInteractionSnapshot(ctx, s.db, existing.SnapshotID)
	if err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	if err := validateRunExecutionInteractionOperationBinding(existing, stored); err != nil {
		return domain.RunExecutionInteractionSnapshot{}, false, err
	}
	return stored, true, nil
}

func validateRunExecutionInteractionReplay(existing,
	request domain.RunExecutionInteractionOperation,
) error {
	if existing.KeyDigest != request.KeyDigest ||
		existing.RequestFingerprint != request.RequestFingerprint ||
		existing.RunID != request.RunID || existing.RequestedBy != request.RequestedBy {
		return apperror.New(apperror.CodeConflict,
			"Run execution interaction operation key was already used for different intent")
	}
	return nil
}

func validateRunExecutionInteractionOperationBinding(
	operation domain.RunExecutionInteractionOperation,
	snapshot domain.RunExecutionInteractionSnapshot,
) error {
	if operation.SnapshotID != snapshot.ID || operation.RunID != snapshot.RunID ||
		operation.RequestedBy != snapshot.RequestedBy ||
		!operation.CreatedAt.Equal(snapshot.CreatedAt) ||
		operation.RequestFingerprint != runExecutionInteractionRequestFingerprint(snapshot) {
		return apperror.New(apperror.CodeInternal,
			"stored Run execution interaction operation binding is invalid")
	}
	return nil
}

func runExecutionInteractionRequestFingerprint(
	snapshot domain.RunExecutionInteractionSnapshot,
) string {
	return runmutation.Fingerprint("run_execution_interaction_change_request.v1",
		snapshot.RunID, string(snapshot.Mode), string(snapshot.Surface),
		string(snapshot.ExecutionProfile),
		fmt.Sprintf("%d", snapshot.ExecutionProfileRevision),
		string(snapshot.WorkspaceTrust), fmt.Sprintf("%t", snapshot.OperatorConfirmed),
		snapshot.RequestedBy, snapshot.Reason)
}

func getRunExecutionInteractionSnapshot(ctx context.Context,
	queryer runExecutionInteractionQueryer, id string,
) (domain.RunExecutionInteractionSnapshot, error) {
	return scanRunExecutionInteractionSnapshot(queryer.QueryRowContext(ctx,
		runExecutionInteractionSnapshotSelect+` WHERE id = ?`, id))
}

func getCurrentRunExecutionInteractionSnapshot(ctx context.Context,
	queryer runExecutionInteractionQueryer, runID string,
) (domain.RunExecutionInteractionSnapshot, error) {
	return scanRunExecutionInteractionSnapshot(queryer.QueryRowContext(ctx,
		runExecutionInteractionSnapshotSelect+
			` WHERE run_id = ? ORDER BY revision DESC LIMIT 1`, runID))
}

func scanRunExecutionInteractionSnapshot(scanner interface{ Scan(...any) error }) (
	domain.RunExecutionInteractionSnapshot, error,
) {
	var snapshot domain.RunExecutionInteractionSnapshot
	var createdAt string
	if err := scanner.Scan(&snapshot.ID, &snapshot.RunID, &snapshot.MissionID,
		&snapshot.Revision, &snapshot.ProtocolVersion, &snapshot.Mode,
		&snapshot.Surface, &snapshot.ExecutionProfile,
		&snapshot.ExecutionProfileRevision, &snapshot.WorkspaceTrust,
		&snapshot.CommandForm, &snapshot.PersistentTerminal,
		&snapshot.UserInputAvailable, &snapshot.AgentInputDefault,
		&snapshot.NetworkScope, &snapshot.RequiredGate, &snapshot.PolicyVersion,
		&snapshot.OperatorConfirmed, &snapshot.ProcessEnabled,
		&snapshot.ExecutionAuthorized, &snapshot.CapabilityGrant,
		&snapshot.RequestedBy, &snapshot.Reason, &createdAt); err != nil {
		return domain.RunExecutionInteractionSnapshot{}, err
	}
	snapshot.CreatedAt = parseTS(createdAt)
	return snapshot, snapshot.Validate()
}

func getRunExecutionInteractionOperation(ctx context.Context,
	queryer runExecutionInteractionQueryer, keyDigest string,
) (domain.RunExecutionInteractionOperation, bool, error) {
	var operation domain.RunExecutionInteractionOperation
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT operation_key_digest,
		request_fingerprint, snapshot_id, run_id, requested_by, created_at
		FROM run_execution_interaction_operations WHERE operation_key_digest = ?`,
		keyDigest).Scan(&operation.KeyDigest, &operation.RequestFingerprint,
		&operation.SnapshotID, &operation.RunID, &operation.RequestedBy, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RunExecutionInteractionOperation{}, false, nil
	}
	if err != nil {
		return domain.RunExecutionInteractionOperation{}, false, err
	}
	operation.CreatedAt = parseTS(createdAt)
	return operation, true, operation.Validate()
}
