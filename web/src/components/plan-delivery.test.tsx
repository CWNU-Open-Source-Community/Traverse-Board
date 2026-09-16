import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { APIRequestError, type CyberAgentClient } from "../api/client";
import type { PlanDeliveryStateView, RunDetailView } from "../api/types";
import { PlanDeliveryPanel } from "./run-workspace";

const directions: NonNullable<PlanDeliveryStateView["proposal"]>["directions"] = [
  { ordinal: 1, title: "Conservative", summary: "Keep changes narrow.", tradeoffs: ["More sequential work"],
    modules: [{ ordinal: 1, title: "Inspect", objective: "Inspect current boundaries.",
      acceptance_criteria: ["Boundaries recorded"], dependencies: [] }] },
  { ordinal: 2, title: "Balanced", summary: "Deliver a vertical slice.", tradeoffs: ["Moderate breadth"],
    modules: [{ ordinal: 1, title: "Implement", objective: "Implement the core path.",
      acceptance_criteria: ["Focused tests pass"], dependencies: [] }] },
  { ordinal: 3, title: "Accelerated", summary: "Prepare independent slices.", tradeoffs: ["Higher review load"],
    modules: [{ ordinal: 1, title: "Prepare", objective: "Prepare independent work.",
      acceptance_criteria: ["Work stays bounded"], dependencies: [] }] },
];

