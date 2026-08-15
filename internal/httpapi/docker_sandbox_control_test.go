package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/sandbox"
)

type dockerSandboxControllerStub struct {
	capabilities sandbox.DockerRuntimeCapabilities
	readiness    sandbox.DockerReadiness
	record       domain.DockerSandboxRecord

	readinessCalls int
	admitCalls     int
	startCalls     int
	cancelCalls    int
	getCalls       int

	lastReadiness application.DockerSandboxReadinessRequest
	lastAdmission application.DockerSandboxAdmissionRequest
	lastStart     application.DockerSandboxStartRequest
	lastCancel    application.DockerSandboxCancelRequest
}

func (stub *dockerSandboxControllerStub) RuntimeCapabilities() (
	sandbox.DockerRuntimeCapabilities, string, error,
) {
	return stub.capabilities, strings.Repeat("a", 64), nil
}

func (stub *dockerSandboxControllerStub) Readiness(_ context.Context,
	request application.DockerSandboxReadinessRequest,
) (sandbox.DockerReadiness, error) {
	stub.readinessCalls++
	stub.lastReadiness = request
	return stub.readiness, nil
}

func (stub *dockerSandboxControllerStub) Admit(_ context.Context,
	request application.DockerSandboxAdmissionRequest,
) (application.DockerSandboxAdmissionResult, error) {
	stub.admitCalls++
	stub.lastAdmission = request
	return application.DockerSandboxAdmissionResult{
		Readiness: stub.readiness, Allowed: false,
		ReasonCode:      sandbox.DockerReadinessReasonFeatureDisabled,
		RemediationCode: sandbox.DockerReadinessRemediationEnableFeature,
	}, nil
}

func (stub *dockerSandboxControllerStub) Get(context.Context, string) (
	domain.DockerSandboxRecord, error,
) {
	stub.getCalls++
	return stub.record, nil
}

func (stub *dockerSandboxControllerStub) Start(_ context.Context,
	request application.DockerSandboxStartRequest,
) (application.DockerSandboxStartResult, error) {
	stub.startCalls++
	stub.lastStart = request
	return application.DockerSandboxStartResult{Record: stub.record}, nil
}

func (stub *dockerSandboxControllerStub) Cancel(_ context.Context,
	request application.DockerSandboxCancelRequest,
) (application.DockerSandboxCancelResult, error) {
	stub.cancelCalls++
	stub.lastCancel = request
	return application.DockerSandboxCancelResult{Record: stub.record,
		Cancellation: domain.DockerSandboxCancellation{
			ID: "docker-sandbox-cancel-http", AdmissionID: request.AdmissionID,
			ReasonCode: domain.DockerSandboxReasonCancelled, RequestedAt: time.Now().UTC(),
		}}, nil
}

