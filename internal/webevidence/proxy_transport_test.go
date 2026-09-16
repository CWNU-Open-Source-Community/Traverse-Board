package webevidence

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http/httpproxy"
)

func TestWebProxySelectionPreservesExplicitConfiguration(t *testing.T) {
	systemCalls := 0
	system := func() (webSystemProxy, error) {
		systemCalls++
		return webSystemProxy{enabled: true, server: "http=127.0.0.1:7891;https=127.0.0.1:7890", bypass: "*.internal.example;<local>"}, nil
	}
	for _, test := range []struct {
		name        string
		environment httpproxy.Config
		all         string
		host        string
		want        string
		wantError   bool
		usesSystem  bool
	}{
		{name: "HTTPS environment wins", environment: httpproxy.Config{HTTPSProxy: "http://127.0.0.1:8900"}, host: "public.example", want: "127.0.0.1:8900"},
		{name: "environment NO_PROXY is explicit direct", environment: httpproxy.Config{HTTPSProxy: "127.0.0.1:8900", NoProxy: "public.example"}, host: "public.example"},
		{name: "HTTP only retains Go HTTPS semantics", environment: httpproxy.Config{HTTPProxy: "127.0.0.1:8900"}, host: "public.example"},
		{name: "system HTTPS mapping", host: "public.example", want: "127.0.0.1:7890", usesSystem: true},
		{name: "NO_PROXY applies to system", environment: httpproxy.Config{NoProxy: "public.example"}, host: "public.example", usesSystem: true},
		{name: "system bypass", host: "api.internal.example", usesSystem: true},
		{name: "unsupported protocol is not direct", environment: httpproxy.Config{HTTPSProxy: "socks5://private:credential@127.0.0.1:8900"}, host: "public.example", wantError: true},
		{name: "ALL_PROXY is not ignored", all: "socks5://private:credential@127.0.0.1:8900", host: "public.example", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := systemCalls
			selected, err := selectWebProxy(test.host, test.environment, test.all, system)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v", err)
			}
			if err != nil && strings.Contains(err.Error(), "credential") {
				t.Fatal("proxy error exposed configured credentials")
			}
			got := ""
			if selected != nil {
				got = selected.Host
			}
			if got != test.want || (systemCalls > before) != test.usesSystem {
				t.Fatalf("selected host=%q system read=%v", got, systemCalls > before)
			}
		})
	}
	for _, settings := range []webSystemProxy{
		{pac: true}, {enabled: true, server: "socks=127.0.0.1:7890"},
		{enabled: true, server: "https=127.0.0.1:7890;https=127.0.0.1:7891"},
	} {
		if _, err := selectWebProxy("public.example", httpproxy.Config{}, "", func() (webSystemProxy, error) { return settings, nil }); err == nil {
			t.Fatal("unsupported automatic/static configuration silently fell back to direct")
		}
	}
}

func TestSafeHTTPProxyConnectPinsIPAndVerifiesOriginTLS(t *testing.T) {
	var originRequests atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		if r.Host != "example.com" || r.TLS == nil || r.TLS.ServerName != "example.com" ||
			r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer origin-only" {
			t.Error("origin identity or credential boundary changed")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"input":"one request"}` {
			t.Error("origin request body changed")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "bounded origin result")
	}))
	defer origin.Close()
	originURL, _ := url.Parse(origin.URL)
	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects.Add(1)
		if r.Method != http.MethodConnect || r.Host != "93.184.216.34:443" || r.URL.Host != "93.184.216.34:443" {
			t.Error("proxy CONNECT did not use the verified public IP")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "Basic cHJveHktdXNlcjpwcm94eS1wYXNz" {
			t.Error("CONNECT credential boundary changed")
		}
		upstream, err := net.Dial("tcp", originURL.Host)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffered); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
		<-done
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("proxy-user", "proxy-pass")
	t.Setenv("HTTPS_PROXY", proxyURL.String())
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("NO_PROXY", "")
	client := NewProviderSearchHTTPClient()
	client.Resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	})
	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	client.TransportFactory = func(host string, addresses []netip.Addr) http.RoundTripper {
		transport := pinnedTransport(host, addresses).(*http.Transport)
		transport.TLSClientConfig.RootCAs = pool // Trust this fixture CA; hostname verification stays enabled.
		return transport
	}
	document, err := client.PostJSONAuthorizedNoRedirect(t.Context(), "https://example.com/responses",
		[]byte(`{"input":"one request"}`), 1024, http.Header{"Authorization": {"Bearer origin-only"}}, nil)
	if err != nil || document.StatusCode != http.StatusServiceUnavailable || string(document.Body) != "bounded origin result" || connects.Load() != 1 || originRequests.Load() != 1 {
		t.Fatalf("status=%d err=%v CONNECT=%d POST=%d", document.StatusCode, err, connects.Load(), originRequests.Load())
	}
	_, err = client.PostJSONAuthorizedNoRedirect(t.Context(), "https://wrong.example.org/responses",
		[]byte(`{"input":"one request"}`), 1024, nil, nil)
	if err == nil || connects.Load() != 2 || originRequests.Load() != 1 {
		t.Fatalf("wrong-host TLS rejection: err=%v CONNECT=%d origin requests=%d", err, connects.Load(), originRequests.Load())
	}
}

func TestWebProxyCONNECTCancellationAndRejectionDoNotDialDirect(t *testing.T) {
	for _, status := range []int{http.StatusProxyAuthRequired, 0} {
		name := http.StatusText(status)
		if status == 0 {
			name = "cancel pending CONNECT"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			arrived := make(chan struct{})
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				close(arrived)
				if status != 0 {
					w.WriteHeader(status)
					return
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_, _ = io.Copy(io.Discard, conn) // No CONNECT response; cancellation must close it.
			}))
			defer proxy.Close()
			proxyURL, _ := url.Parse(proxy.URL)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				conn, err := dialPinnedWebTarget(ctx, []netip.Addr{netip.MustParseAddr("93.184.216.34")}, proxyURL)
				if conn != nil {
					_ = conn.Close()
				}
				result <- err
			}()
			select {
			case <-arrived:
			case <-time.After(time.Second):
				t.Fatal("proxy did not receive CONNECT")
			}
			if status == 0 {
				cancel()
			}
			select {
			case err := <-result:
				if err == nil || (status == 0 && !errors.Is(err, context.Canceled)) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("proxy failure/cancellation did not terminate promptly")
			}
			if requests.Load() != 1 {
				t.Fatalf("CONNECT count=%d", requests.Load())
			}
		})
	}
}

func TestSafeHTTPProxyDoesNotBypassAuthorityOrPublicDNS(t *testing.T) {
	var factories int
	client := NewSafeHTTPClient()
	client.Resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	})
	client.TransportFactory = func(string, []netip.Addr) http.RoundTripper { factories++; return nil }
	for _, authority := range []NetworkAuthority{{Mode: "disabled"}, {Mode: "allowlist", AllowedTargets: []string{"example.com"}}} {
		_, err := client.GetAuthorized(t.Context(), "https://example.com/", 1024, "text/plain", func(target string) error { _, err := authority.Authorize(target); return err })
		if err == nil || factories != 0 {
			t.Fatal("proxy transport was reached before authority/public DNS rejection")
		}
	}
	if conn, err := dialPinnedWebTarget(t.Context(), []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil); err == nil || conn != nil {
		t.Fatal("pinned dial accepted a private target")
	}
}
