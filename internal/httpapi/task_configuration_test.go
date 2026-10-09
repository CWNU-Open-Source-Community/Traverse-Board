package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func previewConfigurationRequest(api *API, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+TaskConfigurationPreviewPath, strings.NewReader(body))
	request.Host, request.RemoteAddr = "127.0.0.1:8765", "127.0.0.1:45000"
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func TestTaskConfigurationHTTPReadOnlyPreviewAndPinnedCreation(t *testing.T) {
	fixture := newAPIFixture(t)
	filename := filepath.Join(fixture.workspace.RootPath, ".prayu", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("protocol: project_config.v1\nbudget: {max_turns: 12, max_tool_calls: 7}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	body := `{"workspace_id":"` + fixture.workspace.ID + `","budget":{"max_turns":20,"max_tool_calls":10,"max_tokens":1000,"max_cost_usd":0.25,"timeout_seconds":60}}`
	var preview application.TaskConfigurationView
	decodeData(t, previewConfigurationRequest(fixture.api, testAccessToken, body), &preview)
	if preview.ProjectDisposition != "applied" || preview.Budget.MaxTurns != 12 || preview.Budget.MaxToolCalls != 7 || preview.CapabilityGrant {
		t.Fatalf("preview=%#v", preview)
	}
	assertAPIError(t, previewConfigurationRequest(fixture.api, testControlToken, body), http.StatusUnauthorized, "POLICY_DENIED")
	createBody := strings.Replace(body, `"workspace_id":`, `"version":"thread_creation.v1","goal":"budget from safe preview","workspace_id":`, 1)
	var created ThreadCreationControlView
	decodeDataStatus(t, performControlPathRequest(t, fixture.api, ThreadCollectionPath, "http-config-budget-0001", strings.NewReader(createBody)), http.StatusAccepted, &created)
	if created.Run.Budget.MaxTurns != 12 || created.Run.Budget.MaxCostUSD != 0.25 || created.Run.Config.RequestedBudget == nil || created.Run.Config.RequestedBudget.MaxTurns != 20 {
		t.Fatalf("creation=%#v", created.Run)
	}
	if err := os.WriteFile(filename, []byte("protocol: project_config.v1\nread_only: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var pinned application.TaskConfigurationView
	response := fixture.get(t, "/api/v1/runs/"+created.Run.ID+"/task-configuration")
	decodeData(t, response, &pinned)
	if pinned.Fingerprint != preview.Fingerprint || strings.Contains(response.Body.String(), fixture.workspace.RootPath) || strings.Contains(response.Body.String(), "root_path") {
		t.Fatalf("pinned drift/leak: %s", response.Body.String())
	}
	var replayed ThreadCreationControlView
	decodeDataStatus(t, performControlPathRequest(t, fixture.api, ThreadCollectionPath, "http-config-budget-0001", strings.NewReader(createBody)), http.StatusAccepted, &replayed)
	if !replayed.Replayed || replayed.Run.ID != created.Run.ID || replayed.Run.Budget != created.Run.Budget {
		t.Fatalf("HTTP replay drift=%#v", replayed)
	}
	assertAPIError(t, performControlPathRequest(t, fixture.api, ThreadCollectionPath, "http-config-budget-0002", strings.NewReader(createBody)), http.StatusPreconditionFailed, "FAILED_PRECONDITION")
	for _, budget := range []string{`null`, `{"max_tokens":null}`, `{"max_cost_usd":null}`, `{"timeout_seconds":null}`, `{"max_turns":0}`, `{"max_tool_calls":0}`, `{"max_turns":10001}`, `{"max_tokens":-1}`, `{"timeout_seconds":604801}`, `{"max_cost_usd":0.0000001}`, `{"max_turns":20,"max_turns":21}`, `{"credential":"forbidden"}`} {
		assertAPIError(t, previewConfigurationRequest(fixture.api, testAccessToken, `{"workspace_id":"`+fixture.workspace.ID+`","budget":`+budget+`}`), http.StatusBadRequest, "INVALID_ARGUMENT")
	}
}

func TestTaskConfigurationHTTPLegacyRunKeepsZeroToolLimit(t *testing.T) {
	fixture := newAPIFixture(t)
	budget := domain.Budget{MaxTurns: 20_000, MaxTokens: 2_000_000_000, MaxCostUSD: 200_000, TimeoutSeconds: 1_000_000}
	_, run, err := application.NewRunService(fixture.store).Create(t.Context(), application.CreateRunRequest{
		Goal: "read legacy CLI ceilings", WorkspaceID: fixture.workspace.ID, Profile: "review", Budget: budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	var pinned application.TaskConfigurationView
	response := fixture.get(t, "/api/v1/runs/"+run.ID+"/task-configuration")
	decodeData(t, response, &pinned)
	if pinned.Budget != budget || pinned.RequestedBudget != budget || strings.Contains(response.Body.String(), `"max_tool_calls"`) {
		t.Fatalf("legacy snapshot was reinterpreted: %s", response.Body.String())
	}
	for _, source := range pinned.Sources {
		if source.Source != "snapshot" {
			t.Fatalf("legacy source=%#v", source)
		}
	}
}
