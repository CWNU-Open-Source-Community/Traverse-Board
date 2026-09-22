import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { APIRequestError, type CyberAgentClient } from "../../api/client";
import type { V2FileReference } from "./file-context";
import type { PageResult, ThreadDetailView, ThreadExecutionView, ThreadTranscriptItemView, WorkspaceView } from "../../api/types";
import { v2QueryKeys } from "../query-keys";
import { V2Conversation } from "./conversation";

const composerFiles = vi.hoisted(() => ({ current: undefined as V2FileReference[] | undefined }));

vi.mock("../../hooks/use-run-event-stream", () => ({
  useRunEventStream: () => ({ error: null, frames: [] }),
}));

vi.mock("../../hooks/use-public-model-stream", () => ({
  usePublicModelStream: () => ({ error: null, snapshot: null, status: "idle" }),
}));

vi.mock("../projection/narrative", () => ({
  projectThreadNarrative: (items: ThreadTranscriptItemView[]) => items.map((item) => ({
    id: item.id,
    kind: item.source === "operator" ? "user" : "assistant",
    text: item.detail ?? item.title,
    createdAt: item.created_at,
  })),
}));

vi.mock("./composer", () => ({
  V2Composer: ({ disabled, onSubmit, threadID, fileReferenceUnavailableReason }: {
    disabled: boolean;
    onSubmit: (content: string, files?: V2FileReference[]) => Promise<void>;
    threadID: string;
    fileReferenceUnavailableReason?: string;
  }) => <button data-file-reference-unavailable={fileReferenceUnavailableReason}
    disabled={disabled} onClick={() => void onSubmit(`pending-${threadID}`, composerFiles.current).catch(() => undefined)} type="button">
    发送 {threadID}
  </button>,
}));

afterEach(() => { cleanup(); composerFiles.current = undefined; });

const workspaces = [{ id: "workspace-1", name: "Workspace 1" }] as WorkspaceView[];

function detail(threadID: string): ThreadDetailView {
  return {
    thread: {
      id: threadID,
      title: `Title ${threadID}`,
      status: "active",
      workspace_id: "workspace-1",
      composer_state: "ready",
    },
    last_run: { id: `run-${threadID}`, status: "completed" },
    runs: [],
    mission: {},
  } as unknown as ThreadDetailView;
}

function transcriptItem(id: string, detailText: string, sequence: number): ThreadTranscriptItemView {
  return {
    activity_type: "message",
    canonical_id: `canonical-${id}`,
    created_at: new Date(sequence * 1_000).toISOString(),
    detail: detailText,
    durable: true,
    id,
    instruction_authorized: false,
    kind: "model_update",
    provisional: false,
    run_id: "run-thread-a",
    run_ordinal: 1,
    sequence,
    source: "model",
    stage: "result",
    title: detailText,
    verifiable: true,
    version: "thread_transcript.v1",
  };
}

function page(items: ThreadTranscriptItemView[], nextCursor = ""): PageResult<ThreadTranscriptItemView> {
  return {
    items,
    page: { limit: 100, ...(nextCursor ? { next_cursor: nextCursor } : {}) },
    requestID: "request-1",
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

function renderConversation(client: CyberAgentClient, initialThreadID = "thread-a", extras?: ReactNode) {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false, staleTime: Number.POSITIVE_INFINITY },
  } });
  const props = (threadID: string) => <QueryClientProvider client={queryClient}>
    {extras}
    <V2Conversation client={client} onArchive={vi.fn()} onManageModels={vi.fn()}
      onOpenInspector={vi.fn()} threadID={threadID} workspaces={workspaces} />
  </QueryClientProvider>;
  const view = render(props(initialThreadID));
  return { ...view, queryClient,
    rerenderThread: (threadID: string) => view.rerender(props(threadID)) };
}

