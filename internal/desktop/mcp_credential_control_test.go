package desktop

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/store"
)

func TestDesktopMCPCredentialUsesSharedAPIAndInjectedStore(t *testing.T) {
	owned := credential.NewMemoryStore()
	plane, err := OpenControlPlane(ControlPlaneConfig{DatabasePath: filepath.Join(t.TempDir(), "credentials.db"), HomePath: t.TempDir(),
		ReadToken: desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken, CredentialStore: owned})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plane.Close() })
	workspace := store.WorkspaceRecord{ID: "desktop-credential-workspace", Name: "Credential fixture", RootPath: t.TempDir()}
	if err := plane.stateStore.SaveWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	request := httpapi.ExtensionMCPRegistrationRequestView{Version: httpapi.ExtensionControlProtocol,
		Descriptor: httpapi.ExtensionMCPRegistrationDescriptorView{ProtocolVersion: mcp.ClientProtocolVersion,
			ID: "desktop-credential-server", Name: "Desktop fixture", Transport: mcp.TransportStreamableHTTP,
			Target: "https://desktop-fixture.invalid/mcp", CredentialRef: "mcp-desktop-fixture", Scope: mcp.ScopeWorkspace,
			WorkspaceID: workspace.ID, DeclaredCapabilities: []mcp.CapabilityKind{mcp.CapabilityTools}, CallTimeoutMillis: 3000, MaxResultBytes: 8192}}
	raw, _ := json.Marshal(request)
	response := desktopControlRequest(plane.Handler(), http.MethodPost, httpapi.ExtensionMCPRegistrationPath, "", string(raw))
	if response.Code != http.StatusAccepted {
		t.Fatalf("registration status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope desktopAPIEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var registration httpapi.ExtensionMCPRegistrationView
	if err := json.Unmarshal(envelope.Data, &registration); err != nil {
		t.Fatal(err)
	}
	server := registration.Server
	binding := httpapi.MCPCredentialBindingView{ServerID: server.ID, WorkspaceID: server.WorkspaceID,
		ExpectedDescriptorFingerprint: server.DescriptorFingerprint, Target: server.Target, CredentialRef: server.CredentialRef}
	query := url.Values{"workspace_id": {binding.WorkspaceID}, "expected_descriptor_fingerprint": {binding.ExpectedDescriptorFingerprint},
		"target": {binding.Target}, "credential_ref": {binding.CredentialRef}}
	path := "/api/v1/extensions/mcp/" + server.ID + "/credential"
	response = desktopAPIRequest(plane.Handler(), path+"?"+query.Encode())
	if response.Code != http.StatusOK {
		t.Fatalf("presence status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var status httpapi.MCPCredentialStatusView
	if err := json.Unmarshal(envelope.Data, &status); err != nil {
		t.Fatal(err)
	}
	if status.Configured || status.StoreKind != "memory_test_only" {
		t.Fatal("Desktop did not use isolated injected store")
	}
	change := httpapi.MCPCredentialRequestView{Version: application.MCPCredentialProtocolVersion, Binding: binding,
		Action: "set", Secret: "synthetic-desktop-token", Confirm: true, ExpectedReferenceFingerprint: status.ReferenceFingerprint}
	raw, _ = json.Marshal(change)
	response = desktopControlRequest(plane.Handler(), http.MethodPost, path, "", string(raw))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "synthetic-desktop-token") {
		t.Fatalf("unsafe Desktop mutation response: %d %s", response.Code, response.Body.String())
	}
	if present, _ := owned.Configured(t.Context(), binding.CredentialRef); !present {
		t.Fatal("Desktop did not write through shared store")
	}
	record, err := plane.stateStore.GetMCPClientServer(t.Context(), server.ID)
	if err != nil || record.State != mcp.TrustStaged {
		t.Fatal("Desktop credential entry changed MCP authority")
	}
}
