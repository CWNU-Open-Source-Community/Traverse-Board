package browserruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"cyberagent-workbench/internal/domain"
)

const (
	BrowserStartAuthorizationProtocolVersion  = "browser_start_authorization.v1"
	RestrictedCDPAuthorizationProtocolVersion = "restricted_cdp_authorization.v1"
)

// ProductionRuntimeCapabilities are process-local startup gates. Persisted
// policy selections never populate these values.
type ProductionRuntimeCapabilities struct {
	SafeWebStartEnabled       bool `json:"safe_web_start_enabled"`
	DisposableProfileEnabled  bool `json:"disposable_profile_enabled"`
	NetworkContainmentEnabled bool `json:"network_containment_enabled"`
	RestrictedCDPEnabled      bool `json:"restricted_cdp_enabled"`
}

func (capabilities ProductionRuntimeCapabilities) Validate() error {
	if capabilities.RestrictedCDPEnabled &&
		(!capabilities.SafeWebStartEnabled || !capabilities.DisposableProfileEnabled ||
			!capabilities.NetworkContainmentEnabled) {
		return errors.New("restricted CDP requires Safe Web, disposable Profile, and network containment gates")
	}
	if capabilities.SafeWebStartEnabled != capabilities.DisposableProfileEnabled {
		return errors.New("safe-web start and disposable Profile gates must be enabled together")
	}
	return nil
}

// BrowserStartAuthorization is a short-lived, in-memory authorization. It is
// derived from non-authorizing durable facts and cannot be reconstructed from
// a database row without the current process-local capabilities.
type BrowserStartAuthorization struct {
	ProtocolVersion               string    `json:"protocol_version"`
	SessionPlanFingerprint        string    `json:"session_plan_fingerprint"`
	ExecutableIdentityFingerprint string    `json:"executable_identity_fingerprint"`
	AcceptanceFingerprint         string    `json:"acceptance_fingerprint"`
	ProfileOwnershipFingerprint   string    `json:"profile_ownership_fingerprint"`
	NetworkEvidenceFingerprint    string    `json:"network_evidence_fingerprint"`
	NetworkReviewFingerprint      string    `json:"network_review_fingerprint"`
	NetworkPlanFingerprint        string    `json:"network_plan_fingerprint"`
	AttemptFingerprint            string    `json:"attempt_fingerprint"`
	LeaseFingerprint              string    `json:"lease_fingerprint"`
	ReviewFingerprint             string    `json:"review_fingerprint"`
	PermissionSnapshotID          string    `json:"permission_snapshot_id"`
	PermissionRevision            int64     `json:"permission_revision"`
	ScopeFingerprint              string    `json:"scope_fingerprint"`
	ProfileGeneration             uint64    `json:"profile_generation"`
	ProcessStartAuthorized        bool      `json:"process_start_authorized"`
	ProcessTerminationAuthorized  bool      `json:"process_termination_authorized"`
	ProfileCreateAuthorized       bool      `json:"profile_create_authorized"`
	ProfileReleaseAuthorized      bool      `json:"profile_release_authorized"`
	ExactOwnedCleanupAuthorized   bool      `json:"exact_owned_cleanup_authorized"`
	LoopbackNavigationRequired    bool      `json:"loopback_navigation_required"`
	NetworkContainmentAuthorized  bool      `json:"network_containment_authorized"`
	PersonalProfileAuthorized     bool      `json:"personal_profile_authorized"`
	ShellAuthorized               bool      `json:"shell_authorized"`
	FullCDPAuthorized             bool      `json:"full_cdp_authorized"`
	IssuedAt                      time.Time `json:"issued_at"`
	StartDeadline                 time.Time `json:"start_deadline"`
	RuntimeDeadline               time.Time `json:"runtime_deadline"`
	Fingerprint                   string    `json:"fingerprint"`
}

