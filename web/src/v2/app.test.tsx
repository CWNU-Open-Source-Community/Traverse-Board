import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { APIRequestError, type CyberAgentClient } from "../api/client";
import type { ThreadView, WorkspaceView } from "../api/types";
import { useConnectionStore } from "../state/connection";
import { V2Workbench } from "./app";
import * as desktopBridge from "../lib/desktop-bridge";
import { v2FileReferenceKey } from "./components/file-context";

const openLegacyInspector = vi.hoisted(() => vi.fn());

vi.mock("../legacy-route", async (importOriginal) => ({
  ...await importOriginal<typeof import("../legacy-route")>(),
  openLegacyInspector,
}));

vi.mock("./components/conversation", () => ({
  V2Conversation: ({ threadID, view = "conversation", draft, onDraftChange }: {
    threadID: string;
    view?: "conversation" | "inspector";
    draft: string;
    onDraftChange: (value: string) => void;
  }) => (
    <div data-testid="v2-conversation" data-view={view}>{threadID}
      <textarea aria-label="任务草稿 fixture" value={draft} onChange={(event) => onDraftChange(event.target.value)} />
    </div>
  ),
}));

const workspace: WorkspaceView = {
  id: "workspace-first-turn",
  name: "First turn workspace",
  created_at: "2026-08-29T00:00:00Z",
};

