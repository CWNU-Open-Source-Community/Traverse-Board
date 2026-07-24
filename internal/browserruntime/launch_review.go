package browserruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const BrowserLaunchReviewProtocolVersion = "browser_launch_review.v1"

type BrowserLaunchReviewDecision string

const (
	BrowserLaunchReviewAcceptCandidate BrowserLaunchReviewDecision = "accept_candidate"
	BrowserLaunchReviewRejectCandidate BrowserLaunchReviewDecision = "reject_candidate"
)

type BrowserLaunchReviewReason string

const (
	BrowserLaunchReviewReasonEvidenceConfirmed BrowserLaunchReviewReason = "evidence_confirmed"
	BrowserLaunchReviewReasonOperatorRejected  BrowserLaunchReviewReason = "operator_rejected"
)

// BrowserLaunchReview is independent of discovery and preparation. Accepting
// the evidence only records that a future start adapter may be considered; it
// never grants process, network, profile, cleanup, or artifact authority.
type BrowserLaunchReview struct {
	ProtocolVersion               string                      `json:"protocol_version"`
	ID                            string                      `json:"id"`
	AttemptID                     string                      `json:"attempt_id"`
	AttemptFingerprint            string                      `json:"attempt_fingerprint"`
	LeaseFingerprint              string                      `json:"lease_fingerprint"`
	SessionPlanFingerprint        string                      `json:"session_plan_fingerprint"`
	ExecutableIdentityFingerprint string                      `json:"executable_identity_fingerprint"`
	AcceptanceFingerprint         string                      `json:"acceptance_fingerprint"`
	ProfileOwnershipFingerprint   string                      `json:"profile_ownership_fingerprint"`
	ScopeFingerprint              string                      `json:"scope_fingerprint"`
	BudgetFingerprint             string                      `json:"budget_fingerprint"`
	ProfileGeneration             uint64                      `json:"profile_generation"`
	LeaseGeneration               uint64                      `json:"lease_generation"`
	RequiredBackend               string                      `json:"required_backend"`
	Decision                      BrowserLaunchReviewDecision `json:"decision"`
	ReasonCode                    BrowserLaunchReviewReason   `json:"reason_code"`
	ReviewerSHA256                string                      `json:"reviewer_sha256"`
	OperationKeySHA256            string                      `json:"operation_key_sha256"`
	AuditParentFingerprint        string                      `json:"audit_parent_fingerprint"`
	IndependentReviewerVerified   bool                        `json:"independent_reviewer_verified"`
	PublisherEvidenceConfirmed    bool                        `json:"publisher_evidence_confirmed"`
	ExactScopeConfirmed           bool                        `json:"exact_scope_confirmed"`
	SandboxBackendConfirmed       bool                        `json:"sandbox_backend_confirmed"`
	BudgetConfirmed               bool                        `json:"budget_confirmed"`
	ProfileGenerationConfirmed    bool                        `json:"profile_generation_confirmed"`
	ProcessTreeContractConfirmed  bool                        `json:"process_tree_contract_confirmed"`
	AppendOnlyAuditRequired       bool                        `json:"append_only_audit_required"`
	AcceptedForFutureAdapter      bool                        `json:"accepted_for_future_adapter"`
	StartAuthorized               bool                        `json:"start_authorized"`
	ProcessExecutionAuthorized    bool                        `json:"process_execution_authorized"`
	NetworkAuthorized             bool                        `json:"network_authorized"`
	ProfileWriteAuthorized        bool                        `json:"profile_write_authorized"`
	ProcessTerminationAuthorized  bool                        `json:"process_termination_authorized"`
	FilesystemCleanupAuthorized   bool                        `json:"filesystem_cleanup_authorized"`
	ArtifactCommitAuthorized      bool                        `json:"artifact_commit_authorized"`
	Authority                     RuntimeAuthority            `json:"authority"`
	CreatedAt                     time.Time                   `json:"created_at"`
	Fingerprint                   string                      `json:"fingerprint"`
}

