package httpapi

import (
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/modelregistry"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProviderModelDiscoveryHTTPControlGateAndNoPersistence(t *testing.T) {
	f := newAPIFixture(t)
	definitions, err := application.NewProviderDefinitionService(f.store, f.api.modelRegistry)
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken, ProviderDefinitionEnabled: true, ProviderDefinitionController: definitions})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"fresh-model"}]}`)
	}))
	defer srv.Close()
	r := application.ProviderModelDiscoveryRequest{Version: modelregistry.ModelDiscoveryVersion, ProviderID: "model-discovery-draft", EndpointURL: srv.URL + "/v1/responses", Transport: modelregistry.ProviderTransportOpenAIResponses, AdvancedConfig: json.RawMessage(`{}`), ConfirmDiscovery: true}
	body, _ := json.Marshal(r)
	response := performSessionMessageRequest(t, api, http.MethodPost, ProviderModelDiscoveryPath, testAccessToken, "", "application/json", strings.NewReader(string(body)))
	assertAPIError(t, response, http.StatusUnauthorized, "POLICY_DENIED")
	if calls.Load() != 0 {
		t.Fatal("read bearer caused discovery request")
	}
	response = performSessionMessageRequest(t, api, http.MethodGet, ProviderModelDiscoveryPath, testControlToken, "", "", nil)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET accepted: %d", response.Code)
	}
	response = performSessionMessageRequest(t, api, http.MethodPost, ProviderModelDiscoveryPath, testControlToken, "", "application/json", strings.NewReader(string(body)))
	var result modelregistry.ModelDiscoveryResult
	decodeDataStatus(t, response, http.StatusOK, &result)
	if len(result.Models) != 1 || calls.Load() != 1 {
		t.Fatalf("unexpected catalog %v calls%d", result, calls.Load())
	}
	collection, err := definitions.List(t.Context())
	if err != nil || len(collection.Providers) != 0 || collection.Revision != 0 {
		t.Fatalf("discovery mutated definitions: %v %v", collection, err)
	}
	duplicate := strings.TrimSuffix(string(body), "}") + `,"confirm_discovery":true}`
	response = performSessionMessageRequest(t, api, http.MethodPost, ProviderModelDiscoveryPath, testControlToken, "", "application/json", strings.NewReader(duplicate))
	if response.Code != http.StatusBadRequest || calls.Load() != 1 {
		t.Fatalf("duplicate request dispatched: %d calls%d", response.Code, calls.Load())
	}
}