// RestrictedCDPAuthorization narrows a start authorization to three fixed
// operations. It does not authorize request capture, mutation, replay,
// cookies, arbitrary methods, or browser-content instructions.
type RestrictedCDPAuthorization struct {
	ProtocolVersion               string    `json:"protocol_version"`
	StartAuthorizationFingerprint string    `json:"start_authorization_fingerprint"`
	PermissionSnapshotID          string    `json:"permission_snapshot_id"`
	PermissionRevision            int64     `json:"permission_revision"`
	ScopeFingerprint              string    `json:"scope_fingerprint"`
	NavigateAuthorized            bool      `json:"navigate_authorized"`
	DOMMetadataAuthorized         bool      `json:"dom_metadata_authorized"`
	ScreenshotAuthorized          bool      `json:"screenshot_authorized"`
	RequestCaptureAuthorized      bool      `json:"request_capture_authorized"`
	RequestMutationAuthorized     bool      `json:"request_mutation_authorized"`
	RequestReplayAuthorized       bool      `json:"request_replay_authorized"`
	CookieAccessAuthorized        bool      `json:"cookie_access_authorized"`
	ArbitraryMethodAuthorized     bool      `json:"arbitrary_method_authorized"`
	InstructionAuthorized         bool      `json:"instruction_authorized"`
	IssuedAt                      time.Time `json:"issued_at"`
	ExpiresAt                     time.Time `json:"expires_at"`
	Fingerprint                   string    `json:"fingerprint"`
}

func AuthorizeSafeWebStart(session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease, review BrowserLaunchReview,
	networkEvidence BrowserNetworkContainmentEvidence,
	networkReview BrowserNetworkContainmentReview,
	networkPlan BrowserNetworkContainmentPlan,
	permission domain.RunBrowserCDPPermissionSnapshot,
	permissionCapabilities domain.BrowserCDPPermissionRuntimeCapabilities,
	runtimeCapabilities ProductionRuntimeCapabilities, now time.Time,
) (BrowserStartAuthorization, error) {
	if err := ValidateBrowserLaunchReview(review, session, identity, acceptance,
		ownership, attempt, lease); err != nil {
		return BrowserStartAuthorization{}, err
	}
	if err := permission.Validate(); err != nil {
		return BrowserStartAuthorization{}, err
	}
	if err := permissionCapabilities.Validate(); err != nil {
		return BrowserStartAuthorization{}, err
	}
	if err := runtimeCapabilities.Validate(); err != nil {
		return BrowserStartAuthorization{}, err
	}
	if err := ValidateBrowserNetworkContainmentPlan(networkPlan, session, identity,
		acceptance, networkEvidence, networkReview); err != nil {
		return BrowserStartAuthorization{}, err
	}
	if !review.AcceptedForFutureAdapter ||
		permission.Mode != domain.RunBrowserCDPPermissionRestricted ||
		permission.RunID != session.RunID || !permissionCapabilities.ControlEnabled ||
		!runtimeCapabilities.SafeWebStartEnabled ||
		!runtimeCapabilities.DisposableProfileEnabled ||
		!runtimeCapabilities.NetworkContainmentEnabled {
		return BrowserStartAuthorization{}, errors.New("safe-web runtime gates are not all satisfied")
	}
	if err := validateRestrictedLoopbackSession(session); err != nil {
		return BrowserStartAuthorization{}, err
	}
	now = now.UTC()
	if now.IsZero() || now.Before(review.CreatedAt) || !now.Before(lease.ExpiresAt) {
		return BrowserStartAuthorization{}, errors.New("browser launch review lease is not active")
	}
	runtimeDeadline := now.Add(time.Duration(attempt.MaxRuntimeMS) * time.Millisecond)
	authorization := BrowserStartAuthorization{
		ProtocolVersion:               BrowserStartAuthorizationProtocolVersion,
		SessionPlanFingerprint:        session.Fingerprint,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		AcceptanceFingerprint:         acceptance.Fingerprint,
		ProfileOwnershipFingerprint:   ownership.Fingerprint,
		NetworkEvidenceFingerprint:    networkEvidence.Fingerprint,
		NetworkReviewFingerprint:      networkReview.Fingerprint,
		NetworkPlanFingerprint:        networkPlan.Fingerprint,
		AttemptFingerprint:            attempt.Fingerprint,
		LeaseFingerprint:              lease.Fingerprint,
		ReviewFingerprint:             review.Fingerprint,
		PermissionSnapshotID:          permission.ID,
		PermissionRevision:            permission.Revision,
		ScopeFingerprint:              session.Scope.Fingerprint,
		ProfileGeneration:             ownership.Generation,
		ProcessStartAuthorized:        true,
		ProcessTerminationAuthorized:  true,
		ProfileCreateAuthorized:       true,
		ProfileReleaseAuthorized:      true,
		ExactOwnedCleanupAuthorized:   true,
		LoopbackNavigationRequired:    true,
		NetworkContainmentAuthorized:  true,
		IssuedAt:                      now,
		StartDeadline:                 lease.ExpiresAt,
		RuntimeDeadline:               runtimeDeadline,
	}
	authorization.Fingerprint = browserRuntimeFingerprint(authorization)
	if err := ValidateBrowserStartAuthorization(authorization, session, identity,
		acceptance, ownership, attempt, lease, review, networkEvidence,
		networkReview, networkPlan, permission); err != nil {
		return BrowserStartAuthorization{}, err
	}
	return authorization, nil
}

