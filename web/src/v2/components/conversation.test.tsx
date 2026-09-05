import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CyberAgentClient } from "../../api/client";
import type { PageResult, ThreadDetailView, ThreadTranscriptItemView, WorkspaceView } from "../../api/types";
import { v2QueryKeys } from "../query-keys";
import { V2Conversation } from "./conversation";

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
  V2Composer: ({ disabled, onSubmit, threadID }: {
    disabled: boolean;
    onSubmit: (content: string) => Promise<void>;
    threadID: string;
  }) => <button disabled={disabled} onClick={() => void onSubmit(`pending-${threadID}`)} type="button">
    发送 {threadID}
  </button>,
}));

afterEach(() => cleanup());

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

    expect(await screen.findByText("可继续")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "发送 thread-a" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: /结束旧 Run/u })).not.toBeInTheDocument();
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

  it("shows working only for a live Turn and consumes the completed Thread projection", async () => {
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
    expect(await screen.findByText("正在工作")).toBeInTheDocument();

    const completed = detail("thread-a").thread;
    await act(async () => {
      submission.resolve({ steering: { id: "steering-a" }, thread: completed } as Awaited<
        ReturnType<CyberAgentClient["submitThreadTurn"]>>);
      await submission.promise;
    });
    await waitFor(() => expect(screen.queryByText("正在工作")).not.toBeInTheDocument());
    expect(view.queryClient.getQueryData<ThreadDetailView>(v2QueryKeys.thread("thread-a"))?.thread)
      .toEqual(completed);
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
