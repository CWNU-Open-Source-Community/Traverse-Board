package webevidence

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

const webSearchHTMLFixture = `<html><body><div id="links">
<div class="result--ad"><h2><a href="https://ads.example.com/">Sponsored</a></h2></div>
<div class="web-result result--ad"><h2><a href="https://ads.example.com/mixed">Sponsored mixed</a></h2></div>
<div class="web-result"><h2><a href="https://docs.example.com/report">Research &amp; evidence</a></h2><a class="result__snippet"><b>Verified</b> excerpt<script>bad()</script></a></div>
<div class="web-result"><h2><a href="https://docs.example.com/report#part">Duplicate</a></h2></div>
<div class="web-result"><h2><a href="https://127.0.0.1/private">Private</a></h2></div>
<div class="web-result"><h2><a href="http://docs.example.com/old">Insecure</a></h2></div>
<div class="web-result"><h2><a href="https://other.example.net/page">Second</a></h2></div>
</div></body></html>`

func TestWebSearchUsesAuthorizedSearchAndKeepsDiscoveryUnverified(t *testing.T) {
	requests := 0
	client := &SafeHTTPClient{Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}), TransportFactory: func(host string, _ []netip.Addr) http.RoundTripper {
		return roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			body, _ := io.ReadAll(request.Body)
			form, err := url.ParseQuery(string(body))
			if err != nil || request.Method != http.MethodPost || form.Get("q") != "黎曼猜想 最新进展" ||
				form.Get("kl") != "wt-wt" || !form.Has("b") || form.Get("b") != "" ||
				request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
				request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
				t.Fatal("search did not preserve the public query / credential-free HTML form")
			}
			if host != "html.duckduckgo.com" {
				t.Fatalf("unexpected search host %q", host)
			}
			return webResponse(http.StatusOK, http.Header{"Content-Type": {"text/html; charset=utf-8"}}, webSearchHTMLFixture), nil
		})
	}}
	provider := NewWebSearchProvider(client)
	results, err := provider.Search(t.Context(), "黎曼猜想 最新进展", 5, NetworkAuthority{
		Mode: "allowlist", AllowedTargets: []string{"html.duckduckgo.com"}})
	if err != nil || requests != 1 || len(results) != 2 || results[0].Title != "Research & evidence" ||
		results[0].Snippet != "Verified excerpt" || results[0].PublishedAt != "" || results[1].Rank != 2 {
		t.Fatalf("results=%#v requests=%d err=%v", results, requests, err)
	}
	resolver := &fakeSearchResolver{selection: SearchSelection{Policy: SearchPolicyWeb,
		Backend: "duckduckgo", SelectionReason: "configured_web_selected", Binding: strings.Repeat("a", 64), Provider: provider}}
	service := NewService(newMemoryWebStore(), nil, &fakeFetchBackend{}).WithSearchProviderResolver(resolver)
	scope := ExecutionScope{RunID: "run-web", MissionID: "mission-web", ModelRoute: "deepseek/model",
		Authority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"html.duckduckgo.com"}}}
	scope.ProviderFingerprint = service.SearchProviderFingerprintForScope(t.Context(), scope)
	first, err := service.Search(t.Context(), scope, SearchRequest{Query: "黎曼猜想 最新进展", Limit: 5}, "web-op")
	if err != nil || len(first.Sources) != 2 || first.SearchPolicy != SearchPolicyWeb ||
		first.Sources[0].Citeable || first.Sources[0].Fetched || first.Sources[0].ProviderGroundedCitation != nil {
		t.Fatalf("search=%#v err=%v", first, err)
	}
	beforeReplay := requests
	replay, err := service.Search(t.Context(), scope, SearchRequest{Query: "黎曼猜想 最新进展", Limit: 5}, "web-op")
	if err != nil || !replay.Replayed || requests != beforeReplay {
		t.Fatalf("replay=%#v requests=%d err=%v", replay, requests, err)
	}
}

func TestWebSearchRejectsDisabledNetworkAndRedirects(t *testing.T) {
	requests := 0
	client := &SafeHTTPClient{Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}), TransportFactory: func(string, []netip.Addr) http.RoundTripper {
		return roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return webResponse(http.StatusFound, http.Header{"Location": {"https://other.example.com/search"}}, ""), nil
		})
	}}
	provider := NewWebSearchProvider(client)
	if _, err := provider.Search(t.Context(), "public query", 1, NetworkAuthority{Mode: "disabled"}); err == nil || requests != 0 {
		t.Fatal("disabled network performed a request")
	}
	if _, err := provider.Search(t.Context(), "public query", 1, NetworkAuthority{
		Mode: "allowlist", AllowedTargets: []string{"html.duckduckgo.com"}}); err == nil || requests != 1 {
		t.Fatal("redirect escaped the exact Run allowlist")
	}
}

func TestWebSearchRejectsChallengesAndMalformedResults(t *testing.T) {
	for _, body := range []string{
		`<form id="challenge-form"></form>` + webSearchHTMLFixture,
		`<div class="g-recaptcha"></div>` + webSearchHTMLFixture,
		`<html><body>challenge</body></html>`, `<html><div id="links"><div class="web-result">missing link</div></div></html>`,
		`<div id="links"><div class="web-result"><h2><a href="https://127.0.0.1/private">private</a></h2></div></div>`,
	} {
		if result, err := parseWebSearchPage([]byte(body), 5); err == nil {
			t.Fatalf("invalid page accepted: %#v", result)
		}
	}
	if result, err := parseWebSearchPage([]byte(`<div id="links"><div class="result--no-result">No results</div></div>`), 5); err != nil || len(result) != 0 {
		t.Fatalf("valid empty result=%#v err=%v", result, err)
	}
}
