import { CyberAgentClient } from "./client";

const view = {
  protocol_version: "search_diagnostics.v1", thread_id: "thread-1", run_id: "run-1",
  mode_revision: 3, model_route: "provider/model", provider: "provider", model: "model",
  search_policy: "provider_native", backend: "provider", checked_at: "2026-09-21T08:00:00Z",
  state: "succeeded", code: "none", result_count: 2, network_request_attempted: true,
};

function response(data: unknown) {
  return new Response(JSON.stringify({ version: "api.v1", request_id: "request-1", data }), {
    status: 200, headers: { "Content-Type": "application/json" },
  });
}

describe("search diagnostics client", () => {
  afterEach(() => { vi.unstubAllGlobals(); });

  it("uses a control-authenticated POST and accepts the closed response contract", async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(view));
    vi.stubGlobal("fetch", fetchMock);
    const client = new CyberAgentClient("read-token", "/api/v1", "control-token");

    await expect(client.diagnoseThreadSearch("thread-1", {
      version: "search_diagnostics.v1", confirm: true,
    })).resolves.toEqual(view);

    expect(fetchMock).toHaveBeenCalledExactlyOnceWith(
      "/api/v1/threads/thread-1/search-diagnostics",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ Authorization: "Bearer control-token" }),
        body: JSON.stringify({ version: "search_diagnostics.v1", confirm: true }),
      }),
    );
  });

  it("rejects forged success and unknown diagnostic codes", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ ...view, result_count: 0 }))
      .mockResolvedValueOnce(response({ ...view, state: "failed", code: "captcha", result_count: 0 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new CyberAgentClient("read-token", "/api/v1", "control-token");
    const request = { version: "search_diagnostics.v1" as const, confirm: true as const };

    await expect(client.diagnoseThreadSearch("thread-1", request)).rejects
      .toThrow("violated its outcome binding");
    await expect(client.diagnoseThreadSearch("thread-1", request)).rejects
      .toThrow("response is invalid");
  });

  it("shows unconfigured search without turning it into a protocol error", async () => {
    const unconfigured = { ...view, search_policy: "", backend: "", state: "failed",
      code: "not_configured", result_count: 0, network_request_attempted: false };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(unconfigured)));
    const client = new CyberAgentClient("read-token", "/api/v1", "control-token");
    await expect(client.diagnoseThreadSearch("thread-1", {
      version: "search_diagnostics.v1", confirm: true,
    })).resolves.toEqual(unconfigured);
  });
});
