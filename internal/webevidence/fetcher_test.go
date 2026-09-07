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
	_, err := NewFetcher(client).Fetch(t.Context(), "https://origin.example.com/start", authority,
		RobotsPolicyEnforce)
	if err == nil || !strings.Contains(err.Error(), "robots") {
		t.Fatalf("redirect robots error=%v requests=%v", err, requests)
	}
	want := "origin.example.com/robots.txt,origin.example.com/start,target.example.net/robots.txt"
	if strings.Join(requests, ",") != want {
		t.Fatalf("requests=%v want=%s", requests, want)
	}
}

func TestFetcherAuditOnlyBypassesRobotsDisallowAndRecordsIt(t *testing.T) {
	requests := make([]string, 0, 2)
	client := &SafeHTTPClient{
		Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}),
		TransportFactory: func(string, []netip.Addr) http.RoundTripper {
			return roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.URL.Path)
				switch request.URL.Path {
				case "/robots.txt":
					return webResponse(http.StatusOK,
						http.Header{"Content-Type": {"text/plain"}},
						"User-agent: *\nDisallow: /article\n"), nil
				case "/article":
					return webResponse(http.StatusOK,
						http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
						"operator-authorized public content"), nil
				default:
					return webResponse(http.StatusNotFound,
						http.Header{"Content-Type": {"text/plain"}}, ""), nil
				}
			})
		},
	}
	authority := NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{PublicHTTPSTarget}}
	result, err := NewFetcher(client).Fetch(t.Context(), "https://docs.example.com/article",
		authority, RobotsPolicyAuditOnly)
	if err != nil || result.Robots != "bypassed_disallow" ||
		result.HTTPStatus != http.StatusOK ||
		result.Parsed.Body != "operator-authorized public content" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if got := strings.Join(requests, ","); got != "/robots.txt,/article" {
		t.Fatalf("requests=%q", got)
	}
}

func TestFetcherAuditOnlyBypassesUnknownRobotsAndRecordsIt(t *testing.T) {
	requests := make([]string, 0, 2)
	client := &SafeHTTPClient{
		Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}),
		MaxRetries: 0,
		TransportFactory: func(string, []netip.Addr) http.RoundTripper {
			return roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.URL.Path)
				if request.URL.Path == "/robots.txt" {
					return webResponse(http.StatusServiceUnavailable,
						http.Header{"Content-Type": {"text/plain"}}, "unavailable"), nil
				}
				return webResponse(http.StatusOK,
					http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
					"public content despite unknown robots"), nil
			})
		},
	}
	authority := NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{PublicHTTPSTarget}}
	result, err := NewFetcher(client).Fetch(t.Context(), "https://docs.example.com/article",
		authority, RobotsPolicyAuditOnly)
	if err != nil || result.Robots != "bypassed_unknown" ||
		result.HTTPStatus != http.StatusOK ||
		result.Parsed.Body != "public content despite unknown robots" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if got := strings.Join(requests, ","); got != "/robots.txt,/article" {
		t.Fatalf("requests=%q", got)
	}
}

func TestFetcherReturnsObservedHTTPStatusOnFailure(t *testing.T) {
	t.Parallel()
	client := &SafeHTTPClient{
		Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}),
		MaxRetries: 0,
		TransportFactory: func(string, []netip.Addr) http.RoundTripper {
			return roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/robots.txt" {
					return webResponse(http.StatusNotFound,
						http.Header{"Content-Type": {"text/plain"}}, ""), nil
				}
				return webResponse(http.StatusTooManyRequests,
					http.Header{"Content-Type": {"text/plain"}}, "retry later"), nil
			})
		},
	}
	result, err := NewFetcher(client).Fetch(t.Context(), "https://docs.example.com/rate-limited",
		NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}},
		RobotsPolicyEnforce)
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") ||
		result.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
