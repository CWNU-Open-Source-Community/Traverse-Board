package httpapi

import (
	"net/http"
	"testing"
)

func TestExtensionOnboardingCapabilitiesFollowActualControllerAndGate(t *testing.T) {
	f, _, _, _ := newExtensionOnboardingFixture(t)
	assertCapabilities := func(wantMCP, wantPlugin, wantLSP bool) {
		t.Helper()
		var view ExtensionInventoryView
		decodeDataStatus(t, f.get(t, ExtensionInventoryPath), http.StatusOK, &view)
		if view.Onboarding == nil || view.Onboarding.MCPRegistration != wantMCP ||
			view.Onboarding.PluginImport != wantPlugin || view.Onboarding.LSPConfiguration != wantLSP {
			t.Fatalf("onboarding availability does not match this backend: %+v", view.Onboarding)
		}
	}
	assertCapabilities(true, true, false)
	f.api.codeIntelController = newCodeIntelOpenAPITestController(t, f)
	assertCapabilities(true, true, true)
	f.api.extensionControlEnabled = false
	assertCapabilities(false, false, false)
	f.api.extensionControlEnabled = true
	f.api.extensionController = &extensionControllerStub{}
	assertCapabilities(false, false, true)
	response := f.get(t, ExtensionInventoryPath+"?workspace_id="+f.workspace.ID)
	assertAPIError(t, response, http.StatusServiceUnavailable, "UNAVAILABLE")
}

func TestExtensionOnboardingInventoryRejectsAmbiguousQueries(t *testing.T) {
	f, _, _, _ := newExtensionOnboardingFixture(t)
	for _, query := range []string{
		"?workspace_id=a&workspace_id=b", "?run_id=a&run_id=b", "?unknown=value",
		"?workspace_id=", "?run_id=",
	} {
		assertAPIError(t, f.get(t, ExtensionInventoryPath+query), http.StatusBadRequest, "INVALID_ARGUMENT")
	}
}