func BuildBrowserLaunchReview(session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease, reviewID string,
	decision BrowserLaunchReviewDecision, reviewerIdentity string, operationKey string,
	auditParentFingerprint string, createdAt time.Time,
) (BrowserLaunchReview, error) {
	if err := ValidateBrowserLaunchAttempt(attempt, session, identity, acceptance, ownership); err != nil {
		return BrowserLaunchReview{}, err
	}
	if err := ValidateBrowserLaunchLease(lease, attempt); err != nil {
		return BrowserLaunchReview{}, err
	}
	reviewerIdentity = strings.TrimSpace(reviewerIdentity)
	operationKey = strings.TrimSpace(operationKey)
	if !validPlanIdentity(reviewID) || !validPlanIdentity(reviewerIdentity) ||
		!validPlanIdentity(operationKey) || createdAt.IsZero() ||
		createdAt.Before(lease.AcquiredAt) || !createdAt.Before(lease.ExpiresAt) {
		return BrowserLaunchReview{}, errors.New("browser launch review identity, operation, or timestamp is invalid")
	}
	if browserLaunchOwnerToken(attempt, reviewerIdentity) == lease.OwnerTokenSHA256 {
		return BrowserLaunchReview{}, errors.New("browser launch review must use an independent operator")
	}
	if auditParentFingerprint != "" && !validSHA256(auditParentFingerprint) {
		return BrowserLaunchReview{}, errors.New("browser launch review audit parent is invalid")
	}
	var reason BrowserLaunchReviewReason
	switch decision {
	case BrowserLaunchReviewAcceptCandidate:
		reason = BrowserLaunchReviewReasonEvidenceConfirmed
	case BrowserLaunchReviewRejectCandidate:
		reason = BrowserLaunchReviewReasonOperatorRejected
	default:
		return BrowserLaunchReview{}, errors.New("browser launch review decision is invalid")
	}
	review := BrowserLaunchReview{
		ProtocolVersion: BrowserLaunchReviewProtocolVersion, ID: reviewID,
		AttemptID: attempt.ID, AttemptFingerprint: attempt.Fingerprint,
		LeaseFingerprint: lease.Fingerprint, SessionPlanFingerprint: session.Fingerprint,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		AcceptanceFingerprint:         acceptance.Fingerprint,
		ProfileOwnershipFingerprint:   ownership.Fingerprint,
		ScopeFingerprint:              session.Scope.Fingerprint,
		BudgetFingerprint:             attempt.BudgetFingerprint,
		ProfileGeneration:             ownership.Generation, LeaseGeneration: lease.Generation,
		RequiredBackend: session.RequiredBackend, Decision: decision, ReasonCode: reason,
		ReviewerSHA256:              browserLaunchReviewIdentityDigest(reviewerIdentity),
		OperationKeySHA256:          browserLaunchReviewOperationDigest(operationKey),
		AuditParentFingerprint:      auditParentFingerprint,
		IndependentReviewerVerified: true, PublisherEvidenceConfirmed: true,
		ExactScopeConfirmed: true, SandboxBackendConfirmed: true, BudgetConfirmed: true,
		ProfileGenerationConfirmed: true, ProcessTreeContractConfirmed: true,
		AppendOnlyAuditRequired:  true,
		AcceptedForFutureAdapter: decision == BrowserLaunchReviewAcceptCandidate,
		CreatedAt:                createdAt.UTC(),
	}
	var err error
	review.Fingerprint, err = browserLaunchReviewFingerprint(review)
	if err != nil {
		return BrowserLaunchReview{}, err
	}
	if err := ValidateBrowserLaunchReview(review, session, identity, acceptance,
		ownership, attempt, lease); err != nil {
		return BrowserLaunchReview{}, err
	}
	return review, nil
}

func ValidateBrowserLaunchReview(review BrowserLaunchReview, session SessionPlan,
	identity BrowserExecutableIdentity, acceptance BrowserAcceptanceCandidate,
	ownership ProfileOwnershipPlan, attempt BrowserLaunchAttempt,
	lease BrowserLaunchLease,
) error {
	if err := ValidateBrowserLaunchAttempt(attempt, session, identity, acceptance, ownership); err != nil {
		return err
	}
	if err := ValidateBrowserLaunchLease(lease, attempt); err != nil {
		return err
	}
	expectedReason := BrowserLaunchReviewReasonOperatorRejected
	if review.Decision == BrowserLaunchReviewAcceptCandidate {
		expectedReason = BrowserLaunchReviewReasonEvidenceConfirmed
	} else if review.Decision != BrowserLaunchReviewRejectCandidate {
		return errors.New("browser launch review decision is invalid")
	}
	if review.ProtocolVersion != BrowserLaunchReviewProtocolVersion ||
		!validPlanIdentity(review.ID) || review.AttemptID != attempt.ID ||
		review.AttemptFingerprint != attempt.Fingerprint ||
		review.LeaseFingerprint != lease.Fingerprint ||
		review.SessionPlanFingerprint != session.Fingerprint ||
		review.ExecutableIdentityFingerprint != identity.Fingerprint ||
		review.AcceptanceFingerprint != acceptance.Fingerprint ||
		review.ProfileOwnershipFingerprint != ownership.Fingerprint ||
		review.ScopeFingerprint != session.Scope.Fingerprint ||
		review.BudgetFingerprint != attempt.BudgetFingerprint ||
		review.ProfileGeneration != ownership.Generation ||
		review.LeaseGeneration != lease.Generation ||
		review.RequiredBackend != session.RequiredBackend ||
		review.ReasonCode != expectedReason || !validSHA256(review.ReviewerSHA256) ||
		!validSHA256(review.OperationKeySHA256) ||
		(review.AuditParentFingerprint != "" && !validSHA256(review.AuditParentFingerprint)) ||
		!review.IndependentReviewerVerified || !review.PublisherEvidenceConfirmed ||
		!review.ExactScopeConfirmed || !review.SandboxBackendConfirmed ||
		!review.BudgetConfirmed || !review.ProfileGenerationConfirmed ||
		!review.ProcessTreeContractConfirmed || !review.AppendOnlyAuditRequired ||
		review.AcceptedForFutureAdapter !=
			(review.Decision == BrowserLaunchReviewAcceptCandidate) ||
		review.StartAuthorized || review.ProcessExecutionAuthorized ||
		review.NetworkAuthorized || review.ProfileWriteAuthorized ||
		review.ProcessTerminationAuthorized || review.FilesystemCleanupAuthorized ||
		review.ArtifactCommitAuthorized || review.Authority != (RuntimeAuthority{}) ||
		review.CreatedAt.IsZero() || review.CreatedAt.Before(lease.AcquiredAt) ||
		!review.CreatedAt.Before(lease.ExpiresAt) {
		return errors.New("browser launch review lost an exact non-authorizing boundary")
	}
	expected, err := browserLaunchReviewFingerprint(review)
	if err != nil || review.Fingerprint != expected {
		return errors.New("browser launch review fingerprint mismatch")
	}
	return nil
}

