package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/session"
)

type standardCodePresetQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const standardCodePresetOperationSelect = `SELECT protocol_version,
	operation_key_digest, request_fingerprint, requested_run_id, run_id, mission_id,
	workspace_id, action, backend_intent, selected_backend, selection_reason, status,
	drydock_id, drydock_generation, drydock_checkpoint_id, mode_snapshot_id,
	profile_snapshot_id, interaction_snapshot_id, permission_snapshot_id,
	browser_cdp_snapshot_id, event_sequence_start, event_sequence_end, requested_by,
	capability_grant, created_at, updated_at FROM standard_code_preset_operations`

func (s *SQLiteStore) GetStandardCodePresetOperation(ctx context.Context,
	keyDigest string,
) (domain.StandardCodePresetOperation, bool, error) {
	keyDigest = strings.TrimSpace(keyDigest)
	if !validStoreDigest(keyDigest) {
		return domain.StandardCodePresetOperation{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Standard Code preset operation digest is invalid")
	}
	return getStandardCodePresetOperation(ctx, s.db, keyDigest)
}

func (s *SQLiteStore) GetConfiguredStandardCodePresetOperation(ctx context.Context,
	runID string,
) (domain.StandardCodePresetOperation, bool, error) {
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) {
		return domain.StandardCodePresetOperation{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Standard Code preset Run id is invalid")
	}
	operation, err := scanStandardCodePresetOperation(s.db.QueryRowContext(ctx,
		standardCodePresetOperationSelect+` WHERE run_id = ? AND status = 'configured'
			ORDER BY event_sequence_end DESC LIMIT 1`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.StandardCodePresetOperation{}, false, nil
	}
	if err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	return operation, true, nil
}

func (s *SQLiteStore) BeginStandardCodePreset(ctx context.Context,
	operation domain.StandardCodePresetOperation,
) (domain.StandardCodePresetOperation, bool, error) {
	if err := validatePendingStandardCodePreset(operation); err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, getErr := getStandardCodePresetOperation(ctx, tx,
		operation.KeyDigest); getErr != nil {
		return domain.StandardCodePresetOperation{}, false, getErr
	} else if found {
		if err := sameStandardCodePresetIntent(existing, operation); err != nil {
			return domain.StandardCodePresetOperation{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return domain.StandardCodePresetOperation{}, false, err
		}
		return existing, true, nil
	}
	if err := lockRunControlTx(ctx, tx, operation.RunID); err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	run, err := getRunControlRunTx(ctx, tx, operation.RunID)
	if err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	if err := validateStandardCodeIntentRun(run, operation); err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	if operation.Status == domain.StandardCodePresetPreparing {
		if err := requireNoActiveRunControlLeaseTx(ctx, tx, run.ID,
			operation.CreatedAt); err != nil {
			return domain.StandardCodePresetOperation{}, false, err
		}
	}
	operation, err = insertStandardCodePresetIntentTx(ctx, tx, operation)
	if err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	return operation, false, nil
}

func (s *SQLiteStore) CreateMissionRunWithStandardCodePresetIntent(ctx context.Context,
	mission domain.Mission, run domain.Run, mode domain.RunModeSnapshot,
	linkedSession session.Session, initialEvents []events.Event,
	operation domain.StandardCodePresetOperation,
) (domain.StandardCodePresetOperation, bool, error) {
	if err := validatePendingStandardCodePreset(operation); err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	if operation.Status != domain.StandardCodePresetPreparing ||
		operation.RunID != run.ID || operation.MissionID != mission.ID ||
		operation.WorkspaceID != mission.WorkspaceID ||
		mode.Surface != domain.ExecutionSurfaceCode ||
		mode.Phase != domain.ExecutionPhasePlan || run.Status != domain.RunCreated ||
		mission.Profile != domain.ProfileCode || !run.Config.Interactive ||
		mission.Scope.NetworkMode != "disabled" || linkedSession.WorkspaceID != mission.WorkspaceID {
		return domain.StandardCodePresetOperation{}, false, apperror.New(
			apperror.CodeInvalidArgument, "new Standard Code Run graph is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, getErr := getStandardCodePresetOperation(ctx, tx,
		operation.KeyDigest); getErr != nil {
		return domain.StandardCodePresetOperation{}, false, getErr
	} else if found {
		// A concurrent creator prepares fresh Mission and Run identifiers before
		// it can observe the winning operation. Those generated identities are
		// not part of the caller's intent. Rebind the losing probe to the durable
		// winner before comparing the externally fingerprinted request.
		probe := operation
		probe.RunID, probe.MissionID = existing.RunID, existing.MissionID
		if err := sameStandardCodePresetIntent(existing, probe); err != nil {
			return domain.StandardCodePresetOperation{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return domain.StandardCodePresetOperation{}, false, err
		}
		return existing, true, nil
	}
	if err := createMissionRunTx(ctx, tx, mission, run, mode, linkedSession, true,
		initialEvents); err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	operation, err = insertStandardCodePresetIntentTx(ctx, tx, operation)
	if err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	return operation, false, nil
}

func (s *SQLiteStore) CommitStandardCodePreset(ctx context.Context,
	commit domain.StandardCodePresetCommit,
) (domain.StandardCodePresetOperation, domain.Run, bool, error) {
	if commit.Operation.KeyDigest == "" || commit.CommittedAt.IsZero() ||
		commit.DrydockID == "" || commit.DrydockGeneration <= 0 ||
		commit.DrydockCheckpointID == "" {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false,
			apperror.New(apperror.CodeInvalidArgument,
				"Standard Code preset commit binding is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, found, err := getStandardCodePresetOperation(ctx, tx,
		commit.Operation.KeyDigest)
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	if !found {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false,
			apperror.New(apperror.CodeFailedPrecondition,
				"Standard Code preset intent was not prepared")
	}
	if err := sameStandardCodePresetIntent(stored, commit.Operation); err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	if stored.Status == domain.StandardCodePresetConfigured {
		run, getErr := getRunControlRunTx(ctx, tx, stored.RunID)
		if getErr != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, getErr
		}
		if err := tx.Commit(); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
		return stored, run, true, nil
	}
	if err := lockRunControlTx(ctx, tx, stored.RunID); err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	run, err := getRunControlRunTx(ctx, tx, stored.RunID)
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	if stored.Status == domain.StandardCodePresetWaitingForPause {
		if run.Status != domain.RunRunning && run.Status != domain.RunPaused {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false,
				apperror.New(apperror.CodeFailedPrecondition,
					"pause-and-configure expected a running or paused Run")
		}
		if err := requireNoActiveRunControlLeaseTx(ctx, tx, run.ID,
			commit.CommittedAt); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
		if run.Status == domain.RunRunning {
			if err := requireQuiescentRunPauseTx(ctx, tx, run.ID); err != nil {
				return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
			}
			run, _, err = transitionControlledRunTx(ctx, tx, run, domain.RunPaused,
				domain.RunLifecyclePause, commit.CommittedAt)
			if err != nil {
				return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
			}
		}
	} else if !domain.CanChangeRunExecutionProfile(run.Status) {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false,
			apperror.New(apperror.CodeFailedPrecondition,
				"Standard Code preset requires a created or paused Run")
	}
	if err := requireNoActiveRunControlLeaseTx(ctx, tx, run.ID,
		commit.CommittedAt); err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	if err := requireStandardCodeDrydockTx(ctx, tx, stored, commit); err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}

	mode, err := getCurrentRunModeSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	permission, err := getCurrentRunExecutionPermissionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	cdp, err := getCurrentRunBrowserCDPPermissionSnapshot(ctx, tx, run.ID)
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}

	if err := validateStandardCodeSnapshotTuple(stored, mode, profile, interaction,
		permission, cdp, commit); err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	if commit.Mode.ID != mode.ID {
		if err := insertRunModeSnapshotTx(ctx, tx, commit.Mode); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
		if err := appendStandardCodeSnapshotEventTx(ctx, tx, commit.Mode.RunID,
			commit.Mode.MissionID, events.RunPhaseChangedEvent, commit.Mode.ID,
			commit.CommittedAt, map[string]any{"from": mode.Phase, "to": commit.Mode.Phase,
				"revision": commit.Mode.Revision, "source": "standard_code_preset"}); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
	}
	if commit.Profile.ID != profile.ID {
		if err := insertRunExecutionProfileSnapshotTx(ctx, tx, commit.Profile); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
		if err := appendStandardCodeSnapshotEventTx(ctx, tx, commit.Profile.RunID,
			commit.Profile.MissionID, events.RunExecutionProfileSelectedEvent,
			commit.Profile.ID, commit.CommittedAt, map[string]any{"from": profile.Profile,
				"to": commit.Profile.Profile, "revision": commit.Profile.Revision,
				"source": "standard_code_preset", "capability_grant": false}); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
	}
	if commit.Interaction.ID != interaction.ID {
		if err := insertRunExecutionInteractionSnapshotTx(ctx, tx,
			commit.Interaction); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
		if err := appendStandardCodeSnapshotEventTx(ctx, tx, commit.Interaction.RunID,
			commit.Interaction.MissionID, events.RunExecutionInteractionSelectedEvent,
			commit.Interaction.ID, commit.CommittedAt, map[string]any{"from": interaction.Mode,
				"to": commit.Interaction.Mode, "revision": commit.Interaction.Revision,
				"source": "standard_code_preset", "capability_grant": false}); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
	}
	if commit.Permission.ID != permission.ID {
		if err := insertRunExecutionPermissionSnapshotTx(ctx, tx,
			commit.Permission); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
		if err := appendStandardCodeSnapshotEventTx(ctx, tx, commit.Permission.RunID,
			commit.Permission.MissionID, events.RunExecutionPermissionSelectedEvent,
			commit.Permission.ID, commit.CommittedAt, map[string]any{"from": permission.Mode,
				"to": commit.Permission.Mode, "revision": commit.Permission.Revision,
				"source": "standard_code_preset", "capability_grant": false}); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
	}
	if commit.BrowserCDP.ID != cdp.ID {
		if err := insertRunBrowserCDPPermissionSnapshotTx(ctx, tx,
			commit.BrowserCDP); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
		if err := appendStandardCodeSnapshotEventTx(ctx, tx, commit.BrowserCDP.RunID,
			commit.BrowserCDP.MissionID, events.RunBrowserCDPPermissionSelectedEvent,
			commit.BrowserCDP.ID, commit.CommittedAt, map[string]any{"from": cdp.Mode,
				"to": commit.BrowserCDP.Mode, "revision": commit.BrowserCDP.Revision,
				"source": "standard_code_preset", "capability_grant": false}); err != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
		}
	}
	if err := syncControlledRunRootTx(ctx, tx, run); err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	finalEvent, err := events.New(run.ID, run.MissionID,
		events.StandardCodePresetConfiguredEvent, "standard_code_preset", run.ID,
		map[string]any{"action": stored.Action, "backend_intent": stored.BackendIntent,
			"selected_backend": stored.SelectedBackend,
			"selection_reason": stored.SelectionReason, "surface": "code",
			"phase": "plan", "interaction": "controlled",
			"permission": "workspace_access", "network": "disabled",
			"credentials": "none", "capability_grant": false})
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	finalEvent.CreatedAt = commit.CommittedAt.UTC()
	inserted, err := insertRunEventTx(ctx, tx, finalEvent)
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	stored.Status = domain.StandardCodePresetConfigured
	stored.DrydockID = commit.DrydockID
	stored.DrydockGeneration = commit.DrydockGeneration
	stored.DrydockCheckpointID = commit.DrydockCheckpointID
	stored.ModeSnapshotID = commit.Mode.ID
	stored.ProfileSnapshotID = commit.Profile.ID
	stored.InteractionSnapshotID = commit.Interaction.ID
	stored.PermissionSnapshotID = commit.Permission.ID
	stored.BrowserCDPSnapshotID = commit.BrowserCDP.ID
	stored.EventSequenceEnd = inserted.Sequence
	stored.UpdatedAt = commit.CommittedAt.UTC()
	if err := stored.Validate(); err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE standard_code_preset_operations SET
		status = ?, drydock_id = ?, drydock_generation = ?, drydock_checkpoint_id = ?,
		mode_snapshot_id = ?, profile_snapshot_id = ?, interaction_snapshot_id = ?,
		permission_snapshot_id = ?, browser_cdp_snapshot_id = ?, event_sequence_end = ?,
		updated_at = ? WHERE operation_key_digest = ? AND status IN ('preparing','waiting_for_pause')`,
		stored.Status, stored.DrydockID, stored.DrydockGeneration,
		stored.DrydockCheckpointID, stored.ModeSnapshotID, stored.ProfileSnapshotID,
		stored.InteractionSnapshotID, stored.PermissionSnapshotID,
		stored.BrowserCDPSnapshotID, stored.EventSequenceEnd, ts(stored.UpdatedAt),
		stored.KeyDigest)
	if err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return domain.StandardCodePresetOperation{}, domain.Run{}, false, rowsErr
		}
		return domain.StandardCodePresetOperation{}, domain.Run{}, false,
			apperror.New(apperror.CodeConflict,
				"Standard Code preset operation changed concurrently")
	}
	if err := tx.Commit(); err != nil {
		return domain.StandardCodePresetOperation{}, domain.Run{}, false, err
	}
	return stored, run, false, nil
}

func validatePendingStandardCodePreset(operation domain.StandardCodePresetOperation) error {
	if operation.Status != domain.StandardCodePresetPreparing &&
		operation.Status != domain.StandardCodePresetWaitingForPause {
		return apperror.New(apperror.CodeInvalidArgument,
			"Standard Code preset intent must be pending")
	}
	if operation.EventSequenceStart != 0 || operation.EventSequenceEnd != 0 {
		return apperror.New(apperror.CodeInvalidArgument,
			"Standard Code preset intent cannot choose event sequences")
	}
	probe := operation
	probe.EventSequenceStart, probe.EventSequenceEnd = 1, 1
	if err := probe.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"Standard Code preset intent is invalid", err)
	}
	return nil
}

func validateStandardCodeIntentRun(run domain.Run,
	operation domain.StandardCodePresetOperation,
) error {
	if run.ID != operation.RunID || run.MissionID != operation.MissionID {
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset Run binding changed")
	}
	if operation.Status == domain.StandardCodePresetPreparing &&
		!domain.CanChangeRunExecutionProfile(run.Status) {
		return apperror.New(apperror.CodeFailedPrecondition,
			"Standard Code preset requires a created or paused Run")
	}
	if operation.Status == domain.StandardCodePresetWaitingForPause &&
		run.Status != domain.RunRunning {
		return apperror.New(apperror.CodeFailedPrecondition,
			"pause-and-configure requires a running Run")
	}
	return nil
}

func insertStandardCodePresetIntentTx(ctx context.Context, tx *sql.Tx,
	operation domain.StandardCodePresetOperation,
) (domain.StandardCodePresetOperation, error) {
	event, err := events.New(operation.RunID, operation.MissionID,
		events.StandardCodePresetIntentRecordedEvent, "standard_code_preset",
		operation.RunID, map[string]any{"action": operation.Action,
			"backend_intent":   operation.BackendIntent,
			"selected_backend": operation.SelectedBackend,
			"selection_reason": operation.SelectionReason, "status": operation.Status,
			"capability_grant": false})
	if err != nil {
		return domain.StandardCodePresetOperation{}, err
	}
	event.CreatedAt = operation.CreatedAt.UTC()
	inserted, err := insertRunEventTx(ctx, tx, event)
	if err != nil {
		return domain.StandardCodePresetOperation{}, err
	}
	operation.EventSequenceStart = inserted.Sequence
	operation.EventSequenceEnd = inserted.Sequence
	if err := operation.Validate(); err != nil {
		return domain.StandardCodePresetOperation{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO standard_code_preset_operations
		(operation_key_digest, request_fingerprint, protocol_version, requested_run_id,
		run_id, mission_id, workspace_id, action, backend_intent, selected_backend,
		selection_reason, status, drydock_id, drydock_generation,
		drydock_checkpoint_id, mode_snapshot_id, profile_snapshot_id,
		interaction_snapshot_id, permission_snapshot_id, browser_cdp_snapshot_id,
		event_sequence_start, event_sequence_end, requested_by, capability_grant,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL,
		NULL, NULL, NULL, NULL, NULL, NULL, NULL, ?, ?, ?, 0, ?, ?)`,
		operation.KeyDigest, operation.RequestFingerprint, operation.ProtocolVersion,
		operation.RequestedRunID, operation.RunID, operation.MissionID,
		operation.WorkspaceID, operation.Action, operation.BackendIntent,
		operation.SelectedBackend, operation.SelectionReason, operation.Status,
		operation.EventSequenceStart, operation.EventSequenceEnd,
		operation.RequestedBy, ts(operation.CreatedAt), ts(operation.UpdatedAt))
	if err != nil {
		return domain.StandardCodePresetOperation{}, err
	}
	return operation, nil
}

func getStandardCodePresetOperation(ctx context.Context,
	queryer standardCodePresetQueryer, keyDigest string,
) (domain.StandardCodePresetOperation, bool, error) {
	operation, err := scanStandardCodePresetOperation(queryer.QueryRowContext(ctx,
		standardCodePresetOperationSelect+` WHERE operation_key_digest = ?`, keyDigest))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.StandardCodePresetOperation{}, false, nil
	}
	if err != nil {
		return domain.StandardCodePresetOperation{}, false, err
	}
	return operation, true, nil
}

