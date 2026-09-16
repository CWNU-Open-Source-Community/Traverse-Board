import { CyberAgentClient } from "./client";
import type { PlanDirectionControlRequestView } from "./types";

const item = { id: "item-a", run_id: "run-a", version: 2, title: "Review actual output", status: "in_progress",
  acceptance_criteria: ["Review evidence"], dependencies: [] };
const flags = { version: "plan_delivery_control.v1", run_id: "run-a", replayed: false,
  execution_started: false, model_called: false, tool_called: false, capability_grant: false };
const transition = { ...flags, work_item_id: "item-a", applied_status: "in_progress", applied_version: 2, current_work_item: item };
const request = { version: "plan_delivery_control.v1", expected_work_item_version: 1 } as const;
const evidence = { ...request, expected_work_item_version: 2, focused_verification: "job-a: failed before script ran",
  diff_audit: "Reviewed current change", security_audit: "No new authority", handoff_summary: "Shell compatibility remains unresolved",
  functional_verification: "Not passed", robustness_audit: "Failure preserved" };
const checkpoint = { ...flags, checkpoint: { id: "checkpoint-a", work_item_id: "item-a", work_item_version: 2,
  gate_ready: true, full_gate_required: true, mode_revision: 3, module_ordinal: 1, module_count: 1,
  created_at: "2026-09-09T00:00:00Z", handoff_note_id: "note-a" },
  note: { id: "note-a", run_id: "run-a", content: "Manual evidence; job-a failed." }, current_work_item: item };
function reply(data: unknown) {
  const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ version: "api.v1", request_id: "req-a", data }),
    { status: 202, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
afterEach(() => vi.unstubAllGlobals());
const client = () => new CyberAgentClient("read-token", "/api/v1", "control-token", { planDeliveryControlEnabled: true });

it("binds the explicit manual policy to the selection response while preserving old required responses", async () => {
  const body = { version: "plan_delivery_control.v1", proposal_id: "proposal-a", direction: 1 } as const;
  const selection = { ...flags, proposal_id: "proposal-a", selection_id: "selection-a", note_id: "note-a",
    direction: 1, work_item_count: 1, phase_changed: false };
  for (const policy of [undefined, "required", "on_demand"] as const) {
    const request = policy ? { ...body, manual_acceptance: policy } : body;
    const result = policy ? { ...selection, manual_acceptance: policy } : selection;
    const fetcher = reply(result);
    await expect(client().selectPlanDirection("run-a", request, "plan-policy-exact-key-0001")).resolves.toEqual(result);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual(request);
  }
  for (const drift of [{}, { manual_acceptance: "required" }, { manual_acceptance: null }, { manual_acceptance: true }]) {
    reply({ ...selection, ...drift });
    await expect(client().selectPlanDirection("run-a", { ...body, manual_acceptance: "on_demand" }, "plan-policy-exact-key-0001"))
      .rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  }
  const fetcher = reply(selection);
  for (const drift of [{ direction: 0 }, { direction: 4 }, { direction: 1.5 }, { manual_acceptance: "optional" }]) {
    await expect(client().selectPlanDirection("run-a", { ...body, ...drift } as PlanDirectionControlRequestView,
      "plan-policy-exact-key-0001")).rejects.toThrow();
  }
  expect(fetcher).not.toHaveBeenCalled();
});

it("sends the exact version-bound item action and accepts replay with a later current item", async () => {
  const fetcher = reply({ ...transition, replayed: true, current_work_item: { ...item, status: "completed", version: 3 } });
  const result = await client().controlPlanDeliveryWorkItem("run-a", "item-a", "start", request, "plan-item-exact-key-0001");
  expect(result.applied_version).toBe(2);
  expect(result.current_work_item.version).toBe(3);
  expect(fetcher).toHaveBeenCalledWith("/api/v1/runs/run-a/plan/work-items/item-a/start",
    expect.objectContaining({ method: "POST", body: JSON.stringify(request),
      headers: expect.objectContaining({ Authorization: "Bearer control-token", "Idempotency-Key": "plan-item-exact-key-0001" }) }));
});

it.each([
  { ...transition, run_id: "run-b" },
  { ...transition, current_work_item: { ...item, run_id: "run-b" } },
  { ...transition, applied_version: 3 },
  { ...transition, applied_status: "completed" },
  { ...transition, execution_started: true },
])("rejects a mismatched or authority-changing item result", async (result) => {
  reply(result);
  await expect(client().controlPlanDeliveryWorkItem("run-a", "item-a", "start", request, "plan-item-exact-key-0001"))
    .rejects.toMatchObject({ code: "INVALID_RESPONSE" });
});

it("records failed manual evidence as supplied and rejects cross-run notes and wrong item versions", async () => {
  const fetcher = reply(checkpoint);
  const result = await client().recordPlanDeliveryCheckpoint("run-a", "item-a", evidence, "manual-evidence-key-0001");
  expect(result).not.toHaveProperty("verified");
  expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual(evidence);
  reply({ ...checkpoint, note: { ...checkpoint.note, run_id: "run-b" } });
  await expect(client().recordPlanDeliveryCheckpoint("run-a", "item-a", evidence, "manual-evidence-key-0001"))
    .rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  reply({ ...checkpoint, checkpoint: { ...checkpoint.checkpoint, work_item_version: 4 } });
  await expect(client().recordPlanDeliveryCheckpoint("run-a", "item-a", evidence, "manual-evidence-key-0001"))
    .rejects.toMatchObject({ code: "INVALID_RESPONSE" });
});

it("does not send manual mutations without control authority or with oversized evidence", async () => {
  const fetcher = reply(checkpoint);
  await expect(new CyberAgentClient("read-token", "/api/v1", "", { planDeliveryControlEnabled: true })
    .recordPlanDeliveryCheckpoint("run-a", "item-a", evidence, "manual-evidence-key-0001")).rejects.toThrow();
  await expect(client().recordPlanDeliveryCheckpoint("run-a", "item-a", { ...evidence, diff_audit: "x".repeat(1_025) },
    "manual-evidence-key-0001")).rejects.toThrow();
  expect(fetcher).not.toHaveBeenCalled();
});
