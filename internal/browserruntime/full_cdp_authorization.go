package browserruntime

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"cyberagent-workbench/internal/domain"
)

const (
	FullCDPAuthorizationProtocolVersion = "browser_full_cdp_authorization.v2"
	FullCDPCapabilityTTL                = 5 * time.Minute
)

// FullCDPRuntimeCapabilities are the independent, process-local production
// gates for the highly-sensitive Full CDP path. They deliberately do not reuse
// Safe Web's WFP/network-containment capability bundle.
type FullCDPRuntimeCapabilities struct {
	StartEnabled             bool `json:"start_enabled"`
	DisposableProfileEnabled bool `json:"disposable_profile_enabled"`
	TransportEnabled         bool `json:"transport_enabled"`
}

func (capabilities FullCDPRuntimeCapabilities) Validate() error {
	if capabilities.StartEnabled != capabilities.DisposableProfileEnabled ||
		capabilities.TransportEnabled != capabilities.StartEnabled {
		return errors.New(
			"full CDP start, disposable Profile, and transport gates must match")
	}
	return nil
}

// FullCDPAuthorization is a short-lived, per-call, highly-sensitive CDP
// authorization. It is fully independent from the Safe Web restricted
// authorization: Safe Web evidence or authorization never grants Full CDP, and
// Full CDP never inherits the Safe Web boundary. It binds the Run, Workspace,
// accepted browser executable, permission revision, and session scope.
type FullCDPAuthorization struct {
	ProtocolVersion               string    `json:"protocol_version"`
	RunID                         string    `json:"run_id"`
	WorkspaceID                   string    `json:"workspace_id"`
	ExecutableIdentityFingerprint string    `json:"executable_identity_fingerprint"`
	PermissionSnapshotID          string    `json:"permission_snapshot_id"`
	PermissionRevision            int64     `json:"permission_revision"`
	ExecutionPermissionSnapshotID string    `json:"execution_permission_snapshot_id"`
	ExecutionPermissionRevision   int64     `json:"execution_permission_revision"`
	ExecutionPermissionMode       string    `json:"execution_permission_mode"`
	ExecutionActivationGeneration uint64    `json:"execution_activation_generation"`
	ExecutionAuthorizationFence   uint64    `json:"execution_authorization_fence"`
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
	Confirmed                     bool      `json:"confirmed"`
	IssuedAt                      time.Time `json:"issued_at"`
	ExpiresAt                     time.Time `json:"expires_at"`
	Fingerprint                   string    `json:"fingerprint"`
}

// AuthorizeFullCDP issues a highly-sensitive CDP authorization only when the
// Run uses live Full Access or Debug permission (with operator confirmation),
// the process installed the Full CDP adapter, and the caller supplies an exact
// per-call confirmation. The capability is TTL-bounded and bound to the Run,
// Workspace, executable identity, permission revision, and scope; it never
// authorizes webpage-instruction elevation.
func AuthorizeFullCDP(session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, permission domain.RunBrowserCDPPermissionSnapshot,
	executionPermission domain.RunExecutionPermissionSnapshot,
	runtimeCapabilities FullCDPRuntimeCapabilities,
	permissionCapabilities domain.BrowserCDPPermissionRuntimeCapabilities,
	executionCapabilities domain.ExecutionPermissionRuntimeCapabilities,
	executionFence uint64, confirmed bool, now time.Time,
) (FullCDPAuthorization, error) {
	if err := session.Validate(); err != nil {
		return FullCDPAuthorization{}, err
	}
	if err := ValidateBrowserExecutableIdentity(identity); err != nil {
		return FullCDPAuthorization{}, err
	}
	if err := ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return FullCDPAuthorization{}, err
	}
	if err := validateFullCDPSession(session); err != nil {
		return FullCDPAuthorization{}, err
	}
	if err := runtimeCapabilities.Validate(); err != nil {
		return FullCDPAuthorization{}, err
	}
	if err := permissionCapabilities.Validate(); err != nil {
		return FullCDPAuthorization{}, err
	}
	if err := executionPermission.Validate(); err != nil {
		return FullCDPAuthorization{}, err
	}
	if err := executionCapabilities.Validate(); err != nil {
		return FullCDPAuthorization{}, err
	}
	now = now.UTC()
	activationGeneration, executionAllowed :=
		executionCapabilities.FullAccessGeneration(executionPermission)
	if permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		!permission.OperatorConfirmed || permission.RunID != session.RunID ||
		!permissionCapabilities.FullDebugEnabled || !runtimeCapabilities.TransportEnabled ||
		executionPermission.RunID != session.RunID ||
		(executionPermission.Mode != domain.RunExecutionPermissionFullAccess &&
			executionPermission.Mode != domain.RunExecutionPermissionDebug) ||
		!executionAllowed || executionCapabilities.RuntimeAuthority == nil ||
		executionFence == 0 || !executionCapabilities.RuntimeAuthority.
		AllowsRunAuthorizationFence(session.RunID, executionFence) ||
		!confirmed || now.IsZero() {
		return FullCDPAuthorization{}, errors.New(
			"full CDP requires live Full Access or Debug authority, its confirmed sub-permission, per-call confirmation, and a restricted-CDP runtime")
	}
	expiresAt := now.Add(FullCDPCapabilityTTL)
	authorization := FullCDPAuthorization{
		ProtocolVersion:               FullCDPAuthorizationProtocolVersion,
		RunID:                         session.RunID,
		WorkspaceID:                   session.WorkspaceID,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		PermissionSnapshotID:          permission.ID,
		PermissionRevision:            permission.Revision,
		ExecutionPermissionSnapshotID: executionPermission.ID,
		ExecutionPermissionRevision:   executionPermission.Revision,
		ExecutionPermissionMode:       string(executionPermission.Mode),
		ExecutionActivationGeneration: activationGeneration,
		ExecutionAuthorizationFence:   executionFence,
		ScopeFingerprint:              session.Scope.Fingerprint,
		NavigateAuthorized:            true,
		DOMMetadataAuthorized:         true,
		ScreenshotAuthorized:          true,
		RequestCaptureAuthorized:      true,
		RequestMutationAuthorized:     true,
		RequestReplayAuthorized:       true,
		CookieAccessAuthorized:        true,
		ArbitraryMethodAuthorized:     true,
		InstructionAuthorized:         false,
		Confirmed:                     true,
		IssuedAt:                      now,
		ExpiresAt:                     expiresAt,
	}
	authorization.Fingerprint = browserRuntimeFingerprint(authorization)
	if err := ValidateFullCDPAuthorization(authorization, session, identity, permission,
		executionPermission, executionCapabilities); err != nil {
		return FullCDPAuthorization{}, err
	}
	return authorization, nil
}

