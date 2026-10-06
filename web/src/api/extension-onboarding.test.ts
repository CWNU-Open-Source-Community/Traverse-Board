import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, expect, it, vi } from "vitest";
import { APIClient } from "./client";
import { fingerprint, lspConfiguration, lspTest, mcpServer, plugin } from "../test/extension-onboarding-fixtures";
import type { ExtensionMCPRegistrationRequestView } from "./types";

afterEach(() => vi.unstubAllGlobals());
function envelope(data: unknown) { return { version: "api.v1", request_id: "onboarding-test", data }; }
function respond(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } }); }
function realFixture(name: string, fallback: unknown) {
  const directory = process.env.UC_EXTENSION_ONBOARDING_EVIDENCE;
  return directory ? JSON.parse(readFileSync(join(directory, name), "utf8")) : envelope(fallback);
}
function mcpRequest(server = mcpServer()): ExtensionMCPRegistrationRequestView {
  return { version: "extension-control.v1", descriptor: { protocol_version: "mcp-client.v1", id: server.id, name: server.name,
    transport: server.transport, target: server.target, declared_capabilities: server.declared_capabilities, scope: server.scope,
    workspace_id: server.workspace_id, ...(server.run_id ? { run_id: server.run_id } : {}), call_timeout_ms: 30_000, max_result_bytes: 65_536 } };
}

it("decodes the actual registration/import envelopes without granting review or invocation", async () => {
  const registered = realFixture("extension-mcp-registration.json", { protocol_version: "extension-onboarding.v1", replayed: false, next_step: "approve_discovery", server: mcpServer() });
  const imported = realFixture("extension-plugin-import.json", { protocol_version: "extension-onboarding.v1", replayed: false, next_step: "approve", installation: plugin() });
  const fetchMock = vi.fn().mockResolvedValueOnce(respond(registered)).mockResolvedValueOnce(respond(imported));
  vi.stubGlobal("fetch", fetchMock);
  const client = new APIClient("read", "/api/v1", "control");
  expect((await client.registerMCPServer(mcpRequest(registered.data.server))).server.state).toBe("staged");
  expect((await client.importPluginPackage({ version: "extension-control.v1", archive_base64: btoa("zip"),
    archive_sha256: imported.data.installation.archive_sha256 })).installation.enabled_capabilities).toEqual([]);
  expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(["/api/v1/extensions/mcp", "/api/v1/extensions/plugins/import"]);
});

it("reads real completed MCP audits and keeps workspace inventory separate from Run results", async () => {
  const call = { id: "call-one", run_id: "run-one", workspace_id: "project-one", server_id: "first-mcp", tool_name: "lookup",
    capability_fingerprint: "b".repeat(64), arguments_sha256: fingerprint, status: "completed", result_bytes: 65,
    truncated: false, started_at: "2026-10-07T01:00:00Z", completed_at: "2026-10-07T01:00:01Z" };
  const invoked = realFixture("extension-mcp-invocation-ask.json", { protocol_version: "extension-inventory.v1", run_id: "run-one",
    workspace_id: "project-one", mcp_servers: [mcpServer("enabled")], plugins: [], mcp_calls: [call] });
  const workspace = realFixture("extension-workspace-inventory.json", { protocol_version: "extension-inventory.v1", workspace_id: "project-one",
    mcp_servers: [mcpServer()], plugins: [], mcp_calls: [] });
  const fetchMock = vi.fn().mockResolvedValueOnce(respond(invoked)).mockResolvedValueOnce(respond(workspace));
  vi.stubGlobal("fetch", fetchMock);
  const client = new APIClient("read");
  const actual = await client.extensionInventory(invoked.data.run_id);
  expect(actual.mcp_calls).toHaveLength(1);
  expect(actual.mcp_calls[0]).toMatchObject({ status: "completed", result_bytes: 65 });
  expect((await client.extensionInventory("", undefined, workspace.data.workspace_id)).mcp_calls).toEqual([]);
  expect(fetchMock.mock.calls[1][0]).toContain(`workspace_id=${workspace.data.workspace_id}`);
});

it.each(["scope", "authority", "onboarding"])("rejects unsafe registration or capability projection: %s", async (kind) => {
  const value = { protocol_version: "extension-onboarding.v1", replayed: false, next_step: "approve_discovery", server: mcpServer() };
  if (kind === "scope") value.server.workspace_id = "other-project";
  if (kind === "authority") Object.assign(value.server, { arguments: ["private"] });
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(respond(envelope(kind === "onboarding" ? {
    protocol_version: "extension-inventory.v1", mcp_servers: [], plugins: [], mcp_calls: [], onboarding: { mcp_registration: "true" },
  } : value))));
  const client = new APIClient("read", "/api/v1", "control");
  await expect(kind === "onboarding" ? client.extensionInventory() : client.registerMCPServer(mcpRequest())).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
});

