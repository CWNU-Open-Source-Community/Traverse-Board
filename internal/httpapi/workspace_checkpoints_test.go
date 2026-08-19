package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

type workspaceCheckpointControllerStub struct {
	captureRequest application.WorkspaceCheckpointCaptureRequest
	restoreRequest application.WorkspaceRestoreRequest
	forkRequest    application.WorkspaceForkRequest
	action         string
}

func (s *workspaceCheckpointControllerStub) Capture(_ context.Context,
	request application.WorkspaceCheckpointCaptureRequest,
) (workspacecheckpoint.Checkpoint, bool, error) {
	s.captureRequest = request
	return workspacecheckpoint.Checkpoint{ID: "checkpoint-created",
		ProtocolVersion: workspacecheckpoint.ProtocolVersion}, false, nil
}

func (s *workspaceCheckpointControllerStub) Timeline(_ context.Context, runID string,
	_ int,
) (application.WorkspaceCheckpointTimeline, error) {
	return application.WorkspaceCheckpointTimeline{
		ProtocolVersion: application.WorkspaceCheckpointAPIProtocolVersion,
		RunID:           runID,
		Checkpoints: []workspacecheckpoint.Checkpoint{{ID: "checkpoint-current",
			ProtocolVersion: workspacecheckpoint.ProtocolVersion}},
	}, nil
}

func (s *workspaceCheckpointControllerStub) Restore(_ context.Context,
	request application.WorkspaceRestoreRequest,
) (application.WorkspaceRestoreResult, error) {
	s.action = string(request.Kind)
	s.restoreRequest = request
	return application.WorkspaceRestoreResult{
		ProtocolVersion: application.WorkspaceCheckpointAPIProtocolVersion,
		Confirmed:       request.Confirm,
	}, nil
}

func (s *workspaceCheckpointControllerStub) Undo(_ context.Context, runID,
	expectedCurrentCheckpointID, operationKey, requestedBy string, confirm bool,
) (application.WorkspaceRestoreResult, error) {
	s.action = "undo"
	s.restoreRequest = application.WorkspaceRestoreRequest{RunID: runID,
		ExpectedCurrentCheckpointID: expectedCurrentCheckpointID,
		OperationKey:                operationKey, RequestedBy: requestedBy, Confirm: confirm}
	return application.WorkspaceRestoreResult{
		ProtocolVersion: application.WorkspaceCheckpointAPIProtocolVersion,
		Confirmed:       confirm,
	}, nil
}

func (s *workspaceCheckpointControllerStub) Redo(_ context.Context, runID,
	expectedCurrentCheckpointID, operationKey, requestedBy string, confirm bool,
) (application.WorkspaceRestoreResult, error) {
	s.action = "redo"
	s.restoreRequest = application.WorkspaceRestoreRequest{RunID: runID,
		ExpectedCurrentCheckpointID: expectedCurrentCheckpointID,
		OperationKey:                operationKey, RequestedBy: requestedBy, Confirm: confirm}
	return application.WorkspaceRestoreResult{
		ProtocolVersion: application.WorkspaceCheckpointAPIProtocolVersion,
		Confirmed:       confirm,
	}, nil
}

func (s *workspaceCheckpointControllerStub) Fork(_ context.Context,
	request application.WorkspaceForkRequest,
) (application.WorkspaceForkResult, error) {
	s.action = "fork"
	s.forkRequest = request
	return application.WorkspaceForkResult{
		ProtocolVersion: application.WorkspaceCheckpointAPIProtocolVersion,
		SourceRunID:     request.RunID,
		Workspace: session.WorkspaceRecord{ID: "workspace-fork", Name: "fork",
			RootPath: `D:\private\workspace-fork`},
		Mission: domain.Mission{ID: "mission-fork", WorkspaceID: "workspace-fork",
			Profile: domain.ProfileCode},
		Run: domain.Run{ID: "run-fork", MissionID: "mission-fork",
			SessionID: "session-fork", Status: domain.RunCreated},
	}, nil
}

