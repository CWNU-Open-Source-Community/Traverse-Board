package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func sdkHTTPFixture(t *testing.T, tool func(http.ResponseWriter, *http.Request, Envelope), catalogs ...func() string) (*sdkClientTransport, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request Envelope
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if request.Method == "notifications/initialized" {
			t.Error("modern peer received initialized")
			w.WriteHeader(202)
			return
		}
		var result string
		switch request.Method {
		case "initialize":
			t.Error("modern peer received initialize")
			result = "{}"
		case "server/discover":
			result = "{\"supportedVersions\":[\"2026-07-28\"],\"capabilities\":{\"tools\":{}},\"_meta\":{\"io.modelcontextprotocol/serverInfo\":{\"name\":\"fixture\",\"version\":\"1.0.0\"}},\"resultType\":\"complete\",\"ttlMs\":0,\"cacheScope\":\"private\"}"
		case "tools/list":
			result = "{\"tools\":[{\"name\":\"lookup\",\"inputSchema\":{\"type\":\"object\"}}],\"resultType\":\"complete\",\"ttlMs\":0,\"cacheScope\":\"private\"}"
			if len(catalogs) > 0 {
				result = catalogs[0]()
			}
		case "tools/call":
			calls.Add(1)
			if r.Header.Get("Mcp-Protocol-Version") != preferredClientProtocolVersion || r.Header.Get("Mcp-Method") != "tools/call" || r.Header.Get("Mcp-Name") != "lookup" {
				t.Errorf("modern request headers were not projected: %v", r.Header)
			}
			var params map[string]json.RawMessage
			if json.Unmarshal(request.Params, &params) != nil || !bytes.Contains(params["_meta"], []byte(preferredClientProtocolVersion)) {
				t.Errorf("missing per-request profile metadata: %s", request.Params)
			}
			tool(w, r, request)
			return
		default:
			t.Errorf("unexpected interaction: %s", request.Method)
			w.WriteHeader(400)
			return
		}
		sdkWriteResponse(w, request.ID, result)
	}))
	t.Cleanup(server.Close)
	descriptor := clientTransportDescriptor(TransportStreamableHTTP, server.URL, nil)
	transport, err := newRemoteClientTransport(descriptor, "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	adapter := transport.(*sdkClientTransport)
	adapter.protocolVersion = preferredClientProtocolVersion
	t.Cleanup(func() { _ = adapter.Close() })
	return adapter, calls
}

