package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/toolcontract"
)

func TestResolvedHTTPGeneratedHeadersPrecedeMixedCaseConfiguration(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(204)
			return
		}
		var request Envelope
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("configured framing corrupted request")
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("Authorization") != "literal-configured-auth" || r.Header.Get("X-Literal") != "${PLUGIN_ROOT}" ||
			r.Header.Get("Content-Type") != "application/json" || !strings.Contains(r.Header.Get("Accept"), "application/json") || r.ContentLength <= 0 {
			t.Error("generated framing or frozen literal header was lost")
		}
		if request.Method == "initialize" {
			if r.Header.Get("Mcp-Session-Id") != "" {
				t.Error("configuration injected session before initialization")
			}
		} else if r.Header.Get("Mcp-Session-Id") != "peer-session" || r.Header.Get("Mcp-Protocol-Version") != legacyClientProtocolVersion {
			t.Error("configuration replaced negotiated protocol or session")
		}
		if request.Method == "tools/call" {
			calls.Add(1)
		}
		if len(request.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Mcp-Session-Id", "peer-session")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(clientFixtureResponse(request))
	}))
	defer server.Close()
	declaration, host := testResolvedDeclaration(t)
	declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
	declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: server.URL, Headers: map[string]string{
		"aUtHoRiZaTiOn": "literal-configured-auth", "x-lItErAl": "${PLUGIN_ROOT}", "aCcEpT": "bad-accept", "CONTENT-TYPE": "text/plain",
		"CONTENT-length": "1", "hOsT": "other.invalid", "mCP-protocol-Version": "unapproved", "MCP-session-ID": "injected-session",
		"Mcp-Method": "unapproved", "mcp-NAME": "different", "Connection": "upgrade", "Upgrade": "websocket"}}
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]string, len(declaration.HTTP.Headers))
	for name, value := range declaration.HTTP.Headers {
		want[http.CanonicalHeaderKey(name)] = value
	}
	if !maps.Equal(launch.HTTP.Headers, want) || declaration.HTTP.Headers["hOsT"] != "other.invalid" {
		t.Fatal("literal configured headers were dropped or declaration changed")
	}
	fingerprint, _ := toolcontract.FingerprintLaunch(resolvedTestKey, launch)
	declaration.HTTP.Headers = make(map[string]string, len(want))
	for name, value := range want {
		declaration.HTTP.Headers[strings.ToUpper(name)] = value
	}
	equivalent, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	if actual, _ := toolcontract.FingerprintLaunch(resolvedTestKey, equivalent); actual != fingerprint {
		t.Fatal("header casing changed the frozen configuration fingerprint")
	}
	declaration.HTTP.Headers["MCP-SESSION-ID"] = "different-ignored-configuration"
	changed, _ := ResolveLaunch(declaration, host)
	if actual, _ := toolcontract.FingerprintLaunch(resolvedTestKey, changed); actual == fingerprint {
		t.Fatal("configured value overridden on wire was absent from the fingerprint")
	}
	declaration.HTTP.Headers["MCP-SESSION-ID"] = want["Mcp-Session-Id"]
	declaration.HTTP.Headers["X-LITERAL"] = "changed"
	changed, _ = ResolveLaunch(declaration, host)
	if actual, _ := toolcontract.FingerprintLaunch(resolvedTestKey, changed); actual == fingerprint {
		t.Fatal("effective configured value was absent from the fingerprint")
	}
	probe := &resolvedGuardProbe{}
	client := resolvedFixtureClient(t, launch, probe, server.Client())
	defer client.Close()
	launch.HTTP.Headers["X-Literal"] = "caller-mutation"
	if _, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, client.resolved.launch, 3)); err != nil {
		t.Fatal(err)
	}
	operation := resolvedFixtureCall(t, client, client.resolved.launch, "{}")
	if _, _, receipt, err := client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}")); err != nil || receipt.State != toolcontract.ReceiptResultReceived || calls.Load() != 1 {
		t.Fatalf("frozen header guarded call failed: %v %v", receipt, err)
	}
}

