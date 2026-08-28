import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import type { ApprovalQueueItemView, ThreadDetailView, ThreadMessageControlView,
  ThreadTranscriptItemView } from "../api/types";
import { ThreadWorkspace } from "./thread-workspace";

function detail(status: "failed" | "waiting_approval" | "running"): ThreadDetailView {
  const run = {
    id: "run-last", mission_id: "mission-1", session_id: "session-last", status,
    config: { model_route: "review", interactive: true },
    budget: { max_turns: 4, max_tool_calls: 8 },
    created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:01:00Z",
  };
  return {
    thread: { id: "thread-1", protocol_version: "thread.v1", workspace_id: "workspace-1",
      mission_id: "mission-1", title: "Stable task", status: "active",
      ...(status !== "failed" ? { active_run_id: "run-last" } : {}),
      last_run_id: "run-last", version: 2,
      composer_state: status === "waiting_approval" ? "waiting_approval" :
        status === "running" ? "ready" : "successor_required",
      created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:01:00Z" },
    mission: { id: "mission-1", goal: "Stable task", profile: "review",
      workspace_id: "workspace-1", scope: { workspace_id: "workspace-1", network_mode: "disabled" },
      created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" },
    ...(status !== "failed" ? { active_run: run } : {}),
    last_run: run,
    runs: [{ run, ordinal: 1, created_at: "2026-08-24T00:00:00Z" }],
  } as ThreadDetailView;
}

function continuation(successor: boolean): ThreadMessageControlView {
  return {
    version: "thread_message_submission.v1",
    thread: { ...detail("failed").thread, active_run_id: successor ? "run-successor" : "run-last",
      last_run_id: successor ? "run-successor" : "run-last", version: 3 },
    run_id: successor ? "run-successor" : "run-last",
    session_id: successor ? "session-successor" : "session-last",
    ...(successor ? { predecessor_run_id: "run-last" } : {}),
    successor_created: successor,
    steering: { id: "steering-1", run_id: successor ? "run-successor" : "run-last",
      session_id: successor ? "session-successor" : "session-last", sequence: 1,
      content: "continue", status: "pending", prepared: false,
      created_at: "2026-08-24T00:02:00Z", updated_at: "2026-08-24T00:02:00Z" },
    replayed: false, execution_started: false, model_called: false, tool_called: false,
    capability_grant: false,
  } as ThreadMessageControlView;
}

function renderThread(current: ThreadDetailView, result: ThreadMessageControlView,
  resolvedRunStatus = "", transcriptItems: ThreadTranscriptItemView[] = [],
  approvalItems: ApprovalQueueItemView[] = [], hasThreadControl = true) {
  const submitThreadMessage = vi.fn().mockResolvedValue(result);
  const controlRunLifecycle = vi.fn().mockResolvedValue({ run: { status: "running" } });
  const executeRun = vi.fn().mockResolvedValue({ run_id: result.run_id });
  const get = vi.fn().mockImplementation((path: string) => {
    if (path.startsWith("/threads/")) return Promise.resolve(current);
    const selected = current.runs.find((binding) => binding.run.id === result.run_id)?.run ??
      current.last_run;
    return Promise.resolve({
      run: { ...selected, id: result.run_id,
        status: resolvedRunStatus || selected.status },
      operator_steering: { pending: 0, prepared: 0, committed: 0, cancelled: 0,
        messages: [] },
    });
  });
  const holdUntilAbort = (_runID: string, optionsOrSignal: { signal: AbortSignal } | AbortSignal) =>
    new Promise((_resolve, reject) => {
      const signal = optionsOrSignal instanceof AbortSignal ? optionsOrSignal : optionsOrSignal.signal;
      signal.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")),
        { once: true });
    });
  const client = {
    hasThreadControl, hasRunLifecycle: true, hasRunExecution: true,
    hasApprovalControl: true,
    get,
    getPage: vi.fn().mockResolvedValue({ items: transcriptItems,
      page: { limit: 100 }, requestID: "req" }),
    submitThreadMessage, controlRunLifecycle, executeRun,
    streamRunEvents: holdUntilAbort,
    getPublicModelStream: holdUntilAbort,
    approvalQueue: vi.fn().mockResolvedValue({ protocol_version: "approval_queue.v1",
      run_id: result.run_id, items: approvalItems, truncated: false,
      process_execution_enabled: false, session_grant_created: false, capability_grant: false }),
    decideApproval: vi.fn().mockResolvedValue({ status: "approved" }),
  } as unknown as CyberAgentClient;
  render(<QueryClientProvider client={new QueryClient()}>
    <ThreadWorkspace client={client} threadID="thread-1" />
  </QueryClientProvider>);
  return { get, submitThreadMessage, controlRunLifecycle, executeRun };
}

describe("ThreadWorkspace", () => {
  it("keeps the composer open on a terminal Run and starts its authority-free successor", async () => {
    const controls = renderThread(detail("failed"), continuation(true));
    const user = userEvent.setup();
    const composer = await screen.findByLabelText("Continue this Thread");
    expect(composer).toHaveAttribute("placeholder",
      "Type an instruction and continue this task");
    await user.type(composer, "continue");
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(controls.submitThreadMessage).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(controls.controlRunLifecycle).toHaveBeenCalledWith(
      "run-successor", { version: "run_lifecycle_control.v1", action: "start" },
      expect.stringMatching(/^web-thread-lifecycle-/u)));
    expect(controls.executeRun).toHaveBeenCalledWith("run-successor",
      { version: "run_execution_handoff.v1", max_steps: 1 },
      expect.stringMatching(/^web-thread-execution-/u));
  });

  it("queues input on the same waiting-approval Run without starting another Run", async () => {
    const approval = { id: "approval-1", proposal_id: "proposal-1", run_id: "run-last",
      session_id: "session-last", workspace_id: "workspace-1", tool_name: "replace_file",
      action_class: "workspace_write", mode: "per_call", status: "pending",
      allowed_actions: ["approve_once", "deny"], version: 1,
      created_at: "2026-08-24T00:01:00Z", updated_at: "2026-08-24T00:01:00Z",
      process_execution_enabled: false, capability_grant: false } as ApprovalQueueItemView;
    const controls = renderThread(detail("waiting_approval"), continuation(false), "", [],
      [approval]);
    const user = userEvent.setup();
    const composer = await screen.findByLabelText("Continue this Thread");
    await user.type(composer, "continue");
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(controls.submitThreadMessage).toHaveBeenCalledTimes(1));
    expect(controls.controlRunLifecycle).not.toHaveBeenCalled();
    expect(controls.executeRun).not.toHaveBeenCalled();
    expect(await screen.findByText("The message will queue until approval resolves"))
      .toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Approve once" })).toBeInTheDocument();
  });

  it("resolves a replayed concurrent successor before starting its Run", async () => {
    const replay = { ...continuation(true), predecessor_run_id: undefined,
      successor_created: false, replayed: true } as ThreadMessageControlView;
    const controls = renderThread(detail("failed"), replay, "created");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Continue this Thread"), "retry continuation");
    await user.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() => expect(controls.get).toHaveBeenCalledWith(
      "/runs/run-successor"));
    await waitFor(() => expect(controls.controlRunLifecycle).toHaveBeenCalledWith(
      "run-successor", { version: "run_lifecycle_control.v1", action: "start" },
      expect.stringMatching(/^web-thread-lifecycle-/u)));
    expect(controls.executeRun).toHaveBeenCalledWith("run-successor",
      { version: "run_execution_handoff.v1", max_steps: 1 },
      expect.stringMatching(/^web-thread-execution-/u));
  });

  it("sends with Enter while leaving Shift+Enter and Chinese IME composition untouched", async () => {
    const controls = renderThread(detail("failed"), continuation(true));
    const composer = await screen.findByLabelText("Continue this Thread");
    await userEvent.type(composer, "继续");
    expect(fireEvent.keyDown(composer, { key: "Enter", shiftKey: true })).toBe(true);
    expect(controls.submitThreadMessage).not.toHaveBeenCalled();
    expect(fireEvent.keyDown(composer, { key: "Enter", isComposing: true })).toBe(true);
    expect(controls.submitThreadMessage).not.toHaveBeenCalled();
    expect(fireEvent.keyDown(composer, { key: "Enter" })).toBe(false);
    await waitFor(() => expect(controls.submitThreadMessage).toHaveBeenCalledTimes(1));
  });

  it("shows submitted input while the one-step execution handoff is still pending", async () => {
    const controls = renderThread(detail("failed"), continuation(true));
    let finishExecution!: () => void;
    controls.executeRun.mockImplementationOnce(() => new Promise((resolve) => {
      finishExecution = () => resolve({ run_id: "run-successor" });
    }));
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Continue this Thread"),
      "visible while execution is pending");
    await user.click(screen.getByRole("button", { name: "Send" }));

    expect(await screen.findByRole("article", { name: "Operator: 用户消息已排队" }))
      .toHaveTextContent("visible while execution is pending");
    expect(controls.executeRun).toHaveBeenCalledTimes(1);
    finishExecution();
    await waitFor(() => expect(screen.getByLabelText("Continue this Thread")).toHaveValue(""));
  });

  it("exposes pause, structured tool state, delivery, and Composer on the primary Thread page", async () => {
    const transcript = [
      { version: "thread_transcript.v1", id: "tool-1", canonical_id: "item-1",
        run_id: "run-last", run_ordinal: 1, sequence: 8, activity_type: "edit",
        stage: "running", kind: "tool_call", source: "harness", title: "Tool running",
        tool_name: "replace_file", stream_item_id: "item-1", status: "running",
        verifiable: true, instruction_authorized: false, provisional: false, durable: true,
        created_at: "2026-08-24T00:01:00Z" },
      { version: "thread_transcript.v1", id: "delivery-1", canonical_id: "delivery-1",
        run_id: "run-last", run_ordinal: 1, sequence: 9, activity_type: "delivery",
        stage: "result", kind: "plan", source: "harness", title: "Delivery ready",
        detail: "Verified package", status: "completed", verifiable: true,
        instruction_authorized: false, provisional: false, durable: true,
        created_at: "2026-08-24T00:02:00Z" },
    ] as ThreadTranscriptItemView[];
    renderThread(detail("running"), continuation(false), "", transcript);

    await userEvent.click(await screen.findByText("Run and approval controls"));
    expect(await screen.findByRole("button", { name: "Pause" })).toBeInTheDocument();
    expect(await screen.findByText("replace_file")).toBeInTheDocument();
    expect(screen.getByText("Delivery ready")).toBeInTheDocument();
    expect(screen.getByLabelText("Continue this Thread")).toBeInTheDocument();
    expect(screen.getAllByText("Harness fact").length).toBeGreaterThan(0);
  });

  it("keeps a disabled composer visible with an explanation on a read-only connection", async () => {
    renderThread(detail("failed"), continuation(true), "", [], [], false);

    const composer = await screen.findByLabelText("Continue this Thread");
    expect(composer).toBeDisabled();
    expect(composer).toHaveAttribute("placeholder",
      "This connection is read-only and cannot send messages");
    expect(screen.getByText("Read-only connection: use Desktop control mode"))
      .toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
  });

  it("keeps a disabled composer visible with restore guidance for an archived Thread", async () => {
    const archived = detail("failed");
    archived.thread = { ...archived.thread, status: "archived", composer_state: "unavailable",
      archived_at: "2026-08-28T03:00:00Z" };
    renderThread(archived, continuation(true));

    const composer = await screen.findByLabelText("Continue this Thread");
    expect(composer).toBeDisabled();
    expect(composer).toHaveAttribute("placeholder",
      "This Thread is archived; restore it to continue");
    expect(screen.getByText("Restore this Thread to continue")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
  });
});
