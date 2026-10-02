package application

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolgateway"
)

// Exercise the native package path, including operator discovery and a new
// runtime connection pinned to the reviewed legacy version. The peer is local;
// import, SQLite, SDK negotiation, wire guards and the executor are production.
func TestNativeMCPLegacyFallbackReachesReviewedProductionCall(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05", "2099-01-01"} {
		t.Run(version, func(t *testing.T) {
			ctx := t.Context()
			state, run, root, lease, capabilities := newMCPApprovalModeRuntime(t, ctx)
			root = ensureCommandRuntimeTestAgent(t, ctx, state, lease, root)
			var runtimePhase atomic.Bool
			var traceMu sync.Mutex
			var trace []string
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				var frame mcp.Envelope
				if err := json.NewDecoder(r.Body).Decode(&frame); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				traceMu.Lock()
				trace = append(trace, frame.Method)
				traceMu.Unlock()
				response := mcp.Envelope{JSONRPC: "2.0", ID: frame.ID}
				switch frame.Method {
				case "server/discover":
					response.Error = &mcp.RPCError{Code: mcp.CodeMethodNotFound, Message: "legacy only"}
				case "initialize":
					var params struct {
						ProtocolVersion string `json:"protocolVersion"`
					}
					want := "2025-11-25" // SDK v1.8.0's actual fallback request.
					if runtimePhase.Load() {
						want = version
					}
					if err := json.Unmarshal(frame.Params, &params); err != nil || params.ProtocolVersion != want {
						t.Errorf("initialize sent %q, want explicitly permitted %q: %v", params.ProtocolVersion, want, err)
					}
					response.Result = json.RawMessage(fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}},"serverInfo":{"name":"native-legacy","version":"1"}}`, version))
				case "notifications/initialized":
					w.WriteHeader(http.StatusAccepted)
					return
				case "tools/list":
					response.Result = json.RawMessage(`{"tools":[{"name":"legacy_lookup","inputSchema":{"type":"object"}}]}`)
				case "tools/call":
					response.Result = json.RawMessage(`{"content":[{"type":"text","text":"legacy reply"}]}`)
				default:
					t.Errorf("unexpected legacy method %q", frame.Method)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer peer.Close()
			wire := func() []string {
				traceMu.Lock()
				defer traceMu.Unlock()
				return slices.Clone(trace)
			}
			directory := t.TempDir()
			config, _ := json.Marshal(map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": map[string]any{
				"legacy-service": map[string]string{"type": "streamable-http", "url": peer.URL}}})
			for name, raw := range map[string][]byte{
				"plugin.json": []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"native-legacy"}`), "mcp.json": config,
			} {
				if err := os.WriteFile(filepath.Join(directory, name), raw, 0600); err != nil {
					t.Fatal(err)
				}
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
				OperationKey: "native-legacy-import", InstalledBy: "operator", ConfirmUntrusted: true})
			if err != nil || imported.Portable == nil {
				t.Fatal("native import failed", err)
			}
			installed := *imported.Portable
			service, _ := plugins.NewService(state)
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
			if err != nil || len(records) != 1 || len(wire()) != 0 {
				t.Fatal("native staging failed or connected early", err, wire())
			}
			record := records[0]
			record, err = manager.Review(ctx, record.Descriptor.ID, mcp.ReviewRequest{Action: mcp.ReviewApproveDiscovery, ExpectedDescriptorFingerprint: record.DescriptorFingerprint, ReviewedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			record, err = manager.Refresh(ctx, record.Descriptor.ID)
			want := []string{"server/discover", "initialize"}
			if version == "2099-01-01" {
				if err == nil || !slices.Equal(wire(), want) || record.Capabilities.Fingerprint != "" {
					t.Fatal("unsupported peer version accepted or follow-up request dispatched", err, wire())
				}
				return
			}
			want = append(want, "notifications/initialized", "tools/list")
			if err != nil || record.Capabilities.ProtocolVersion != version || !slices.Equal(wire(), want) {
				t.Fatalf("native legacy review discovery failed: negotiated=%q wire=%v err=%v", record.Capabilities.ProtocolVersion, wire(), err)
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
			result, err := executor.ExecuteMCP(ctx, scope, toolgateway.MCPToolCallPayload{Version: toolgateway.MCPClientToolProtocolVersion, ServerID: record.Descriptor.ID,
				ToolName: "legacy_lookup", CapabilityFingerprint: record.Capabilities.Fingerprint, Arguments: json.RawMessage(`{}`)})
			want = append(want, "initialize", "notifications/initialized", "tools/list", "tools/call")
			if err != nil || !strings.Contains(result.Content, "legacy reply") || !slices.Equal(wire(), want) {
				t.Fatalf("native reviewed legacy production call failed: wire=%v result=%+v err=%v", wire(), result, err)
			}
		})
	}
}
