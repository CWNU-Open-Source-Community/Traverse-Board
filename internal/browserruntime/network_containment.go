package browserruntime

import (
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"strings"
	"time"
)

const (
	BrowserNetworkContainmentPlanProtocolVersion     = "browser_network_containment_plan.v1"
	BrowserNetworkContainmentEvidenceProtocolVersion = "browser_network_containment_evidence.v1"
	BrowserNetworkContainmentReviewProtocolVersion   = "browser_network_containment_review.v1"
	WindowsWFPBrowserContainmentAdapterName          = "windows_wfp_dynamic.v1"
	DisabledBrowserContainmentAdapterName            = "disabled"
	FakeBrowserContainmentAdapterName                = "fake"
	BrowserNetworkContainmentPolicyVersion           = "browser_network_containment_policy.v1"
	MaxBrowserNetworkEvidenceTTL                     = 15 * time.Minute
)

type BrowserNetworkContainmentPlan struct {
	ProtocolVersion               string    `json:"protocol_version"`
	SessionPlanFingerprint        string    `json:"session_plan_fingerprint"`
	ExecutableIdentityFingerprint string    `json:"executable_identity_fingerprint"`
	EvidenceFingerprint           string    `json:"evidence_fingerprint"`
	ReviewFingerprint             string    `json:"review_fingerprint"`
	ExecutablePath                string    `json:"executable_path"`
	TargetAddress                 string    `json:"target_address"`
	TargetPort                    uint16    `json:"target_port"`
	TransportProtocol             string    `json:"transport_protocol"`
	Adapter                       string    `json:"adapter"`
	PolicyVersion                 string    `json:"policy_version"`
	DynamicSessionRequired        bool      `json:"dynamic_session_required"`
	AtomicInstallRequired         bool      `json:"atomic_install_required"`
	DefaultDenyIPv4               bool      `json:"default_deny_ipv4"`
	DefaultDenyIPv6               bool      `json:"default_deny_ipv6"`
	ExactTargetOnly               bool      `json:"exact_target_only"`
	DNSAuthorized                 bool      `json:"dns_authorized"`
	ProxyAuthorized               bool      `json:"proxy_authorized"`
	ExistingProcessAllowed        bool      `json:"existing_process_allowed"`
	CDPUsedAsEvidence             bool      `json:"cdp_used_as_evidence"`
	NetworkAuthorized             bool      `json:"network_authorized"`
	CreatedAt                     time.Time `json:"created_at"`
	ExpiresAt                     time.Time `json:"expires_at"`
	Fingerprint                   string    `json:"fingerprint"`
}

// BrowserNetworkContainmentEvidence is a bounded production probe result. A
// passing result must come from the concrete Windows WFP adapter and must show
// both a positive exact-target request and negative outside-scope canaries
// without using CDP as the observation mechanism.
type BrowserNetworkContainmentEvidence struct {
	ProtocolVersion               string    `json:"protocol_version"`
	ID                            string    `json:"id"`
	ExecutableIdentityFingerprint string    `json:"executable_identity_fingerprint"`
	AcceptanceFingerprint         string    `json:"acceptance_fingerprint"`
	Adapter                       string    `json:"adapter"`
	PolicyVersion                 string    `json:"policy_version"`
	OperatingSystem               string    `json:"operating_system"`
	Architecture                  string    `json:"architecture"`
	CollectorIdentity             string    `json:"collector_identity"`
	DynamicSessionObserved        bool      `json:"dynamic_session_observed"`
	AtomicInstallObserved         bool      `json:"atomic_install_observed"`
	ExactTargetObserved           bool      `json:"exact_target_observed"`
	WrongPortDenied               bool      `json:"wrong_port_denied"`
	WrongLoopbackAddressDenied    bool      `json:"wrong_loopback_address_denied"`
	NonLoopbackAddressDenied      bool      `json:"non_loopback_address_denied"`
	IPv6Denied                    bool      `json:"ipv6_denied"`
	RuleCleanupObserved           bool      `json:"rule_cleanup_observed"`
	CDPUsed                       bool      `json:"cdp_used"`
	Synthetic                     bool      `json:"synthetic"`
	Production                    bool      `json:"production"`
	Passed                        bool      `json:"passed"`
	FailureCode                   string    `json:"failure_code,omitempty"`
	StartedAt                     time.Time `json:"started_at"`
	CompletedAt                   time.Time `json:"completed_at"`
	ExpiresAt                     time.Time `json:"expires_at"`
	Fingerprint                   string    `json:"fingerprint"`
}

