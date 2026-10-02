import { afterEach, expect, it, vi } from "vitest";
import { CyberAgentClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

function nativePlugin() {
  return { protocol_version: "plugin-installation.v2", id: "plugin-import-native",
    manifest: { id: "portable-native", name: "Native skill", version: "", publisher: "",
      description: "", capabilities: ["skills"] },
    snapshot: { format: "agent-skills", revision: "a".repeat(64), surface: "code" },
    source: { kind: "local_directory", uri: "C:\\plugins\\native", sha256: "a".repeat(64) },
    archive_sha256: "a".repeat(64), package_fingerprint: "b".repeat(64),
    signature_present: false, signature_valid: false, state: "enabled", enabled_capabilities: ["skills"],
    generation: 3, staged_by: "cli_operator", created_at: "2026-10-02T01:00:00Z", updated_at: "2026-10-02T01:01:00Z" };
}

function envelope(data: unknown) {
  return new Response(JSON.stringify({ version: "api.v1", request_id: "native-extension", data }),
    { status: 200, headers: { "Content-Type": "application/json" } });
}

it("reads native plugins with absent author/version and uses the existing pinned disable control", async () => {
  const plugin = nativePlugin();
  const fetchMock = vi.fn().mockResolvedValueOnce(envelope({ protocol_version: "extension-inventory.v1",
    mcp_servers: [], mcp_calls: [], plugins: [plugin] })).mockResolvedValueOnce(envelope({ ...plugin, state: "disabled", generation: 4 }));
  vi.stubGlobal("fetch", fetchMock);
  const client = new CyberAgentClient("read", "/api/v1", "control", { extensionControlEnabled: true });
  await expect(client.extensionInventory()).resolves.toMatchObject({ plugins: [plugin] });
  await expect(client.reviewPluginInstallation(plugin.id, { version: "extension-control.v1", action: "disable",
    expected_package_fingerprint: plugin.package_fingerprint, expected_generation: 3, confirm_untrusted: false,
  })).resolves.toMatchObject({ state: "disabled", generation: 4, manifest: { version: "", publisher: "" } });
  expect(JSON.parse(String(fetchMock.mock.calls[1][1].body))).toMatchObject({ expected_generation: 3, expected_package_fingerprint: plugin.package_fingerprint });
});

it.each(["missing snapshot", "changed revision", "unknown surface", "unsupported protocol"])(
  "rejects an invalid native plugin projection: %s", async (failure) => {
    const plugin: Record<string, unknown> = nativePlugin();
    if (failure === "missing snapshot") delete plugin.snapshot;
    if (failure === "changed revision") plugin.snapshot = { ...nativePlugin().snapshot, revision: "c".repeat(64) };
    if (failure === "unknown surface") plugin.snapshot = { ...nativePlugin().snapshot, surface: "any" };
    if (failure === "unsupported protocol") plugin.protocol_version = "unsupported";
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(envelope({ protocol_version: "extension-inventory.v1",
      mcp_servers: [], mcp_calls: [], plugins: [plugin] })));
    await expect(new CyberAgentClient("read").extensionInventory()).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  });
