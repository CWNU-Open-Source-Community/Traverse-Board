import type { ReactNode } from "react";
import { LocaleProvider } from "../lib/locale";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render as renderComponent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import type { RunDetailView, RunExecutionPermissionControlView, RunExecutionPermissionView,
  ThreadExecutionPermissionControlView } from "../api/types";
import { capabilityReadinessFixture } from "../test/capability-readiness";
import { v2QueryKeys } from "../v2/query-keys";
import { V2PermissionControl } from "../v2/components/permission-control";
import { ExecutionPermissionPanel } from "./run-permission-settings";

afterEach(() => { cleanup(); window.localStorage.removeItem("prayu.locale.v1"); });

function permission(full = false): RunExecutionPermissionView {
  return {
    protocol_version: "run_execution_permission.v2", policy_version: "execution_permission_policy.v2",
    revision: full ? 2 : 1, mode: full ? "full" : "ask", approval_mode: full ? "full" : "ask",
    full_activation: full ? "active" : "inactive", approval_policy: "per_operation",
    command_scope: "per_operation", filesystem_scope: "per_operation", network_scope: "per_operation",
    persistent_terminal: false, background_process: false, agent_terminal_input: false,
    risk_tier: full ? "high" : "minimal", required_gate: "operation_authority", operator_confirmed: full,
    process_enabled: false, execution_authorized: false, capability_grant: false,
    runtime_gate_available: true, runtime: { workspace_sandbox_enabled: false,
      operator_approval_enabled: true, danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    capability_matrix: { workspace_read: true, workspace_write: true, sandboxed_command_runtime: true,
      unsandboxed_host_process: true, network_access: true, credential_access: true, user_home_access: true,
      persistent_user_terminal: true, persistent_agent_terminal: true, full_cdp: full,
      out_of_scope_policy: full ? "not_required" : "exact_once_required" },
    created_at: "2026-10-02T00:00:00Z",
  };
}

function threadResult(id: string, full = false): ThreadExecutionPermissionControlView {
  return { execution_permission: { ...permission(full), thread_id: id,
    protocol_version: "thread_execution_permission.v2",
    applies_to_current_run: false, applies_to_future_successor_runs: true },
    current_run_effect: "no_active_run", current_run_synchronized: false, replayed: false };
}

function runDetail(id: string, full = false): RunDetailView {
  // This exported permission panel consumes only its Run identity and preference.
  return { run: { id }, execution_permission: permission(full) } as RunDetailView;
}

function deferred<T>() {
  let resolve!: (result: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

type Host = "thread menu" | "thread settings" | "run panel";
function fixture(host: Host) {
  const queries = new QueryClient({ defaultOptions: {
    queries: { retry: false, staleTime: Infinity }, mutations: { retry: false },
  } });
  const pending = deferred<ThreadExecutionPermissionControlView | RunExecutionPermissionControlView>();
  const change = vi.fn((_target: string, _request: unknown, _operationKey: string) => pending.promise);
  const get = vi.fn(async (id: string) => threadResult(id));
  const client = { hasExecutionPermissionControl: true, getThreadExecutionPermission: get,
    changeThreadExecutionPermission: change, postControl: change } as unknown as CyberAgentClient;
  for (const id of ["target-A", "target-B"]) {
    queries.setQueryData(v2QueryKeys.permission(id), threadResult(id));
    queries.setQueryData(["run", id], runDetail(id));
  }
  const view = (id: string) => <QueryClientProvider client={queries}>
    {host === "run panel"
      ? <ExecutionPermissionPanel client={client} detail={runDetail(id)} readiness={capabilityReadinessFixture()} />
      : <V2PermissionControl client={client} threadID={id} variant={host === "thread menu" ? "menu" : "settings"} />}
  </QueryClientProvider>;
  const mounted = render(view("target-A"));
  const user = userEvent.setup();
  const openFull = async () => {
    if (host === "thread menu") await user.click(screen.getByRole("button", { name: "请求批准" }));
    await user.click(screen.getByRole(host === "thread menu" ? "menuitemradio" : "button", { name: "完全访问权限" }));
    return within(screen.getByRole("dialog", { name: "启用完全访问权限？" }))
      .getByRole("button", { name: "确认启用" });
  };
  const targetMode = (id: string) => host === "run panel"
    ? queries.getQueryData<RunDetailView>(["run", id])!.execution_permission.approval_mode
    : queries.getQueryData<ThreadExecutionPermissionControlView>(v2QueryKeys.permission(id))!.execution_permission.approval_mode;
  const fullResult = () => host === "run panel"
    ? { execution_permission: permission(true), replayed: false }
    : threadResult("target-A", true);
  return { queries, pending, change, get, user, openFull, targetMode, fullResult,
    switchToB: () => mounted.rerender(view("target-B")) };
}

describe.each(["thread menu", "thread settings", "run panel"] as const)("%s target isolation", (host) => {
  it("cancels A's Full confirmation when B has the same cached preference", async () => {
    const f = fixture(host);
    const oldConfirm = await f.openFull();
    f.switchToB();
    // A stale pointer event must not turn the old confirmation into consent for B.
    await act(async () => {
      fireEvent.click(oldConfirm);
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(f.change).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(f.targetMode("target-A")).toBe("ask");
    expect(f.targetMode("target-B")).toBe("ask");
    expect(f.get).not.toHaveBeenCalled(); // No loading-state remount hides the regression.
  });

  it("keeps A's pending success in A's cache after switching to cached B", async () => {
    const f = fixture(host);
    await f.user.click(await f.openFull());
    await waitFor(() => expect(f.change).toHaveBeenCalledTimes(1));
    expect(f.change.mock.calls[0]![0]).toBe(host === "run panel"
      ? "/runs/target-A/execution-permission" : "target-A");
    f.switchToB();
    await act(async () => { f.pending.resolve(f.fullResult()); });
    await waitFor(() => expect(f.targetMode("target-A")).toBe("full"));
    expect(f.targetMode("target-B")).toBe("ask");
    expect(f.change).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("正在更新权限…")).not.toBeInTheDocument();
  });

  it("does not carry A's pending state or late failure into B", async () => {
    const f = fixture(host);
    await f.user.click(await f.openFull());
    await waitFor(() => expect(f.change).toHaveBeenCalledTimes(1));
    f.switchToB();
    if (host === "thread menu") await f.user.click(screen.getByRole("button", { name: "请求批准" }));
    const full = screen.getByRole(host === "thread menu" ? "menuitemradio" : "button", { name: "完全访问权限" });
    const blockedByOldTarget = full.hasAttribute("disabled");
    await act(async () => {
      f.pending.reject(new Error("target-A permission failed"));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(screen.queryByText("target-A permission failed")).not.toBeInTheDocument();
    expect(blockedByOldTarget).toBe(false);
    expect(f.targetMode("target-B")).toBe("ask");
    expect(f.change).toHaveBeenCalledTimes(1);
  });
});

// Match the production locale boundary instead of the isolated English default.
function render(ui: ReactNode) {
  window.localStorage.setItem("prayu.locale.v1", "zh-CN");
  return renderComponent(ui, { wrapper: LocaleProvider });
}
