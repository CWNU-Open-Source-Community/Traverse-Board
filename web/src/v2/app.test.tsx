import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { CyberAgentClient } from "../api/client";
import type { ThreadView, WorkspaceView } from "../api/types";
import { useConnectionStore } from "../state/connection";
import { V2Workbench } from "./app";

const openLegacyInspector = vi.hoisted(() => vi.fn());

vi.mock("../legacy-route", async (importOriginal) => ({
  ...await importOriginal<typeof import("../legacy-route")>(),
  openLegacyInspector,
}));

vi.mock("./components/conversation", () => ({
  V2Conversation: ({ threadID, onOpenInspector }: {
    threadID: string;
    onOpenInspector: (returnFocus: HTMLElement) => void;
  }) => (
    <div data-testid="v2-conversation">{threadID}
      <button onClick={(event) => onOpenInspector(event.currentTarget)}
        type="button">Open inspector fixture</button>
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
});

describe("V2Workbench inspector navigation", () => {
  it("opens the full inspector through the query-free legacy route adapter", async () => {
    const getPage = vi.fn(async (path: string) => ({
      items: path === "/workspaces" ? [workspace] : [createdThread],
      page: { limit: 100 },
      requestID: `${path}-request`,
    }));
    const client = {
      hasThreadControl: true,
      getPage,
      get: vi.fn(() => new Promise(() => undefined)),
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });

    render(<QueryClientProvider client={queryClient}>
      <V2Workbench client={client} />
    </QueryClientProvider>);

    const user = userEvent.setup();
    const trigger = await screen.findByRole("button", { name: "Open inspector fixture" });
    await user.click(trigger);
    await user.click(screen.getByRole("button", { name: "打开完整 Harness Inspector" }));

    expect(openLegacyInspector).toHaveBeenCalledOnce();
    expect(openLegacyInspector).toHaveBeenCalledWith(createdThread.id);
    expect(screen.queryByRole("dialog", { name: "Inspector" })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("traps focus, supports every dismissal path, and restores the inspector trigger", async () => {
    const getPage = vi.fn(async (path: string) => ({
      items: path === "/workspaces" ? [workspace] : [createdThread],
      page: { limit: 100 },
      requestID: `${path}-request`,
    }));
    const client = {
      hasThreadControl: true,
      getPage,
      get: vi.fn(() => new Promise(() => undefined)),
    } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    render(<QueryClientProvider client={queryClient}>
      <V2Workbench client={client} />
    </QueryClientProvider>);
    const user = userEvent.setup();
    const trigger = await screen.findByRole("button", { name: "Open inspector fixture" });

    await user.click(trigger);
    let dialog = screen.getByRole("dialog", { name: "Inspector" });
    expect(dialog).toHaveAttribute("aria-modal", "true");
    const close = within(dialog).getByRole("button", { name: "关闭 Inspector" });
    const full = within(dialog).getByRole("button", { name: "打开完整 Harness Inspector" });
    await waitFor(() => expect(close).toHaveFocus());
    expect(document.querySelector(".v2-shell-body")).toHaveAttribute("inert");
    expect(document.querySelector(".v2-shell-body")).toHaveAttribute("aria-hidden", "true");
    await user.tab({ shift: true });
    expect(full).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "Inspector" })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();

    await user.click(trigger);
    dialog = screen.getByRole("dialog", { name: "Inspector" });
    await user.click(within(dialog).getByRole("button", { name: "关闭 Inspector" }));
    expect(screen.queryByRole("dialog", { name: "Inspector" })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();

    await user.click(trigger);
    const backdrop = document.querySelector<HTMLElement>(".v2-inspector-backdrop");
    expect(backdrop).not.toBeNull();
    fireEvent.mouseDown(backdrop!);
    expect(screen.queryByRole("dialog", { name: "Inspector" })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
    expect(document.querySelector(".v2-shell-body")).not.toHaveAttribute("inert");
    expect(document.querySelector(".v2-shell-body")).not.toHaveAttribute("aria-hidden");
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
  it("waits for the first turn to settle before opening the created thread", async () => {
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
    await user.click(screen.getByRole("button", { name: "模型路由，当前 模型" }));
    await user.click(screen.getByRole("menuitem", { name: /^模型/ }));
    await user.click(await screen.findByRole("menuitemradio", { name: /deepseek-v4-flash/ }));
    await user.type(composer, content);
    await user.click(screen.getByRole("button", { name: "发送消息" }));

    await waitFor(() => expect(submitThreadTurn).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("textbox", { name: "开始新对话" })).toBeDisabled();
    expect(screen.queryByTestId("v2-conversation")).not.toBeInTheDocument();
    expect(createThread).toHaveBeenCalledTimes(1);
    expect(createThread.mock.invocationCallOrder[0]).toBeLessThan(
      submitThreadTurn.mock.invocationCallOrder[0],
    );

    const [createBody, createKey] = createThread.mock.calls[0]!;
    const [turnThreadID, turnBody, turnKey] = submitThreadTurn.mock.calls[0]!;

    expect(createBody).toMatchObject({
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

    await waitFor(() => expect(submitThreadTurn).toHaveBeenCalledTimes(1));
    expect(screen.queryByTestId("v2-conversation")).not.toBeInTheDocument();
    await act(async () => {
      submission.reject(new Error("first turn failed at its durable boundary"));
      await Promise.resolve();
    });

    expect(await screen.findByTestId("v2-conversation")).toHaveTextContent(createdThread.id);
    expect(createThread).toHaveBeenCalledTimes(1);
    expect(submitThreadTurn).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("textbox", { name: "开始新对话" })).not.toBeInTheDocument();
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
});
