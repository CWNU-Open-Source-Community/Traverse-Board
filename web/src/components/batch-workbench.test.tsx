import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type APIClient } from "../api/client";
import { batchProposalFixture, batchWorkbenchFixture } from "../test/batch-workbench-fixture";
import { BatchPreparation, BatchWorkbenchControls } from "./batch-workbench";
import { BatchDeliveriesPanel } from "./run-projections";

function provider(children: React.ReactNode) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>{children}</QueryClientProvider>;
}
beforeEach(() => sessionStorage.clear());

describe("Batch workbench", () => {
  it("requires owned paths and confirmation, retains the original preparation after remount, and never executes on preparation", async () => {
    const prepare = vi.fn().mockRejectedValueOnce(new Error("Preparation response interrupted"))
      .mockResolvedValue(batchWorkbenchFixture());
    const execute = vi.fn();
    const client = { baseURL: "http://batch-test", hasBatchDeliveryHostValidation: false,
      getRunChildTaskProposals: vi.fn().mockResolvedValue({ items: [batchProposalFixture()] }),
      prepareBatchWorkbench: prepare, executeBatchWorkbenchChild: execute } as unknown as APIClient;
    const user = userEvent.setup();
    const first = render(provider(<BatchPreparation client={client} runID="run-1" usedProposalIDs={[]} onReviewProposals={vi.fn()} />));
    const start = await screen.findByRole("button", { name: "Prepare isolated tasks" });
    expect(start).toBeDisabled();
    await user.type(screen.getByLabelText("Owned paths (one per line)"), "internal/parser/a.go");
    expect(start).toBeDisabled();
    await user.click(screen.getByLabelText(/I reviewed the tasks/));
    await user.click(start);
    await screen.findByRole("alert");
    expect(prepare).toHaveBeenCalledTimes(1);
    const original = prepare.mock.calls[0];
    expect(original[1]).toEqual({ version: "batch-delivery-workbench.v1", proposal_id: "proposal-1", confirm: true,
      tasks: [{ ordinal: 1, ownership_hints: [{ path: "internal/parser/a.go", kind: "file" }],
        validations: [{ id: "diff", kind: "git_diff_check", scope: "." }] }] });
    first.unmount();
    render(provider(<BatchPreparation client={client} runID="run-1" usedProposalIDs={[]} onReviewProposals={vi.fn()} />));
    expect(screen.queryByLabelText("Owned paths (one per line)")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Check preparation result" }));
    await waitFor(() => expect(prepare).toHaveBeenCalledTimes(2));
    expect(prepare.mock.calls[1]).toEqual(original);
    expect(execute).not.toHaveBeenCalled();
  });

  it("prepares feedback rework for the exact generation and waits for a separate start", async () => {
    const current = batchWorkbenchFixture("changes_requested", 2);
    const next = batchWorkbenchFixture("question", 3);
    const recover = vi.fn().mockResolvedValue(next);
    const execute = vi.fn().mockResolvedValue(batchWorkbenchFixture("ready_for_review", 3));
    const client = { baseURL: "http://batch-test", getBatchWorkbench: vi.fn().mockResolvedValue(current),
      recoverBatchWorkbenchOwner: recover, executeBatchWorkbenchChild: execute } as unknown as APIClient;
    const user = userEvent.setup();
    render(provider(<BatchWorkbenchControls client={client} runID="run-1" planID="batch-1" ordinal={1} />));
    await user.click(await screen.findByRole("button", { name: "Prepare rework from review feedback" }));
    const start = await screen.findByRole("button", { name: "Start child and return delivery" });
    expect(recover).toHaveBeenCalledWith("run-1", "batch-1", 1,
      { version: "batch-delivery-workbench.v1", confirm: true, retry: true, expected_generation: 2 }, expect.stringMatching(/^web-batch-owner-/));
    expect(execute).not.toHaveBeenCalled();
    await user.click(start);
    await waitFor(() => expect(execute).toHaveBeenCalledWith("run-1", "batch-1", 1,
      { version: "batch-delivery-workbench.v1", confirm: true, expected_generation: 3 }, expect.stringMatching(/^web-batch-execute-/)));
  });

  it("retains an interrupted execution identity and makes refresh read only", async () => {
    const current = batchWorkbenchFixture();
    const unresolved = structuredClone(current);
    unresolved.children[0].outcome_unresolved = true;
    const execute = vi.fn().mockRejectedValueOnce(new Error("Worker result interrupted"))
      .mockResolvedValue({ ...unresolved, replayed: true });
    const get = vi.fn().mockResolvedValue(current);
    const client = { baseURL: "http://batch-test", getBatchWorkbench: get, executeBatchWorkbenchChild: execute,
      recoverBatchWorkbenchOwner: vi.fn() } as unknown as APIClient;
    const user = userEvent.setup();
    const first = render(provider(<BatchWorkbenchControls client={client} runID="run-1" planID="batch-1" ordinal={1} />));
    await user.click(await screen.findByRole("button", { name: "Start child and return delivery" }));
    await screen.findByRole("alert");
    const original = execute.mock.calls[0];
    first.unmount();
    get.mockResolvedValue(unresolved);
    render(provider(<BatchWorkbenchControls client={client} runID="run-1" planID="batch-1" ordinal={1} />));
    await user.click(await screen.findByRole("button", { name: "Check child operation result" }));
    await screen.findByRole("button", { name: "Recover child ownership" });
    expect(execute.mock.calls[1]).toEqual(original);
    await user.click(screen.getByRole("button", { name: "Refresh child progress" }));
    expect(execute).toHaveBeenCalledTimes(2);
    expect(client.recoverBatchWorkbenchOwner).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Start child and return delivery" })).not.toBeInTheDocument();
  });

  it("blocks execution while a dependency remains unaccepted", async () => {
    const current = batchWorkbenchFixture();
    current.snapshot.plan.spec.tasks[0].dependency_ordinals = [2];
    const client = { baseURL: "http://batch-test", getBatchWorkbench: vi.fn().mockResolvedValue(current),
      executeBatchWorkbenchChild: vi.fn() } as unknown as APIClient;
    render(provider(<BatchWorkbenchControls client={client} runID="run-1" planID="batch-1" ordinal={1} />));
    expect(await screen.findByRole("button", { name: "Start child and return delivery" })).toBeDisabled();
    expect(screen.getByText("Accept the dependency task before starting this task.")).toBeInTheDocument();
    expect(client.executeBatchWorkbenchChild).not.toHaveBeenCalled();
  });

  it("refreshes the feedback workflow after an independent changes-requested review", async () => {
    const current = batchWorkbenchFixture("ready_for_review");
    current.snapshot.children[0].receipt = { id: "receipt-1", ordinal: 1, generation: 1,
      protocol_version: "batch-delivery-receipt.v1", base_commit: "a".repeat(40), head_commit: "b".repeat(40),
      diff_sha256: "c".repeat(64), call_chain_sha256: "d".repeat(64), diff_bytes: 128,
      diff_stat: "1 file changed", changed_files: ["internal/parser/a.go"], test_receipts: [],
      evidence_refs: [], limitations: [], created_at: "2026-10-10T00:00:00Z" };
    const next = structuredClone(current); next.snapshot.children[0].workspace.status = "changes_requested";
    let reviewed = false;
    const client = { baseURL: "http://batch-test", hasBatchDeliveryControl: true, hasBatchDeliveryHostValidation: false,
      getRunBatchDeliveries: vi.fn().mockResolvedValue({ items: [current.snapshot.plan] }),
      getRunBatchDelivery: vi.fn().mockImplementation(() => Promise.resolve(reviewed ? next.snapshot : current.snapshot)),
      getBatchWorkbench: vi.fn().mockImplementation(() => Promise.resolve(reviewed ? next : current)),
      reviewRunBatchDeliveryChild: vi.fn().mockImplementation(() => { reviewed = true; return Promise.resolve({}); }),
    } as unknown as APIClient;
    const user = userEvent.setup();
    render(provider(<BatchDeliveriesPanel client={client} runID="run-1" />));
    await user.type(await screen.findByLabelText("Independent review summary"), "Add the bounded regression example");
    await user.click(screen.getByLabelText(/I independently reviewed/));
    await user.click(screen.getByRole("button", { name: "Request changes" }));
    expect(await screen.findByRole("button", { name: "Prepare rework from review feedback" })).toBeInTheDocument();
    expect(client.reviewRunBatchDeliveryChild).toHaveBeenCalledWith("run-1", "batch-1", 1,
      expect.objectContaining({ generation: 1, summary: "Add the bounded regression example", verdict: "changes_requested" }), expect.any(String));
  });

  it("keeps independent review confirmation bound to one child receipt", async () => {
    const current = batchWorkbenchFixture("ready_for_review");
    current.snapshot.children[0].receipt = { id: "receipt-1", ordinal: 1, generation: 1,
      protocol_version: "batch-delivery-receipt.v1", base_commit: "a".repeat(40), head_commit: "b".repeat(40),
      diff_sha256: "c".repeat(64), call_chain_sha256: "d".repeat(64), diff_bytes: 128,
      diff_stat: "1 file changed", changed_files: ["internal/parser/a.go"], test_receipts: [],
      evidence_refs: [], limitations: [], created_at: "2026-10-10T00:00:00Z" };
    const second = structuredClone(current.snapshot.children[0]);
    second.workspace.ordinal = 2; second.workspace.agent_id = "agent-child-2";
    second.receipt!.id = "receipt-2"; second.receipt!.ordinal = 2;
    current.snapshot.children.push(second);
    const client = { baseURL: "http://batch-test", hasBatchDeliveryControl: true,
      getRunBatchDeliveries: vi.fn().mockResolvedValue({ items: [current.snapshot.plan] }),
      getRunBatchDelivery: vi.fn().mockResolvedValue(current.snapshot),
    } as unknown as APIClient;
    const user = userEvent.setup();
    render(provider(<BatchDeliveriesPanel client={client} runID="run-1" />));
    const summaries = await screen.findAllByLabelText("Independent review summary");
    await user.type(summaries[0], "Independently checked child one");
    await user.click(screen.getAllByLabelText(/I independently reviewed/)[0]);
    const accept = screen.getAllByRole("button", { name: "Accept" });
    expect(accept[0]).toBeEnabled(); expect(accept[1]).toBeDisabled();
  });
});
