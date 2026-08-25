package webevidence

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func webResponse(status int, headers http.Header, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: headers,
		Body: io.NopCloser(strings.NewReader(body))}
}

func TestSafeHTTPClientPinsResolvedPublicAddress(t *testing.T) {
	approved := netip.MustParseAddr("93.184.216.34")
	transportCalled := false
	client := &SafeHTTPClient{Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{approved, approved}, nil
	}), MaxRedirects: 2, TransportFactory: func(host string, addresses []netip.Addr) http.RoundTripper {
		transportCalled = true
		if host != "www.example.com" || len(addresses) != 1 || addresses[0] != approved {
			t.Fatalf("unpinned transport host=%q addresses=%v", host, addresses)
		}
		return roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("User-Agent") != WebEvidenceUserAgent ||
				request.Header.Get("Cookie") != "" || request.URL.User != nil {
				t.Fatalf("unsafe request %#v", request)
			}
			return webResponse(http.StatusOK, http.Header{"Content-Type": {"text/plain"}}, "evidence"), nil
		})
	}}
	document, err := client.GetAuthorized(context.Background(), "https://www.example.com", 32,
		"text/plain", func(string) error { return nil })
	if err != nil || !transportCalled || string(document.Body) != "evidence" ||
		document.FinalURL != "https://www.example.com/" {
		t.Fatalf("document=%#v transport=%t err=%v", document, transportCalled, err)
	}
}

func TestSafeHTTPClientRejectsMixedOrPrivateDNSBeforeTransport(t *testing.T) {
	transportCalled := false
	client := &SafeHTTPClient{Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"),
			netip.MustParseAddr("127.0.0.1")}, nil
	}), TransportFactory: func(string, []netip.Addr) http.RoundTripper {
		transportCalled = true
		return roundTripFunc(nil)
	}}
	if _, err := client.Get(context.Background(), "https://www.example.com/", 32,
		"text/plain"); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("mixed DNS err=%v", err)
	}
	if transportCalled {
		t.Fatal("transport was created for mixed public/private DNS")
	}
}

func TestSafeHTTPClientRejectsOversizedDNSAndMissingPinnedTransport(t *testing.T) {
	addresses := make([]netip.Addr, DefaultAddressLimit+1)
	for index := range addresses {
		addresses[index] = netip.MustParseAddr("93.184.216.34")
	}
	client := &SafeHTTPClient{Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return addresses, nil
	})}
	if _, err := client.Get(t.Context(), "https://www.example.com/", 32,
		"text/plain"); err == nil || !strings.Contains(err.Error(), "address limit") {
		t.Fatalf("oversized DNS err=%v", err)
	}
	client.Resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	})
	client.TransportFactory = func(string, []netip.Addr) http.RoundTripper { return nil }
	if _, err := client.Get(t.Context(), "https://www.example.com/", 32,
		"text/plain"); err == nil || !strings.Contains(err.Error(), "pinned transport") {
		t.Fatalf("missing transport err=%v", err)
	}
}

func TestSafeHTTPClientRequestDeadlineIncludesDNSResolution(t *testing.T) {
	client := &SafeHTTPClient{Timeout: 20 * time.Millisecond,
		Resolver: resolverFunc(func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})}
	started := time.Now()
	_, err := client.Get(t.Context(), "https://www.example.com/", 32, "text/plain")
	if err == nil || !strings.Contains(err.Error(), "resolve web target") ||
		time.Since(started) > time.Second {
		t.Fatalf("DNS deadline elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestSafeHTTPClientRevalidatesRedirectDNSAndRunAuthority(t *testing.T) {
	var resolved []string
	client := &SafeHTTPClient{MaxRedirects: 2,
		Resolver: resolverFunc(func(_ context.Context, _, host string) ([]netip.Addr, error) {
			resolved = append(resolved, host)
			if host == "redirect.example.com" {
				return []netip.Addr{netip.MustParseAddr("10.0.0.7")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}),
		TransportFactory: func(string, []netip.Addr) http.RoundTripper {
			return roundTripFunc(func(*http.Request) (*http.Response, error) {
				return webResponse(http.StatusFound,
					http.Header{"Location": {"https://redirect.example.com/private"}}, ""), nil
			})
		}}
	if _, err := client.GetAuthorized(context.Background(), "https://www.example.com/", 32,
		"text/plain", func(string) error { return nil }); err == nil ||
		!strings.Contains(err.Error(), "non-public") {
		t.Fatalf("redirect DNS err=%v", err)
	}
	if strings.Join(resolved, ",") != "www.example.com,redirect.example.com" {
		t.Fatalf("resolved hops=%v", resolved)
	}

	authorityCalls := 0
	client.Resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	})
	_, err := client.GetAuthorized(context.Background(), "https://www.example.com/", 32,
		"text/plain", func(raw string) error {
			authorityCalls++
			if strings.Contains(raw, "redirect.example.com") {
				return io.EOF
			}
			return nil
		})
	if err == nil || authorityCalls != 2 || !strings.Contains(err.Error(), "Run authority") {
		t.Fatalf("redirect authority calls=%d err=%v", authorityCalls, err)
	}
}

func TestSafeHTTPClientRetriesOnlyBoundedTransientStatuses(t *testing.T) {
	requests := 0
	resolves := 0
	client := &SafeHTTPClient{MaxRetries: 1,
		Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
			resolves++
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}),
		TransportFactory: func(string, []netip.Addr) http.RoundTripper {
			return roundTripFunc(func(*http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					return webResponse(http.StatusServiceUnavailable, http.Header{}, "retry"), nil
				}
				return webResponse(http.StatusOK, http.Header{}, "ok"), nil
			})
		}}
	document, err := client.Get(context.Background(), "https://www.example.com/", 32, "text/plain")
	if err != nil || string(document.Body) != "ok" || requests != 2 || resolves != 2 {
		t.Fatalf("document=%#v requests=%d resolves=%d err=%v",
			document, requests, resolves, err)
	}
}

func TestSafeHTTPClientEnforcesRedirectAndResponseBounds(t *testing.T) {
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	})
	redirects := 0
	redirectClient := &SafeHTTPClient{Resolver: resolver, MaxRedirects: 1,
		TransportFactory: func(string, []netip.Addr) http.RoundTripper {
			return roundTripFunc(func(*http.Request) (*http.Response, error) {
				redirects++
				return webResponse(http.StatusFound,
					http.Header{"Location": {"https://www.example.com/next"}}, ""), nil
			})
		}}
	if _, err := redirectClient.Get(context.Background(), "https://www.example.com/start", 32,
		"text/plain"); err == nil || !strings.Contains(err.Error(), "redirect limit") || redirects != 2 {
		t.Fatalf("redirects=%d err=%v", redirects, err)
	}

	boundedClient := &SafeHTTPClient{Resolver: resolver,
		TransportFactory: func(string, []netip.Addr) http.RoundTripper {
			return roundTripFunc(func(*http.Request) (*http.Response, error) {
				return webResponse(http.StatusOK, http.Header{}, "0123456789"), nil
			})
		}}
	document, err := boundedClient.Get(context.Background(), "https://www.example.com/", 4,
		"text/plain")
	if err != nil || !document.Truncated || string(document.Body) != "0123" {
		t.Fatalf("document=%#v err=%v", document, err)
	}
}
