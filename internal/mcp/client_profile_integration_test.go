package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"cyberagent-workbench/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const profileFixtureResult = `{"content":[{"type":"text","text":"received"}],"structuredContent":{"large":9007199254740993},"vendorExtension":{"keep":true},"resultType":"complete"}`

type profileFixtureTrace struct {
	mu      sync.Mutex
	methods []string
}

func (p *profileFixtureTrace) received() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.methods)
}

// The SDK client, TLS transport, final encoded frames and C's bounded-send guard
// are real. Only the remote peer and host authority are controlled fixtures.
func resolvedProfileFixture(t *testing.T, allowed []string, fallback string) (*Client, toolcontract.ResolvedLaunch, *resolvedGuardProbe, *profileFixtureTrace) {
	t.Helper()
	trace := &profileFixtureTrace{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request Envelope
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid frame reached peer")
			w.WriteHeader(400)
			return
		}
		trace.mu.Lock()
		trace.methods = append(trace.methods, request.Method)
		trace.mu.Unlock()
		if request.Method == "server/discover" || fallback == "" {
			var params struct {
				Meta map[string]any `json:"_meta"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.Meta[sdk.MetaKeyProtocolVersion] != preferredClientProtocolVersion || r.Header.Get("Mcp-Protocol-Version") != preferredClientProtocolVersion {
				t.Error("unapproved or absent modern version reached peer")
			}
		}
		switch request.Method {
		case "server/discover":
			if fallback != "" {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"legacy only"}}`, request.ID)
				return
			}
			sdkWriteResponse(w, request.ID, `{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{}},"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"fixture","version":"1"}},"resultType":"complete","ttlMs":0,"cacheScope":"private"}`)
		case "initialize":
			if fallback == "" {
				t.Error("unexpected initialize")
			}
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if json.Unmarshal(request.Params, &params) != nil || !slices.Contains(allowed, params.ProtocolVersion) {
				t.Error("unapproved fallback initialize reached peer")
			}
			sdkWriteResponse(w, request.ID, fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}`, fallback))
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			sdkWriteResponse(w, request.ID, `{"tools":[{"name":"lookup","inputSchema":{"type":"object"}}],"resultType":"complete","ttlMs":0,"cacheScope":"private"}`)
		case "tools/call":
			sdkWriteResponse(w, request.ID, profileFixtureResult)
		default:
			t.Errorf("unexpected method %q", request.Method)
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(server.Close)
	declaration, host := testResolvedDeclaration(t)
	declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
	declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: server.URL}
	host.ProtocolVersions = allowed
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	probe := &resolvedGuardProbe{}
	return resolvedFixtureClient(t, launch, probe, server.Client()), launch, probe, trace
}

func TestResolvedModernSDKUsesBoundedDiscoveryAndTypedReceipt(t *testing.T) {
	client, launch, probe, trace := resolvedProfileFixture(t, []string{preferredClientProtocolVersion}, "")
	scope := testResolvedScope(t, launch, 2)
	scope.Methods = []string{"server/discover", "tools/list"}
	snapshot, err := client.DiscoverWithScope(t.Context(), scope)
	if err != nil || snapshot.ProtocolVersion != preferredClientProtocolVersion {
		t.Fatalf("modern guarded discovery: %v", err)
	}
	operation := resolvedFixtureCall(t, client, launch, "{}")
	result, native, receipt, err := client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}"))
	if err != nil || result == nil || receipt.State != toolcontract.ReceiptResultReceived || !bytes.Equal(native, []byte(profileFixtureResult)) {
		t.Fatalf("modern typed/native receipt: %v %v", receipt, err)
	}
	if !slices.Equal(trace.received(), []string{"server/discover", "tools/list", "tools/call"}) || probe.connects.Load() != 1 || probe.discoveries.Load() != 1 || probe.sends.Load() != 2 || probe.calls.Load() != 1 {
		t.Fatalf("modern guards or wire sequence differ: %v", trace.received())
	}
}

func TestResolvedModernSDKCannotBroadenMethodsOrBudget(t *testing.T) {
	for _, scenario := range []string{"method", "budget"} {
		t.Run(scenario, func(t *testing.T) {
			client, launch, probe, trace := resolvedProfileFixture(t, []string{preferredClientProtocolVersion}, "")
			scope := testResolvedScope(t, launch, 1)
			scope.Methods = []string{"tools/list"}
			want := []string{}
			if scenario == "budget" {
				scope.Methods = []string{"server/discover", "tools/list"}
				want = []string{"server/discover"}
			}
			if _, err := client.DiscoverWithScope(t.Context(), scope); err == nil || client.NativeCapabilityFingerprint() != "" {
				t.Fatal("SDK exceeded bounded discovery")
			}
			if !slices.Equal(trace.received(), want) || probe.discoveries.Load() != 1 {
				t.Fatalf("scope silently broadened: %v", trace.received())
			}
		})
	}
}

func TestResolvedModernSDKFallbackRequiresEveryAllowedVersion(t *testing.T) {
	for _, scenario := range []string{"modern-only", "different-legacy", "authorized-fallback", "unauthorized-peer-selection"} {
		t.Run(scenario, func(t *testing.T) {
			allowed := []string{preferredClientProtocolVersion}
			fallback := "2025-11-25"
			want := []string{"server/discover"}
			if scenario == "different-legacy" {
				allowed = append(allowed, legacyClientProtocolVersion)
			}
			if scenario == "authorized-fallback" || scenario == "unauthorized-peer-selection" {
				allowed = append(allowed, "2025-11-25")
				want = append(want, "initialize")
			}
			if scenario == "authorized-fallback" {
				want = append(want, "notifications/initialized", "tools/list")
			}
			if scenario == "unauthorized-peer-selection" {
				fallback = legacyClientProtocolVersion
			}
			client, launch, probe, trace := resolvedProfileFixture(t, allowed, fallback)
			scope := testResolvedScope(t, launch, 4)
			scope.Methods = append(scope.Methods, "server/discover")
			snapshot, err := client.DiscoverWithScope(t.Context(), scope)
			if scenario == "authorized-fallback" {
				if err != nil || snapshot.ProtocolVersion != fallback {
					t.Fatalf("explicitly permitted fallback failed: %v", err)
				}
			} else if err == nil || client.NativeCapabilityFingerprint() != "" {
				t.Fatal("unapproved fallback accepted")
			}
			if !slices.Equal(trace.received(), want) || probe.discoveries.Load() != 1 {
				t.Fatalf("unexpected downgrade sends: %v; want %v", trace.received(), want)
			}
		})
	}
}

type mutateProtocolTransport struct {
	sdk.Transport
	method, scenario string
}

func (t *mutateProtocolTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	connection, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &mutateProtocolConnection{Connection: connection, method: t.method, scenario: t.scenario}, nil
}

type mutateProtocolConnection struct {
	sdk.Connection
	method, scenario string
}

// Mutate the real SDK message before the HTTP transport encodes it. This proves
// B inspects the actual frame, not the version it originally asked the SDK for.
func (c *mutateProtocolConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if request, ok := message.(*jsonrpc.Request); ok && request.Method == c.method {
		copy := *request
		var params map[string]any
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return err
		}
		meta := params["_meta"].(map[string]any)
		switch c.scenario {
		case "missing":
			delete(meta, sdk.MetaKeyProtocolVersion)
		case "wrong-type":
			meta[sdk.MetaKeyProtocolVersion] = 20260728
		default:
			meta[sdk.MetaKeyProtocolVersion] = legacyClientProtocolVersion
		}
		copy.Params, _ = json.Marshal(params)
		message = &copy
	}
	return c.Connection.Write(ctx, message)
}

func TestResolvedModernSDKRejectsActualFrameVersionDrift(t *testing.T) {
	for _, method := range []string{"server/discover", "tools/list", "tools/call"} {
		for _, scenario := range []string{"missing", "wrong-type", "unauthorized"} {
			t.Run(method+"/"+scenario, func(t *testing.T) {
				client, launch, probe, trace := resolvedProfileFixture(t, []string{preferredClientProtocolVersion}, "")
				transport := client.transport.(*sdkClientTransport)
				transport.transport = &mutateProtocolTransport{Transport: transport.transport, method: method, scenario: scenario}
				scope := testResolvedScope(t, launch, 2)
				scope.Methods = []string{"server/discover", "tools/list"}
				_, err := client.DiscoverWithScope(t.Context(), scope)
				want := []string{}
				switch method {
				case "tools/list":
					want = []string{"server/discover"}
				case "tools/call":
					if err != nil {
						t.Fatal(err)
					}
					want = []string{"server/discover", "tools/list"}
					operation := resolvedFixtureCall(t, client, launch, "{}")
					var receipt toolcontract.Receipt
					_, _, receipt, err = client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}"))
					if receipt.State != toolcontract.ReceiptNotDispatched || probe.calls.Load() != 0 {
						t.Fatalf("bad frame consumed authority or dispatched: %v", receipt)
					}
				}
				if err == nil || !slices.Equal(trace.received(), want) {
					t.Fatalf("mutated protocol metadata reached peer: %v %v", trace.received(), err)
				}
			})
		}
	}
}