type headerProbeRoundTripper func(*http.Request) (*http.Response, error)

func (f headerProbeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPProjectionGeneratedAuthorizationAndHeadersWinCaseInsensitively(t *testing.T) {
	var sends atomic.Int32
	bound, _ := url.Parse("https://fixture.invalid/mcp")
	transport := &mcpHTTPTransport{origin: bound, bearer: "host-bound-token", headers: http.Header{
		"authorization": {"configured-token"}, "mCP-session-ID": {"configured-session"}, "mCP-protocol-Version": {"configured-version"},
		"x-extra": {"literal"}, "accept": {"configured-accept"}},
		base: headerProbeRoundTripper(func(r *http.Request) (*http.Response, error) {
			sends.Add(1)
			for name, want := range map[string]string{"Authorization": "Bearer host-bound-token", "Mcp-Session-Id": "sdk-session", "Mcp-Protocol-Version": preferredClientProtocolVersion, "X-Extra": "literal", "Accept": "application/json"} {
				if r.Header.Get(name) != want || len(r.Header.Values(name)) != 1 {
					t.Errorf("generated header precedence failed for %s", name)
				}
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
		})}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, bound.String(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	request.Header = http.Header{"AUTHORIZATION": {"sdk-auth"}, "mcp-session-id": {"sdk-session"}, "mcp-protocol-version": {preferredClientProtocolVersion}, "accept": {"application/json"}}
	original := request.Header.Clone()
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if sends.Load() != 1 || fmt.Sprint(request.Header) != fmt.Sprint(original) {
		t.Fatal("projection mutated caller headers or repeated send")
	}
}

func TestResolvedHTTPRejectsDuplicateHeaderAliases(t *testing.T) {
	declaration, host := testResolvedDeclaration(t)
	declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
	declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: "https://fixture.invalid/mcp", Headers: map[string]string{"Authorization": "one", "authorization": "two"}}
	if _, err := ResolveLaunch(declaration, host); err == nil {
		t.Fatal("duplicate case aliases silently selected a configured value")
	}
}

func TestHTTPOriginBindingRunsBeforeCredentialsOrSend(t *testing.T) {
	for _, target := range []string{"https://elsewhere.invalid/mcp", "http://fixture.invalid/mcp", "https://fixture.invalid:444/mcp", "https://user@fixture.invalid/mcp"} {
		t.Run(target, func(t *testing.T) {
			var credentials, sends atomic.Int32
			bound, _ := url.Parse("https://fixture.invalid/mcp")
			transport := &mcpHTTPTransport{origin: bound,
				beforeConnect: func(context.Context) error { credentials.Add(1); return nil },
				base: headerProbeRoundTripper(func(r *http.Request) (*http.Response, error) {
					sends.Add(1)
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
				})}
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			if _, err := transport.RoundTrip(request); err == nil || credentials.Load() != 0 || sends.Load() != 0 {
				t.Fatal("changed origin reached credentials or network")
			}
		})
	}
	bound, _ := url.Parse("https://fixture.invalid/mcp")
	same, _ := url.Parse("https://FIXTURE.invalid:443/other-path")
	if !sameMCPOrigin(bound, same) {
		t.Fatal("origin comparison confused path, case or default port with origin")
	}
}

func TestResolvedHTTPNeverForwardsConfiguredHeadersToRedirectOrSSEEndpoint(t *testing.T) {
	for _, kind := range []string{"301", "302", "307", "308", "legacy-sse-endpoint"} {
		t.Run(kind, func(t *testing.T) {
			var targetCalls, sourceCalls atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetCalls.Add(1)
				w.WriteHeader(204)
			}))
			defer target.Close()
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceCalls.Add(1)
				if r.Header.Get("X-Configured") != "bound-to-source" || r.Header.Get("Authorization") != "Bearer bound-to-source" {
					t.Error("source did not receive bound headers")
				}
				if kind == "legacy-sse-endpoint" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", target.URL)
					return
				}
				var code int
				fmt.Sscan(kind, &code)
				w.Header().Set("Location", target.URL)
				w.WriteHeader(code)
			}))
			defer source.Close()
			declaration, host := testResolvedDeclaration(t)
			declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
			declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: source.URL, Headers: map[string]string{"X-Configured": "bound-to-source"}, Credential: &toolcontract.CredentialRef{ID: "fixture", Revision: "1"}}
			launch, err := ResolveLaunch(declaration, host)
			if err != nil {
				t.Fatal(err)
			}
			fp, _ := toolcontract.FingerprintLaunch(resolvedTestKey, launch)
			connect := testResolvedOperation(toolcontract.OperationConnect, launch.Component, fp)
			probe := &resolvedGuardProbe{}
			client, err := NewResolvedClient(launch, resolvedTestKey, connect, probe.guards(t, connect), func(context.Context, toolcontract.CredentialRef) (string, error) { return "bound-to-source", nil }, source.Client())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			if _, err := client.DiscoverWithScope(ctx, testResolvedScope(t, launch, 3)); err == nil {
				t.Fatal("redirect/legacy endpoint discovery unexpectedly completed")
			}
			if sourceCalls.Load() != 1 || targetCalls.Load() != 0 {
				t.Fatalf("headers escaped/retried: source=%d target=%d", sourceCalls.Load(), targetCalls.Load())
			}
		})
	}
}

