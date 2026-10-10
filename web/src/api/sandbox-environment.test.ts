import { APIClient } from "./client";
import { parseSandboxEnvironment, validSandboxEnvironmentRequest } from "./sandbox-environment";
import { sandboxEnvironmentFixture } from "../test/sandbox-environment";

afterEach(() => vi.unstubAllGlobals());

function respond(data: unknown) {
  return new Response(JSON.stringify({ version: "api.v1", request_id: "sandbox-test", data }), { status: 200, headers: { "Content-Type": "application/json" } });
}

it("reads all three actual states using the read token and never infers saved settings are active", async () => {
  const value = sandboxEnvironmentFixture({ docker_enabled: true, docker_image_digest: `sha256:${"a".repeat(64)}` });
  const fetchMock = vi.fn().mockResolvedValue(respond(value)); vi.stubGlobal("fetch", fetchMock);
  const result = await new APIClient("read-token").getSandboxEnvironment();
  expect(result).toEqual(value); expect(result.backends[1]?.enabled).toBe(false);
  expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/sandbox/environment");
  expect(new Headers(fetchMock.mock.calls[0]?.[1].headers).get("Authorization")).toBe("Bearer read-token");
});

it("saves the complete revision-bound settings by PUT and checks the exact nonauthorizing receipt", async () => {
  const settings = { ...sandboxEnvironmentFixture().settings, sbx_enabled: true, default_backend: "sbx" as const,
    sbx_template: `docker/sandbox-templates@sha256:${"b".repeat(64)}` };
  const body = { version: "sandbox_environment.v1" as const, expected_revision: 1, settings };
  const saved = sandboxEnvironmentFixture(settings, {}, 2, false);
  const fetchMock = vi.fn().mockResolvedValue(respond(saved)); vi.stubGlobal("fetch", fetchMock);
  const client = new APIClient("read", "/api/v1", "control");
  expect(await client.saveSandboxEnvironment(body)).toEqual(saved);
  const init = fetchMock.mock.calls[0]?.[1] as RequestInit;
  expect(init.method).toBe("PUT"); expect(JSON.parse(String(init.body))).toEqual(body);
  expect(new Headers(init.headers).get("Authorization")).toBe("Bearer control");
  expect(new Headers(init.headers).get("Idempotency-Key")).toBeNull();
});

it.each([
  (value: ReturnType<typeof sandboxEnvironmentFixture>) => { value.capability_grant = true; },
  (value: ReturnType<typeof sandboxEnvironmentFixture>) => { value.backends[1]!.ready = true; },
  (value: ReturnType<typeof sandboxEnvironmentFixture>) => { value.backends.reverse(); },
  (value: ReturnType<typeof sandboxEnvironmentFixture>) => { value.restart_required = true; },
  (value: ReturnType<typeof sandboxEnvironmentFixture>) => { value.backends[1]!.enabled = true; },
  (value: ReturnType<typeof sandboxEnvironmentFixture>) => { Object.assign(value, { command: "arbitrary" }); },
])("rejects inconsistent or expanded environment projections", (mutate) => {
  const value = sandboxEnvironmentFixture(); mutate(value); expect(() => parseSandboxEnvironment(value)).toThrow();
});

it("rejects omitted booleans, unpinned templates and revision-zero configuration before sending", async () => {
  const settings = sandboxEnvironmentFixture().settings;
  expect(validSandboxEnvironmentRequest({ version: "sandbox_environment.v1", expected_revision: 0, settings })).toBe(false);
  expect(validSandboxEnvironmentRequest({ version: "sandbox_environment.v1", expected_revision: 1, settings: { ...settings, sbx_template: "docker/sandbox-templates:latest" } })).toBe(false);
  const missing = { ...settings } as Partial<typeof settings>; delete missing.docker_enabled;
  expect(validSandboxEnvironmentRequest({ version: "sandbox_environment.v1", expected_revision: 1, settings: missing })).toBe(false);
  const fetchMock = vi.fn(); vi.stubGlobal("fetch", fetchMock);
  await expect(new APIClient("read").saveSandboxEnvironment({ version: "sandbox_environment.v1", expected_revision: 1, settings })).rejects.toThrow();
  expect(fetchMock).not.toHaveBeenCalled();
});

it.each(["Docker/sandbox", "docker/../sandbox", "docker//sandbox", "https://docker/sandbox", "/docker/sandbox"])(
  "rejects a template repository outside the official pinned parser boundary: %s", (repository) => {
    expect(validSandboxEnvironmentRequest({ version: "sandbox_environment.v1", expected_revision: 1,
      settings: { ...sandboxEnvironmentFixture().settings, sbx_template: `${repository}@sha256:${"a".repeat(64)}` } })).toBe(false);
  });

it("uses bounded single-line blocker copy and accepts Unicode rune length", () => {
  const value = sandboxEnvironmentFixture();
  value.backends[1]!.blockers = [{ code: "SBX-NOT-READY", message: "🧭".repeat(240) }];
  expect(parseSandboxEnvironment(value)).toEqual(value);
  for (const message of ["🧭".repeat(241), "ready\nsecret", "ready\tsecret", "\ud800"]) {
    value.backends[1]!.blockers[0]!.message = message;
    expect(() => parseSandboxEnvironment(value)).toThrow();
  }
});

it("rejects a save receipt that switched the requested default or revision", async () => {
  const body = { version: "sandbox_environment.v1" as const, expected_revision: 1, settings: sandboxEnvironmentFixture().settings };
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(respond(sandboxEnvironmentFixture({}, {}, 3, false))));
  await expect(new APIClient("read", "/api/v1", "control").saveSandboxEnvironment(body)).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
});
