import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { CyberAgentClient } from "../api/client";
import type { RunView, SessionView, ThreadView } from "../api/types";
import { formatCompactDate } from "../lib/format";
import { useConnectionStore } from "../state/connection";
import { ResourceSidebar } from "./resource-sidebar";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

function run(id: string, status: RunView["status"]): RunView {
  return {
    id,
    mission_id: `mission-${id}`,
    session_id: `session-${id}`,
    status,
    config: { model_route: "code", interactive: false },
    budget: { max_turns: 8 },
    created_at: "2026-07-13T00:00:00Z",
    updated_at: "2026-07-13T00:01:00Z",
  };
}

function session(id: string, title: string): SessionView {
  return {
    id,
    route: "code",
    status: "active",
    title,
    workspace_id: "workspace-demo",
    created_at: "2026-07-13T00:00:00Z",
    updated_at: "2026-07-13T00:01:00Z",
  };
}

function thread(id: string, title: string): ThreadView {
  return {
    id,
    protocol_version: "thread.v1",
    mission_id: `mission-${id}`,
    title,
    status: "active",
    active_run_id: `run-${id}`,
    last_run_id: `run-${id}`,
    version: 2,
    composer_state: "ready",
    created_at: "2026-07-13T00:00:00Z",
    updated_at: "2026-07-13T00:01:00Z",
  };
}

describe("ResourceSidebar", () => {
  beforeEach(() => {
    useConnectionStore.getState().disconnect();
  });

  it("renders lifecycle states and appends the next opaque cursor page", async () => {
    const firstPage = [
      run("run-paused", "paused"),
      run("run-completed", "completed"),
      run("run-failed", "failed"),
      run("run-cancelled", "cancelled"),
    ];
    const secondPage = [run("run-running", "running")];
    const getPage = vi.fn().mockImplementation((path: string, _query: unknown, cursor: string) => {
      if (path === "/sessions") {
        return Promise.resolve({
          items: [session("session-alpha", "修复登录回归")],
          page: { limit: 50 },
          requestID: "req-sessions-1",
        });
      }
      if (path !== "/runs") throw new Error(`unexpected path ${path}`);
      if (cursor === "cursor-terminal-page") {
        return Promise.resolve({
          items: secondPage,
          page: { limit: 50 },
          requestID: "req-runs-2",
        });
      }
      return Promise.resolve({
        items: firstPage,
        page: { limit: 50, next_cursor: "cursor-terminal-page" },
        requestID: "req-runs-1",
      });
    });
    const client = { getPage } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    const onCreateRun = vi.fn();
    const onNavigate = vi.fn();
    const onOpenSettings = vi.fn();
    const { container } = render(
      <QueryClientProvider client={queryClient}>
        <ResourceSidebar activeSection="conversation" client={client}
          onCreateRun={onCreateRun} onNavigate={onNavigate} onOpenSettings={onOpenSettings} />
      </QueryClientProvider>,
    );

    await waitFor(() => {
      for (const status of ["paused", "completed", "failed", "cancelled"]) {
        expect(container.querySelector(`.history-status.status-${status}`)).toBeInTheDocument();
      }
    });
    const loadMore = Array.from(container.querySelectorAll<HTMLButtonElement>("button.load-more"))
      .find((button) => button.textContent?.includes("加载更多"));
    expect(loadMore).not.toBeNull();
    await act(async () => {
      fireEvent.click(loadMore!);
    });

    await waitFor(() => expect(container.querySelector(".history-status.status-running"))
      .toBeInTheDocument());
    await waitFor(() => expect(getPage.mock.calls.some((call) =>
      call[0] === "/runs" && call[2] === "cursor-terminal-page")).toBe(true));
    expect(useConnectionStore.getState().resourceKind).toBe("thread");
    expect(useConnectionStore.getState().selectedRunID).toBe("");
    expect(screen.getByRole("img", { name: "针路簿" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "工作台导航" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "模型切换" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "定时 Run" })).toBeInTheDocument();
    const diagnosticSummary = screen.getByText("高级诊断与兼容视图");
    expect(diagnosticSummary.tagName).toBe("SUMMARY");
    expect(diagnosticSummary.closest("details")).not.toHaveAttribute("open");
    expect(screen.getByText("Run 执行尝试").closest("details"))
      .toBe(diagnosticSummary.closest("details"));
    expect(screen.getByText(/Session 只保存该 Run 的上下文与授权边界/)).toBeInTheDocument();
    expect(screen.getByText("修复登录回归")).toBeInTheDocument();
    expect(screen.getAllByText(new RegExp(formatCompactDate("2026-07-13T00:00:00Z"))).length)
      .toBeGreaterThan(0);
    expect(screen.queryByText(new RegExp(formatCompactDate("2026-07-13T00:01:00Z"))))
      .not.toBeInTheDocument();
    expect(container.querySelector(".resource-row.selected strong")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /新建 Thread/ }));
    fireEvent.click(screen.getByRole("button", { name: "模型切换" }));
    fireEvent.click(screen.getByRole("button", { name: /本地操作者/ }));
    expect(onCreateRun).toHaveBeenCalledTimes(1);
    expect(onNavigate).toHaveBeenCalledWith("models");
    expect(onOpenSettings).toHaveBeenCalledTimes(1);
  });

  it("archives the canonical Thread after confirmation and leaves Session diagnostics read-only", async () => {
    const getPage = vi.fn().mockImplementation((path: string) => Promise.resolve({
      items: path === "/threads" ? [thread("thread-alpha", "修复登录回归")] :
        path === "/sessions" ? [session("session-alpha", "Run 诊断")] : [],
      page: { limit: 50 },
      requestID: `req-${path}`,
    }));
    const transitionThread = vi.fn().mockResolvedValue({
      version: "thread_lifecycle.v1",
      action: "archive",
      thread: { ...thread("thread-alpha", "修复登录回归"), status: "archived", version: 3 },
      replayed: false,
    });
    const client = { getPage, transitionThread } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    render(<QueryClientProvider client={queryClient}>
      <ResourceSidebar activeSection="conversation" client={client} />
    </QueryClientProvider>);

    await screen.findByText("修复登录回归");
    fireEvent.click(screen.getByRole("button", { name: "归档 Thread 修复登录回归" }));
    expect(screen.getByRole("dialog", { name: "归档 Thread" })).toHaveTextContent("设置中恢复");
    fireEvent.click(screen.getByRole("button", { name: "归档" }));

    await waitFor(() => expect(transitionThread).toHaveBeenCalledWith("thread-alpha", "archive", {
      version: "thread_lifecycle.v1", expected_version: 2,
    }, expect.stringMatching(/^web-thread-archive-/u)));
    fireEvent.click(screen.getByText("高级诊断与兼容视图"));
    expect(screen.getByText("Run 诊断")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /删除对话/u })).not.toBeInTheDocument();
  });

  it("preserves a deep-linked Thread omitted from the bounded sidebar page", async () => {
    useConnectionStore.getState().selectThread("thread-beyond-first-page");
    const getPage = vi.fn().mockResolvedValue({
      items: [], page: { limit: 50 }, requestID: "req-empty",
    });
    const client = { getPage } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}>
      <ResourceSidebar activeSection="conversation" client={client} />
    </QueryClientProvider>);

    await waitFor(() => expect(getPage).toHaveBeenCalled());
    expect(useConnectionStore.getState().selectedThreadID).toBe("thread-beyond-first-page");
    expect(useConnectionStore.getState().resourceKind).toBe("thread");
  });
});
