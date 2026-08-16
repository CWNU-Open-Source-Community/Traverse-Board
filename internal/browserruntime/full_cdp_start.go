package browserruntime

import (
	"errors"
	"time"

	"cyberagent-workbench/internal/domain"
)

const FullCDPStartAuthorizationProtocolVersion = "browser_full_cdp_start_authorization.v1"

// FullCDPStartAuthorization is the process-launch authorization for the Full
// CDP debug channel. It is independent from the Safe Web start authorization:
// it requires the maximum-access debug permission and never carries the Safe
// Web WFP containment or loopback-navigation flags.
type FullCDPStartAuthorization struct {
	ProtocolVersion               string    `json:"protocol_version"`
	SessionPlanFingerprint        string    `json:"session_plan_fingerprint"`
	ExecutableIdentityFingerprint string    `json:"executable_identity_fingerprint"`
	AcceptanceFingerprint         string    `json:"acceptance_fingerprint"`
	ProfileOwnershipFingerprint   string    `json:"profile_ownership_fingerprint"`
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
	IssuedAt                      time.Time `json:"issued_at"`
	StartDeadline                 time.Time `json:"start_deadline"`
	RuntimeDeadline               time.Time `json:"runtime_deadline"`
	Fingerprint                   string    `json:"fingerprint"`
}

// AuthorizeFullCDPStart issues the process-launch authorization for the Full
// CDP channel. It requires the maximum-access debug permission (with operator
// confirmation), the full-debug process gate, and a live launch lease, and it
// never grants the Safe Web network-containment or loopback-navigation flags.
func AuthorizeFullCDPStart(session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease, review BrowserLaunchReview,
	permission domain.RunBrowserCDPPermissionSnapshot,
	runtimeCapabilities ProductionRuntimeCapabilities,
	permissionCapabilities domain.BrowserCDPPermissionRuntimeCapabilities, now time.Time,
) (FullCDPStartAuthorization, error) {
	if err := ValidateBrowserLaunchReview(review, session, identity, acceptance,
		ownership, attempt, lease); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	if err := permission.Validate(); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	if err := permissionCapabilities.Validate(); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	if err := runtimeCapabilities.Validate(); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	if err := validateFullCDPSession(session); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	if !review.AcceptedForFutureAdapter || permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		!permission.OperatorConfirmed || !permissionCapabilities.FullDebugEnabled ||
		!runtimeCapabilities.RestrictedCDPEnabled {
		return FullCDPStartAuthorization{}, errors.New(
			"full CDP launch requires the maximum-access debug permission and the full-debug gate")
	}
	now = now.UTC()
	if now.IsZero() || now.Before(review.CreatedAt) || !now.Before(lease.ExpiresAt) {
		return FullCDPStartAuthorization{}, errors.New("full CDP launch lease is not active")
	}
	runtimeDeadline := now.Add(time.Duration(attempt.MaxRuntimeMS) * time.Millisecond)
	authorization := FullCDPStartAuthorization{
		ProtocolVersion:               FullCDPStartAuthorizationProtocolVersion,
		SessionPlanFingerprint:        session.Fingerprint,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		AcceptanceFingerprint:         acceptance.Fingerprint,
		ProfileOwnershipFingerprint:   ownership.Fingerprint,
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
		IssuedAt:                      now,
		StartDeadline:                 lease.ExpiresAt,
		RuntimeDeadline:               runtimeDeadline,
	}
	authorization.Fingerprint = browserRuntimeFingerprint(authorization)
	if err := ValidateFullCDPStartAuthorization(authorization, session, identity,
		acceptance, ownership, attempt, lease, review, permission); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	return authorization, nil
}

// ValidateFullCDPStartAuthorization re-checks that a Full CDP launch
// authorization still binds the maximum-access debug permission, session,
// executable, ownership, attempt, lease, and review, and that it carries the
// process-launch authority without any Safe Web containment flag.
func ValidateFullCDPStartAuthorization(authorization FullCDPStartAuthorization,
	session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease, review BrowserLaunchReview,
	permission domain.RunBrowserCDPPermissionSnapshot,
) error {
	if err := ValidateBrowserLaunchReview(review, session, identity, acceptance,
		ownership, attempt, lease); err != nil {
		return err
	}
	if err := permission.Validate(); err != nil {
		return err
	}
	if err := validateFullCDPSession(session); err != nil {
		return err
	}
	if authorization.ProtocolVersion != FullCDPStartAuthorizationProtocolVersion ||
		authorization.SessionPlanFingerprint != session.Fingerprint ||
		authorization.ExecutableIdentityFingerprint != identity.Fingerprint ||
		authorization.AcceptanceFingerprint != acceptance.Fingerprint ||
		authorization.ProfileOwnershipFingerprint != ownership.Fingerprint ||
		authorization.AttemptFingerprint != attempt.Fingerprint ||
		authorization.LeaseFingerprint != lease.Fingerprint ||
		authorization.ReviewFingerprint != review.Fingerprint ||
		authorization.PermissionSnapshotID != permission.ID ||
		authorization.PermissionRevision != permission.Revision ||
		authorization.ScopeFingerprint != session.Scope.Fingerprint ||
		authorization.ProfileGeneration != ownership.Generation ||
		permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		!permission.OperatorConfirmed ||
		!authorization.ProcessStartAuthorized || !authorization.ProcessTerminationAuthorized ||
		!authorization.ProfileCreateAuthorized || !authorization.ProfileReleaseAuthorized ||
		!authorization.ExactOwnedCleanupAuthorized ||
		authorization.IssuedAt.IsZero() || authorization.StartDeadline.IsZero() ||
		authorization.RuntimeDeadline.IsZero() ||
		!authorization.StartDeadline.Equal(lease.ExpiresAt) ||
		authorization.IssuedAt.Before(review.CreatedAt) ||
		!authorization.StartDeadline.After(authorization.IssuedAt) ||
		!authorization.RuntimeDeadline.After(authorization.IssuedAt) ||
		authorization.RuntimeDeadline.Sub(authorization.IssuedAt) !=
			time.Duration(attempt.MaxRuntimeMS)*time.Millisecond ||
		authorization.Fingerprint != browserRuntimeFingerprint(authorization) {
		return errors.New("full CDP start authorization lost its independent launch boundary")
	}
	return nil
}
