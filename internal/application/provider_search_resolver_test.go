package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/webevidence"
)

type providerSearchResolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f providerSearchResolverFunc) LookupNetIP(ctx context.Context, network,
	host string,
) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

type providerSearchRoundTripFunc func(*http.Request) (*http.Response, error)

func (f providerSearchRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type providerSearchSettings map[string]string

func (s providerSearchSettings) GetProviderSetting(_ context.Context,
	key string,
) (string, bool, error) {
	value, found := s[key]
	return value, found, nil
}

type providerSearchFakeBackend struct{ endpoint string }

func (*providerSearchFakeBackend) Name() string { return "searxng" }
func (p *providerSearchFakeBackend) Endpoint() string {
	return p.endpoint
}
func (*providerSearchFakeBackend) Search(context.Context, string, int,
	webevidence.NetworkAuthority,
) ([]webevidence.ProviderResult, error) {
	return []webevidence.ProviderResult{}, nil
}

type providerSearchReadinessStoreFake struct {
	thread     domain.Thread
	run        domain.Run
	mode       domain.RunModeSnapshot
	permission domain.RunExecutionPermissionSnapshot
}

func (s providerSearchReadinessStoreFake) GetThread(context.Context, string) (domain.Thread, error) {
	return s.thread, nil
}

func (s providerSearchReadinessStoreFake) GetRun(context.Context, string) (domain.Run, error) {
	return s.run, nil
}

func (s providerSearchReadinessStoreFake) GetRunMode(context.Context, string) (
	domain.RunModeSnapshot, error,
) {
	return s.mode, nil
}

func (s providerSearchReadinessStoreFake) GetRunExecutionPermission(context.Context,
	string,
) (domain.RunExecutionPermissionSnapshot, error) {
	return s.permission, nil
}

func TestProviderSearchResolverUsesCurrentCustomRoutePolicy(t *testing.T) {
	definition := testProviderSearchDefinition("resolver-searxng",
		modelregistry.ProviderSearchModeSearXNG)
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	searxng := &providerSearchFakeBackend{endpoint: "https://search.example.com/search"}
	resolver, err := NewProviderSearchResolver(registry, settings, credentials,
		searxng, webevidence.NewSafeHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	selection, err := resolver.ResolveSearch(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"},
		webevidence.NetworkAuthority{Mode: "allowlist",
			AllowedTargets: []string{"search.example.com"}})
	if err != nil || selection.Policy != webevidence.SearchPolicySearXNG ||
		selection.Backend != "searxng" || selection.Provider != searxng ||
		selection.SelectionReason != "configured_searxng_selected" ||
		len(selection.Binding) != 64 {
		t.Fatalf("selection=%#v err=%v", selection, err)
	}

	// A direct Provider/model route is resolved independently of the symbolic
	// route table and therefore cannot inherit the custom Provider's policy.
	builtin, err := resolver.ResolveSearch(t.Context(),
		webevidence.SearchRoute{ModelRoute: "mock/mock-code"},
		webevidence.NetworkAuthority{Mode: "allowlist",
			AllowedTargets: []string{"search.example.com"}})
	if err != nil || builtin.SelectionReason != "process_searxng_selected" ||
		builtin.Provider != searxng {
		t.Fatalf("builtin selection=%#v err=%v", builtin, err)
	}
}

func TestProviderSearchResolverKeepsDisabledAndProviderNativeFailClosed(t *testing.T) {
	for _, mode := range []string{modelregistry.ProviderSearchModeDisabled,
		modelregistry.ProviderSearchModeProviderNative} {
		definition := testProviderSearchDefinition("resolver-disabled-"+mode, mode)
		if mode == modelregistry.ProviderSearchModeProviderNative {
			definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
			// A declaration on a Chat Completions transport must not be inferred
			// as hosted search merely because an endpoint path looks compatible.
			definition.Transport = modelregistry.ProviderTransportOpenAIChatCompletions
		}
		registry, settings, credentials := testProviderSearchRegistry(t, definition)
		resolver, err := NewProviderSearchResolver(registry, settings, credentials,
			&providerSearchFakeBackend{endpoint: "https://search.example.com/search"},
			webevidence.NewSafeHTTPClient())
		if err != nil {
			t.Fatal(err)
		}
		if selection, err := resolver.ResolveSearch(t.Context(),
			webevidence.SearchRoute{ModelRoute: "code"},
			webevidence.NetworkAuthority{Mode: "allowlist",
				AllowedTargets: []string{"api.example.com", "search.example.com"}}); err == nil {
			t.Fatalf("mode=%s unexpectedly selected %#v", mode, selection)
		}
	}
}

func TestProviderSearchResolverLazilyQualifiesExactNativePolicyOnFirstSearch(t *testing.T) {
	definition := testProviderSearchDefinition("resolver-native-once",
		modelregistry.ProviderSearchModeProviderNative)
	definition.Transport = modelregistry.ProviderTransportOpenAIResponses
	definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	requests, probes := 0, 0
	client := providerSearchHTTPClient(t, func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != "https://api.example.com/v1/responses" {
			t.Fatalf("Responses URL=%s", request.URL)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if input, _ := body["input"].(string); input == "OpenAI official website" {
			probes++
		}
		return &http.Response{StatusCode: http.StatusOK,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body:   io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"sources":[{"url":"https://result.example.net/item","title":"Result"}]}}]}`))}, nil
	})
	resolver, err := NewProviderSearchResolver(registry, settings, credentials,
		nil, client)
	if err != nil {
		t.Fatal(err)
	}
	authority := webevidence.NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{"api.example.com"}}
	// This is the same cold-process path used by Supervisor capability
	// construction. Advertising the exact provider_native tool must be local
	// and deterministic; the user's first real query is the bounded probe.
	searchService := webevidence.NewService(nil, nil, nil).
		WithSearchProviderResolver(resolver)
	fingerprint := searchService.SearchProviderFingerprintForScope(t.Context(),
		webevidence.ExecutionScope{RunID: "run-native-cold",
			MissionID: "mission-native-cold", ModelRoute: "code",
			Authority: authority})
	if len(fingerprint) != 64 || requests != 0 || probes != 0 {
		t.Fatalf("cold fingerprint=%q requests=%d probes=%d",
			fingerprint, requests, probes)
	}
	first, err := resolver.ResolveSearch(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"}, authority)
	if err != nil || requests != 0 || probes != 0 ||
		first.Policy != webevidence.SearchPolicyProviderNative ||
		first.SelectionReason != "declared_provider_native" || len(first.Binding) != 64 {
		t.Fatalf("first=%#v requests=%d probes=%d err=%v", first, requests, probes, err)
	}
	second, err := resolver.ResolveSearch(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"}, authority)
	if err != nil || requests != 0 || probes != 0 || first.Provider != second.Provider ||
		first.Binding != second.Binding {
		t.Fatalf("second=%#v requests=%d probes=%d err=%v", second, requests, probes, err)
	}
	results, err := second.Provider.Search(t.Context(), "real query", 1,
		second.ProviderAuthority)
	if err != nil || requests != 1 || probes != 0 || len(results) != 1 ||
		results[0].URL != "https://result.example.net/item" {
		t.Fatalf("results=%#v requests=%d probes=%d err=%v", results, requests, probes, err)
	}

	// Provider API egress is derived exactly from the configured model route and
	// is independent from the Run's direct web_fetch targets.
	independent, err := resolver.ResolveSearch(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"},
		webevidence.NetworkAuthority{Mode: "allowlist",
			AllowedTargets: []string{"result.example.net"}})
	if err != nil || requests != 1 || !independent.ProviderAuthorityIndependent {
		t.Fatalf("independent=%#v requests=%d err=%v", independent, requests, err)
	}
	if err := credentials.Put(t.Context(), definition.ID,
		"rotated-provider-secret-654321"); err != nil {
		t.Fatal(err)
	}
	rotated, err := resolver.ResolveSearch(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"}, authority)
	if err != nil || requests != 1 || probes != 0 {
		t.Fatalf("rotated requests=%d probes=%d err=%v", requests, probes, err)
	}
	if _, err := rotated.Provider.Search(t.Context(), "rotated query", 1,
		rotated.ProviderAuthority); err != nil || requests != 2 || probes != 0 {
		t.Fatalf("rotated search requests=%d probes=%d err=%v", requests, probes, err)
	}
}

func TestProviderSearchReadinessIsReadOnlyAndDeclaredCapabilityIsNotReady(t *testing.T) {
	definition := testProviderSearchDefinition("resolver-native-readiness",
		modelregistry.ProviderSearchModeProviderNative)
	definition.Transport = modelregistry.ProviderTransportOpenAIResponses
	definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	requests := 0
	client := providerSearchHTTPClient(t, func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"sources":[]}}]}`))}, nil
	})
	resolver, err := NewProviderSearchResolver(registry, settings, credentials, nil, client)
	if err != nil {
		t.Fatal(err)
	}
	route := webevidence.SearchRoute{ModelRoute: "code"}
	disabled := resolver.SearchReadiness(t.Context(), route,
		webevidence.NetworkAuthority{Mode: "disabled"})
	if disabled.State != ProviderSearchStateProviderUnqualified || disabled.RuntimeReady ||
		disabled.Reason != ProviderSearchReasonQualificationRequired ||
		disabled.RequiredTarget != "" || requests != 0 {
		t.Fatalf("disabled=%+v requests=%d", disabled, requests)
	}
	missing := resolver.SearchReadiness(t.Context(), route,
		webevidence.NetworkAuthority{Mode: "allowlist",
			AllowedTargets: []string{"docs.example.com"}})
	if missing.State != ProviderSearchStateProviderUnqualified || missing.RuntimeReady ||
		missing.RequiredTarget != "" || requests != 0 {
		t.Fatalf("missing=%+v requests=%d", missing, requests)
	}
	authority := webevidence.NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{"api.example.com"}}
	unqualified := resolver.SearchReadiness(t.Context(), route, authority)
	if unqualified.State != ProviderSearchStateProviderUnqualified ||
		unqualified.RuntimeReady ||
		unqualified.Reason != ProviderSearchReasonQualificationRequired || requests != 0 {
		t.Fatalf("unqualified=%+v requests=%d", unqualified, requests)
	}
	selection, err := resolver.ResolveSearch(t.Context(), route, authority)
	if err != nil || requests != 0 {
		t.Fatalf("selection=%#v requests=%d err=%v", selection, requests, err)
	}
	stillUnqualified := resolver.SearchReadiness(t.Context(), route, authority)
	if stillUnqualified.State != ProviderSearchStateProviderUnqualified ||
		stillUnqualified.RuntimeReady || requests != 0 {
		t.Fatalf("stillUnqualified=%+v requests=%d", stillUnqualified, requests)
	}
	if _, err := selection.Provider.Search(t.Context(), "first real query", 1,
		selection.ProviderAuthority); err != nil {
		t.Fatal(err)
	}
	ready := resolver.SearchReadiness(t.Context(), route, authority)
	if ready.State != ProviderSearchStateReady || !ready.RuntimeReady ||
		ready.Reason != ProviderSearchReasonBackendReady || requests != 1 {
		t.Fatalf("ready=%+v requests=%d", ready, requests)
	}
}

