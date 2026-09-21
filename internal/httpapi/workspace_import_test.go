package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/workspace"
)

func TestWorkspaceImportRegistersAndReplaysWithoutDirectoryWritesOrAuthority(t *testing.T) {
	fixture := newAPIFixture(t)
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, WorkspaceImportEnabled: true,
		WorkspaceImporter: workspace.NewManager("", fixture.store)})
	if err != nil {
		t.Fatal(err)
	}
	// TEMP may use forward slashes on Windows; the stored import path uses
	// filepath's native spelling. Compare the same canonical spelling.
	selected := filepath.Clean(t.TempDir())
	marker := filepath.Join(selected, "user-owned.txt")
	if err := os.WriteFile(marker, []byte("user content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(WorkspaceImportRequestView{
		Version: WorkspaceImportProtocolVersion, DirectoryPath: selected, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	var first WorkspaceImportView
	for i := 0; i < 2; i++ {
		response := performSessionMessageRequest(t, api, http.MethodPost,
			WorkspaceImportPath, testControlToken, "", "application/json", bytes.NewReader(body))
		var view WorkspaceImportView
		decodeDataStatus(t, response, http.StatusOK, &view)
		if i == 0 {
			first = view
		}
		if view.ProtocolVersion != WorkspaceImportProtocolVersion || view.Workspace != first.Workspace ||
			view.DirectoryContentModified || view.AgentAuthorityGranted {
			t.Fatalf("invalid import projection: %#v", view)
		}
		if bytes.Contains(response.Body.Bytes(), []byte(selected)) ||
			strings.Contains(response.Body.String(), "root_path") {
			t.Fatal("import response exposed a host path")
		}
	}
	record, err := fixture.store.GetWorkspaceByID(t.Context(), first.Workspace.ID)
	if err != nil || record.RootPath != selected {
		t.Fatalf("registration failed: selected=%q record=%#v err=%v", selected, record, err)
	}
	entries, err := os.ReadDir(selected)
	content, readErr := os.ReadFile(marker)
	if err != nil || readErr != nil || len(entries) != 1 || string(content) != "user content\n" {
		t.Fatalf("import modified project content: entries=%#v err=%v read=%v", entries, err, readErr)
	}
	var capabilities RuntimeCapabilitiesView
	decodeDataStatus(t, performSessionMessageRequest(t, api, http.MethodGet,
		"/api/v1/capabilities", testAccessToken, "", "", nil), http.StatusOK, &capabilities)
	if !capabilities.WorkspaceImportEnabled || capabilities.RunCreationEnabled ||
		capabilities.ProcessExecutionEnabled || capabilities.OperatorApprovalEnabled || capabilities.DangerFullAccessEnabled {
		t.Fatalf("import widened unrelated capability: %#v", capabilities)
	}
}

func TestWorkspaceImportRequiresExplicitGateControlAndStrictRequest(t *testing.T) {
	fixture := newAPIFixture(t)
	manager := workspace.NewManager("", fixture.store)
	for _, config := range []Config{
		{AccessToken: testAccessToken, WorkspaceImportEnabled: true, WorkspaceImporter: manager},
		{AccessToken: testAccessToken, ControlToken: testControlToken, WorkspaceImportEnabled: true},
	} {
		if _, err := New(fixture.store, config); err == nil {
			t.Fatal("missing import gate dependency accepted")
		}
	}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		WorkspaceImportEnabled: true, WorkspaceImporter: manager})
	if err != nil {
		t.Fatal(err)
	}
	selected := t.TempDir()
	valid, err := json.Marshal(WorkspaceImportRequestView{
		Version: WorkspaceImportProtocolVersion, DirectoryPath: selected, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	before, err := fixture.store.ListWorkspaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", testAccessToken} {
		assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost,
			WorkspaceImportPath, token, "", "application/json", bytes.NewReader(valid)),
			http.StatusUnauthorized, "POLICY_DENIED")
	}
	assertAPIError(t, performSessionMessageRequest(t, fixture.api, http.MethodPost,
		WorkspaceImportPath, testControlToken, "", "application/json", bytes.NewReader(valid)),
		http.StatusNotFound, "NOT_FOUND")
	for _, body := range []string{
		`{"version":"workspace_import.v1","directory_path":"relative","confirmed":true}`,
		strings.Replace(string(valid), `"confirmed":true`, `"confirmed":false`, 1),
		strings.TrimSuffix(string(valid), "}") + `,"root_path":"ignored"}`,
		strings.TrimSuffix(string(valid), "}") + `,"directory_path":"other"}`,
		string(valid) + `{}`,
		`null`,
		`{"version":"workspace_import.v1","directory_path":"` + strings.Repeat("x", 4097) + `","confirmed":true}`,
		`{"version":"workspace_import.v1","directory_path":"` + strings.Repeat("中", 1400) + `","confirmed":true}`,
		`{"version":"workspace_import.v1","directory_path":"\u0000","confirmed":true}`,
	} {
		response := performSessionMessageRequest(t, api, http.MethodPost,
			WorkspaceImportPath, testControlToken, "", "application/json", strings.NewReader(body))
		assertAPIError(t, response, http.StatusBadRequest, "INVALID_ARGUMENT")
		if strings.Contains(response.Body.String(), selected) {
			t.Fatal("invalid request leaked entered path")
		}
	}
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost,
		WorkspaceImportPath+"?force=true", testControlToken, "", "application/json", bytes.NewReader(valid)),
		http.StatusBadRequest, "INVALID_ARGUMENT")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost,
		WorkspaceImportPath, testControlToken, "", "text/plain", bytes.NewReader(valid)),
		http.StatusUnsupportedMediaType, "INVALID_ARGUMENT")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodGet,
		WorkspaceImportPath, testControlToken, "", "", nil), http.StatusMethodNotAllowed, "INVALID_ARGUMENT")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost,
		WorkspaceImportPath, testControlToken, "", "application/json", bytes.NewReader([]byte{0xff})),
		http.StatusBadRequest, "INVALID_ARGUMENT")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost,
		WorkspaceImportPath, testControlToken, "", "application/json", strings.NewReader(strings.Repeat(" ", MaxWorkspaceImportRequestBodyBytes+1))),
		http.StatusRequestEntityTooLarge, "RESOURCE_EXHAUSTED")
	remote := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+WorkspaceImportPath, bytes.NewReader(valid))
	remote.RemoteAddr = "192.0.2.10:1234"
	remote.Header.Set("Authorization", "Bearer "+testControlToken)
	remote.Header.Set("Content-Type", "application/json")
	remoteResponse := httptest.NewRecorder()
	api.ServeHTTP(remoteResponse, remote)
	assertAPIError(t, remoteResponse, http.StatusForbidden, "POLICY_DENIED")
	after, err := fixture.store.ListWorkspaces(t.Context())
	if err != nil || len(after) != len(before) {
		t.Fatalf("rejected import changed registration: %#v %v", after, err)
	}
}