func sdkWriteResponse(w http.ResponseWriter, id json.RawMessage, result string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}", id, result)
}
func sdkTestRequest(params string) Envelope {
	return Envelope{JSONRPC: "2.0", ID: json.RawMessage("77"), Method: "tools/call", Params: json.RawMessage(params)}
}
func TestSDKModernHTTPPreservesNativeJSON(t *testing.T) {
	raw := "{\"content\":[{\"type\":\"text\",\"text\":\"ok\",\"_meta\":{\"vendor\":\"native\"}},{\"type\":\"image\",\"data\":\"aGVsbG8=\",\"mimeType\":\"image/png\"},{\"type\":\"audio\",\"data\":\"aGVsbG8=\",\"mimeType\":\"audio/wav\"},{\"type\":\"resource_link\",\"uri\":\"fixture://one\",\"name\":\"one\"},{\"type\":\"resource\",\"resource\":{\"uri\":\"fixture://two\",\"text\":\"two\"}}],\"structuredContent\":{\"large\":9007199254740993},\"_meta\":{\"vendor\":{\"large\":9007199254740993}},\"vendorExtension\":{\"keep\":true},\"resultType\":\"complete\"}"
	transport, calls := sdkHTTPFixture(t, func(w http.ResponseWriter, r *http.Request, request Envelope) {
		if !bytes.Contains(request.Params, []byte("9007199254740993")) {
			t.Errorf("argument precision was lost: %s", request.Params)
		}
		sdkWriteResponse(w, request.ID, raw)
	})
	result, err := transport.Exchange(t.Context(), sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{\"large\":9007199254740993}}"))
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Result) != raw || calls.Load() != 1 {
		t.Fatalf("native result changed or repeated: %s calls=%d", result.Result, calls.Load())
	}
}
func TestSDKDoesNotAutomaticallyRetryInputRequired(t *testing.T) {
	transport, calls := sdkHTTPFixture(t, func(w http.ResponseWriter, r *http.Request, request Envelope) {
		sdkWriteResponse(w, request.ID, "{\"resultType\":\"input_required\",\"inputRequests\":{},\"requestState\":\"opaque-native-state\"}")
	})
	_, err := transport.Exchange(t.Context(), sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{}}"))
	var required *sdkInputRequiredError
	if !errors.As(err, &required) || !bytes.Contains(required.raw, []byte("opaque-native-state")) || calls.Load() != 1 {
		t.Fatalf("input-required must be retained without automatic interaction/replay: %v calls=%d", err, calls.Load())
	}
}
func TestSDKLostHTTPResponseIsUnknownAndNeverRepeated(t *testing.T) {
	transport, calls := sdkHTTPFixture(t, func(w http.ResponseWriter, r *http.Request, request Envelope) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close() // The fixture has already counted its side effect.
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, err := transport.Exchange(ctx, sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{}}"))
	var unknown *sdkOutcomeUnknownError
	if !errors.As(err, &unknown) || calls.Load() != 1 {
		t.Fatalf("lost response=%v calls=%d; want unknown, once", err, calls.Load())
	}
}
func TestSDKRemoteRPCErrorIsAReceivedResponse(t *testing.T) {
	transport, calls := sdkHTTPFixture(t, func(w http.ResponseWriter, r *http.Request, request Envelope) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"error\":{\"code\":-32602,\"message\":\"fixture rejection\"}}", request.ID)
	})
	response, err := transport.Exchange(t.Context(), sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{}}"))
	if err != nil || response.Error == nil || response.Error.Code != -32602 || calls.Load() != 1 {
		t.Fatalf("wire error must be a received response: %#v %v calls=%d", response, err, calls.Load())
	}
}
func TestSDKOversizedHTTPResultIsUnknownWithoutRetry(t *testing.T) {
	transport, calls := sdkHTTPFixture(t, func(w http.ResponseWriter, r *http.Request, request Envelope) {
		sdkWriteResponse(w, request.ID, "{\"content\":[{\"type\":\"text\",\"text\":\""+strings.Repeat("x", MaxMessageBytes)+"\"}]}")
	})
	_, err := transport.Exchange(t.Context(), sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{}}"))
	var unknown *sdkOutcomeUnknownError
	if !errors.As(err, &unknown) || calls.Load() != 1 {
		t.Fatalf("oversized result=%v calls=%d; must not claim no side effect", err, calls.Load())
	}
}
func TestSDKCatalogRefreshPreservesNativeJSONAndIgnoresSDKCache(t *testing.T) {
	var lists atomic.Int32
	raw := "{\"tools\":[{\"name\":\"lookup\",\"inputSchema\":{\"type\":\"object\",\"x-large\":9007199254740993},\"vendorExtension\":true}],\"resultType\":\"complete\",\"ttlMs\":600000,\"cacheScope\":\"private\",\"vendor\":9007199254740993}"
	transport, _ := sdkHTTPFixture(t, func(http.ResponseWriter, *http.Request, Envelope) { t.Error("unexpected tool call") }, func() string {
		lists.Add(1)
		return raw
	})
	for i := 0; i < 2; i++ {
		response, err := transport.Exchange(t.Context(), Envelope{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list", Params: json.RawMessage("{}")})
		if err != nil || string(response.Result) != raw {
			t.Fatalf("catalog %d lost native JSON: %s %v", i, response.Result, err)
		}
	}
	if lists.Load() != 2 {
		t.Fatalf("explicit discovery must refresh under host authority; wire requests=%d", lists.Load())
	}
}
func TestSDKModernHTTPCancellationClosesOnlyTheRequestStream(t *testing.T) {
	started, ended := make(chan struct{}), make(chan struct{})
	transport, calls := sdkHTTPFixture(t, func(w http.ResponseWriter, r *http.Request, request Envelope) {
		if bytes.Contains(request.Params, []byte("\"wait\"")) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			close(ended)
			return
		}
		sdkWriteResponse(w, request.ID, "{\"content\":[{\"type\":\"text\",\"text\":\"alive\"}],\"resultType\":\"complete\"}")
	})
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	blocked, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := transport.Exchange(blocked, sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{\"mode\":\"wait\"}}"))
		result <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case err := <-result:
		var unknown *sdkOutcomeUnknownError
		if !errors.As(err, &unknown) || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation does not imply rollback: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-ended:
	case <-ctx.Done():
		t.Fatal("HTTP response stream was not cancelled")
	}
	response, err := transport.Exchange(ctx, sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{}}"))
	if err != nil || !bytes.Contains(response.Result, []byte("alive")) || calls.Load() != 2 {
		t.Fatalf("subsequent request failed: %s %v calls=%d", response.Result, err, calls.Load())
	}
}
func TestSDKGuardsRunBeforeStartupAndFinalDispatch(t *testing.T) {
	denied := errors.New("host authorization revoked")
	transport, calls := sdkHTTPFixture(t, func(w http.ResponseWriter, r *http.Request, request Envelope) { t.Error("guarded call reached server") })
	transport.beforeConnect = func(context.Context) error { return denied }
	_, err := transport.Exchange(t.Context(), sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{}}"))
	if !errors.Is(err, denied) || calls.Load() != 0 {
		t.Fatalf("startup guard: %v calls=%d", err, calls.Load())
	}
	transport2, calls2 := sdkHTTPFixture(t, func(w http.ResponseWriter, r *http.Request, request Envelope) { t.Error("guarded call reached server") })
	transport2.beforeCall = func(context.Context) error { return denied }
	_, err = transport2.Exchange(t.Context(), sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{}}"))
	var unknown *sdkOutcomeUnknownError
	if !errors.Is(err, denied) || errors.As(err, &unknown) || calls2.Load() != 0 {
		t.Fatalf("dispatch guard: %v calls=%d", err, calls2.Load())
	}
}
func TestSDKCancellationKeepsOtherStdioCallsAlive(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	descriptor := clientTransportDescriptor(TransportStdio, executable, []string{"-test.run=^TestSDKConcurrentStdioHelper$", "--", "sdk-concurrent-helper"})
	tr, err := newStdioClientTransport(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	transport := tr.(*sdkClientTransport)
	defer transport.Close()
	ready := make(chan struct{})
	var once sync.Once
	transport.progress = func(_ context.Context, _ *sdk.ProgressNotificationParams) { once.Do(func() { close(ready) }) }
	client := newClient(transport, descriptor)
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	if _, err := client.Discover(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithCancel(ctx)
	cancelled := make(chan error, 1)
	go func() {
		_, err := transport.Exchange(blocked, sdkTestRequest("{\"name\":\"lookup\",\"arguments\":{\"mode\":\"wait\"},\"_meta\":{\"progressToken\":\"blocked\"}}"))
		cancelled <- err
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A separate request completes while the first remains pending.
	result, err := client.CallTool(ctx, "lookup", json.RawMessage("{\"mode\":\"fast\"}"), 4096)
	if err != nil || !strings.Contains(result.Content, "fast") {
		t.Fatalf("concurrent call: %#v %v", result, err)
	}
	cancel()
	select {
	case err := <-cancelled:
		var unknown *sdkOutcomeUnknownError
		if !errors.Is(err, context.Canceled) || !errors.As(err, &unknown) {
			t.Fatalf("cancel result must not claim rollback: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The same process must still be responsive after request cancellation.
	result, err = client.CallTool(ctx, "lookup", json.RawMessage("{\"mode\":\"stderr\"}"), 4096)
	if err != nil || !strings.Contains(result.Content, "stderr") {
		t.Fatalf("same-process call after cancellation: %#v %v", result, err)
	}
}
func TestSDKConcurrentStdioHelper(t *testing.T) {
	if !slices.Contains(os.Args, "sdk-concurrent-helper") {
		t.Skip("helper subprocess only")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), MaxMessageBytes)
	for scanner.Scan() {
		request, err := DecodeEnvelope(scanner.Bytes())
		if err != nil {
			return
		}
		if len(request.ID) == 0 {
			continue
		}
		if request.Method == "tools/call" {
			if bytes.Contains(request.Params, []byte("\"wait\"")) {
				fmt.Fprintln(os.Stdout, "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progressToken\":\"blocked\",\"progress\":1,\"total\":2}}")
				continue
			}
			text := "fast"
			if bytes.Contains(request.Params, []byte("\"stderr\"")) {
				fmt.Fprint(os.Stderr, strings.Repeat("diagnostic ", 20000))
				text = "stderr"
			}
			fmt.Fprintln(os.Stdout, string(clientFixtureResponseWithText(request, text)))
		} else {
			fmt.Fprintln(os.Stdout, string(clientFixtureResponse(request)))
		}
	}
}