func TestProviderSearchReadinessProjectsConfiguredSearXNGWithoutProbe(t *testing.T) {
	definition := testProviderSearchDefinition("resolver-searxng-readiness",
		modelregistry.ProviderSearchModeSearXNG)
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	searxng := &providerSearchFakeBackend{endpoint: "https://search.example.com/search"}
	resolver, err := NewProviderSearchResolver(registry, settings, credentials,
		searxng, webevidence.NewSafeHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	readiness := resolver.SearchReadiness(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"},
		webevidence.NetworkAuthority{Mode: "allowlist",
			AllowedTargets: []string{"search.example.com"}})
	if readiness.State != ProviderSearchStateReady || !readiness.RuntimeReady ||
		readiness.SearchPolicy != modelregistry.ProviderSearchModeSearXNG ||
		readiness.RequiredTarget != "search.example.com" {
		t.Fatalf("readiness=%+v", readiness)
	}
}

func TestProviderSearchReadinessServiceBindsActiveThreadRunAndMode(t *testing.T) {
	definition := testProviderSearchDefinition("resolver-thread-readiness",
		modelregistry.ProviderSearchModeSearXNG)
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	resolver, err := NewProviderSearchResolver(registry, settings, credentials,
		&providerSearchFakeBackend{endpoint: "https://search.example.com/search"},
		webevidence.NewSafeHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	store := providerSearchReadinessStoreFake{
		thread: domain.Thread{ID: "thread-search-ready", ActiveRunID: "run-search-ready"},
		run: domain.Run{ID: "run-search-ready",
			Config: domain.RunConfig{ModelRoute: "code"}},
		mode: domain.RunModeSnapshot{RunID: "run-search-ready", Revision: 7,
			Scope: domain.Scope{NetworkMode: "allowlist",
				AllowedTargets: []string{"search.example.com"}}},
		permission: domain.RunExecutionPermissionSnapshot{
			Mode: domain.RunExecutionPermissionConservative},
	}
	view, err := NewProviderSearchReadinessService(store, resolver).Get(
		t.Context(), store.thread.ID)
	if err != nil || view.ThreadID != store.thread.ID || view.RunID != store.run.ID ||
		view.ModeRevision != 7 || view.State != ProviderSearchStateReady ||
		!view.RuntimeReady || view.CapabilityGrant {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	store.thread.ActiveRunID = ""
	noRun, err := NewProviderSearchReadinessService(store, resolver).Get(
		t.Context(), store.thread.ID)
	if err != nil || noRun.State != ProviderSearchStateProviderUnavailable ||
		noRun.Reason != ProviderSearchReasonNoActiveRun || noRun.RuntimeReady {
		t.Fatalf("noRun=%+v err=%v", noRun, err)
	}
}

func TestProviderSearchReadinessServiceUsesEffectiveFullAccessWebAuthority(t *testing.T) {
	definition := testProviderSearchDefinition("resolver-full-access-readiness",
		modelregistry.ProviderSearchModeSearXNG)
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	resolver, err := NewProviderSearchResolver(registry, settings, credentials,
		&providerSearchFakeBackend{endpoint: "https://search.example.com/search"},
		webevidence.NewSafeHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	store := providerSearchReadinessStoreFake{
		thread: domain.Thread{ID: "thread-search-full", ActiveRunID: "run-search-full"},
		run: domain.Run{ID: "run-search-full",
			Config: domain.RunConfig{ModelRoute: "code"}},
		mode: domain.RunModeSnapshot{RunID: "run-search-full", Revision: 9,
			Scope: domain.Scope{NetworkMode: "disabled"}},
		permission: domain.RunExecutionPermissionSnapshot{
			Mode: domain.RunExecutionPermissionFullAccess},
	}
	view, err := NewProviderSearchReadinessService(store, resolver).Get(
		t.Context(), store.thread.ID)
	if err != nil || view.State != ProviderSearchStateReady || !view.RuntimeReady ||
		view.NetworkMode != "allowlist" || view.RequiredTarget != "search.example.com" {
		t.Fatalf("full access readiness=%+v err=%v", view, err)
	}
}

func TestProviderSearchResolverAutoFallbackIsExplicitAndNegativeCached(t *testing.T) {
	definition := testProviderSearchDefinition("resolver-auto-negative",
		modelregistry.ProviderSearchModeAuto)
	definition.Transport = modelregistry.ProviderTransportOpenAIResponses
	definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	requests := 0
	client := providerSearchHTTPClient(t, func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusUnauthorized,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body:   io.NopCloser(strings.NewReader(`{"error":{"message":"denied"}}`))}, nil
	})
	searxng := &providerSearchFakeBackend{endpoint: "https://search.example.com/search"}
	resolver, err := NewProviderSearchResolver(registry, settings, credentials,
		searxng, client)
	if err != nil {
		t.Fatal(err)
	}
	authority := webevidence.NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{"api.example.com", "search.example.com"}}
	for attempt := 0; attempt < 2; attempt++ {
		selection, err := resolver.ResolveSearch(t.Context(),
			webevidence.SearchRoute{ModelRoute: "code"}, authority)
		if err != nil || selection.Provider != searxng ||
			selection.Policy != webevidence.SearchPolicyAuto ||
			selection.SelectionReason != "auto_native_unavailable_selected_searxng" {
			t.Fatalf("attempt=%d selection=%#v err=%v", attempt, selection, err)
		}
	}
	if requests != 1 {
		t.Fatalf("auto negative qualification requests=%d", requests)
	}
}

func TestProviderSearchResolverAutoWithoutFallbackLazilyUsesDeclaredNative(t *testing.T) {
	definition := testProviderSearchDefinition("official-deepseek",
		modelregistry.ProviderSearchModeAuto)
	definition.DisplayName = "DeepSeek Official"
	definition.EndpointURL = "https://api.deepseek.com/responses"
	definition.DefaultModel = "deepseek-v4-flash"
	definition.Models = []string{"deepseek-v4-flash", "deepseek-v4-pro"}
	definition.Transport = modelregistry.ProviderTransportOpenAIResponses
	definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	requests := 0
	client := providerSearchHTTPClientForHost(t, "api.deepseek.com",
		func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search","queries":["first auto query"]}},{"type":"message","status":"completed","content":[{"type":"output_text","text":"{\"results\":[{\"url\":\"https://result.example.net/auto\",\"title\":\"Auto result\",\"snippet\":\"Grounded result\"}]}"}]}]}`))}, nil
		})
	resolver, err := NewProviderSearchResolver(registry, settings, credentials, nil, client)
	if err != nil {
		t.Fatal(err)
	}
	authority := webevidence.NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{"api.deepseek.com"}}
	fingerprint := webevidence.NewService(nil, nil, nil).
		WithSearchProviderResolver(resolver).
		SearchProviderFingerprintForScope(t.Context(), webevidence.ExecutionScope{
			RunID: "run-official-deepseek", MissionID: "mission-official-deepseek",
			ModelRoute: "code", Authority: authority})
	if len(fingerprint) != 64 || requests != 0 {
		t.Fatalf("official DeepSeek fingerprint=%q requests=%d", fingerprint, requests)
	}
	selection, err := resolver.ResolveSearch(t.Context(),
		webevidence.SearchRoute{ModelRoute: "code"}, authority)
	if err != nil || requests != 0 || selection.Policy != webevidence.SearchPolicyAuto ||
		selection.SelectionReason != "auto_declared_provider_native" ||
		len(selection.Binding) != 64 {
		t.Fatalf("selection=%#v requests=%d err=%v", selection, requests, err)
	}
	results, err := selection.Provider.Search(t.Context(), "first auto query", 1,
		selection.ProviderAuthority)
	if err != nil || requests != 1 || len(results) != 1 ||
		results[0].URL != "https://result.example.net/auto" {
		t.Fatalf("results=%#v requests=%d err=%v", results, requests, err)
	}
}

func testProviderSearchDefinition(id string, searchMode string) modelregistry.ProviderDefinition {
	return modelregistry.ProviderDefinition{Version: modelregistry.ProviderDefinitionVersion,
		ID: id, DisplayName: id, EndpointURL: "https://api.example.com/v1",
		DefaultModel: "model-a", Models: []string{"model-a"},
		Transport:                 modelregistry.ProviderTransportOpenAIChatCompletions,
		SearchMode:                searchMode,
		NativeWebSearchCapability: modelregistry.NativeWebSearchUnsupported,
		AdvancedConfig:            json.RawMessage(`{}`), Enabled: true, Revision: 1}
}

func testProviderSearchRegistry(t *testing.T,
	definition modelregistry.ProviderDefinition,
) (*modelregistry.Registry, providerSearchSettings, *credential.MemoryStore) {
	t.Helper()
	encoded, err := modelregistry.EncodeProviderDefinitionCollection(
		modelregistry.ProviderDefinitionCollection{
			Version:  modelregistry.ProviderDefinitionCollectionVersion,
			Revision: 1, Providers: []modelregistry.ProviderDefinition{definition}})
	if err != nil {
		t.Fatal(err)
	}
	settings := providerSearchSettings{modelregistry.ProviderDefinitionsSettingKey: encoded,
		"route.code": definition.ID + "/" + definition.DefaultModel}
	credentials := credential.NewMemoryStore()
	if err := credentials.Put(t.Context(), definition.ID,
		"test-provider-secret-123456"); err != nil {
		t.Fatal(err)
	}
	registry, err := modelregistry.NewFromEnvironmentWithCredentials(credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	return registry, settings, credentials
}

func providerSearchHTTPClient(t *testing.T,
	roundTrip providerSearchRoundTripFunc,
) *webevidence.SafeHTTPClient {
	return providerSearchHTTPClientForHost(t, "api.example.com", roundTrip)
}

func providerSearchHTTPClientForHost(t *testing.T, expectedHost string,
	roundTrip providerSearchRoundTripFunc,
) *webevidence.SafeHTTPClient {
	t.Helper()
	return &webevidence.SafeHTTPClient{Resolver: providerSearchResolverFunc(
		func(_ context.Context, _, host string) ([]netip.Addr, error) {
			if host != expectedHost {
				t.Fatalf("resolved host=%q", host)
			}
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}), TransportFactory: func(host string,
		_ []netip.Addr,
	) http.RoundTripper {
		if host != expectedHost {
			t.Fatalf("transport host=%q", host)
		}
		return roundTrip
	}}
}
