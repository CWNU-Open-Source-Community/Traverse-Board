import { APIClient } from "./client";

function environment() { return { protocol_version: "docker_environment.v1", feature_enabled: true,
  image_configured: true, image_digest: `sha256:${"a".repeat(64)}`, restart_required: false,
  readiness: { protocol_version: "sandbox.readiness.v1", status: "ready", ready: true,
    feature_enabled: true, daemon_reachable: true, image_inspected: true, image_profile_safe: true,
    network_mode: "disabled", reason_code: "none", remediation_code: "none", checked_at: "2026-10-10T00:00:00Z",
    expires_at: "2026-10-10T00:00:30Z", endpoint_class: "local_unix", endpoint_fingerprint: "b".repeat(64), readiness_fingerprint: "c".repeat(64) } }; }
function respond(value: unknown) { vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ version: "api.v1", request_id: "request-1", data: value }), { status: 200, headers: { "Content-Type": "application/json" } }))); }
afterEach(() => vi.unstubAllGlobals());
it("reads the fixed Docker environment with read authority and no renderer configuration", async () => {
  const value = environment(); respond(value);
  expect(await new APIClient("read-token").getDockerEnvironment()).toEqual(value);
  expect(fetch).toHaveBeenCalledWith(expect.stringContaining("/sandbox/docker/environment"), expect.objectContaining({ headers: expect.objectContaining({ Authorization: "Bearer read-token" }) }));
  expect((vi.mocked(fetch).mock.calls[0]![1] as RequestInit).body).toBeUndefined();
});
it.each([
  (value: ReturnType<typeof environment>) => { value.image_digest = "latest"; },
  (value: ReturnType<typeof environment>) => { value.readiness.daemon_reachable = false; },
  (value: ReturnType<typeof environment>) => { value.readiness.feature_enabled = false; },
  (value: ReturnType<typeof environment>) => { value.readiness.endpoint_class = "remote"; },
  (value: ReturnType<typeof environment>) => { value.readiness.expires_at = "2026-10-10T00:01:00Z"; },
  (value: ReturnType<typeof environment>) => { Object.assign(value.readiness, { daemon_endpoint: "other" }); },
])("rejects unpinned, inconsistent or widened observations", async (mutate) => {
  const value = environment(); mutate(value); respond(value);
  await expect(new APIClient("read-token").getDockerEnvironment()).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
});
