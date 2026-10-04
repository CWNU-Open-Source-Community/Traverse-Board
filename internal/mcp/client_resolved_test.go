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
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/toolcontract"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var resolvedTestKey = bytes.Repeat([]byte{42}, 32)

func testResolvedDeclaration(t *testing.T) (toolcontract.LaunchDeclaration, toolcontract.LaunchContext) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	base := map[string]string{"PATH": filepath.Dir(executable), "PATHEXT": ".EXE;.COM", "HOST_VALUE": "frozen-value"}
	if runtime.GOOS == "windows" {
		base["SystemRoot"] = os.Getenv("SystemRoot")
	}
	return toolcontract.LaunchDeclaration{Component: toolcontract.ComponentRef{PackageID: "fixture-package", ComponentID: "mcp-fixture"},
			Format: "agent-plugins", FormatVersion: "1.0.0", Transport: toolcontract.TransportStdio,
			Stdio: &toolcontract.StdioLaunch{Command: filepath.Base(executable), Args: []string{"-test.run=^TestResolvedStdioHelper$", "--", "resolved-helper", "${PLUGIN_DATA}", "literal arg"},
				Env: map[string]string{"MCP_FIXTURE_VALUE": "frozen-value"}, Cwd: "${PLUGIN_ROOT}"}},
		toolcontract.LaunchContext{InstanceID: "fixture-instance", InstallRoot: root, DataRoot: root, BaseEnv: base, ProtocolVersions: []string{legacyClientProtocolVersion}}
}

func testResolvedOperation(kind toolcontract.OperationKind, component toolcontract.ComponentRef, input string) toolcontract.Operation {
	return toolcontract.Operation{ID: string(kind) + "-fixture", Kind: kind, ToolID: "fixture/lookup", Component: component,
		AdapterID: "mcp", AdapterRevision: "go-sdk-v1.8.0", InputFingerprint: input,
		Targets: []toolcontract.Target{{Kind: "endpoint", Locator: "fixture-target"}}, Effects: []toolcontract.Effect{toolcontract.EffectUnknown}}
}
func testResolvedScope(t *testing.T, launch toolcontract.ResolvedLaunch, requests int) toolcontract.DiscoveryScope {
	t.Helper()
	fingerprint, err := toolcontract.FingerprintLaunch(resolvedTestKey, launch)
	if err != nil {
		t.Fatal(err)
	}
	return toolcontract.DiscoveryScope{OperationID: "discover-fixture", ConnectionFingerprint: fingerprint, Profile: launch.ProtocolVersions[0],
		Methods: []string{"initialize", "notifications/initialized", "tools/list"}, MaxRequests: requests, MaxBytes: 32768, ExpiresAt: time.Now().Add(time.Minute)}
}

type resolvedGuardProbe struct {
	connects, discoveries, sends, calls atomic.Int32
	mu                                  sync.Mutex
	lastCall                            string
	denyConnect, denyCall               bool
}

