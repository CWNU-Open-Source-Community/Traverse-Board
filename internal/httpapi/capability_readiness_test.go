package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
)

func TestRunCapabilityReadinessHTTPProjectsSameStableGoFacts(t *testing.T) {
	fixture := newAPIFixture(t)
	path := "/api/v1/runs/" + fixture.run.ID + "/capability-readiness"
	response := performSessionMessageRequest(t, fixture.api, http.MethodGet,
		path, testAccessToken, "", "", nil)
	var view RunCapabilityReadinessView
	decodeDataStatus(t, response, http.StatusOK, &view)
	if view.ProtocolVersion != application.RunCapabilityReadinessProtocolVersion ||
		view.RunID != fixture.run.ID || view.CapabilityGrant || len(view.Permissions) != 5 ||
		len(view.Profiles) != 3 || len(view.Interactions) != 4 ||
		len(view.BrowserCDPPermissions) != 2 || len(view.Presets) != 1 {
		t.Fatalf("unexpected HTTP readiness projection: %#v", view)
	}
	preview := readinessHTTPOption(t, view.Profiles, "preview")
	if !preview.Selected || preview.Selectable || !preview.RuntimeAvailable ||
		!containsString(preview.BlockedBy, string(application.CapabilityBlockerRunNotQuiescent)) ||
		!containsString(preview.BlockedBy, string(application.CapabilityBlockerExecutionLeaseActive)) {
		t.Fatalf("HTTP readiness conflated selected and unavailable: %#v", preview)
	}
	local := readinessHTTPOption(t, view.Profiles, "local")
	if local.RuntimeAvailable ||
		!containsString(local.BlockedBy, string(application.CapabilityBlockerSandboxUnproven)) {
		t.Fatalf("HTTP readiness claimed an unproven Local runtime: %#v", local)
	}
	raw := strings.ToLower(response.Body.String())
	for _, forbidden := range []string{"root_path", "docker_socket", "endpoint_fingerprint",
		"profile_path", "credential", "lease_id", "owner_id", "operation_key"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("readiness projection exposed private field %q: %s", forbidden, raw)
		}
	}
	assertAPIError(t, performSessionMessageRequest(t, fixture.api, http.MethodGet,
		path+"?debug=true", testAccessToken, "", "", nil),
		http.StatusBadRequest, "INVALID_ARGUMENT")
}

func readinessHTTPOption(t *testing.T, options []CapabilityReadinessOptionView,
	value string,
) CapabilityReadinessOptionView {
	t.Helper()
	for _, option := range options {
		if option.Value == value {
			return option
		}
	}
	t.Fatalf("HTTP readiness option %q is missing", value)
	return CapabilityReadinessOptionView{}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
