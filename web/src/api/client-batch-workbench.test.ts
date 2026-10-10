import { APIClient } from "./client";
import { batchWorkbenchFixture } from "../test/batch-workbench-fixture";

function response(data: unknown) {
  return new Response(JSON.stringify({ version: "api.v1", request_id: "request-workbench", data }),
    { status: 200, headers: { "Content-Type": "application/json" } });
}
const body = { version: "batch-delivery-workbench.v1", confirm: true, proposal_id: "proposal-1",
  tasks: [{ ordinal: 1, ownership_hints: [{ path: "internal/parser/a.go", kind: "file" }],
    validations: [{ id: "diff", kind: "git_diff_check", scope: "." }] }] };

describe("Batch workbench API boundary", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends only the confirmed product request with the original control identity", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(response(batchWorkbenchFixture())));
    vi.stubGlobal("fetch", fetchMock);
    const client = new APIClient("read-secret", "/api/v1", "control-secret", { batchDeliveryControlEnabled: true });
    await client.prepareBatchWorkbench("run-1", body, "workbench-original-0001");
    await client.executeBatchWorkbenchChild("run-1", "batch-1", 1,
      { version: body.version, confirm: true, expected_generation: 1 }, "workbench-original-0002");
    await client.recoverBatchWorkbenchOwner("run-1", "batch-1", 1,
      { version: body.version, confirm: true, expected_generation: 1, retry: false }, "workbench-original-0003");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/runs/run-1/batch-deliveries/prepare-workbench");
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual(body);
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer control-secret");
    expect(headers.get("Idempotency-Key")).toBe("workbench-original-0001");
    expect(String(init.body)).not.toMatch(/owner_token|turn_limit|token_limit|shell|credentials/u);
  });

  it("rejects authority leakage, mismatched child generation, and a foreign Run", async () => {
    const leaked = batchWorkbenchFixture();
    Object.assign(leaked.snapshot.children[0].workspace, { owner_token: "private-owner" });
    const stale = batchWorkbenchFixture(); stale.children[0].generation = 2;
    const foreign = batchWorkbenchFixture(); foreign.snapshot.plan.run_id = "run-other";
    const fetchMock = vi.fn().mockResolvedValueOnce(response(leaked)).mockResolvedValueOnce(response(stale)).mockResolvedValueOnce(response(foreign));
    vi.stubGlobal("fetch", fetchMock);
    const client = new APIClient("read-secret", "/api/v1");
    await expect(client.getBatchWorkbench("run-1", "batch-1")).rejects.toThrow();
    await expect(client.getBatchWorkbench("run-1", "batch-1")).rejects.toThrow();
    await expect(client.getBatchWorkbench("run-1", "batch-1")).rejects.toThrow();
  });

  it("rejects unconfirmed, traversal, and disabled controls before network dispatch", async () => {
    const fetchMock = vi.fn(); vi.stubGlobal("fetch", fetchMock);
    const control = new APIClient("read-secret", "/api/v1", "control-secret", { batchDeliveryControlEnabled: true });
    await expect(control.prepareBatchWorkbench("run-1", { ...body, confirm: false }, "workbench-original-0001")).rejects.toThrow();
    await expect(control.recoverBatchWorkbenchOwner("run-1", "batch-1", 1,
      { version: body.version, confirm: true, expected_generation: 1, retry: false }, "x".repeat(225))).rejects.toThrow();
    await expect(control.prepareBatchWorkbench("run-1", { ...body, tasks: [{ ...body.tasks[0],
      ownership_hints: [{ path: "../private/a.go", kind: "file" }] }] }, "workbench-original-0001")).rejects.toThrow();
    const readOnly = new APIClient("read-secret", "/api/v1");
    await expect(readOnly.executeBatchWorkbenchChild("run-1", "batch-1", 1,
      { version: body.version, confirm: true, expected_generation: 1 }, "workbench-original-0001")).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
