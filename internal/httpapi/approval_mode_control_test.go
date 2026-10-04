package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestApprovalModeHTTPPersistsThreeModesAndNeverRestoresFullFromGET(t *testing.T) {
	f := newAPIFixture(t)
	_, run, err := application.NewRunService(f.store).Create(t.Context(), application.CreateRunRequest{
		Goal: "HTTP three-mode writer", Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := f.store.GetThreadByRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{AccessToken: testAccessToken, ControlToken: testControlToken, ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true,
			DangerFullAccessEnabled: true, RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority()}}
	api, err := New(f.store, config)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/threads/" + thread.ID + "/execution-permission"
	read := func(api *API) ThreadExecutionPermissionControlView {
		response := performRequest(t, api, http.MethodGet, path, testAccessToken, "127.0.0.1:8765", "127.0.0.1:45000", nil)
		var result ThreadExecutionPermissionControlView
		decodeDataStatus(t, response, http.StatusOK, &result)
		return result
	}
	initial := read(api)
	if initial.ExecutionPermission.Mode != "ask" || initial.ExecutionPermission.ApprovalMode != "ask" || initial.ExecutionPermission.FullActivation != "inactive" {
		t.Fatal(initial)
	}
	var states []ThreadExecutionPermissionControlView
	states = append(states, initial)
	for _, body := range []string{`{"mode":"full","confirm_full":false}`, `{"mode":"debug","confirm_debug_access":true}`,
		`{"mode":"full_access","confirm_full":true}`, `{"mode":"auto","confirm_full":true}`, `{"mode":"ask","confirm_workspace_access":true}`} {
		response := performControlPathRequest(t, api, path, "http-three-mode-invalid-selection", strings.NewReader(body))
		assertAPIError(t, response, http.StatusBadRequest, "INVALID_ARGUMENT")
	}
	for _, mode := range []string{"auto", "full", "ask"} {
		body, _ := json.Marshal(map[string]any{"mode": mode, "confirm_full": mode == "full"})
		response := performControlPathRequest(t, api, path, "http-three-mode-change-"+mode, strings.NewReader(string(body)))
		var result ThreadExecutionPermissionControlView
		decodeDataStatus(t, response, http.StatusAccepted, &result)
		if result.ExecutionPermission.ProtocolVersion != "thread_execution_permission.v2" || result.ExecutionPermission.Mode != mode || result.ExecutionPermission.ApprovalMode != mode || result.CurrentRunMode != mode {
			t.Fatal(result)
		}
		states = append(states, result)
		if mode == "full" {
			if result.ExecutionPermission.FullActivation != "active" {
				t.Fatal("explicit Full did not activate", result)
			}
			coldConfig := config
			coldConfig.ExecutionPermissionCapabilities.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
			coldAPI, err := New(f.store, coldConfig)
			if err != nil {
				t.Fatal(err)
			}
			cold := read(coldAPI)
			if cold.ExecutionPermission.Mode != "full" || cold.ExecutionPermission.FullActivation != "inactive" || cold.ExecutionPermission.RuntimeGateAvailable {
				t.Fatal("GET restored saved Full authority", cold)
			}
			states = append(states, cold)
		}
	}
	// The lower-level Run route must use exactly the same confirmation field.
	response := performControlPathRequest(t, api, "/api/v1/runs/"+run.ID+"/execution-permission", "http-run-three-mode-full", strings.NewReader(`{"mode":"full","confirm_full":true}`))
	var selected RunExecutionPermissionControlView
	decodeDataStatus(t, response, http.StatusAccepted, &selected)
	if selected.ExecutionPermission.Mode != "full" || selected.ExecutionPermission.FullActivation != "active" {
		t.Fatal(selected)
	}
	if output := os.Getenv("TRAVERSE_TEST_APPROVAL_MODE_OUTPUT"); output != "" {
		data, err := json.MarshalIndent(states, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
