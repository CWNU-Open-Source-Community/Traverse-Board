package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/skills"
)

// A real import and SQLite registration must remain manageable through the
// existing HTTP inventory beside a legacy registration, without exposing the
// native launch configuration. The optional output feeds the TypeScript client
// contract test with this exact HTTP envelope, not a hand-authored substitute.
func TestExtensionNativeMCPInventoryFromPersistedRegistration(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	directory := t.TempDir()
	for name, raw := range map[string][]byte{
		"plugin.json": []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"native-inventory"}`),
		"mcp.json":    []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"inventory-peer":{"type":"streamable-http","url":"https://native-inventory.invalid/mcp","headers":{"X-Private":"inventory-credential-canary"}}}}`),
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
	catalog := application.NewSkillCatalogService(f.store, application.NewSkillPackageRegistryService(f.store, objects, builtins))
	imported, err := catalog.ImportFromDirectory(ctx, application.ImportSkillFromDirectoryRequest{Directory: directory,
		Surface: domain.ExecutionSurfaceCode, OperationKey: "native-inventory-import", InstalledBy: "operator", ConfirmUntrusted: true})
	if err != nil || imported.Portable == nil {
		t.Fatal("native inventory import failed", err)
	}
	installed := *imported.Portable
	service, _ := plugins.NewService(f.store)
	for _, action := range []plugins.ReviewAction{plugins.ReviewApprove, plugins.ReviewEnable} {
		installed, err = service.Review(ctx, installed.ID, plugins.ReviewRequest{Action: action,
			ExpectedPackageFingerprint: installed.PackageFingerprint, ExpectedGeneration: installed.Generation,
			Capabilities: []plugins.Capability{plugins.CapabilityMCP}, ConfirmUntrusted: true, ReviewedBy: "operator"})
		if err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := application.NewNativeMCPSourceResolver(f.store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := mcp.NewClientManager(f.store, nil, mcp.ManagerOptions{NativeSources: resolver})
	if err != nil {
		t.Fatal(err)
	}
	records, err := service.StageMCPServers(ctx, installed.ID, mcp.ScopeWorkspace, "", f.workspace.ID, manager)
	if err != nil || len(records) != 1 {
		t.Fatal("native inventory staging failed", err)
	}
	native := records[0]
	legacy := extensionTestMCPServer("legacy-inventory").Descriptor
	legacy.WorkspaceID = f.workspace.ID
	if _, _, err := manager.Stage(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	controller, err := application.NewExtensionControlService(f.store, manager, service)
	if err != nil {
		t.Fatal(err)
	}
	f.api, err = New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		ExtensionControlEnabled: true, ExtensionController: controller})
	if err != nil {
		t.Fatal(err)
	}
	response := f.get(t, ExtensionInventoryPath+"?run_id="+f.run.ID)
	var inventory ExtensionInventoryView
	decodeData(t, response, &inventory)
	if output := os.Getenv("TRAVERSE_TEST_NATIVE_INVENTORY_OUTPUT"); output != "" {
		if err := os.WriteFile(output, response.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if len(inventory.MCPServers) != 2 {
		t.Fatal("mixed inventory lost a registration", len(inventory.MCPServers))
	}
	for _, server := range inventory.MCPServers {
		if server.ProtocolVersion != mcp.ServerRecordProtocolVersion {
			t.Fatal("descriptor version incorrectly replaced the record protocol")
		}
		if server.ID == native.Descriptor.ID {
			raw, _ := json.Marshal(server)
			var projected struct {
				NativeSource *ExtensionMCPNativeSourceView `json:"native_source"`
			}
			source := native.Descriptor.NativeSource
			want := ExtensionMCPNativeSourceView{InstallationID: source.InstallationID,
				PackageID: source.Component.PackageID, ComponentID: source.Component.ComponentID,
				Revision: source.Revision, InstallationGeneration: source.InstallationGeneration, Surface: source.Surface}
			if err := json.Unmarshal(raw, &projected); err != nil || projected.NativeSource == nil ||
				*projected.NativeSource != want || server.Target != "" || server.CredentialRef != "" {
				t.Fatalf("native inventory lost its pinned source or invented a legacy target: %s, %v", raw, err)
			}
		} else if server.ID != legacy.ID || server.Target != legacy.Target || server.CredentialRef != legacy.CredentialRef {
			t.Fatal("legacy inventory changed", server)
		}
	}
	for _, secret := range []string{"inventory-credential-canary", "native-inventory.invalid", "X-Private", "arguments\""} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("inventory exposed native launch material", secret)
		}
	}
	body, _ := json.Marshal(ExtensionMCPReviewRequestView{Version: ExtensionControlProtocol,
		Action: mcp.ReviewDisable, ExpectedDescriptorFingerprint: native.DescriptorFingerprint})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/api/v1/extensions/mcp/"+native.Descriptor.ID+"/review", bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:45000"
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	request.Header.Set("Content-Type", "application/json")
	review := httptest.NewRecorder()
	f.api.ServeHTTP(review, request)
	if review.Code != http.StatusAccepted {
		t.Fatalf("existing HTTP native disable failed: %d %s", review.Code, review.Body.String())
	}
	var envelope apiTestEnvelope
	var disabled ExtensionMCPServerView
	if err := json.Unmarshal(review.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope.Data, &disabled); err != nil || disabled.State != string(mcp.TrustDisabled) ||
		disabled.NativeSource == nil || disabled.NativeSource.InstallationID != installed.ID ||
		disabled.DescriptorFingerprint != native.DescriptorFingerprint || disabled.Generation != native.Generation+1 {
		t.Fatal("HTTP disable lost the native binding", disabled, err)
	}
	stored, err := f.store.GetMCPClientServer(ctx, native.Descriptor.ID)
	if err != nil || stored.State != mcp.TrustDisabled || stored.Descriptor.NativeSource == nil ||
		*stored.Descriptor.NativeSource != *native.Descriptor.NativeSource {
		t.Fatal("HTTP disable did not preserve the durable native source", err)
	}
	if output := os.Getenv("TRAVERSE_TEST_NATIVE_INVENTORY_OUTPUT"); output != "" {
		if err := os.WriteFile(output+".review.json", review.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
