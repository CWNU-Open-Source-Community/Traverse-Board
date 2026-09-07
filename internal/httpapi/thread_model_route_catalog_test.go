package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
)

type modelRouteCatalogJSONRegistry struct {
	snapshot modelregistry.Snapshot
	router   *llm.Router
}

func (s modelRouteCatalogJSONRegistry) Snapshot() modelregistry.Snapshot {
	return s.snapshot
}

func (s modelRouteCatalogJSONRegistry) Router() *llm.Router {
	return s.router
}

func TestAvailableModelRoutesSerializesEmptyDefaultsAsJSONArray(t *testing.T) {
	fixture := newAPIFixture(t)
	registry := modelRouteCatalogJSONRegistry{router: llm.NewDefaultRouter(),
		snapshot: modelregistry.Snapshot{ProtocolVersion: modelregistry.ProtocolVersion,
			Generation: 1, Providers: []modelregistry.ProviderAvailability{{
				Name: "official-deepseek", DisplayName: "DeepSeek",
				Kind:   modelregistry.ProviderKindAnthropicCompatible,
				Status: modelregistry.ProviderAvailable, Models: []string{"deepseek-v4-flash"},
				CredentialSource: "system", NetworkRequired: true, Enabled: true,
				Harnesses: []modelregistry.HarnessAvailability{{
					ProtocolVersion:     modelregistry.HarnessQualificationProtocolVersion,
					Model:               "deepseek-v4-flash",
					QualificationStatus: llm.HarnessQualificationRequired,
				}},
			}},
		}}
	controller := application.NewThreadModelRouteService(nil, registry)
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ThreadModelRouteController: controller, AppVersion: "model-route-catalog-json-test"})
	if err != nil {
		t.Fatal(err)
	}

	response := performRequest(t, api, http.MethodGet, AvailableModelRoutesPath,
		testAccessToken, "127.0.0.1:8765", "127.0.0.1:45000", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("API status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			Routes []struct {
				QualificationStatus string          `json:"qualification_status"`
				UnavailableReason   string          `json:"unavailable_reason"`
				DefaultForRoutes    json.RawMessage `json:"default_for_routes"`
			} `json:"routes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode model route catalog response: %v body=%s", err, response.Body.String())
	}
	if len(envelope.Data.Routes) != 1 {
		t.Fatalf("route count=%d, want 1 body=%s", len(envelope.Data.Routes), response.Body.String())
	}
	if got := string(envelope.Data.Routes[0].DefaultForRoutes); got != "[]" {
		t.Fatalf("default_for_routes=%s, want [] body=%s", got, response.Body.String())
	}
	if route := envelope.Data.Routes[0]; route.QualificationStatus != llm.HarnessQualificationRequired ||
		route.UnavailableReason != "harness_qualification_required" {
		t.Fatalf("DeepSeek qualification response was not normalized: %+v body=%s",
			route, response.Body.String())
	}
}
