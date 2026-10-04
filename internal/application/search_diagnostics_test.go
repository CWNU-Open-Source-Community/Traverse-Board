package application

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/webevidence"
)

type diagnosticBackend struct {
	calls  int
	action func()
	err    error
}

func (*diagnosticBackend) Name() string     { return "searxng" }
func (*diagnosticBackend) Endpoint() string { return "https://search.example.com/search" }
func (b *diagnosticBackend) Search(context.Context, string, int, webevidence.NetworkAuthority) ([]webevidence.ProviderResult, error) {
	b.calls++
	if b.action != nil {
		b.action()
	}
	if b.err != nil {
		return nil, b.err
	}
	return []webevidence.ProviderResult{{URL: "https://result.example.com/one", Title: "Result"}}, nil
}

func diagnosticStore() *providerSearchReadinessStoreFake {
	return &providerSearchReadinessStoreFake{
		thread: domain.Thread{ID: "thread-search-check", ActiveRunID: "run-search-check"},
		run:    domain.Run{ID: "run-search-check", Config: domain.RunConfig{ModelRoute: "code"}},
		mode:   domain.RunModeSnapshot{Revision: 3, Scope: domain.Scope{NetworkMode: "allowlist", AllowedTargets: []string{"search.example.com"}}},
	}
}

func TestSearchDiagnosticsProbeAndConfigurationDrift(t *testing.T) {
	for _, change := range []string{"none", "mode", "run", "route", "rate_limit", "shell_network_disabled"} {
		t.Run(change, func(t *testing.T) {
			definition := testProviderSearchDefinition("diagnostic-drift", modelregistry.ProviderSearchModeSearXNG)
			registry, settings, credentials := testProviderSearchRegistry(t, definition)
			backend := &diagnosticBackend{}
			resolver, err := NewProviderSearchResolver(registry, settings, credentials, backend, nil)
			if err != nil {
				t.Fatal(err)
			}
			st := diagnosticStore()
			backend.action = func() {
				switch change {
				case "mode":
					st.mode.Revision++
				case "run":
					st.thread.ActiveRunID = "run-other"
					st.run.ID = "run-other"
				case "route":
					settings["route.code"] = "mock/mock-code"
					if _, err := registry.Reload(t.Context(), settings); err != nil {
						t.Fatal(err)
					}
				}
			}
			if change == "rate_limit" {
				backend.err = &webevidence.SearchDiagnosticError{Code: "rate_limited", HTTPStatus: 429, RetryAfter: "60", Message: "limited"}
			}
			if change == "shell_network_disabled" {
				st.mode.Scope.NetworkMode = "disabled"
				st.mode.Scope.AllowedTargets = nil
			}
			service := NewProviderSearchReadinessService(st, resolver)
			if _, err := service.Get(t.Context(), st.thread.ID); err != nil {
				t.Fatal(err)
			}
			if backend.calls != 0 {
				t.Fatal("readiness performed network I/O")
			}
			got, err := service.Check(t.Context(), st.thread.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "none", "shell_network_disabled":
				if got.State != "succeeded" || got.Code != "none" || got.ResultCount != 1 {
					t.Fatalf("%+v", got)
				}
			case "rate_limit":
				if got.Code != "rate_limited" || got.HTTPStatus != 429 || got.RetryAfter != "60" {
					t.Fatalf("%+v", got)
				}
			default:
				if got.Code != "configuration_changed" || got.State != "failed" || got.ResultCount != 0 {
					t.Fatalf("%+v", got)
				}
			}
			if backend.calls != 1 || !got.NetworkRequestAttempted {
				t.Fatalf("calls=%d result=%+v", backend.calls, got)
			}
		})
	}
}

func TestSearchDiagnosticsAutoUsesOneLocalCandidate(t *testing.T) {
	definition := testProviderSearchDefinition("diagnostic-auto", modelregistry.ProviderSearchModeAuto)
	definition.Transport = modelregistry.ProviderTransportOpenAIResponses
	definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	nativeCalls := 0
	client := providerSearchHTTPClient(t, func(*http.Request) (*http.Response, error) { nativeCalls++; return nil, context.Canceled })
	backend := &diagnosticBackend{}
	resolver, err := NewProviderSearchResolver(registry, settings, credentials, backend, client)
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewProviderSearchReadinessService(diagnosticStore(), resolver).Check(t.Context(), "thread-search-check")
	if err != nil || got.State != "succeeded" || got.Backend != "searxng" || backend.calls != 1 || nativeCalls != 0 {
		t.Fatalf("result=%+v err=%v fallback=%d native=%d", got, err, backend.calls, nativeCalls)
	}
}

func TestSearchDiagnosticsNativeRetriesFailedQualificationOnlyWhenExplicit(t *testing.T) {
	definition := testProviderSearchDefinition("diagnostic-native", modelregistry.ProviderSearchModeProviderNative)
	definition.Transport = modelregistry.ProviderTransportOpenAIResponses
	definition.NativeWebSearchCapability = modelregistry.NativeWebSearchDeclaredUnverified
	registry, settings, credentials := testProviderSearchRegistry(t, definition)
	calls := 0
	client := providerSearchHTTPClient(t, func(*http.Request) (*http.Response, error) {
		calls++
		status, body := 401, `{"error":{"message":"denied"}}`
		if calls > 1 {
			status = 200
			body = `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"sources":[{"url":"https://result.example.com/one","title":"Result"}]}}]}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	resolver, err := NewProviderSearchResolver(registry, settings, credentials, nil, client)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := resolver.ResolveSearch(t.Context(), webevidence.SearchRoute{ModelRoute: "code"}, webevidence.NetworkAuthority{Mode: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = selection.Provider.Search(t.Context(), "first", 1, selection.ProviderAuthority); err == nil {
		t.Fatal("expected prior qualification failure")
	}
	service := NewProviderSearchReadinessService(diagnosticStore(), resolver)
	before, err := service.Get(t.Context(), "thread-search-check")
	if err != nil || before.State != ProviderSearchStateProviderUnavailable || calls != 1 {
		t.Fatalf("%+v calls=%d err=%v", before, calls, err)
	}
	got, err := service.Check(t.Context(), "thread-search-check")
	if err != nil || got.State != "succeeded" || got.ResultCount != 1 || calls != 2 {
		t.Fatalf("%+v calls=%d err=%v", got, calls, err)
	}
}
