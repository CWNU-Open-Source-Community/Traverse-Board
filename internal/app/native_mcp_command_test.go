package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/store"
)

func TestNativeMCPCLIUsesExistingImportReviewAndDiscoveryLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	state, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SaveWorkspace(t.Context(), store.WorkspaceRecord{ID: "native-cli-workspace", Name: "native", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(204)
			return
		}
		requests.Add(1)
		var frame mcp.Envelope
		if err := json.NewDecoder(r.Body).Decode(&frame); err != nil {
			t.Error(err)
			return
		}
		var result json.RawMessage
		switch frame.Method {
		case "server/discover":
			result = json.RawMessage(`{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{}},"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"native-cli","version":"1"}},"resultType":"complete","ttlMs":0,"cacheScope":"private"}`)
		case "tools/list":
			result = json.RawMessage(`{"tools":[{"name":"cli_lookup","inputSchema":{"type":"object"}}],"resultType":"complete","ttlMs":0,"cacheScope":"private"}`)
		default:
			t.Errorf("CLI discovery sent unexpected operation %s", frame.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mcp.Envelope{JSONRPC: "2.0", ID: frame.ID, Result: result})
	}))
	defer peer.Close()
	directory := filepath.Join(t.TempDir(), "native-cli")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": map[string]any{"peer": map[string]string{"type": "streamable-http", "url": peer.URL}}})
	// A malformed sibling Skill must not prevent the valid native MCP component.
	for name, raw := range map[string][]byte{"plugin.json": []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"native-cli"}`), "mcp.json": config,
		"skills/broken/SKILL.md": []byte("---\nname: broken\n---\nMissing required description.\n")} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(directory, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := func(args ...string) string {
		t.Helper()
		out, stderr, code := executeTestCommand(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("command %v: exit=%d stderr=%q", args, code, stderr)
		}
		return out
	}
	if out := command("skill", "import-dir", directory, "--surface", "code", "--operation-key", "native-cli-import", "--confirm-untrusted-skill"); !strings.Contains(out, "state: staged") || requests.Load() != 0 {
		t.Fatal("MCP-only source activated on import", out, requests.Load())
	}
	var installations []plugins.Installation
	if err := json.Unmarshal([]byte(command("plugin", "list")), &installations); err != nil || len(installations) != 1 {
		t.Fatal("imported native plugin is absent", err)
	}
	installed := installations[0]
	if len(installed.Snapshot.Diagnostics) == 0 || len(installed.Snapshot.Skills) != 0 {
		t.Fatal("malformed sibling Skill was not diagnosed separately")
	}
	reviewPlugin := func(action string) {
		t.Helper()
		args := []string{"plugin", "review", installed.ID, "--action", action, "--fingerprint", installed.PackageFingerprint, "--generation", fmt.Sprint(installed.Generation), "--by", "operator", "--confirm-untrusted"}
		if action == "enable" {
			args = append(args, "--capabilities", string(plugins.CapabilityMCP))
		}
		if err := json.Unmarshal([]byte(command(args...)), &installed); err != nil {
			t.Fatal(err)
		}
	}
	stage := func() mcp.ServerRecord {
		t.Helper()
		var records []mcp.ServerRecord
		if err := json.Unmarshal([]byte(command("plugin", "stage-mcp", installed.ID, "--scope", "workspace", "--workspace", "native-cli-workspace")), &records); err != nil || len(records) != 1 {
			t.Fatal("stage native reference", err)
		}
		return records[0]
	}
	reviewPlugin("approve")
	reviewPlugin("enable")
	record := stage()
	if stage().DescriptorFingerprint != record.DescriptorFingerprint || requests.Load() != 0 {
		t.Fatal("staging replay changed or connected")
	}
	command("mcp", "client", "review", record.Descriptor.ID, "--action", "approve_discovery", "--descriptor-fingerprint", record.DescriptorFingerprint, "--by", "operator")
	if err := json.Unmarshal([]byte(command("mcp", "client", "refresh", record.Descriptor.ID)), &record); err != nil || requests.Load() != 2 {
		t.Fatal("actual CLI discovery did not run", err, requests.Load())
	}
	command("mcp", "client", "review", record.Descriptor.ID, "--action", "enable_capabilities", "--descriptor-fingerprint", record.DescriptorFingerprint, "--capability-fingerprint", record.Capabilities.Fingerprint, "--by", "operator")
	approved := record.Capabilities.Fingerprint
	reviewPlugin("disable")
	if _, _, code := executeTestCommand(t, "mcp", "client", "refresh", record.Descriptor.ID); code == 0 || requests.Load() != 2 {
		t.Fatal("disabled plugin reached discovery")
	}
	if err := json.Unmarshal([]byte(command("mcp", "client", "show", record.Descriptor.ID)), &record); err != nil || record.Health != mcp.HealthUnavailable || record.Capabilities.Fingerprint != approved || record.ApprovedCapabilityFingerprint != approved {
		t.Fatal("failed refresh lost historical capability evidence", err)
	}
	reviewPlugin("enable")
	if _, _, code := executeTestCommand(t, "mcp", "client", "refresh", record.Descriptor.ID); code == 0 || requests.Load() != 2 {
		t.Fatal("new installation generation revived old review")
	}
	next := stage()
	if next.Descriptor.ID == record.Descriptor.ID || next.State != mcp.TrustStaged || next.Descriptor.NativeSource.InstallationGeneration != installed.Generation || requests.Load() != 2 {
		t.Fatal("reenablement did not require fresh staging/review")
	}
}