func (p *resolvedGuardProbe) guards(t *testing.T, connect toolcontract.Operation) toolcontract.ExecutionGuards {
	t.Helper()
	expected, err := toolcontract.FingerprintOperation(connect)
	if err != nil {
		t.Fatal(err)
	}
	return toolcontract.ExecutionGuards{
		BeforeConnect: func(_ context.Context, actual string) error {
			p.connects.Add(1)
			if actual != expected || p.denyConnect {
				return errors.New("fixture startup denied")
			}
			return nil
		},
		BeginDiscovery: func(_ context.Context, scope toolcontract.DiscoveryScope) (toolcontract.DiscoverySendGuard, error) {
			p.discoveries.Add(1)
			return toolcontract.NewDiscoverySendGuard(scope, func(context.Context) error { p.sends.Add(1); return nil })
		},
		BeforeToolCall: func(_ context.Context, actual string) error {
			p.calls.Add(1)
			p.mu.Lock()
			p.lastCall = actual
			p.mu.Unlock()
			if p.denyCall {
				return errors.New("fixture tool denied")
			}
			return nil
		},
	}
}
func resolvedFixtureClient(t *testing.T, launch toolcontract.ResolvedLaunch, probe *resolvedGuardProbe, base *http.Client) *Client {
	t.Helper()
	fingerprint, err := toolcontract.FingerprintLaunch(resolvedTestKey, launch)
	if err != nil {
		t.Fatal(err)
	}
	connect := testResolvedOperation(toolcontract.OperationConnect, launch.Component, fingerprint)
	client, err := NewResolvedClient(launch, resolvedTestKey, connect, probe.guards(t, connect), nil, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
func resolvedFixtureCall(t *testing.T, client *Client, launch toolcontract.ResolvedLaunch, args string) toolcontract.Operation {
	t.Helper()
	fingerprint, err := FingerprintCallInput(resolvedTestKey, launch, "lookup", json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	operation := testResolvedOperation(toolcontract.OperationToolCall, launch.Component, fingerprint)
	operation.CapabilityFingerprint = client.NativeCapabilityFingerprint()
	return operation
}

func TestResolvedStdioRunsFrozenEnvironmentCwdAndArguments(t *testing.T) {
	declaration, host := testResolvedDeclaration(t)
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := launch.Stdio.Cwd
	declaration.Stdio.Args[4] = "changed-argument"
	declaration.Stdio.Env["MCP_FIXTURE_VALUE"] = "changed-env"
	host.BaseEnv["HOST_VALUE"] = "changed-host"
	probe := &resolvedGuardProbe{}
	client := resolvedFixtureClient(t, launch, probe, nil)
	// Mutation of a resolved value after construction cannot alter the command.
	launch.Stdio.Env["MCP_FIXTURE_VALUE"] = "changed-resolved"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := client.DiscoverWithScope(ctx, testResolvedScope(t, client.resolved.launch, 3)); err != nil {
		t.Fatal(err)
	}
	operation := resolvedFixtureCall(t, client, client.resolved.launch, "{}")
	var notifications atomic.Int32
	progressDone := make(chan struct{})
	if err := client.SetProgressHandler(func(_ context.Context, params *sdk.ProgressNotificationParams) {
		if params.ProgressToken == operation.ID && notifications.Add(1) == 40 {
			close(progressDone)
		}
	}); err != nil {
		t.Fatal(err)
	}
	result, native, receipt, err := client.CallToolWithReceipt(ctx, operation, "lookup", json.RawMessage("{}"))
	if err != nil || result == nil || receipt.State != toolcontract.ReceiptResultReceived {
		t.Fatalf("call: %v %v", receipt, err)
	}
	select {
	case <-progressDone:
	case <-ctx.Done():
		t.Fatal("progress notifications were lost")
	}
	var shape struct {
		StructuredContent struct{ Value, Cwd, Data, Arg string } `json:"structuredContent"`
	}
	if json.Unmarshal(native, &shape) != nil || shape.StructuredContent.Value != "frozen-value" || shape.StructuredContent.Cwd != wantRoot || shape.StructuredContent.Data != wantRoot || shape.StructuredContent.Arg != "literal arg" {
		t.Fatalf("frozen process configuration was not used: %s", native)
	}
	expected, _ := toolcontract.FingerprintOperation(operation)
	probe.mu.Lock()
	actual := probe.lastCall
	probe.mu.Unlock()
	if actual != expected || probe.connects.Load() != 1 || probe.discoveries.Load() != 1 || probe.sends.Load() != 3 || probe.calls.Load() != 1 {
		t.Fatalf("guards must separate startup/discovery/dispatch: %d/%d/%d/%d", probe.connects.Load(), probe.discoveries.Load(), probe.sends.Load(), probe.calls.Load())
	}
}

func TestResolveLaunchPreservesUnknownVariablesAndRejectsWindowsEnvironmentAliases(t *testing.T) {
	declaration, host := testResolvedDeclaration(t)
	declaration.Stdio.Args = []string{"${MISSING}"}
	if resolved, err := ResolveLaunch(declaration, host); err != nil || resolved.Stdio.Args[0] != "${MISSING}" {
		t.Fatal("unknown placeholder did not remain literal")
	}
	if runtime.GOOS == "windows" {
		declaration.Stdio.Args = nil
		host.BaseEnv["Path"] = host.BaseEnv["PATH"]
		if _, err := ResolveLaunch(declaration, host); err == nil {
			t.Fatal("ambiguous Path/PATH accepted")
		}
	}
}

func TestResolvedLaunchRechecksExecutableAfterPreparation(t *testing.T) {
	declaration, host := testResolvedDeclaration(t)
	fake := filepath.Join(host.InstallRoot, "fixture.exe")
	if err := os.WriteFile(fake, []byte("prepared executable"), 0755); err != nil {
		t.Fatal(err)
	}
	declaration.Stdio.Command = "./fixture.exe"
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	probe := &resolvedGuardProbe{}
	client := resolvedFixtureClient(t, launch, probe, nil)
	if err := os.WriteFile(fake, []byte("replacement executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, launch, 3)); err == nil || probe.connects.Load() != 0 {
		t.Fatal("changed executable reached startup authorization")
	}
}

func TestResolvedDiscoveryPaginationCannotOverspendOrConsumeToolApproval(t *testing.T) {
	declaration, host := testResolvedDeclaration(t)
	declaration.Stdio.Env["MCP_FIXTURE_PAGINATION"] = "1"
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	probe := &resolvedGuardProbe{}
	client := resolvedFixtureClient(t, launch, probe, nil)
	_, err = client.DiscoverWithScope(t.Context(), testResolvedScope(t, launch, 3))
	if err == nil || probe.discoveries.Load() != 1 || probe.sends.Load() != 3 || probe.calls.Load() != 0 || client.NativeCapabilityFingerprint() != "" {
		t.Fatalf("pagination budget was not enforced: %v sends=%d", err, probe.sends.Load())
	}
}

func TestResolvedHTTPGuardsHeadersCredentialsAndUnknownReceipt(t *testing.T) {
	var requests, calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-Fixture") != "original-header" || r.Header.Get("Authorization") != "Bearer fixture-credential" {
			t.Error("frozen headers or bound credential did not reach server")
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
		if request.Method == "tools/call" {
			calls.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(clientFixtureResponse(request))
	}))
	defer server.Close()
	declaration, host := testResolvedDeclaration(t)
	declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
	declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: server.URL + "?private=fixture-query", Headers: map[string]string{"X-Fixture": "original-header"}, Credential: &toolcontract.CredentialRef{ID: "fixture-credential", Revision: "1"}}
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, _ := toolcontract.FingerprintLaunch(resolvedTestKey, launch)
	connect := testResolvedOperation(toolcontract.OperationConnect, launch.Component, fingerprint)
	probe := &resolvedGuardProbe{}
	var credentials atomic.Int32
	client, err := NewResolvedClient(launch, resolvedTestKey, connect, probe.guards(t, connect), func(_ context.Context, ref toolcontract.CredentialRef) (string, error) {
		credentials.Add(1)
		if ref.ID != "fixture-credential" || ref.Revision != "1" {
			t.Error("credential revision not preserved")
		}
		return "fixture-credential", nil
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	launch.HTTP.Headers["X-Fixture"] = "mutated-header"
	launch.HTTP.Credential.Revision = "2"
	if requests.Load() != 0 || credentials.Load() != 0 {
		t.Fatal("constructor performed egress")
	}
	if _, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, client.resolved.launch, 3)); err != nil {
		t.Fatal(err)
	}
	operation := resolvedFixtureCall(t, client, client.resolved.launch, "{}")
	_, _, receipt, err := client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}"))
	if err == nil || receipt.State != toolcontract.ReceiptOutcomeUnknown || calls.Load() != 1 || credentials.Load() != 1 || strings.Contains(err.Error(), "fixture-query") {
		t.Fatalf("unknown receipt/redaction/replay: %v %v calls=%d credentials=%d", receipt, err, calls.Load(), credentials.Load())
	}
}