const createdThread: ThreadView = {
  id: "thread-created-from-first-turn",
  title: "First turn",
  workspace_id: workspace.id,
  mission_id: "mission-created-from-first-turn",
  active_run_id: "run-created-from-first-turn",
  last_run_id: "run-created-from-first-turn",
  protocol_version: "thread.v1",
  composer_state: "ready",
  status: "active",
  version: 1,
  created_at: "2026-08-29T00:00:00Z",
  updated_at: "2026-08-29T00:00:00Z",
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

afterEach(() => {
  cleanup();
  useConnectionStore.getState().disconnect();
  openLegacyInspector.mockClear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

function navigationClient() {
  return { hasThreadControl: true,
    getPage: vi.fn(async (path: string) => ({ items: path === "/workspaces" ? [workspace] : [createdThread],
      page: { limit: 100 }, requestID: path })),
    get: vi.fn(() => new Promise(() => undefined)),
    createThread: vi.fn(), submitThreadTurn: vi.fn(), executeRun: vi.fn(), transitionThread: vi.fn(),
  } as unknown as CyberAgentClient;
}

function expectNoNavigationWrites(client: CyberAgentClient) {
  for (const call of [client.createThread, client.submitThreadTurn, client.executeRun, client.transitionThread]) {
    expect(call).not.toHaveBeenCalled();
  }
  expect(openLegacyInspector).not.toHaveBeenCalled();
}

describe("V2Workbench inspector navigation", () => {
  it("closes the narrow navigation drawer after new-task and settings navigation while retaining the draft", async () => {
    vi.stubGlobal("matchMedia", vi.fn((query: string) => ({
      matches: query === "(max-width: 760px)", media: query,
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
      addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(), onchange: null,
    })));
    window.history.replaceState({}, "", `#/threads/${createdThread.id}`);
    const client = { hasThreadControl: true, getPage: vi.fn(async (path: string) => ({
      items: path === "/workspaces" ? [workspace] : [createdThread],
      page: { limit: 100 }, requestID: path,
    })) } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><V2Workbench client={client} /></QueryClientProvider>);
    const user = userEvent.setup();
    await screen.findByTestId("v2-conversation");
    const expectDrawerClosed = () => {
      expect(screen.queryByRole("button", { name: "关闭侧栏" })).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "显示侧栏" })).toHaveAttribute("aria-pressed", "false");
    };
    await user.click(screen.getByRole("button", { name: "显示侧栏" }));
    await user.click(within(screen.getByRole("navigation", { name: "对话导航" })).getByRole("button", { name: "新对话" }));
    expectDrawerClosed();
    await user.click(screen.getByRole("button", { name: "接入项目" }));
    expect(screen.getByRole("dialog", { name: "接入已有项目" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await user.type(screen.getByRole("textbox", { name: "开始新对话" }), "窄窗口中的项目需求");
    await user.click(screen.getByRole("button", { name: "显示侧栏" }));
    await user.click(screen.getByRole("button", { name: "接入模型" }));
    expect(await screen.findByRole("heading", { name: "模型" })).toBeInTheDocument();
    expectDrawerClosed();
    await user.click(screen.getByRole("button", { name: "显示侧栏" }));
    await user.click(screen.getByRole("button", { name: "常规" }));
    expect(screen.getByRole("heading", { name: "常规", level: 1 })).toBeInTheDocument();
    expectDrawerClosed();
    await user.click(screen.getByRole("button", { name: "显示侧栏" }));
    await user.click(screen.getByRole("button", { name: "返回应用" }));
    expect(await screen.findByRole("textbox", { name: "开始新对话" })).toHaveValue("窄窗口中的项目需求");
    expectDrawerClosed();
    await user.click(screen.getByRole("button", { name: "显示侧栏" }));
    await user.click(screen.getByRole("button", { name: "打开设置" }));
    expect(screen.getByRole("heading", { name: "常规", level: 1 })).toBeInTheDocument();
    expectDrawerClosed();
    await user.click(screen.getByRole("button", { name: "显示侧栏" }));
    await user.click(screen.getByRole("button", { name: "返回" }));
    expect(await screen.findByRole("textbox", { name: "开始新对话" })).toHaveValue("窄窗口中的项目需求");
    expectDrawerClosed();
  });

  it("loads past 100 Threads and preserves an older URL selection through failed paging and refresh", async () => {
    const user = userEvent.setup();
    const first = Array.from({ length: 100 }, (_, index) => ({ ...createdThread,
      id: `history-${index}`, title: `History ${index}` }));
    const oldest = { ...createdThread, id: "history-oldest", title: "Oldest task" };
    let failNext = true;
    const getPage = vi.fn(async (path: string, _query: unknown, cursor: string) => {
      if (path === "/workspaces") return { items: [workspace], page: { limit: 100 }, requestID: path };
      if (cursor && failNext) { failNext = false; throw new Error("historical page unavailable"); }
      return { items: cursor ? [oldest] : first, page: { limit: 100, next_cursor: cursor ? "" : "older-cursor" }, requestID: path };
    });
    window.history.replaceState({}, "", "#/threads/history-oldest");
    const client = { getPage, get: vi.fn() } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><V2Workbench client={client} /></QueryClientProvider>);
    expect(await screen.findByTestId("v2-conversation")).toHaveTextContent(oldest.id);
    await user.click(await screen.findByRole("button", { name: "加载更早对话" }));
    await screen.findByRole("button", { name: "重试加载对话" });
    expect(screen.getByTestId("v2-conversation")).toHaveTextContent(oldest.id);
    expect(screen.getByRole("button", { name: "History 0" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试加载对话" }));
    await user.click(await screen.findByRole("button", { name: "Oldest task" }));
    expect(getPage).toHaveBeenCalledWith("/threads", { limit: 100, status: "active" }, "older-cursor", expect.any(AbortSignal));
    await user.click(screen.getByRole("button", { name: "刷新对话列表" }));
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
    expect(screen.getByTestId("v2-conversation")).toHaveTextContent(oldest.id);
    expect(window.location.hash).toBe("#/threads/history-oldest");
    await user.click(screen.getByRole("button", { name: "History 0" }));
    expect(window.location.hash).toBe("#/threads/history-0");
    await act(async () => { window.history.back(); });
    await waitFor(() => expect(screen.getByTestId("v2-conversation")).toHaveTextContent(oldest.id));
  });
  it("retains the same Thread draft and references across both views and shared settings without writes", async () => {
    window.history.replaceState({}, "", `#/threads/${createdThread.id}`);
    const client = navigationClient();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const files = [{ id: "view-file", path: "README.md", digest: "a".repeat(64), partial: false, redacted: false }];
    queryClient.setQueryData(v2FileReferenceKey(workspace.id, createdThread.id), files);
    render(<QueryClientProvider client={queryClient}><V2Workbench client={client} /></QueryClientProvider>);
    const user = userEvent.setup();
    await user.type(await screen.findByRole("textbox", { name: "任务草稿 fixture" }), "尚未发送的任务要求");
    await user.click(screen.getByRole("button", { name: "Inspector 视图" }));
    expect(screen.getByTestId("v2-conversation")).toHaveAttribute("data-view", "inspector");
    expect(window.location.hash).toBe(`#/threads/${createdThread.id}/inspector`);
    expect(screen.getByRole("textbox", { name: "任务草稿 fixture" })).toHaveValue("尚未发送的任务要求");
    expect(screen.queryByRole("dialog", { name: "Inspector" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "打开设置" }));
    expect(screen.getByRole("heading", { name: "常规", level: 1 })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "模型" }));
    expect(window.location.hash).toBe(`#/threads/${createdThread.id}/inspector/settings/models`);
    await user.click(screen.getByRole("button", { name: "返回应用" }));
    expect(await screen.findByTestId("v2-conversation")).toHaveAttribute("data-view", "inspector");
    await user.click(screen.getByRole("button", { name: "对话视图" }));
    expect(screen.getByTestId("v2-conversation")).toHaveAttribute("data-view", "conversation");
    expect(screen.getByRole("textbox", { name: "任务草稿 fixture" })).toHaveValue("尚未发送的任务要求");
    expect(queryClient.getQueryData(v2FileReferenceKey(workspace.id, createdThread.id))).toEqual(files);
    expect(window.location.hash).toBe(`#/threads/${createdThread.id}`);
    expectNoNavigationWrites(client);
  });

  it.each([`#/threads/${createdThread.id}/inspector`, "#/new/inspector"])(
    "returns to its explicit Inspector source after direct loading settings at %s", async (hash) => {
      window.history.replaceState({}, "", `${hash}/settings/appearance`);
      const historyBack = vi.spyOn(window.history, "back");
      const client = navigationClient();
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      render(<QueryClientProvider client={queryClient}><V2Workbench client={client} /></QueryClientProvider>);
      const user = userEvent.setup();
      expect(await screen.findByRole("heading", { name: "外观", level: 1 })).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "返回应用" }));
      expect(historyBack).not.toHaveBeenCalled();
      expect(window.location.hash).toBe(hash);
      expect(screen.getByRole("button", { name: "Inspector 视图" })).toHaveAttribute("aria-pressed", "true");
      await user.click(screen.getByRole("button", { name: "对话视图" }));
      expect(window.location.hash).toBe(hash.replace("/inspector", ""));
      expect(screen.getByRole("button", { name: "对话视图" })).toHaveAttribute("aria-pressed", "true");
      expectNoNavigationWrites(client);
    },
  );

  it("retains project selection, new-task draft and references before a Thread exists", async () => {
    window.history.replaceState({}, "", "#/new");
    const client = navigationClient();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const files = [{ id: "new-view-file", path: "README.md", digest: "b".repeat(64), partial: false, redacted: false }];
    queryClient.setQueryData(v2FileReferenceKey(workspace.id, ""), files);
    render(<QueryClientProvider client={queryClient}><V2Workbench client={client} /></QueryClientProvider>);
    const user = userEvent.setup();
    await user.type(await screen.findByRole("textbox", { name: "开始新对话" }), "没有创建任务前的需求");
    await waitFor(() => expect(screen.getByRole("combobox", { name: "选择工作区" })).toHaveValue(workspace.id));
    await user.click(screen.getByRole("button", { name: "Inspector 视图" }));
    expect(window.location.hash).toBe("#/new/inspector");
    await user.click(screen.getByRole("button", { name: "打开设置" }));
    await user.click(screen.getByRole("button", { name: "返回应用" }));
    expect(window.location.hash).toBe("#/new/inspector");
    await user.click(screen.getByRole("button", { name: "对话视图" }));
    expect(await screen.findByRole("textbox", { name: "开始新对话" })).toHaveValue("没有创建任务前的需求");
    expect(screen.getByRole("combobox", { name: "选择工作区" })).toHaveValue(workspace.id);
    expect(queryClient.getQueryData(v2FileReferenceKey(workspace.id, ""))).toEqual(files);
    expectNoNavigationWrites(client);
  });
});