func ValidateBrowserStartAuthorization(authorization BrowserStartAuthorization,
	session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease, review BrowserLaunchReview,
	networkEvidence BrowserNetworkContainmentEvidence,
	networkReview BrowserNetworkContainmentReview,
	networkPlan BrowserNetworkContainmentPlan,
	permission domain.RunBrowserCDPPermissionSnapshot,
) error {
	if err := ValidateBrowserLaunchReview(review, session, identity, acceptance,
		ownership, attempt, lease); err != nil {
		return err
	}
	if err := permission.Validate(); err != nil {
		return err
	}
	if err := ValidateBrowserNetworkContainmentPlan(networkPlan, session, identity,
		acceptance, networkEvidence, networkReview); err != nil {
		return err
	}
	if err := validateRestrictedLoopbackSession(session); err != nil {
		return err
	}
	if authorization.ProtocolVersion != BrowserStartAuthorizationProtocolVersion ||
		authorization.SessionPlanFingerprint != session.Fingerprint ||
		authorization.ExecutableIdentityFingerprint != identity.Fingerprint ||
		authorization.AcceptanceFingerprint != acceptance.Fingerprint ||
		authorization.ProfileOwnershipFingerprint != ownership.Fingerprint ||
		authorization.NetworkEvidenceFingerprint != networkEvidence.Fingerprint ||
		authorization.NetworkReviewFingerprint != networkReview.Fingerprint ||
		authorization.NetworkPlanFingerprint != networkPlan.Fingerprint ||
		authorization.AttemptFingerprint != attempt.Fingerprint ||
		authorization.LeaseFingerprint != lease.Fingerprint ||
		authorization.ReviewFingerprint != review.Fingerprint ||
		authorization.PermissionSnapshotID != permission.ID ||
		authorization.PermissionRevision != permission.Revision ||
		authorization.ScopeFingerprint != session.Scope.Fingerprint ||
		authorization.ProfileGeneration != ownership.Generation ||
		permission.Mode != domain.RunBrowserCDPPermissionRestricted ||
		permission.RunID != session.RunID || !authorization.ProcessStartAuthorized ||
		!authorization.ProcessTerminationAuthorized ||
		!authorization.ProfileCreateAuthorized ||
		!authorization.ProfileReleaseAuthorized ||
		!authorization.ExactOwnedCleanupAuthorized ||
		!authorization.LoopbackNavigationRequired ||
		!authorization.NetworkContainmentAuthorized ||
		authorization.PersonalProfileAuthorized || authorization.ShellAuthorized ||
		authorization.FullCDPAuthorized || authorization.IssuedAt.IsZero() ||
		authorization.StartDeadline.IsZero() || authorization.RuntimeDeadline.IsZero() ||
		!authorization.StartDeadline.Equal(lease.ExpiresAt) ||
		authorization.IssuedAt.Before(networkPlan.CreatedAt) ||
		authorization.RuntimeDeadline.After(networkPlan.ExpiresAt) ||
		!authorization.StartDeadline.After(authorization.IssuedAt) ||
		!authorization.RuntimeDeadline.After(authorization.IssuedAt) ||
		authorization.RuntimeDeadline.Sub(authorization.IssuedAt) !=
			time.Duration(attempt.MaxRuntimeMS)*time.Millisecond ||
		authorization.Fingerprint != browserRuntimeFingerprint(authorization) {
		return errors.New("browser start authorization lost an exact runtime boundary")
	}
	return nil
}

