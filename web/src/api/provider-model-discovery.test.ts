import { CyberAgentClient } from "./client";
import type { ProviderModelDiscoveryRequestView } from "./types";

const body: ProviderModelDiscoveryRequestView = { version: "provider_model_discovery.v1",
  provider_id: "model-discovery-draft", endpoint_url: "https://api.openai.com/v1/responses",
  transport: "openai_responses", advanced_config: {}, secret: "ordinary-private-key", confirm_discovery: true };
const catalog = { version: "provider_model_discovery.v1", source: "provider_api",
  models: [{ id: "fresh-model" }], truncated: false };

describe("Provider model discovery control", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("uses the control bearer, forwards cancellation, and fetches an empty draft", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ version: "api.v1", request_id: "catalog", data: catalog }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new CyberAgentClient("read-token", "/api/v1", "control-token");
    const signal = new AbortController().signal;
    expect(await client.discoverProviderModels(body, signal)).toEqual(catalog);
    const [url, request] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/models/model-discovery");
    expect(request.signal).toBe(signal);
    expect(request.headers).toMatchObject({ Authorization: "Bearer control-token" });
    expect(JSON.parse(String(request.body))).toEqual(body);
  });
  it("rejects echoed private keys and duplicate model IDs", async () => {
    const client = new CyberAgentClient("read-token", "/api/v1", "control-token");
    for (const models of [[{ id: body.secret }], [{ id: "valid", display_name: body.secret }], [{ id: "same" }, { id: "same" }]]) {
      vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ version: "api.v1", request_id: "catalog", data: { ...catalog, models } }), { status: 200 })));
      await expect(client.discoverProviderModels(body)).rejects.toThrow("invalid catalog");
    }
  });
  it("does not treat provider authentication failure as a successful preset", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ version: "api.v1", request_id: "catalog", error: { code: "FAILED_PRECONDITION", message: "Provider returned HTTP 401" } }), { status: 412 })));
    await expect(new CyberAgentClient("read-token", "/api/v1", "control-token").discoverProviderModels(body)).rejects.toThrow();
  });
});
