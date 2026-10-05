import { readFileSync } from "node:fs";
import { afterEach, expect, it, vi } from "vitest";
import { APIClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

function fixture() {
  // A cross-language acceptance run sets this to the exact envelope emitted by
  // TestExtensionNativeMCPInventoryFromPersistedRegistration. Ordinary frontend
  // runs retain a small deterministic specimen of the same public projection.
  const path = process.env.TRAVERSE_TEST_NATIVE_INVENTORY_OUTPUT;
  if (path) return JSON.parse(readFileSync(path, "utf8"));
  const legacy = { protocol_version: "mcp-client-server.v1", id: "legacy-inventory", name: "Legacy server",
    transport: "streamable_http", target: "https://legacy.invalid/mcp", declared_capabilities: ["tools"],
    scope: "workspace", workspace_id: "workspace-inventory", source: { kind: "manual", uri: "operator" },
    descriptor_fingerprint: "a".repeat(64), state: "staged", health: "unknown", generation: 1,
    capabilities: { negotiated: [], tools: [], resources: [], prompts: [] },
    created_at: "2026-10-02T01:00:00Z", updated_at: "2026-10-02T01:00:00Z" };
  return { version: "api.v1", request_id: "native-inventory", data: { protocol_version: "extension-inventory.v1",
    mcp_calls: [], plugins: [], mcp_servers: [legacy, { ...legacy, id: "native-inventory", target: "",
      native_source: { installation_id: "native-installation", package_id: "native-package", component_id: "inventory-peer",
        revision: "b".repeat(64), installation_generation: 3, surface: "code" } }] } };
}

function response(value: unknown) {
  return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } });
}

it("parses a mixed legacy/native HTTP inventory and its existing pinned disable response", async () => {
  const envelope = fixture();
  const native = envelope.data.mcp_servers.find((item: { target: string }) => item.target === "");
  expect(native).toBeDefined();
  const path = process.env.TRAVERSE_TEST_NATIVE_INVENTORY_OUTPUT;
  const reviewed = path ? JSON.parse(readFileSync(`${path}.review.json`, "utf8"))
    : { ...envelope, data: { ...native, state: "disabled", generation: native.generation + 1 } };
  const fetchMock = vi.fn().mockResolvedValueOnce(response(envelope))
    .mockResolvedValueOnce(response(reviewed));
  vi.stubGlobal("fetch", fetchMock);
  const client = new APIClient("read", "/api/v1", "control", { extensionControlEnabled: true });
  await expect(client.extensionInventory(envelope.data.run_id)).resolves.toMatchObject({ mcp_servers: envelope.data.mcp_servers });
  await expect(client.reviewMCPServer(native.id, { version: "extension-control.v1", action: "disable",
    expected_descriptor_fingerprint: native.descriptor_fingerprint })).resolves.toMatchObject({ target: "",
    native_source: native.native_source, state: "disabled" });
  expect(JSON.parse(String(fetchMock.mock.calls[1][1].body))).toEqual({ version: "extension-control.v1",
    action: "disable", expected_descriptor_fingerprint: native.descriptor_fingerprint });
});

it.each(["missing source", "missing component", "invalid revision", "invalid generation", "invalid surface", "legacy target", "legacy credential"])(
  "rejects an ambiguous or invalid native projection: %s", async (failure) => {
    const envelope = fixture();
    const native = envelope.data.mcp_servers.find((item: { target: string }) => item.target === "");
    if (failure === "missing source") delete native.native_source;
    if (failure === "missing component") delete native.native_source.component_id;
    if (failure === "invalid revision") native.native_source.revision = "not-a-revision";
    if (failure === "invalid generation") native.native_source.installation_generation = 0;
    if (failure === "invalid surface") native.native_source.surface = "any";
    if (failure === "legacy target") native.target = "https://ambiguous.invalid";
    if (failure === "legacy credential") native.credential_ref = "ambiguous-credential";
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(envelope)));
    await expect(new APIClient("read").extensionInventory()).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  });
