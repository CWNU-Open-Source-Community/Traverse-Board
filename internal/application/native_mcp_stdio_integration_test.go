package application

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/toolgateway"
)

// The original package contains the script and its native launch declaration.
// Node is a locally installed interpreter; this test never installs dependencies.
func TestNativeMCPOriginalScriptReachesGuardedProductionStdio(t *testing.T) {
	node := os.Getenv("TRAVERSE_TEST_NODE_PATH")
	if node == "" {
		var err error
		node, err = exec.LookPath("node")
		if err != nil {
			t.Skip("local Node interpreter is unavailable")
		}
	}
	node, err := filepath.Abs(node)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(node)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NATIVE_MCP_AMBIENT_CANARY", "must-not-inherit")
	for _, scenario := range []string{"call", "change_script_during_discovery"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			state, run, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
			root = ensureCommandRuntimeTestAgent(t, ctx, state, lease, root)
			config, _ := json.Marshal(map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": map[string]any{"packaged-script": map[string]any{
				"type": "stdio", "command": filepath.Base(node), "args": []string{"${PLUGIN_ROOT}/server.cjs"}, "cwd": "${PLUGIN_DATA}",
				"env": map[string]string{"NATIVE_SECRET": "credential-${PLUGIN_DATA}-end"}}}})
			installed := importNativeMCPFixture(t, state, config, map[string][]byte{"server.cjs": []byte(nativeMCPNodeScript)})
			service, err := plugins.NewService(state)
			if err != nil {
				t.Fatal(err)
			}
			stateRoot := t.TempDir()
			resolver, err := NewNativeMCPSourceResolver(state, stateRoot)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := mcp.NewClientManager(state, nil, mcp.ManagerOptions{NativeSources: resolver})
			if err != nil {
				t.Fatal(err)
			}
			records, err := service.StageMCPServers(ctx, installed.ID, mcp.ScopeRun, run.ID, "workspace-command-runtime-app", manager)
			if err != nil || len(records) != 1 {
				t.Fatal("native script staging", err)
			}
			record := records[0]
			record, err = manager.Review(ctx, record.Descriptor.ID, mcp.ReviewRequest{Action: mcp.ReviewApproveDiscovery, ExpectedDescriptorFingerprint: record.DescriptorFingerprint, ReviewedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			record, err = manager.Refresh(ctx, record.Descriptor.ID)
			if err != nil {
				t.Fatal("native script discovery", err)
			}
			record, err = manager.Review(ctx, record.Descriptor.ID, mcp.ReviewRequest{Action: mcp.ReviewEnableCapabilities,
				ExpectedDescriptorFingerprint: record.DescriptorFingerprint, ExpectedCapabilityFingerprint: record.Capabilities.Fingerprint, ReviewedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			dataRoot := filepath.Join(stateRoot, "data", portableIdentity(installed.ID+"\x00"+record.Descriptor.NativeSource.Component.ComponentID))
			if scenario == "change_script_during_discovery" {
				if err := os.WriteFile(filepath.Join(dataRoot, "change-script"), []byte("explicit fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			permission, err := state.GetRunExecutionPermission(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			scope := exactPermissionMCPScope(run, lease, permission)
			scope.AgentID, scope.AgentAttemptID = root.ID, root.ActiveAttemptID
			executor, err := NewMCPClientToolExecutor(manager, state, capabilities)
			if err != nil {
				t.Fatal(err)
			}
			result, callErr := executor.ExecuteMCP(ctx, scope, toolgateway.MCPToolCallPayload{Version: toolgateway.MCPClientToolProtocolVersion, ServerID: record.Descriptor.ID,
				ToolName: "packaged_lookup", CapabilityFingerprint: record.Capabilities.Fingerprint, Arguments: json.RawMessage(`{}`)})
			trace, err := os.ReadFile(filepath.Join(dataRoot, "wire.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			calls, roots := 0, map[string]bool{}
			for _, line := range strings.Split(strings.TrimSpace(string(trace)), "\n") {
				var event struct {
					Method, Root, Cwd, Script string
					Ambient, ExpandedSecret   bool
				}
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				if event.Ambient || !event.ExpandedSecret || !strings.EqualFold(filepath.Clean(event.Cwd), filepath.Clean(dataRoot)) || !strings.EqualFold(filepath.Dir(event.Script), event.Root) {
					t.Fatalf("original script launch lost cwd/env/source semantics: %+v", event)
				}
				roots[event.Root] = true
				if event.Method == "tools/call" {
					calls++
				}
			}
			if len(roots) != 2 {
				t.Fatal("expected independently checked review/runtime materializations", len(roots))
			}
			if scenario == "call" {
				if callErr != nil || calls != 1 || !strings.Contains(result.Content, "packaged-result") || strings.Contains(result.Content, "credential-") {
					t.Fatalf("native script dispatch/redaction: calls=%d result=%+v err=%v", calls, result, callErr)
				}
			} else {
				receipt, found := mcp.InvocationReceipt(callErr)
				if callErr == nil || !found || receipt.State != toolcontract.ReceiptNotDispatched || calls != 0 || result.Content != "" {
					t.Fatalf("changed source reached tools/call: calls=%d receipt=%+v err=%v", calls, receipt, callErr)
				}
			}
			for root := range roots {
				if _, err := os.Stat(root); !os.IsNotExist(err) {
					t.Fatal("transient source extraction remained after session close", err)
				}
			}
		})
	}
}

func importNativeMCPFixture(t *testing.T, state *store.SQLiteStore, config []byte, files map[string][]byte) plugins.Installation {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "native-mcp")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{"plugin.json": []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"native-mcp"}`), "mcp.json": config}
	for name, raw := range files {
		contents[name] = raw
	}
	for name, raw := range contents {
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
	imported, err := catalog.ImportFromDirectory(t.Context(), ImportSkillFromDirectoryRequest{Directory: directory, Surface: domain.ExecutionSurfaceCode,
		OperationKey: "native-mcp-import", InstalledBy: "operator", ConfirmUntrusted: true})
	if err != nil || imported.Portable == nil || imported.Portable.State != plugins.StateStaged {
		t.Fatal("original import must remain staged", err)
	}
	installed := *imported.Portable
	service, err := plugins.NewService(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []plugins.ReviewAction{plugins.ReviewApprove, plugins.ReviewEnable} {
		installed, err = service.Review(t.Context(), installed.ID, plugins.ReviewRequest{Action: action, ExpectedPackageFingerprint: installed.PackageFingerprint,
			ExpectedGeneration: installed.Generation, Capabilities: []plugins.Capability{plugins.CapabilityMCP}, ConfirmUntrusted: true, ReviewedBy: "operator"})
		if err != nil {
			t.Fatal(err)
		}
	}
	return installed
}

const nativeMCPNodeScript = `const fs = require('fs');
const path = require('path');
const readline = require('readline');
const root = process.env.PLUGIN_ROOT, data = process.env.PLUGIN_DATA;
readline.createInterface({input: process.stdin}).on('line', line => {
  const frame = JSON.parse(line);
  fs.appendFileSync(path.join(data, 'wire.jsonl'), JSON.stringify({Method: frame.method, Root: root, Cwd: process.cwd(), Script: __filename,
    Ambient: !!process.env.NATIVE_MCP_AMBIENT_CANARY, ExpandedSecret: process.env.NATIVE_SECRET === 'credential-' + data + '-end'}) + '\n');
  if (frame.id === undefined) return;
  let result;
  if (frame.method === 'server/discover') {
    result = {supportedVersions:['2026-07-28'], capabilities:{tools:{}}, _meta:{'io.modelcontextprotocol/serverInfo':{name:'packaged-script',version:'1'}}, resultType:'complete', ttlMs:0, cacheScope:'private'};
  } else if (frame.method === 'tools/list') {
    if (fs.existsSync(path.join(data, 'change-script'))) fs.appendFileSync(__filename, '\n// source changed during discovery\n');
    result = {tools:[{name:'packaged_lookup',inputSchema:{type:'object'}}], resultType:'complete', ttlMs:0, cacheScope:'private'};
  } else if (frame.method === 'tools/call') {
    result = {content:[{type:'text',text:'packaged-result ' + process.env.NATIVE_SECRET}],resultType:'complete'};
  } else { process.exit(3); }
  process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:frame.id,result}) + '\n');
});
`
