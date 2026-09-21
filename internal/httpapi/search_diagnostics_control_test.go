package httpapi

import (
	"context"
	"cyberagent-workbench/internal/application"
	"net/http"
	"strings"
	"testing"
)

type searchDiagnosticsFake struct {
	providerSearchReadinessControllerFake
	probes int
}

func (f *searchDiagnosticsFake) Check(_ context.Context, id string) (application.SearchDiagnostics, error) {
	f.probes++
	return application.SearchDiagnostics{ProtocolVersion: application.SearchDiagnosticsProtocolVersion, ThreadID: id, State: "failed", Code: "access_challenge", NetworkRequestAttempted: true}, nil
}

func TestSearchDiagnosticsHTTPRequiresExplicitControlRequest(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := &searchDiagnosticsFake{}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken, ProviderSearchReadinessController: controller, AppVersion: "diagnostic-test"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/threads/thread-search-check/search-diagnostics"
	good := `{"version":"search_diagnostics.v1","confirm":true}`
	for _, tc := range []struct {
		name, method, token, body string
		status                    int
	}{
		{"get", http.MethodGet, testControlToken, "", 405},
		{"read token", http.MethodPost, testAccessToken, good, 401},
		{"no token", http.MethodPost, "", good, 401},
		{"missing confirmation", http.MethodPost, testControlToken, `{"version":"search_diagnostics.v1"}`, 400},
		{"unknown field", http.MethodPost, testControlToken, `{"version":"search_diagnostics.v1","confirm":true,"auto":true}`, 400},
		{"duplicate", http.MethodPost, testControlToken, `{"version":"search_diagnostics.v1","confirm":false,"confirm":true}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := performSessionMessageRequest(t, api, tc.method, path, tc.token, "", "application/json", strings.NewReader(tc.body))
			if response.Code != tc.status || controller.probes != 0 {
				t.Fatalf("status=%d probes=%d body=%s", response.Code, controller.probes, response.Body.String())
			}
		})
	}
	response := performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "", "application/json", strings.NewReader(good))
	if response.Code != 200 || controller.probes != 1 || !strings.Contains(response.Body.String(), `"access_challenge"`) {
		t.Fatalf("status=%d probes=%d body=%s", response.Code, controller.probes, response.Body.String())
	}
}
