package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestRunBrowserCDPPermissionControlRequiresDebugAndExactConfirmation(t *testing.T) {
	fixture := newAPIFixture(t)
	_, run, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{
			Goal: "select browser CDP permission through HTTP", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	permissionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true,
	}
	closed, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled:  true,
		ExecutionPermissionCapabilities:    permissionCapabilities,
		BrowserCDPPermissionControlEnabled: true,
		BrowserCDPPermissionCapabilities: domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/runs/" + run.ID + "/browser-cdp-permission"
	body := `{"mode":"full_debug","confirm_full_cdp_debug":true}`
	denied := performControlPathRequest(t, closed, path,
		"http-browser-cdp-closed-0001", strings.NewReader(body))
	assertAPIError(t, denied, http.StatusForbidden, "POLICY_DENIED")

	open, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled:  true,
		ExecutionPermissionCapabilities:    permissionCapabilities,
		BrowserCDPPermissionControlEnabled: true,
		BrowserCDPPermissionCapabilities: domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled: true, FullDebugEnabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	executionPath := "/api/v1/runs/" + run.ID + "/execution-permission"
	debug := performControlPathRequest(t, open, executionPath,
		"http-browser-cdp-debug-0001",
		strings.NewReader(`{"mode":"debug","confirm_debug_access":true}`))
	var debugSelection RunExecutionPermissionControlView
	decodeDataStatus(t, debug, http.StatusAccepted, &debugSelection)
	if debugSelection.ExecutionPermission.Mode != string(domain.RunExecutionPermissionDebug) {
		t.Fatalf("execution permission = %+v", debugSelection.ExecutionPermission)
	}

	malformed := performControlPathRequest(t, open, path,
		"http-browser-cdp-malformed-0001",
		strings.NewReader(`{"mode":"full_debug"}`))
	assertAPIError(t, malformed, http.StatusBadRequest, "INVALID_ARGUMENT")
	disabled := performControlPathRequest(t, open, path,
		"http-browser-cdp-disabled-0001", strings.NewReader(`{"mode":"restricted"}`))
	var disabledSelection RunBrowserCDPPermissionControlView
	decodeDataStatus(t, disabled, http.StatusAccepted, &disabledSelection)
	if disabledSelection.BrowserCDPPermission.Mode !=
		string(domain.RunBrowserCDPPermissionRestricted) {
		t.Fatalf("browser CDP disable = %+v", disabledSelection)
	}

	first := performControlPathRequest(t, open, path,
		"http-browser-cdp-open-0001", strings.NewReader(body))
	var selected RunBrowserCDPPermissionControlView
	decodeDataStatus(t, first, http.StatusAccepted, &selected)
	permission := selected.BrowserCDPPermission
	if selected.Replayed || permission.Mode != string(domain.RunBrowserCDPPermissionFullDebug) ||
		!permission.RequestCaptureAllowed || !permission.RequestMutationAllowed ||
		!permission.RequestReplayAllowed || !permission.CookieAccessAllowed ||
		!permission.ArbitraryMethodAllowed || !permission.RuntimeGateAvailable ||
		!permission.Runtime.FullDebugEnabled || !permission.Runtime.ExecutionDebugSelected ||
		permission.TransportEnabled || permission.BrowserStartAuthorized ||
		permission.RuntimeAuthorized || permission.CapabilityGrant {
		t.Fatalf("HTTP browser CDP selection escaped its boundary: %+v", selected)
	}
	replay := performControlPathRequest(t, open, path,
		"http-browser-cdp-open-0001", strings.NewReader(body))
	var replayed RunBrowserCDPPermissionControlView
	decodeDataStatus(t, replay, http.StatusAccepted, &replayed)
	if !replayed.Replayed || replayed.BrowserCDPPermission.Revision != permission.Revision {
		t.Fatalf("HTTP browser CDP replay changed result: %+v", replayed)
	}
}
