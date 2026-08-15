package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
)

type BrowserLaunchPreparationRecord struct {
	Attempt   browserruntime.BrowserLaunchAttempt
	Lease     browserruntime.BrowserLaunchLease
	MissionID string
	Replayed  bool
}

type BrowserLaunchReviewRecord struct {
	Review        browserruntime.BrowserLaunchReview
	MissionID     string
	EventSequence int64
	Replayed      bool
}

// PrepareBrowserLaunch persists one immutable attempt and its first
// generation-fenced lease. It never invokes a lifecycle adapter.
func (s *SQLiteStore) PrepareBrowserLaunch(ctx context.Context,
	session browserruntime.SessionPlan, identity browserruntime.BrowserExecutableIdentity,
	acceptance browserruntime.BrowserAcceptanceCandidate,
	ownership browserruntime.ProfileOwnershipPlan, operationKey string,
	leaseOwnerIdentity string,
) (browserruntime.BrowserLaunchAttempt, browserruntime.BrowserLaunchLease, bool, error) {
	if s == nil || s.db == nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{},
			false, errors.New("sqlite store is not open")
	}
	if err := session.Validate(); err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	if err := browserruntime.ValidateProfileOwnershipPlan(ownership, session, identity); err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	if err := browserruntime.ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	operationKey = strings.TrimSpace(operationKey)
	leaseOwnerIdentity = strings.TrimSpace(leaseOwnerIdentity)
	if !validBrowserLaunchStoreIdentity(operationKey) ||
		!validBrowserLaunchStoreIdentity(leaseOwnerIdentity) {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{},
			false, errors.New("browser launch preparation identity is invalid")
	}
	keyDigest := browserLaunchStoreDigest("browser-launch-preparation-operation.v1", operationKey)
	ownerDigest := browserLaunchStoreDigest("browser-launch-lease-owner-request.v1",
		leaseOwnerIdentity)
	requestFingerprint := browserLaunchStoreDigest("browser-launch-preparation-request.v1",
		session.Fingerprint, identity.Fingerprint, acceptance.Fingerprint, ownership.Fingerprint,
		ownerDigest)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if record, found, loadErr := loadBrowserLaunchPreparationOperationTx(ctx, tx, keyDigest); loadErr != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, loadErr
	} else if found {
		if record.requestFingerprint != requestFingerprint {
			return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{},
				false, errors.New("browser launch preparation operation key was reused with another request")
		}
		replayed, loadErr := loadBrowserLaunchPreparationTx(ctx, tx,
			record.attemptID, record.leaseID)
		if loadErr != nil {
			return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, loadErr
		}
		if err := browserruntime.ValidateBrowserLaunchAttempt(replayed.Attempt, session,
			identity, acceptance, ownership); err != nil {
			return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
		}
		return replayed.Attempt, replayed.Lease, true, nil
	}
	missionID, err := validateBrowserLaunchRunBindingTx(ctx, tx, session)
	if err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	now := time.Now().UTC()
	attempt, err := browserruntime.BuildBrowserLaunchAttempt(session, identity, acceptance,
		ownership, idgen.New("browser_attempt"), now)
	if err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	lease, err := browserruntime.BuildBrowserLaunchLease(attempt,
		idgen.New("browser_lease"), leaseOwnerIdentity, now, 30*time.Second)
	if err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	attemptJSON, err := json.Marshal(attempt)
	if err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	leaseJSON, err := json.Marshal(lease)
	if err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_launch_attempts
		(id, run_id, mission_id, workspace_id, session_id, fingerprint, generation,
			payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.ID, attempt.RunID, missionID, attempt.WorkspaceID, attempt.SessionID,
		attempt.Fingerprint, attempt.Generation, string(attemptJSON), ts(attempt.CreatedAt)); err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{},
			false, fmt.Errorf("insert browser launch attempt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_launch_leases
		(id, attempt_id, fingerprint, generation, status, payload_json, acquired_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, lease.ID, lease.AttemptID, lease.Fingerprint,
		lease.Generation, lease.Status, string(leaseJSON), ts(lease.AcquiredAt),
		ts(lease.ExpiresAt)); err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{},
			false, fmt.Errorf("insert browser launch lease: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_launch_preparation_operations
		(key_digest, request_fingerprint, attempt_id, lease_id, created_at)
		VALUES (?, ?, ?, ?, ?)`, keyDigest, requestFingerprint, attempt.ID, lease.ID,
		ts(attempt.CreatedAt)); err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{},
			false, fmt.Errorf("insert browser launch preparation operation: %w", err)
	}
	if err := appendBrowserLaunchPreparedEventsTx(ctx, tx, missionID, attempt, lease); err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return browserruntime.BrowserLaunchAttempt{}, browserruntime.BrowserLaunchLease{}, false, err
	}
	return attempt, lease, false, nil
}

func (s *SQLiteStore) RecordBrowserLaunchReview(ctx context.Context,
	session browserruntime.SessionPlan, identity browserruntime.BrowserExecutableIdentity,
	acceptance browserruntime.BrowserAcceptanceCandidate,
	ownership browserruntime.ProfileOwnershipPlan,
	attempt browserruntime.BrowserLaunchAttempt, lease browserruntime.BrowserLaunchLease,
	decision browserruntime.BrowserLaunchReviewDecision, operationKey string,
	reviewerIdentity string,
) (browserruntime.BrowserLaunchReview, bool, error) {
	if s == nil || s.db == nil {
		return browserruntime.BrowserLaunchReview{}, false, errors.New("sqlite store is not open")
	}
	if err := browserruntime.ValidateBrowserLaunchAttempt(attempt, session, identity,
		acceptance, ownership); err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	if err := browserruntime.ValidateBrowserLaunchLease(lease, attempt); err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	operationKey = strings.TrimSpace(operationKey)
	reviewerIdentity = strings.TrimSpace(reviewerIdentity)
	if !validBrowserLaunchStoreIdentity(operationKey) ||
		!validBrowserLaunchStoreIdentity(reviewerIdentity) {
		return browserruntime.BrowserLaunchReview{}, false, errors.New("browser launch review identity is invalid")
	}
	keyDigest := browserLaunchStoreDigest("browser-launch-review-operation.v1", operationKey)
	reviewerDigest := browserLaunchStoreDigest("browser-launch-reviewer-request.v1",
		reviewerIdentity)
	requestFingerprint := browserLaunchStoreDigest("browser-launch-review-request.v1",
		attempt.Fingerprint, lease.Fingerprint, string(decision), reviewerDigest)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if record, found, loadErr := loadBrowserLaunchReviewOperationTx(ctx, tx, keyDigest); loadErr != nil {
		return browserruntime.BrowserLaunchReview{}, false, loadErr
	} else if found {
		if record.requestFingerprint != requestFingerprint {
			return browserruntime.BrowserLaunchReview{}, false, errors.New("browser launch review operation key was reused with another request")
		}
		replayed, loadErr := loadBrowserLaunchReviewTx(ctx, tx, record.reviewID)
		if loadErr != nil {
			return browserruntime.BrowserLaunchReview{}, false, loadErr
		}
		if err := browserruntime.ValidateBrowserLaunchReview(replayed.Review, session, identity,
			acceptance, ownership, attempt, lease); err != nil {
			return browserruntime.BrowserLaunchReview{}, false, err
		}

		return replayed.Review, true, nil
	}
	missionID, err := validateBrowserLaunchRunBindingTx(ctx, tx, session)
	if err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	stored, err := loadBrowserLaunchPreparationTx(ctx, tx, attempt.ID, lease.ID)
	if err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	if stored.Attempt.Fingerprint != attempt.Fingerprint ||
		stored.Lease.Fingerprint != lease.Fingerprint || stored.MissionID != missionID {
		return browserruntime.BrowserLaunchReview{}, false, errors.New("browser launch review preparation binding changed")
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM browser_launch_reviews
		WHERE attempt_id = ?`, attempt.ID).Scan(&existing); err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	if existing != 0 {
		return browserruntime.BrowserLaunchReview{}, false, errors.New("browser launch attempt already has an immutable review")
	}
	var auditParent string
	err = tx.QueryRowContext(ctx, `SELECT fingerprint FROM browser_launch_reviews
		WHERE run_id = ? ORDER BY event_sequence DESC LIMIT 1`, session.RunID).Scan(&auditParent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	now := time.Now().UTC()
	review, err := browserruntime.BuildBrowserLaunchReview(session, identity, acceptance,
		ownership, attempt, lease, idgen.New("browser_review"), decision, reviewerIdentity,
		operationKey, auditParent, now)
	if err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	event, err := newBrowserLaunchReviewEvent(session.RunID, missionID, review)
	if err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	event.CreatedAt = review.CreatedAt
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	reviewJSON, err := json.Marshal(review)
	if err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_launch_reviews
		(id, attempt_id, lease_id, run_id, mission_id, workspace_id, decision,
			fingerprint, audit_parent_fingerprint, reviewer_sha256, event_sequence,
			payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		review.ID, attempt.ID, lease.ID, session.RunID, missionID, session.WorkspaceID,
		review.Decision, review.Fingerprint, review.AuditParentFingerprint,
		review.ReviewerSHA256, event.Sequence, string(reviewJSON), ts(review.CreatedAt)); err != nil {
		return browserruntime.BrowserLaunchReview{}, false, fmt.Errorf("insert browser launch review: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_launch_review_operations
		(key_digest, request_fingerprint, review_id, created_at) VALUES (?, ?, ?, ?)`,
		keyDigest, requestFingerprint, review.ID, ts(review.CreatedAt)); err != nil {
		return browserruntime.BrowserLaunchReview{}, false, fmt.Errorf("insert browser launch review operation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return browserruntime.BrowserLaunchReview{}, false, err
	}
	return review, false, nil
}

func validateBrowserLaunchRunBindingTx(ctx context.Context, tx *sql.Tx,
	session browserruntime.SessionPlan,
) (string, error) {
	var missionID string
	var status string
	err := tx.QueryRowContext(ctx, `SELECT run.mission_id, run.status FROM runs run
		JOIN missions mission ON mission.id = run.mission_id
		JOIN sessions session_record ON session_record.id = run.session_id
		WHERE run.id = ? AND run.session_id = ? AND mission.workspace_id = ?
			AND session_record.workspace_id = ?`,
		session.RunID, session.SessionID, session.WorkspaceID, session.WorkspaceID).
		Scan(&missionID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("browser launch session is not bound to the exact Run and Workspace")
	}
	if err != nil {
		return "", err
	}
	switch status {
	case "created", "preparing", "running", "waiting_approval", "paused":
	default:
		return "", errors.New("browser launch cannot be prepared or reviewed for a terminal Run")
	}
	return missionID, nil
}

func appendBrowserLaunchPreparedEventsTx(ctx context.Context, tx *sql.Tx, missionID string,
	attempt browserruntime.BrowserLaunchAttempt, lease browserruntime.BrowserLaunchLease,
) error {
	attemptEvent, err := events.New(attempt.RunID, missionID,
		events.BrowserLaunchAttemptPreparedEvent, "browser_runtime", attempt.ID, map[string]any{
			"attempt_id": attempt.ID, "attempt_fingerprint": attempt.Fingerprint,
			"scope_fingerprint":  attempt.ScopeFingerprint,
			"budget_fingerprint": attempt.BudgetFingerprint,
			"profile_generation": attempt.ProfileGeneration,
			"required_backend":   attempt.RequiredBackend,
			"start_blocked":      true, "process_start_authorized": false,
			"network_authorized": false, "profile_write_authorized": false,
			"artifact_commit_authorized": false,
		})
	if err != nil {
		return err
	}
	attemptEvent.CreatedAt = attempt.CreatedAt
	if _, err := insertRunEventTx(ctx, tx, attemptEvent); err != nil {
		return err
	}
	leaseEvent, err := events.New(attempt.RunID, missionID,
		events.BrowserLaunchLeaseRecordedEvent, "browser_runtime", lease.ID, map[string]any{
			"attempt_id": attempt.ID, "attempt_fingerprint": attempt.Fingerprint,
			"lease_fingerprint": lease.Fingerprint, "generation": lease.Generation,
			"expires_at": lease.ExpiresAt, "start_blocked": true,
			"process_execution_authorized":   false,
			"process_termination_authorized": false,
		})
	if err != nil {
		return err
	}
	leaseEvent.CreatedAt = lease.AcquiredAt
	_, err = insertRunEventTx(ctx, tx, leaseEvent)
	return err
}

func newBrowserLaunchReviewEvent(runID string, missionID string,
	review browserruntime.BrowserLaunchReview,
) (events.Event, error) {
	return events.New(runID, missionID, events.BrowserLaunchReviewedEvent,
		"browser_runtime", review.ID, map[string]any{
			"review_id": review.ID, "attempt_id": review.AttemptID,
			"review_fingerprint":     review.Fingerprint,
			"acceptance_fingerprint": review.AcceptanceFingerprint,
			"scope_fingerprint":      review.ScopeFingerprint,
			"budget_fingerprint":     review.BudgetFingerprint,
			"profile_generation":     review.ProfileGeneration,
			"lease_generation":       review.LeaseGeneration,
			"required_backend":       review.RequiredBackend, "decision": review.Decision,
			"reason_code":                 review.ReasonCode,
			"audit_parent_fingerprint":    review.AuditParentFingerprint,
			"accepted_for_future_adapter": review.AcceptedForFutureAdapter,
			"start_authorized":            false, "process_execution_authorized": false,
			"network_authorized": false, "profile_write_authorized": false,
			"process_termination_authorized": false,
			"filesystem_cleanup_authorized":  false, "artifact_commit_authorized": false,
		})
}

type browserLaunchPreparationOperation struct {
	requestFingerprint string
	attemptID          string
	leaseID            string
}

func loadBrowserLaunchPreparationOperationTx(ctx context.Context, tx *sql.Tx,
	keyDigest string,
) (browserLaunchPreparationOperation, bool, error) {
	var value browserLaunchPreparationOperation
	err := tx.QueryRowContext(ctx, `SELECT request_fingerprint, attempt_id, lease_id
		FROM browser_launch_preparation_operations WHERE key_digest = ?`, keyDigest).
		Scan(&value.requestFingerprint, &value.attemptID, &value.leaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return browserLaunchPreparationOperation{}, false, nil
	}
	return value, err == nil, err
}

func loadBrowserLaunchPreparationTx(ctx context.Context, tx *sql.Tx,
	attemptID string, leaseID string,
) (BrowserLaunchPreparationRecord, error) {
	var attemptJSON string
	var missionID string
	if err := tx.QueryRowContext(ctx, `SELECT payload_json, mission_id
		FROM browser_launch_attempts WHERE id = ?`, attemptID).
		Scan(&attemptJSON, &missionID); err != nil {
		return BrowserLaunchPreparationRecord{}, err
	}
	var attempt browserruntime.BrowserLaunchAttempt
	if err := json.Unmarshal([]byte(attemptJSON), &attempt); err != nil {
		return BrowserLaunchPreparationRecord{}, err
	}
	if err := browserruntime.ValidateStoredBrowserLaunchAttempt(attempt); err != nil {
		return BrowserLaunchPreparationRecord{}, err
	}
	var leaseJSON string
	if err := tx.QueryRowContext(ctx, `SELECT payload_json FROM browser_launch_leases
		WHERE id = ? AND attempt_id = ?`, leaseID, attemptID).Scan(&leaseJSON); err != nil {
		return BrowserLaunchPreparationRecord{}, err
	}
	var lease browserruntime.BrowserLaunchLease
	if err := json.Unmarshal([]byte(leaseJSON), &lease); err != nil {
		return BrowserLaunchPreparationRecord{}, err
	}
	if err := browserruntime.ValidateBrowserLaunchLease(lease, attempt); err != nil {
		return BrowserLaunchPreparationRecord{}, err
	}
	return BrowserLaunchPreparationRecord{
		Attempt: attempt, Lease: lease, MissionID: missionID,
	}, nil
}

type browserLaunchReviewOperation struct {
	requestFingerprint string
	reviewID           string
}

func loadBrowserLaunchReviewOperationTx(ctx context.Context, tx *sql.Tx,
	keyDigest string,
) (browserLaunchReviewOperation, bool, error) {
	var value browserLaunchReviewOperation
	err := tx.QueryRowContext(ctx, `SELECT request_fingerprint, review_id
		FROM browser_launch_review_operations WHERE key_digest = ?`, keyDigest).
		Scan(&value.requestFingerprint, &value.reviewID)
	if errors.Is(err, sql.ErrNoRows) {
		return browserLaunchReviewOperation{}, false, nil
	}
	return value, err == nil, err
}

func loadBrowserLaunchReviewTx(ctx context.Context, tx *sql.Tx,
	reviewID string,
) (BrowserLaunchReviewRecord, error) {
	var payload string
	var missionID string
	var sequence int64
	if err := tx.QueryRowContext(ctx, `SELECT payload_json, mission_id, event_sequence
		FROM browser_launch_reviews WHERE id = ?`, reviewID).
		Scan(&payload, &missionID, &sequence); err != nil {
		return BrowserLaunchReviewRecord{}, err
	}
	var review browserruntime.BrowserLaunchReview
	if err := json.Unmarshal([]byte(payload), &review); err != nil {
		return BrowserLaunchReviewRecord{}, err
	}
	if err := browserruntime.ValidateStoredBrowserLaunchReview(review); err != nil {
		return BrowserLaunchReviewRecord{}, err
	}
	return BrowserLaunchReviewRecord{
		Review: review, MissionID: missionID, EventSequence: sequence,
	}, nil
}

func browserLaunchStoreDigest(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func validBrowserLaunchStoreIdentity(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) ||
		utf8.RuneCountInString(value) > 128 || strings.ContainsRune(value, 0) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}
