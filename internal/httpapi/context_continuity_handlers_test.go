package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/projectconfig"
)

func TestContextContinuityHTTPMemoryCheckpointTreeAndFork(t *testing.T) {
	fixture := newAPIFixture(t)
	create := continuityHTTPMutation(t, fixture.api, http.MethodPost, "/api/v1/memories",
		testControlToken, `{"scope":"project","scope_id":"`+fixture.workspace.ID+`",`+
			`"title":"API preference","content":"Keep exported schemas stable."}`)
	var memory contextmgr.Memory
	decodeDataStatus(t, create, http.StatusCreated, &memory)
	if memory.ID == "" || memory.ScopeID != fixture.workspace.ID || memory.CreatedBy != "http_control" {
		t.Fatalf("unexpected memory: %#v", memory)
	}
	var userMemories []contextmgr.Memory
	decodeData(t, fixture.get(t, "/api/v1/memories?scope=user"), &userMemories)
	if userMemories == nil || len(userMemories) != 0 {
		t.Fatalf("user scope default was not an empty local-user collection: %#v", userMemories)
	}
	var loaded contextmgr.Memory
	decodeData(t, fixture.get(t, "/api/v1/memories/"+memory.ID), &loaded)
	if loaded.ContentSHA256 != memory.ContentSHA256 {
		t.Fatalf("memory readback changed: %#v", loaded)
	}
	patch := continuityHTTPMutation(t, fixture.api, http.MethodPatch,
		"/api/v1/memories/"+memory.ID, testControlToken,
		`{"expected_version":1,"status":"disabled"}`)
	var disabled contextmgr.Memory
	decodeData(t, patch, &disabled)
	if disabled.Status != contextmgr.MemoryStatusDisabled || disabled.Version != 2 {
		t.Fatalf("memory was not disabled: %#v", disabled)
	}
	var exported application.ContextMemoryExport
	decodeData(t, fixture.get(t, "/api/v1/memories/export?scope=project&scope_id="+
		fixture.workspace.ID), &exported)
	if len(exported.Items) != 1 || exported.CapabilityGrant {
		t.Fatalf("unexpected export: %#v", exported)
	}

	checkpointResponse := continuityHTTPMutation(t, fixture.api, http.MethodPost,
		"/api/v1/runs/"+fixture.run.ID+"/continuity-checkpoints", testControlToken,
		`{"title":"Before API change","summary":"preserve compatibility"}`)
	var checkpoint contextmgr.ContinuityNode
	decodeDataStatus(t, checkpointResponse, http.StatusCreated, &checkpoint)
	if checkpoint.Kind != contextmgr.ContinuityNodeCheckpoint ||
		checkpoint.Snapshot.Authority != (contextmgr.ContinuityAuthority{}) {
		t.Fatalf("unexpected checkpoint: %#v", checkpoint)
	}
	var tree contextmgr.SessionTree
	decodeData(t, fixture.get(t, "/api/v1/sessions/"+fixture.run.SessionID+"/tree"), &tree)
	if tree.CapabilityGrant || len(tree.Nodes) < 2 {
		t.Fatalf("unexpected session tree: %#v", tree)
	}
	forkResponse := continuityHTTPMutation(t, fixture.api, http.MethodPost,
		"/api/v1/continuity-nodes/"+checkpoint.ID+"/fork", testControlToken,
		`{"goal":"continue from API checkpoint"}`)
	var fork continuityBranchView
	decodeDataStatus(t, forkResponse, http.StatusCreated, &fork)
	if fork.Run.ID == fixture.run.ID || fork.Run.Config.ContinuityContextFingerprint == "" ||
		fork.CapabilityGrant || len(fork.NotInherited) < 8 {
		t.Fatalf("unsafe fork response: %#v", fork)
	}

	unauthorized := continuityHTTPMutation(t, fixture.api, http.MethodPost,
		"/api/v1/runs/"+fixture.run.ID+"/continuity-checkpoints", testAccessToken, `{}`)
	assertAPIError(t, unauthorized, http.StatusUnauthorized, "POLICY_DENIED")
	unknown := continuityHTTPMutation(t, fixture.api, http.MethodPost, "/api/v1/memories",
		testControlToken, `{"scope":"user","scope_id":"local-user","title":"x",`+
			`"content":"y","capability":true}`)
	assertAPIError(t, unknown, http.StatusBadRequest, "INVALID_ARGUMENT")
}

func TestProjectInstructionHTTPExplainsDriftAndConfirmsRefresh(t *testing.T) {
	fixture := newAPIFixture(t)
	path := filepath.Join(fixture.workspace.RootPath, "AGENTS.md")
	if err := os.WriteFile(path, []byte("run focused tests\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := projectconfig.DiscoverInstructions(t.Context(), fixture.workspace.RootPath, ".")
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "instruction HTTP", Profile: "review",
			WorkspaceID: fixture.workspace.ID, Budget: domain.DefaultBudget(),
			ProjectInstructions: &snapshot, RequestedBy: "http_test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	var state application.ProjectInstructionState
	decodeData(t, fixture.get(t, "/api/v1/runs/"+run.ID+"/project-instructions"), &state)
	if state.Stale || state.Pinned.Snapshot.Fingerprint != snapshot.Fingerprint ||
		len(state.Pinned.Snapshot.Sources) != 1 {
		t.Fatalf("unexpected instruction state: %#v", state)
	}
	if err := os.WriteFile(path, []byte("run all tests\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	decodeData(t, fixture.get(t, "/api/v1/runs/"+run.ID+"/project-instructions"), &state)
	if !state.Stale || !state.Diff.RequiresConfirmation {
		t.Fatalf("instruction drift was not explained: %#v", state)
	}
	body, err := json.Marshal(projectInstructionRefreshRequestView{
		ExpectedFingerprint:     snapshot.Fingerprint,
		ExpectedLiveFingerprint: state.Live.Fingerprint, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	changedAgain := filepath.Join(fixture.workspace.RootPath, "AGENTS.md")
	if err := os.WriteFile(changedAgain, []byte("run race-safe tests\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleLiveResponse := continuityHTTPMutation(t, fixture.api, http.MethodPost,
		"/api/v1/runs/"+run.ID+"/project-instructions/refresh", testControlToken,
		string(body))
	assertAPIError(t, staleLiveResponse, http.StatusPreconditionFailed, "FAILED_PRECONDITION")
	decodeData(t, fixture.get(t, "/api/v1/runs/"+run.ID+"/project-instructions"), &state)
	body, err = json.Marshal(projectInstructionRefreshRequestView{
		ExpectedFingerprint:     snapshot.Fingerprint,
		ExpectedLiveFingerprint: state.Live.Fingerprint, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	response := continuityHTTPMutation(t, fixture.api, http.MethodPost,
		"/api/v1/runs/"+run.ID+"/project-instructions/refresh", testControlToken,
		string(body))
	decodeData(t, response, &state)
	if !state.RefreshConfirmed || state.Stale || state.Pinned.Revision != 2 {
		t.Fatalf("instruction refresh was not confirmed: %#v", state)
	}
}

func continuityHTTPMutation(t *testing.T, api *API, method, path, token,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://127.0.0.1"+path,
		strings.NewReader(body))
	request.Host = "127.0.0.1:8765"
	request.RemoteAddr = "127.0.0.1:45000"
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