type BrowserNetworkContainmentReview struct {
	ProtocolVersion               string    `json:"protocol_version"`
	ID                            string    `json:"id"`
	EvidenceFingerprint           string    `json:"evidence_fingerprint"`
	ExecutableIdentityFingerprint string    `json:"executable_identity_fingerprint"`
	ReviewerIdentity              string    `json:"reviewer_identity"`
	Accepted                      bool      `json:"accepted"`
	ReasonCode                    string    `json:"reason_code"`
	NetworkAuthorized             bool      `json:"network_authorized"`
	ReviewedAt                    time.Time `json:"reviewed_at"`
	Fingerprint                   string    `json:"fingerprint"`
}

type BrowserNetworkProbeReport struct {
	ID                         string
	CollectorIdentity          string
	Adapter                    string
	DynamicSessionObserved     bool
	AtomicInstallObserved      bool
	ExactTargetObserved        bool
	WrongPortDenied            bool
	WrongLoopbackAddressDenied bool
	NonLoopbackAddressDenied   bool
	IPv6Denied                 bool
	RuleCleanupObserved        bool
	CDPUsed                    bool
	Synthetic                  bool
	Production                 bool
	FailureCode                string
	StartedAt                  time.Time
	CompletedAt                time.Time
}

type browserNetworkContainmentGuard interface {
	Adapter() string
	Fingerprint() string
	Close() error
	CleanupVerified() bool
}

type browserNetworkContainmentFactory interface {
	Name() string
	Available() bool
	Prepare(BrowserNetworkContainmentPlan) (browserNetworkContainmentGuard, error)
}

func BuildBrowserNetworkContainmentEvidence(identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, report BrowserNetworkProbeReport,
) (BrowserNetworkContainmentEvidence, error) {
	if err := ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return BrowserNetworkContainmentEvidence{}, err
	}
	if acceptance.Decision != BrowserAcceptanceAccepted || !acceptance.ReviewEligible ||
		!validPlanIdentity(report.ID) || !validPlanIdentity(report.CollectorIdentity) ||
		report.StartedAt.IsZero() || !report.CompletedAt.After(report.StartedAt) {
		return BrowserNetworkContainmentEvidence{}, errors.New("browser network probe report is invalid")
	}
	passed := report.Adapter == WindowsWFPBrowserContainmentAdapterName &&
		report.DynamicSessionObserved && report.AtomicInstallObserved &&
		report.ExactTargetObserved && report.WrongPortDenied &&
		report.WrongLoopbackAddressDenied && report.NonLoopbackAddressDenied &&
		report.IPv6Denied && report.RuleCleanupObserved && !report.CDPUsed &&
		!report.Synthetic && report.Production && report.FailureCode == ""
	if report.Production && report.Adapter != WindowsWFPBrowserContainmentAdapterName {
		return BrowserNetworkContainmentEvidence{}, errors.New("production browser network evidence used an unrecognized adapter")
	}
	if passed != (strings.TrimSpace(report.FailureCode) == "") {
		return BrowserNetworkContainmentEvidence{}, errors.New("browser network probe pass and failure code disagree")
	}
	evidence := BrowserNetworkContainmentEvidence{
		ProtocolVersion: BrowserNetworkContainmentEvidenceProtocolVersion,
		ID:              report.ID, ExecutableIdentityFingerprint: identity.Fingerprint,
		AcceptanceFingerprint: acceptance.Fingerprint, Adapter: report.Adapter,
		PolicyVersion:   BrowserNetworkContainmentPolicyVersion,
		OperatingSystem: runtime.GOOS, Architecture: runtime.GOARCH,
		CollectorIdentity:          report.CollectorIdentity,
		DynamicSessionObserved:     report.DynamicSessionObserved,
		AtomicInstallObserved:      report.AtomicInstallObserved,
		ExactTargetObserved:        report.ExactTargetObserved,
		WrongPortDenied:            report.WrongPortDenied,
		WrongLoopbackAddressDenied: report.WrongLoopbackAddressDenied,
		NonLoopbackAddressDenied:   report.NonLoopbackAddressDenied,
		IPv6Denied:                 report.IPv6Denied, RuleCleanupObserved: report.RuleCleanupObserved,
		CDPUsed: report.CDPUsed, Synthetic: report.Synthetic,
		Production: report.Production, Passed: passed,
		FailureCode: strings.TrimSpace(report.FailureCode),
		StartedAt:   report.StartedAt.UTC(), CompletedAt: report.CompletedAt.UTC(),
		ExpiresAt: report.CompletedAt.UTC().Add(MaxBrowserNetworkEvidenceTTL),
	}
	evidence.Fingerprint = browserRuntimeFingerprint(evidence)
	if err := ValidateBrowserNetworkContainmentEvidence(evidence, identity, acceptance); err != nil {
		return BrowserNetworkContainmentEvidence{}, err
	}
	return evidence, nil
}