func TestWorkspaceImportProjectsRegisteredLongNameWithoutChangingStoredIdentity(t *testing.T) {
	fixture := newAPIFixture(t)
	manager := workspace.NewManager(t.TempDir(), fixture.store)
	// Use the real workspace init implementation, which accepts this legacy
	// name. The re-import must project it without rewriting the registration.
	original, err := manager.Init(t.Context(), strings.Repeat("a", 140))
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, WorkspaceImportEnabled: true, WorkspaceImporter: manager})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(WorkspaceImportRequestView{Version: WorkspaceImportProtocolVersion,
		DirectoryPath: original.RootPath, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		response := performSessionMessageRequest(t, api, http.MethodPost,
			WorkspaceImportPath, testControlToken, "", "application/json", bytes.NewReader(body))
		var view WorkspaceImportView
		decodeDataStatus(t, response, http.StatusOK, &view)
		if view.Workspace.ID != original.ID || view.Workspace.Name != strings.Repeat("a", 125)+"..." ||
			!view.Workspace.CreatedAt.Equal(original.CreatedAt) {
			t.Fatalf("existing import projection is invalid: %#v", view)
		}
		stored, err := fixture.store.GetWorkspaceByID(t.Context(), original.ID)
		if err != nil || stored.ID != original.ID || stored.Name != original.Name ||
			stored.RootPath != original.RootPath || !stored.CreatedAt.Equal(original.CreatedAt) {
			t.Fatalf("bounded projection rewrote registration: %#v %v", stored, err)
		}
	}
}
