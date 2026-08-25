package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

type standardCodePresetControllerStub struct {
	calls []application.ConfigureStandardCodeRequest
}

func (s *standardCodePresetControllerStub) Configure(_ context.Context,
	request application.ConfigureStandardCodeRequest,
) (application.StandardCodePresetResult, error) {
	s.calls = append(s.calls, request)
	workspaceID := request.WorkspaceID
	if workspaceID == "" {
		workspaceID = "workspace-standard-code-http"
	}
	selected := domain.StandardCodeSelectedLocal
	reason := domain.StandardCodeReasonAutoLocalReady
	if request.BackendIntent == "docker" {
		selected, reason = domain.StandardCodeSelectedDocker,
			domain.StandardCodeReasonExplicitDocker
	}
	return application.StandardCodePresetResult{
		ProtocolVersion: domain.StandardCodePresetProtocolVersion,
		Status:          application.StandardCodeResultBlocked,
		RunID:           request.RunID,
		WorkspaceID:     workspaceID,
		Action:          domain.StandardCodePresetAction(request.Action),
		BackendIntent:   domain.StandardCodeBackendIntent(request.BackendIntent),
		SelectedBackend: selected,
		SelectionReason: reason,
		LocalReadiness: application.StandardCodeBackendReadiness{
			Backend: domain.StandardCodeSelectedLocal, Available: true,
			BlockedBy:   []application.CapabilityReadinessBlocker{},
			Remediation: []application.CapabilityReadinessRemediation{},
		},
		DockerReadiness: application.StandardCodeBackendReadiness{
			Backend: domain.StandardCodeSelectedDocker, Available: false,
			BlockedBy: []application.CapabilityReadinessBlocker{
				application.CapabilityBlockerDockerUnavailable},
			Remediation: []application.CapabilityReadinessRemediation{
				application.CapabilityRemediationInstallOrStartDocker},
		},
		BlockedBy: []application.CapabilityReadinessBlocker{
			application.CapabilityBlockerWorkspaceUntrusted},
		NextSteps: []application.StandardCodeNextStep{
			application.StandardCodeNextConfirmWorkspaceTrust},
		TrustRequired: true, TrustDigest: strings.Repeat("a", 64),
		Network: "disabled", Credentials: "none", CapabilityGrant: false,
	}, nil
}

func newStandardCodePresetTestAPI(t *testing.T) (*apiFixture,
	*API, *standardCodePresetControllerStub,
) {
	t.Helper()
	fixture := newAPIFixture(t)
	controller := &standardCodePresetControllerStub{}
	api, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		RunControlEnabled: true, ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true,
		},
		StandardCodePresetEnabled:    true,
		StandardCodePresetController: controller,
		AppVersion:                   "standard-code-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture, api, controller
}

func TestStandardCodePresetControlRoutesBindOperatorIntent(t *testing.T) {
	fixture, api, controller := newStandardCodePresetTestAPI(t)
	tests := []struct {
		name, path, body, runID, action string
	}{
		{name: "create", path: StandardCodePresetCreatePath,
			body: `{"version":"standard_code_preset.v1","workspace_id":"` +
				fixture.workspace.ID + `","goal":"implement parser","backend_intent":"auto",` +
				`"confirm_workspace_trust":false}`,
			action: "configure"},
		{name: "existing", path: "/api/v1/runs/run-standard-code-1/standard-code/preset",
			body: `{"version":"standard_code_preset.v1","backend_intent":"auto",` +
				`"confirm_workspace_trust":false}`,
			runID: "run-standard-code-1", action: "configure"},
		{name: "pause", path: "/api/v1/runs/run-standard-code-2/standard-code/pause-and-configure",
			body: `{"version":"standard_code_preset.v1","backend_intent":"docker",` +
				`"confirm_workspace_trust":true,"expected_trust_digest":"` +
				strings.Repeat("b", 64) + `"}`,
			runID: "run-standard-code-2", action: "pause_and_configure"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performControlPathRequest(t, api, test.path,
				"standard-code-http-operation-000"+string(rune('1'+index)),
				strings.NewReader(test.body))
			var view StandardCodePresetControlView
			decodeDataStatus(t, response, http.StatusAccepted, &view)
			if view.ProtocolVersion != domain.StandardCodePresetProtocolVersion ||
				view.Action != domain.StandardCodePresetAction(test.action) ||
				!view.TrustRequired || view.Network != "disabled" ||
				view.Credentials != "none" || view.CapabilityGrant ||
				strings.Contains(response.Body.String(), "bearer") {
				t.Fatalf("view=%+v body=%s", view, response.Body.String())
			}
			call := controller.calls[len(controller.calls)-1]
			if call.RunID != test.runID || call.Action != test.action ||
				call.RequestedBy != "http_control" || call.OperationKey == "" {
				t.Fatalf("bound request=%+v", call)
			}
		})
	}

	capabilities := httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1/api/v1/capabilities", nil)
	capabilities.Host = "127.0.0.1:8765"
	capabilities.RemoteAddr = "127.0.0.1:45000"
	capabilities.Header.Set("Authorization", "Bearer "+testAccessToken)
	capabilityResponse := httptest.NewRecorder()
	api.ServeHTTP(capabilityResponse, capabilities)
	var runtime RuntimeCapabilitiesView
	decodeDataStatus(t, capabilityResponse, http.StatusOK, &runtime)
	if !runtime.StandardCodePresetEnabled {
		t.Fatal("runtime capabilities omitted Standard Code preset control")
	}
}

func TestStandardCodePresetControlFailsClosedAtHTTPBoundary(t *testing.T) {
	_, api, controller := newStandardCodePresetTestAPI(t)
	body := `{"version":"standard_code_preset.v1","backend_intent":"auto",` +
		`"confirm_workspace_trust":false}`
	request := func(token, key, contentType, target, value string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+target,
			strings.NewReader(value))
		req.Host, req.RemoteAddr = "127.0.0.1:8765", "127.0.0.1:45000"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, req)
		return response
	}
	path := "/api/v1/runs/run-standard-code-boundary/standard-code/preset"
	key := "standard-code-http-boundary-0001"
	assertAPIError(t, request(testAccessToken, key, "application/json", path, body),
		http.StatusUnauthorized, "POLICY_DENIED")
	assertAPIError(t, request(testControlToken, "", "application/json", path, body),
		http.StatusBadRequest, "INVALID_ARGUMENT")
	assertAPIError(t, request(testControlToken, key, "text/plain", path, body),
		http.StatusUnsupportedMediaType, "INVALID_ARGUMENT")
	assertAPIError(t, request(testControlToken, key, "application/json", path+"?force=true", body),
		http.StatusBadRequest, "INVALID_ARGUMENT")
	assertAPIError(t, request(testControlToken, key, "application/json", path,
		strings.TrimSuffix(body, "}")+`,"backend_intent":"docker"}`),
		http.StatusBadRequest, "INVALID_ARGUMENT")
	assertAPIError(t, request(testControlToken, key, "application/json", path,
		strings.TrimSuffix(body, "}")+`,"requester":"model"}`),
		http.StatusBadRequest, "INVALID_ARGUMENT")
	if len(controller.calls) != 0 {
		t.Fatalf("rejected HTTP inputs reached controller: %+v", controller.calls)
	}
}
