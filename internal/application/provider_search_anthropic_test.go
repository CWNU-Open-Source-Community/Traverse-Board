package application

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/webevidence"
)

const applicationAnthropicSearchResponse = `{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"server_tool_use","id":"srv-search-1","name":"web_search","input":{"query":"release date"}},{"type":"web_search_tool_result","tool_use_id":"srv-search-1","content":[{"type":"web_search_result","url":"https://docs.example.com/report","title":"Original report","encrypted_content":"fixture-opaque"}]},{"type":"text","text":"Report found","citations":[{"type":"web_search_result_location","url":"https://docs.example.com/report","title":"Original report","cited_text":"The original release date"}]}]}`

func TestAnthropicSearchResolverUsesCurrentRouteAndDomainTool(t *testing.T) {
	for _, mode := range []string{modelregistry.ProviderSearchModeProviderNative, modelregistry.ProviderSearchModeAuto} {
		t.Run(mode, func(t *testing.T) {
			definition := testProviderSearchDefinition("anthropic-route-"+mode, mode)
			definition.Transport = modelregistry.ProviderTransportAnthropicMessages
			definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
			registry, settings, credentials := testProviderSearchRegistry(t, definition)
			calls := 0
			client := providerSearchHTTPClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				var body struct {
					Model string
					Tools []struct {
						Type, Name     string
						AllowedDomains []string `json:"allowed_domains"`
					}
				}
				if json.NewDecoder(request.Body).Decode(&body) != nil || request.URL.String() != "https://api.example.com/v1/messages" ||
					request.Header.Get("x-api-key") == "" || request.Header.Get("anthropic-version") == "" || body.Model != definition.DefaultModel ||
					len(body.Tools) != 1 || body.Tools[0].Type != "web_search_20250305" || body.Tools[0].Name != "web_search" ||
					!reflect.DeepEqual(body.Tools[0].AllowedDomains, []string{"docs.example.com"}) {
					t.Fatal("current model, endpoint, native search or domain conditions did not reach provider request")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(applicationAnthropicSearchResponse))}, nil
			})
			resolver, err := NewProviderSearchResolver(registry, settings, credentials, nil, client)
			if err != nil {
				t.Fatal(err)
			}
			authority := webevidence.NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"docs.example.com"}}
			selection, err := resolver.ResolveSearch(t.Context(), webevidence.SearchRoute{ModelRoute: "code"}, authority)
			if err != nil || calls != 0 {
				t.Fatalf("local selection: calls=%d err=%v", calls, err)
			}
			native, ok := selection.Provider.(webevidence.NativeSearchProvider)
			filtered, filteredOK := selection.Provider.(webevidence.FilteredSearchProvider)
			if !ok || !filteredOK || native.QualificationSnapshot(t.Context(), selection.ProviderAuthority).Status != webevidence.SearchQualificationUnqualified || calls != 0 {
				t.Fatal("declaration performed network or claimed ready")
			}
			results, err := filtered.SearchFiltered(t.Context(), webevidence.SearchRequest{Query: "release date", Limit: 1, AllowedDomains: []string{"docs.example.com"}}, selection.ProviderAuthority)
			if err != nil || calls != 1 || len(results) != 1 || results[0].URL != "https://docs.example.com/report" || results[0].Snippet != "The original release date" ||
				native.QualificationSnapshot(t.Context(), selection.ProviderAuthority).Status != webevidence.SearchQualificationReady {
				t.Fatalf("native search result: calls=%d results=%+v err=%v", calls, results, err)
			}
		})
	}
}

func TestAnthropicSearchDiagnosticPauseDoesNotContinueOrFallBack(t *testing.T) {
	definition := testProviderSearchDefinition("anthropic-diagnostic", modelregistry.ProviderSearchModeProviderNative)
	definition.Transport = modelregistry.ProviderTransportAnthropicMessages
	definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	calls := 0
	client := providerSearchHTTPClient(t, func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(strings.Replace(applicationAnthropicSearchResponse, `"end_turn"`, `"pause_turn"`, 1)))}, nil
	})
	resolver, err := NewProviderSearchResolver(registry, settings, credentials, nil, client)
	if err != nil {
		t.Fatal(err)
	}
	service := NewProviderSearchReadinessService(diagnosticStore(), resolver)
	if _, err := service.Get(t.Context(), "thread-search-check"); err != nil || calls != 0 {
		t.Fatal("readiness called network", err)
	}
	result, err := service.Check(t.Context(), "thread-search-check")
	if err != nil || calls != 1 || result.State != "failed" || result.ResultCount != 0 || !result.NetworkRequestAttempted {
		t.Fatalf("incomplete one-shot diagnostic: calls=%d result=%+v err=%v", calls, result, err)
	}
}
