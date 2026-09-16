package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

type standardCodePresetControllerStub struct {
	calls []application.ConfigureStandardCodeRequest
	err   error
}

func (s *standardCodePresetControllerStub) Configure(_ context.Context,
	request application.ConfigureStandardCodeRequest,
) (application.StandardCodePresetResult, error) {
	s.calls = append(s.calls, request)
	if s.err != nil {
		return application.StandardCodePresetResult{}, s.err
	}
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
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			assertStandardCodePresetArrayShape(t, envelope["data"])
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

func TestStandardCodePresetControlEmptyCollectionsEncodeAsArrays(t *testing.T) {
	api := &API{}
	// The application may return nil or allocated empty collections. The wire
	// contract requires arrays in both cases, including a configured result's
	// empty top-level blockers and next steps.
	for _, allocated := range []bool{false, true} {
		result := application.StandardCodePresetResult{
			Status: application.StandardCodeResultConfigured,
			LocalReadiness: application.StandardCodeBackendReadiness{
				Backend: domain.StandardCodeSelectedLocal, Available: true},
			DockerReadiness: application.StandardCodeBackendReadiness{
				Backend: domain.StandardCodeSelectedDocker, Available: true},
		}
		if allocated {
			result.BlockedBy = []application.CapabilityReadinessBlocker{}
			result.NextSteps = []application.StandardCodeNextStep{}
			result.LocalReadiness.BlockedBy = []application.CapabilityReadinessBlocker{}
			result.LocalReadiness.Remediation = []application.CapabilityReadinessRemediation{}
			result.DockerReadiness.BlockedBy = []application.CapabilityReadinessBlocker{}
			result.DockerReadiness.Remediation = []application.CapabilityReadinessRemediation{}
		}
		body, err := json.Marshal(api.standardCodePresetControlView(result))
		if err != nil {
			t.Fatal(err)
		}
		assertStandardCodePresetArrayShape(t, body)
	}
}

func TestStandardCodePresetControlOnlyMarksPermanentlyInvalidatedIntent(t *testing.T) {
	_, api, controller := newStandardCodePresetTestAPI(t)
	path := "/api/v1/runs/run-standard-code-invalidated/standard-code/preset"
	body := `{"version":"standard_code_preset.v1","backend_intent":"auto","confirm_workspace_trust":false}`
	for _, test := range []struct {
		name        string
		err         error
		invalidated bool
	}{
		{"ordinary conflict", apperror.New(apperror.CodeConflict, "Thread binding changed"), false},
		{"obsolete preference", apperror.Wrap(apperror.CodeConflict, "Thread permission changed after preset intent", domain.ErrStandardCodePresetThreadPreferenceChanged), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller.err = test.err
			response := performControlPathRequest(t, api, path,
				"standard-code-invalidated-0001", strings.NewReader(body))
			var envelope errorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusConflict || envelope.Error.MessageQueued != nil ||
				(envelope.Error.OperationKeyInvalidated != nil) != test.invalidated ||
				(test.invalidated && !*envelope.Error.OperationKeyInvalidated) {
				t.Fatalf("unexpected operation outcome marker: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func assertStandardCodePresetArrayShape(t *testing.T, body []byte) {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"blocked_by", "next_steps"} {
		if _, ok := value[field].([]any); !ok {
			t.Fatalf("%s must encode as a JSON array: %s", field, body)
		}
	}
	for _, backend := range []string{"local_readiness", "docker_readiness"} {
		readiness, ok := value[backend].(map[string]any)
		if !ok {
			t.Fatalf("%s must encode as a JSON object: %s", backend, body)
		}
		for _, field := range []string{"blocked_by", "remediation"} {
			if _, ok := readiness[field].([]any); !ok {
				t.Fatalf("%s.%s must encode as a JSON array: %s", backend, field, body)
			}
		}
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
