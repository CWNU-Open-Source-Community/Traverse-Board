package browserruntime

import (
	"errors"
	"time"

	"cyberagent-workbench/internal/domain"
)

const FullCDPStartAuthorizationProtocolVersion = "browser_full_cdp_start_authorization.v2"

// FullCDPStartAuthorization is the process-launch authorization for the Full
// CDP debug channel. It is independent from the Safe Web start authorization:
// it requires live Full Access or Debug permission and never carries the Safe
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
	ExecutionPermissionSnapshotID string    `json:"execution_permission_snapshot_id"`
	ExecutionPermissionRevision   int64     `json:"execution_permission_revision"`
	ExecutionPermissionMode       string    `json:"execution_permission_mode"`
	ExecutionActivationGeneration uint64    `json:"execution_activation_generation"`
	ExecutionAuthorizationFence   uint64    `json:"execution_authorization_fence"`
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
// CDP channel. It requires live Full Access or Debug permission, the separately
// confirmed Full CDP sub-permission, its installed adapter, and a live launch lease, and it
// never grants the Safe Web network-containment or loopback-navigation flags.
func AuthorizeFullCDPStart(session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease, review BrowserLaunchReview,
	permission domain.RunBrowserCDPPermissionSnapshot,
	executionPermission domain.RunExecutionPermissionSnapshot,
	runtimeCapabilities FullCDPRuntimeCapabilities,
	permissionCapabilities domain.BrowserCDPPermissionRuntimeCapabilities,
	executionCapabilities domain.ExecutionPermissionRuntimeCapabilities,
	executionFence uint64, now time.Time,
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
	if err := executionPermission.Validate(); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	if err := executionCapabilities.Validate(); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	if err := runtimeCapabilities.Validate(); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	if err := validateFullCDPSession(session); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	activationGeneration, executionAllowed :=
		executionCapabilities.FullAccessGeneration(executionPermission)
	if !review.AcceptedForFutureAdapter || permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		!permission.OperatorConfirmed || permission.RunID != session.RunID ||
		!permissionCapabilities.FullDebugEnabled ||
		!runtimeCapabilities.StartEnabled ||
		!runtimeCapabilities.DisposableProfileEnabled ||
		executionPermission.RunID != session.RunID ||
		(executionPermission.Mode != domain.RunExecutionPermissionFullAccess &&
			executionPermission.Mode != domain.RunExecutionPermissionDebug) ||
		!executionAllowed || executionCapabilities.RuntimeAuthority == nil ||
		executionFence == 0 || !executionCapabilities.RuntimeAuthority.
		AllowsRunAuthorizationFence(session.RunID, executionFence) {
		return FullCDPStartAuthorization{}, errors.New(
			"full CDP launch requires live Full Access or Debug authority and its confirmed sub-permission")
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
		ExecutionPermissionSnapshotID: executionPermission.ID,
		ExecutionPermissionRevision:   executionPermission.Revision,
		ExecutionPermissionMode:       string(executionPermission.Mode),
		ExecutionActivationGeneration: activationGeneration,
		ExecutionAuthorizationFence:   executionFence,
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
		acceptance, ownership, attempt, lease, review, permission,
		executionPermission, executionCapabilities); err != nil {
		return FullCDPStartAuthorization{}, err
	}
	return authorization, nil
}

// ValidateFullCDPStartAuthorization re-checks that a Full CDP launch
// authorization still binds the exact live Full Access or Debug permission, session,
// executable, ownership, attempt, lease, and review, and that it carries the
// process-launch authority without any Safe Web containment flag.
func ValidateFullCDPStartAuthorization(authorization FullCDPStartAuthorization,
	session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	attempt BrowserLaunchAttempt, lease BrowserLaunchLease, review BrowserLaunchReview,
	permission domain.RunBrowserCDPPermissionSnapshot,
	executionPermission domain.RunExecutionPermissionSnapshot,
	executionCapabilities domain.ExecutionPermissionRuntimeCapabilities,
) error {
	if err := ValidateBrowserLaunchReview(review, session, identity, acceptance,
		ownership, attempt, lease); err != nil {
		return err
	}
	if err := permission.Validate(); err != nil {
		return err
	}
	if err := executionPermission.Validate(); err != nil {
		return err
	}
	if err := executionCapabilities.Validate(); err != nil {
		return err
	}
	if err := validateFullCDPSession(session); err != nil {
		return err
	}
	activationGeneration, executionAllowed :=
		executionCapabilities.FullAccessGeneration(executionPermission)
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
		authorization.ExecutionPermissionSnapshotID != executionPermission.ID ||
		authorization.ExecutionPermissionRevision != executionPermission.Revision ||
		authorization.ExecutionPermissionMode != string(executionPermission.Mode) ||
		authorization.ExecutionActivationGeneration != activationGeneration ||
		authorization.ExecutionAuthorizationFence == 0 ||
		executionCapabilities.RuntimeAuthority == nil ||
		!executionCapabilities.RuntimeAuthority.AllowsRunAuthorizationFence(
			executionPermission.RunID, authorization.ExecutionAuthorizationFence) ||
		!executionAllowed ||
		authorization.ScopeFingerprint != session.Scope.Fingerprint ||
		authorization.ProfileGeneration != ownership.Generation ||
		permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		!permission.OperatorConfirmed || permission.RunID != session.RunID ||
		executionPermission.RunID != session.RunID ||
		(executionPermission.Mode != domain.RunExecutionPermissionFullAccess &&
			executionPermission.Mode != domain.RunExecutionPermissionDebug) ||
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
