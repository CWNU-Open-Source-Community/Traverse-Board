package webevidence

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSafeHTTPFormPOSTPreservesBodyAndNeverRetriesOrRedirects(t *testing.T) {
	fields := url.Values{"q": {`黎曼猜想 latest + "progress" & sources`}, "b": {""}, "kl": {"wt-wt"}}
	body := []byte(fields.Encode())
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := NewSafeHTTPClient()
			client.MaxRetries, client.MaxRedirects = 1, 3
			client.Resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			})
			requests, authorized := 0, 0
			client.TransportFactory = func(host string, addresses []netip.Addr) http.RoundTripper {
				if host != "html.duckduckgo.com" || len(addresses) != 1 || !IsPublicAddress(addresses[0]) {
					t.Fatal("form lost exact public target pin")
				}
				return roundTripFunc(func(request *http.Request) (*http.Response, error) {
					requests++
					actual, _ := io.ReadAll(request.Body)
					decoded, err := url.ParseQuery(string(actual))
					if err != nil || !reflect.DeepEqual(decoded, fields) || string(actual) != string(body) || request.Method != http.MethodPost || request.URL.String() != "https://html.duckduckgo.com/html/" {
						t.Fatal("encoded form/endpoint changed")
					}
					if request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || request.Header.Get("Accept") != "text/html, application/xhtml+xml" || request.Header.Get("User-Agent") != WebEvidenceUserAgent || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
						t.Fatal("form headers or credential isolation changed")
					}
					return webResponse(status, http.Header{"Location": {"https://other.example/"}}, "0123456789"), nil
				})
			}
			document, err := client.PostFormAuthorizedNoRedirect(t.Context(), "https://html.duckduckgo.com/html/", body, 8, func(target string) error {
				authorized++
				if target != "https://html.duckduckgo.com/html/" {
					t.Fatal("authorization target changed")
				}
				return nil
			})
			if requests != 1 || authorized != 1 {
				t.Fatalf("POST was retried or redirected: requests=%d authorized=%d", requests, authorized)
			}
			if status == http.StatusFound {
				if err == nil {
					t.Fatal("form followed redirect")
				}
				return
			}
			if err != nil || document.StatusCode != status || !document.Truncated || string(document.Body) != "01234567" || document.FinalURL != document.RequestedURL {
				t.Fatalf("bounded response changed: %+v err=%v", document, err)
			}
		})
	}
}

func TestSafeHTTPFormPOSTHonorsAuthorityDNSBodyAndCancellation(t *testing.T) {
	for _, boundary := range []string{"authority", "private DNS", "body bound", "cancel"} {
		t.Run(boundary, func(t *testing.T) {
			client := NewSafeHTTPClient()
			client.Timeout = 20 * time.Millisecond
			lookups, requests := 0, 0
			client.Resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
				lookups++
				if boundary == "private DNS" {
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			})
			client.TransportFactory = func(string, []netip.Addr) http.RoundTripper {
				return roundTripFunc(func(request *http.Request) (*http.Response, error) {
					requests++
					<-request.Context().Done()
					return nil, request.Context().Err()
				})
			}
			body := []byte("q=example")
			if boundary == "body bound" {
				body = []byte(strings.Repeat("x", DefaultMaxRequest+1))
			}
			_, err := client.PostFormAuthorizedNoRedirect(t.Context(), "https://html.duckduckgo.com/html/", body, 32, func(string) error {
				if boundary == "authority" {
					return errors.New("not authorized")
				}
				return nil
			})
			if err == nil {
				t.Fatal("form boundary was bypassed")
			}
			if boundary == "cancel" {
				if requests != 1 || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("cancel/retry=%d err=%v", requests, err)
				}
			} else if requests != 0 {
				t.Fatal("refused form reached HTTP transport")
			}
			if (boundary == "authority" || boundary == "body bound") && lookups != 0 {
				t.Fatal("preflight refusal performed DNS")
			}
		})
	}
}
