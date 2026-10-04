package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runner"
)

// Frozen old writer used only to construct authentic v141/v142 upgrade input.
// New application work uses Command Runtime. Current schemas are rejected here
// before any validation or database write; original SQL guards remain in force.
func seedHistoricalHostExecutionIntent(
	ctx context.Context, s *SQLiteStore,
	intent runner.HostExecutionIntent,
) (bool, error) {
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return false, err
	}
	if version != 141 && version != 142 {
		return false, fmt.Errorf("historical host intent fixture rejects schema %d", version)
	}
	if err := intent.Validate(); err != nil {
		return false, apperror.Wrap(apperror.CodeInvalidArgument,
			"host command execution intent is invalid", err)
	}
	argvJSON, environmentKeysJSON, err := encodeHostExecutionSpec(intent.Spec)
	if err != nil {
		return false, err
	}
	if redact.String(argvJSON) != argvJSON ||
		redact.String(environmentKeysJSON) != environmentKeysJSON ||
		redact.String(intent.Spec.ExecutablePath) !=
			intent.Spec.ExecutablePath ||
		redact.String(intent.Spec.WorkingDirectory) !=
			intent.Spec.WorkingDirectory {
		return false, apperror.New(apperror.CodeInvalidArgument,
			"host command execution intent contains secret-like data")
	}
	requestFingerprint := runner.HostExecutionIntentFingerprint(intent)
	if requestFingerprint == "" {
		return false, apperror.New(apperror.CodeInvalidArgument,
			"host command execution request fingerprint is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunExecutionInteractionWriteLockTx(
		ctx, tx, intent.RunID); err != nil {
		return false, err
	}
	existing, found, err := getHostExecutionIntent(
		ctx, tx, intent.RequestID)
	if err != nil {
		return false, err
	}
	if found {
		if !hostExecutionIntentsEqual(existing, intent) {
			return false, apperror.New(apperror.CodeConflict,
				"host command execution intent conflicts with its durable record")
		}
		operation, operationFound, err := getHostExecutionOperation(
			ctx, tx, intent.OperationKeyDigest)
		if err != nil {
			return false, err
		}
		if !operationFound ||
			operation.RequestID != intent.RequestID ||
			operation.RequestFingerprint != requestFingerprint {
			return false, apperror.New(apperror.CodeConflict,
				"host command execution operation record is inconsistent")
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	operation, found, err := getHostExecutionOperation(
		ctx, tx, intent.OperationKeyDigest)
	if err != nil {
		return false, err
	}
	if found {
		if operation.RequestID != intent.RequestID ||
			operation.RequestFingerprint != requestFingerprint ||
			operation.RunID != intent.RunID ||
			operation.RequestedBy != intent.RequestedBy {
			return false, apperror.New(apperror.CodeConflict,
				"host command execution operation key was reused for different intent")
		}
		return false, apperror.New(apperror.CodeConflict,
			"host command operation exists without its immutable intent")
	}

	runRecord, mission, err := getCoordinatorRunTx(ctx, tx, intent.RunID)
	if err != nil {
		return false, err
	}
	if (runRecord.Status != domain.RunCreated &&
		runRecord.Status != domain.RunPaused) ||
		runRecord.MissionID != intent.MissionID ||
		runRecord.SessionID != intent.SessionID ||
		mission.WorkspaceID != intent.WorkspaceID {
		return false, apperror.New(apperror.CodeFailedPrecondition,
			"host command execution requires the current created or paused Run")
	}
	if err := requireNoActiveRunControlLeaseTx(
		ctx, tx, runRecord.ID, intent.CreatedAt); err != nil {
		return false, err
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(
		ctx, tx, runRecord.ID)
	if err != nil {
		return false, err
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(
		ctx, tx, runRecord.ID)
	if err != nil {
		return false, err
	}
	permission, err := getCurrentRunExecutionPermissionSnapshot(
		ctx, tx, runRecord.ID)
	if err != nil {
		return false, err
	}
	mode, err := getCurrentRunModeSnapshot(ctx, tx, runRecord.ID)
	if err != nil {
		return false, err
	}
	if interaction.ID != intent.InteractionSnapshotID ||
		interaction.Revision != intent.InteractionRevision ||
		interaction.Mode != domain.RunExecutionInteractionControlled ||
		interaction.ExecutionProfileRevision !=
			intent.ExecutionProfileRevision ||
		profile.Revision != intent.ExecutionProfileRevision ||
		profile.Profile != domain.RunExecutionProfileLocal ||
		permission.ID != intent.PermissionSnapshotID ||
		permission.Revision != intent.PermissionRevision ||
		permission.Mode != intent.PermissionMode ||
		!permission.Mode.IncludesFullAccess() ||
		mode.Surface != domain.ExecutionSurfaceCode {
		return false, apperror.New(apperror.CodeConflict,
			"host command execution durable binding is stale")
	}
	spec := intent.Spec
	if _, err := tx.ExecContext(ctx, `INSERT INTO
		host_command_execution_intents
		(request_id, protocol_version, policy_version, operation_key_digest,
		run_id, mission_id, session_id, workspace_id, interaction_snapshot_id,
		interaction_revision, execution_profile_revision,
		permission_snapshot_id, permission_revision, permission_mode,
		spec_protocol_version, spec_policy_version, executable_path,
		executable_sha256, argv_json, working_directory, environment_policy,
		environment_keys_json, environment_sha256, network_intent,
		timeout_millis, purpose, spec_fingerprint, requested_by,
		non_sandboxed, automatic_retry_allowed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		intent.RequestID, intent.ProtocolVersion, intent.PolicyVersion,
		intent.OperationKeyDigest, intent.RunID, intent.MissionID,
		intent.SessionID, intent.WorkspaceID, intent.InteractionSnapshotID,
		intent.InteractionRevision, intent.ExecutionProfileRevision,
		intent.PermissionSnapshotID, intent.PermissionRevision,
		intent.PermissionMode, spec.ProtocolVersion, spec.PolicyVersion,
		spec.ExecutablePath, spec.ExecutableSHA256, argvJSON,
		spec.WorkingDirectory, spec.EnvironmentPolicy, environmentKeysJSON,
		spec.EnvironmentSHA256, spec.NetworkIntent,
		spec.TimeoutMilliseconds, spec.Purpose, spec.Fingerprint,
		intent.RequestedBy, intent.NonSandboxed,
		intent.AutomaticRetryAllowed, ts(intent.CreatedAt)); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO
		host_command_execution_operations
		(operation_key_digest, request_fingerprint, request_id, run_id,
		requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		intent.OperationKeyDigest, requestFingerprint, intent.RequestID,
		intent.RunID, intent.RequestedBy, ts(intent.CreatedAt)); err != nil {
		return false, err
	}
	if err := appendSupervisorEventTx(ctx, tx, runRecord,
		events.HostCommandExecutionPreparedEvent,
		"host_command_execution", intent.RequestID, map[string]any{
			"protocol":                     intent.ProtocolVersion,
			"permission_mode":              string(intent.PermissionMode),
			"non_sandboxed":                true,
			"automatic_retry_allowed":      false,
			"environment_values_persisted": false,
			"raw_output_persisted":         false,
		}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

func encodeHostExecutionSpec(
	spec runner.HostCommandSpec,
) (string, string, error) {
	argv, err := json.Marshal(spec.Argv)
	if err != nil {
		return "", "", err
	}
	keys, err := json.Marshal(spec.EnvironmentKeys)
	if err != nil {
		return "", "", err
	}
	return string(argv), string(keys), nil
}

func hostExecutionIntentsEqual(
	left runner.HostExecutionIntent,
	right runner.HostExecutionIntent,
) bool {
	left.CreatedAt = time.Time{}
	right.CreatedAt = time.Time{}
	return reflect.DeepEqual(left, right)
}

func TestHistoricalHostIntentSeedRejectsCurrentSchema(t *testing.T) {
	for _, version := range []int{178, 0} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			state := openRunSeedBoundaryStore(t, version)
			before := runSeedBoundaryRows(t, state)
			if _, err := seedHistoricalHostExecutionIntent(t.Context(), state, runner.HostExecutionIntent{}); err == nil || !strings.Contains(err.Error(), "historical host intent fixture rejects schema") {
				t.Fatalf("current host seed accepted: %v", err)
			}
			if after := runSeedBoundaryRows(t, state); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected historical host intent left rows: %v => %v", before, after)
			}
		})
	}
}
