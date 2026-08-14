package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/sandbox"
)

const dockerContainerLifecycleIntentSelect = `SELECT id, attempt_id, plan_id, run_id,
	mission_id, workspace_id, protocol_version, resource_generation,
	operation_key_digest, request_fingerprint, spec_fingerprint, plan_fingerprint,
	authority_fingerprint, base_label_plan_fingerprint, ownership_label_fingerprint,
	container_name_fingerprint, endpoint_class, endpoint_fingerprint, intent_fingerprint,
	product_entry_enabled, execution_authorized, artifact_commit_authorized,
	requested_by, created_at FROM sandbox_docker_lifecycle_intents`

// BeginDockerContainerLifecycle atomically commits immutable launch intent and
// the initial lease. A caller must complete this method before any Docker write.
func (s *SQLiteStore) BeginDockerContainerLifecycle(ctx context.Context,
	intent sandbox.DockerContainerLaunchIntent, ownerID string, ttl time.Duration,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	ownerID = strings.TrimSpace(ownerID)
	if intent.Validate() != nil || !validDockerLifecycleOwner(ownerID) ||
		sandbox.ValidateDockerContainerLifecycleLeaseTTL(ttl) != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle intent or lease owner is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireSandboxManifestWriteLock(ctx, tx, intent.RunID); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if existing, found, getErr := getDockerContainerLifecycleByOperation(ctx, tx,
		intent.OperationKeyDigest); getErr != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, getErr
	} else if found {
		if existing.Intent.IntentFingerprint != intent.IntentFingerprint ||
			existing.Intent.ID != intent.ID || existing.Intent.PlanID != intent.PlanID ||
			existing.Intent.RequestedBy != intent.RequestedBy {
			return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
				apperror.CodeConflict, "Docker lifecycle operation key was used for different intent")
		}
		if err := tx.Commit(); err != nil {
			return sandbox.DockerContainerLifecycleRecord{}, false, err
		}
		existing.Replayed = true
		return existing, true, nil
	}
	if _, found, getErr := getDockerContainerLifecycleByPlan(ctx, tx, intent.PlanID); getErr != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, getErr
	} else if found {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeConflict, "Docker container plan already has a lifecycle intent")
	}
	if err := validateDockerContainerLaunchIntentCurrentTx(ctx, tx, intent); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	now := time.Now().UTC()
	if intent.CreatedAt.After(now) {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle intent timestamp is in the future")
	}
	lease, err := sandbox.NewDockerContainerLifecycleLease(intent, newSandboxLeaseID(),
		ownerID, 1, now, ttl)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := insertDockerContainerLifecycleIntentTx(ctx, tx, intent); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_lifecycle_leases
		(intent_id, resource_generation, lease_id, owner_id, generation, status,
		acquired_at, renewed_at, expires_at, released_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, lease.IntentID,
		lease.ResourceGeneration, lease.LeaseID, lease.OwnerID, lease.Generation,
		lease.Status, ts(lease.AcquiredAt), ts(lease.RenewedAt), ts(lease.ExpiresAt)); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := appendDockerContainerLifecycleEvent(ctx, tx, intent,
		events.SandboxDockerLifecyclePreparedEvent, intent.CreatedAt, map[string]any{
			"resource_generation": 1, "lease_generation": 1,
			"product_entry_enabled": false, "execution_authorized": false,
			"artifact_commit_authorized": false,
		}); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	record := sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease}
	return record, false, record.Validate()
}