// ValidateFullCDPAuthorization re-checks that a Full CDP authorization still
// binds the exact live Full Access or Debug permission, session scope, executable
// identity, Run, and Workspace, and that its capability was confirmed,
// TTL-bounded, and never grants webpage-instruction elevation.
func ValidateFullCDPAuthorization(authorization FullCDPAuthorization,
	session SessionPlan, identity BrowserExecutableIdentity,
	permission domain.RunBrowserCDPPermissionSnapshot,
	executionPermission domain.RunExecutionPermissionSnapshot,
	executionCapabilities domain.ExecutionPermissionRuntimeCapabilities,
) error {
	if err := session.Validate(); err != nil {
		return err
	}
	if err := ValidateBrowserExecutableIdentity(identity); err != nil {
		return err
	}
	if err := validateFullCDPSession(session); err != nil {
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
	activationGeneration, executionAllowed :=
		executionCapabilities.FullAccessGeneration(executionPermission)
	if authorization.ProtocolVersion != FullCDPAuthorizationProtocolVersion ||
		authorization.RunID != session.RunID ||
		authorization.WorkspaceID != session.WorkspaceID ||
		authorization.ExecutableIdentityFingerprint != identity.Fingerprint ||
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
		permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		!permission.OperatorConfirmed || permission.RunID != session.RunID ||
		executionPermission.RunID != session.RunID ||
		(executionPermission.Mode != domain.RunExecutionPermissionFullAccess &&
			executionPermission.Mode != domain.RunExecutionPermissionDebug) ||
		!authorization.NavigateAuthorized || !authorization.DOMMetadataAuthorized ||
		!authorization.ScreenshotAuthorized || !authorization.RequestCaptureAuthorized ||
		!authorization.RequestMutationAuthorized || !authorization.RequestReplayAuthorized ||
		!authorization.CookieAccessAuthorized || !authorization.ArbitraryMethodAuthorized ||
		authorization.InstructionAuthorized || !authorization.Confirmed ||
		authorization.IssuedAt.IsZero() || authorization.ExpiresAt.IsZero() ||
		!authorization.ExpiresAt.After(authorization.IssuedAt) ||
		authorization.ExpiresAt.Sub(authorization.IssuedAt) > FullCDPCapabilityTTL ||
		authorization.Fingerprint != browserRuntimeFingerprint(authorization) {
		return errors.New("full CDP authorization lost its highly-sensitive boundary")
	}
	return nil
}

// validateFullCDPSession accepts only the CTF lab profile (full request
// interception/mutation/replay/cookie tools) with a single literal loopback
// target and direct proxy, while still forbidding any origin-policy or
// certificate relaxation. Full CDP never disables browser security.
func validateFullCDPSession(session SessionPlan) error {
	if err := session.Validate(); err != nil {
		return err
	}
	if session.ProfileID != ProfileCTFLab || session.Proxy.Mode != ProxyModeDirect ||
		len(session.Scope.Origins) != 1 ||
		!session.Features.RequestInterception || !session.Features.RequestMutation ||
		!session.Features.RequestReplay || !session.Features.CookieEditing ||
		session.Features.RelaxOriginPolicy || session.Features.AllowInsecureContent ||
		session.Features.IgnoreCertificateErrors {
		return errors.New(
			"full CDP requires the CTF lab profile with full request tools and preserved security")
	}
	origin := session.Scope.Origins[0]
	address, err := netip.ParseAddr(origin.Host)
	if err != nil || !address.Unmap().IsLoopback() || origin.HostClass != HostClassLoopback ||
		origin.ResolutionCheckRequired {
		return fmt.Errorf("full CDP requires a literal loopback target, got %q", origin.Host)
	}
	return nil
}

// ValidateFullCDPSessionPlan validates the public-input-derived session before
// any durable browser launch preparation is recorded.
func ValidateFullCDPSessionPlan(session SessionPlan) error {
	return validateFullCDPSession(session)
}
