package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/browserruntime"
)

type BrowserNetworkEvidenceRecord struct {
	Evidence browserruntime.BrowserNetworkContainmentEvidence
	Replayed bool
}

type BrowserNetworkReviewRecord struct {
	Review   browserruntime.BrowserNetworkContainmentReview
	Replayed bool
}

// RecordBrowserNetworkEvidence persists one immutable production containment
// evidence payload keyed by an idempotent operation key. It never runs a probe
// and never authorizes a launch.
func (s *SQLiteStore) RecordBrowserNetworkEvidence(ctx context.Context,
	evidence browserruntime.BrowserNetworkContainmentEvidence, operationKey string,
) (BrowserNetworkEvidenceRecord, error) {
	if s == nil || s.db == nil {
		return BrowserNetworkEvidenceRecord{}, errors.New("sqlite store is not open")
	}
	if err := validateStoredBrowserNetworkEvidence(evidence); err != nil {
		return BrowserNetworkEvidenceRecord{}, err
	}
	operationKey = strings.TrimSpace(operationKey)
	if !validBrowserLaunchStoreIdentity(operationKey) {
		return BrowserNetworkEvidenceRecord{}, errors.New("browser network evidence operation key is invalid")
	}
	keyDigest := browserLaunchStoreDigest("browser-network-evidence-operation.v1", operationKey)
	requestFingerprint := browserLaunchStoreDigest("browser-network-evidence-request.v1",
		evidence.Fingerprint)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return BrowserNetworkEvidenceRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var existingEvidenceID string
	var existingFingerprint string
	err = tx.QueryRowContext(ctx, `SELECT evidence_id, request_fingerprint
		FROM browser_network_evidence_operations WHERE key_digest = ?`, keyDigest).
		Scan(&existingEvidenceID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != requestFingerprint {
			return BrowserNetworkEvidenceRecord{}, errors.New("browser network evidence operation key was reused with another payload")
		}
		replayed, loadErr := loadBrowserNetworkEvidenceByIDTx(ctx, tx, existingEvidenceID)
		if loadErr != nil {
			return BrowserNetworkEvidenceRecord{}, loadErr
		}
		replayed.Replayed = true
		return replayed, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BrowserNetworkEvidenceRecord{}, err
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return BrowserNetworkEvidenceRecord{}, err
	}
	passed := 0
	if evidence.Passed {
		passed = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_network_evidences
		(id, fingerprint, executable_identity_fingerprint, acceptance_fingerprint,
			adapter, policy_version, collector_identity, passed, payload_json,
			completed_at, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		evidence.ID, evidence.Fingerprint, evidence.ExecutableIdentityFingerprint,
		evidence.AcceptanceFingerprint, evidence.Adapter, evidence.PolicyVersion,
		evidence.CollectorIdentity, passed, string(evidenceJSON),
		ts(evidence.CompletedAt), ts(evidence.ExpiresAt), ts(evidence.CompletedAt)); err != nil {
		return BrowserNetworkEvidenceRecord{}, fmt.Errorf("insert browser network evidence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_network_evidence_operations
		(key_digest, request_fingerprint, evidence_id, created_at) VALUES (?, ?, ?, ?)`,
		keyDigest, requestFingerprint, evidence.ID, ts(evidence.CompletedAt)); err != nil {
		return BrowserNetworkEvidenceRecord{}, fmt.Errorf("insert browser network evidence operation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return BrowserNetworkEvidenceRecord{}, err
	}
	return BrowserNetworkEvidenceRecord{Evidence: evidence}, nil
}

// RecordBrowserNetworkReview persists one immutable operator review of a stored
// evidence payload keyed by an idempotent operation key.
func (s *SQLiteStore) RecordBrowserNetworkReview(ctx context.Context,
	review browserruntime.BrowserNetworkContainmentReview, operationKey string,
) (BrowserNetworkReviewRecord, error) {
	if s == nil || s.db == nil {
		return BrowserNetworkReviewRecord{}, errors.New("sqlite store is not open")
	}
	if err := validateStoredBrowserNetworkReview(review); err != nil {
		return BrowserNetworkReviewRecord{}, err
	}
	operationKey = strings.TrimSpace(operationKey)
	if !validBrowserLaunchStoreIdentity(operationKey) {
		return BrowserNetworkReviewRecord{}, errors.New("browser network review operation key is invalid")
	}
	keyDigest := browserLaunchStoreDigest("browser-network-review-operation.v1", operationKey)
	requestFingerprint := browserLaunchStoreDigest("browser-network-review-request.v1",
		review.Fingerprint)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return BrowserNetworkReviewRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var existingReviewID string
	var existingFingerprint string
	err = tx.QueryRowContext(ctx, `SELECT review_id, request_fingerprint
		FROM browser_network_review_operations WHERE key_digest = ?`, keyDigest).
		Scan(&existingReviewID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != requestFingerprint {
			return BrowserNetworkReviewRecord{}, errors.New("browser network review operation key was reused with another payload")
		}
		replayed, loadErr := loadBrowserNetworkReviewByIDTx(ctx, tx, existingReviewID)
		if loadErr != nil {
			return BrowserNetworkReviewRecord{}, loadErr
		}
		replayed.Replayed = true
		return replayed, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BrowserNetworkReviewRecord{}, err
	}
	var evidenceExists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM browser_network_evidences
		WHERE fingerprint = ?`, review.EvidenceFingerprint).Scan(&evidenceExists); err != nil {
		return BrowserNetworkReviewRecord{}, err
	}
	if evidenceExists == 0 {
		return BrowserNetworkReviewRecord{}, errors.New("browser network review references an unknown evidence")
	}
	reviewJSON, err := json.Marshal(review)
	if err != nil {
		return BrowserNetworkReviewRecord{}, err
	}
	accepted := 0
	if review.Accepted {
		accepted = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_network_reviews
		(id, fingerprint, evidence_fingerprint, reviewer_identity, accepted,
			reason_code, payload_json, reviewed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		review.ID, review.Fingerprint, review.EvidenceFingerprint, review.ReviewerIdentity,
		accepted, review.ReasonCode, string(reviewJSON), ts(review.ReviewedAt),
		ts(review.ReviewedAt)); err != nil {
		return BrowserNetworkReviewRecord{}, fmt.Errorf("insert browser network review: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_network_review_operations
		(key_digest, request_fingerprint, review_id, created_at) VALUES (?, ?, ?, ?)`,
		keyDigest, requestFingerprint, review.ID, ts(review.ReviewedAt)); err != nil {
		return BrowserNetworkReviewRecord{}, fmt.Errorf("insert browser network review operation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return BrowserNetworkReviewRecord{}, err
	}
	return BrowserNetworkReviewRecord{Review: review}, nil
}

// LoadLatestBrowserNetworkEvidence returns the most recent evidence recorded for
// an exact executable identity fingerprint, or sql.ErrNoRows if none exists.
func (s *SQLiteStore) LoadLatestBrowserNetworkEvidence(ctx context.Context,
	executableIdentityFingerprint string,
) (browserruntime.BrowserNetworkContainmentEvidence, error) {
	if s == nil || s.db == nil {
		return browserruntime.BrowserNetworkContainmentEvidence{}, errors.New("sqlite store is not open")
	}
	executableIdentityFingerprint = strings.TrimSpace(executableIdentityFingerprint)
	if len(executableIdentityFingerprint) != 64 {
		return browserruntime.BrowserNetworkContainmentEvidence{}, errors.New("browser network evidence identity fingerprint is invalid")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM browser_network_evidences
		WHERE executable_identity_fingerprint = ?
		ORDER BY completed_at DESC, id DESC LIMIT 1`, executableIdentityFingerprint).Scan(&payload)
	if err != nil {
		return browserruntime.BrowserNetworkContainmentEvidence{}, err
	}
	var evidence browserruntime.BrowserNetworkContainmentEvidence
	if err := json.Unmarshal([]byte(payload), &evidence); err != nil {
		return browserruntime.BrowserNetworkContainmentEvidence{}, err
	}
	if err := validateStoredBrowserNetworkEvidence(evidence); err != nil {
		return browserruntime.BrowserNetworkContainmentEvidence{}, err
	}
	return evidence, nil
}

// LoadBrowserNetworkReview returns the review recorded for an exact evidence
// fingerprint, or sql.ErrNoRows if none exists.
func (s *SQLiteStore) LoadBrowserNetworkReview(ctx context.Context,
	evidenceFingerprint string,
) (browserruntime.BrowserNetworkContainmentReview, error) {
	if s == nil || s.db == nil {
		return browserruntime.BrowserNetworkContainmentReview{}, errors.New("sqlite store is not open")
	}
	evidenceFingerprint = strings.TrimSpace(evidenceFingerprint)
	if len(evidenceFingerprint) != 64 {
		return browserruntime.BrowserNetworkContainmentReview{}, errors.New("browser network review evidence fingerprint is invalid")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM browser_network_reviews
		WHERE evidence_fingerprint = ? ORDER BY reviewed_at DESC, id DESC LIMIT 1`,
		evidenceFingerprint).Scan(&payload)
	if err != nil {
		return browserruntime.BrowserNetworkContainmentReview{}, err
	}
	var review browserruntime.BrowserNetworkContainmentReview
	if err := json.Unmarshal([]byte(payload), &review); err != nil {
		return browserruntime.BrowserNetworkContainmentReview{}, err
	}
	if err := validateStoredBrowserNetworkReview(review); err != nil {
		return browserruntime.BrowserNetworkContainmentReview{}, err
	}
	return review, nil
}

func validateStoredBrowserNetworkEvidence(evidence browserruntime.BrowserNetworkContainmentEvidence) error {
	if evidence.ProtocolVersion != browserruntime.BrowserNetworkContainmentEvidenceProtocolVersion ||
		evidence.Adapter != browserruntime.WindowsWFPBrowserContainmentAdapterName ||
		evidence.PolicyVersion != browserruntime.BrowserNetworkContainmentPolicyVersion ||
		evidence.ID == "" || len(evidence.Fingerprint) != 64 ||
		evidence.ExecutableIdentityFingerprint == "" || evidence.AcceptanceFingerprint == "" ||
		evidence.CollectorIdentity == "" || evidence.StartedAt.IsZero() ||
		evidence.CompletedAt.IsZero() || !evidence.CompletedAt.After(evidence.StartedAt) ||
		evidence.ExpiresAt.IsZero() || !evidence.ExpiresAt.After(evidence.CompletedAt) {
		return errors.New("browser network evidence lost an immutable production boundary")
	}
	return nil
}

func validateStoredBrowserNetworkReview(review browserruntime.BrowserNetworkContainmentReview) error {
	if review.ProtocolVersion != browserruntime.BrowserNetworkContainmentReviewProtocolVersion ||
		review.ID == "" || len(review.Fingerprint) != 64 ||
		review.EvidenceFingerprint == "" || review.ReviewerIdentity == "" ||
		review.ReviewedAt.IsZero() {
		return errors.New("browser network review lost an immutable operator boundary")
	}
	expectedReason := "operator_rejected"
	if review.Accepted {
		expectedReason = "production_probe_confirmed"
	}
	if review.ReasonCode != expectedReason {
		return errors.New("browser network review acceptance and reason disagree")
	}
	return nil
}

func loadBrowserNetworkEvidenceByIDTx(ctx context.Context, tx *sql.Tx, id string) (BrowserNetworkEvidenceRecord, error) {
	var payload string
	if err := tx.QueryRowContext(ctx, `SELECT payload_json FROM browser_network_evidences
		WHERE id = ?`, id).Scan(&payload); err != nil {
		return BrowserNetworkEvidenceRecord{}, err
	}
	var evidence browserruntime.BrowserNetworkContainmentEvidence
	if err := json.Unmarshal([]byte(payload), &evidence); err != nil {
		return BrowserNetworkEvidenceRecord{}, err
	}
	if err := validateStoredBrowserNetworkEvidence(evidence); err != nil {
		return BrowserNetworkEvidenceRecord{}, err
	}
	return BrowserNetworkEvidenceRecord{Evidence: evidence}, nil
}

func loadBrowserNetworkReviewByIDTx(ctx context.Context, tx *sql.Tx, id string) (BrowserNetworkReviewRecord, error) {
	var payload string
	if err := tx.QueryRowContext(ctx, `SELECT payload_json FROM browser_network_reviews
		WHERE id = ?`, id).Scan(&payload); err != nil {
		return BrowserNetworkReviewRecord{}, err
	}
	var review browserruntime.BrowserNetworkContainmentReview
	if err := json.Unmarshal([]byte(payload), &review); err != nil {
		return BrowserNetworkReviewRecord{}, err
	}
	if err := validateStoredBrowserNetworkReview(review); err != nil {
		return BrowserNetworkReviewRecord{}, err
	}
	return BrowserNetworkReviewRecord{Review: review}, nil
}