func (s *SQLiteStore) AcquireDockerContainerLifecycle(ctx context.Context,
	intentID, requestedBy, ownerID string, ttl time.Duration,
) (sandbox.DockerContainerLifecycleRecord, error) {
	intentID, requestedBy, ownerID = strings.TrimSpace(intentID),
		strings.TrimSpace(requestedBy), strings.TrimSpace(ownerID)
	if !domain.ValidAgentID(intentID) || !domain.ValidAgentID(requestedBy) ||
		!validDockerLifecycleOwner(ownerID) ||
		sandbox.ValidateDockerContainerLifecycleLeaseTTL(ttl) != nil {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle acquisition is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getDockerContainerLifecycle(ctx, tx, intentID)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	if record.Intent.RequestedBy != requestedBy {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
			apperror.CodeConflict, "Docker lifecycle requester changed")
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, record.Intent.RunID); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	if record.Receipt != nil {
		if err := tx.Commit(); err != nil {
			return sandbox.DockerContainerLifecycleRecord{}, err
		}
		record.Replayed = true
		return record, nil
	}
	now := time.Now().UTC()
	if record.Lease.ActiveAt(now) {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
			apperror.CodeConflict, "Docker lifecycle already has an active lease")
	}
	next, err := sandbox.NewDockerContainerLifecycleLease(record.Intent, newSandboxLeaseID(),
		ownerID, record.Lease.Generation+1, now, ttl)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sandbox_docker_lifecycle_leases SET
		resource_generation = ?, lease_id = ?, owner_id = ?, generation = ?, status = 'active',
		acquired_at = ?, renewed_at = ?, expires_at = ?, released_at = NULL
		WHERE intent_id = ? AND lease_id = ? AND owner_id = ? AND generation = ?
		AND resource_generation = ? AND status = ? AND expires_at = ?`,
		next.ResourceGeneration, next.LeaseID, next.OwnerID, next.Generation,
		ts(next.AcquiredAt), ts(next.RenewedAt), ts(next.ExpiresAt), record.Intent.ID,
		record.Lease.LeaseID, record.Lease.OwnerID, record.Lease.Generation,
		record.Lease.ResourceGeneration, record.Lease.Status, ts(record.Lease.ExpiresAt))
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	if err := requireSingleLeaseUpdate(result, "Docker lifecycle lease changed before takeover"); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	if err := appendDockerContainerLifecycleEvent(ctx, tx, record.Intent,
		events.SandboxDockerLifecycleTakenOverEvent, now, map[string]any{
			"resource_generation": next.ResourceGeneration,
			"lease_generation":    next.Generation,
			"previous_generation": record.Lease.Generation,
		}); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	record.Lease, record.TookOver = next, true
	return record, record.Validate()
}

func (s *SQLiteStore) RenewDockerContainerLifecycleLease(ctx context.Context,
	expected sandbox.DockerContainerLifecycleLease, ttl time.Duration,
) (sandbox.DockerContainerLifecycleLease, error) {
	if expected.Validate() != nil || sandbox.ValidateDockerContainerLifecycleLeaseTTL(ttl) != nil {
		return sandbox.DockerContainerLifecycleLease{}, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle lease renewal is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return sandbox.DockerContainerLifecycleLease{}, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getDockerContainerLifecycle(ctx, tx, expected.IntentID)
	if err != nil {
		return sandbox.DockerContainerLifecycleLease{}, err
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, record.Intent.RunID); err != nil {
		return sandbox.DockerContainerLifecycleLease{}, err
	}
	now := time.Now().UTC()
	if err := requireCurrentDockerContainerLifecycleLease(record.Lease, expected, now); err != nil {
		return sandbox.DockerContainerLifecycleLease{}, err
	}
	next, err := expected.Renew(now, ttl)
	if err != nil {
		return sandbox.DockerContainerLifecycleLease{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sandbox_docker_lifecycle_leases
		SET renewed_at = ?, expires_at = ? WHERE intent_id = ? AND lease_id = ?
		AND owner_id = ? AND generation = ? AND status = 'active'
		AND renewed_at = ? AND expires_at = ?`, ts(next.RenewedAt), ts(next.ExpiresAt),
		expected.IntentID, expected.LeaseID, expected.OwnerID, expected.Generation,
		ts(expected.RenewedAt), ts(expected.ExpiresAt))
	if err != nil {
		return sandbox.DockerContainerLifecycleLease{}, err
	}
	if err := requireSingleLeaseUpdate(result, "Docker lifecycle lease changed before renewal"); err != nil {
		return sandbox.DockerContainerLifecycleLease{}, err
	}
	if err := tx.Commit(); err != nil {
		return sandbox.DockerContainerLifecycleLease{}, err
	}
	return next, nil
}

