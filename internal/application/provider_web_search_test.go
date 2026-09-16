package application

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/webevidence"
)

func TestProviderSearchResolverDeepSeekUsesIndependentWebWithoutModelRequests(t *testing.T) {
	for _, mode := range []string{modelregistry.ProviderSearchModeAuto, modelregistry.ProviderSearchModeProviderNative, modelregistry.ProviderSearchModeWeb} {
		t.Run(mode, func(t *testing.T) {
			definition := testProviderSearchDefinition("official-deepseek", mode)
			definition.EndpointURL = "https://api.deepseek.com/responses"
			definition.Transport = modelregistry.ProviderTransportOpenAIResponses
			definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
			registry, settings, credentials := testProviderSearchRegistry(t, definition)
			requests := 0
			client := providerSearchHTTPClientForHost(t, "html.duckduckgo.com", func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Method != http.MethodPost || request.Header.Get("Authorization") != "" {
					t.Fatal("independent search used model credentials or the wrong method")
				}
				return &http.Response{StatusCode: http.StatusOK,
					Header: http.Header{"Content-Type": {"text/html"}},
					Body:   io.NopCloser(strings.NewReader(`<div id="links"><div class="web-result"><h2><a href="https://result.example.net/item">Search result</a></h2></div></div>`))}, nil
			})
			resolver, err := NewProviderSearchResolver(registry, settings, credentials, nil, client)
			if err != nil {
				t.Fatal(err)
			}
			authority := webevidence.NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"html.duckduckgo.com"}}
			scope := webevidence.ExecutionScope{RunID: "run-web-choice", MissionID: "mission-web-choice", ModelRoute: "code", Authority: authority}
			service := webevidence.NewService(nil, nil, nil).WithSearchProviderResolver(resolver)
			if fingerprint := service.SearchProviderFingerprintForScope(t.Context(), scope); len(fingerprint) != 64 || requests != 0 {
				t.Fatalf("fingerprint=%q requests=%d", fingerprint, requests)
			}
			readiness := resolver.SearchReadiness(t.Context(), webevidence.SearchRoute{ModelRoute: "code"}, authority)
			if readiness.State != ProviderSearchStateReady || readiness.RequiredTarget != "html.duckduckgo.com" || requests != 0 {
				t.Fatalf("readiness=%#v requests=%d", readiness, requests)
			}
			selection, err := resolver.ResolveSearch(t.Context(), webevidence.SearchRoute{ModelRoute: "code"}, authority)
			if err != nil || selection.Backend != "duckduckgo" || selection.ProviderAuthorityIndependent {
				t.Fatalf("selection=%#v err=%v", selection, err)
			}
			if mode == modelregistry.ProviderSearchModeProviderNative && (selection.Policy != webevidence.SearchPolicyWeb || selection.SelectionReason != "legacy_unsupported_native_selected_web") {
				t.Fatalf("legacy compatibility decision was not visible: %#v", selection)
			}
			results, err := selection.Provider.Search(t.Context(), "public query", 1, authority)
			if err != nil || requests != 1 || len(results) != 1 {
				t.Fatalf("results=%#v requests=%d err=%v", results, requests, err)
			}
			for _, denied := range []webevidence.NetworkAuthority{{Mode: "disabled"}, {Mode: "allowlist", AllowedTargets: []string{"api.deepseek.com"}}} {
				scope.Authority = denied
				if fingerprint := service.SearchProviderFingerprintForScope(t.Context(), scope); fingerprint != "" {
					t.Fatalf("model API authorization became public search authorization: %q", fingerprint)
				}
			}
			if requests != 1 {
				t.Fatal("readiness performed hidden I/O")
			}
		})
	}
}
