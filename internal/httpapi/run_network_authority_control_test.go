package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

func TestRunNetworkAuthorityControlHTTPIsRevisionBoundAndIdempotent(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "run-network-authority-http.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, run, err := application.NewRunService(state).Create(t.Context(),
		application.CreateRunRequest{Goal: "HTTP exact network authority",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	api, err := New(state, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, RunControlEnabled: true, AppVersion: "test",
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			RuntimeAuthority: authority,
		}})
	if err != nil {
		t.Fatal(err)
	}
	path := strings.ReplaceAll(RunNetworkAuthorityControlPathTemplate, "{run_id}", run.ID)
	body := `{"version":"run_network_authority_control.v1","expected_mode_revision":1,` +
		`"add_allowed_targets":["https://SEARCH.Example.org/"],"reason":"operator approved search"}`
	response := performSessionMessageRequest(t, api, http.MethodPost, path,
		testControlToken, "http-network-authority-0001", "application/json",
		strings.NewReader(body))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data RunNetworkAuthorityControlView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	view := envelope.Data
	if view.Version != application.RunNetworkAuthorityControlProtocolVersion ||
		view.RunID != run.ID || view.Mode.Revision != 2 || view.Replayed ||
		!view.CapabilityGrant || len(view.AddedTargets) != 1 ||
		view.AddedTargets[0] != "search.example.org" ||
		len(view.Mode.Scope.AllowedTargets) != 1 ||
		view.Mode.Scope.AllowedTargets[0] != "search.example.org" {
		t.Fatalf("unexpected response: %#v", view)
	}
	replay := performSessionMessageRequest(t, api, http.MethodPost, path,
		testControlToken, "http-network-authority-0001", "application/json",
		strings.NewReader(body))
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Data.Replayed || envelope.Data.Mode.Revision != view.Mode.Revision ||
		len(envelope.Data.Mode.Scope.AllowedTargets) != 1 ||
		envelope.Data.Mode.Scope.AllowedTargets[0] != "search.example.org" {
		t.Fatalf("replay diverged: %#v", envelope.Data)
	}
	staleBody := `{"version":"run_network_authority_control.v1","expected_mode_revision":1,` +
		`"add_allowed_targets":["docs.example.org"]}`
	stale := performSessionMessageRequest(t, api, http.MethodPost, path,
		testControlToken, "http-network-authority-stale-0001", "application/json",
		strings.NewReader(staleBody))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale status=%d body=%s", stale.Code, stale.Body.String())
	}
	wildcardBody := `{"version":"run_network_authority_control.v1","expected_mode_revision":2,` +
		`"add_allowed_targets":["*.example.org"]}`
	wildcard := performSessionMessageRequest(t, api, http.MethodPost, path,
		testControlToken, "http-network-authority-wildcard-0001", "application/json",
		strings.NewReader(wildcardBody))
	if wildcard.Code != http.StatusBadRequest {
		t.Fatalf("wildcard status=%d body=%s", wildcard.Code, wildcard.Body.String())
	}
}