func scanStandardCodePresetOperation(row scanner) (domain.StandardCodePresetOperation, error) {
	var operation domain.StandardCodePresetOperation
	var drydockID, drydockCheckpointID, modeID, profileID, interactionID,
		permissionID, browserCDPID sql.NullString
	var drydockGeneration sql.NullInt64
	var createdAt, updatedAt string
	err := row.Scan(
		&operation.ProtocolVersion, &operation.KeyDigest,
		&operation.RequestFingerprint, &operation.RequestedRunID, &operation.RunID,
		&operation.MissionID, &operation.WorkspaceID, &operation.Action,
		&operation.BackendIntent, &operation.SelectedBackend,
		&operation.SelectionReason, &operation.Status, &drydockID,
		&drydockGeneration, &drydockCheckpointID, &modeID, &profileID,
		&interactionID, &permissionID, &browserCDPID,
		&operation.EventSequenceStart, &operation.EventSequenceEnd,
		&operation.RequestedBy, &operation.CapabilityGrant, &createdAt, &updatedAt)
	if err != nil {
		return domain.StandardCodePresetOperation{}, err
	}
	operation.DrydockID = drydockID.String
	operation.DrydockGeneration = drydockGeneration.Int64
	operation.DrydockCheckpointID = drydockCheckpointID.String
	operation.ModeSnapshotID = modeID.String
	operation.ProfileSnapshotID = profileID.String
	operation.InteractionSnapshotID = interactionID.String
	operation.PermissionSnapshotID = permissionID.String
	operation.BrowserCDPSnapshotID = browserCDPID.String
	operation.CreatedAt = parseTS(createdAt)
	operation.UpdatedAt = parseTS(updatedAt)
	if err := operation.Validate(); err != nil {
		return domain.StandardCodePresetOperation{},
			fmt.Errorf("validate stored Standard Code preset operation: %w", err)
	}
	return operation, nil
}

