package application

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/webevidence"
)

func TestProviderSearchResolverDeepSeekUsesDDGOnlyForExplicitWebPolicy(t *testing.T) {
	definition := testProviderSearchDefinition("official-deepseek",
		modelregistry.ProviderSearchModeWeb)
	definition.EndpointURL = "https://api.deepseek.com/responses"
	definition.Transport = modelregistry.ProviderTransportOpenAIResponses
	definition.NativeWebSearchCapability = modelregistry.NativeWebSearchUnsupported
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
	authority := webevidence.NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{"html.duckduckgo.com"}}
	scope := webevidence.ExecutionScope{RunID: "run-web-choice",
		MissionID: "mission-web-choice", ModelRoute: "code", Authority: authority}
	service := webevidence.NewService(nil, nil, nil).WithSearchProviderResolver(resolver)
	if fingerprint := service.SearchProviderFingerprintForScope(t.Context(), scope); len(fingerprint) != 64 || requests != 0 {
		t.Fatalf("fingerprint=%q requests=%d", fingerprint, requests)
	}
	readiness := resolver.SearchReadiness(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"}, authority)
	if readiness.State != ProviderSearchStateReady ||
		readiness.RequiredTarget != "html.duckduckgo.com" || requests != 0 {
		t.Fatalf("readiness=%#v requests=%d", readiness, requests)
	}
	selection, err := resolver.ResolveSearch(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"}, authority)
	if err != nil || selection.Backend != "duckduckgo" ||
		selection.Policy != webevidence.SearchPolicyWeb ||
		selection.SelectionReason != "configured_web_selected" ||
		selection.ProviderAuthorityIndependent {
		t.Fatalf("selection=%#v err=%v", selection, err)
	}
	results, err := selection.Provider.Search(t.Context(), "public query", 1, authority)
	if err != nil || requests != 1 || len(results) != 1 {
		t.Fatalf("results=%#v requests=%d err=%v", results, requests, err)
	}
}

func TestProviderSearchResolverNeverSilentlyFallsBackToDDG(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		reason     string
		detailCode string
	}{
		{mode: modelregistry.ProviderSearchModeAuto,
			reason: ProviderSearchReasonBackendNotConfigured},
		{mode: modelregistry.ProviderSearchModeProviderNative,
			reason:     ProviderSearchReasonQualificationFailed,
			detailCode: webevidence.NativeSearchReasonToolUnsupported},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			definition := testProviderSearchDefinition("official-deepseek", tc.mode)
			definition.EndpointURL = "https://api.deepseek.com/responses"
			definition.Transport = modelregistry.ProviderTransportOpenAIResponses
			definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
			registry, settings, credentials := testProviderSearchRegistry(t, definition)
			requests := 0
			client := providerSearchHTTPClientForHost(t, "html.duckduckgo.com",
				func(*http.Request) (*http.Response, error) {
					requests++
					t.Fatal("an implicit policy attempted DuckDuckGo")
					return nil, nil
				})
			resolver, err := NewProviderSearchResolver(registry, settings, credentials, nil, client)
			if err != nil {
				t.Fatal(err)
			}
			authority := webevidence.NetworkAuthority{Mode: "allowlist",
				AllowedTargets: []string{"html.duckduckgo.com", "api.deepseek.com"}}
			scope := webevidence.ExecutionScope{RunID: "run-no-ddg",
				MissionID: "mission-no-ddg", ModelRoute: "code", Authority: authority}
			service := webevidence.NewService(nil, nil, nil).WithSearchProviderResolver(resolver)
			if fingerprint := service.SearchProviderFingerprintForScope(t.Context(), scope); fingerprint != "" {
				t.Fatalf("unexpected search fingerprint=%q", fingerprint)
			}
			readiness := resolver.SearchReadiness(t.Context(),
				webevidence.SearchRoute{ModelRoute: "code"}, authority)
			if readiness.State != ProviderSearchStateProviderUnavailable ||
				readiness.Reason != tc.reason || readiness.DetailCode != tc.detailCode ||
				readiness.RequiredTarget != "" {
				t.Fatalf("readiness=%#v", readiness)
			}
			if selection, err := resolver.ResolveSearch(t.Context(),
				webevidence.SearchRoute{ModelRoute: "code"}, authority); err == nil {
				t.Fatalf("unexpected selection=%#v", selection)
			}
			if requests != 0 {
				t.Fatalf("implicit DuckDuckGo requests=%d", requests)
			}
		})
	}
}

func TestProviderSearchResolverBuiltinRequiresConfiguredBackend(t *testing.T) {
	requests := 0
	client := providerSearchHTTPClientForHost(t, "html.duckduckgo.com",
		func(*http.Request) (*http.Response, error) {
			requests++
			t.Fatal("an unconfigured built-in route attempted DuckDuckGo")
			return nil, nil
		})
	resolver, err := NewProviderSearchResolver(modelregistry.New(nil),
		providerSearchSettings{}, credential.NewMemoryStore(), nil, client)
	if err != nil {
		t.Fatal(err)
	}
	authority := webevidence.NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{"html.duckduckgo.com"}}
	readiness := resolver.SearchReadiness(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"}, authority)
	if readiness.State != ProviderSearchStateProviderUnavailable ||
		readiness.Reason != ProviderSearchReasonBackendNotConfigured ||
		readiness.RequiredTarget != "" {
		t.Fatalf("readiness=%#v", readiness)
	}
	if selection, err := resolver.ResolveSearch(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"}, authority); err == nil {
		t.Fatalf("unexpected selection=%#v", selection)
	}
	if requests != 0 {
		t.Fatalf("implicit DuckDuckGo requests=%d", requests)
	}
}
