import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import type { ThreadDetailView, ThreadMessageControlView } from "../api/types";
import { ThreadWorkspace } from "./thread-workspace";

function detail(status: "failed" | "waiting_approval"): ThreadDetailView {
  const run = {
    id: "run-last", mission_id: "mission-1", session_id: "session-last", status,
    config: { model_route: "review", interactive: true },
    budget: { max_turns: 4, max_tool_calls: 8 },
    created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:01:00Z",
  };
  return {
    thread: { id: "thread-1", protocol_version: "thread.v1", workspace_id: "workspace-1",
      mission_id: "mission-1", title: "Stable task", status: "active",
      ...(status === "waiting_approval" ? { active_run_id: "run-last" } : {}),
      last_run_id: "run-last", version: 2,
      composer_state: status === "waiting_approval" ? "waiting_approval" : "successor_required",
      created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:01:00Z" },
    mission: { id: "mission-1", goal: "Stable task", profile: "review",
      workspace_id: "workspace-1", scope: { workspace_id: "workspace-1", network_mode: "disabled" },
      created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" },
    ...(status === "waiting_approval" ? { active_run: run } : {}),
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
  resolvedRunStatus = "") {
  const submitThreadMessage = vi.fn().mockResolvedValue(result);
  const controlRunLifecycle = vi.fn().mockResolvedValue({ run: { status: "running" } });
  const executeRun = vi.fn().mockResolvedValue({ run_id: result.run_id });
  const get = vi.fn().mockResolvedValue(current);
  if (resolvedRunStatus) {
    get.mockResolvedValueOnce(current).mockResolvedValueOnce({
      run: { id: result.run_id, status: resolvedRunStatus },
    });
  }
  const client = {
    hasThreadControl: true, hasRunLifecycle: true, hasRunExecution: true,
    get,
    getPage: vi.fn().mockResolvedValue({ items: [], page: { limit: 100 }, requestID: "req" }),
    submitThreadMessage, controlRunLifecycle, executeRun,
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
    const composer = await screen.findByLabelText("Continue this task");
    expect(composer).toHaveAttribute("placeholder",
      "This Run ended; sending creates a safe successor Run");
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
    const controls = renderThread(detail("waiting_approval"), continuation(false));
    const user = userEvent.setup();
    const composer = await screen.findByLabelText("Continue this task");
    await user.type(composer, "continue");
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(controls.submitThreadMessage).toHaveBeenCalledTimes(1));
    expect(controls.controlRunLifecycle).not.toHaveBeenCalled();
    expect(controls.executeRun).not.toHaveBeenCalled();
    expect(await screen.findByText("The message will queue until approval resolves"))
      .toBeInTheDocument();
  });

  it("resolves a replayed concurrent successor before starting its Run", async () => {
    const replay = { ...continuation(true), predecessor_run_id: undefined,
      successor_created: false, replayed: true } as ThreadMessageControlView;
    const controls = renderThread(detail("failed"), replay, "created");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Continue this task"), "retry continuation");
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
});
