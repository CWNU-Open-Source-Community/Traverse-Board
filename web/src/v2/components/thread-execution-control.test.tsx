import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { APIRequestError, CyberAgentClient } from "../../api/client";
import type { ThreadExecutionView } from "../../api/types";
import { v2QueryKeys } from "../query-keys";
import { useV2ThreadExecution, V2PausedThreadControl, V2ThreadExecutionControl } from "./thread-execution-control";

function ObservedControl({ client }: { client: CyberAgentClient }) {
  const query = useV2ThreadExecution(client, "thread-a");
  return <><output>{query.data?.state ?? "unavailable"}</output>
    <V2ThreadExecutionControl client={client} threadID="thread-a" execution={query.data} /></>;
}

it("only lifts a pause on an explicit click without resubmitting the failed tool or a message", async () => {
  const controlRunLifecycle = vi.fn().mockRejectedValueOnce(new Error("connection interrupted"))
    .mockResolvedValue({});
  const submitThreadTurn = vi.fn();
  const client = { hasRunLifecycle: true, controlRunLifecycle, submitThreadTurn } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  const view = render(<QueryClientProvider client={queryClient}>
    <V2PausedThreadControl client={client} threadID="thread-a" runID="run-a" />
  </QueryClientProvider>);
  try {
    const user = userEvent.setup();
    const button = screen.getByRole("button", { name: "解除暂停" });
    expect(screen.getByRole("status")).toHaveTextContent("本轮已暂停，可发送新消息继续。");
    expect(button).toHaveAttribute("title", expect.stringContaining("不会重试失败工具"));
    expect(controlRunLifecycle).not.toHaveBeenCalled();
    await user.click(button);
    expect(await screen.findByRole("alert")).toHaveTextContent("解除暂停未确认，可重试：connection interrupted");
    expect(controlRunLifecycle).toHaveBeenCalledWith("run-a",
      { version: "run_lifecycle_control.v1", action: "resume" }, expect.stringMatching(/^v2-resume-/));
    await user.click(button);
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    expect(controlRunLifecycle).toHaveBeenCalledTimes(2);
    expect(controlRunLifecycle.mock.calls[1]).toEqual(controlRunLifecycle.mock.calls[0]);
    expect(submitThreadTurn).not.toHaveBeenCalled();
  } finally {
    view.unmount();
    queryClient.clear();
  }
});

it("does not request or poll an unavailable execution route even with a control token", async () => {
  vi.useFakeTimers();
  const client = new CyberAgentClient("read", "/api/v1", "control", {
    runExecutionEnabled: true, threadExecutionReadEnabled: false,
  });
  const read = vi.spyOn(client, "threadExecution").mockRejectedValue(new Error("route unavailable"));
  const queryClient = new QueryClient();
  const view = render(<QueryClientProvider client={queryClient}><ObservedControl client={client} /></QueryClientProvider>);
  try {
    await act(async () => { await vi.advanceTimersByTimeAsync(1_800); });
    expect(read).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "停止当前执行" })).not.toBeInTheDocument();
  } finally {
    view.unmount();
    queryClient.clear();
    vi.useRealTimers();
  }
});

it("keeps observing a supported execution route with only the read token and cannot stop it", async () => {
  const client = new CyberAgentClient("read", "/api/v1", "", {
    runExecutionEnabled: true, threadExecutionReadEnabled: true, threadControlEnabled: true,
  });
  const read = vi.spyOn(client, "threadExecution").mockResolvedValueOnce(execution("thread-a"))
    .mockResolvedValue(execution("thread-a", "idle"));
  const stop = vi.spyOn(client, "interruptThread");
  const queryClient = new QueryClient();
  const view = render(<QueryClientProvider client={queryClient}><ObservedControl client={client} /></QueryClientProvider>);
  try {
    await screen.findByText("running");
    expect(screen.queryByRole("button", { name: "停止当前执行" })).not.toBeInTheDocument();
    await screen.findByText("idle");
    expect(read).toHaveBeenCalledTimes(2);
    expect(stop).not.toHaveBeenCalled();
  } finally {
    view.unmount();
    queryClient.clear();
  }
});