function baseClient(overrides: Partial<CyberAgentClient> = {}): CyberAgentClient {
  return {
    get: vi.fn((path: string) => {
      if (path.endsWith("/agent-browser")) return Promise.reject(new APIRequestError("Route unavailable", "NOT_FOUND", 404));
      const match = path.match(/^\/threads\/([^/]+)$/u);
      return Promise.resolve(detail(decodeURIComponent(match?.[1] ?? "missing")));
    }),
    getPage: vi.fn(() => Promise.resolve(page([]))),
    hasThreadControl: true,
    submitThreadTurn: vi.fn(() => Promise.resolve({ steering: { id: "steering-1" } })),
    ...overrides,
  } as unknown as CyberAgentClient;
}

describe("V2Conversation", () => {
  it("explains unavailable execution observation while retaining readable work history", async () => {
    const threadExecution = vi.fn();
    renderConversation(baseClient({ hasThreadExecutionRead: false, threadExecution,
      getPage: async <T,>() => page([transcriptItem("history", "Persisted work remains readable", 1)]) as PageResult<T>,
    }));
    expect(await screen.findByText("当前连接不提供 Agent 活动状态。对话与工作记录仍可查看。")).toBeInTheDocument();
    expect(await screen.findByText("Persisted work remains readable")).toBeInTheDocument();
    expect(threadExecution).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "刷新执行状态" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "停止当前执行" })).not.toBeInTheDocument();
  });

  it("replaces a terminal file rejection with a new turn after references are removed", async () => {
    const submitThreadTurn = vi.fn().mockRejectedValueOnce(new APIRequestError("File changed", "CONFLICT", 409, "rejected", false))
      .mockResolvedValue({ steering: { id: "steering-corrected" } });
    composerFiles.current = [{ id: "reference-1", path: "README.md", digest: "a".repeat(64), partial: false, redacted: false }];
    const view = renderConversation(baseClient({ submitThreadTurn }));
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "发送 thread-a" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("这条消息未入队");
    composerFiles.current = undefined;
    await user.click(screen.getByRole("button", { name: "发送 thread-a" }));
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    expect(submitThreadTurn).toHaveBeenCalledTimes(2);
    expect(submitThreadTurn.mock.calls[0]![1]).toMatchObject({ files: [
      { source_kind: "workspace_file", path: "README.md", expected_sha256: "a".repeat(64) }] });
    expect(submitThreadTurn.mock.calls[1]![1]).toEqual({ version: "thread_message_submission.v1", content: "pending-thread-a" });
    expect(submitThreadTurn.mock.calls[1]![2]).not.toBe(submitThreadTurn.mock.calls[0]![2]);
    expect(view.container.querySelector(".v2-user-turn")).not.toBeInTheDocument();
  });

  it("rechecks an unknown turn with the original key and files before allowing a correction", async () => {
    const submitThreadTurn = vi.fn().mockRejectedValueOnce(new Error("response lost"))
      .mockRejectedValueOnce(new APIRequestError("File intent rejected", "CONFLICT", 409, "confirmed", false))
      .mockResolvedValue({ steering: { id: "steering-corrected" } });
    composerFiles.current = [{ id: "reference-old", path: "README.md", digest: "a".repeat(64), partial: false, redacted: false }];
    renderConversation(baseClient({ submitThreadTurn }));
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "发送 thread-a" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("这条消息未入队"));
    expect(submitThreadTurn).toHaveBeenCalledTimes(2);
    expect(submitThreadTurn.mock.calls[1]).toEqual(submitThreadTurn.mock.calls[0]);
    composerFiles.current = [{ ...composerFiles.current[0]!, id: "reference-new", digest: "b".repeat(64) }];
    await user.click(screen.getByRole("button", { name: "发送 thread-a" }));
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    expect(submitThreadTurn).toHaveBeenCalledTimes(3);
    expect(submitThreadTurn.mock.calls[2]![2]).not.toBe(submitThreadTurn.mock.calls[1]![2]);
    expect(submitThreadTurn.mock.calls[2]![1]).toMatchObject({ files: [{ expected_sha256: "b".repeat(64) }] });
  });

  it("allows references for a successor and explains the boundary only while execution owns the task", async () => {
    const restoredDetail = { ...detail("thread-a"), thread: { ...detail("thread-a").thread,
      composer_state: "successor_required" }, last_run: { id: "run-thread-a", status: "cancelled" } } as ThreadDetailView;
    const view = renderConversation(baseClient({
      get: async <T,>() => restoredDetail as T,
    }));
    const composer = await screen.findByRole("button", { name: "发送 thread-a" });
    expect(composer).not.toHaveAttribute("data-file-reference-unavailable");
    await act(async () => view.queryClient.setQueryData(v2QueryKeys.thread("thread-a"), {
      ...restoredDetail, thread: { ...restoredDetail.thread, composer_state: "ready" },
      active_run: { id: "successor-run", status: "running" },
    }));
    await waitFor(() => expect(composer).toHaveAttribute("data-file-reference-unavailable", expect.stringContaining("活动状态尚未确认")));
    await act(async () => view.queryClient.setQueryData(v2QueryKeys.thread("thread-a"), restoredDetail));
    await waitFor(() => expect(composer).not.toHaveAttribute("data-file-reference-unavailable"));
  });

  it("does not claim cached activity in file guidance after a failed status read", async () => {
    const execution: ThreadExecutionView = { version: "thread_execution.v1", thread_id: "thread-a",
      state: "running", queued_messages: 0, capability_grant: false };
    const threadExecution = vi.fn().mockResolvedValue(execution);
    const view = renderConversation(baseClient({ hasThreadExecutionRead: true, threadExecution }));
    await screen.findByText("正在工作");
    const composer = screen.getByRole("button", { name: "发送 thread-a" });
    expect(composer).toHaveAttribute("data-file-reference-unavailable", expect.stringContaining("项目内文件引用需等执行结束"));
    threadExecution.mockRejectedValue(new Error("execution read failed"));
    await act(async () => { await view.queryClient.invalidateQueries({ queryKey: v2QueryKeys.execution("thread-a") }); });
    expect(await screen.findByText("状态读取失败")).toBeInTheDocument();
    expect(composer).toHaveAttribute("data-file-reference-unavailable", expect.stringContaining("活动状态尚未确认"));
    expect(composer).toBeEnabled();
    expect(screen.queryByText("正在工作")).not.toBeInTheDocument();
    threadExecution.mockResolvedValue({ ...execution, state: "idle" });
    await userEvent.setup().click(screen.getByRole("button", { name: "刷新执行状态" }));
    await screen.findByText("等待新消息");
    expect(composer).not.toHaveAttribute("data-file-reference-unavailable");
  });

  it("restores actual execution control on reopen and waits for confirmed stopping", async () => {
    let execution: ThreadExecutionView = { version: "thread_execution.v1", thread_id: "thread-a",
      execution_id: "thread-execution-a", state: "running", queued_messages: 0, capability_grant: false };
    const interruptThread = vi.fn(async () => { execution = { ...execution, state: "stopping" }; return execution; });
    const client = baseClient({ hasRunExecution: true, hasThreadExecutionRead: true,
      threadExecution: vi.fn(async () => execution), interruptThread });
    const view = renderConversation(client);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "停止当前执行" }));
    expect(interruptThread).toHaveBeenCalledWith("thread-a", "thread-execution-a", expect.any(String));
    expect(await screen.findByRole("button", { name: "正在停止" })).toBeDisabled();
    await act(async () => {
      execution = { version: "thread_execution.v1", thread_id: "thread-a", state: "idle",
        queued_messages: 0, capability_grant: false };
      view.queryClient.setQueryData(v2QueryKeys.execution("thread-a"), execution);
    });
    await waitFor(() => expect(screen.queryByRole("button", { name: "正在停止" })).not.toBeInTheDocument());
    expect(screen.queryByText("正在工作")).not.toBeInTheDocument();
  });

  it("keeps the reader's scroll position when new content arrives and restores it on return", async () => {
    const view = renderConversation(baseClient());
    await screen.findByText("Title thread-a");
    await waitFor(() => expect(view.queryClient.getQueryState(v2QueryKeys.transcript("thread-a"))?.status).toBe("success"));
    const scroller = view.container.querySelector<HTMLDivElement>(".v2-conversation-scroll")!;
    Object.defineProperties(scroller, { scrollHeight: { value: 2000, configurable: true },
      clientHeight: { value: 500, configurable: true } });
    scroller.scrollTop = 180;
    fireEvent.scroll(scroller);
    await act(async () => view.queryClient.setQueryData(v2QueryKeys.transcript("thread-a"), {
      pages: [page([transcriptItem("new", "new content", 1)])], pageParams: [""],
    }));
    expect(await screen.findByText("new content")).toBeInTheDocument();
    expect(scroller.scrollTop).toBe(180);
    expect(await screen.findByRole("button", { name: "有新内容 · 回到最新" })).toBeInTheDocument();
    view.rerenderThread("thread-b");
    await screen.findByText("Title thread-b");
    view.rerenderThread("thread-a");
    await screen.findByText("Title thread-a");
    expect(view.container.querySelector<HTMLDivElement>(".v2-conversation-scroll")!.scrollTop).toBe(180);
  });

  it("keeps a decided web approval recoverable while the Run is already running", async () => {
    const runningDetail = { ...detail("thread-a"),
      active_run: { id: "run-thread-a", status: "running" } } as ThreadDetailView;
    const client = baseClient({
      hasApprovalControl: true,
      get: vi.fn(() => Promise.resolve(runningDetail)),
      approvalQueue: vi.fn(() => Promise.resolve({
        protocol_version: "approval_queue.v1", run_id: "run-thread-a", truncated: false,
        process_execution_enabled: false, session_grant_created: false,
        capability_grant: false, items: [{
          id: "approval-web-recovery", proposal_id: "web-fetch-authorization-recovery",
          run_id: "run-thread-a", session_id: "session-thread-a", workspace_id: "",
          tool_name: "web_fetch", action_class: "public_https_fetch", mode: "per_call",
          status: "approved", allowed_actions: ["approve_once"],
          canonical_url: "https://arxiv.org/abs/2608.13637", exact_target: "arxiv.org",
          version: 2, created_at: "2026-09-02T00:00:00Z",
          updated_at: "2026-09-02T00:00:01Z", process_execution_enabled: false,
          capability_grant: false,
        }],
      })),
      decideApproval: vi.fn(),
    } as Partial<CyberAgentClient>);

    renderConversation(client);

    expect(await screen.findByText("恢复上次网页读取")).toBeInTheDocument();
    expect(screen.getByText("已允许，等待恢复")).toBeInTheDocument();
  });

  it("keeps a failed Thread sendable without exposing manual Run controls", async () => {
    const failedDetail = { ...detail("thread-a"), recovery: {
      version: "thread_run_recovery.v1",
      run_id: "run-thread-a",
      handoff_operation_id: "handoff-thread-a",
      error_code: "failed_precondition",
      stop_reason: "failed_precondition",
      detail: "上一次执行已经停止，下一条消息会自动继续。",
      quiescent: true,
      failed_at: "2026-09-01T00:00:00Z",
    } } as ThreadDetailView;
    const client = baseClient({
      get: vi.fn(() => Promise.resolve(failedDetail)),
    } as Partial<CyberAgentClient>);

    renderConversation(client);

    expect(await screen.findByRole("button", { name: "发送 thread-a" })).toBeInTheDocument();
    expect(screen.queryByText("可继续")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "发送 thread-a" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: /结束旧 Run/u })).not.toBeInTheDocument();
  });

  it("describes an unconfirmed stop without claiming to cancel accepted input", async () => {
    const current = detail("thread-a");
    current.last_run.session_id = "session-thread-a";
    renderConversation(baseClient({ hasThreadExecutionRead: true,
      threadExecution: vi.fn(async () => ({ state: "stop_failed", queued_messages: 0 } as ThreadExecutionView)),
      get: vi.fn(async (path: string) => path.endsWith("/queued-messages") ? {
        version: "thread_queued_messages.v1", thread_id: "thread-a", run_id: "run-thread-a", session_id: "session-thread-a",
        pending: 1, prepared: 0, capability_grant: false, items: [{ id: "message-pending", sequence: 1, status: "pending", prepared: false,
          content: "停止后仍保留的要求", content_sha256: "a".repeat(64), content_redacted: false, revision: 0,
          created_at: "2026-09-22T01:00:00Z", images: [], attachments: [], can_edit: false, can_cancel: false }],
      } : current) as CyberAgentClient["get"],
    }));
    expect(await screen.findByText("停止尚未完成。请重试停止，确认后再发送；已受理的要求会保留。")).toBeInTheDocument();
    expect(await screen.findByText("待处理 1 条")).toBeInTheDocument();
    expect(screen.queryByText(/排队消息的取消/u)).not.toBeInTheDocument();
  });

  it("retains the exact failure reason in conversation details", async () => {
    const reason = new APIRequestError("Recorded provider failure at model call call-17", "UNAVAILABLE", 503,
      "request-failed", undefined, undefined, true);
    renderConversation(baseClient({ submitThreadTurn: vi.fn().mockRejectedValue(reason) }));
    fireEvent.click(await screen.findByRole("button", { name: "发送 thread-a" }));
    const originalReason = await screen.findByText(reason.message);
    expect(originalReason.closest("details")).toHaveTextContent("查看原因");
    expect(screen.queryByRole("button", { name: "重试核对" })).not.toBeInTheDocument();
  });

  it("isolates optimistic sends and their async completion by Thread", async () => {
    const submission = deferred<Awaited<ReturnType<CyberAgentClient["submitThreadTurn"]>>>();
    const submitThreadTurn = vi.fn(() => submission.promise);
    const client = baseClient({ submitThreadTurn } as Partial<CyberAgentClient>);
    const user = userEvent.setup();
    const view = renderConversation(client);

    await screen.findByText("Title thread-a");
    await user.click(screen.getByRole("button", { name: "发送 thread-a" }));
    expect(screen.getByText("pending-thread-a")).toBeInTheDocument();
    expect(submitThreadTurn).toHaveBeenCalledWith("thread-a", expect.anything(), expect.any(String));

    view.rerenderThread("thread-b");
    await screen.findByText("Title thread-b");
    expect(screen.queryByText("pending-thread-a")).not.toBeInTheDocument();

    await act(async () => {
      submission.resolve({ steering: { id: "steering-a" } } as Awaited<
        ReturnType<CyberAgentClient["submitThreadTurn"]>>);
      await submission.promise;
    });
    expect(screen.queryByText("pending-thread-a")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "发送 thread-b" })).toBeInTheDocument();
  });

  it("distinguishes an in-flight submission from Agent execution and consumes the completed Thread projection", async () => {
    const submission = deferred<Awaited<ReturnType<CyberAgentClient["submitThreadTurn"]>>>();
    const client = baseClient({
      get: vi.fn(() => Promise.resolve({ ...detail("thread-a"),
        active_run: { id: "run-thread-a", status: "running" } })),
      submitThreadTurn: vi.fn(() => submission.promise),
    } as Partial<CyberAgentClient>);
    const user = userEvent.setup();
    const view = renderConversation(client);

    await screen.findByText("Title thread-a");
    expect(screen.queryByText("正在工作")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "发送 thread-a" }));
    expect(await screen.findByText("正在发送消息")).toBeInTheDocument();
    expect(screen.queryByText("正在工作")).not.toBeInTheDocument();
    expect(screen.getByText("可以补充要求，已受理的消息会提供给下一次模型调用；受理不代表已执行，停止后仍会保留。")).toBeInTheDocument();
    expect(screen.queryByText(/停止会取消/u)).not.toBeInTheDocument();

    const completed = detail("thread-a").thread;
    await act(async () => {
      submission.resolve({ steering: { id: "steering-a" }, thread: completed } as Awaited<
        ReturnType<CyberAgentClient["submitThreadTurn"]>>);
      await submission.promise;
    });
    await waitFor(() => expect(screen.queryByText("正在发送消息")).not.toBeInTheDocument());
    expect(view.queryClient.getQueryData<ThreadDetailView>(v2QueryKeys.thread("thread-a"))?.thread)
      .toEqual(completed);
  });

  it("shows confirmed Agent activity and stopping while the submission response is still pending", async () => {
    const submission = deferred<Awaited<ReturnType<CyberAgentClient["submitThreadTurn"]>>>();
    const execution: ThreadExecutionView = { version: "thread_execution.v1", thread_id: "thread-a",
      state: "idle", queued_messages: 0, capability_grant: false };
    const view = renderConversation(baseClient({ hasThreadExecutionRead: true,
      threadExecution: vi.fn(async () => execution), submitThreadTurn: vi.fn(() => submission.promise) }));
    await screen.findByText("等待新消息");
    await userEvent.setup().click(screen.getByRole("button", { name: "发送 thread-a" }));
    expect(await screen.findByText("正在发送消息")).toBeInTheDocument();
    await act(async () => { view.queryClient.setQueryData(v2QueryKeys.execution("thread-a"),
      { ...execution, state: "running", execution_id: "accepted-execution" }); });
    expect(await screen.findByText("正在工作")).toBeInTheDocument();
    expect(screen.queryByText("正在发送消息")).not.toBeInTheDocument();
    await act(async () => { view.queryClient.setQueryData(v2QueryKeys.execution("thread-a"),
      { ...execution, state: "stopping", execution_id: "accepted-execution" }); });
    expect(await screen.findByText("正在停止")).toBeInTheDocument();
    await act(async () => { submission.resolve({ steering: { id: "steering-a" } } as Awaited<
      ReturnType<CyberAgentClient["submitThreadTurn"]>>); await submission.promise; });
  });

  it("pages toward older transcript records without reversing or duplicating the timeline", async () => {
    const middle = transcriptItem("middle", "middle message", 2);
    const getPage = vi.fn((_path: string, _query: unknown, cursor: string) => Promise.resolve(
      cursor === "older-cursor"
        ? page([transcriptItem("oldest", "oldest message", 1), middle])
        : page([middle, transcriptItem("newest", "newest message", 3)], "older-cursor"),
    ));
    const client = baseClient({ getPage } as Partial<CyberAgentClient>);
    const user = userEvent.setup();
    const view = renderConversation(client);

    await screen.findByText("newest message");
    expect(Array.from(view.container.querySelectorAll(".v2-assistant-turn"))
      .map((element) => element.textContent)).toEqual(["middle message", "newest message"]);

    await user.click(screen.getByRole("button", { name: "加载更早记录" }));
    await screen.findByText("oldest message");
    expect(getPage).toHaveBeenCalledWith(expect.stringContaining("/thread-a/transcript"),
      { limit: 100 }, "older-cursor", expect.any(AbortSignal));
    expect(Array.from(view.container.querySelectorAll(".v2-assistant-turn"))
      .map((element) => element.textContent)).toEqual([
      "oldest message", "middle message", "newest message",
    ]);
    expect(screen.queryByRole("button", { name: "加载更早记录" })).not.toBeInTheDocument();
  });

  it("closes the title menu with Escape or an outside click and restores focus predictably", async () => {
    const onArchive = vi.fn();
    const client = baseClient();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const user = userEvent.setup();
    render(<><button type="button">Outside</button><QueryClientProvider client={queryClient}>
      <V2Conversation client={client} onArchive={onArchive} onManageModels={vi.fn()}
        onOpenInspector={vi.fn()} threadID="thread-a" workspaces={workspaces} />
    </QueryClientProvider></>);
    await screen.findByText("Title thread-a");
    const trigger = screen.getByRole("button", { name: "对话操作" });

    await user.click(trigger);
    expect(screen.getByRole("menuitem", { name: "归档对话" })).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();

    await user.click(trigger);
    await user.click(screen.getByRole("button", { name: "Outside" }));
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Outside" })).toHaveFocus();

    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: "归档对话" }));
    expect(onArchive).toHaveBeenCalledTimes(1);
    expect(trigger).toHaveFocus();
    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
  });
});