func ValidateBrowserNetworkContainmentEvidence(evidence BrowserNetworkContainmentEvidence,
	identity BrowserExecutableIdentity, acceptance BrowserAcceptanceCandidate,
) error {
	if err := ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return err
	}
	passed := evidence.Adapter == WindowsWFPBrowserContainmentAdapterName &&
		evidence.DynamicSessionObserved && evidence.AtomicInstallObserved &&
		evidence.ExactTargetObserved && evidence.WrongPortDenied &&
		evidence.WrongLoopbackAddressDenied && evidence.NonLoopbackAddressDenied &&
		evidence.IPv6Denied && evidence.RuleCleanupObserved && !evidence.CDPUsed &&
		!evidence.Synthetic && evidence.Production && evidence.FailureCode == ""
	if evidence.ProtocolVersion != BrowserNetworkContainmentEvidenceProtocolVersion ||
		!validPlanIdentity(evidence.ID) ||
		evidence.ExecutableIdentityFingerprint != identity.Fingerprint ||
		evidence.AcceptanceFingerprint != acceptance.Fingerprint ||
		evidence.PolicyVersion != BrowserNetworkContainmentPolicyVersion ||
		evidence.OperatingSystem != runtime.GOOS || evidence.Architecture != runtime.GOARCH ||
		!validPlanIdentity(evidence.CollectorIdentity) || evidence.Passed != passed ||
		evidence.StartedAt.IsZero() || !evidence.CompletedAt.After(evidence.StartedAt) ||
		!evidence.ExpiresAt.Equal(evidence.CompletedAt.Add(MaxBrowserNetworkEvidenceTTL)) ||
		evidence.Fingerprint != browserRuntimeFingerprint(evidence) {
		return errors.New("browser network containment evidence lost a production boundary")
	}
	return nil
}

func BuildBrowserNetworkContainmentReview(evidence BrowserNetworkContainmentEvidence,
	identity BrowserExecutableIdentity, acceptance BrowserAcceptanceCandidate,
	reviewID string, reviewerIdentity string, accepted bool, reviewedAt time.Time,
) (BrowserNetworkContainmentReview, error) {
	if err := ValidateBrowserNetworkContainmentEvidence(evidence, identity, acceptance); err != nil {
		return BrowserNetworkContainmentReview{}, err
	}
	if !validPlanIdentity(reviewID) || !validPlanIdentity(reviewerIdentity) ||
		reviewerIdentity == evidence.CollectorIdentity || reviewedAt.IsZero() ||
		reviewedAt.Before(evidence.CompletedAt) || !reviewedAt.Before(evidence.ExpiresAt) {
		return BrowserNetworkContainmentReview{}, errors.New("browser network review identity or time is invalid")
	}
	if accepted && !evidence.Passed {
		return BrowserNetworkContainmentReview{}, errors.New("failed browser network evidence cannot be accepted")
	}
	reason := "operator_rejected"
	if accepted {
		reason = "production_probe_confirmed"
	}
	review := BrowserNetworkContainmentReview{
		ProtocolVersion: BrowserNetworkContainmentReviewProtocolVersion,
		ID:              reviewID, EvidenceFingerprint: evidence.Fingerprint,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		ReviewerIdentity:              reviewerIdentity, Accepted: accepted,
		ReasonCode: reason, ReviewedAt: reviewedAt.UTC(),
	}
	review.Fingerprint = browserRuntimeFingerprint(review)
	if err := ValidateBrowserNetworkContainmentReview(review, evidence, identity, acceptance); err != nil {
		return BrowserNetworkContainmentReview{}, err
	}
	return review, nil
}

func ValidateBrowserNetworkContainmentReview(review BrowserNetworkContainmentReview,
	evidence BrowserNetworkContainmentEvidence, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate,
) error {
	if err := ValidateBrowserNetworkContainmentEvidence(evidence, identity, acceptance); err != nil {
		return err
	}
	expectedReason := "operator_rejected"
	if review.Accepted {
		expectedReason = "production_probe_confirmed"
	}
	if review.ProtocolVersion != BrowserNetworkContainmentReviewProtocolVersion ||
		!validPlanIdentity(review.ID) || review.EvidenceFingerprint != evidence.Fingerprint ||
		review.ExecutableIdentityFingerprint != identity.Fingerprint ||
		!validPlanIdentity(review.ReviewerIdentity) ||
		review.ReviewerIdentity == evidence.CollectorIdentity ||
		review.ReasonCode != expectedReason || review.NetworkAuthorized ||
		review.ReviewedAt.IsZero() || review.ReviewedAt.Before(evidence.CompletedAt) ||
		!review.ReviewedAt.Before(evidence.ExpiresAt) ||
		(review.Accepted && !evidence.Passed) ||
		review.Fingerprint != browserRuntimeFingerprint(review) {
		return errors.New("browser network containment review lost its non-authorizing boundary")
	}
	return nil
}

