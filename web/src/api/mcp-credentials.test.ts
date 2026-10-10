import { afterEach, expect, it, vi } from "vitest";
import { APIClient } from "./client";
import { credentialBinding, credentialStatus } from "../test/mcp-credential-fixtures";

afterEach(() => vi.unstubAllGlobals());
const respond = (data: unknown) => new Response(JSON.stringify({ version: "api.v1", request_id: "credential-test", data }),
  { status: 200, headers: { "Content-Type": "application/json" } });

it("reads exact descriptor presence with the read token and writes only through control", async () => {
  const binding = credentialBinding();
  const fetchMock = vi.fn().mockResolvedValueOnce(respond(credentialStatus())).mockResolvedValueOnce(respond(credentialStatus(undefined, true)))
    .mockResolvedValueOnce(respond(credentialStatus()));
  vi.stubGlobal("fetch", fetchMock);
  const client = new APIClient("read", "/api/v1", "control");
  const status = await client.mcpCredentialStatus(binding);
  await client.changeMCPCredential({ version: "mcp-credential.v1", binding, action: "set", secret: "synthetic-bearer-token",
    confirm: true, expected_reference_fingerprint: status.reference_fingerprint });
  await client.changeMCPCredential({ version: "mcp-credential.v1", binding, action: "delete", confirm: true,
    expected_reference_fingerprint: status.reference_fingerprint });
  expect(fetchMock.mock.calls[0][0]).toContain("/extensions/mcp/first-mcp/credential?");
  expect(fetchMock.mock.calls[0][0]).not.toContain("synthetic-bearer-token");
  expect(fetchMock.mock.calls[0][1].headers.Authorization).toBe("Bearer read");
  expect(fetchMock.mock.calls[1][1].headers.Authorization).toBe("Bearer control");
  expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toMatchObject({ binding, action: "set", secret: "synthetic-bearer-token" });
  expect(JSON.parse(fetchMock.mock.calls[2][1].body)).not.toHaveProperty("secret");
});

it.each(["secret", "plaintext", "endpoint", "workspace", "run", "reference", "descriptor", "unavailable-presence"])(
  "rejects leaked or incorrectly bound credential presence: %s", async (kind) => {
    const value = credentialStatus();
    if (kind === "secret") Object.assign(value, { secret: "synthetic-bearer-token" });
    if (kind === "plaintext") Object.assign(value, { plaintext_returned: true });
    if (kind === "endpoint") value.target = "https://other.invalid/mcp";
    if (kind === "workspace") value.workspace_id = "other";
    if (kind === "run") value.run_id = "other";
    if (kind === "reference") value.credential_ref = "other";
    if (kind === "descriptor") value.descriptor_fingerprint = "f".repeat(64);
    if (kind === "unavailable-presence") { value.store_available = false; value.configured = true; }
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(respond(value)));
    await expect(new APIClient("read").mcpCredentialStatus(credentialBinding())).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  });

it.each(["http", "userinfo", "query", "fragment", "invalid-name", "secret-length", "delete-secret", "no-confirm", "read-only"])(
  "rejects unsupported input before sending plaintext: %s", async (kind) => {
    const binding = credentialBinding();
    if (kind === "http") binding.target = "http://example.invalid/mcp";
    if (kind === "userinfo") binding.target = "https://user:secret@example.invalid/mcp";
    if (kind === "query") binding.target += "?secret=x";
    if (kind === "fragment") binding.target += "#secret";
    if (kind === "invalid-name") binding.credential_ref = "unsafe ref";
    const fetchMock = vi.fn(); vi.stubGlobal("fetch", fetchMock);
    await expect(new APIClient("read", "/api/v1", kind === "read-only" ? "" : "control").changeMCPCredential({
      version: "mcp-credential.v1", binding, action: kind === "delete-secret" ? "delete" : "set", confirm: kind !== "no-confirm",
      secret: kind === "secret-length" ? "x".repeat(2561) : "synthetic-bearer-token", expected_reference_fingerprint: "e".repeat(64),
    })).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });

it("does not report a stored token when mutation readback says absent or sharing drifted", async () => {
  const binding = credentialBinding();
  for (const response of [credentialStatus(), { ...credentialStatus(undefined, true), reference_fingerprint: "f".repeat(64) }]) {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(respond(response)));
    await expect(new APIClient("read", "/api/v1", "control").changeMCPCredential({ version: "mcp-credential.v1", binding,
      action: "set", secret: "synthetic-bearer-token", confirm: true, expected_reference_fingerprint: "e".repeat(64),
    })).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  }
});