func TestResolvedToolGuardDenialRetainsNotDispatchedReceipt(t *testing.T) {
	declaration, host := testResolvedDeclaration(t)
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	probe := &resolvedGuardProbe{denyCall: true}
	client := resolvedFixtureClient(t, launch, probe, nil)
	if _, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, launch, 3)); err != nil {
		t.Fatal(err)
	}
	operation := resolvedFixtureCall(t, client, launch, "{}")
	_, native, receipt, err := client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}"))
	if err == nil || len(native) != 0 || receipt.State != toolcontract.ReceiptNotDispatched || probe.calls.Load() != 1 {
		t.Fatalf("host denial lost its receipt: %v %v", receipt, err)
	}
}

func TestResolvedHTTPStartupDenialPreventsAnyEgress(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	declaration, host := testResolvedDeclaration(t)
	declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
	declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: server.URL}
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	probe := &resolvedGuardProbe{denyConnect: true}
	client := resolvedFixtureClient(t, launch, probe, server.Client())
	if _, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, launch, 3)); err == nil || requests.Load() != 0 || probe.connects.Load() != 1 || probe.sends.Load() != 0 {
		t.Fatalf("startup denial leaked egress: %v requests=%d sends=%d", err, requests.Load(), probe.sends.Load())
	}
}

