package browserruntime

import (
	"errors"
	"runtime"
	"time"
)

const BrowserSafeWebReadinessProtocolVersion = "browser_safe_web_readiness.v1"

// BrowserSafeWebReadiness blocking reasons. A ready receipt always carries an
// empty reason; a fail-closed receipt carries exactly one precise reason.
const (
	BrowserSafeWebBlockedEvidenceMissing         = "evidence_missing"
	BrowserSafeWebBlockedEvidenceVersionMismatch = "evidence_version_mismatch"
	BrowserSafeWebBlockedExecutableMismatch      = "executable_identity_mismatch"
	BrowserSafeWebBlockedAcceptanceMismatch      = "acceptance_mismatch"
	BrowserSafeWebBlockedPolicyVersionMismatch   = "policy_version_mismatch"
	BrowserSafeWebBlockedAdapterMismatch         = "adapter_mismatch"
	BrowserSafeWebBlockedPlatformMismatch        = "platform_mismatch"
	BrowserSafeWebBlockedEvidenceNotPassed       = "evidence_not_passed"
	BrowserSafeWebBlockedReviewMissing           = "review_missing"
	BrowserSafeWebBlockedReviewBindingMismatch   = "review_binding_mismatch"
	BrowserSafeWebBlockedReviewNotAccepted       = "review_not_accepted"
	BrowserSafeWebBlockedEvidenceExpired         = "evidence_expired"
)

// BrowserSafeWebReadiness is a bounded, fail-closed judgment over a production
// network-containment evidence + review pair. It records only whether Safe Web
// is currently ready for one exact accepted browser executable, and why not. It
// never authorizes a launch by itself: it is the process-local gate consumed by
// the Safe Web operator entry.
type BrowserSafeWebReadiness struct {
	ProtocolVersion               string    `json:"protocol_version"`
	EvidenceFingerprint           string    `json:"evidence_fingerprint"`
	ReviewFingerprint             string    `json:"review_fingerprint"`
	ExecutableIdentityFingerprint string    `json:"executable_identity_fingerprint"`
	AcceptanceFingerprint         string    `json:"acceptance_fingerprint"`
	Adapter                       string    `json:"adapter"`
	PolicyVersion                 string    `json:"policy_version"`
	OperatingSystem               string    `json:"operating_system"`
	Architecture                  string    `json:"architecture"`
	CollectorIdentity             string    `json:"collector_identity"`
	ReviewerIdentity              string    `json:"reviewer_identity"`
	Ready                         bool      `json:"ready"`
	BlockingReason                string    `json:"blocking_reason,omitempty"`
	IssuedAt                      time.Time `json:"issued_at"`
	ExpiresAt                     time.Time `json:"expires_at"`
	Fingerprint                   string    `json:"fingerprint"`
}

// BuildBrowserSafeWebReadiness evaluates a production containment evidence +
// review pair against the current accepted browser identity. The evidence and
// review are untrusted operator inputs: a missing, expired, version-mismatched,
// hash-mismatched, policy-mismatched, or rejected pair fails closed as a
// Ready=false receipt with a precise blocking reason rather than an error.
func BuildBrowserSafeWebReadiness(evidence BrowserNetworkContainmentEvidence,
	review BrowserNetworkContainmentReview, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, now time.Time,
) (BrowserSafeWebReadiness, error) {
	if err := ValidateBrowserExecutableIdentity(identity); err != nil {
		return BrowserSafeWebReadiness{}, err
	}
	if err := ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return BrowserSafeWebReadiness{}, err
	}
	now = now.UTC()
	if now.IsZero() {
		return BrowserSafeWebReadiness{}, errors.New("browser safe-web readiness requires a current time")
	}
	ready, reason := judgeBrowserSafeWebReadiness(evidence, review, identity, acceptance, now)
	readiness := BrowserSafeWebReadiness{
		ProtocolVersion:               BrowserSafeWebReadinessProtocolVersion,
		EvidenceFingerprint:           evidence.Fingerprint,
		ReviewFingerprint:             review.Fingerprint,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		AcceptanceFingerprint:         acceptance.Fingerprint,
		Adapter:                       evidence.Adapter,
		PolicyVersion:                 evidence.PolicyVersion,
		OperatingSystem:               evidence.OperatingSystem,
		Architecture:                  evidence.Architecture,
		CollectorIdentity:             evidence.CollectorIdentity,
		ReviewerIdentity:              review.ReviewerIdentity,
		Ready:                         ready,
		BlockingReason:                reason,
		IssuedAt:                      now,
		ExpiresAt:                     evidence.ExpiresAt,
	}
	readiness.Fingerprint = browserRuntimeFingerprint(readiness)
	if err := ValidateBrowserSafeWebReadiness(readiness, evidence, review,
		identity, acceptance); err != nil {
		return BrowserSafeWebReadiness{}, err
	}
	return readiness, nil
}

