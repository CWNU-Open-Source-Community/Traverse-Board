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

const runExecutionPermissionSnapshotSelect = `SELECT id, run_id, mission_id, revision,
	protocol_version, mode, approval_policy, command_scope, filesystem_scope,
	network_scope, persistent_terminal, background_process, agent_terminal_input,
	risk_tier, required_gate, policy_version, operator_confirmed, process_enabled,
	execution_authorized, capability_grant, requested_by, reason, created_at
	FROM run_execution_permission_snapshots`

type runExecutionPermissionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLiteStore) GetRunExecutionPermission(ctx context.Context,
	runID string,
) (domain.RunExecutionPermissionSnapshot, error) {
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || strings.ContainsRune(runID, 0) {
		return domain.RunExecutionPermissionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument, "Run execution permission Run id is invalid")
	}
	return getCurrentRunExecutionPermissionSnapshot(ctx, s.db, runID)
}

func (s *SQLiteStore) GetRunExecutionPermissionSnapshot(ctx context.Context,
	id string,
) (domain.RunExecutionPermissionSnapshot, error) {
	id = strings.TrimSpace(id)
	if !domain.ValidAgentID(id) || strings.ContainsRune(id, 0) {
		return domain.RunExecutionPermissionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument, "Run execution permission snapshot id is invalid")
	}
	return getRunExecutionPermissionSnapshot(ctx, s.db, id)
}

