package webevidence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestConnectorHTTPFailureClassificationAndBoundedDiagnostics(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		headers     http.Header
		body        string
		code        string
		wantRetry   string
		wantReset   string
		wantRequest string
	}{
		{name: "ordinary forbidden", status: http.StatusForbidden,
			headers: http.Header{"X-Github-Request-Id": {"request-forbidden"}},
			body:    "repository policy denied", code: "access_blocked",
			wantRequest: "request-forbidden"},
		{name: "primary rate limit", status: http.StatusForbidden,
			headers: http.Header{"X-Ratelimit-Remaining": {"0"},
				"X-Ratelimit-Reset":   {"1789923975"},
				"X-Github-Request-Id": {"request-primary"}},
			body: "API rate limit exceeded", code: "rate_limited",
			wantReset: "1789923975", wantRequest: "request-primary"},
		{name: "secondary rate limit", status: http.StatusForbidden,
			headers: http.Header{"Retry-After": {"60"}},
			body:    "secondary rate limit", code: "rate_limited", wantRetry: "60"},
		{name: "too many requests", status: http.StatusTooManyRequests,
			headers: http.Header{"Retry-After": {"120"}}, body: "slow down",
			code: "rate_limited", wantRetry: "120"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			client := &SafeHTTPClient{MaxRetries: 1,
				Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
				}),
				TransportFactory: func(string, []netip.Addr) http.RoundTripper {
					return roundTripFunc(func(request *http.Request) (*http.Response, error) {
						requests++
						return &http.Response{StatusCode: test.status, Header: test.headers.Clone(),
							Body: io.NopCloser(strings.NewReader(test.body))}, nil
					})
				}}
			_, err := connectorGet(t.Context(), client, "https://api.example.com/resource",
				"application/json", NetworkAuthority{Mode: "allowlist",
					AllowedTargets: []string{PublicHTTPSTarget}})
			var typed *connectorError
			if !errors.As(err, &typed) || connectorFailureCode(err) != test.code ||
				typed.httpStatus != test.status || typed.endpoint != "https://api.example.com/resource" ||
				typed.retryAfter != test.wantRetry || typed.rateLimitReset != test.wantReset ||
				typed.remoteRequestID != test.wantRequest {
				t.Fatalf("error=%#v code=%s", typed, connectorFailureCode(err))
			}
			if test.status == http.StatusTooManyRequests && requests != 1 {
				t.Fatalf("HTTP 429 was retried immediately: requests=%d", requests)
			}
		})
	}
}

func TestConnectorPartialReadsKeepEvidenceAndFailureThroughReplay(t *testing.T) {
	for _, kind := range []string{"github", "hacker_news", "hn_depth"} {
		t.Run(kind, func(t *testing.T) {
			requests := 0
			client := &SafeHTTPClient{Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}), TransportFactory: func(string, []netip.Addr) http.RoundTripper {
				return roundTripFunc(func(request *http.Request) (*http.Response, error) {
					requests++
					status, body := 200, ""
					headers := http.Header{"Content-Type": {"application/json"}}
					switch kind {
					case "github":
						if strings.HasSuffix(request.URL.Path, "/comments") {
							status, body = 429, "rate limit"
							headers.Set("Retry-After", "60")
							headers.Set("X-GitHub-Request-Id", "partial-request")
						} else {
							body = `{"id":1,"html_url":"https://github.com/example/project/pull/1","title":"Issue","body":"Retained root evidence","comments":3,"pull_request":{}}`
						}
					case "hacker_news":
						switch request.URL.Path {
						case "/v0/item/1.json":
							body = `{"id":1,"type":"story","text":"Retained root evidence","kids":[2,3],"descendants":2}`
						case "/v0/item/2.json":
							body = `{"id":2,"type":"comment","text":"Retained first comment"}`
						default:
							status, body = 503, "unavailable"
						}
					default:
						var id int
						fmt.Sscanf(request.URL.Path, "/v0/item/%d.json", &id)
						if id > 5 {
							t.Fatalf("read beyond depth budget: %d", id)
						}
						body = fmt.Sprintf(`{"id":%d,"type":"comment","text":"Retained root evidence","kids":[%d]}`, id, id+1)
					}
					return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
				})
			}}
			var connector SourceConnector = NewHackerNewsSourceConnector(client)
			canonical := "https://news.ycombinator.com/item?id=1"
			if kind == "github" {
				connector, canonical = NewGitHubSourceConnector(client), "https://github.com/example/project/pull/1"
			}
			service := NewService(newMemoryWebStore(), nil, &fakeFetchBackend{}).WithSourceConnectors(connector)
			scope := ExecutionScope{RunID: "partial-run", MissionID: "partial-mission", WorkspaceID: "partial-workspace",
				Authority: NetworkAuthority{Mode: "allowlist", AllowedTargets: []string{PublicHTTPSTarget}}}
			result, err := service.Fetch(t.Context(), scope, FetchRequest{URL: canonical, MaxItems: 10}, "partial-read")
			if err != nil {
				t.Fatal(err)
			}
			got := result.Snapshot
			if got.State != SourcePartial || got.Validate() != nil || !strings.Contains(got.Body, "Retained root evidence") {
				t.Fatalf("lost partial root: %+v", got)
			}
			if kind == "hn_depth" {
				if got.ItemsIncluded != 5 || got.ItemsAvailable != 0 || got.ContinuationFailure != nil || got.TruncationReason != "comment_or_depth_limit" {
					t.Fatalf("incorrect depth result: %+v", got)
				}
			} else {
				failure := got.ContinuationFailure
				if failure == nil || failure.Validate() != nil || failure.Endpoint == "" || got.TruncationReason != "comment_read_failed" {
					t.Fatalf("lost failure diagnostics: %+v", got)
				}
				if kind == "github" && (failure.Code != "rate_limited" || failure.HTTPStatus != 429 || failure.RetryAfter != "60" || failure.RemoteRequestID != "partial-request" || !strings.Contains(got.Coverage, "without_review_comments")) {
					t.Fatalf("incorrect GitHub partial: %+v", got)
				}
				if kind == "hacker_news" && (failure.HTTPStatus != 503 || got.ItemsIncluded != 2 || !strings.Contains(got.Body, "Retained first comment")) {
					t.Fatalf("lost preceding comment: %+v", got)
				}
			}
			before := requests
			replay, err := service.Fetch(t.Context(), scope, FetchRequest{URL: canonical, MaxItems: 10}, "partial-read")
			if err != nil || !replay.Replayed || requests != before || replay.Snapshot.Fingerprint != got.Fingerprint {
				t.Fatalf("bad partial replay: %+v err=%v requests=%d/%d", replay, err, requests, before)
			}
			citation, err := service.Cite(t.Context(), scope, CiteRequest{SourceID: result.Source.ID, SnapshotID: got.ID, Claim: "Root evidence is retained."}, "partial-cite")
			if err != nil || !citation.Citation.Partial {
				t.Fatalf("partial citation=%+v err=%v", citation, err)
			}
		})
	}
}
