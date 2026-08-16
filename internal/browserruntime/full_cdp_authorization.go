package browserruntime

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"cyberagent-workbench/internal/domain"
)

const (
	FullCDPAuthorizationProtocolVersion = "browser_full_cdp_authorization.v1"
	FullCDPCapabilityTTL                = 5 * time.Minute
)

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
// Run uses the maximum-access debug permission (with operator confirmation),
// the process enabled the full-debug gate, and the caller supplies an exact
// per-call confirmation. The capability is TTL-bounded and bound to the Run,
// Workspace, executable identity, permission revision, and scope; it never
// authorizes webpage-instruction elevation.
func AuthorizeFullCDP(session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, permission domain.RunBrowserCDPPermissionSnapshot,
	runtimeCapabilities ProductionRuntimeCapabilities,
	permissionCapabilities domain.BrowserCDPPermissionRuntimeCapabilities,
	confirmed bool, now time.Time,
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
	now = now.UTC()
	if permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		!permission.OperatorConfirmed || permission.RunID != session.RunID ||
		!permissionCapabilities.FullDebugEnabled || !runtimeCapabilities.RestrictedCDPEnabled ||
		!confirmed || now.IsZero() {
		return FullCDPAuthorization{}, errors.New(
			"full CDP debug requires maximum-access debug permission, per-call confirmation, and a live restricted-CDP runtime")
	}
	expiresAt := now.Add(FullCDPCapabilityTTL)
	authorization := FullCDPAuthorization{
		ProtocolVersion:               FullCDPAuthorizationProtocolVersion,
		RunID:                         session.RunID,
		WorkspaceID:                   session.WorkspaceID,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		PermissionSnapshotID:          permission.ID,
		PermissionRevision:            permission.Revision,
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
	if err := ValidateFullCDPAuthorization(authorization, session, identity, permission); err != nil {
		return FullCDPAuthorization{}, err
	}
	return authorization, nil
}

// ValidateFullCDPAuthorization re-checks that a Full CDP authorization still
// binds the maximum-access debug permission, session scope, executable
// identity, Run, and Workspace, and that its capability was confirmed,
// TTL-bounded, and never grants webpage-instruction elevation.
func ValidateFullCDPAuthorization(authorization FullCDPAuthorization,
	session SessionPlan, identity BrowserExecutableIdentity,
	permission domain.RunBrowserCDPPermissionSnapshot,
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
	if authorization.ProtocolVersion != FullCDPAuthorizationProtocolVersion ||
		authorization.RunID != session.RunID ||
		authorization.WorkspaceID != session.WorkspaceID ||
		authorization.ExecutableIdentityFingerprint != identity.Fingerprint ||
		authorization.PermissionSnapshotID != permission.ID ||
		authorization.PermissionRevision != permission.Revision ||
		authorization.ScopeFingerprint != session.Scope.Fingerprint ||
		permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		!permission.OperatorConfirmed || permission.RunID != session.RunID ||
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