describe("PlanDeliveryPanel", () => {
  it("renders the selected direction as a read-only projection", () => {
    const state: PlanDeliveryStateView = {
      operator_choice_needed: false,
      phase_change_needed: true,
      capability_grant: false,
      delivery_gate_enforced: true,
      required_checkpoints: 1,
      ready_checkpoints: 1,
      checkpoints: [{ id: "checkpoint-1", work_item_id: "work-1", module_ordinal: 1,
        module_count: 1, mode_revision: 5, work_item_version: 2,
        full_gate_required: true, handoff_note_id: "note-handoff",
        gate_ready: true, created_at: "2026-07-13T00:02:00Z" }],
      proposal: { id: "proposal-1", protocol_version: "plan_delivery.v1", status: "proposed",
        mode_revision: 4, directions, version: 1, created_at: "2026-07-13T00:00:00Z" },
      selection: { id: "selection-1", proposal_id: "proposal-1", direction_ordinal: 2,
        note_id: "note-1", items: [{ ordinal: 1, module_ordinal: 1, work_item_id: "work-1" }],
        version: 1, created_at: "2026-07-13T00:01:00Z" },
    };
    const { container } = renderWithQuery(<PlanDeliveryPanel state={state} />);
    expect(screen.getByText("Deliver phase required")).toBeInTheDocument();
    expect(screen.queryByText("Capability grant: no")).not.toBeInTheDocument();
    expect(screen.queryByText(/Mode revision/)).not.toBeInTheDocument();
    expect(screen.getByText("Current manual records 1 / 1")).toBeInTheDocument();
    expect(screen.getByText("Checkpoint history")).toBeInTheDocument();
    expect(screen.getByText("full gate")).toBeInTheDocument();
    expect(screen.getByText("Balanced")).toBeInTheDocument();
    expect(screen.getByText("Implement the core path.")).toBeInTheDocument();
    expect(container.querySelector("details.selected")?.getAttribute("open")).not.toBeNull();
    expect(container.querySelector("button")).toBeNull();
  });

  it.each([1, 2, 3])("adopts one of %i real options and enters Deliver with distinct exact requests", async (count) => {
    const selectPlanDirection = vi.fn().mockResolvedValue({ selection_id: "selection-1" });
    const enterPlanDelivery = vi.fn().mockResolvedValue({ selection_id: "selection-1" });
    const client = { hasPlanDelivery: true, selectPlanDirection, enterPlanDelivery } as unknown as CyberAgentClient;
    const state = choiceState(count);
    const user = userEvent.setup();
    const view = renderWithQuery(<PlanDeliveryPanel client={client} detail={planDetail("run-a")} state={state} />);
    expect(view.container.querySelectorAll("details.plan-direction")).toHaveLength(count);
    const manualOption = screen.getByRole("checkbox", { name: "Require manual acceptance for each item" });
    expect(manualOption).toHaveAccessibleDescription("Manual notes are optional by default; actual checks, file approval, and Plan item completion remain required.");
    expect(screen.queryByText(/Current manual records|legacy exempt|Gate enforcement/)).not.toBeInTheDocument();
    if (count === 1) expect(view.container.querySelector("details.plan-direction")).toHaveAttribute("open");
    else await user.click(screen.getByText("Conservative"));
    if (count === 3) await user.click(screen.getByRole("checkbox", { name: /Require manual acceptance/ }));
    const button = screen.getByRole("button", { name: count === 1 ? "Adopt plan and enter Deliver" : "Adopt plan 1 and enter Deliver" });
    await user.click(button);
    await waitFor(() => expect(enterPlanDelivery).toHaveBeenCalledTimes(1));
    expect(selectPlanDirection).toHaveBeenCalledWith("run-a", {
      version: "plan_delivery_control.v1", proposal_id: "proposal-1", direction: 1,
      manual_acceptance: count === 3 ? "required" : "on_demand",
    }, expect.any(String));
    expect(enterPlanDelivery).toHaveBeenCalledWith("run-a", { version: "plan_delivery_control.v1" }, expect.any(String));
    expect(selectPlanDirection.mock.invocationCallOrder[0]).toBeLessThan(enterPlanDelivery.mock.invocationCallOrder[0]);
    expect(selectPlanDirection.mock.calls[0][2]).not.toBe(enterPlanDelivery.mock.calls[0][2]);
  });

  it.each(["empty", "four", "ordinal"])("rejects %s actual option arrays before selection", (kind) => {
    const selectPlanDirection = vi.fn();
    const state = choiceState(1);
    state.proposal!.directions = kind === "empty" ? [] : kind === "four" ? [...directions, { ...directions[0], ordinal: 4 }] :
      [{ ...directions[0], ordinal: 2 }];
    renderWithQuery(<PlanDeliveryPanel client={{ hasPlanDelivery: true, selectPlanDirection } as unknown as CyberAgentClient}
      detail={planDetail("run-a")} state={state} />);
    expect(screen.getByRole("alert")).toHaveTextContent("count or ordinals are inconsistent");
    for (const button of screen.queryAllByRole("button", { name: /Adopt plan/, hidden: true })) expect(button).toBeDisabled();
    expect(selectPlanDirection).not.toHaveBeenCalled();
  });

  it("distinguishes inherited manual progress from newly recorded checkpoints", () => {
    const state: PlanDeliveryStateView = {
      operator_choice_needed: false, phase_change_needed: false, capability_grant: false,
      delivery_gate_enforced: true, required_checkpoints: 1, ready_checkpoints: 1, checkpoints: [],
      proposal: { id: "continued-plan", protocol_version: "plan_delivery.v1", status: "proposed",
        mode_revision: 1, directions, version: 1, created_at: "2026-09-09T00:00:00Z" },
      selection: { id: "continued-selection", proposal_id: "continued-plan", direction_ordinal: 2,
        note_id: "continued-note", items: [{ ordinal: 1, module_ordinal: 1, work_item_id: "continued-work" }],
        version: 1, created_at: "2026-09-09T00:00:00Z" },
      continued_completions: [{ work_item_id: "continued-work", source_run_id: "original-run", source_work_item_id: "original-work",
        checkpoint_id: "original-checkpoint", handoff_note_id: "original-note", completed_at: "2026-09-08T00:00:00Z" }],
    };
    renderWithQuery(<PlanDeliveryPanel state={state} />);
    expect(screen.getByText("Valid manual records (including continued) 1 / 1")).toBeInTheDocument();
    expect(screen.getByText(/This execution has no new manual acceptance records/)).toBeInTheDocument();
    expect(screen.queryByText("No checkpoints recorded")).not.toBeInTheDocument();
  });

  it("confirms a lost selection response using the same body and key before entering Deliver", async () => {
    const selectPlanDirection = vi.fn().mockRejectedValueOnce(new Error("selection response lost"))
      .mockRejectedValueOnce(new APIRequestError("Authorization changed", "FORBIDDEN", 403))
      .mockResolvedValueOnce({ selection_id: "selection-1" });
    const enterPlanDelivery = vi.fn().mockResolvedValue({ selection_id: "selection-1" });
    const client = { hasPlanDelivery: true, selectPlanDirection, enterPlanDelivery } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderWithQuery(<PlanDeliveryPanel client={client} detail={planDetail("run-a")} state={choiceState()} />);
    await user.click(screen.getByRole("button", { name: "Adopt plan and enter Deliver" }));
    await user.click(await screen.findByRole("button", { name: "Confirm previous plan operation" }));
    await screen.findByText(/Authorization changed/);
    expect(screen.queryByRole("button", { name: "Return to editing" })).not.toBeInTheDocument();
    expect(enterPlanDelivery).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm previous plan operation" }));
    await waitFor(() => expect(enterPlanDelivery).toHaveBeenCalledTimes(1));
    expect(selectPlanDirection.mock.calls[1]).toEqual(selectPlanDirection.mock.calls[0]);
    expect(selectPlanDirection.mock.calls[2]).toEqual(selectPlanDirection.mock.calls[0]);
  });

  it("keeps the second-step original request across closure and isolates late results after navigation", async () => {
    let finishFirst!: (value: unknown) => void;
    const selectPlanDirection = vi.fn(async (runID: string) => ({ selection_id: `selection-${runID}` }));
    const enterPlanDelivery = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { finishFirst = resolve; }))
      .mockRejectedValueOnce(new Error("delivery response lost")).mockResolvedValueOnce({ selection_id: "selection-run-b" });
    const client = { hasPlanDelivery: true, selectPlanDirection, enterPlanDelivery } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const content = (runID: string) => <QueryClientProvider client={queryClient}>
      <PlanDeliveryPanel client={client} threadID={`thread-${runID}`} state={choiceState()} detail={planDetail(runID)} />
    </QueryClientProvider>;
    const user = userEvent.setup();
    const view = render(content("run-a"));
    await user.click(screen.getByRole("button", { name: "Adopt plan and enter Deliver" }));
    await waitFor(() => expect(enterPlanDelivery).toHaveBeenCalledTimes(1));
    view.rerender(content("run-b"));
    await user.click(screen.getByRole("button", { name: "Adopt plan and enter Deliver" }));
    await screen.findByText(/delivery response lost/);
    const original = enterPlanDelivery.mock.calls[1];
    expect(screen.getByText(/The plan is adopted/)).toBeInTheDocument();
    view.unmount();
    invalidate.mockClear();
    await act(async () => finishFirst({ selection_id: "selection-run-a" }));
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["run", "run-a"] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["v2", "thread", "thread-run-a"] });
    expect(invalidate).not.toHaveBeenCalledWith({ queryKey: ["run", "run-b"] });
    render(content("run-b"));
    await user.click(screen.getByRole("button", { name: "Confirm previous plan operation" }));
    await waitFor(() => expect(queryClient.getQueryData(["run", "run-b", "plan-control-intent"])).toBeNull());
    expect(selectPlanDirection).toHaveBeenCalledTimes(2);
    expect(enterPlanDelivery.mock.calls[2]).toEqual(original);
  });

  it("retains the selected plan after an explicit Deliver rejection and retries only that step", async () => {
    const selectPlanDirection = vi.fn().mockResolvedValue({ selection_id: "selection-1" });
    const enterPlanDelivery = vi.fn().mockRejectedValueOnce(new APIRequestError("Wait for active execution", "CONFLICT", 409))
      .mockResolvedValueOnce({ selection_id: "selection-1" });
    const user = userEvent.setup();
    renderWithQuery(<PlanDeliveryPanel client={{ hasPlanDelivery: true, selectPlanDirection, enterPlanDelivery } as unknown as CyberAgentClient}
      detail={planDetail("run-a")} state={choiceState()} />);
    await user.click(screen.getByRole("button", { name: "Adopt plan and enter Deliver" }));
    await user.click(await screen.findByRole("button", { name: "Retry entering Deliver" }));
    await waitFor(() => expect(enterPlanDelivery).toHaveBeenCalledTimes(2));
    expect(selectPlanDirection).toHaveBeenCalledTimes(1);
    expect(enterPlanDelivery.mock.calls[1]).toEqual(enterPlanDelivery.mock.calls[0]);
  });

  it("keeps mismatched Deliver results unknown and permits an existing legacy selection to enter Deliver", async () => {
    const state = { ...choiceState(), operator_choice_needed: false, phase_change_needed: true,
      selection: { id: "legacy-selection", proposal_id: "proposal-1", direction_ordinal: 1,
        note_id: "note-1", items: [], version: 1, created_at: "2026-09-09T00:00:00Z" } };
    const enterPlanDelivery = vi.fn().mockResolvedValue({ selection_id: "other-selection" });
    const user = userEvent.setup();
    renderWithQuery(<PlanDeliveryPanel client={{ hasPlanDelivery: true, enterPlanDelivery } as unknown as CyberAgentClient}
      detail={planDetail("run-a")} state={state} />);
    await user.click(screen.getByRole("button", { name: "Enter Deliver" }));
    expect(await screen.findByRole("button", { name: "Confirm previous plan operation" })).toBeInTheDocument();
    expect(screen.getByText(/different Plan selection/)).toBeInTheDocument();
  });

  it("does not imply that an absent plan was selected", () => {
    renderWithQuery(<PlanDeliveryPanel state={{ operator_choice_needed: false, phase_change_needed: false,
      capability_grant: false, delivery_gate_enforced: true, required_checkpoints: 0, ready_checkpoints: 0, checkpoints: [] }} />);
    expect(screen.getByText("No plan proposal yet")).toBeInTheDocument();
    expect(screen.queryByText("Direction selected")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Enter Deliver" })).not.toBeInTheDocument();
  });

  it("explains legacy acceptance only for an actual selected legacy Plan", () => {
    const state: PlanDeliveryStateView = { ...choiceState(), operator_choice_needed: false, phase_change_needed: true,
      delivery_gate_enforced: false, selection: { id: "legacy-selection", proposal_id: "proposal-1", direction_ordinal: 1,
        note_id: "note-1", items: [], version: 1, created_at: "2026-09-09T00:00:00Z" } };
    renderWithQuery(<PlanDeliveryPanel state={state} />);
    expect(screen.getByText(/This legacy Plan does not require per-item manual acceptance/)).toBeInTheDocument();
    expect(screen.queryByText(/Current manual records|Capability grant|Mode revision/)).not.toBeInTheDocument();
  });
});

function renderWithQuery(node: React.ReactNode) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false },
    mutations: { retry: false } } });
  return { ...render(<QueryClientProvider client={queryClient}>{node}</QueryClientProvider>),
    queryClient };
}

function choiceState(count = 1): PlanDeliveryStateView {
  return { operator_choice_needed: true, phase_change_needed: false, capability_grant: false,
    delivery_gate_enforced: true, required_checkpoints: 0, ready_checkpoints: 0, checkpoints: [],
    proposal: { id: "proposal-1", protocol_version: "plan_delivery.v1", status: "proposed", mode_revision: 4,
      directions: directions.slice(0, count), version: 1, created_at: "2026-07-13T00:00:00Z" } };
}
function planDetail(runID: string): RunDetailView {
  return { run: { id: runID, status: "paused" }, mode: { phase: "plan" } } as RunDetailView;
}
