import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { APIRequestError, type CyberAgentClient } from "../api/client";
import type { PlanDeliveryStateView, RunDetailView, WorkItemView } from "../api/types";
import { PlanDeliveryWorkItems } from "./plan-delivery-work-items";

const labels = ["Focused verification results", "Diff review", "Security review", "Full functional verification",
  "Failure and recovery checks", "Handoff summary"];
function workItem(runID = "run-a", version = 2): WorkItemView {
  return { id: `${runID}-item`, run_id: runID, title: `${runID}: Review the actual change`, status: "in_progress",
    version, acceptance_criteria: ["Review the actual check result"], dependencies: [], priority: "normal",
    created_at: "2026-09-09T00:00:00Z", updated_at: "2026-09-09T00:00:00Z" };
}
function plan(runID = "run-a", enrolled = true): PlanDeliveryStateView {
  return { operator_choice_needed: false, phase_change_needed: false, capability_grant: false,
    delivery_gate_enforced: enrolled, required_checkpoints: enrolled ? 1 : 0, ready_checkpoints: 0, checkpoints: [],
    selection: { id: `${runID}-selection`, proposal_id: `${runID}-proposal`, direction_ordinal: 1,
      note_id: `${runID}-note`, version: 1, created_at: "2026-09-09T00:00:00Z",
      items: [{ ordinal: 1, module_ordinal: 1, work_item_id: workItem(runID).id }] } };
}
function fixture(record = vi.fn().mockResolvedValue({ current_work_item: workItem() })) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const client = { hasPlanDelivery: true, getWorkItem: vi.fn(async (id: string) => workItem(id.startsWith("run-b") ? "run-b" : "run-a")),
    recordPlanDeliveryCheckpoint: record, controlPlanDeliveryWorkItem: vi.fn() } as unknown as CyberAgentClient;
  const content = (runID = "run-a", state = plan(runID)) => <QueryClientProvider client={queryClient}>
    <PlanDeliveryWorkItems client={client} detail={{ run: { id: runID, status: "paused" },
      mode: { phase: "deliver", revision: 5 } } as RunDetailView} state={state} threadID={`thread-${runID}`} />
  </QueryClientProvider>;
  const view = render(content());
  return { ...view, queryClient, client, record, content, user: userEvent.setup() };
}
async function fillEvidence(text: string) {
  await screen.findByLabelText(labels[0]);
  for (const label of labels) fireEvent.change(screen.getByLabelText(label), { target: { value: text } });
}

it("prevents rewriting a recorded version and restores the author's draft when the item version changes", async () => {
  const test = fixture();
  await fillEvidence("Original evidence with unresolved issues");
  const recorded = plan();
  recorded.checkpoints = [{ id: "checkpoint-recorded", work_item_id: workItem().id, module_ordinal: 1, module_count: 1,
    mode_revision: 5, work_item_version: 2, full_gate_required: true, gate_ready: true,
    handoff_note_id: "handoff-recorded", created_at: "2026-09-09T00:00:00Z" }];
  test.rerender(test.content("run-a", recorded));
  expect(screen.queryByRole("button", { name: "Record manual acceptance and handoff" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Complete this item" })).toBeEnabled();
  expect(screen.getByText(/record cannot be overwritten/)).toBeInTheDocument();
  await act(async () => { test.queryClient.setQueryData(["work-item", workItem().id], workItem("run-a", 3)); });
  expect(await screen.findByLabelText(labels[0])).toHaveValue("Original evidence with unresolved issues");
  expect(screen.getByRole("button", { name: "Complete this item" })).toBeDisabled();
  expect(test.record).not.toHaveBeenCalled();
});

it("does not offer mutations for a legacy selection that is exempt from checkpoint enrollment", async () => {
  const test = fixture();
  test.rerender(test.content("run-a", plan("run-a", false)));
  await screen.findByRole("heading", { name: "1. run-a: Review the actual change" });
  for (const button of screen.queryAllByRole("button", { name: /Record manual acceptance and handoff|Complete this item|Start this item/ })) {
    expect(button).toBeDisabled();
  }
  expect(test.record).not.toHaveBeenCalled();
  expect(test.client.controlPlanDeliveryWorkItem).not.toHaveBeenCalled();
});

it("keeps an unknown original operation recoverable after an authentication rejection of its confirmation", async () => {
  const record = vi.fn().mockRejectedValueOnce(new Error("response lost"))
    .mockRejectedValueOnce(new APIRequestError("Control credential expired", "POLICY_DENIED", 401))
    .mockResolvedValueOnce({ current_work_item: workItem() });
  const test = fixture(record);
  await fillEvidence("Evidence submitted before the connection failed");
  await test.user.click(screen.getByRole("button", { name: "Record manual acceptance and handoff" }));
  await test.user.click(await screen.findByRole("button", { name: "Confirm previous acceptance operation" }));
  await screen.findByText(/Control credential expired/);
  expect(screen.queryByRole("button", { name: "Return to editing" })).not.toBeInTheDocument();
  expect(screen.getByLabelText(labels[0])).toBeDisabled();
  await test.user.click(screen.getByRole("button", { name: "Confirm previous acceptance operation" }));
  await waitFor(() => expect(record).toHaveBeenCalledTimes(3));
  expect(record.mock.calls[1]).toEqual(record.mock.calls[0]);
  expect(record.mock.calls[2]).toEqual(record.mock.calls[0]);
});

it("keeps a late rejection on the submitted task and uses its refreshed version for a corrected new request", async () => {
  let reject!: (error: Error) => void;
  const record = vi.fn().mockImplementationOnce(() => new Promise((_resolve, rejectPromise) => { reject = rejectPromise; }))
    .mockResolvedValueOnce({ current_work_item: workItem("run-a", 3) });
  const test = fixture(record);
  await fillEvidence("Task A draft");
  await test.user.click(screen.getByRole("button", { name: "Record manual acceptance and handoff" }));
  await waitFor(() => expect(record).toHaveBeenCalledTimes(1));
  test.rerender(test.content("run-b"));
  await screen.findByRole("heading", { name: "1. run-b: Review the actual change" });
  await fillEvidence("Task B independent draft");
  await waitFor(() => expect(screen.getByLabelText(labels[0])).toHaveValue("Task B independent draft"));
  await act(async () => reject(new APIRequestError("WorkItem version changed", "CONFLICT", 409)));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(screen.getByLabelText(labels[0])).toHaveValue("Task B independent draft");
  vi.mocked(test.client.getWorkItem).mockImplementation(async (id) => workItem(id.startsWith("run-b") ? "run-b" : "run-a", 3));
  test.rerender(test.content("run-a"));
  await test.user.click(await screen.findByRole("button", { name: "Return to editing" }));
  expect(screen.getByLabelText(labels[0])).toHaveValue("Task A draft");
  await waitFor(() => expect(test.queryClient.getQueryData<WorkItemView>(["work-item", workItem().id])?.version).toBe(3));
  await test.user.click(screen.getByRole("button", { name: "Record manual acceptance and handoff" }));
  await waitFor(() => expect(record).toHaveBeenCalledTimes(2));
  expect(record.mock.calls[1][0]).toBe("run-a");
  expect(record.mock.calls[1][2].expected_work_item_version).toBe(3);
  expect(record.mock.calls[1][3]).not.toBe(record.mock.calls[0][3]);
});
