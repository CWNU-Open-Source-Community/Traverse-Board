package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestWorkspaceFileAttachmentHTTPUploadLookupDownloadAndGates(t *testing.T) {
	f := newAPIFixture(t)
	api, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken, RunCreationEnabled: true, SessionMessageEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/workspaces/" + f.workspace.ID + "/file-attachments"
	raw := []byte("%PDF-1.7 raw\x00\xff")
	body, _ := json.Marshal(WorkspaceFileUploadRequestView{Version: WorkspaceFileUploadVersion, Name: "原件.pdf", MIMEType: "application/pdf", DataBase64: base64.StdEncoding.EncodeToString(raw)})
	lookup := performSessionMessageRequest(t, api, http.MethodGet, path+"/request", testAccessToken, "file-upload-operation-key", "", nil)
	var obs domain.FileAttachmentObservation
	decodeData(t, lookup, &obs)
	if obs.State != "not_received" {
		t.Fatal("absent key received")
	}
	response := performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "file-upload-operation-key", "application/json", bytes.NewReader(body))
	var uploaded WorkspaceFileAttachmentView
	decodeData(t, response, &uploaded)
	response = performSessionMessageRequest(t, api, http.MethodGet, path+"/request", testAccessToken, "file-upload-operation-key", "", nil)
	decodeData(t, response, &obs)
	if obs.State != "stored" || obs.Attachment == nil || *obs.Attachment != uploaded.Attachment {
		t.Fatalf("lookup %#v", obs)
	}
	response = performSessionMessageRequest(t, api, http.MethodGet, path+"/"+uploaded.Attachment.ID+"/content", testAccessToken, "", "", nil)
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), raw) || response.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment;") || response.Header().Get("X-Cyberagent-Content-SHA256") != uploaded.Attachment.SHA256 {
		t.Fatalf("raw download %d %v", response.Code, response.Header())
	}
	for _, readPath := range []string{path + "/request", path + "/" + uploaded.Attachment.ID, path + "/" + uploaded.Attachment.ID + "/content"} {
		response = performSessionMessageRequest(t, api, http.MethodGet, readPath, testAccessToken, "file-upload-operation-key", "", strings.NewReader("not allowed"))
		if response.Code != 400 {
			t.Fatalf("GET accepted body %s %d", readPath, response.Code)
		}
	}
	response = performSessionMessageRequest(t, api, http.MethodPost, path, testAccessToken, "another-file-upload", "application/json", bytes.NewReader(body))
	if response.Code != 401 {
		t.Fatal("read token uploaded", response.Code)
	}
	response = performSessionMessageRequest(t, api, http.MethodGet, "/api/v1/workspaces/another-workspace/file-attachments/"+uploaded.Attachment.ID, testAccessToken, "", "", nil)
	if response.Code != 404 {
		t.Fatal("cross workspace", response.Code)
	}
	empty, _ := json.Marshal(WorkspaceFileUploadRequestView{Version: WorkspaceFileUploadVersion, Name: "empty.txt", MIMEType: "text/plain", DataBase64: ""})
	response = performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "empty-file-upload", "application/json", bytes.NewReader(empty))
	decodeData(t, response, &uploaded)
	if uploaded.Attachment.ByteSize != 0 || uploaded.Attachment.Readability != "text" {
		t.Fatal("empty file lost")
	}
}

func TestThreadAttachmentHTTPForwardsExactEmptyUserInput(t *testing.T) {
	f := newAPIFixture(t)
	controller := &threadTurnControllerStub{}
	api, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken, RunCreationEnabled: true, SessionMessageEnabled: true, RunLifecycleEnabled: true, RunExecutionEnabled: true, RunLifecycleController: application.NewRunLifecycleControlService(f.store), RunExecutionController: runExecutionControllerFake{}, ThreadTurnController: controller})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"version":"thread_message_submission.v1","content":"","attachments":[{"id":"attachment-example","workspace_id":"` + f.workspace.ID + `","sha256":"` + strings.Repeat("a", 64) + `","byte_size":0}]}`
	response := performSessionMessageRequest(t, api, http.MethodPost, ThreadCollectionPath+"/"+domain.InitialThreadID(f.run.ID)+"/turns", testControlToken, "file-turn-operation-key", "application/json", strings.NewReader(body))
	if controller.request.Content != "" || len(controller.request.Attachments) != 1 || controller.request.Attachments[0].ByteSize != 0 {
		t.Fatalf("exact file input lost: %#v %d", controller.request, response.Code)
	}
}
