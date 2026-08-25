package webevidence

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestFetcherRechecksRobotsForEveryRedirectDestination(t *testing.T) {
	requests := make([]string, 0, 4)
	client := &SafeHTTPClient{
		Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}),
		TransportFactory: func(string, []netip.Addr) http.RoundTripper {
			return roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.URL.Host+request.URL.RequestURI())
				switch request.URL.Host + request.URL.Path {
				case "origin.example.com/robots.txt":
					return webResponse(http.StatusOK,
						http.Header{"Content-Type": {"text/plain"}},
						"User-agent: *\nAllow: /\n"), nil
				case "origin.example.com/start":
					return webResponse(http.StatusFound,
						http.Header{"Location": {"https://target.example.net/blocked"}}, ""), nil
				case "target.example.net/robots.txt":
					return webResponse(http.StatusOK,
						http.Header{"Content-Type": {"text/plain"}},
						"User-agent: *\nDisallow: /blocked\n"), nil
				case "target.example.net/blocked":
					t.Fatal("redirect destination was fetched despite its robots policy")
				}
				return webResponse(http.StatusNotFound,
					http.Header{"Content-Type": {"text/plain"}}, ""), nil
			})
		},
	}
	authority := NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{
		"origin.example.com", "target.example.net",
	}}
	_, err := NewFetcher(client).Fetch(t.Context(), "https://origin.example.com/start", authority)
	if err == nil || !strings.Contains(err.Error(), "robots") {
		t.Fatalf("redirect robots error=%v requests=%v", err, requests)
	}
	want := "origin.example.com/robots.txt,origin.example.com/start,target.example.net/robots.txt"
	if strings.Join(requests, ",") != want {
		t.Fatalf("requests=%v want=%s", requests, want)
	}
}