describe("V2Workbench model navigation", () => {
  it("opens the dedicated Models settings section from the conversation sidebar", async () => {
    const getPage = vi.fn(async (path: string) => ({
      items: path === "/workspaces" ? [workspace] : [createdThread],
      page: { limit: 100 },
      requestID: `${path}-request`,
    }));
    const client = {
      hasThreadControl: true,
      getPage,
      providerDefinitions: vi.fn().mockResolvedValue({ providers: [] }),
      providerCredentialStatuses: vi.fn().mockResolvedValue({ items: [] }),
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    render(<QueryClientProvider client={queryClient}>
      <V2Workbench client={client} />
    </QueryClientProvider>);

    const user = userEvent.setup();
    await screen.findByRole("button", { name: "接入模型" });
    await user.click(screen.getByRole("button", { name: "接入模型" }));

    const models = screen.getByRole("button", { name: "模型" });
    expect(models).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("navigation", { name: "设置分类" })).toContainElement(models);
  });
});

describe("V2Workbench first turn", () => {
  it("keeps the import receipt and current draft when a reimported project is outside the first page", async () => {
    const imported = { ...workspace, id: "workspace-older-import", name: "Older project" };
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const client = { hasThreadControl: true, hasWorkspaceImport: true,
      importWorkspace: vi.fn().mockResolvedValue({ protocol_version: "workspace_import.v1",
        workspace: imported, directory_content_modified: false, agent_authority_granted: false }),
      getPage: vi.fn(async (path: string) => ({ items: path === "/workspaces" ? [workspace] : [],
        page: { limit: 100 }, requestID: path })) } as unknown as CyberAgentClient;
    render(<QueryClientProvider client={queryClient}><V2Workbench client={client} /></QueryClientProvider>);
    const user = userEvent.setup();
    await user.type(await screen.findByRole("textbox", { name: "开始新对话" }), "在新项目中继续这份需求");
    await user.click(screen.getByRole("button", { name: "接入项目" }));
    const dialog = screen.getByRole("dialog", { name: "接入已有项目" });
    await user.type(within(dialog).getByRole("textbox", { name: "项目文件夹路径" }), "D:\\older project");
    await user.click(within(dialog).getByRole("button", { name: "接入此目录" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
    expect(screen.getByRole("combobox", { name: "选择工作区" })).toHaveValue(imported.id);
    expect(screen.getByRole("textbox", { name: "开始新对话" })).toHaveValue("在新项目中继续这份需求");
    expect(client.importWorkspace).toHaveBeenCalledWith("D:\\older project");
    expect(screen.getByRole("button", { name: "接入项目" })).toHaveFocus();
  });

  it("preserves a new-task draft across the model settings round trip", async () => {
    const client = { hasThreadControl: true, getPage: vi.fn(async (path: string) => ({
      items: path === "/workspaces" ? [workspace] : [], page: { limit: 100 }, requestID: path,
    })) } as unknown as CyberAgentClient;
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <V2Workbench client={client} />
    </QueryClientProvider>);
    const user = userEvent.setup();
    await user.type(await screen.findByRole("textbox", { name: "开始新对话" }), "保留配置前的需求");
    await user.click(screen.getByRole("button", { name: "接入模型" }));
    await user.click(screen.getByRole("button", { name: "返回应用" }));
    expect(screen.getByRole("textbox", { name: "开始新对话" })).toHaveValue("保留配置前的需求");
  });

  it("imports through the native picker and preserves input when the picker is cancelled", async () => {
    let imported = false;
    vi.spyOn(desktopBridge, "desktopWorkspaceImportEnabled").mockReturnValue(true);
    const picker = vi.spyOn(desktopBridge, "importDesktopWorkspace")
      .mockResolvedValueOnce(null).mockImplementationOnce(async () => { imported = true; return workspace; });
    const client = { hasThreadControl: true, getPage: vi.fn(async (path: string) => ({
      items: path === "/workspaces" && imported ? [workspace] : [],
      page: { limit: 100 }, requestID: path,
    })) } as unknown as CyberAgentClient;
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <V2Workbench client={client} />
    </QueryClientProvider>);
    const user = userEvent.setup();
    const composer = await screen.findByRole("textbox", { name: "开始新对话" });
    await user.type(composer, "在选择项目之前写下的需求");
    expect(screen.getByRole("button", { name: "发送消息" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "打开项目" }));
    expect(composer).toHaveValue("在选择项目之前写下的需求");
    await user.click(screen.getByRole("button", { name: "打开项目" }));
    await waitFor(() => expect(screen.getByRole("combobox", { name: "选择工作区" })).toHaveValue(workspace.id));
    expect(composer).toHaveValue("在选择项目之前写下的需求");
    expect(screen.getByRole("button", { name: "发送消息" })).toBeEnabled();
    expect(picker.mock.calls.every((args) => args.length === 0)).toBe(true);
  });

  it.each(["deliver", "plan"] as const)("opens the durable thread during the first %s turn with its selected phase", async (phase) => {
    const operationID = "00000000-0000-4000-8000-000000000042";
    const content = "请从这句首条消息直接开始执行。";
    let threads: ThreadView[] = [];
    const submission = deferred<Awaited<ReturnType<CyberAgentClient["submitThreadTurn"]>>>();

    vi.stubGlobal("crypto", {
      randomUUID: vi.fn(() => operationID),
    });

    const getPage = vi.fn(async (path: string) => {
      if (path === "/workspaces") {
        return {
          items: [workspace],
          page: { limit: 100 },
          requestID: "workspaces-request",
        };
      }
      if (path === "/threads") {
        return {
          items: threads,
          page: { limit: 100 },
          requestID: "threads-request",
        };
      }
      throw new Error(`Unexpected page request: ${path}`);
    });
    const createThread = vi.fn(async (
      _body: Parameters<CyberAgentClient["createThread"]>[0],
      _idempotencyKey: string,
    ): Promise<Awaited<ReturnType<CyberAgentClient["createThread"]>>> => {
      threads = [createdThread];
      return { thread: createdThread } as Awaited<ReturnType<CyberAgentClient["createThread"]>>;
    });
    const submitThreadTurn = vi.fn((
      _threadID: string,
      _body: Parameters<CyberAgentClient["submitThreadTurn"]>[1],
      _idempotencyKey: string,
    ) => submission.promise);
    const availableModelRoutes = vi.fn().mockResolvedValue({
      protocol_version: "model_route_catalog.v1",
      generation: 1,
      routes: [{ provider_id: "official-deepseek", provider_name: "DeepSeek",
        model: "deepseek-v4-flash", enabled: true, credential_status: "configured",
        qualification_status: "qualified", harness_ready: true, selectable: true,
        unavailable_reason: "", default_for_routes: ["code"] }],
    });
    const client = {
      hasThreadControl: true,
      hasModelControl: true,
      hasPlanDelivery: true,
      getPage,
      createThread,
      submitThreadTurn,
      availableModelRoutes,
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });

    render(
      <QueryClientProvider client={queryClient}>
        <V2Workbench client={client} />
      </QueryClientProvider>,
    );

    const composer = await screen.findByRole("textbox", { name: "开始新对话" });
    await waitFor(() => expect(composer).toBeEnabled());

    const user = userEvent.setup();
    expect(screen.getByRole("button", { name: "计划模式" })).toHaveAttribute("aria-pressed", "false");
    if (phase === "plan") await user.click(screen.getByRole("button", { name: "计划模式" }));
    expect(screen.getByRole("button", { name: "计划模式" })).toHaveAttribute("aria-pressed", String(phase === "plan"));
    expect(createThread).not.toHaveBeenCalled();
    expect(submitThreadTurn).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "模型路由，当前 默认 · 模型；推理强度：随模型" }));
    await user.click(screen.getByRole("menuitem", { name: /^模型/ }));
    await user.click(await screen.findByRole("menuitemradio", { name: /deepseek-v4-flash/ }));
    await user.type(composer, content);
    await waitFor(() => expect(screen.getByRole("button", { name: "发送消息" })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "发送消息" }));

    await waitFor(() => expect(submitThreadTurn).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("textbox", { name: "开始新对话" })).not.toBeInTheDocument();
    expect(await screen.findByTestId("v2-conversation")).toHaveTextContent(createdThread.id);
    expect(createThread).toHaveBeenCalledTimes(1);
    expect(createThread.mock.invocationCallOrder[0]).toBeLessThan(
      submitThreadTurn.mock.invocationCallOrder[0],
    );

    const [createBody, createKey] = createThread.mock.calls[0]!;
    const [turnThreadID, turnBody, turnKey] = submitThreadTurn.mock.calls[0]!;

    expect(createBody).toMatchObject({
      phase,
      workspace_id: workspace.id,
      goal: content,
      network_mode: "disabled",
      provider: "official-deepseek",
      model: "deepseek-v4-flash",
    });
    expect(turnThreadID).toBe(createdThread.id);
    expect(turnBody).toEqual({
      version: "thread_message_submission.v1",
      content,
    });
    expect(turnBody.content).toBe(createBody.goal);

    expect(createKey).toBe(`v2-thread-create-${operationID}`);
    expect(turnKey).toBe(`v2-thread-create-turn-${operationID}`);
    expect(turnKey).not.toBe(createKey);
    expect(turnKey).toBe(createKey.replace("v2-thread-create-", "v2-thread-create-turn-"));

    await act(async () => {
      submission.resolve({ accepted: true } as unknown as Awaited<
        ReturnType<CyberAgentClient["submitThreadTurn"]>>);
      await submission.promise;
    });

    expect(await screen.findByTestId("v2-conversation")).toHaveTextContent(createdThread.id);
    expect(screen.queryByRole("textbox", { name: "开始新对话" })).not.toBeInTheDocument();
  });

  it("opens the already-created thread when its first turn fails", async () => {
    const operationID = "00000000-0000-4000-8000-000000000043";
    const content = "即使首轮执行失败，也继续留在这个对话。";
    const submission = deferred<Awaited<ReturnType<CyberAgentClient["submitThreadTurn"]>>>();
    let threads: ThreadView[] = [];

    vi.stubGlobal("crypto", { randomUUID: vi.fn(() => operationID) });
    const getPage = vi.fn(async (path: string) => ({
      items: path === "/workspaces" ? [workspace] : threads,
      page: { limit: 100 },
      requestID: `${path}-request`,
    }));
    const createThread = vi.fn(async () => {
      threads = [createdThread];
      return { thread: createdThread } as Awaited<ReturnType<CyberAgentClient["createThread"]>>;
    });
    const submitThreadTurn = vi.fn(() => submission.promise);
    const client = {
      hasThreadControl: true,
      hasEvidenceAttachment: true,
      getPage,
      createThread,
      submitThreadTurn,
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const files = [{ id: "first-file", path: "README.md", digest: "a".repeat(64), partial: false, redacted: false }];
    queryClient.setQueryData(v2FileReferenceKey(workspace.id, ""), files);

    render(<QueryClientProvider client={queryClient}>
      <V2Workbench client={client} />
    </QueryClientProvider>);

    const composer = await screen.findByRole("textbox", { name: "开始新对话" });
    await waitFor(() => expect(composer).toBeEnabled());
    const user = userEvent.setup();
    await user.type(composer, content);
    await user.click(screen.getByRole("button", { name: "发送消息" }));

    await waitFor(() => expect(submitThreadTurn).toHaveBeenCalledTimes(1));
    expect(await screen.findByTestId("v2-conversation")).toHaveTextContent(createdThread.id);
    await act(async () => {
      submission.reject(new APIRequestError("first turn failed at its durable boundary", "UNAVAILABLE", 503,
        "failed-turn", undefined, undefined, true));
      await Promise.resolve();
    });

    expect(await screen.findByTestId("v2-conversation")).toHaveTextContent(createdThread.id);
    expect(createThread).toHaveBeenCalledTimes(1);
    expect(submitThreadTurn).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("textbox", { name: "开始新对话" })).not.toBeInTheDocument();
    // A known execution failure has already admitted this exact first input.
    // Its original payload remains in the turn record, not as an unsent draft.
    expect(screen.getByRole("textbox", { name: "任务草稿 fixture" })).toHaveValue("");
    expect(queryClient.getQueryData(v2FileReferenceKey(workspace.id, createdThread.id))).toEqual([]);
    expect(queryClient.getQueryData(v2FileReferenceKey(workspace.id, ""))).toEqual([]);
    expect(submitThreadTurn).toHaveBeenCalledWith(createdThread.id, {
      version: "thread_message_submission.v1", content,
      files: [{ source_kind: "workspace_file", path: "README.md", expected_sha256: files[0]!.digest }],
    }, `v2-thread-create-turn-${operationID}`);
  });

  it("hands off only the submitted draft and files when creation finishes after project navigation", async () => {
    const otherWorkspace = { ...workspace, id: "workspace-other", name: "Other project" };
    const creation = deferred<Awaited<ReturnType<CyberAgentClient["createThread"]>>>();
    const submission = deferred<Awaited<ReturnType<CyberAgentClient["submitThreadTurn"]>>>();
    const files = [{ id: "submitted-file", path: "README.md", digest: "a".repeat(64), partial: false, redacted: false }];
    const lateFile = { ...files[0]!, id: "late-file", path: "notes.md" };
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    queryClient.setQueryData(v2FileReferenceKey(workspace.id, ""), files);
    const client = { hasThreadControl: true, hasEvidenceAttachment: true,
      getPage: vi.fn(async (path: string) => ({ items: path === "/workspaces" ? [workspace, otherWorkspace] : [],
        page: { limit: 100 }, requestID: path })),
      createThread: vi.fn(() => creation.promise), submitThreadTurn: vi.fn(() => submission.promise),
    } as unknown as CyberAgentClient;
    render(<QueryClientProvider client={queryClient}><V2Workbench client={client} /></QueryClientProvider>);
    const user = userEvent.setup();
    const input = await screen.findByRole("textbox", { name: "开始新对话" });
    await user.type(input, "首条提交");
    await user.click(screen.getByRole("button", { name: "发送消息" }));
    fireEvent.change(input, { target: { value: "创建期间的新草稿" } });
    act(() => queryClient.setQueryData(v2FileReferenceKey(workspace.id, ""), [...files, lateFile]));
    await user.click(screen.getByRole("button", { name: "接入模型" }));
    await user.click(screen.getByRole("button", { name: "返回应用" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "选择工作区" }), otherWorkspace.id);
    await user.type(screen.getByRole("textbox", { name: "开始新对话" }), "另一项目的独立草稿");
    await act(async () => { creation.resolve({ thread: createdThread } as Awaited<ReturnType<CyberAgentClient["createThread"]>>); });
    expect(await screen.findByRole("textbox", { name: "任务草稿 fixture" })).toHaveValue("首条提交");
    expect(queryClient.getQueryData(v2FileReferenceKey(workspace.id, createdThread.id))).toEqual(files);
    expect(queryClient.getQueryData(v2FileReferenceKey(workspace.id, ""))).toEqual([lateFile]);
    fireEvent.change(screen.getByRole("textbox", { name: "任务草稿 fixture" }), { target: { value: "任务内追加的草稿" } });
    await user.click(screen.getByRole("button", { name: "新对话" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "选择工作区" }), workspace.id);
    expect(screen.getByRole("textbox", { name: "开始新对话" })).toHaveValue("创建期间的新草稿");
    await user.selectOptions(screen.getByRole("combobox", { name: "选择工作区" }), otherWorkspace.id);
    expect(screen.getByRole("textbox", { name: "开始新对话" })).toHaveValue("另一项目的独立草稿");
    await act(async () => { submission.resolve({ accepted: true } as unknown as Awaited<ReturnType<CyberAgentClient["submitThreadTurn"]>>); });
    expect(screen.getByRole("textbox", { name: "开始新对话" })).toHaveValue("另一项目的独立草稿");
    expect(queryClient.getQueryData(v2FileReferenceKey(workspace.id, createdThread.id))).toEqual([]);
    act(() => { window.history.pushState({}, "", `#/threads/${createdThread.id}`); window.dispatchEvent(new PopStateEvent("popstate")); });
    expect(await screen.findByRole("textbox", { name: "任务草稿 fixture" })).toHaveValue("任务内追加的草稿");
    expect(client.createThread).toHaveBeenCalledTimes(1);
  });

  it("reuses the creation idempotency key when a creation response is retried", async () => {
    const operationID = "00000000-0000-4000-8000-000000000044";
    const content = "响应丢失后不要创建第二个 Thread。";
    let threads: ThreadView[] = [];

    const randomUUID = vi.fn(() => operationID);
    vi.stubGlobal("crypto", { randomUUID });
    const getPage = vi.fn(async (path: string) => ({
      items: path === "/workspaces" ? [workspace] : threads,
      page: { limit: 100 },
      requestID: `${path}-request`,
    }));
    const createThread = vi.fn()
      .mockRejectedValueOnce(new Error("creation response was lost"))
      .mockImplementationOnce(async () => {
        threads = [createdThread];
        return { thread: createdThread } as Awaited<ReturnType<CyberAgentClient["createThread"]>>;
      });
    const submitThreadTurn = vi.fn().mockResolvedValue(
      { accepted: true } as unknown as Awaited<ReturnType<CyberAgentClient["submitThreadTurn"]>>,
    );
    const client = {
      hasThreadControl: true,
      getPage,
      createThread,
      submitThreadTurn,
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });

    render(<QueryClientProvider client={queryClient}>
      <V2Workbench client={client} />
    </QueryClientProvider>);

    const composer = await screen.findByRole("textbox", { name: "开始新对话" });
    await waitFor(() => expect(composer).toBeEnabled());
    const user = userEvent.setup();
    await user.type(composer, content);
    await user.click(screen.getByRole("button", { name: "发送消息" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("creation response was lost");

    await user.click(screen.getByRole("button", { name: "发送消息" }));
    expect(await screen.findByTestId("v2-conversation")).toHaveTextContent(createdThread.id);

    expect(createThread).toHaveBeenCalledTimes(2);
    expect(createThread.mock.calls[0]?.[1]).toBe(`v2-thread-create-${operationID}`);
    expect(createThread.mock.calls[1]?.[1]).toBe(`v2-thread-create-${operationID}`);
    expect(randomUUID).toHaveBeenCalledTimes(1);
    expect(submitThreadTurn).toHaveBeenCalledTimes(1);
  });

  it("keeps each project's unknown creation key when another project's response arrives late", async () => {
    const otherWorkspace = { ...workspace, id: "workspace-second-create", name: "Second project" };
    const otherThread = { ...createdThread, id: "thread-second-create", workspace_id: otherWorkspace.id };
    const firstCreation = deferred<Awaited<ReturnType<CyberAgentClient["createThread"]>>>();
    const secondCreation = deferred<Awaited<ReturnType<CyberAgentClient["createThread"]>>>();
    const createThread = vi.fn().mockImplementationOnce(() => firstCreation.promise)
      .mockImplementationOnce(() => secondCreation.promise).mockResolvedValueOnce({ thread: otherThread });
    const client = { hasThreadControl: true, createThread,
      getPage: vi.fn(async (path: string) => ({ items: path === "/workspaces" ? [workspace, otherWorkspace] : [],
        page: { limit: 100 }, requestID: path })),
      submitThreadTurn: vi.fn().mockResolvedValue({ accepted: true }),
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><V2Workbench client={client} /></QueryClientProvider>);
    const user = userEvent.setup();
    await user.type(await screen.findByRole("textbox", { name: "开始新对话" }), "A项目请求");
    await user.click(screen.getByRole("button", { name: "发送消息" }));
    await user.click(screen.getByRole("button", { name: "接入模型" }));
    await user.click(screen.getByRole("button", { name: "返回应用" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "选择工作区" }), otherWorkspace.id);
    await user.type(screen.getByRole("textbox", { name: "开始新对话" }), "B项目请求");
    await user.click(screen.getByRole("button", { name: "发送消息" }));
    await waitFor(() => expect(createThread).toHaveBeenCalledTimes(2));
    await act(async () => { secondCreation.reject(new Error("B creation response lost")); });
    expect(await screen.findByRole("alert")).toHaveTextContent("B creation response lost");
    await act(async () => { firstCreation.resolve({ thread: createdThread } as Awaited<ReturnType<CyberAgentClient["createThread"]>>); });
    expect(await screen.findByTestId("v2-conversation")).toHaveTextContent(createdThread.id);
    await user.click(screen.getByRole("button", { name: "新对话" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "选择工作区" }), otherWorkspace.id);
    expect(screen.getByRole("textbox", { name: "开始新对话" })).toHaveValue("B项目请求");
    await user.click(screen.getByRole("button", { name: "发送消息" }));
    expect(await screen.findByTestId("v2-conversation")).toHaveTextContent(otherThread.id);
    expect(createThread).toHaveBeenCalledTimes(3);
    expect(createThread.mock.calls[1]).toEqual(createThread.mock.calls[2]);
    expect(createThread.mock.calls[0]![1]).not.toBe(createThread.mock.calls[1]![1]);
    expect(client.submitThreadTurn).toHaveBeenCalledTimes(2);
  });
});