func TestDockerSandboxHTTPBoundaryDelegatesStrictRequests(t *testing.T) {
	fixture := newAPIFixture(t)
	stub := &dockerSandboxControllerStub{}
	api, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true,
		},
		DockerSandboxController: stub,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := dockerSandboxHTTPTestManifest()

	readinessBody, err := json.Marshal(DockerSandboxReadinessRequestView{
		PlanID: "sandbox-docker-plan-http", Manifest: manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	readiness := performDockerSandboxRequest(t, api, http.MethodPost,
		DockerSandboxReadinessPath, testAccessToken, "", readinessBody)
	if readiness.Code != http.StatusOK || stub.readinessCalls != 1 ||
		stub.lastReadiness.PlanID != "sandbox-docker-plan-http" {
		t.Fatalf("readiness was not delegated exactly once: status=%d calls=%d body=%s",
			readiness.Code, stub.readinessCalls, readiness.Body.String())
	}
	assertSecurityHeaders(t, readiness)

	unknown := performDockerSandboxRequest(t, api, http.MethodPost,
		DockerSandboxReadinessPath, testAccessToken, "",
		[]byte(`{"plan_id":"sandbox-docker-plan-http","manifest":{},"daemon_endpoint":"tcp://127.0.0.1:2375"}`))
	assertAPIError(t, unknown, http.StatusBadRequest, "INVALID_ARGUMENT")
	if stub.readinessCalls != 1 {
		t.Fatal("unknown readiness field reached the application controller")
	}
	readinessWithRequesterBody := append([]byte(nil), readinessBody[:len(readinessBody)-1]...)
	readinessWithRequesterBody = append(readinessWithRequesterBody,
		[]byte(`,"requested_by":""}`)...)
	readinessWithRequester := performDockerSandboxRequest(t, api, http.MethodPost,
		DockerSandboxReadinessPath, testAccessToken, "", readinessWithRequesterBody)
	assertAPIError(t, readinessWithRequester, http.StatusBadRequest, "INVALID_ARGUMENT")

	admissionBody, err := json.Marshal(DockerSandboxAdmissionRequestView{
		PlanID: "sandbox-docker-plan-http", Manifest: manifest, RequestedBy: "http_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	missingKey := performDockerSandboxRequest(t, api, http.MethodPost,
		DockerSandboxAdmissionPath, testControlToken, "", admissionBody)
	assertAPIError(t, missingKey, http.StatusBadRequest, "INVALID_ARGUMENT")
	wrongToken := performDockerSandboxRequest(t, api, http.MethodPost,
		DockerSandboxAdmissionPath, testAccessToken, "admission-http-key", admissionBody)
	assertAPIError(t, wrongToken, http.StatusUnauthorized, "POLICY_DENIED")
	admitted := performDockerSandboxRequest(t, api, http.MethodPost,
		DockerSandboxAdmissionPath, testControlToken, "admission-http-key", admissionBody)
	if admitted.Code != http.StatusOK || stub.admitCalls != 1 ||
		stub.lastAdmission.OperationKey != "admission-http-key" ||
		stub.lastAdmission.RequestedBy != "http_operator" {
		t.Fatalf("admission was not exactly delegated: status=%d request=%#v body=%s",
			admitted.Code, stub.lastAdmission, admitted.Body.String())
	}

	identityBody := []byte(`{"admission_id":"docker-sandbox-admission-http","requested_by":"http_operator"}`)
	forbiddenStart := performDockerSandboxRequest(t, api, http.MethodPost,
		DockerSandboxStartPath, testControlToken, "start-forbidden-http-key",
		[]byte(`{"admission_id":"docker-sandbox-admission-http","requested_by":"http_operator","image":"busybox:latest"}`))
	assertAPIError(t, forbiddenStart, http.StatusBadRequest, "INVALID_ARGUMENT")
	if stub.startCalls != 0 {
		t.Fatal("caller-supplied image reached the application controller")
	}
	started := performDockerSandboxRequest(t, api, http.MethodPost,
		DockerSandboxStartPath, testControlToken, "start-http-key", identityBody)
	if started.Code != http.StatusOK || stub.startCalls != 1 ||
		stub.lastStart.OperationKey != "start-http-key" ||
		stub.lastStart.AdmissionID != "docker-sandbox-admission-http" {
		t.Fatalf("start was not exactly delegated: status=%d request=%#v body=%s",
			started.Code, stub.lastStart, started.Body.String())
	}

	cancelled := performDockerSandboxRequest(t, api, http.MethodPost,
		DockerSandboxCancelPath, testControlToken, "cancel-http-key", identityBody)
	if cancelled.Code != http.StatusOK || stub.cancelCalls != 1 ||
		stub.lastCancel.OperationKey != "cancel-http-key" {
		t.Fatalf("cancel was not exactly delegated: status=%d request=%#v body=%s",
			cancelled.Code, stub.lastCancel, cancelled.Body.String())
	}

	status := performDockerSandboxRequest(t, api, http.MethodGet,
		DockerSandboxStatusPath+"?admission_id=docker-sandbox-admission-http",
		testAccessToken, "", nil)
	if status.Code != http.StatusOK || stub.getCalls != 1 {
		t.Fatalf("status was not delegated: status=%d calls=%d body=%s",
			status.Code, stub.getCalls, status.Body.String())
	}
	extraQuery := performDockerSandboxRequest(t, api, http.MethodGet,
		DockerSandboxStatusPath+"?admission_id=docker-sandbox-admission-http&socket=x",
		testAccessToken, "", nil)
	assertAPIError(t, extraQuery, http.StatusBadRequest, "INVALID_ARGUMENT")
	if stub.getCalls != 1 {
		t.Fatal("unknown status query reached the application controller")
	}
}

func TestDockerSandboxRuntimeCapabilityIsControllerOwned(t *testing.T) {
	fixture := newAPIFixture(t)
	enabled := &dockerSandboxControllerStub{capabilities: sandbox.DockerRuntimeCapabilities{Enabled: true}}
	if _, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, DockerSandboxController: enabled}); err == nil {
		t.Fatal("Docker execution was accepted without permission control")
	}
	api, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true,
		},
		DockerSandboxController: enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := performDockerSandboxRequest(t, api, http.MethodGet,
		"/api/v1/capabilities", testAccessToken, "", nil)
	var capabilities RuntimeCapabilitiesView
	decodeData(t, response, &capabilities)
	if !capabilities.DockerExecutionEnabled {
		t.Fatalf("Docker capability did not project the controller grant: %#v", capabilities)
	}
}

func TestDockerSandboxStartDoesNotRelaxGlobalServerWriteTimeout(t *testing.T) {
	fixture := newAPIFixture(t)
	server, err := NewServer(fixture.api, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if server.httpServer.WriteTimeout != 30*time.Second {
		t.Fatalf("global server write timeout changed: %s", server.httpServer.WriteTimeout)
	}
}

func performDockerSandboxRequest(t *testing.T, api *API, method, requestPath,
	token, operationKey string, body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request := httptest.NewRequest(method, "http://127.0.0.1"+requestPath, reader)
	request.Host = "127.0.0.1:8765"
	request.RemoteAddr = "127.0.0.1:45000"
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if operationKey != "" {
		request.Header.Set("Idempotency-Key", operationKey)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func dockerSandboxHTTPTestManifest() sandbox.Manifest {
	return sandbox.Manifest{
		ProtocolVersion: sandbox.ManifestProtocolVersion, Backend: sandbox.BackendDocker,
		Command: sandbox.CommandSpec{Executable: "/bin/true",
			WorkingDirectory: "/workspace"},
		Mounts: []sandbox.Mount{{Source: ".", Target: "/workspace",
			Access: sandbox.MountReadOnly}},
		Network: sandbox.NetworkScope{Mode: "disabled"},
		Resources: sandbox.ResourceLimits{CPUQuotaMillis: 1000,
			MemoryBytes: 256 * 1024 * 1024, PIDs: 64,
			MaxOutputBytes: 4 * 1024 * 1024},
		Output:         sandbox.OutputSpec{CaptureStdout: true, CaptureStderr: true},
		TimeoutSeconds: 300,
		Cancellation:   sandbox.CancellationSpec{GracePeriodMillis: 2000},
	}
}
