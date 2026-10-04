import { afterEach, expect, it, vi } from "vitest";
import { APIClient } from "./client";

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
  const client = new APIClient("read", "/api/v1", "control", { extensionControlEnabled: true });
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
    await expect(new APIClient("read").extensionInventory()).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  });


it.each(["agent-skills", "agent-plugins", "traverse-skill"])("imports %s through the Plugin lifecycle", async (format) => {
  const plugin = { ...nativePlugin(), state: "staged", generation: 1, enabled_capabilities: [],
    snapshot: { ...nativePlugin().snapshot, format } };
  const result = { protocol_version: "plugin-installation.v2", installation: plugin, replayed: false };
  const fetchMock = vi.fn().mockResolvedValue(envelope(result));
  vi.stubGlobal("fetch", fetchMock);
  const client = new APIClient("read", "/api/v1", "control", { skillInstallationEnabled: true });
  await expect(client.installSkillPackage({ version: "plugin-installation.v2", archive_base64: "cGFja2FnZQ==",
    surface: "code", confirm_untrusted: true }, "plugin-install-operation")).resolves.toEqual(result);
});

it.each(["enabled without replay", "wrong surface", "invented receipt"])("rejects Plugin import drift: %s", async (failure) => {
  const plugin = { ...nativePlugin(), state: "staged", generation: 1, enabled_capabilities: [],
    snapshot: { ...nativePlugin().snapshot } };
  const result: Record<string, unknown> = { protocol_version: "plugin-installation.v2", installation: plugin, replayed: false };
  if (failure === "enabled without replay") plugin.state = "enabled";
  if (failure === "wrong surface") plugin.snapshot.surface = "cyber";
  if (failure === "invented receipt") result.receipt = { outcome: "installed" };
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(envelope(result)));
  const client = new APIClient("read", "/api/v1", "control", { skillInstallationEnabled: true });
  await expect(client.installSkillPackage({ version: "plugin-installation.v2", archive_base64: "cGFja2FnZQ==",
    surface: "code", confirm_untrusted: true }, "plugin-install-operation")).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
});

it("preserves a disabled Plugin replay without claiming a new activation", async () => {
  const installation = { ...nativePlugin(), state: "disabled", generation: 4, enabled_capabilities: [] };
  const result = { protocol_version: "plugin-installation.v2", installation, replayed: true };
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(envelope(result)));
  const client = new APIClient("read", "/api/v1", "control", { skillInstallationEnabled: true });
  await expect(client.installSkillPackage({ version: "skill_package_installation.v1", archive_base64: "cGFja2FnZQ==",
    surface: "code", confirm_untrusted: true }, "plugin-install-operation")).resolves.toEqual(result);
});


it("keeps historical recovery responses with their original mode metadata", async () => {
  const result = { protocol_version: "skill_package_installation.v1", name: "historical-skill", version: "1.0.0",
    surface: "code", profiles: ["code"], surfaces: ["code"], phases: ["plan"], roles: ["root"],
    user_invocable: true, model_invocable: false, explicit_only: true,
    trust_class: "operator_installed_untrusted", archive_sha256: "a".repeat(64), package_fingerprint: "b".repeat(64),
    replayed: true, recovered_pending: true, import_command_execution: false, import_network_access: false,
    import_provider_calls: false, tool_capability_grant: false, run_selection_authorized: false, context_injection_authorized: false,
    receipt: { protocol_version: "operation_receipt.v1", kind: "skill_package_install", outcome: "installed", durable: true,
      replayed: true, retry_safe: true, retry_strategy: "same_operation_key", recovery_action: "none", cleanup_state: "not_applicable" } };
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(envelope(result)));
  const client = new APIClient("read", "/api/v1", "control", { skillInstallationEnabled: true });
  await expect(client.installSkillPackage({ version: "skill_package_installation.v1", archive_base64: "cGFja2FnZQ==",
    surface: "code", confirm_untrusted: true }, "historical-import-operation")).resolves.toEqual(result);
});
