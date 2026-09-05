package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestThreadExecutionPermissionControlSynchronizesCurrentRunAndReportsGETState(t *testing.T) {
	fixture := newAPIFixture(t)
	_, run, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "select Thread execution permission through HTTP",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := fixture.store.GetThreadByRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/threads/" + threadRecord.ID + "/execution-permission"
	api, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	initialResponse := performRequest(t, api, http.MethodGet, path, testAccessToken,
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	var initial ThreadExecutionPermissionControlView
	decodeDataStatus(t, initialResponse, http.StatusOK, &initial)
	if initial.ExecutionPermission.Mode != string(domain.RunExecutionPermissionConservative) ||
		initial.CurrentRunID != run.ID || initial.CurrentRunEffect !=
		string(domain.ThreadExecutionPermissionApplied) ||
		initial.CurrentRunMode != string(domain.RunExecutionPermissionConservative) ||
		!initial.CurrentRunSynchronized || !initial.ExecutionPermission.AppliesToCurrentRun ||
		!initial.ExecutionPermission.AppliesToFutureSuccessorRuns || initial.Replayed {
		t.Fatalf("initial Thread permission projection is misleading: %+v", initial)
	}

	body := `{"mode":"workspace_access","reason":"use the bounded Workspace sandbox",` +
		`"confirm_workspace_access":true}`
	unauthorized := performRequest(t, api, http.MethodPost, path, testAccessToken,
		"127.0.0.1:8765", "127.0.0.1:45000", strings.NewReader(body))
	assertAPIError(t, unauthorized, http.StatusUnauthorized, "POLICY_DENIED")

	selectedResponse := performControlPathRequest(t, api, path,
		"http-thread-permission-open-0001", strings.NewReader(body))
	var selected ThreadExecutionPermissionControlView
	decodeDataStatus(t, selectedResponse, http.StatusAccepted, &selected)
	if selected.ExecutionPermission.Mode !=
		string(domain.RunExecutionPermissionWorkspaceAccess) ||
		selected.CurrentRunID != run.ID || selected.CurrentRunEffect !=
		string(domain.ThreadExecutionPermissionApplied) ||
		selected.CurrentRunMode != string(domain.RunExecutionPermissionWorkspaceAccess) ||
		!selected.CurrentRunSynchronized || !selected.ExecutionPermission.AppliesToCurrentRun ||
		selected.ExecutionPermission.ProcessEnabled ||
		selected.ExecutionPermission.ExecutionAuthorized ||
		selected.ExecutionPermission.CapabilityGrant || selected.Replayed {
		t.Fatalf("Thread permission selection escaped its boundary: %+v", selected)
	}
	runPermission, err := fixture.store.GetRunExecutionPermission(t.Context(), run.ID)
	if err != nil || runPermission.Mode != domain.RunExecutionPermissionWorkspaceAccess {
		t.Fatalf("current Run did not receive Thread permission: %+v err=%v",
			runPermission, err)
	}

	afterResponse := performRequest(t, api, http.MethodGet, path, testAccessToken,
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	var after ThreadExecutionPermissionControlView
	decodeDataStatus(t, afterResponse, http.StatusOK, &after)
	if after.CurrentRunID != run.ID || !after.CurrentRunSynchronized ||
		after.CurrentRunEffect != string(domain.ThreadExecutionPermissionApplied) ||
		after.CurrentRunMode != string(domain.RunExecutionPermissionWorkspaceAccess) ||
		!after.ExecutionPermission.AppliesToCurrentRun {
		t.Fatalf("GET lost current Run synchronization: %+v", after)
	}

	replayResponse := performControlPathRequest(t, api, path,
		"http-thread-permission-open-0001", strings.NewReader(body))
	var replayed ThreadExecutionPermissionControlView
	decodeDataStatus(t, replayResponse, http.StatusAccepted, &replayed)
	if !replayed.Replayed ||
		replayed.ExecutionPermission.Revision != selected.ExecutionPermission.Revision ||
		replayed.CurrentRunID != run.ID || replayed.CurrentRunEffect != selected.CurrentRunEffect {
		t.Fatalf("Thread permission replay changed its result: %+v", replayed)
	}
}

func TestThreadExecutionPermissionControlRejectsMissingGateAndDefersLeasedRun(t *testing.T) {
	fixture := newAPIFixture(t)
	_, run, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "reject unavailable Thread permission",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := fixture.store.GetThreadByRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, ExecutionPermissionControlEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	response := performControlPathRequest(t, api,
		"/api/v1/threads/"+threadRecord.ID+"/execution-permission",
		"http-thread-permission-closed-0001",
		strings.NewReader(`{"mode":"workspace_access","confirm_workspace_access":true}`))
	assertAPIError(t, response, http.StatusForbidden, "POLICY_DENIED")

	if _, err := application.NewRunService(fixture.store).Start(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.AcquireRunExecutionLease(t.Context(),
		domain.AcquireRunExecutionLeaseRequest{RunID: run.ID,
			OwnerID: "thread-permission-http-worker", TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	open, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true,
		}})
	if err != nil {
		t.Fatal(err)
	}
	leased := performControlPathRequest(t, open,
		"/api/v1/threads/"+threadRecord.ID+"/execution-permission",
		"http-thread-permission-leased-0001",
		strings.NewReader(`{"mode":"workspace_access","confirm_workspace_access":true}`))
	var deferred ThreadExecutionPermissionControlView
	decodeDataStatus(t, leased, http.StatusAccepted, &deferred)
	if deferred.CurrentRunID != run.ID ||
		deferred.CurrentRunEffect != string(domain.ThreadExecutionPermissionDeferred) ||
		deferred.CurrentRunMode != string(domain.RunExecutionPermissionConservative) ||
		deferred.CurrentRunSynchronized ||
		deferred.ExecutionPermission.AppliesToCurrentRun {
		t.Fatalf("leased Thread preference did not report next-Run semantics: %+v", deferred)
	}
	preference, err := fixture.store.GetThreadExecutionPermission(t.Context(), threadRecord.ID)
	if err != nil || preference.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		preference.Revision != 2 {
		t.Fatalf("leased request did not persist the future preference: %+v err=%v",
			preference, err)
	}
}
