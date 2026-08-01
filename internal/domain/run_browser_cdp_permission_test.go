package domain

import (
	"testing"
	"time"
)

func TestRunBrowserCDPPermissionModesAreClosedAndNonAuthorizing(t *testing.T) {
	now := time.Now().UTC()
	mission := Mission{ID: "mission-browser-cdp", CreatedAt: now}
	run := Run{ID: "run-browser-cdp", MissionID: mission.ID,
		Status: RunCreated, CreatedAt: now}
	initial, err := NewInitialRunBrowserCDPPermissionSnapshot(
		"browser-cdp-initial", run, mission, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Mode != RunBrowserCDPPermissionRestricted ||
		!initial.NavigateAllowed || !initial.DOMSnapshotAllowed ||
		!initial.ScreenshotAllowed || initial.RequestCaptureAllowed ||
		initial.RequestMutationAllowed || initial.RequestReplayAllowed ||
		initial.CookieAccessAllowed || initial.ArbitraryMethodAllowed ||
		initial.OperatorConfirmed || initial.TransportEnabled ||
		initial.BrowserStartAuthorized || initial.RuntimeAuthorized ||
		initial.CapabilityGrant {
		t.Fatalf("unexpected restricted CDP permission: %+v", initial)
	}
	full, err := initial.Next("browser-cdp-full",
		RunBrowserCDPPermissionFullDebug, true, "test_operator",
		"operator selected complete debug CDP", now)
	if err != nil {
		t.Fatal(err)
	}
	if !full.RequestCaptureAllowed || !full.RequestMutationAllowed ||
		!full.RequestReplayAllowed || !full.CookieAccessAllowed ||
		!full.ArbitraryMethodAllowed || full.RiskTier != ExecutionRiskHigh ||
		full.RequiredGate != BrowserCDPPermissionGateFullDebug ||
		full.TransportEnabled || full.BrowserStartAuthorized ||
		full.RuntimeAuthorized || full.CapabilityGrant {
		t.Fatalf("unexpected full-debug CDP permission: %+v", full)
	}
}

func TestBrowserCDPPermissionRuntimeCapabilitiesRequireControl(t *testing.T) {
	if err := (BrowserCDPPermissionRuntimeCapabilities{
		FullDebugEnabled: true,
	}).Validate(); err == nil {
		t.Fatal("full CDP debug without CDP control was accepted")
	}
	capabilities := BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true,
	}
	if err := capabilities.Validate(); err != nil {
		t.Fatal(err)
	}
	if !capabilities.Allows(RunBrowserCDPPermissionRestricted) ||
		!capabilities.Allows(RunBrowserCDPPermissionFullDebug) {
		t.Fatal("complete CDP runtime capabilities did not allow both modes")
	}
}
