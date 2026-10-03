package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestRunExecutionPermissionControlRequiresRuntimeGateAndExactConfirmation(t *testing.T) {
	fixture := newAPIFixture(t)
	_, run, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{
			Goal: "select execution permission through HTTP", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/runs/" + run.ID + "/execution-permission"
	body := `{"mode":"full","confirm_full":true}`
	denied := performControlPathRequest(t, closed, path,
		"http-permission-closed-0001", strings.NewReader(body))
	assertAPIError(t, denied, http.StatusForbidden, "POLICY_DENIED")

	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true,
		RuntimeAuthority:          domain.NewExecutionPermissionRuntimeAuthority(),
	}
	open, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities:   capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	malformed := performControlPathRequest(t, open, path,
		"http-permission-malformed-0001",
		strings.NewReader(`{"mode":"full","confirm_user_approval":true}`))
	assertAPIError(t, malformed, http.StatusBadRequest, "INVALID_ARGUMENT")

	first := performControlPathRequest(t, open, path,
		"http-permission-open-0001", strings.NewReader(body))
	var selected RunExecutionPermissionControlView
	decodeDataStatus(t, first, http.StatusAccepted, &selected)
	permission := selected.ExecutionPermission
	if selected.Replayed || permission.Mode !=
		string(domain.RunExecutionPermissionFull) ||
		!permission.RuntimeGateAvailable ||
		!permission.Runtime.DangerFullAccessEnabled ||
		permission.PersistentTerminal || permission.BackgroundProcess ||
		permission.AgentTerminalInput || permission.ProcessEnabled ||
		permission.ExecutionAuthorized || permission.CapabilityGrant {
		t.Fatalf("HTTP permission selection escaped its boundary: %+v", selected)
	}
	replay := performControlPathRequest(t, open, path,
		"http-permission-open-0001", strings.NewReader(body))
	var replayed RunExecutionPermissionControlView
	decodeDataStatus(t, replay, http.StatusAccepted, &replayed)
	if !replayed.Replayed ||
		replayed.ExecutionPermission.Revision != permission.Revision {
		t.Fatalf("HTTP permission replay changed result: %+v", replayed)
	}
}

func TestRunExecutionPermissionControlAutoDoesNotCreateWorkspaceRuntime(t *testing.T) {
	fixture := newAPIFixture(t)
	for _, sandboxGate := range []bool{false, true} {
		_, run, err := application.NewRunService(fixture.store).Create(t.Context(),
			application.CreateRunRequest{Goal: "select Auto with independent sandbox readiness",
				Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
		if err != nil {
			t.Fatal(err)
		}
		api, err := New(fixture.store, Config{AccessToken: testAccessToken,
			ControlToken: testControlToken, ExecutionPermissionControlEnabled: true,
			ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
				WorkspaceSandboxEnabled: sandboxGate}})
		if err != nil {
			t.Fatal(err)
		}
		path := "/api/v1/runs/" + run.ID + "/execution-permission"
		// Retired selectors/confirmation flags never reopen a five-mode writer,
		// including when the old process gate happens to be enabled.
		legacy := performControlPathRequest(t, api, path, "retired-workspace-permission-0001",
			strings.NewReader(`{"mode":"workspace_access","confirm_workspace_access":true}`))
		assertAPIError(t, legacy, http.StatusBadRequest, "INVALID_ARGUMENT")
		response := performControlPathRequest(t, api, path, "http-auto-independent-runtime-0001",
			strings.NewReader(`{"mode":"auto","confirm_full":false}`))
		var selected RunExecutionPermissionControlView
		decodeDataStatus(t, response, http.StatusAccepted, &selected)
		permission := selected.ExecutionPermission
		if permission.Mode != string(domain.RunExecutionPermissionAuto) ||
			permission.ApprovalMode != "auto" || !permission.RuntimeGateAvailable ||
			permission.Runtime.WorkspaceSandboxEnabled != sandboxGate ||
			permission.ApprovalPolicy != "per_operation" ||
			permission.CommandScope != "per_operation" ||
			permission.ProcessEnabled || permission.ExecutionAuthorized || permission.CapabilityGrant {
			t.Fatalf("Auto selection created or misreported runtime authority: %+v", permission)
		}
		var readiness RunCapabilityReadinessView
		decodeDataStatus(t, performSessionMessageRequest(t, api, http.MethodGet,
			"/api/v1/runs/"+run.ID+"/capability-readiness", testAccessToken, "", "", nil),
			http.StatusOK, &readiness)
		if readiness.CommandRuntime.AdapterInstalled || readiness.CommandRuntime.AdapterReady ||
			readiness.CommandRuntime.CurrentRunGranted || readiness.CapabilityGrant {
			t.Fatalf("approval preference fabricated an actual command backend: %+v", readiness)
		}
	}
}
