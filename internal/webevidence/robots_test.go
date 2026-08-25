package webevidence

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestRobotsCheckFailsClosedAndHonorsExplicitPolicy(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantAllowed bool
		wantState   string
		wantError   bool
	}{
		{name: "not present", status: http.StatusNotFound,
			wantAllowed: true, wantState: "not_present"},
		{name: "server denies policy", status: http.StatusForbidden,
			wantState: "blocked"},
		{name: "rule blocks page", status: http.StatusOK,
			body: "User-agent: *\nDisallow: /private\n", wantState: "blocked"},
		{name: "rule allows page", status: http.StatusOK,
			body: "User-agent: *\nDisallow: /other\n", wantAllowed: true, wantState: "allowed"},
		{name: "indeterminate status", status: http.StatusInternalServerError,
			wantState: "unknown", wantError: true},
		{name: "invalid MIME", status: http.StatusOK,
			body: "User-agent: *\nAllow: /\n", wantState: "unknown", wantError: true},
		{name: "oversized policy", status: http.StatusOK,
			body: strings.Repeat("a", 256*1024+1), wantState: "unknown", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &SafeHTTPClient{Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}), TransportFactory: func(string, []netip.Addr) http.RoundTripper {
				return roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.URL.Path != "/robots.txt" {
						t.Fatalf("unexpected robots request path: %s", request.URL.Path)
					}
					contentType := "text/plain"
					if test.name == "invalid MIME" {
						contentType = "text/html"
					}
					return webResponse(test.status,
						http.Header{"Content-Type": {contentType}}, test.body), nil
				})
			}}
			decision, err := CheckRobots(t.Context(), client,
				"https://www.example.com/private/page")
			if decision.Allowed != test.wantAllowed || decision.State != test.wantState ||
				(err != nil) != test.wantError {
				t.Fatalf("decision=%#v err=%v", decision, err)
			}
		})
	}
}

func TestRobotsUsesMostSpecificMatchingAgentGroups(t *testing.T) {
	tests := []struct {
		name    string
		content string
		allowed bool
	}{
		{name: "specific disallow overrides longer wildcard allow",
			content: "User-agent: *\nAllow: /private/report\n\n" +
				"User-agent: Traverse-Board\nDisallow: /\n"},
		{name: "specific allow overrides wildcard disallow",
			content: "User-agent: *\nDisallow: /private/report\n\n" +
				"User-agent: Traverse-Board-WebEvidence\nAllow: /\n", allowed: true},
		{name: "unrelated containing token does not match",
			content: "User-agent: evil-traverse-board\nAllow: /\n\n" +
				"User-agent: *\nDisallow: /private\n"},
		{name: "same specific groups merge",
			content: "User-agent: Traverse-Board\nDisallow: /private\n\n" +
				"User-agent: traverse-board\nAllow: /private/report\n", allowed: true},
		{name: "query pattern is evaluated",
			content: "User-agent: *\nDisallow: /*?download=*\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := "/private/report"
			if test.name == "query pattern is evaluated" {
				target = "/private/report?download=1"
			}
			if got := robotsAllows(test.content, target); got != test.allowed {
				t.Fatalf("allowed=%t want=%t", got, test.allowed)
			}
		})
	}
}

func TestRobotsWildcardAndTerminalAnchorMatching(t *testing.T) {
	for _, test := range []struct {
		pattern string
		target  string
		match   bool
	}{
		{pattern: "/private", target: "/private/report", match: true},
		{pattern: "/*.pdf$", target: "/guide.pdf", match: true},
		{pattern: "/*.pdf$", target: "/guide.pdf?download=1"},
		{pattern: "/*/report$", target: "/private/daily/report", match: true},
		{pattern: "/*/report$", target: "/private/report/more"},
		{pattern: "*ab*ab$", target: "ab"},
		{pattern: "*ab*ab$", target: "abxxab", match: true},
	} {
		matched, _ := robotsPatternMatches(test.pattern, test.target)
		if matched != test.match {
			t.Fatalf("pattern=%q target=%q match=%t want=%t", test.pattern,
				test.target, matched, test.match)
		}
	}
}
