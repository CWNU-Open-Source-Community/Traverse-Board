package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/toolcontract"
)

func TestNativeMCPReferenceSurvivesSQLiteRestartBesideLegacy(t *testing.T) {
	ctx := t.Context()
	database := filepath.Join(t.TempDir(), "native-mcp.db")
	state, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	if err := state.SaveWorkspace(ctx, WorkspaceRecord{ID: "native-workspace", Name: "native", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 64)
	descriptors := []mcp.ServerDescriptor{{ProtocolVersion: mcp.ClientProtocolVersion, ID: "legacy", Name: "legacy", Transport: mcp.TransportStreamableHTTP,
		Target: "https://legacy.invalid/mcp", Scope: mcp.ScopeWorkspace, WorkspaceID: "native-workspace", Source: mcp.Source{Kind: "manual", URI: "operator"},
		DeclaredCapabilities: []mcp.CapabilityKind{mcp.CapabilityTools}, CallTimeoutMillis: 1000, MaxResultBytes: 4096},
		{ProtocolVersion: mcp.NativeClientProtocolVersion, ID: "native", Name: "native", Transport: mcp.TransportStreamableHTTP,
			Scope: mcp.ScopeWorkspace, WorkspaceID: "native-workspace", Source: mcp.Source{Kind: "plugin", URI: "test:original", PluginID: "native-package", SHA256: revision, Fingerprint: revision},
			NativeSource:         &mcp.NativeSourceRef{InstallationID: "native-installation", Component: toolcontract.ComponentRef{PackageID: "native-package", ComponentID: "mcp:peer"}, Revision: revision, InstallationGeneration: 3, Surface: "code"},
			DeclaredCapabilities: []mcp.CapabilityKind{mcp.CapabilityTools}, CallTimeoutMillis: 1000, MaxResultBytes: 4096}}
	for _, descriptor := range descriptors {
		now := time.Now().UTC()
		record := mcp.ServerRecord{ProtocolVersion: mcp.ServerRecordProtocolVersion, Descriptor: descriptor, DescriptorFingerprint: descriptor.Fingerprint(), State: mcp.TrustStaged,
			Health: mcp.HealthUnknown, Generation: 1, CreatedAt: now, UpdatedAt: now}
		if _, replayed, err := state.CreateMCPClientServer(ctx, record); err != nil || replayed {
			t.Fatal("store registration", replayed, err)
		}
		var locator string
		if err := state.db.QueryRowContext(ctx, `SELECT target FROM mcp_client_servers WHERE id = ?`, descriptor.ID).Scan(&locator); err != nil {
			t.Fatal(err)
		}
		want := descriptor.Target
		if descriptor.NativeSource != nil {
			want = "plugin-object:" + descriptor.Fingerprint()
		}
		if locator != want {
			t.Fatal("derived locator projected different source", locator)
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		record, err := state.GetMCPClientServer(ctx, descriptor.ID)
		before, _ := json.Marshal(descriptor)
		after, _ := json.Marshal(record.Descriptor)
		if err != nil || string(before) != string(after) || record.DescriptorFingerprint != descriptor.Fingerprint() {
			t.Fatal("registration changed across actual close/reopen", descriptor.ID, err)
		}
		if _, replayed, err := state.CreateMCPClientServer(ctx, record); err != nil || !replayed {
			t.Fatal("exact staging replay changed", replayed, err)
		}
		changed := record
		changed.Descriptor.Target = "https://replacement.invalid"
		changed.DescriptorFingerprint = changed.Descriptor.Fingerprint()
		changed.Generation++
		if _, err := state.UpdateMCPClientServer(ctx, changed, record.Generation); err == nil {
			t.Fatal("immutable descriptor replacement succeeded")
		}
	}
}
