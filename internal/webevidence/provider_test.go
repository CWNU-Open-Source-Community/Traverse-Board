package webevidence

import (
	"context"
	"net/http"
	"net/netip"
	"testing"
)

func TestSearXNGProviderUsesOnlyBoundedDocumentedJSONContract(t *testing.T) {
	requests := 0
	client := &SafeHTTPClient{Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}), TransportFactory: func(host string, _ []netip.Addr) http.RoundTripper {
		if host != "search.example.com" {
			t.Fatalf("provider host=%q", host)
		}
		return roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			query := request.URL.Query()
			if request.URL.Path != "/search" || query.Get("q") != "public evidence" ||
				query.Get("format") != "json" || query.Get("safesearch") != "1" ||
				request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
				t.Fatalf("provider request URL=%s headers=%v", request.URL, request.Header)
			}
			body := `{"results":[` +
				`{"url":"https://docs.example.com/b/../report","title":" Report\nTitle ","content":" bounded\tsnippet ","publishedDate":"2026-08-25"},` +
				`{"url":"http://docs.example.com/insecure","title":"insecure"},` +
				`{"url":"https://127.0.0.1/private","title":"private"},` +
				`{"url":"https://docs.example.com/report","title":"duplicate"},` +
				`{"url":"https://other.example.net/item","title":"Other"}` +
				`]}`
			return webResponse(http.StatusOK,
				http.Header{"Content-Type": {"application/json"}}, body), nil
		})
	}}
	provider, err := NewSearXNGProvider(client, "https://search.example.com/")
	if err != nil || provider.Endpoint() != "https://search.example.com/search" {
		t.Fatalf("endpoint=%q err=%v", provider.Endpoint(), err)
	}
	results, err := provider.Search(t.Context(), "public evidence", 3,
		NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"search.example.com"}})
	if err != nil || requests != 1 || len(results) != 2 ||
		results[0].URL != "https://docs.example.com/report" || results[0].Title != "Report Title" ||
		results[0].Snippet != "bounded snippet" || results[0].Rank != 1 ||
		results[1].URL != "https://other.example.net/item" || results[1].Rank != 2 {
		t.Fatalf("results=%#v requests=%d err=%v", results, requests, err)
	}
	if _, err := NewSearXNGProvider(client,
		"https://user:secret@search.example.com/search"); err == nil {
		t.Fatal("credential-bearing search endpoint was accepted")
	}
}

func TestSearXNGProviderRejectsNonJSONMIME(t *testing.T) {
	client := &SafeHTTPClient{Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}), TransportFactory: func(string, []netip.Addr) http.RoundTripper {
		return roundTripFunc(func(*http.Request) (*http.Response, error) {
			return webResponse(http.StatusOK,
				http.Header{"Content-Type": {"text/html"}}, `{"results":[]}`), nil
		})
	}}
	provider, err := NewSearXNGProvider(client, "https://search.example.com/search")
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Search(t.Context(), "public evidence", 1,
		NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{"search.example.com"}})
	if err == nil {
		t.Fatal("non-JSON provider MIME was accepted")
	}
}