it("retains old inventories without onboarding controls and rejects noncanonical ZIP input before fetch", async () => {
  const fetchMock = vi.fn().mockResolvedValue(respond(envelope({ protocol_version: "extension-inventory.v1", mcp_servers: [], plugins: [], mcp_calls: [] })));
  vi.stubGlobal("fetch", fetchMock);
  const client = new APIClient("read", "/api/v1", "control");
  expect((await client.extensionInventory()).onboarding).toBeUndefined();
  await expect(client.importPluginPackage({ version: "extension-control.v1", archive_sha256: fingerprint, archive_base64: "a" })).rejects.toThrow("bounded Plugin ZIP");
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

it.each(["run", "workspace", "unselected calls", "unknown status"])("rejects unbound inventory or unknown invocation outcome: %s", async (kind) => {
  const call = { id: "call-one", run_id: "run-one", workspace_id: "project-one", server_id: "first-mcp", tool_name: "lookup",
    capability_fingerprint: "b".repeat(64), arguments_sha256: fingerprint, status: "completed", result_bytes: 65,
    truncated: false, started_at: "2026-10-07T01:00:00Z", completed_at: "2026-10-07T01:00:01Z" };
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(respond(envelope({ protocol_version: "extension-inventory.v1",
    run_id: kind === "run" ? "other-run" : "run-one", workspace_id: "project-one", mcp_servers: [], plugins: [],
    mcp_calls: [kind === "unknown status" ? { ...call, status: "ready" } : call] }))));
  await expect(new APIClient("read").extensionInventory(kind === "unselected calls" ? "" : "run-one", undefined,
    kind === "workspace" ? "other-project" : "")).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
});

it("binds staged/reviewed LSP metadata and actual readonly evidence to the workspace and descriptor", async () => {
  const stagedEnvelope = realFixture("lsp/stage.json", lspConfiguration());
  const reviewedEnvelope = realFixture("lsp/review.json", { ...lspConfiguration(true), descriptor_fingerprint: "7".repeat(64) });
  const testedEnvelope = realFixture("lsp/test.json", { ...lspTest(), configuration: reviewedEnvelope.data,
    server: { ...lspTest().server, descriptor_fingerprint: reviewedEnvelope.data.descriptor_fingerprint } });
  const stage = stagedEnvelope.data;
  const tested = testedEnvelope.data;
  vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(respond(stagedEnvelope))
    .mockResolvedValueOnce(respond(reviewedEnvelope)).mockResolvedValueOnce(respond(testedEnvelope)));
  const client = new APIClient("read", "/api/v1", "control");
  expect((await client.stageCodeIntelConfiguration({ version: "code-intel-configuration.v1", server_id: stage.server_id,
    name: stage.server_name, workspace_id: stage.workspace_id, executable: "C:\\tools\\server.exe", executable_sha256: stage.executable_sha256,
    languages: stage.languages, arguments: [], request_timeout_ms: 30_000 })).review_state).toBe("pending_review");
  const body = { version: "code-intel-configuration.v1", workspace_id: stage.workspace_id, expected_descriptor_fingerprint: stage.descriptor_fingerprint };
  expect((await client.reviewCodeIntelConfiguration(stage.server_id, body)).review_state).toBe("reviewed");
  expect((await client.testCodeIntelConfiguration(stage.server_id, { ...body,
    expected_descriptor_fingerprint: reviewedEnvelope.data.descriptor_fingerprint,
    tool: "code_document_symbols", path: tested.result.document_path })).result.items[0].name).toBe(tested.result.items[0].name);
});

it.each(["workspace", "generation", "path", "private", "counts"])("rejects LSP probe evidence drift or private output: %s", async (kind) => {
  const tested = lspTest();
  if (kind === "workspace") tested.result.workspace_id = "another-project";
  if (kind === "generation") tested.result.server_generation = "9".repeat(64);
  if (kind === "path") tested.result.items[0].path = "C:/private/main.ts";
  if (kind === "private") Object.assign(tested.result, { stdout: "private" });
  if (kind === "counts") tested.result.page.returned = 2;
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(respond(envelope(tested))));
  await expect(new APIClient("read", "/api/v1", "control").testCodeIntelConfiguration("first-lsp", {
    version: "code-intel-configuration.v1", workspace_id: "project-one", expected_descriptor_fingerprint: "d".repeat(64),
    tool: "code_document_symbols", path: "src/main.ts",
  })).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
});

it("decodes reviewed and pending replacement configurations without hiding the previous review", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(respond(envelope({ protocol_version: "code-intel-lsp.v1", enabled: true,
    qualifications: [], servers: [], configurations: [lspConfiguration(true), { ...lspConfiguration(), descriptor_fingerprint: "6".repeat(64) }] }))));
  expect((await new APIClient("read").codeIntelInventory("project-one")).configurations).toHaveLength(2);
});
