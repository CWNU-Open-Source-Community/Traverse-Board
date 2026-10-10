import type { MCPCredentialBindingView, MCPCredentialStatusView } from "../api/types";
import { mcpServer } from "./extension-onboarding-fixtures";

export function credentialServer() { return { ...mcpServer(), credential_ref: "mcp-fixture" }; }
export function credentialBinding(server = credentialServer()): MCPCredentialBindingView {
  return { server_id: server.id, workspace_id: server.workspace_id, ...(server.run_id ? { run_id: server.run_id } : {}),
    target: server.target, credential_ref: server.credential_ref, expected_descriptor_fingerprint: server.descriptor_fingerprint };
}
export function credentialStatus(server = credentialServer(), configured = false): MCPCredentialStatusView {
  return { protocol_version: "mcp-credential.v1", server_id: server.id, workspace_id: server.workspace_id,
    ...(server.run_id ? { run_id: server.run_id } : {}), descriptor_fingerprint: server.descriptor_fingerprint,
    target: server.target, credential_ref: server.credential_ref, configured, store_kind: "memory_test_only",
    store_available: true, plaintext_returned: false, registration_count: 1, reference_fingerprint: "e".repeat(64), endpoint_conflict: false };
}
