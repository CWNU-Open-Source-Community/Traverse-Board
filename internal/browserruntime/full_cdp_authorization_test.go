package browserruntime

import (
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func fullCDPAuthorizationFacts(t *testing.T) (SessionPlan, BrowserExecutableIdentity,
	BrowserAcceptanceCandidate, domain.RunBrowserCDPPermissionSnapshot,
) {
	t.Helper()
	_, identity, acceptance, _ := browserLaunchFixture(t)
	now := time.Now().UTC().Round(time.Millisecond)
	session, err := BuildSessionPlan(NewSessionPlanRequest{
		SessionID: "session-full-cdp", RunID: "run-full-cdp",
		WorkspaceID: "workspace-full-cdp", ProfileID: ProfileCTFLab,
		Targets: []string{"http://127.0.0.1:18080"},
		Features: FeatureRequest{
			InterceptRequests: true, ModifyRequests: true,
			ReplayRequests: true, EditCookies: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := domain.NewInitialRunBrowserCDPPermissionSnapshot(
		"browser-cdp-full", domain.Run{ID: session.RunID,
			MissionID: "mission-full-cdp", Status: domain.RunCreated, CreatedAt: now},
		domain.Mission{ID: "mission-full-cdp", CreatedAt: now},
		"runtime-test-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	full, err := permission.Next("browser-cdp-full-debug",
		domain.RunBrowserCDPPermissionFullDebug, true, "runtime-test-operator",
		"test full debug boundary", now)
	if err != nil {
		t.Fatal(err)
	}
	return session, identity, acceptance, full
}

func TestAuthorizeFullCDPRequiresMaximumAccessDebugAndConfirmation(t *testing.T) {
	session, identity, acceptance, full := fullCDPAuthorizationFacts(t)
	runtimeCapabilities := ProductionRuntimeCapabilities{
		SafeWebStartEnabled: true, DisposableProfileEnabled: true,
		NetworkContainmentEnabled: true, RestrictedCDPEnabled: true,
	}
	permissionCapabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true,
	}
	now := time.Now().UTC().Add(time.Second)

	authorization, err := AuthorizeFullCDP(session, identity, acceptance, full,
		runtimeCapabilities, permissionCapabilities, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if !authorization.RequestCaptureAuthorized || !authorization.RequestMutationAuthorized ||
		!authorization.RequestReplayAuthorized || !authorization.CookieAccessAuthorized ||
		!authorization.ArbitraryMethodAuthorized || authorization.InstructionAuthorized ||
		!authorization.Confirmed || authorization.ExpiresAt.After(now.Add(FullCDPCapabilityTTL)) {
		t.Fatalf("full CDP authorization has wrong sensitive boundary: %#v", authorization)
	}
	if err := ValidateFullCDPAuthorization(authorization, session, identity, full); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeFullCDPRejectsWithoutDebugMaximumAccessOrConfirmation(t *testing.T) {
	session, identity, acceptance, full := fullCDPAuthorizationFacts(t)
	runtimeCapabilities := ProductionRuntimeCapabilities{
		SafeWebStartEnabled: true, DisposableProfileEnabled: true,
		NetworkContainmentEnabled: true, RestrictedCDPEnabled: true,
	}
	permissionCapabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true,
	}
	now := time.Now().UTC().Add(time.Second)

	t.Run("safe web session", func(t *testing.T) {
		safeSession, err := BuildSessionPlan(NewSessionPlanRequest{
			SessionID: "session-safe-web", RunID: session.RunID,
			WorkspaceID: session.WorkspaceID, ProfileID: ProfileSafeWeb,
			Targets: []string{"http://127.0.0.1:18080"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := AuthorizeFullCDP(safeSession, identity, acceptance, full,
			runtimeCapabilities, permissionCapabilities, true, now); err == nil {
			t.Fatal("Safe Web session unexpectedly authorized full CDP")
		}
	})
	t.Run("restricted permission", func(t *testing.T) {
		if _, err := AuthorizeFullCDP(session, identity, acceptance,
			permissionForFullCDP(t, session), runtimeCapabilities,
			permissionCapabilities, true, now); err == nil {
			t.Fatal("restricted permission unexpectedly authorized full CDP")
		}
	})
	t.Run("not confirmed", func(t *testing.T) {
		if _, err := AuthorizeFullCDP(session, identity, acceptance, full,
			runtimeCapabilities, permissionCapabilities, false, now); err == nil {
			t.Fatal("unconfirmed full CDP unexpectedly authorized")
		}
	})
	t.Run("full debug gate disabled", func(t *testing.T) {
		if _, err := AuthorizeFullCDP(session, identity, acceptance, full,
			runtimeCapabilities,
			domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true},
			true, now); err == nil {
			t.Fatal("disabled full-debug gate unexpectedly authorized full CDP")
		}
	})
	t.Run("restricted CDP runtime disabled", func(t *testing.T) {
		if _, err := AuthorizeFullCDP(session, identity, acceptance, full,
			ProductionRuntimeCapabilities{
				SafeWebStartEnabled: true, DisposableProfileEnabled: true,
				NetworkContainmentEnabled: true,
			}, permissionCapabilities, true, now); err == nil {
			t.Fatal("disabled restricted-CDP runtime unexpectedly authorized full CDP")
		}
	})
}

func permissionForFullCDP(t *testing.T, session SessionPlan) domain.RunBrowserCDPPermissionSnapshot {
	t.Helper()
	now := time.Now().UTC().Round(time.Millisecond)
	permission, err := domain.NewInitialRunBrowserCDPPermissionSnapshot(
		"browser-cdp-restricted", domain.Run{ID: session.RunID,
			MissionID: "mission-full-cdp", Status: domain.RunCreated, CreatedAt: now},
		domain.Mission{ID: "mission-full-cdp", CreatedAt: now},
		"runtime-test-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	return permission
}

func TestValidateFullCDPAuthorizationRejectsTampering(t *testing.T) {
	session, identity, acceptance, full := fullCDPAuthorizationFacts(t)
	runtimeCapabilities := ProductionRuntimeCapabilities{
		SafeWebStartEnabled: true, DisposableProfileEnabled: true,
		NetworkContainmentEnabled: true, RestrictedCDPEnabled: true,
	}
	authorization, err := AuthorizeFullCDP(session, identity, acceptance, full,
		runtimeCapabilities,
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true,
			FullDebugEnabled: true}, true, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for name, tamper := range map[string]func(*FullCDPAuthorization){
		"unconfirmed":  func(a *FullCDPAuthorization) { a.Confirmed = false },
		"instruction":  func(a *FullCDPAuthorization) { a.InstructionAuthorized = true },
		"scope_forged": func(a *FullCDPAuthorization) { a.ScopeFingerprint = strings.Repeat("e", 64) },
		"ttl_extended": func(a *FullCDPAuthorization) { a.ExpiresAt = a.IssuedAt.Add(time.Hour) },
		"run_forged":   func(a *FullCDPAuthorization) { a.RunID = "other-run" },
	} {
		t.Run(name, func(t *testing.T) {
			receipt := authorization
			tamper(&receipt)
			if err := ValidateFullCDPAuthorization(receipt, session,
				identity, full); err == nil {
				t.Fatal("tampered full CDP authorization was accepted")
			}
		})
	}
}
