package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/toolgateway"
)

// Original Agent Plugins source travels through the existing source import,
// plugin review, MCP staging/review, SQLite records, and production executor.
func TestNativeMCPOriginalSourceReachesGuardedProductionTLS(t *testing.T) {
	for _, scenario := range []string{"call", "disable_during_discovery", "disable_during_runtime", "disable_after_call"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			state, run, root, lease, capabilities := newMCPApprovalModeRuntime(t, ctx)
			root = ensureCommandRuntimeTestAgent(t, ctx, state, lease, root)
			service, _ := plugins.NewService(state)
			var installed plugins.Installation
			var runtimePhase atomic.Bool
			var calls, runtimeLists, totalRequests atomic.Int32
			disable := func() {
				_, err := service.Review(ctx, installed.ID, plugins.ReviewRequest{Action: plugins.ReviewDisable,
					ExpectedPackageFingerprint: installed.PackageFingerprint, ExpectedGeneration: installed.Generation, ReviewedBy: "operator"})
				if err != nil {
					t.Error(err)
				}
			}
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(204)
					return
				}
				totalRequests.Add(1)
				if r.URL.Query().Get("literal") != "${PLUGIN_ROOT}" || r.Header.Get("X-Native-Token") != "native-source-credential" {
					t.Error("native literal query/header was lost or expanded")
				}
				var frame mcp.Envelope
				if json.NewDecoder(r.Body).Decode(&frame) != nil {
					w.WriteHeader(400)
					return
				}
				if len(frame.ID) == 0 {
					w.WriteHeader(202)
					return
				}
				var body json.RawMessage
				switch frame.Method {
				case "server/discover":
					body = json.RawMessage(`{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{}},"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"native-source","version":"1"}},"resultType":"complete","ttlMs":0,"cacheScope":"private"}`)
				case "tools/list":
					body = json.RawMessage(`{"tools":[{"name":"native_lookup","inputSchema":{"type":"object"}}],"resultType":"complete","ttlMs":0,"cacheScope":"private"}`)
					if runtimePhase.Load() {
						runtimeLists.Add(1)
					}
					if (scenario == "disable_during_discovery" && !runtimePhase.Load()) || (scenario == "disable_during_runtime" && runtimePhase.Load()) {
						disable()
						body = json.RawMessage(`{"tools":[],"nextCursor":"must-not-send"}`)
					}
				case "tools/call":
					calls.Add(1)
					if scenario == "disable_after_call" {
						disable()
					}
					body = json.RawMessage(`{"content":[{"type":"text","text":"native-source-credential received"}],"structuredContent":{"value":9007199254740993},"vendorExtension":{"retained":true},"resultType":"complete"}`)
				default:
					t.Errorf("unexpected native method %s", frame.Method)
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(mcp.Envelope{JSONRPC: "2.0", ID: frame.ID, Result: body})
			}))
			defer peer.Close()
			directory := filepath.Join(t.TempDir(), "native-mcp")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			manifest := []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"native-mcp"}`)
			config, _ := json.Marshal(map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": map[string]any{"native-service": map[string]any{
				"type": "streamable-http", "url": peer.URL + "/mcp?literal=${PLUGIN_ROOT}", "headers": map[string]string{"X-Native-Token": "native-source-credential"}}}})
			if err := os.WriteFile(filepath.Join(directory, "plugin.json"), manifest, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "mcp.json"), config, 0600); err != nil {
				t.Fatal(err)
			}
			builtins, err := skills.BuiltinRegistry()
			if err != nil {
				t.Fatal(err)
			}
			objects, err := skills.NewLocalPackageObjectStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			catalog := NewSkillCatalogService(state, NewSkillPackageRegistryService(state, objects, builtins))
			imported, err := catalog.ImportFromDirectory(ctx, ImportSkillFromDirectoryRequest{Directory: directory, Surface: domain.ExecutionSurfaceCode,
				OperationKey: "native-mcp-import", InstalledBy: "operator", ConfirmUntrusted: true})
			if err != nil || imported.Portable == nil {
				t.Fatal("original import failed", err)
			}
			installed = *imported.Portable
			if installed.State != plugins.StateStaged || len(installed.EnabledCapabilities) != 0 || installed.Snapshot.AuthorVersion != "" || !slices.Contains(installed.Capabilities(), plugins.CapabilityMCP) || totalRequests.Load() != 0 {
				t.Fatal("import fabricated identity or activated MCP", installed.State, totalRequests.Load())
			}
			for _, action := range []plugins.ReviewAction{plugins.ReviewApprove, plugins.ReviewEnable} {
				installed, err = service.Review(ctx, installed.ID, plugins.ReviewRequest{Action: action, ExpectedPackageFingerprint: installed.PackageFingerprint,
					ExpectedGeneration: installed.Generation, Capabilities: []plugins.Capability{plugins.CapabilityMCP}, ConfirmUntrusted: true, ReviewedBy: "operator"})
				if err != nil {
					t.Fatal(err)
				}
			}
			resolver, err := NewNativeMCPSourceResolver(state, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			manager, err := mcp.NewClientManager(state, nil, mcp.ManagerOptions{HTTPClient: peer.Client(), NativeSources: resolver})
			if err != nil {
				t.Fatal(err)
			}
			records, err := service.StageMCPServers(ctx, installed.ID, mcp.ScopeRun, run.ID, "workspace-command-runtime-app", manager)
			if err != nil || len(records) != 1 {
				t.Fatal("native staging failed", len(records), err)
			}
			record := records[0]
			if record.Descriptor.ProtocolVersion != mcp.NativeClientProtocolVersion || record.Descriptor.NativeSource == nil || record.Descriptor.Target != "" || len(record.Descriptor.Arguments) != 0 || totalRequests.Load() != 0 {
				t.Fatal("native registration projected legacy config or connected early")
			}
			encoded, _ := json.Marshal(record)
			if strings.Contains(string(encoded), "native-source-credential") || strings.Contains(string(encoded), peer.URL) {
				t.Fatal("registration exposed raw native configuration")
			}
			// Mutation of the import directory cannot replace the retained object.
			if err := os.WriteFile(filepath.Join(directory, "mcp.json"), []byte(`{"bad":"replaced source"}`), 0600); err != nil {
				t.Fatal(err)
			}
			record, err = manager.Review(ctx, record.Descriptor.ID, mcp.ReviewRequest{Action: mcp.ReviewApproveDiscovery, ExpectedDescriptorFingerprint: record.DescriptorFingerprint, ReviewedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			record, err = manager.Refresh(ctx, record.Descriptor.ID)
			if scenario == "disable_during_discovery" {
				if err == nil || calls.Load() != 0 || totalRequests.Load() != 2 {
					t.Fatal("revoked native discovery continued", totalRequests.Load(), err)
				}
				return
			}
			if err != nil {
				t.Fatal("native discovery failed", err)
			}
			record, err = manager.Review(ctx, record.Descriptor.ID, mcp.ReviewRequest{Action: mcp.ReviewEnableCapabilities, ExpectedDescriptorFingerprint: record.DescriptorFingerprint,
				ExpectedCapabilityFingerprint: record.Capabilities.Fingerprint, ReviewedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			permission, err := state.GetRunExecutionPermission(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			scope := exactPermissionMCPScope(run, lease, permission)
			bindMCPScopeRuntime(t, &scope, permission, capabilities)
			scope.AgentID, scope.AgentAttemptID = root.ID, root.ActiveAttemptID
			executor, err := NewMCPClientToolExecutor(manager, state, capabilities)
			if err != nil {
				t.Fatal(err)
			}
			runtimePhase.Store(true)
			result, callErr := executor.ExecuteMCP(ctx, scope, toolgateway.MCPToolCallPayload{Version: toolgateway.MCPClientToolProtocolVersion, ServerID: record.Descriptor.ID,
				ToolName: "native_lookup", CapabilityFingerprint: record.Capabilities.Fingerprint, Arguments: json.RawMessage(`{"value":9007199254740993}`)})
			if scenario == "call" {
				if callErr != nil || calls.Load() != 1 || !strings.Contains(result.Content, "vendorExtension") || !strings.Contains(result.Content, "9007199254740993") || strings.Contains(result.Content, "native-source-credential") {
					t.Fatalf("native guarded call failed: %+v calls=%d err=%v", result, calls.Load(), callErr)
				}
				return
			}
			receipt, found := mcp.InvocationReceipt(callErr)
			want, wantCalls := toolcontract.ReceiptNotDispatched, int32(0)
			if scenario == "disable_after_call" {
				want, wantCalls = toolcontract.ReceiptResultReceived, 1
			}
			if callErr == nil || !found || receipt.State != want || calls.Load() != wantCalls || result.Content != "" || runtimeLists.Load() != 1 {
				t.Fatalf("native revocation failed: %+v calls=%d err=%v", receipt, calls.Load(), callErr)
			}
			available, err := manager.Capabilities(context.Background(), run.ID, "workspace-command-runtime-app")
			if err != nil || len(available.Servers) != 0 {
				t.Fatal("disabled installation remained advertised", err)
			}
		})
	}
}