func ValidateStoredBrowserLaunchReview(review BrowserLaunchReview) error {
	expectedReason := BrowserLaunchReviewReasonOperatorRejected
	if review.Decision == BrowserLaunchReviewAcceptCandidate {
		expectedReason = BrowserLaunchReviewReasonEvidenceConfirmed
	} else if review.Decision != BrowserLaunchReviewRejectCandidate {
		return errors.New("browser launch review decision is invalid")
	}
	if review.ProtocolVersion != BrowserLaunchReviewProtocolVersion ||
		!validPlanIdentity(review.ID) || !validPlanIdentity(review.AttemptID) ||
		!validSHA256(review.AttemptFingerprint) || !validSHA256(review.LeaseFingerprint) ||
		!validSHA256(review.SessionPlanFingerprint) ||
		!validSHA256(review.ExecutableIdentityFingerprint) ||
		!validSHA256(review.AcceptanceFingerprint) ||
		!validSHA256(review.ProfileOwnershipFingerprint) ||
		!validSHA256(review.ScopeFingerprint) || !validSHA256(review.BudgetFingerprint) ||
		review.ProfileGeneration == 0 || review.LeaseGeneration == 0 ||
		review.RequiredBackend == "" || review.ReasonCode != expectedReason ||
		!validSHA256(review.ReviewerSHA256) || !validSHA256(review.OperationKeySHA256) ||
		(review.AuditParentFingerprint != "" && !validSHA256(review.AuditParentFingerprint)) ||
		!review.IndependentReviewerVerified || !review.PublisherEvidenceConfirmed ||
		!review.ExactScopeConfirmed || !review.SandboxBackendConfirmed ||
		!review.BudgetConfirmed || !review.ProfileGenerationConfirmed ||
		!review.ProcessTreeContractConfirmed || !review.AppendOnlyAuditRequired ||
		review.AcceptedForFutureAdapter !=
			(review.Decision == BrowserLaunchReviewAcceptCandidate) ||
		review.StartAuthorized || review.ProcessExecutionAuthorized ||
		review.NetworkAuthorized || review.ProfileWriteAuthorized ||
		review.ProcessTerminationAuthorized || review.FilesystemCleanupAuthorized ||
		review.ArtifactCommitAuthorized || review.Authority != (RuntimeAuthority{}) ||
		review.CreatedAt.IsZero() {
		return errors.New("stored browser launch review is invalid")
	}
	expected, err := browserLaunchReviewFingerprint(review)
	if err != nil || review.Fingerprint != expected {
		return errors.New("browser launch review fingerprint mismatch")
	}
	return nil
}

func browserLaunchReviewIdentityDigest(identity string) string {
	digest := sha256.Sum256([]byte("browser-launch-reviewer.v1\x00" + identity))
	return hex.EncodeToString(digest[:])
}

func browserLaunchReviewOperationDigest(operationKey string) string {
	digest := sha256.Sum256([]byte("browser-launch-review-operation.v1\x00" + operationKey))
	return hex.EncodeToString(digest[:])
}

func browserLaunchReviewFingerprint(value BrowserLaunchReview) (string, error) {
	copyValue := value
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue, "browser launch review")
}
