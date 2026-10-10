package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/store"
)

func newMCPCredentialFixture(t *testing.T) (*apiFixture, MCPCredentialBindingView, MCPCredentialStatusView, *credential.MemoryStore) {
	t.Helper()
	f, controller, _, _ := newExtensionOnboardingFixture(t)
	owned := credential.NewMemoryStore()
	controller.WithCredentials(owned)
	request := extensionRegistrationRequest(f)
	request.Descriptor.ID = "credential-server"
	request.Descriptor.CredentialRef = "mcp-synthetic"
	var registered ExtensionMCPRegistrationView
	decodeDataStatus(t, extensionOnboardingRequest(t, f.api, ExtensionMCPRegistrationPath, request), http.StatusAccepted, &registered)
	server := registered.Server
	binding := MCPCredentialBindingView{ServerID: server.ID, WorkspaceID: server.WorkspaceID, RunID: server.RunID,
		ExpectedDescriptorFingerprint: server.DescriptorFingerprint, Target: server.Target, CredentialRef: server.CredentialRef}
	var status MCPCredentialStatusView
	decodeData(t, f.get(t, mcpCredentialStatusPath(binding)), &status)
	return f, binding, status, owned
}

func TestMCPCredentialRegistryCountsOtherWorkspacesAndRejectsStaleSharing(t *testing.T) {
	f, binding, status, owned := newMCPCredentialFixture(t)
	request := MCPCredentialRequestView{Version: application.MCPCredentialProtocolVersion, Binding: binding,
		Action: "set", Secret: "synthetic-shared-token", Confirm: true, ExpectedReferenceFingerprint: status.ReferenceFingerprint}
	path := "/api/v1/extensions/mcp/" + binding.ServerID + "/credential"
	if err := f.store.SaveWorkspace(t.Context(), store.WorkspaceRecord{ID: "credential-workspace-two", Name: "Credential second", RootPath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	second := extensionRegistrationRequest(f)
	second.Descriptor.ID, second.Descriptor.CredentialRef = "second-credential-server", binding.CredentialRef
	second.Descriptor.Scope, second.Descriptor.RunID, second.Descriptor.WorkspaceID = mcp.ScopeWorkspace, "", "credential-workspace-two"
	var registered ExtensionMCPRegistrationView
	decodeDataStatus(t, extensionOnboardingRequest(t, f.api, ExtensionMCPRegistrationPath, second), http.StatusAccepted, &registered)
	response := extensionOnboardingRequest(t, f.api, path, request)
	assertAPIError(t, response, http.StatusConflict, "CONFLICT")
	if present, _ := owned.Configured(t.Context(), binding.CredentialRef); present {
		t.Fatal("stale sharing acknowledgment wrote token")
	}
	decodeData(t, f.get(t, mcpCredentialStatusPath(binding)), &status)
	if status.RegistrationCount != 2 || status.EndpointConflict {
		t.Fatal("other Workspace reference was omitted")
	}
	request.ExpectedReferenceFingerprint = status.ReferenceFingerprint
	decodeData(t, extensionOnboardingRequest(t, f.api, path, request), &status)
	second.Descriptor.ID, second.Descriptor.Target = "third-credential-server", "https://different.invalid/mcp"
	decodeDataStatus(t, extensionOnboardingRequest(t, f.api, ExtensionMCPRegistrationPath, second), http.StatusAccepted, &registered)
	decodeData(t, f.get(t, mcpCredentialStatusPath(binding)), &status)
	if !status.EndpointConflict || status.RegistrationCount != 3 {
		t.Fatal("endpoint conflict was omitted")
	}
	request.Action, request.Secret, request.ExpectedReferenceFingerprint = "delete", "", status.ReferenceFingerprint
	assertAPIError(t, extensionOnboardingRequest(t, f.api, path, request), http.StatusConflict, "CONFLICT")
	if present, _ := owned.Configured(t.Context(), binding.CredentialRef); !present {
		t.Fatal("endpoint conflict deleted shared token")
	}
}

func mcpCredentialStatusPath(binding MCPCredentialBindingView) string {
	query := url.Values{"workspace_id": {binding.WorkspaceID}, "expected_descriptor_fingerprint": {binding.ExpectedDescriptorFingerprint},
		"target": {binding.Target}, "credential_ref": {binding.CredentialRef}}
	if binding.RunID != "" {
		query.Set("run_id", binding.RunID)
	}
	return "/api/v1/extensions/mcp/" + binding.ServerID + "/credential?" + query.Encode()
}

func TestMCPCredentialProductLifecycleAndSecretNonDisclosure(t *testing.T) {
	f, binding, status, owned := newMCPCredentialFixture(t)
	path := "/api/v1/extensions/mcp/" + binding.ServerID + "/credential"
	request := MCPCredentialRequestView{Version: application.MCPCredentialProtocolVersion, Binding: binding,
		Action: "set", Secret: "synthetic-first-secret", Confirm: true, ExpectedReferenceFingerprint: status.ReferenceFingerprint}
	for _, action := range []string{"set", "set", "delete"} {
		request.Action = action
		if action == "delete" {
			request.Secret = ""
		} else if status.Configured {
			request.Secret = "synthetic-updated-secret"
		}
		response := extensionOnboardingRequest(t, f.api, path, request)
		decodeData(t, response, &status)
		assertSecurityHeaders(t, response)
		if strings.Contains(response.Body.String(), "synthetic-first-secret") || strings.Contains(response.Body.String(), "synthetic-updated-secret") || status.PlaintextReturned {
			t.Fatal("secret entered public response")
		}
		if status.Configured != (action == "set") {
			t.Fatal("presence did not reflect mutation")
		}
		var readback MCPCredentialStatusView
		decodeData(t, f.get(t, mcpCredentialStatusPath(binding)), &readback)
		if readback.Configured != status.Configured || readback.ReferenceFingerprint != status.ReferenceFingerprint {
			t.Fatal("presence readback differs")
		}
		record, err := f.store.GetMCPClientServer(t.Context(), binding.ServerID)
		if err != nil || record.State != mcp.TrustStaged {
			t.Fatal("credential mutation altered server review")
		}
		calls, err := f.store.ListMCPClientCalls(t.Context(), f.run.ID, 100)
		if err != nil || len(calls) != 0 {
			t.Fatal("credential mutation invoked MCP")
		}
		raw, _ := json.Marshal(record)
		if strings.Contains(string(raw), "synthetic-first-secret") || strings.Contains(string(raw), "synthetic-updated-secret") {
			t.Fatal("secret entered durable server metadata")
		}
	}
	if found, _ := owned.Configured(t.Context(), binding.CredentialRef); found {
		t.Fatal("delete did not remove test-store credential")
	}
}

func TestMCPCredentialControlRequiresAuthorityStrictBodyAndBinding(t *testing.T) {
	f, binding, status, owned := newMCPCredentialFixture(t)
	path := "/api/v1/extensions/mcp/" + binding.ServerID + "/credential"
	base := MCPCredentialRequestView{Version: application.MCPCredentialProtocolVersion, Binding: binding, Action: "set",
		Secret: "synthetic-never-written", Confirm: true, ExpectedReferenceFingerprint: status.ReferenceFingerprint}
	for _, kind := range []string{"read-only", "disabled", "method", "unknown-field", "duplicate-secret", "workspace", "target", "reference", "confirm", "stale-references", "path-binding"} {
		t.Run(kind, func(t *testing.T) {
			request := base
			want := http.StatusBadRequest
			method, token := http.MethodPost, testControlToken
			f.api.extensionControlEnabled = true
			switch kind {
			case "read-only":
				token, want = testAccessToken, http.StatusUnauthorized
			case "disabled":
				f.api.extensionControlEnabled, want = false, http.StatusNotFound
			case "method":
				method, want = http.MethodDelete, http.StatusMethodNotAllowed
			case "workspace":
				request.Binding.WorkspaceID, want = "other", http.StatusConflict
			case "target":
				request.Binding.Target, want = "https://other.invalid/mcp", http.StatusConflict
			case "reference":
				request.Binding.CredentialRef, want = "other", http.StatusConflict
			case "confirm":
				request.Confirm = false
			case "stale-references":
				request.ExpectedReferenceFingerprint, want = strings.Repeat("a", 64), http.StatusConflict
			case "path-binding":
				request.Binding.ServerID = "other"
			}
			raw, _ := json.Marshal(request)
			body := string(raw)
			if kind == "unknown-field" {
				body = strings.TrimSuffix(body, "}") + `,"plaintext":"synthetic-never-written"}`
			}
			if kind == "duplicate-secret" {
				body = strings.TrimSuffix(body, "}") + `,"secret":"synthetic-never-written"}`
			}
			var r *httptest.ResponseRecorder
			if token == testControlToken {
				r = performControlMethodPathRequest(t, f.api, method, path, "", strings.NewReader(body))
			} else {
				r = performRequest(t, f.api, method, path, token, "127.0.0.1:8765", "127.0.0.1:45000", strings.NewReader(body))
			}
			if r.Code != want {
				t.Fatalf("status=%d want=%d body=%s", r.Code, want, r.Body.String())
			}
			if strings.Contains(r.Body.String(), "synthetic-never-written") {
				t.Fatal("rejected plaintext leaked")
			}
			if found, _ := owned.Configured(t.Context(), binding.CredentialRef); found {
				t.Fatal("rejected request touched credential store")
			}
		})
	}
	f.api.extensionControlEnabled = false
	decodeData(t, f.get(t, mcpCredentialStatusPath(binding)), &status)
	for _, suffix := range []string{"&unknown=x", "&credential_ref=other", "&workspace_id=other"} {
		response := f.get(t, mcpCredentialStatusPath(binding)+suffix)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("ambiguous query accepted: %s", suffix)
		}
	}
}