func TestResolvedCancellationKeepsReceiptAndConnection(t *testing.T) {
	declaration, host := testResolvedDeclaration(t)
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	probe := &resolvedGuardProbe{}
	client := resolvedFixtureClient(t, launch, probe, nil)
	writeCompleted := make(chan struct{})
	client.transport = &completedMCPWriteTransport{Transport: client.transport, toolWritten: writeCompleted}
	ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	if _, err := client.DiscoverWithScope(ctx, testResolvedScope(t, launch, 3)); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	var once sync.Once
	if err := client.SetProgressHandler(func(context.Context, *sdk.ProgressNotificationParams) { once.Do(func() { close(ready) }) }); err != nil {
		t.Fatal(err)
	}
	operation := resolvedFixtureCall(t, client, launch, "{\"mode\":\"wait\"}")
	blocked, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, receipt, err := client.CallToolWithReceipt(blocked, operation, "lookup", json.RawMessage("{\"mode\":\"wait\"}"))
		if !errors.Is(err, context.Canceled) || receipt.State != toolcontract.ReceiptOutcomeUnknown {
			t.Errorf("cancelled receipt: %v %v", receipt, err)
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Cancel a response wait only after the real adapter's Write has returned.
	// Cancellation while still writing has a different fail-closed contract.
	select {
	case <-writeCompleted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	operation = resolvedFixtureCall(t, client, launch, "{}")
	operation.ID = "after-cancel"
	result, _, receipt, err := client.CallToolWithReceipt(ctx, operation, "lookup", json.RawMessage("{}"))
	if err != nil || result == nil || receipt.State != toolcontract.ReceiptResultReceived {
		t.Fatalf("connection after cancellation: %v %v", receipt, err)
	}
	if probe.connects.Load() != 1 || probe.calls.Load() != 2 {
		t.Fatalf("cancellation reconnected or resent an unknown operation: connects=%d calls=%d", probe.connects.Load(), probe.calls.Load())
	}
}

func TestResolvedNativeCapabilityChangeInvalidatesPreparedOperation(t *testing.T) {
	var catalogVersion atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Envelope
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		if request.Method == "tools/call" {
			t.Error("stale capability reached server")
		}
		if request.Method == "tools/list" {
			sdkWriteResponse(w, request.ID, fmt.Sprintf("{\"tools\":[{\"name\":\"lookup\",\"inputSchema\":{\"type\":\"object\"},\"vendorRevision\":%d}]}", catalogVersion.Load()))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(clientFixtureResponse(request))
	}))
	defer server.Close()
	declaration, host := testResolvedDeclaration(t)
	declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
	declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: server.URL}
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	probe := &resolvedGuardProbe{}
	client := resolvedFixtureClient(t, launch, probe, server.Client())
	first, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, launch, 3))
	if err != nil {
		t.Fatal(err)
	}
	operation := resolvedFixtureCall(t, client, launch, "{}")
	catalogVersion.Store(1)
	second, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, launch, 3))
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatal("fixture unexpectedly changed the legacy projection")
	}
	_, _, receipt, err := client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}"))
	if err == nil || receipt.State != toolcontract.ReceiptNotDispatched || probe.calls.Load() != 0 {
		t.Fatalf("stale native metadata: %v %v", receipt, err)
	}
}

func TestResolvedStdioHelper(t *testing.T) {
	if !slices.Contains(os.Args, "resolved-helper") {
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
			var params struct {
				Meta map[string]string `json:"_meta"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if token := params.Meta["progressToken"]; token != "" {
				for i := 1; i <= 40; i++ {
					notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{"progressToken": token, "progress": i, "total": 40}})
					fmt.Fprintln(os.Stdout, string(notification))
				}
			}
			if bytes.Contains(request.Params, []byte("\"wait\"")) {
				continue
			}
			cwd, _ := os.Getwd()
			result, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": "ok"}},
				"structuredContent": map[string]string{"Value": os.Getenv("MCP_FIXTURE_VALUE"), "Cwd": cwd, "Data": os.Args[len(os.Args)-2], "Arg": os.Args[len(os.Args)-1]}})
			response, _ := json.Marshal(Envelope{JSONRPC: "2.0", ID: request.ID, Result: result})
			fmt.Fprintln(os.Stdout, string(response))
		} else if request.Method == "tools/list" && os.Getenv("MCP_FIXTURE_PAGINATION") == "1" {
			fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"tools\":[],\"nextCursor\":\"again\"}}\n", request.ID)
		} else {
			fmt.Fprintln(os.Stdout, string(clientFixtureResponse(request)))
		}
	}
}