func sameStandardCodePresetIntent(existing, requested domain.StandardCodePresetOperation) error {
	if existing.ProtocolVersion != requested.ProtocolVersion ||
		existing.KeyDigest != requested.KeyDigest ||
		existing.RequestFingerprint != requested.RequestFingerprint ||
		existing.RequestedRunID != requested.RequestedRunID ||
		existing.RunID != requested.RunID || existing.MissionID != requested.MissionID ||
		existing.WorkspaceID != requested.WorkspaceID ||
		existing.Action != requested.Action ||
		existing.BackendIntent != requested.BackendIntent ||
		existing.SelectedBackend != requested.SelectedBackend ||
		existing.SelectionReason != requested.SelectionReason ||
		existing.RequestedBy != requested.RequestedBy {
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset operation key was already used for different intent")
	}
	return nil
}

func requireStandardCodeDrydockTx(ctx context.Context, tx *sql.Tx,
	operation domain.StandardCodePresetOperation, commit domain.StandardCodePresetCommit,
) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drydock_workspaces drydock
		JOIN drydock_workspace_trust trust_record ON trust_record.id = drydock.trust_id
		WHERE drydock.id = ? AND drydock.run_id = ? AND drydock.mission_id = ?
			AND drydock.source_workspace_id = ? AND drydock.generation = ?
			AND drydock.last_checkpoint_id = ? AND drydock.state IN ('ready','delivered')
			AND trust_record.run_id = drydock.run_id AND trust_record.workspace_id = ?
			AND trust_record.grants_process_authority = 0`, commit.DrydockID,
		operation.RunID, operation.MissionID, operation.WorkspaceID,
		commit.DrydockGeneration, commit.DrydockCheckpointID,
		operation.WorkspaceID).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return apperror.New(apperror.CodeFailedPrecondition,
			"Standard Code preset requires an exact ready trusted Drydock")
	}
	return nil
}

func validateStandardCodeSnapshotTuple(operation domain.StandardCodePresetOperation,
	currentMode domain.RunModeSnapshot, currentProfile domain.RunExecutionProfileSnapshot,
	currentInteraction domain.RunExecutionInteractionSnapshot,
	currentPermission domain.RunExecutionPermissionSnapshot,
	currentCDP domain.RunBrowserCDPPermissionSnapshot,
	commit domain.StandardCodePresetCommit,
) error {
	for _, value := range []struct {
		name, runID, missionID string
		revision, current      int64
		id, currentID          string
		err                    error
	}{
		{"mode", commit.Mode.RunID, commit.Mode.MissionID, commit.Mode.Revision,
			currentMode.Revision, commit.Mode.ID, currentMode.ID, commit.Mode.Validate()},
		{"profile", commit.Profile.RunID, commit.Profile.MissionID,
			commit.Profile.Revision, currentProfile.Revision, commit.Profile.ID,
			currentProfile.ID, commit.Profile.Validate()},
		{"interaction", commit.Interaction.RunID, commit.Interaction.MissionID,
			commit.Interaction.Revision, currentInteraction.Revision,
			commit.Interaction.ID, currentInteraction.ID, commit.Interaction.Validate()},
		{"permission", commit.Permission.RunID, commit.Permission.MissionID,
			commit.Permission.Revision, currentPermission.Revision,
			commit.Permission.ID, currentPermission.ID, commit.Permission.Validate()},
		{"browser CDP", commit.BrowserCDP.RunID, commit.BrowserCDP.MissionID,
			commit.BrowserCDP.Revision, currentCDP.Revision, commit.BrowserCDP.ID,
			currentCDP.ID, commit.BrowserCDP.Validate()},
	} {
		if value.err != nil || value.runID != operation.RunID ||
			value.missionID != operation.MissionID ||
			(value.id != value.currentID && value.revision != value.current+1) ||
			(value.id == value.currentID && value.revision != value.current) {
			return apperror.New(apperror.CodeConflict,
				"Standard Code preset "+value.name+" snapshot changed concurrently")
		}
	}
	if commit.Mode.Surface != domain.ExecutionSurfaceCode ||
		commit.Mode.Phase != domain.ExecutionPhasePlan ||
		commit.Profile.Profile != operation.SelectedBackend.ExecutionProfile() ||
		commit.Interaction.Mode != domain.RunExecutionInteractionControlled ||
		commit.Interaction.Surface != domain.ExecutionSurfaceCode ||
		commit.Interaction.ExecutionProfile != commit.Profile.Profile ||
		commit.Interaction.ExecutionProfileRevision != commit.Profile.Revision ||
		commit.Interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted ||
		commit.Permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		commit.Permission.NetworkScope != domain.ExecutionPermissionNetworkDisabled ||
		commit.BrowserCDP.Mode != domain.RunBrowserCDPPermissionRestricted {
		return apperror.New(apperror.CodeInvalidArgument,
			"Standard Code preset snapshot tuple violates the fixed contract")
	}
	if err := validateStandardCodeModeTransition(currentMode, commit.Mode,
		operation, commit.CommittedAt); err != nil {
		return err
	}
	if err := validateStandardCodeProfileTransition(currentProfile, commit.Profile,
		operation, commit.CommittedAt); err != nil {
		return err
	}
	if err := validateStandardCodeInteractionTransition(currentInteraction,
		commit.Interaction, commit.Mode, commit.Profile, operation,
		commit.CommittedAt); err != nil {
		return err
	}
	if err := validateStandardCodePermissionTransition(currentPermission,
		commit.Permission, operation, commit.CommittedAt); err != nil {
		return err
	}
	if err := validateStandardCodeCDPTransition(currentCDP, commit.BrowserCDP,
		operation, commit.CommittedAt); err != nil {
		return err
	}
	return nil
}

func validateStandardCodeModeTransition(current, desired domain.RunModeSnapshot,
	operation domain.StandardCodePresetOperation, at time.Time,
) error {
	if desired.ID == current.ID {
		if reflect.DeepEqual(desired, current) {
			return nil
		}
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset reused the current mode snapshot with changed content")
	}
	expected, err := current.Next(desired.ID, domain.ExecutionPhasePlan,
		operation.RequestedBy, "Standard Code preset enters Plan", at)
	if err != nil || !reflect.DeepEqual(desired, expected) {
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset mode transition is not exact")
	}
	return nil
}

func validateStandardCodeProfileTransition(current,
	desired domain.RunExecutionProfileSnapshot,
	operation domain.StandardCodePresetOperation, at time.Time,
) error {
	if desired.ID == current.ID {
		if reflect.DeepEqual(desired, current) {
			return nil
		}
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset reused the current profile snapshot with changed content")
	}
	expected, err := current.Next(desired.ID, operation.SelectedBackend.ExecutionProfile(),
		operation.RequestedBy, "Standard Code preset selected sandbox backend", at)
	if err != nil || !reflect.DeepEqual(desired, expected) {
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset profile transition is not exact")
	}
	return nil
}

func validateStandardCodeInteractionTransition(current,
	desired domain.RunExecutionInteractionSnapshot, mode domain.RunModeSnapshot,
	profile domain.RunExecutionProfileSnapshot,
	operation domain.StandardCodePresetOperation, at time.Time,
) error {
	if desired.ID == current.ID {
		if reflect.DeepEqual(desired, current) {
			return nil
		}
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset reused the current interaction snapshot with changed content")
	}
	expected, err := current.Next(desired.ID,
		domain.RunExecutionInteractionControlled, mode, profile,
		domain.WorkspaceTrustTrusted, true, operation.RequestedBy,
		"Standard Code preset selected controlled interaction", at)
	if err != nil || !reflect.DeepEqual(desired, expected) {
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset interaction transition is not exact")
	}
	return nil
}

func validateStandardCodePermissionTransition(current,
	desired domain.RunExecutionPermissionSnapshot,
	operation domain.StandardCodePresetOperation, at time.Time,
) error {
	if desired.ID == current.ID {
		if reflect.DeepEqual(desired, current) {
			return nil
		}
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset reused the current permission snapshot with changed content")
	}
	expected, err := current.Next(desired.ID,
		domain.RunExecutionPermissionWorkspaceAccess, true, operation.RequestedBy,
		"Standard Code preset selected Workspace Access", at)
	if err != nil || !reflect.DeepEqual(desired, expected) {
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset permission transition is not exact")
	}
	return nil
}

func validateStandardCodeCDPTransition(current,
	desired domain.RunBrowserCDPPermissionSnapshot,
	operation domain.StandardCodePresetOperation, at time.Time,
) error {
	if desired.ID == current.ID {
		if reflect.DeepEqual(desired, current) {
			return nil
		}
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset reused the current browser CDP snapshot with changed content")
	}
	expected, err := current.Next(desired.ID,
		domain.RunBrowserCDPPermissionRestricted, false, operation.RequestedBy,
		"Standard Code preset disabled Full CDP", at)
	if err != nil || !reflect.DeepEqual(desired, expected) {
		return apperror.New(apperror.CodeConflict,
			"Standard Code preset browser CDP transition is not exact")
	}
	return nil
}

func appendStandardCodeSnapshotEventTx(ctx context.Context, tx *sql.Tx, runID,
	missionID, eventType, subjectID string, at time.Time,
	payload map[string]any,
) error {
	event, err := events.New(runID, missionID, eventType,
		"standard_code_preset", subjectID, payload)
	if err != nil {
		return err
	}
	event.CreatedAt = at.UTC()
	_, err = insertRunEventTx(ctx, tx, event)
	return err
}