func AuthorizeRestrictedCDP(start BrowserStartAuthorization,
	session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease, review BrowserLaunchReview,
	networkEvidence BrowserNetworkContainmentEvidence,
	networkReview BrowserNetworkContainmentReview,
	networkPlan BrowserNetworkContainmentPlan,
	permission domain.RunBrowserCDPPermissionSnapshot,
	runtimeCapabilities ProductionRuntimeCapabilities, now time.Time,
) (RestrictedCDPAuthorization, error) {
	if err := ValidateBrowserStartAuthorization(start, session, identity, acceptance,
		ownership, attempt, lease, review, networkEvidence, networkReview,
		networkPlan, permission); err != nil {
		return RestrictedCDPAuthorization{}, err
	}
	if err := runtimeCapabilities.Validate(); err != nil {
		return RestrictedCDPAuthorization{}, err
	}
	now = now.UTC()
	if !runtimeCapabilities.RestrictedCDPEnabled || now.IsZero() ||
		now.Before(start.IssuedAt) || !now.Before(start.RuntimeDeadline) {
		return RestrictedCDPAuthorization{}, errors.New("restricted CDP runtime gate is unavailable or expired")
	}
	authorization := RestrictedCDPAuthorization{
		ProtocolVersion:               RestrictedCDPAuthorizationProtocolVersion,
		StartAuthorizationFingerprint: start.Fingerprint,
		PermissionSnapshotID:          permission.ID,
		PermissionRevision:            permission.Revision,
		ScopeFingerprint:              session.Scope.Fingerprint,
		NavigateAuthorized:            true,
		DOMMetadataAuthorized:         true,
		ScreenshotAuthorized:          true,
		IssuedAt:                      now,
		ExpiresAt:                     start.RuntimeDeadline,
	}
	authorization.Fingerprint = browserRuntimeFingerprint(authorization)
	if err := ValidateRestrictedCDPAuthorization(authorization, start, session,
		permission); err != nil {
		return RestrictedCDPAuthorization{}, err
	}
	return authorization, nil
}

func ValidateRestrictedCDPAuthorization(authorization RestrictedCDPAuthorization,
	start BrowserStartAuthorization, session SessionPlan,
	permission domain.RunBrowserCDPPermissionSnapshot,
) error {
	if err := permission.Validate(); err != nil {
		return err
	}
	if err := validateRestrictedLoopbackSession(session); err != nil {
		return err
	}
	if authorization.ProtocolVersion != RestrictedCDPAuthorizationProtocolVersion ||
		authorization.StartAuthorizationFingerprint != start.Fingerprint ||
		authorization.PermissionSnapshotID != permission.ID ||
		authorization.PermissionRevision != permission.Revision ||
		authorization.ScopeFingerprint != session.Scope.Fingerprint ||
		permission.Mode != domain.RunBrowserCDPPermissionRestricted ||
		!authorization.NavigateAuthorized || !authorization.DOMMetadataAuthorized ||
		!authorization.ScreenshotAuthorized || authorization.RequestCaptureAuthorized ||
		authorization.RequestMutationAuthorized || authorization.RequestReplayAuthorized ||
		authorization.CookieAccessAuthorized || authorization.ArbitraryMethodAuthorized ||
		authorization.InstructionAuthorized || authorization.IssuedAt.IsZero() ||
		authorization.ExpiresAt.IsZero() || authorization.IssuedAt.Before(start.IssuedAt) ||
		!authorization.ExpiresAt.Equal(start.RuntimeDeadline) ||
		!authorization.ExpiresAt.After(authorization.IssuedAt) ||
		authorization.Fingerprint != browserRuntimeFingerprint(authorization) {
		return errors.New("restricted CDP authorization lost its fixed method boundary")
	}
	return nil
}

func validateRestrictedLoopbackSession(session SessionPlan) error {
	if err := session.Validate(); err != nil {
		return err
	}
	if session.ProfileID != ProfileSafeWeb || session.Proxy.Mode != ProxyModeDirect ||
		len(session.Scope.Origins) != 1 || session.Features.RequestInterception ||
		session.Features.RequestMutation || session.Features.RequestReplay ||
		session.Features.CookieEditing || session.Features.RelaxOriginPolicy ||
		session.Features.AllowInsecureContent || session.Features.IgnoreCertificateErrors {
		return errors.New("production browser runtime currently accepts one direct Safe Web origin only")
	}
	origin := session.Scope.Origins[0]
	address, err := netip.ParseAddr(origin.Host)
	if err != nil || !address.Unmap().IsLoopback() || origin.HostClass != HostClassLoopback ||
		origin.ResolutionCheckRequired {
		return fmt.Errorf("restricted browser runtime requires a literal loopback target, got %q", origin.Host)
	}
	return nil
}

func browserRuntimeFingerprint(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var canonical any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return ""
	}
	if object, ok := canonical.(map[string]any); ok {
		delete(object, "fingerprint")
	}
	raw, err = json.Marshal(canonical)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
