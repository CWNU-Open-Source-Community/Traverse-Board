package browserruntime

import (
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func fullCDPLaunchFacts(t *testing.T) (SessionPlan, BrowserExecutableIdentity,
	BrowserAcceptanceCandidate, ProfileOwnershipPlan, BrowserLaunchAttempt,
	BrowserLaunchLease, BrowserLaunchReview, domain.RunBrowserCDPPermissionSnapshot,
) {
	t.Helper()
	session, identity, acceptance, permission := fullCDPAuthorizationFacts(t)
	now := time.Now().UTC().Round(time.Millisecond)
	ownership, err := BuildProfileOwnershipPlan(session, identity,
		filepath.Join(directTestPath(t, t.TempDir()), ProfileRuntimeRootName))
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := BuildBrowserLaunchAttempt(session, identity, acceptance, ownership,
		"browser-attempt-full-cdp", now)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := BuildBrowserLaunchLease(attempt, "browser-lease-full-cdp",
		"browser-full-cdp-worker", now, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	review, err := BuildBrowserLaunchReview(session, identity, acceptance, ownership,
		attempt, lease, "browser-review-full-cdp", BrowserLaunchReviewAcceptCandidate,
		"independent-runtime-operator", "browser-review-full-cdp-operation", "",
		now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return session, identity, acceptance, ownership, attempt, lease, review, permission
}

func TestAuthorizeFullCDPStartRequiresMaximumAccessDebug(t *testing.T) {
	session, identity, acceptance, ownership, attempt, lease, review,
		permission := fullCDPLaunchFacts(t)
	runtimeCapabilities := ProductionRuntimeCapabilities{
		SafeWebStartEnabled: true, DisposableProfileEnabled: true,
		NetworkContainmentEnabled: true, RestrictedCDPEnabled: true,
	}
	permissionCapabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true,
	}
	now := time.Now().UTC().Add(2 * time.Second)

	authorization, err := AuthorizeFullCDPStart(session, identity, acceptance,
		ownership, attempt, lease, review, permission, runtimeCapabilities,
		permissionCapabilities, now)
	if err != nil {
		t.Fatal(err)
	}
	if !authorization.ProcessStartAuthorized || !authorization.ProcessTerminationAuthorized ||
		!authorization.ProfileCreateAuthorized || !authorization.ProfileReleaseAuthorized ||
		!authorization.ExactOwnedCleanupAuthorized {
		t.Fatalf("full CDP start authorization has wrong process authority: %#v", authorization)
	}
	if err := ValidateFullCDPStartAuthorization(authorization, session, identity,
		acceptance, ownership, attempt, lease, review, permission); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeFullCDPStartRejectsRestrictedPermission(t *testing.T) {
	session, identity, acceptance, ownership, attempt, lease, review,
		_ := fullCDPLaunchFacts(t)
	runtimeCapabilities := ProductionRuntimeCapabilities{
		SafeWebStartEnabled: true, DisposableProfileEnabled: true,
		NetworkContainmentEnabled: true, RestrictedCDPEnabled: true,
	}
	permissionCapabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true,
	}
	restricted := permissionForFullCDP(t, session)
	now := time.Now().UTC().Add(2 * time.Second)
	if _, err := AuthorizeFullCDPStart(session, identity, acceptance, ownership,
		attempt, lease, review, restricted, runtimeCapabilities,
		permissionCapabilities, now); err == nil {
		t.Fatal("restricted permission unexpectedly authorized a full CDP launch")
	}
}
