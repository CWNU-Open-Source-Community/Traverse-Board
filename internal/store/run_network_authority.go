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
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/webevidence"
)

type runNetworkAuthorityEventPayload struct {
	Protocol             string   `json:"protocol"`
	Revision             int64    `json:"revision"`
	ExpectedModeRevision int64    `json:"expected_mode_revision"`
	FromNetworkMode      string   `json:"from_network_mode"`
	ToNetworkMode        string   `json:"to_network_mode"`
	PreviousTargetCount  int      `json:"previous_target_count"`
	AddedTargets         []string `json:"added_targets"`
	AddedTargetCount     int      `json:"added_target_count"`
	AllowedTargetCount   int      `json:"allowed_target_count"`
	RequestedBy          string   `json:"requested_by"`
	Reason               string   `json:"reason"`
	CapabilityGrant      *bool    `json:"capability_grant"`
}

func (s *SQLiteStore) GetRunNetworkAuthorityOperation(ctx context.Context,
	keyDigest string,
) (domain.RunNetworkAuthorityOperation, bool, error) {
	keyDigest = strings.TrimSpace(keyDigest)
	if !validStoreDigest(keyDigest) {
		return domain.RunNetworkAuthorityOperation{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Run network authority operation digest is invalid")
	}
	return getRunNetworkAuthorityOperation(ctx, s.db, keyDigest)
}

func (s *SQLiteStore) TransitionRunNetworkAuthority(ctx context.Context,
	snapshot domain.RunModeSnapshot, operation domain.RunNetworkAuthorityOperation,
	event events.Event,
) (domain.RunModeSnapshot, bool, error) {
	snapshot.Scope = domain.CloneScope(snapshot.Scope)
	payload, err := validateRunNetworkAuthorityMutation(snapshot, operation, event)
	if err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireRunModeWriteLockTx(ctx, tx, snapshot.RunID); err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	if existing, found, err := getRunNetworkAuthorityOperation(
		ctx, tx, operation.KeyDigest); err != nil {
		return domain.RunModeSnapshot{}, false, err
	} else if found {
		if err := validateRunNetworkAuthorityReplay(existing, operation); err != nil {
			return domain.RunModeSnapshot{}, false, err
		}
		stored, err := getRunModeSnapshot(ctx, tx, existing.SnapshotID)
		if err != nil {
			return domain.RunModeSnapshot{}, false, err
		}
		if err := validateRunNetworkAuthorityOperationBinding(ctx, tx, existing, stored); err != nil {
			return domain.RunModeSnapshot{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return domain.RunModeSnapshot{}, false, err
		}
		return stored, true, nil
	}

	current, err := getCurrentRunModeSnapshot(ctx, tx, snapshot.RunID)
	if err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	run, mission, err := getCoordinatorRunTx(ctx, tx, snapshot.RunID)
	if err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	if !domain.CanExpandRunNetworkAuthority(run.Status) {
		return domain.RunModeSnapshot{}, false, apperror.New(apperror.CodeConflict,
			fmt.Sprintf("Run network authority can only expand while created or paused; Run is %s",
				run.Status))
	}
	var activeLeaseCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_execution_leases
		WHERE run_id = ? AND status = 'active' AND julianday(expires_at) > julianday('now')`,
		run.ID).Scan(&activeLeaseCount); err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	if activeLeaseCount != 0 {
		return domain.RunModeSnapshot{}, false, apperror.New(apperror.CodeConflict,
			"Run network authority cannot expand while an execution lease is active")
	}
	if operation.ExpectedModeRevision != current.Revision ||
		snapshot.Revision != current.Revision+1 || snapshot.Revision != operation.ExpectedModeRevision+1 ||
		snapshot.MissionID != run.MissionID || snapshot.MissionID != mission.ID ||
		snapshot.Profile != mission.Profile ||
		snapshot.Scope.WorkspaceID != mission.Scope.WorkspaceID ||
		!snapshot.SameStaticPolicy(current) || snapshot.Phase != current.Phase ||
		snapshot.CreatedAt.Before(current.CreatedAt) {
		return domain.RunModeSnapshot{}, false, apperror.New(apperror.CodeConflict,
			"Run mode changed concurrently or network authority attempted another policy mutation")
	}
	added, err := validateExactRunNetworkExpansion(current, snapshot)
	if err != nil {
		return domain.RunModeSnapshot{}, false, apperror.Wrap(
			apperror.CodeConflict, "Run network authority expansion is invalid", err)
	}
	if payload.FromNetworkMode != current.Scope.NetworkMode ||
		payload.PreviousTargetCount != len(current.Scope.AllowedTargets) ||
		!sameStringSlice(added, payload.AddedTargets) ||
		operation.RequestFingerprint != runmutation.RunNetworkAuthorityRequestFingerprint(
			snapshot.RunID, operation.ExpectedModeRevision, added,
			snapshot.RequestedBy, snapshot.Reason) {
		return domain.RunModeSnapshot{}, false, apperror.New(apperror.CodeInvalidArgument,
			"Run network authority operation or event does not match its exact target delta")
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO run_network_authority_operations
		(operation_key_digest, request_fingerprint, snapshot_id, run_id,
		expected_mode_revision, requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		operation.KeyDigest, operation.RequestFingerprint, operation.SnapshotID,
		operation.RunID, operation.ExpectedModeRevision, operation.RequestedBy,
		ts(operation.CreatedAt)); err != nil {
		_ = tx.Rollback()
		return s.recoverRunNetworkAuthorityTransition(ctx, operation, err)
	}
	if err := insertRunModeSnapshotTx(ctx, tx, snapshot); err != nil {
		_ = tx.Rollback()
		return s.recoverRunNetworkAuthorityTransition(ctx, operation, err)
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	return snapshot, false, nil
}

func validateRunNetworkAuthorityMutation(snapshot domain.RunModeSnapshot,
	operation domain.RunNetworkAuthorityOperation, event events.Event,
) (runNetworkAuthorityEventPayload, error) {
	if err := snapshot.Validate(); err != nil {
		return runNetworkAuthorityEventPayload{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Run network authority snapshot is invalid", err)
	}
	if err := requireRedactedRunModeSnapshot(snapshot); err != nil {
		return runNetworkAuthorityEventPayload{}, err
	}
	if snapshot.Revision <= 1 {
		return runNetworkAuthorityEventPayload{}, apperror.New(
			apperror.CodeInvalidArgument, "Run network authority snapshot revision must exceed one")
	}
	if err := operation.Validate(); err != nil {
		return runNetworkAuthorityEventPayload{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Run network authority operation is invalid", err)
	}
	if operation.SnapshotID != snapshot.ID || operation.RunID != snapshot.RunID ||
		operation.ExpectedModeRevision+1 != snapshot.Revision ||
		operation.RequestedBy != snapshot.RequestedBy ||
		!operation.CreatedAt.Equal(snapshot.CreatedAt) {
		return runNetworkAuthorityEventPayload{}, apperror.New(
			apperror.CodeInvalidArgument, "Run network authority operation does not match its snapshot")
	}
	payload, err := validateRunNetworkAuthorityExpandedEvent(event, snapshot, operation)
	if err != nil {
		return runNetworkAuthorityEventPayload{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Run network authority event is invalid", err)
	}
	return payload, nil
}

func validateRunNetworkAuthorityExpandedEvent(event events.Event,
	snapshot domain.RunModeSnapshot, operation domain.RunNetworkAuthorityOperation,
) (runNetworkAuthorityEventPayload, error) {
	if err := event.Validate(); err != nil {
		return runNetworkAuthorityEventPayload{}, err
	}
	if event.Type != events.RunNetworkAuthorityExpandedEvent ||
		event.Source != "run_network_authority" || event.RunID != snapshot.RunID ||
		event.MissionID != snapshot.MissionID || event.SubjectID != snapshot.ID ||
		!event.CreatedAt.Equal(snapshot.CreatedAt) {
		return runNetworkAuthorityEventPayload{}, errors.New(
			"Run network authority event identity does not match its snapshot")
	}
	if err := rejectDuplicateRunModeEventFields(event.PayloadJSON); err != nil {
		return runNetworkAuthorityEventPayload{}, err
	}
	var payload runNetworkAuthorityEventPayload
	decoder := json.NewDecoder(strings.NewReader(event.PayloadJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return runNetworkAuthorityEventPayload{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return runNetworkAuthorityEventPayload{}, errors.New(
			"Run network authority event contains trailing data")
	}
	canonical, err := webevidence.NormalizeExactAuthorityTargets(payload.AddedTargets)
	if err != nil || !sameStringSlice(canonical, payload.AddedTargets) {
		return runNetworkAuthorityEventPayload{}, errors.New(
			"Run network authority event targets are not exact and canonical")
	}
	if payload.Protocol != domain.RunNetworkAuthorityProtocolVersion ||
		payload.Revision != snapshot.Revision ||
		payload.ExpectedModeRevision != operation.ExpectedModeRevision ||
		payload.ExpectedModeRevision+1 != payload.Revision ||
		(payload.FromNetworkMode != "disabled" && payload.FromNetworkMode != "allowlist") ||
		payload.ToNetworkMode != "allowlist" ||
		payload.PreviousTargetCount < 0 ||
		payload.AddedTargetCount != len(payload.AddedTargets) ||
		payload.AllowedTargetCount != len(snapshot.Scope.AllowedTargets) ||
		payload.PreviousTargetCount+payload.AddedTargetCount != payload.AllowedTargetCount ||
		payload.RequestedBy != snapshot.RequestedBy || payload.Reason != snapshot.Reason ||
		payload.CapabilityGrant == nil || !*payload.CapabilityGrant {
		return runNetworkAuthorityEventPayload{}, errors.New(
			"Run network authority event does not match its explicit capability grant")
	}
	return payload, nil
}

func validateExactRunNetworkExpansion(current, next domain.RunModeSnapshot) ([]string, error) {
	currentTargets, err := canonicalStoredRunNetworkTargets(current.Scope)
	if err != nil {
		return nil, err
	}
	nextTargets, err := canonicalStoredRunNetworkTargets(next.Scope)
	if err != nil || next.Scope.NetworkMode != "allowlist" {
		return nil, errors.New("next Run mode does not contain an exact allowlist")
	}
	currentSet := make(map[string]struct{}, len(currentTargets))
	for _, target := range currentTargets {
		currentSet[target] = struct{}{}
	}
	added := make([]string, 0, len(nextTargets)-len(currentTargets))
	for _, target := range nextTargets {
		if _, exists := currentSet[target]; !exists {
			added = append(added, target)
		}
	}
	if len(added) == 0 || len(nextTargets) != len(currentTargets)+len(added) {
		return nil, errors.New("next Run mode is not a strict target superset")
	}
	for _, target := range currentTargets {
		if !containsSortedTarget(nextTargets, target) {
			return nil, errors.New("next Run mode removed an existing target")
		}
	}
	return added, nil
}

func canonicalStoredRunNetworkTargets(scope domain.Scope) ([]string, error) {
	if scope.NetworkMode == "disabled" {
		if len(scope.AllowedTargets) != 0 {
			return nil, errors.New("disabled Run network authority retained targets")
		}
		return nil, nil
	}
	if scope.NetworkMode != "allowlist" {
		return nil, errors.New("unsupported Run network authority mode")
	}
	targets, err := webevidence.NormalizeExactAuthorityTargets(scope.AllowedTargets)
	if err != nil || !sameStringSlice(targets, scope.AllowedTargets) {
		return nil, errors.New("Run network authority is not an exact canonical allowlist")
	}
	return targets, nil
}

func (s *SQLiteStore) recoverRunNetworkAuthorityTransition(ctx context.Context,
	operation domain.RunNetworkAuthorityOperation, original error,
) (domain.RunModeSnapshot, bool, error) {
	existing, found, err := getRunNetworkAuthorityOperation(ctx, s.db, operation.KeyDigest)
	if err != nil || !found {
		if err == nil {
			return domain.RunModeSnapshot{}, false, original
		}
		return domain.RunModeSnapshot{}, false, errors.Join(original, err)
	}
	if err := validateRunNetworkAuthorityReplay(existing, operation); err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	stored, err := getRunModeSnapshot(ctx, s.db, existing.SnapshotID)
	if err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	if err := validateRunNetworkAuthorityOperationBinding(ctx, s.db, existing, stored); err != nil {
		return domain.RunModeSnapshot{}, false, err
	}
	return stored, true, nil
}

func validateRunNetworkAuthorityReplay(existing,
	request domain.RunNetworkAuthorityOperation,
) error {
	if existing.KeyDigest != request.KeyDigest ||
		existing.RequestFingerprint != request.RequestFingerprint ||
		existing.RunID != request.RunID ||
		existing.ExpectedModeRevision != request.ExpectedModeRevision ||
		existing.RequestedBy != request.RequestedBy {
		return apperror.New(apperror.CodeConflict,
			"Run network authority operation key was already used for different intent")
	}
	return nil
}

func validateRunNetworkAuthorityOperationBinding(ctx context.Context,
	queryer runModeQueryer, operation domain.RunNetworkAuthorityOperation,
	snapshot domain.RunModeSnapshot,
) error {
	if operation.SnapshotID != snapshot.ID || operation.RunID != snapshot.RunID ||
		operation.ExpectedModeRevision+1 != snapshot.Revision ||
		operation.RequestedBy != snapshot.RequestedBy ||
		!operation.CreatedAt.Equal(snapshot.CreatedAt) {
		return apperror.New(apperror.CodeInternal,
			"stored Run network authority operation binding is invalid")
	}
	previous, err := scanRunModeSnapshot(queryer.QueryRowContext(ctx,
		runModeSnapshotSelect+` WHERE run_id = ? AND revision = ?`,
		snapshot.RunID, operation.ExpectedModeRevision))
	if err != nil {
		return err
	}
	if !snapshot.SameStaticPolicy(previous) || snapshot.Phase != previous.Phase {
		return apperror.New(apperror.CodeInternal,
			"stored Run network authority changed immutable mode policy")
	}
	added, err := validateExactRunNetworkExpansion(previous, snapshot)
	if err != nil || operation.RequestFingerprint !=
		runmutation.RunNetworkAuthorityRequestFingerprint(snapshot.RunID,
			operation.ExpectedModeRevision, added, snapshot.RequestedBy, snapshot.Reason) {
		return apperror.New(apperror.CodeInternal,
			"stored Run network authority operation fingerprint is invalid")
	}
	return nil
}

func getRunNetworkAuthorityOperation(ctx context.Context, queryer runModeQueryer,
	keyDigest string,
) (domain.RunNetworkAuthorityOperation, bool, error) {
	var operation domain.RunNetworkAuthorityOperation
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT operation_key_digest,
		request_fingerprint, snapshot_id, run_id, expected_mode_revision,
		requested_by, created_at FROM run_network_authority_operations
		WHERE operation_key_digest = ?`, keyDigest).Scan(
		&operation.KeyDigest, &operation.RequestFingerprint, &operation.SnapshotID,
		&operation.RunID, &operation.ExpectedModeRevision, &operation.RequestedBy,
		&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RunNetworkAuthorityOperation{}, false, nil
	}
	if err != nil {
		return domain.RunNetworkAuthorityOperation{}, false, err
	}
	operation.CreatedAt = parseTS(createdAt)
	return operation, true, operation.Validate()
}

func sameStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsSortedTarget(targets []string, expected string) bool {
	for _, target := range targets {
		if target == expected {
			return true
		}
		if target > expected {
			return false
		}
	}
	return false
}