func judgeBrowserSafeWebReadiness(evidence BrowserNetworkContainmentEvidence,
	review BrowserNetworkContainmentReview, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, now time.Time,
) (bool, string) {
	switch {
	case evidence.Fingerprint == "":
		return false, BrowserSafeWebBlockedEvidenceMissing
	case evidence.ProtocolVersion != BrowserNetworkContainmentEvidenceProtocolVersion:
		return false, BrowserSafeWebBlockedEvidenceVersionMismatch
	case evidence.ExecutableIdentityFingerprint != identity.Fingerprint:
		return false, BrowserSafeWebBlockedExecutableMismatch
	case evidence.AcceptanceFingerprint != acceptance.Fingerprint:
		return false, BrowserSafeWebBlockedAcceptanceMismatch
	case evidence.PolicyVersion != BrowserNetworkContainmentPolicyVersion:
		return false, BrowserSafeWebBlockedPolicyVersionMismatch
	case evidence.Adapter != WindowsWFPBrowserContainmentAdapterName:
		return false, BrowserSafeWebBlockedAdapterMismatch
	case evidence.OperatingSystem != runtime.GOOS || evidence.Architecture != runtime.GOARCH:
		return false, BrowserSafeWebBlockedPlatformMismatch
	case !evidence.Passed:
		return false, BrowserSafeWebBlockedEvidenceNotPassed
	case review.Fingerprint == "":
		return false, BrowserSafeWebBlockedReviewMissing
	case review.EvidenceFingerprint != evidence.Fingerprint:
		return false, BrowserSafeWebBlockedReviewBindingMismatch
	case !review.Accepted:
		return false, BrowserSafeWebBlockedReviewNotAccepted
	case !now.Before(evidence.ExpiresAt):
		return false, BrowserSafeWebBlockedEvidenceExpired
	default:
		return true, ""
	}
}

// ValidateBrowserSafeWebReadiness re-checks that a readiness receipt still
// binds the exact evidence, review, identity, and acceptance it was judged
// against, and that its ready state and blocking reason agree with a fresh
// judgment at its issued-at time.
func ValidateBrowserSafeWebReadiness(readiness BrowserSafeWebReadiness,
	evidence BrowserNetworkContainmentEvidence, review BrowserNetworkContainmentReview,
	identity BrowserExecutableIdentity, acceptance BrowserAcceptanceCandidate,
) error {
	if err := ValidateBrowserExecutableIdentity(identity); err != nil {
		return err
	}
	if err := ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return err
	}
	if readiness.IssuedAt.IsZero() {
		return errors.New("browser safe-web readiness has no issued-at time")
	}
	ready, reason := judgeBrowserSafeWebReadiness(evidence, review, identity,
		acceptance, readiness.IssuedAt)
	if readiness.ProtocolVersion != BrowserSafeWebReadinessProtocolVersion ||
		readiness.EvidenceFingerprint != evidence.Fingerprint ||
		readiness.ReviewFingerprint != review.Fingerprint ||
		readiness.ExecutableIdentityFingerprint != identity.Fingerprint ||
		readiness.AcceptanceFingerprint != acceptance.Fingerprint ||
		readiness.Adapter != evidence.Adapter ||
		readiness.PolicyVersion != evidence.PolicyVersion ||
		readiness.OperatingSystem != evidence.OperatingSystem ||
		readiness.Architecture != evidence.Architecture ||
		readiness.CollectorIdentity != evidence.CollectorIdentity ||
		readiness.ReviewerIdentity != review.ReviewerIdentity ||
		readiness.Ready != ready || readiness.BlockingReason != reason ||
		readiness.ExpiresAt != evidence.ExpiresAt ||
		(readiness.Ready && readiness.BlockingReason != "") ||
		(!readiness.Ready && readiness.BlockingReason == "") ||
		readiness.Fingerprint != browserRuntimeFingerprint(readiness) {
		return errors.New("browser safe-web readiness lost its fail-closed boundary")
	}
	return nil
}