func (s *SQLiteStore) ReleaseDockerContainerLifecycleLease(ctx context.Context,
	expected sandbox.DockerContainerLifecycleLease,
) (sandbox.DockerContainerLifecycleLease, bool, error) {
	if expected.Validate() != nil {
		return sandbox.DockerContainerLifecycleLease{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle lease release is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return sandbox.DockerContainerLifecycleLease{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getDockerContainerLifecycle(ctx, tx, expected.IntentID)
	if err != nil {
		return sandbox.DockerContainerLifecycleLease{}, false, err
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, record.Intent.RunID); err != nil {
		return sandbox.DockerContainerLifecycleLease{}, false, err
	}
	if record.Lease.Status == sandbox.DockerContainerLifecycleLeaseReleased {
		if record.Lease.LeaseID != expected.LeaseID || record.Lease.OwnerID != expected.OwnerID ||
			record.Lease.Generation != expected.Generation {
			return sandbox.DockerContainerLifecycleLease{}, false, apperror.New(
				apperror.CodeConflict, "Docker lifecycle lease was replaced before release")
		}
		if err := tx.Commit(); err != nil {
			return sandbox.DockerContainerLifecycleLease{}, false, err
		}
		return record.Lease, true, nil
	}
	now := time.Now().UTC()
	if err := requireCurrentDockerContainerLifecycleLease(record.Lease, expected, now); err != nil {
		return sandbox.DockerContainerLifecycleLease{}, false, err
	}
	released := expected
	released.Status = sandbox.DockerContainerLifecycleLeaseReleased
	releasedAt := now
	released.ReleasedAt = &releasedAt
	if released.Validate() != nil {
		return sandbox.DockerContainerLifecycleLease{}, false, apperror.New(
			apperror.CodeInternal, "released Docker lifecycle lease is invalid")
	}
	result, err := tx.ExecContext(ctx, `UPDATE sandbox_docker_lifecycle_leases
		SET status = 'released', released_at = ? WHERE intent_id = ? AND lease_id = ?
		AND owner_id = ? AND generation = ? AND status = 'active'
		AND renewed_at = ? AND expires_at = ?`, ts(now), expected.IntentID, expected.LeaseID,
		expected.OwnerID, expected.Generation, ts(expected.RenewedAt), ts(expected.ExpiresAt))
	if err != nil {
		return sandbox.DockerContainerLifecycleLease{}, false, err
	}
	if err := requireSingleLeaseUpdate(result, "Docker lifecycle lease changed before release"); err != nil {
		return sandbox.DockerContainerLifecycleLease{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return sandbox.DockerContainerLifecycleLease{}, false, err
	}
	return released, false, nil
}

// FenceDockerContainerLifecycle performs the last durable full-identity check
// before a Docker write. It intentionally makes no daemon call itself.
func (s *SQLiteStore) FenceDockerContainerLifecycle(ctx context.Context,
	expected sandbox.DockerContainerLifecycleLease,
) error {
	if expected.Validate() != nil {
		return apperror.New(apperror.CodeInvalidArgument, "Docker lifecycle fence is invalid")
	}
	record, err := getDockerContainerLifecycle(ctx, s.db, expected.IntentID)
	if err != nil {
		return err
	}
	if record.Receipt != nil {
		return apperror.New(apperror.CodeConflict, "Docker lifecycle is already complete")
	}
	return requireCurrentDockerContainerLifecycleLease(record.Lease, expected, time.Now().UTC())
}

func (s *SQLiteStore) PrepareDockerContainerLifecycleAction(ctx context.Context,
	action sandbox.DockerContainerLifecyclePreparedAction,
	expected sandbox.DockerContainerLifecycleLease,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	if action.Validate() != nil || expected.Validate() != nil ||
		action.IntentID != expected.IntentID || action.LeaseID != expected.LeaseID ||
		action.OwnerID != expected.OwnerID || action.LeaseGeneration != expected.Generation ||
		action.ResourceGeneration != expected.ResourceGeneration {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle action binding is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getDockerContainerLifecycle(ctx, tx, action.IntentID)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, record.Intent.RunID); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := requireCurrentDockerContainerLifecycleLease(record.Lease, expected,
		time.Now().UTC()); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	for _, existing := range record.Actions {
		if existing.LeaseGeneration == action.LeaseGeneration && existing.Verb == action.Verb {
			if existing.ActionFingerprint != action.ActionFingerprint {
				return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
					apperror.CodeConflict, "Docker lifecycle action is already immutable")
			}
			if err := tx.Commit(); err != nil {
				return sandbox.DockerContainerLifecycleRecord{}, false, err
			}
			return record, true, nil
		}
	}
	if action.Ordinal != len(record.Actions)+1 {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeConflict, "Docker lifecycle action ordinal changed")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_lifecycle_actions
		(intent_id, ordinal, lease_id, owner_id, lease_generation, resource_generation,
		verb, action_fingerprint, prepared_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		action.IntentID, action.Ordinal, action.LeaseID, action.OwnerID,
		action.LeaseGeneration, action.ResourceGeneration, action.Verb,
		action.ActionFingerprint, ts(action.PreparedAt)); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := appendDockerContainerLifecycleEvent(ctx, tx, record.Intent,
		events.SandboxDockerLifecycleActionPreparedEvent, action.PreparedAt, map[string]any{
			"ordinal": action.Ordinal, "verb": action.Verb,
			"resource_generation": action.ResourceGeneration,
			"lease_generation":    action.LeaseGeneration,
		}); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	record.Actions = append(record.Actions, action)
	return record, false, record.Validate()
}

func (s *SQLiteStore) AppendDockerContainerLifecycleTransition(ctx context.Context,
	transition sandbox.DockerContainerLifecycleTransition,
	expected sandbox.DockerContainerLifecycleLease,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	if transition.Validate() != nil || expected.Validate() != nil ||
		transition.IntentID != expected.IntentID || transition.LeaseID != expected.LeaseID ||
		transition.OwnerID != expected.OwnerID ||
		transition.LeaseGeneration != expected.Generation ||
		transition.ResourceGeneration != expected.ResourceGeneration {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle transition binding is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getDockerContainerLifecycle(ctx, tx, transition.IntentID)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, record.Intent.RunID); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := requireCurrentDockerContainerLifecycleLease(record.Lease, expected,
		time.Now().UTC()); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	for _, existing := range record.Transitions {
		if existing.State == transition.State &&
			transition.State != sandbox.DockerContainerLifecycleTransitionFailed {
			if existing.TransitionFingerprint != transition.TransitionFingerprint {
				return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
					apperror.CodeConflict, "Docker lifecycle state is already immutable")
			}
			if err := tx.Commit(); err != nil {
				return sandbox.DockerContainerLifecycleRecord{}, false, err
			}
			return record, true, nil
		}
	}
	if transition.Ordinal != len(record.Transitions)+1 {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeConflict, "Docker lifecycle transition ordinal changed")
	}
	var exitCode any
	if transition.ExitCode != nil {
		exitCode = *transition.ExitCode
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_lifecycle_transitions
		(intent_id, ordinal, lease_id, owner_id, lease_generation, resource_generation,
		state, reason_code, exit_code, container_id_fingerprint, previous_fingerprint,
		transition_fingerprint, recorded_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		transition.IntentID, transition.Ordinal, transition.LeaseID, transition.OwnerID,
		transition.LeaseGeneration, transition.ResourceGeneration, transition.State,
		transition.ReasonCode, exitCode, nullableString(transition.ContainerIDFingerprint),
		nullableString(transition.PreviousFingerprint), transition.TransitionFingerprint,
		ts(transition.RecordedAt)); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := appendDockerContainerLifecycleEvent(ctx, tx, record.Intent,
		events.SandboxDockerLifecycleTransitionEvent, transition.RecordedAt, map[string]any{
			"ordinal": transition.Ordinal, "state": transition.State,
			"reason_code":         transition.ReasonCode,
			"resource_generation": transition.ResourceGeneration,
			"lease_generation":    transition.LeaseGeneration,
		}); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	record.Transitions = append(record.Transitions, transition)
	return record, false, record.Validate()
}

func (s *SQLiteStore) CompleteDockerContainerLifecycle(ctx context.Context,
	receipt sandbox.DockerContainerLifecycleReceipt,
	expected sandbox.DockerContainerLifecycleLease,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	if receipt.Validate() != nil || expected.Validate() != nil ||
		receipt.IntentID != expected.IntentID || receipt.LeaseID != expected.LeaseID ||
		receipt.OwnerID != expected.OwnerID || receipt.LeaseGeneration != expected.Generation ||
		receipt.ResourceGeneration != expected.ResourceGeneration {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle receipt binding is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getDockerContainerLifecycle(ctx, tx, receipt.IntentID)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := acquireSandboxManifestWriteLock(ctx, tx, record.Intent.RunID); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := requireCurrentDockerContainerLifecycleLease(record.Lease, expected,
		time.Now().UTC()); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if record.Receipt != nil {
		if record.Receipt.CleanupFingerprint != receipt.CleanupFingerprint {
			return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
				apperror.CodeConflict, "Docker lifecycle cleanup receipt is already immutable")
		}
		if err := tx.Commit(); err != nil {
			return sandbox.DockerContainerLifecycleRecord{}, false, err
		}
		return record, true, nil
	}
	if len(record.Transitions) == 0 || record.Transitions[len(record.Transitions)-1].State !=
		sandbox.DockerContainerLifecycleTransitionCleaned ||
		receipt.FinalTransitionFingerprint !=
			record.Transitions[len(record.Transitions)-1].TransitionFingerprint {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "Docker lifecycle is not durably cleaned")
	}
	var exitCode any
	if receipt.ExitCode != nil {
		exitCode = *receipt.ExitCode
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_lifecycle_cleanup_receipts
		(intent_id, lease_id, owner_id, lease_generation, resource_generation,
		final_transition_fingerprint, container_id_fingerprint, outcome, exit_code,
		container_removed_now, container_already_absent, cleanup_fingerprint, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, receipt.IntentID, receipt.LeaseID,
		receipt.OwnerID, receipt.LeaseGeneration, receipt.ResourceGeneration,
		receipt.FinalTransitionFingerprint, nullableString(receipt.ContainerIDFingerprint),
		receipt.Outcome, exitCode, boolInt(receipt.ContainerRemovedNow),
		boolInt(receipt.ContainerAlreadyAbsent), receipt.CleanupFingerprint,
		ts(receipt.CompletedAt)); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := appendDockerContainerLifecycleEvent(ctx, tx, record.Intent,
		events.SandboxDockerLifecycleCompletedEvent, receipt.CompletedAt, map[string]any{
			"outcome": receipt.Outcome, "resource_generation": receipt.ResourceGeneration,
			"lease_generation":         receipt.LeaseGeneration,
			"container_removed_now":    receipt.ContainerRemovedNow,
			"container_already_absent": receipt.ContainerAlreadyAbsent,
		}); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	copy := receipt
	record.Receipt = &copy
	return record, false, record.Validate()
}

func (s *SQLiteStore) GetDockerContainerLifecycle(ctx context.Context,
	id string,
) (sandbox.DockerContainerLifecycleRecord, error) {
	return getDockerContainerLifecycle(ctx, s.db, strings.TrimSpace(id))
}

// GetDockerContainerLifecycleByOperation resolves idempotent preparation before
// a caller assembles a new intent ID and timestamp. This prevents an exact
// BeginAndRun retry from conflicting merely because those generated values are
// different while retaining BeginDockerContainerLifecycle's strict immutable
// replay checks for direct callers.
func (s *SQLiteStore) GetDockerContainerLifecycleByOperation(ctx context.Context,
	operationKeyDigest string,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	operationKeyDigest = strings.TrimSpace(operationKeyDigest)
	if !validStoreDigest(operationKeyDigest) {
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Docker lifecycle operation digest is invalid")
	}
	return getDockerContainerLifecycleByOperation(ctx, s.db, operationKeyDigest)
}

func (s *SQLiteStore) ListRecoverableDockerContainerLifecycles(ctx context.Context,
	limit int,
) ([]sandbox.DockerContainerLifecycleRecord, error) {
	if limit < 1 || limit > 128 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"Docker lifecycle recovery list limit is invalid")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT intent.id
		FROM sandbox_docker_lifecycle_intents intent
		JOIN sandbox_docker_lifecycle_leases lease ON lease.intent_id = intent.id
		LEFT JOIN sandbox_docker_lifecycle_cleanup_receipts receipt
			ON receipt.intent_id = intent.id
		WHERE receipt.intent_id IS NULL
			AND (lease.status = 'released' OR julianday(lease.expires_at) <= julianday('now'))
		ORDER BY intent.created_at, intent.id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
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
	values := make([]sandbox.DockerContainerLifecycleRecord, 0, len(ids))
	for _, id := range ids {
		value, err := getDockerContainerLifecycle(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func getDockerContainerLifecycle(ctx context.Context, queryer sandboxLifecycleQueryer,
	id string,
) (sandbox.DockerContainerLifecycleRecord, error) {
	intent, err := scanDockerContainerLifecycleIntent(queryer.QueryRowContext(ctx,
		dockerContainerLifecycleIntentSelect+` WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
				apperror.CodeNotFound, "Docker lifecycle intent not found")
		}
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	lease, err := scanDockerContainerLifecycleLease(queryer.QueryRowContext(ctx,
		`SELECT intent_id, resource_generation, lease_id, owner_id, generation, status,
		acquired_at, renewed_at, expires_at, released_at
		FROM sandbox_docker_lifecycle_leases WHERE intent_id = ?`, id))
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	actions, err := listDockerContainerLifecycleActions(ctx, queryer, id)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	transitions, err := listDockerContainerLifecycleTransitions(ctx, queryer, id)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	receipt, found, err := getDockerContainerLifecycleReceipt(ctx, queryer, id)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	record := sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease,
		Actions: actions, Transitions: transitions}
	if found {
		record.Receipt = &receipt
	}
	if err := record.Validate(); err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, fmt.Errorf(
			"stored Docker lifecycle record is invalid: %w", err)
	}
	return record, nil
}

func getDockerContainerLifecycleByOperation(ctx context.Context,
	queryer sandboxLifecycleQueryer, operationKeyDigest string,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	var id string
	err := queryer.QueryRowContext(ctx, `SELECT id FROM sandbox_docker_lifecycle_intents
		WHERE operation_key_digest = ?`, operationKeyDigest).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return sandbox.DockerContainerLifecycleRecord{}, false, nil
	}
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	record, err := getDockerContainerLifecycle(ctx, queryer, id)
	return record, err == nil, err
}

func getDockerContainerLifecycleByPlan(ctx context.Context, queryer sandboxLifecycleQueryer,
	planID string,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	var id string
	err := queryer.QueryRowContext(ctx, `SELECT id FROM sandbox_docker_lifecycle_intents
		WHERE plan_id = ?`, planID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return sandbox.DockerContainerLifecycleRecord{}, false, nil
	}
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	record, err := getDockerContainerLifecycle(ctx, queryer, id)
	return record, err == nil, err
}

func scanDockerContainerLifecycleIntent(row scanner) (sandbox.DockerContainerLaunchIntent, error) {
	var intent sandbox.DockerContainerLaunchIntent
	var productEntry, executionAuthorized, artifactAuthorized int
	var createdAt string
	if err := row.Scan(&intent.ID, &intent.AttemptID, &intent.PlanID, &intent.RunID,
		&intent.MissionID, &intent.WorkspaceID, &intent.ProtocolVersion,
		&intent.ResourceGeneration, &intent.OperationKeyDigest, &intent.RequestFingerprint,
		&intent.SpecFingerprint, &intent.PlanFingerprint, &intent.AuthorityFingerprint,
		&intent.BaseLabelPlanFingerprint, &intent.OwnershipLabelFingerprint,
		&intent.ContainerNameFingerprint, &intent.EndpointClass, &intent.EndpointFingerprint,
		&intent.IntentFingerprint, &productEntry, &executionAuthorized, &artifactAuthorized,
		&intent.RequestedBy, &createdAt); err != nil {
		return sandbox.DockerContainerLaunchIntent{}, err
	}
	intent.ProductEntryEnabled = productEntry != 0
	intent.ExecutionAuthorized = executionAuthorized != 0
	intent.ArtifactCommitAuthorized = artifactAuthorized != 0
	intent.CreatedAt = parseTS(createdAt)
	return intent, intent.Validate()
}

func scanDockerContainerLifecycleLease(row scanner) (sandbox.DockerContainerLifecycleLease, error) {
	var lease sandbox.DockerContainerLifecycleLease
	var acquiredAt, renewedAt, expiresAt string
	var releasedAt sql.NullString
	if err := row.Scan(&lease.IntentID, &lease.ResourceGeneration, &lease.LeaseID,
		&lease.OwnerID, &lease.Generation, &lease.Status, &acquiredAt, &renewedAt,
		&expiresAt, &releasedAt); err != nil {
		return sandbox.DockerContainerLifecycleLease{}, err
	}
	lease.AcquiredAt, lease.RenewedAt, lease.ExpiresAt = parseTS(acquiredAt),
		parseTS(renewedAt), parseTS(expiresAt)
	if releasedAt.Valid {
		value := parseTS(releasedAt.String)
		lease.ReleasedAt = &value
	}
	return lease, lease.Validate()
}

func listDockerContainerLifecycleActions(ctx context.Context, queryer sandboxLifecycleQueryer,
	intentID string,
) ([]sandbox.DockerContainerLifecyclePreparedAction, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT intent_id, ordinal, lease_id, owner_id,
		lease_generation, resource_generation, verb, action_fingerprint, prepared_at
		FROM sandbox_docker_lifecycle_actions WHERE intent_id = ? ORDER BY ordinal`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []sandbox.DockerContainerLifecyclePreparedAction{}
	for rows.Next() {
		var action sandbox.DockerContainerLifecyclePreparedAction
		var preparedAt string
		if err := rows.Scan(&action.IntentID, &action.Ordinal, &action.LeaseID,
			&action.OwnerID, &action.LeaseGeneration, &action.ResourceGeneration,
			&action.Verb, &action.ActionFingerprint, &preparedAt); err != nil {
			return nil, err
		}
		action.State = sandbox.DockerContainerLifecycleActionPrepared
		action.PreparedAt = parseTS(preparedAt)
		if err := action.Validate(); err != nil {
			return nil, err
		}
		values = append(values, action)
	}
	return values, rows.Err()
}

func listDockerContainerLifecycleTransitions(ctx context.Context,
	queryer sandboxLifecycleQueryer, intentID string,
) ([]sandbox.DockerContainerLifecycleTransition, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT intent_id, ordinal, lease_id, owner_id,
		lease_generation, resource_generation, state, reason_code, exit_code,
		container_id_fingerprint, previous_fingerprint, transition_fingerprint, recorded_at
		FROM sandbox_docker_lifecycle_transitions WHERE intent_id = ? ORDER BY ordinal`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []sandbox.DockerContainerLifecycleTransition{}
	for rows.Next() {
		var transition sandbox.DockerContainerLifecycleTransition
		var exitCode sql.NullInt64
		var containerID, previous sql.NullString
		var recordedAt string
		if err := rows.Scan(&transition.IntentID, &transition.Ordinal, &transition.LeaseID,
			&transition.OwnerID, &transition.LeaseGeneration, &transition.ResourceGeneration,
			&transition.State, &transition.ReasonCode, &exitCode, &containerID, &previous,
			&transition.TransitionFingerprint, &recordedAt); err != nil {
			return nil, err
		}
		if exitCode.Valid {
			value := int(exitCode.Int64)
			transition.ExitCode = &value
		}
		transition.ContainerIDFingerprint, transition.PreviousFingerprint =
			containerID.String, previous.String
		transition.RecordedAt = parseTS(recordedAt)
		if err := transition.Validate(); err != nil {
			return nil, err
		}
		values = append(values, transition)
	}
	return values, rows.Err()
}

func getDockerContainerLifecycleReceipt(ctx context.Context, queryer sandboxLifecycleQueryer,
	intentID string,
) (sandbox.DockerContainerLifecycleReceipt, bool, error) {
	var receipt sandbox.DockerContainerLifecycleReceipt
	var containerID sql.NullString
	var exitCode sql.NullInt64
	var removedNow, alreadyAbsent int
	var completedAt string
	err := queryer.QueryRowContext(ctx, `SELECT intent_id, lease_id, owner_id,
		lease_generation, resource_generation, final_transition_fingerprint,
		container_id_fingerprint, outcome, exit_code, container_removed_now,
		container_already_absent, cleanup_fingerprint, completed_at
		FROM sandbox_docker_lifecycle_cleanup_receipts WHERE intent_id = ?`, intentID).Scan(
		&receipt.IntentID, &receipt.LeaseID, &receipt.OwnerID, &receipt.LeaseGeneration,
		&receipt.ResourceGeneration, &receipt.FinalTransitionFingerprint, &containerID,
		&receipt.Outcome, &exitCode, &removedNow, &alreadyAbsent,
		&receipt.CleanupFingerprint, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return sandbox.DockerContainerLifecycleReceipt{}, false, nil
	}
	if err != nil {
		return sandbox.DockerContainerLifecycleReceipt{}, false, err
	}
	receipt.ContainerIDFingerprint = containerID.String
	if exitCode.Valid {
		value := int(exitCode.Int64)
		receipt.ExitCode = &value
	}
	receipt.ContainerRemovedNow, receipt.ContainerAlreadyAbsent = removedNow != 0, alreadyAbsent != 0
	receipt.CompletedAt = parseTS(completedAt)
	return receipt, true, receipt.Validate()
}

func insertDockerContainerLifecycleIntentTx(ctx context.Context, tx *sql.Tx,
	intent sandbox.DockerContainerLaunchIntent,
) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sandbox_docker_lifecycle_intents
		(id, attempt_id, plan_id, run_id, mission_id, workspace_id, protocol_version,
		resource_generation, operation_key_digest, request_fingerprint, spec_fingerprint,
		plan_fingerprint, authority_fingerprint, base_label_plan_fingerprint,
		ownership_label_fingerprint, container_name_fingerprint, endpoint_class,
		endpoint_fingerprint, intent_fingerprint, product_entry_enabled,
		execution_authorized, artifact_commit_authorized, requested_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		intent.ID, intent.AttemptID, intent.PlanID, intent.RunID, intent.MissionID,
		intent.WorkspaceID, intent.ProtocolVersion, intent.ResourceGeneration,
		intent.OperationKeyDigest, intent.RequestFingerprint, intent.SpecFingerprint,
		intent.PlanFingerprint, intent.AuthorityFingerprint, intent.BaseLabelPlanFingerprint,
		intent.OwnershipLabelFingerprint, intent.ContainerNameFingerprint,
		intent.EndpointClass, intent.EndpointFingerprint, intent.IntentFingerprint,
		boolInt(intent.ProductEntryEnabled), boolInt(intent.ExecutionAuthorized),
		boolInt(intent.ArtifactCommitAuthorized), intent.RequestedBy, ts(intent.CreatedAt))
	return err
}

func validateDockerContainerLaunchIntentCurrentTx(ctx context.Context, tx *sql.Tx,
	intent sandbox.DockerContainerLaunchIntent,
) error {
	plan, err := getDockerContainerPlan(ctx, tx, intent.PlanID)
	if err != nil {
		return err
	}
	if intent.RunID != plan.RunID || intent.MissionID != plan.MissionID ||
		intent.WorkspaceID != plan.WorkspaceID || intent.SpecFingerprint != plan.SpecFingerprint ||
		intent.PlanFingerprint != plan.PlanFingerprint ||
		intent.AuthorityFingerprint != plan.AuthorityFingerprint ||
		intent.BaseLabelPlanFingerprint != plan.LabelPlanFingerprint ||
		intent.ContainerNameFingerprint != plan.ContainerNameFingerprint ||
		intent.RequestedBy != plan.RequestedBy || plan.NetworkMode != "disabled" ||
		plan.NetworkTargetCount != 0 || plan.EnvironmentCount != 0 ||
		plan.SecretReferenceCount != 0 || !plan.SimulationOnly || plan.ProductionSubmitted ||
		plan.ProductionVerified || plan.BackendAvailable || plan.BackendEnabled ||
		plan.ExecutionAuthorized || plan.ArtifactCommitAuthorized {
		return apperror.New(apperror.CodeConflict,
			"Docker lifecycle intent does not match the current v54 plan")
	}
	return nil
}

func requireCurrentDockerContainerLifecycleLease(current,
	expected sandbox.DockerContainerLifecycleLease, now time.Time,
) error {
	if !current.Fences(expected, now) {
		return apperror.New(apperror.CodeConflict,
			"Docker lifecycle lease expired or was replaced")
	}
	return nil
}

func appendDockerContainerLifecycleEvent(ctx context.Context, tx *sql.Tx,
	intent sandbox.DockerContainerLaunchIntent, eventType string, createdAt time.Time,
	payload map[string]any,
) error {
	event, err := events.New(intent.RunID, intent.MissionID, eventType,
		"sandbox_docker_container_lifecycle", intent.ID, payload)
	if err != nil {
		return err
	}
	event.CreatedAt = createdAt
	_, err = insertRunEventTx(ctx, tx, event)
	return err
}

func validDockerLifecycleOwner(value string) bool {
	return domain.ValidAgentID(value) && !strings.ContainsRune(value, 0) &&
		redact.String(value) == value
}
