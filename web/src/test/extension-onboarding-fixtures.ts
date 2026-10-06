import type { CodeIntelConfigurationTestView, CodeIntelConfigurationView, CodeIntelServerView,
  ExtensionMCPServerView, ExtensionPluginInstallationView } from "../api/types";

export const fingerprint = "a".repeat(64);
export function mcpServer(state = "staged"): ExtensionMCPServerView {
  return { protocol_version: "mcp-client-server.v1", id: "first-mcp", name: "First MCP",
    transport: "streamable_http", target: "https://example.invalid/mcp", declared_capabilities: ["tools"],
    scope: "workspace", workspace_id: "project-one", source: { kind: "manual", uri: "http-upload" },
    descriptor_fingerprint: fingerprint, state, health: state === "enabled" ? "healthy" : "unknown", generation: 1,
    capabilities: { negotiated: state === "staged" ? [] : ["tools"], tools: state === "staged" ? [] : ["lookup"],
      resources: [], prompts: [], ...(state === "staged" ? {} : { fingerprint: "b".repeat(64) }) },
    created_at: "2026-10-07T01:00:00Z", updated_at: "2026-10-07T01:00:00Z" };
}
export function plugin(state = "staged"): ExtensionPluginInstallationView {
  return { protocol_version: "plugin-installation.v1", id: "plugin-one",
    manifest: { id: "first-plugin", name: "First Plugin", version: "1.0.0", publisher: "operator",
      description: "A fixture", capabilities: ["hooks"] }, source: { kind: "upload", uri: `sha256:${fingerprint}` },
    archive_sha256: fingerprint, package_fingerprint: "c".repeat(64), signature_present: false, signature_valid: false,
    state, enabled_capabilities: state === "enabled" ? ["hooks"] : [], generation: 1, staged_by: "operator",
    created_at: "2026-10-07T01:00:00Z", updated_at: "2026-10-07T01:00:00Z" };
}
export function lspConfiguration(reviewed = false): CodeIntelConfigurationView {
  return { protocol_version: "code-intel-configuration.v1", server_id: "first-lsp", server_name: "First LSP",
    workspace_id: "project-one", scope: "workspace", languages: [{ id: "typescript", extensions: [".ts"] }],
    executable_sha256: fingerprint, descriptor_fingerprint: "d".repeat(64), review_state: reviewed ? "reviewed" : "pending_review",
    source_kind: "operator_config", source_label: "managed-settings", source_sha256: "e".repeat(64),
    ...(reviewed ? { reviewed_by: "operator", reviewed_at: "2026-10-07T01:00:00Z" } : {}) };
}
export function lspServer(): CodeIntelServerView {
  return { protocol_version: "code-intel-lsp.v1", server_id: "first-lsp", server_name: "First LSP", workspace_id: "project-one",
    languages: ["typescript"], source_kind: "operator_config", source_label: "managed-settings", source_sha256: "e".repeat(64),
    descriptor_fingerprint: "d".repeat(64), capability_fingerprint: "f".repeat(64), generation: "1".repeat(64),
    health: "healthy", capabilities: { workspace_symbols: true, document_symbols: true, definition: false, references: false,
      implementation: false, hover: false, signature_help: false, diagnostics: false, call_hierarchy: false, type_hierarchy: false },
    model_visible_tools: ["code_workspace_symbols", "code_document_symbols"], process_owned: true, read_only: true,
    network_access_granted: false, credentials_granted: false, shell_profile_loaded: false, qualified_at: "2026-10-07T01:00:00Z" };
}
export function lspTest(): CodeIntelConfigurationTestView {
  return { protocol_version: "code-intel-configuration.v1", configuration: lspConfiguration(true), server: lspServer(),
    result: { protocol_version: "code-intel-lsp.v1", tool: "code_document_symbols", state: "current",
      evidence_level: "semantic_language_server", workspace_id: "project-one", server_id: "first-lsp",
      server_generation: "1".repeat(64), capability_fingerprint: "f".repeat(64), query_fingerprint: "2".repeat(64),
      document_path: "src/main.ts", document_sha256: "3".repeat(64), items: [{ kind: "symbol", name: "main", path: "src/main.ts",
        range: { start: { line: 0, character: 0 }, end: { line: 0, character: 4 } } }],
      page: { limit: 200, returned: 1, total: 1, truncated: false }, warnings: [] } };
}