func BuildBrowserNetworkContainmentPlan(session SessionPlan,
	identity BrowserExecutableIdentity, acceptance BrowserAcceptanceCandidate,
	evidence BrowserNetworkContainmentEvidence, review BrowserNetworkContainmentReview,
	createdAt time.Time, expiresAt time.Time,
) (BrowserNetworkContainmentPlan, error) {
	if err := ValidateBrowserNetworkContainmentReview(review, evidence, identity, acceptance); err != nil {
		return BrowserNetworkContainmentPlan{}, err
	}
	if !evidence.Passed || !review.Accepted || createdAt.IsZero() ||
		createdAt.Before(review.ReviewedAt) || !expiresAt.After(createdAt) ||
		expiresAt.After(evidence.ExpiresAt) {
		return BrowserNetworkContainmentPlan{}, errors.New("browser network containment evidence is not active")
	}
	if err := validateRestrictedLoopbackSession(session); err != nil {
		return BrowserNetworkContainmentPlan{}, err
	}
	origin := session.Scope.Origins[0]
	address, err := netip.ParseAddr(origin.Host)
	if err != nil || !address.Is4() {
		return BrowserNetworkContainmentPlan{}, errors.New("browser containment currently requires IPv4 loopback")
	}
	plan := BrowserNetworkContainmentPlan{
		ProtocolVersion:               BrowserNetworkContainmentPlanProtocolVersion,
		SessionPlanFingerprint:        session.Fingerprint,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		EvidenceFingerprint:           evidence.Fingerprint, ReviewFingerprint: review.Fingerprint,
		ExecutablePath: identity.CanonicalPath, TargetAddress: address.String(),
		TargetPort: origin.Port, TransportProtocol: "tcp",
		Adapter:                WindowsWFPBrowserContainmentAdapterName,
		PolicyVersion:          BrowserNetworkContainmentPolicyVersion,
		DynamicSessionRequired: true, AtomicInstallRequired: true,
		DefaultDenyIPv4: true, DefaultDenyIPv6: true, ExactTargetOnly: true,
		NetworkAuthorized: true, CreatedAt: createdAt.UTC(), ExpiresAt: expiresAt.UTC(),
	}
	plan.Fingerprint = browserRuntimeFingerprint(plan)
	if err := ValidateBrowserNetworkContainmentPlan(plan, session, identity,
		acceptance, evidence, review); err != nil {
		return BrowserNetworkContainmentPlan{}, err
	}
	return plan, nil
}

func ValidateBrowserNetworkContainmentPlan(plan BrowserNetworkContainmentPlan,
	session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, evidence BrowserNetworkContainmentEvidence,
	review BrowserNetworkContainmentReview,
) error {
	if err := ValidateBrowserNetworkContainmentReview(review, evidence, identity, acceptance); err != nil {
		return err
	}
	if err := validateRestrictedLoopbackSession(session); err != nil {
		return err
	}
	origin := session.Scope.Origins[0]
	if plan.ProtocolVersion != BrowserNetworkContainmentPlanProtocolVersion ||
		plan.SessionPlanFingerprint != session.Fingerprint ||
		plan.ExecutableIdentityFingerprint != identity.Fingerprint ||
		plan.EvidenceFingerprint != evidence.Fingerprint ||
		plan.ReviewFingerprint != review.Fingerprint ||
		plan.ExecutablePath != identity.CanonicalPath || plan.TargetAddress != origin.Host ||
		plan.TargetPort != origin.Port || plan.TransportProtocol != "tcp" ||
		plan.Adapter != WindowsWFPBrowserContainmentAdapterName ||
		plan.PolicyVersion != BrowserNetworkContainmentPolicyVersion ||
		!plan.DynamicSessionRequired || !plan.AtomicInstallRequired ||
		!plan.DefaultDenyIPv4 || !plan.DefaultDenyIPv6 || !plan.ExactTargetOnly ||
		plan.DNSAuthorized || plan.ProxyAuthorized || plan.ExistingProcessAllowed ||
		plan.CDPUsedAsEvidence || !plan.NetworkAuthorized || plan.CreatedAt.IsZero() ||
		!plan.ExpiresAt.After(plan.CreatedAt) || plan.ExpiresAt.After(evidence.ExpiresAt) ||
		!evidence.Passed || !review.Accepted ||
		plan.Fingerprint != browserRuntimeFingerprint(plan) {
		return fmt.Errorf("browser network containment plan lost exact-target default-deny policy")
	}
	return nil
}
