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

func fullCDPExecutionFacts(t *testing.T, session SessionPlan) (
	domain.RunExecutionPermissionSnapshot,
	domain.ExecutionPermissionRuntimeCapabilities, uint64,
) {
	t.Helper()
	now := time.Now().UTC().Round(time.Millisecond)
	run := domain.Run{ID: session.RunID, MissionID: "mission-full-cdp",
		Status: domain.RunCreated, CreatedAt: now, UpdatedAt: now}
	mission := domain.Mission{ID: run.MissionID, CreatedAt: now}
	initial, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"execution-permission-initial", run, mission, "runtime-test-operator", now)
	if err != nil {
		t.Fatal(err)
	}
	full, err := initial.Next("execution-permission-full",
		domain.RunExecutionPermissionFullAccess, true, "runtime-test-operator",
		"confirm full access for CDP", now)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	if _, err := authority.ActivateRunFullAccess(full); err != nil {
		t.Fatal(err)
	}
	fence, err := authority.IssueRunAuthorizationFence(session.RunID)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	return full, capabilities, fence
}

func TestAuthorizeFullCDPRequiresMaximumAccessDebugAndConfirmation(t *testing.T) {
	session, identity, acceptance, full := fullCDPAuthorizationFacts(t)
	runtimeCapabilities := FullCDPRuntimeCapabilities{
		StartEnabled: true, DisposableProfileEnabled: true, TransportEnabled: true,
	}
	permissionCapabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true,
	}
	executionPermission, executionCapabilities, executionFence :=
		fullCDPExecutionFacts(t, session)
	now := time.Now().UTC().Add(time.Second)

	authorization, err := AuthorizeFullCDP(session, identity, acceptance, full,
		executionPermission, runtimeCapabilities, permissionCapabilities,
		executionCapabilities, executionFence, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if !authorization.RequestCaptureAuthorized || !authorization.RequestMutationAuthorized ||
		!authorization.RequestReplayAuthorized || !authorization.CookieAccessAuthorized ||
		!authorization.ArbitraryMethodAuthorized || authorization.InstructionAuthorized ||
		!authorization.Confirmed || authorization.ExpiresAt.After(now.Add(FullCDPCapabilityTTL)) {
		t.Fatalf("full CDP authorization has wrong sensitive boundary: %#v", authorization)
	}
	if err := ValidateFullCDPAuthorization(authorization, session, identity, full,
		executionPermission, executionCapabilities); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeFullCDPRejectsWithoutDebugMaximumAccessOrConfirmation(t *testing.T) {
	session, identity, acceptance, full := fullCDPAuthorizationFacts(t)
	runtimeCapabilities := FullCDPRuntimeCapabilities{
		StartEnabled: true, DisposableProfileEnabled: true, TransportEnabled: true,
	}
	permissionCapabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true,
	}
	executionPermission, executionCapabilities, executionFence :=
		fullCDPExecutionFacts(t, session)
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
			executionPermission, runtimeCapabilities, permissionCapabilities,
			executionCapabilities, executionFence, true, now); err == nil {
			t.Fatal("Safe Web session unexpectedly authorized full CDP")
		}
	})
	t.Run("restricted permission", func(t *testing.T) {
		if _, err := AuthorizeFullCDP(session, identity, acceptance,
			permissionForFullCDP(t, session), executionPermission,
			runtimeCapabilities, permissionCapabilities, executionCapabilities,
			executionFence, true, now); err == nil {
			t.Fatal("restricted permission unexpectedly authorized full CDP")
		}
	})
	t.Run("not confirmed", func(t *testing.T) {
		if _, err := AuthorizeFullCDP(session, identity, acceptance, full,
			executionPermission, runtimeCapabilities, permissionCapabilities,
			executionCapabilities, executionFence, false, now); err == nil {
			t.Fatal("unconfirmed full CDP unexpectedly authorized")
		}
	})
	t.Run("full debug gate disabled", func(t *testing.T) {
		if _, err := AuthorizeFullCDP(session, identity, acceptance, full,
			executionPermission, runtimeCapabilities,
			domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true},
			executionCapabilities, executionFence, true, now); err == nil {
			t.Fatal("disabled full-debug gate unexpectedly authorized full CDP")
		}
	})
	t.Run("restricted CDP runtime disabled", func(t *testing.T) {
		if _, err := AuthorizeFullCDP(session, identity, acceptance, full,
			executionPermission,
			FullCDPRuntimeCapabilities{
				StartEnabled: true, DisposableProfileEnabled: true,
			}, permissionCapabilities, executionCapabilities, executionFence,
			true, now); err == nil {
			t.Fatal("disabled restricted-CDP runtime unexpectedly authorized full CDP")
		}
	})
	t.Run("execution authority revoked", func(t *testing.T) {
		executionCapabilities.RuntimeAuthority.RevokeRun(session.RunID)
		if _, err := AuthorizeFullCDP(session, identity, acceptance, full,
			executionPermission, runtimeCapabilities, permissionCapabilities,
			executionCapabilities, executionFence, true, now); err == nil {
			t.Fatal("revoked execution authority unexpectedly authorized full CDP")
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
	runtimeCapabilities := FullCDPRuntimeCapabilities{
		StartEnabled: true, DisposableProfileEnabled: true, TransportEnabled: true,
	}
	executionPermission, executionCapabilities, executionFence :=
		fullCDPExecutionFacts(t, session)
	authorization, err := AuthorizeFullCDP(session, identity, acceptance, full,
		executionPermission, runtimeCapabilities,
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true,
			FullDebugEnabled: true}, executionCapabilities, executionFence,
		true, time.Now().UTC().Add(time.Second))
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
				identity, full, executionPermission, executionCapabilities); err == nil {
				t.Fatal("tampered full CDP authorization was accepted")
			}
		})
	}
}
