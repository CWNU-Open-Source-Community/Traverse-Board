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
	body := `{"mode":"full_access","confirm_danger_full_access":true}`
	denied := performControlPathRequest(t, closed, path,
		"http-permission-closed-0001", strings.NewReader(body))
	assertAPIError(t, denied, http.StatusForbidden, "POLICY_DENIED")

	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true,
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
		strings.NewReader(`{"mode":"full_access","confirm_user_approval":true}`))
	assertAPIError(t, malformed, http.StatusBadRequest, "INVALID_ARGUMENT")

	first := performControlPathRequest(t, open, path,
		"http-permission-open-0001", strings.NewReader(body))
	var selected RunExecutionPermissionControlView
	decodeDataStatus(t, first, http.StatusAccepted, &selected)
	permission := selected.ExecutionPermission
	if selected.Replayed || permission.Mode !=
		string(domain.RunExecutionPermissionFullAccess) ||
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