func TestWorkspaceCheckpointHTTPRoutesAuthenticateAndBindIntent(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := &workspaceCheckpointControllerStub{}
	fixture.api.workspaceCheckpointController = controller
	fixture.api.workspaceCheckpointControlEnabled = true

	timeline := fixture.get(t, "/api/v1/runs/"+fixture.run.ID+"/workspace-checkpoints?limit=7")
	if timeline.Code != http.StatusOK || !strings.Contains(timeline.Body.String(), "checkpoint-current") {
		t.Fatalf("timeline status=%d body=%s", timeline.Code, timeline.Body.String())
	}

	capture := performControlMethodPathRequest(t, fixture.api, http.MethodPost,
		"/api/v1/runs/"+fixture.run.ID+"/workspace-checkpoints",
		"workspace-checkpoint-http-create-0001",
		strings.NewReader(`{"operation_key":"capture-op","title":"before refactor"}`))
	if capture.Code != http.StatusCreated || controller.captureRequest.RunID != fixture.run.ID ||
		controller.captureRequest.OperationKey != "capture-op" ||
		controller.captureRequest.RequestedBy != "api_operator" {
		t.Fatalf("capture status=%d request=%#v body=%s", capture.Code,
			controller.captureRequest, capture.Body.String())
	}

	rewind := performControlMethodPathRequest(t, fixture.api, http.MethodPost,
		"/api/v1/runs/"+fixture.run.ID+"/workspace-checkpoints/rewind",
		"workspace-checkpoint-http-rewind-0001", strings.NewReader(
			`{"target_checkpoint_id":"target","expected_current_checkpoint_id":"current",`+
				`"operation_key":"rewind-op","confirm":true}`))
	if rewind.Code != http.StatusOK || !controller.restoreRequest.Confirm ||
		controller.restoreRequest.TargetCheckpointID != "target" ||
		controller.restoreRequest.ExpectedCurrentCheckpointID != "current" {
		t.Fatalf("rewind status=%d request=%#v body=%s", rewind.Code,
			controller.restoreRequest, rewind.Body.String())
	}

	fork := performControlMethodPathRequest(t, fixture.api, http.MethodPost,
		"/api/v1/runs/"+fixture.run.ID+"/workspace-checkpoints/fork",
		"workspace-checkpoint-http-fork-0001", strings.NewReader(
			`{"target_checkpoint_id":"target","expected_current_checkpoint_id":"current",`+
				`"operation_key":"fork-op","workspace_name":"fork",`+
				`"branch":"codex/fork","confirm":true}`))
	if fork.Code != http.StatusCreated || controller.forkRequest.Branch != "codex/fork" ||
		controller.forkRequest.RequestedBy != "api_operator" ||
		controller.forkRequest.WorkspaceRoot != "" ||
		!strings.Contains(fork.Body.String(), `"run":{"id":"run-fork"`) ||
		strings.Contains(fork.Body.String(), `D:\private`) ||
		strings.Contains(fork.Body.String(), "RootPath") {
		t.Fatalf("fork status=%d request=%#v body=%s", fork.Code,
			controller.forkRequest, fork.Body.String())
	}
}

func TestWorkspaceCheckpointHTTPRoutesFailClosed(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.api.workspaceCheckpointController = &workspaceCheckpointControllerStub{}
	fixture.api.workspaceCheckpointControlEnabled = true
	path := "/api/v1/runs/" + fixture.run.ID + "/workspace-checkpoints/rewind"

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path,
		strings.NewReader(`{"target_checkpoint_id":"target","confirm":true}`))
	request.Host = "127.0.0.1:8765"
	request.RemoteAddr = "127.0.0.1:45000"
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	fixture.api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("read token authorized mutation: status=%d body=%s", response.Code,
			response.Body.String())
	}

	for name, body := range map[string]string{
		"confirmation missing": `{"target_checkpoint_id":"target",` +
			`"expected_current_checkpoint_id":"current","operation_key":"op"}`,
		"expected cursor missing": `{"target_checkpoint_id":"target",` +
			`"operation_key":"op","confirm":true}`,
		"operation key missing": `{"target_checkpoint_id":"target",` +
			`"expected_current_checkpoint_id":"current","confirm":true}`,
		"duplicate intent": `{"target_checkpoint_id":"target",` +
			`"target_checkpoint_id":"other","confirm":true}`,
		"unknown field": `{"target_checkpoint_id":"target","confirm":true,"force":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := performControlMethodPathRequest(t, fixture.api, http.MethodPost, path,
				"workspace-checkpoint-http-invalid-"+strings.ReplaceAll(name, " ", "-"),
				strings.NewReader(body))
			if result.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
			}
		})
	}
	forkWithRendererPath := performControlMethodPathRequest(t, fixture.api, http.MethodPost,
		"/api/v1/runs/"+fixture.run.ID+"/workspace-checkpoints/fork",
		"workspace-checkpoint-http-renderer-path-0001", strings.NewReader(
			`{"target_checkpoint_id":"target","expected_current_checkpoint_id":"current",`+
				`"operation_key":"fork-op","workspace_name":"fork",`+
				`"workspace_root":"D:\\private\\fork",`+
				`"branch":"codex/fork","confirm":true}`))
	if forkWithRendererPath.Code != http.StatusBadRequest {
		t.Fatalf("renderer path accepted: status=%d body=%s", forkWithRendererPath.Code,
			forkWithRendererPath.Body.String())
	}
}
