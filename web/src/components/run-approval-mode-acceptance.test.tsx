import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CyberAgentClient } from "../api/client";
import type { RunDetailView, RunExecutionPermissionView } from "../api/types";
import { capabilityReadinessFixture } from "../test/capability-readiness";
import { LocaleProvider } from "../lib/locale";
import { ExecutionPermissionPanel } from "./run-permission-settings";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.localStorage.removeItem("prayu.locale.v1"); });

function permission(mode: "ask" | "auto" | "full" = "ask", active = false): RunExecutionPermissionView {
  return {
    protocol_version: "run_execution_permission.v2", revision: 1, mode, approval_mode: mode,
    full_activation: active ? "active" : "inactive", approval_policy: "per_operation",
    command_scope: "per_operation", filesystem_scope: "per_operation", network_scope: "per_operation",
    persistent_terminal: false, background_process: false, agent_terminal_input: false,
    risk_tier: mode === "full" ? "high" : "minimal", required_gate: "operation_authority",
    policy_version: "execution_permission_policy.v2", operator_confirmed: mode === "full",
    runtime_gate_available: mode !== "full" || active,
    runtime: { workspace_sandbox_enabled: false, operator_approval_enabled: true,
      danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    capability_matrix: { workspace_read: true, workspace_write: true, sandboxed_command_runtime: true,
      unsandboxed_host_process: true, network_access: true, credential_access: true, user_home_access: true,
      persistent_user_terminal: true, persistent_agent_terminal: true, full_cdp: false,
      out_of_scope_policy: "exact_once_required" },
    created_at: "2026-10-02T00:00:00Z", process_enabled: false, execution_authorized: false, capability_grant: false,
  };
}

function install(initial = permission(), deferResponse = false) {
  window.localStorage.setItem("prayu.locale.v1", "zh-CN");
  const queries = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  // The exported real panel consumes only these RunDetail fields. Other Run
  // settings are deliberately not mocked into this panel's authority boundary.
  for (const runID of ["run-ui-a", "run-ui-b"]) {
    queries.setQueryData(["run", runID], { run: { id: runID }, execution_permission: initial } as RunDetailView);
  }
  let release: (() => void) | undefined;
  const fetch = vi.fn(async (path: unknown, options?: RequestInit) => {
    if (options?.method !== "POST" || !String(path).match(/^\/api\/v1\/runs\/run-ui-[ab]\/execution-permission$/u)) {
      throw new Error(`Unexpected fixture request: ${String(path)}`);
    }
    const body = JSON.parse(String(options.body));
    const response = new Response(JSON.stringify({ version: "api.v1", request_id: "run-ui-fixture",
      data: { execution_permission: { ...permission(body.mode, body.mode === "full"), revision: 2 }, replayed: false } }),
    { status: 202, headers: { "Content-Type": "application/json" } });
    if (deferResponse) await new Promise<void>((resolve) => { release = resolve; });
    return response;
  });
  vi.stubGlobal("fetch", fetch);
  const client = new CyberAgentClient("fixture-read", "/api/v1", "fixture-control", { executionPermissionControlEnabled: true });
  function Host({ runID }: { runID: string }) {
    const { data } = useQuery<RunDetailView>({ queryKey: ["run", runID], enabled: false,
      queryFn: async () => queries.getQueryData<RunDetailView>(["run", runID])! });
    return <ExecutionPermissionPanel client={client} detail={data!} readiness={capabilityReadinessFixture(runID)} />;
  }
  const tree = (runID: string) => <LocaleProvider><QueryClientProvider client={queries}>
    <Host runID={runID} /></QueryClientProvider></LocaleProvider>;
  const view = render(tree("run-ui-a"));
  return { queries, fetch, release: () => release?.(), changeRun: () => view.rerender(tree("run-ui-b")),
    rerender: () => view.rerender(tree("run-ui-a")),
    requests: () => fetch.mock.calls.map(([path, options]) => ({ path: String(path), body: JSON.parse(String(options?.body)) })) };
}

describe("real Run ExecutionPermissionPanel approval acceptance", () => {
  it.each([
    ["ask", "auto", "帮我批准"], ["auto", "ask", "请求批准"],
  ] as const)("uses the Run endpoint for %s to %s with no Full acknowledgement", async (from, to, label) => {
    const user = userEvent.setup(); const fixture = install(permission(from));
    expect(within(screen.getByRole("group", { name: "执行权限档位" })).getAllByRole("button")
      .map((item) => item.getAttribute("aria-label"))).toEqual(["请求批准", "帮我批准", "完全访问权限"]);
    expect(fixture.fetch).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: label }));
    await waitFor(() => expect(fixture.requests()).toEqual([{ path: "/api/v1/runs/run-ui-a/execution-permission",
      body: { mode: to, confirm_full: false, reason: "Run approval preference selection" } }]));
    const headers = new Headers(fixture.fetch.mock.calls[0][1]?.headers);
    expect(headers.get("Authorization")).toBe("Bearer fixture-control");
    expect(headers.get("Idempotency-Key")).toMatch(/^run-approval-preference-/u);
    await waitFor(() => expect(screen.getByRole("button", { name: label })).toHaveAttribute("aria-pressed", "true"));
  });

  it("writes exact confirm_full only after explicit Full confirmation", async () => {
    const user = userEvent.setup(); const fixture = install();
    await user.click(screen.getByRole("button", { name: "完全访问权限" }));
    expect(fixture.fetch).not.toHaveBeenCalled();
    const confirm = within(screen.getByRole("dialog")).getByRole("button", { name: "确认启用" });
    confirm.focus(); await user.keyboard("{Enter}");
    await waitFor(() => expect(fixture.requests()).toEqual([{ path: "/api/v1/runs/run-ui-a/execution-permission",
      body: { mode: "full", confirm_full: true, reason: "Run approval preference selection" } }]));
    await waitFor(() => expect(screen.getByText("完全访问已激活")).toBeInTheDocument());
  });

  it("does not activate cold Full on read/rerender and requires fresh confirmation", async () => {
    const user = userEvent.setup(); const fixture = install(permission("full"));
    fixture.rerender();
    expect(screen.getByText("完全访问未激活")).toBeInTheDocument();
    expect(fixture.fetch).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: /重新激活完全访问权限/u }));
    expect(fixture.fetch).not.toHaveBeenCalled();
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确认重新激活" }));
    await waitFor(() => expect(fixture.requests()).toEqual([{ path: "/api/v1/runs/run-ui-a/execution-permission",
      body: { mode: "full", confirm_full: true, reason: "Run approval preference selection" } }]));
  });

  it.each(["Escape", "取消"])("dismisses Full by %s without a write", async (action) => {
    const user = userEvent.setup(); const fixture = install();
    const full = screen.getByRole("button", { name: "完全访问权限" });
    await user.click(full);
    if (action === "Escape") await user.keyboard("{Escape}");
    else await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(full).toHaveFocus(); expect(fixture.fetch).not.toHaveBeenCalled();
  });

  it("cannot transfer an unconfirmed Full choice from Run A to Run B", async () => {
    const user = userEvent.setup(); const fixture = install();
    await user.click(screen.getByRole("button", { name: "完全访问权限" }));
    fixture.changeRun();
    const stale = screen.queryByRole("dialog");
    if (stale) await user.click(within(stale).getByRole("button", { name: "确认启用" }));
    console.info("Run target switch before confirmation:", JSON.stringify(fixture.requests()));
    expect(fixture.requests()).toEqual([]);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(fixture.queries.getQueryData<RunDetailView>(["run", "run-ui-b"])?.execution_permission.approval_mode).toBe("ask");
  });

  it("does not apply a late Run A result to Run B after the target changes", async () => {
    const user = userEvent.setup(); const fixture = install(permission(), true);
    await user.click(screen.getByRole("button", { name: "完全访问权限" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确认启用" }));
    await waitFor(() => expect(fixture.requests()).toHaveLength(1));
    fixture.changeRun();
    await act(async () => { fixture.release(); });
    await waitFor(() => expect(fixture.queries.getMutationCache().getAll()[0]?.state.status).toBe("success"));
    const a = fixture.queries.getQueryData<RunDetailView>(["run", "run-ui-a"])!.execution_permission;
    const b = fixture.queries.getQueryData<RunDetailView>(["run", "run-ui-b"])!.execution_permission;
    console.info("Late result after target switch:", JSON.stringify({ requests: fixture.requests(), a: a.approval_mode, b: b.approval_mode, bActivation: b.full_activation }));
    expect(fixture.requests()[0].path).toBe("/api/v1/runs/run-ui-a/execution-permission");
    expect(a.approval_mode).toBe("full"); expect(a.full_activation).toBe("active");
    expect(b.approval_mode).toBe("ask"); expect(b.full_activation).toBe("inactive");
  });
});