func TestHTTPGeneratedCredentialProjectionPreservesConfigurationIdentity(t *testing.T) {
	var requests, credentialReads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer host-bound-token" || r.Header.Get("X-Extra") != "literal" {
			t.Error("configured Authorization overrode the bound host credential")
		}
		var request Envelope
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(clientFixtureResponse(request))
	}))
	defer server.Close()
	declaration, host := testResolvedDeclaration(t)
	declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
	declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: server.URL, Headers: map[string]string{"aUtHoRiZaTiOn": "package-auth", "X-Extra": "literal"}, Credential: &toolcontract.CredentialRef{ID: "fixture", Revision: "1"}}
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	if launch.HTTP.Headers["Authorization"] != "package-auth" {
		t.Fatal("raw configured Authorization was removed before fingerprinting")
	}
	one, _ := toolcontract.FingerprintLaunch(resolvedTestKey, launch)
	launch.HTTP.Headers["Authorization"] = "different-package-auth"
	two, _ := toolcontract.FingerprintLaunch(resolvedTestKey, launch)
	if one == two {
		t.Fatal("configured Authorization missing from configuration identity")
	}
	launch.HTTP.Headers["Authorization"] = "package-auth"
	launch.HTTP.Credential.Revision = "2"
	two, _ = toolcontract.FingerprintLaunch(resolvedTestKey, launch)
	if one == two {
		t.Fatal("credential revision missing from configuration identity")
	}
	launch.HTTP.Credential.Revision = "1"
	connect := testResolvedOperation(toolcontract.OperationConnect, launch.Component, one)
	probe := &resolvedGuardProbe{}
	client, err := NewResolvedClient(launch, resolvedTestKey, connect, probe.guards(t, connect), func(_ context.Context, ref toolcontract.CredentialRef) (string, error) {
		credentialReads.Add(1)
		if ref.Revision != "1" {
			t.Error("credential resolver received a different revision")
		}
		return "host-bound-token", nil
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	launch.HTTP.Headers["Authorization"] = "caller-mutation"
	if _, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, client.resolved.launch, 3)); err != nil {
		t.Fatal(err)
	}
	operation := resolvedFixtureCall(t, client, client.resolved.launch, "{}")
	if _, _, receipt, err := client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}")); err != nil || receipt.State != toolcontract.ReceiptResultReceived || credentialReads.Load() != 1 || requests.Load() != 4 {
		t.Fatalf("bound credential call or request count differs: %v %v", receipt, err)
	}
}
