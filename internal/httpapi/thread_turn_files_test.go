package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestThreadTurnFilesHTTPUsesEvidenceGateAndDurableNegativeAcknowledgement(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := &threadTurnControllerStub{err: apperror.New(apperror.CodeConflict, "execution outcome is unknown")}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		RunCreationEnabled: true, SessionMessageEnabled: true, RunLifecycleEnabled: true, RunExecutionEnabled: true,
		RunLifecycleController: application.NewRunLifecycleControlService(fixture.store), RunExecutionController: runExecutionControllerFake{}, ThreadTurnController: controller})
	if err != nil {
		t.Fatal(err)
	}
	path := ThreadCollectionPath + "/" + domain.InitialThreadID(fixture.run.ID) + "/turns"
	body := `{"version":"thread_message_submission.v1","content":"Review selected file","files":[{"source_kind":"workspace_file","path":"a.txt","expected_sha256":"` + strings.Repeat("a", 64) + `"}]}`
	response := performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "thread-files-http-operation-0001", "application/json", strings.NewReader(body))
	if response.Code != http.StatusPreconditionFailed || controller.request.ThreadID != "" {
		t.Fatalf("disabled evidence gate bypassed: %d %s", response.Code, response.Body.String())
	}
	api.evidenceAttachmentEnabled = true
	response = performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "thread-files-http-operation-0001", "application/json", strings.NewReader(body))
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || len(controller.request.Files) != 1 || controller.request.Files[0].Path != "a.txt" || envelope.Error.MessageQueued != nil {
		t.Fatalf("request/unknown error projection: %#v %#v", controller.request, envelope)
	}
	controller.err = &application.ThreadMessageNotQueuedError{Cause: apperror.New(apperror.CodeConflict, "file verification rejected")}
	response = performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "thread-files-http-operation-0001", "application/json", strings.NewReader(body))
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.MessageQueued == nil || *envelope.Error.MessageQueued {
		t.Fatalf("durable rejection marker absent: %s", response.Body.String())
	}
	controller.request = application.ExecuteThreadTurnRequest{}
	response = performSessionMessageRequest(t, api, http.MethodPost, strings.TrimSuffix(path, "/turns")+"/messages", testControlToken, "thread-files-http-legacy-operation-0002", "application/json", strings.NewReader(body))
	if response.Code != http.StatusBadRequest || controller.request.ThreadID != "" {
		t.Fatalf("legacy endpoint accepted files: %d %s", response.Code, response.Body.String())
	}
	response = performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "thread-files-http-unknown-operation-0003", "application/json", strings.NewReader(strings.Replace(body, `"path":"a.txt"`, `"path":"a.txt","authority":true`, 1)))
	if response.Code != http.StatusBadRequest || controller.request.ThreadID != "" {
		t.Fatalf("unknown nested file field accepted: %d %s", response.Code, response.Body.String())
	}
}
