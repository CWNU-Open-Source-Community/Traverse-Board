package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

func TestEmbeddedAnalyzerControlExecutesFixedWASIAndCommitsArtifact(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "embedded-analyzer-http.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	workspace := store.WorkspaceRecord{ID: "workspace-analyzer-http", Name: "analyzer-http",
		RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err := state.SaveWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	created, err := application.NewControlledRunCreationService(state).Create(t.Context(),
		application.ControlledRunCreationRequest{
			Version: domain.RunCreationProtocolVersion, Goal: "analyze bounded input",
			WorkspaceID: workspace.ID, OperationKey: "embedded-analyzer-http-create-0001",
			RequestedBy: "test_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(state, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, AppVersion: "test",
		EmbeddedAnalyzerExecutionEnabled:    true,
		EmbeddedAnalyzerExecutionController: application.NewEmbeddedAnalyzerExecutionService(state)})
	if err != nil {
		t.Fatal(err)
	}
	path := strings.ReplaceAll(EmbeddedAnalyzerExecutionPathTemplate, "{run_id}", created.Run.ID)
	body := `{"version":"embedded_analyzer_operator_request.v1","text":"first line\nsecond line\n","media_type":"text/plain","confirmation":"RUN-EMBEDDED-ANALYZER"}`
	response := performSessionMessageRequest(t, api, http.MethodPost, path,
		testControlToken, "unused-analyzer-operation-key-0001", "application/json",
		strings.NewReader(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data EmbeddedAnalyzerExecutionControlView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Version != EmbeddedAnalyzerExecutionControlVersion ||
		envelope.Data.Status != "succeeded" || envelope.Data.InputBytes != 23 ||
		envelope.Data.LineCount != 2 || envelope.Data.RunID != created.Run.ID ||
		!envelope.Data.CapabilityConsumed || !envelope.Data.ArtifactAtomic ||
		envelope.Data.FilesystemMounted || envelope.Data.NetworkEnabled ||
		envelope.Data.SubprocessEnabled || envelope.Data.HostProcessAuthorized ||
		envelope.Data.RawRequestIncluded || envelope.Data.BearerTokenIncluded ||
		envelope.Data.Replayed {
		t.Fatalf("unexpected analyzer response: %#v", envelope.Data)
	}
	artifactBody, err := state.GetRunArtifact(t.Context(), envelope.Data.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(artifactBody.Content, "first line") ||
		!strings.Contains(artifactBody.Content, `"input_bytes":23`) {
		t.Fatalf("artifact leaked input or omitted metadata: %s", artifactBody.Content)
	}
}

func TestEmbeddedAnalyzerControlFailsClosed(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "embedded-analyzer-http-closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	api, err := New(state, Config{AccessToken: testAccessToken, AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	path := strings.ReplaceAll(EmbeddedAnalyzerExecutionPathTemplate, "{run_id}", "run-closed")
	response := performSessionMessageRequest(t, api, http.MethodPost, path,
		testControlToken, "unused-analyzer-operation-key-0002", "application/json",
		strings.NewReader(`{"version":"embedded_analyzer_operator_request.v1","text":"x","confirmation":"WRONG"}`))
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled endpoint status=%d body=%s", response.Code, response.Body.String())
	}
}