func (s *SQLiteStore) GetRunExecutionPermissionOperation(ctx context.Context,
	keyDigest string,
) (domain.RunExecutionPermissionOperation, bool, error) {
	keyDigest = strings.TrimSpace(keyDigest)
	if !validStoreDigest(keyDigest) {
		return domain.RunExecutionPermissionOperation{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Run execution permission operation digest is invalid")
	}
	return getRunExecutionPermissionOperation(ctx, s.db, keyDigest)
}

func (s *SQLiteStore) TransitionRunExecutionPermission(ctx context.Context,
	snapshot domain.RunExecutionPermissionSnapshot,
	operation domain.RunExecutionPermissionOperation, event events.Event,
) (domain.RunExecutionPermissionSnapshot, bool, error) {
	if err := validateRunExecutionPermissionMutation(snapshot, operation, event); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunExecutionPermissionWriteLockTx(ctx, tx, snapshot.RunID); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	if existing, found, err := getRunExecutionPermissionOperation(
		ctx, tx, operation.KeyDigest); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	} else if found {
		if err := validateRunExecutionPermissionReplay(existing, operation); err != nil {
			return domain.RunExecutionPermissionSnapshot{}, false, err
		}
		stored, err := getRunExecutionPermissionSnapshot(ctx, tx, existing.SnapshotID)
		if err != nil {
			return domain.RunExecutionPermissionSnapshot{}, false, err
		}
		if err := validateRunExecutionPermissionOperationBinding(existing, stored); err != nil {
			return domain.RunExecutionPermissionSnapshot{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return domain.RunExecutionPermissionSnapshot{}, false, err
		}
		return stored, true, nil
	}
	current, err := getCurrentRunExecutionPermissionSnapshot(ctx, tx, snapshot.RunID)
	if err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	run, mission, err := getCoordinatorRunTx(ctx, tx, snapshot.RunID)
	if err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	if !domain.CanChangeRunExecutionPermission(run.Status) {
		return domain.RunExecutionPermissionSnapshot{}, false, apperror.New(
			apperror.CodeFailedPrecondition,
			fmt.Sprintf(
				"Run execution permission can only change while created or paused; Run is %s",
				run.Status))
	}
	if snapshot.MissionID != run.MissionID || snapshot.MissionID != mission.ID ||
		snapshot.Revision != current.Revision+1 || snapshot.Mode == current.Mode ||
		snapshot.ProtocolVersion != current.ProtocolVersion ||
		snapshot.PolicyVersion != current.PolicyVersion ||
		snapshot.CreatedAt.Before(current.CreatedAt) {
		return domain.RunExecutionPermissionSnapshot{}, false, apperror.New(
			apperror.CodeConflict,
			"Run execution permission changed concurrently or attempted to change immutable policy")
	}
	lease, found, err := getRunExecutionLeaseTx(ctx, tx, run.ID)
	if err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	if found && lease.ActiveAt(snapshot.CreatedAt) {
		result, err := tx.ExecContext(ctx, `UPDATE run_execution_leases
			SET status = ?, released_at = ?
			WHERE run_id = ? AND lease_id = ? AND owner_id = ? AND generation = ?
				AND status = ?`, domain.RunExecutionLeaseReleased, ts(snapshot.CreatedAt),
			lease.RunID, lease.LeaseID, lease.OwnerID, lease.Generation,
			domain.RunExecutionLeaseActive)
		if err != nil {
			return domain.RunExecutionPermissionSnapshot{}, false, err
		}
		if err := requireSingleLeaseUpdate(result,
			"Run execution lease changed before permission revision revocation"); err != nil {
			return domain.RunExecutionPermissionSnapshot{}, false, err
		}
		if err := appendSupervisorEventTx(ctx, tx, run,
			events.RunExecutionLeaseReleasedEvent, "run_execution_permission", run.ID,
			map[string]any{"owner_id": lease.OwnerID, "generation": lease.Generation,
				"released_at":              snapshot.CreatedAt,
				"reason":                   "execution_permission_revision_changed",
				"next_permission_revision": snapshot.Revision}); err != nil {
			return domain.RunExecutionPermissionSnapshot{}, false, err
		}
	}
	if err := insertRunExecutionPermissionSnapshotTx(ctx, tx, snapshot); err != nil {
		_ = tx.Rollback()
		return s.recoverRunExecutionPermissionTransition(ctx, operation, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_execution_permission_operations
		(operation_key_digest, request_fingerprint, snapshot_id, run_id,
		requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`, operation.KeyDigest,
		operation.RequestFingerprint, operation.SnapshotID, operation.RunID,
		operation.RequestedBy, ts(operation.CreatedAt)); err != nil {
		_ = tx.Rollback()
		return s.recoverRunExecutionPermissionTransition(ctx, operation, err)
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	return snapshot, false, nil
}

func insertInitialRunExecutionPermissionSnapshotTx(ctx context.Context, tx *sql.Tx,
	snapshot domain.RunExecutionPermissionSnapshot, run domain.Run, mission domain.Mission,
) error {
	if err := snapshot.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"initial Run execution permission is invalid", err)
	}
	if err := requireRedactedRunExecutionPermissionSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Revision != 1 || snapshot.RunID != run.ID ||
		snapshot.MissionID != run.MissionID || snapshot.MissionID != mission.ID ||
		snapshot.Mode != domain.RunExecutionPermissionConservative ||
		run.Status != domain.RunCreated || snapshot.CreatedAt.Before(run.CreatedAt) {
		return apperror.New(apperror.CodeInvalidArgument,
			"initial Run execution permission does not match its created Run and Mission")
	}
	return insertRunExecutionPermissionSnapshotTx(ctx, tx, snapshot)
}

func insertRunExecutionPermissionSnapshotTx(ctx context.Context, tx *sql.Tx,
	snapshot domain.RunExecutionPermissionSnapshot,
) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO run_execution_permission_snapshots
		(id, run_id, mission_id, revision, protocol_version, mode, approval_policy,
		command_scope, filesystem_scope, network_scope, persistent_terminal,
		background_process, agent_terminal_input, risk_tier, required_gate,
		policy_version, operator_confirmed, process_enabled, execution_authorized,
		capability_grant, requested_by, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ID, snapshot.RunID, snapshot.MissionID, snapshot.Revision,
		snapshot.ProtocolVersion, snapshot.Mode, snapshot.ApprovalPolicy,
		snapshot.CommandScope, snapshot.FilesystemScope, snapshot.NetworkScope,
		snapshot.PersistentTerminal, snapshot.BackgroundProcess, snapshot.AgentTerminalInput,
		snapshot.RiskTier, snapshot.RequiredGate, snapshot.PolicyVersion,
		snapshot.OperatorConfirmed, snapshot.ProcessEnabled, snapshot.ExecutionAuthorized,
		snapshot.CapabilityGrant, snapshot.RequestedBy, snapshot.Reason,
		ts(snapshot.CreatedAt))
	return err
}

func validateRunExecutionPermissionMutation(
	snapshot domain.RunExecutionPermissionSnapshot,
	operation domain.RunExecutionPermissionOperation, event events.Event,
) error {
	if err := snapshot.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Run execution permission snapshot is invalid", err)
	}
	if err := requireRedactedRunExecutionPermissionSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Revision <= 1 {
		return apperror.New(apperror.CodeInvalidArgument,
			"Run execution permission transition revision must exceed one")
	}
	if err := operation.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Run execution permission operation is invalid", err)
	}
	if operation.SnapshotID != snapshot.ID || operation.RunID != snapshot.RunID ||
		operation.RequestedBy != snapshot.RequestedBy ||
		!operation.CreatedAt.Equal(snapshot.CreatedAt) ||
		operation.RequestFingerprint != runExecutionPermissionRequestFingerprint(snapshot) {
		return apperror.New(apperror.CodeInvalidArgument,
			"Run execution permission operation does not match its snapshot")
	}
	if err := validateRunExecutionPermissionSelectedEvent(event, snapshot); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Run execution permission event is invalid", err)
	}
	return nil
}

func requireRedactedRunExecutionPermissionSnapshot(
	snapshot domain.RunExecutionPermissionSnapshot,
) error {
	if redact.String(snapshot.RequestedBy) != snapshot.RequestedBy ||
		redact.String(snapshot.Reason) != snapshot.Reason {
		return apperror.New(apperror.CodeInvalidArgument,
			"Run execution permission requester and reason must be redacted before persistence")
	}
	return nil
}

func validateRunExecutionPermissionSelectedEvent(event events.Event,
	snapshot domain.RunExecutionPermissionSnapshot,
) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.Type != events.RunExecutionPermissionSelectedEvent ||
		event.Source != "run_execution_permission" || event.RunID != snapshot.RunID ||
		event.MissionID != snapshot.MissionID || event.SubjectID != snapshot.ID ||
		!event.CreatedAt.Equal(snapshot.CreatedAt) {
		return errors.New("run execution permission event identity does not match its snapshot")
	}
	if err := rejectDuplicateJSONFields(event.PayloadJSON); err != nil {
		return err
	}
	var payload struct {
		Protocol            string                                    `json:"protocol"`
		Revision            int64                                     `json:"revision"`
		From                domain.RunExecutionPermissionMode         `json:"from"`
		To                  domain.RunExecutionPermissionMode         `json:"to"`
		ApprovalPolicy      domain.ExecutionPermissionApprovalPolicy  `json:"approval_policy"`
		CommandScope        domain.ExecutionPermissionCommandScope    `json:"command_scope"`
		FilesystemScope     domain.ExecutionPermissionFilesystemScope `json:"filesystem_scope"`
		NetworkScope        domain.ExecutionPermissionNetworkScope    `json:"network_scope"`
		PersistentTerminal  bool                                      `json:"persistent_terminal"`
		BackgroundProcess   bool                                      `json:"background_process"`
		AgentTerminalInput  bool                                      `json:"agent_terminal_input"`
		RiskTier            domain.ExecutionRiskTier                  `json:"risk_tier"`
		RequiredGate        domain.ExecutionPermissionGate            `json:"required_gate"`
		PolicyVersion       string                                    `json:"policy_version"`
		RequestedBy         string                                    `json:"requested_by"`
		Reason              string                                    `json:"reason"`
		ProcessEnabled      *bool                                     `json:"process_enabled"`
		ExecutionAuthorized *bool                                     `json:"execution_authorized"`
		CapabilityGrant     *bool                                     `json:"capability_grant"`
		CapabilityMatrix    struct {
			WorkspaceRead           bool                                       `json:"workspace_read"`
			WorkspaceWrite          bool                                       `json:"workspace_write"`
			SandboxedCommandRuntime bool                                       `json:"sandboxed_command_runtime"`
			UnsandboxedHostProcess  bool                                       `json:"unsandboxed_host_process"`
			NetworkAccess           bool                                       `json:"network_access"`
			CredentialAccess        bool                                       `json:"credential_access"`
			UserHomeAccess          bool                                       `json:"user_home_access"`
			PersistentUserTerminal  bool                                       `json:"persistent_user_terminal"`
			PersistentAgentTerminal bool                                       `json:"persistent_agent_terminal"`
			FullCDP                 bool                                       `json:"full_cdp"`
			OutOfScopePolicy        domain.ExecutionPermissionOutOfScopePolicy `json:"out_of_scope_policy"`
		} `json:"capability_matrix"`
	}
	decoder := json.NewDecoder(strings.NewReader(event.PayloadJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("run execution permission event contains trailing data")
	}
	matrix, err := snapshot.CapabilityMatrix()
	if err != nil {
		return err
	}
	if payload.Protocol != snapshot.ProtocolVersion || payload.Revision != snapshot.Revision ||
		!payload.From.Valid() || payload.From == snapshot.Mode || payload.To != snapshot.Mode ||
		payload.ApprovalPolicy != snapshot.ApprovalPolicy ||
		payload.CommandScope != snapshot.CommandScope ||
		payload.FilesystemScope != snapshot.FilesystemScope ||
		payload.NetworkScope != snapshot.NetworkScope ||
		payload.PersistentTerminal != snapshot.PersistentTerminal ||
		payload.BackgroundProcess != snapshot.BackgroundProcess ||
		payload.AgentTerminalInput != snapshot.AgentTerminalInput ||
		payload.RiskTier != snapshot.RiskTier || payload.RequiredGate != snapshot.RequiredGate ||
		payload.PolicyVersion != snapshot.PolicyVersion ||
		payload.RequestedBy != snapshot.RequestedBy || payload.Reason != snapshot.Reason ||
		payload.ProcessEnabled == nil || *payload.ProcessEnabled ||
		payload.ExecutionAuthorized == nil || *payload.ExecutionAuthorized ||
		payload.CapabilityGrant == nil || *payload.CapabilityGrant ||
		payload.CapabilityMatrix.WorkspaceRead != matrix.WorkspaceRead ||
		payload.CapabilityMatrix.WorkspaceWrite != matrix.WorkspaceWrite ||
		payload.CapabilityMatrix.SandboxedCommandRuntime != matrix.SandboxedCommandRuntime ||
		payload.CapabilityMatrix.UnsandboxedHostProcess != matrix.UnsandboxedHostProcess ||
		payload.CapabilityMatrix.NetworkAccess != matrix.NetworkAccess ||
		payload.CapabilityMatrix.CredentialAccess != matrix.CredentialAccess ||
		payload.CapabilityMatrix.UserHomeAccess != matrix.UserHomeAccess ||
		payload.CapabilityMatrix.PersistentUserTerminal != matrix.PersistentUserTerminal ||
		payload.CapabilityMatrix.PersistentAgentTerminal != matrix.PersistentAgentTerminal ||
		payload.CapabilityMatrix.FullCDP != matrix.FullCDP ||
		payload.CapabilityMatrix.OutOfScopePolicy != matrix.OutOfScopePolicy {
		return errors.New(
			"run execution permission event does not match its closed capability boundary")
	}
	return nil
}

func acquireRunExecutionPermissionWriteLockTx(
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
			"Run execution permission Run was not found")
	}
	return nil
}

func (s *SQLiteStore) recoverRunExecutionPermissionTransition(
	ctx context.Context, operation domain.RunExecutionPermissionOperation, original error,
) (domain.RunExecutionPermissionSnapshot, bool, error) {
	existing, found, err := getRunExecutionPermissionOperation(
		ctx, s.db, operation.KeyDigest)
	if err != nil || !found {
		if err == nil {
			return domain.RunExecutionPermissionSnapshot{}, false, original
		}
		return domain.RunExecutionPermissionSnapshot{}, false, errors.Join(original, err)
	}
	if err := validateRunExecutionPermissionReplay(existing, operation); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	stored, err := getRunExecutionPermissionSnapshot(ctx, s.db, existing.SnapshotID)
	if err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	if err := validateRunExecutionPermissionOperationBinding(existing, stored); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, false, err
	}
	return stored, true, nil
}

func validateRunExecutionPermissionReplay(existing,
	request domain.RunExecutionPermissionOperation,
) error {
	if existing.KeyDigest != request.KeyDigest ||
		existing.RequestFingerprint != request.RequestFingerprint ||
		existing.RunID != request.RunID || existing.RequestedBy != request.RequestedBy {
		return apperror.New(apperror.CodeConflict,
			"Run execution permission operation key was already used for different intent")
	}
	return nil
}

func validateRunExecutionPermissionOperationBinding(
	operation domain.RunExecutionPermissionOperation,
	snapshot domain.RunExecutionPermissionSnapshot,
) error {
	if operation.SnapshotID != snapshot.ID || operation.RunID != snapshot.RunID ||
		operation.RequestedBy != snapshot.RequestedBy ||
		!operation.CreatedAt.Equal(snapshot.CreatedAt) ||
		operation.RequestFingerprint != runExecutionPermissionRequestFingerprint(snapshot) {
		return apperror.New(apperror.CodeInternal,
			"stored Run execution permission operation binding is invalid")
	}
	return nil
}

func runExecutionPermissionRequestFingerprint(
	snapshot domain.RunExecutionPermissionSnapshot,
) string {
	return runmutation.Fingerprint("run_execution_permission_change_request.v1",
		snapshot.RunID, string(snapshot.Mode), fmt.Sprintf("%t", snapshot.OperatorConfirmed),
		snapshot.RequestedBy, snapshot.Reason)
}

func getRunExecutionPermissionSnapshot(ctx context.Context,
	queryer runExecutionPermissionQueryer, id string,
) (domain.RunExecutionPermissionSnapshot, error) {
	return scanRunExecutionPermissionSnapshot(queryer.QueryRowContext(ctx,
		runExecutionPermissionSnapshotSelect+` WHERE id = ?`, id))
}

func getCurrentRunExecutionPermissionSnapshot(ctx context.Context,
	queryer runExecutionPermissionQueryer, runID string,
) (domain.RunExecutionPermissionSnapshot, error) {
	return scanRunExecutionPermissionSnapshot(queryer.QueryRowContext(ctx,
		runExecutionPermissionSnapshotSelect+
			` WHERE run_id = ? ORDER BY revision DESC LIMIT 1`, runID))
}

func scanRunExecutionPermissionSnapshot(scanner interface{ Scan(...any) error }) (
	domain.RunExecutionPermissionSnapshot, error,
) {
	var snapshot domain.RunExecutionPermissionSnapshot
	var createdAt string
	if err := scanner.Scan(&snapshot.ID, &snapshot.RunID, &snapshot.MissionID,
		&snapshot.Revision, &snapshot.ProtocolVersion, &snapshot.Mode,
		&snapshot.ApprovalPolicy, &snapshot.CommandScope, &snapshot.FilesystemScope,
		&snapshot.NetworkScope, &snapshot.PersistentTerminal, &snapshot.BackgroundProcess,
		&snapshot.AgentTerminalInput, &snapshot.RiskTier, &snapshot.RequiredGate,
		&snapshot.PolicyVersion, &snapshot.OperatorConfirmed, &snapshot.ProcessEnabled,
		&snapshot.ExecutionAuthorized, &snapshot.CapabilityGrant, &snapshot.RequestedBy,
		&snapshot.Reason, &createdAt); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, err
	}
	snapshot.CreatedAt = parseTS(createdAt)
	return snapshot, snapshot.Validate()
}

func getRunExecutionPermissionOperation(ctx context.Context,
	queryer runExecutionPermissionQueryer, keyDigest string,
) (domain.RunExecutionPermissionOperation, bool, error) {
	var operation domain.RunExecutionPermissionOperation
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT operation_key_digest,
		request_fingerprint, snapshot_id, run_id, requested_by, created_at
		FROM run_execution_permission_operations WHERE operation_key_digest = ?`, keyDigest).
		Scan(&operation.KeyDigest, &operation.RequestFingerprint, &operation.SnapshotID,
			&operation.RunID, &operation.RequestedBy, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RunExecutionPermissionOperation{}, false, nil
	}
	if err != nil {
		return domain.RunExecutionPermissionOperation{}, false, err
	}
	operation.CreatedAt = parseTS(createdAt)
	return operation, true, operation.Validate()
}