it("stops automatic polling if a previously advertised execution route returns 404", async () => {
  vi.useFakeTimers();
  const client = new CyberAgentClient("read", "/api/v1", "", { threadExecutionReadEnabled: true });
  const read = vi.spyOn(client, "threadExecution").mockRejectedValue(new APIRequestError("Not found", "NOT_FOUND", 404));
  const queryClient = new QueryClient();
  const view = render(<QueryClientProvider client={queryClient}><ObservedControl client={client} /></QueryClientProvider>);
  try {
    await act(async () => { await vi.advanceTimersByTimeAsync(1_800); });
    expect(read).toHaveBeenCalledTimes(1);
    expect(queryClient.getQueryState(v2QueryKeys.execution("thread-a"))?.status).toBe("error");
    expect(screen.queryByRole("button", { name: "停止当前执行" })).not.toBeInTheDocument();
  } finally {
    view.unmount();
    queryClient.clear();
    vi.useRealTimers();
  }
});

function execution(threadID: string, state: "running" | "idle" = "running") {
  return { version: "thread_execution.v1", thread_id: threadID,
    execution_id: state === "running" ? `execution-${threadID}` : undefined,
    state, queued_messages: 0, capability_grant: false, last_turn_interrupted: state === "idle" } as ThreadExecutionView;
}

it.each(["success", "failure"] as const)("keeps a delayed stop %s bound to its submitted task after navigation", async (outcome) => {
  let finish!: (value: ThreadExecutionView) => void;
  let fail!: (reason: Error) => void;
  const pending = new Promise<ThreadExecutionView>((resolve, reject) => { finish = resolve; fail = reject; });
  const interruptThread = vi.fn(() => pending);
  const client = { hasThreadControl: true, hasRunExecution: true, interruptThread } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const stateA = execution("thread-a");
  const stateB = execution("thread-b");
  queryClient.setQueryData(v2QueryKeys.execution("thread-a"), stateA);
  queryClient.setQueryData(v2QueryKeys.execution("thread-b"), stateB);
  function Control({ threadID }: { threadID: string }) {
    const query = useQuery<ThreadExecutionView>({ queryKey: v2QueryKeys.execution(threadID),
      queryFn: async () => execution(threadID), enabled: false });
    return <V2ThreadExecutionControl client={client} threadID={threadID} execution={query.data} />;
  }
  const content = (threadID: string) => <QueryClientProvider client={queryClient}><Control threadID={threadID} /></QueryClientProvider>;
  const view = render(content("thread-a"));
  await userEvent.setup().click(screen.getByRole("button", { name: "停止当前执行" }));
  await waitFor(() => expect(interruptThread).toHaveBeenCalledWith("thread-a", "execution-thread-a", "v2-thread-stop-execution-thread-a"));
  view.rerender(content("thread-b"));
  await waitFor(() => expect(screen.getByRole("button", { name: "停止当前执行" })).toBeEnabled());
  await act(async () => {
    if (outcome === "success") finish(execution("thread-a", "idle"));
    else fail(new Error("Task A connection interrupted"));
    await pending.catch(() => undefined);
  });
  await waitFor(() => expect(queryClient.getQueryState(v2QueryKeys.execution("thread-a"))?.isInvalidated).toBe(true));
  expect(queryClient.getQueryData(v2QueryKeys.execution("thread-b"))).toEqual(stateB);
  expect(queryClient.getQueryState(v2QueryKeys.execution("thread-b"))?.isInvalidated).toBe(false);
  expect(queryClient.getQueryData(v2QueryKeys.execution("thread-a"))).toEqual(outcome === "success" ? execution("thread-a", "idle") : stateA);
  expect(screen.getByRole("button", { name: "停止当前执行" })).toBeEnabled();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  view.unmount();
  queryClient.clear();
});
