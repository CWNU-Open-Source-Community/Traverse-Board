import type { ReactNode } from "react";
import { LocaleProvider } from "../lib/locale";
import { readFileSync } from "node:fs";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render as renderComponent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CyberAgentClient } from "./client";
import { V2PermissionControl } from "../v2/components/permission-control";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.localStorage.removeItem("prayu.locale.v1"); });

function states() {
  const path = process.env.TRAVERSE_TEST_APPROVAL_MODE_OUTPUT;
  if (path) return JSON.parse(readFileSync(path, "utf8"));
  const permission = { thread_id: "thread-1", protocol_version: "thread_execution_permission.v2", revision: 1,
    mode: "ask", approval_mode: "ask", full_activation: "inactive", policy_version: "execution_permission_policy.v2",
    approval_policy: "per_operation", command_scope: "per_operation", filesystem_scope: "per_operation", network_scope: "per_operation",
    risk_tier: "minimal", required_gate: "operation_authority", persistent_terminal: false, background_process: false,
    agent_terminal_input: false, operator_confirmed: false, process_enabled: false, execution_authorized: false, capability_grant: false,
    runtime_gate_available: true, runtime: { workspace_sandbox_enabled: false, operator_approval_enabled: true,
      danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    capability_matrix: {}, created_at: "2026-10-02T00:00:00Z", applies_to_current_run: true, applies_to_future_successor_runs: true };
  return ["ask", "auto", "full", "cold", "ask"].map((mode, i) => ({
    execution_permission: { ...permission, revision: i+1, mode: mode === "cold" ? "full" : mode,
      approval_mode: mode === "cold" ? "full" : mode, full_activation: mode === "full" ? "active" : "inactive",
      operator_confirmed: ["full", "cold"].includes(mode), runtime_gate_available: mode !== "cold" },
    current_run_id: "run-1", current_run_mode: mode === "cold" ? "full" : mode,
    current_run_effect: "applied", current_run_synchronized: true, replayed: false,
  }));
}

function install(initial = states()[0]) {
  const responses = states();
  let current = initial;
  const fetch = vi.fn(async (_path: unknown, options?: RequestInit) => {
    const request = options?.method === "POST" ? JSON.parse(String(options.body)) : null;
    const data = request ? responses.find((value: any) => value.execution_permission.mode === request.mode &&
      (request.mode !== "full" || value.execution_permission.full_activation === "active")) : current;
    if (request) current = data;
    return new Response(JSON.stringify({ version: "api.v1", request_id: "approval-mode-test", data }),
      { status: request ? 202 : 200, headers: { "Content-Type": "application/json" } });
  });
  vi.stubGlobal("fetch", fetch);
  const client = new CyberAgentClient("read", "/api/v1", "control", { executionPermissionControlEnabled: true });
  return { client, fetch, threadID: initial.execution_permission.thread_id,
    posts: () => fetch.mock.calls.filter(([, options]) => options?.method === "POST") };
}

describe("actual permission API to shared selector", () => {
  it.each(["ask", "auto", "full", "cold"])("reads %s without issuing a write", async (mode) => {
    const initial = states()[["ask", "auto", "full", "cold"].indexOf(mode)];
    const fixture = install(initial);
    const value = await fixture.client.getThreadExecutionPermission(fixture.threadID);
    expect(value.execution_permission).toEqual(initial.execution_permission);
    expect(fixture.posts()).toHaveLength(0);
  });

  it.each(["menu", "settings"] as const)("%s writes only three-mode requests and confirms Full", async (variant) => {
    const fixture = install();
    const user = userEvent.setup();
    const queries = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(<QueryClientProvider client={queries}><V2PermissionControl client={fixture.client} threadID={fixture.threadID} variant={variant} /></QueryClientProvider>);
    if (variant === "menu") await user.click(await screen.findByRole("button", { name: "请求批准" }));
    const role = variant === "menu" ? "menuitemradio" : "button";
    const group = await screen.findByRole("group", { name: "执行权限档位" });
    expect(within(group).getAllByRole(role).map((item) => item.getAttribute("aria-label")))
      .toEqual(["请求批准", "帮我批准", "完全访问权限"]);
    await user.click(screen.getByRole(role, { name: "帮我批准" }));
    await waitFor(() => expect(fixture.posts()).toHaveLength(1));
    expect(JSON.parse(String(fixture.posts()[0]![1]!.body))).toEqual({ mode: "auto", confirm_full: false, reason: "Thread approval preference selection" });
    if (variant === "menu") await user.click(await screen.findByRole("button", { name: "帮我批准" }));
    await user.click(screen.getByRole(role, { name: "完全访问权限" }));
    expect(fixture.posts()).toHaveLength(1);
    const dialog = screen.getByRole("dialog", { name: "启用完全访问权限？" });
    await user.click(within(dialog).getByRole("button", { name: "确认启用" }));
    await waitFor(() => expect(fixture.posts()).toHaveLength(2));
    expect(JSON.parse(String(fixture.posts()[1]![1]!.body))).toEqual({ mode: "full", confirm_full: true, reason: "Thread approval preference selection" });
    expect(fixture.posts()[1]![1]!.headers).toMatchObject({ Authorization: "Bearer control" });
  });

  it("keeps a persisted cold Full choice inactive until reconfirmation", async () => {
    const fixture = install(states()[3]);
    const user = userEvent.setup();
    const queries = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queries}><V2PermissionControl client={fixture.client} threadID={fixture.threadID} /></QueryClientProvider>);
    await user.click(await screen.findByRole("button", { name: "完全访问权限 · 未激活" }));
    expect(fixture.posts()).toHaveLength(0);
    await user.click(screen.getByRole("menuitem", { name: /重新激活完全访问权限/u }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确认重新激活" }));
    await waitFor(() => expect(fixture.posts()).toHaveLength(1));
  });

  it.each(["conservative", "workspace_access", "approval", "full_access", "debug"])("projects old %s history without rewriting it", async (mode) => {
    const initial = states()[0];
    Object.assign(initial.execution_permission, { protocol_version: "thread_execution_permission.v1", policy_version: "execution_permission_policy.v1", mode });
    delete initial.execution_permission.approval_mode;
    delete initial.execution_permission.full_activation;
    const fixture = install(initial);
    const result = await fixture.client.getThreadExecutionPermission(fixture.threadID);
    expect(result.execution_permission.mode).toBe(mode);
    expect(result.execution_permission.approval_mode).toBe(["full_access", "debug"].includes(mode) ? "full" : "ask");
    expect(result.execution_permission.full_activation).toBe("inactive");
    expect(fixture.posts()).toHaveLength(0);
  });

  it.each(["legacy mode", "legacy activation", "bad projection", "stored authority", "missing activation"])("rejects inconsistent response: %s", async (failure) => {
    const initial = states()[0];
    if (failure === "legacy mode") initial.execution_permission.mode = "full_access";
    if (failure === "legacy activation") Object.assign(initial.execution_permission, { protocol_version: "thread_execution_permission.v1", policy_version: "execution_permission_policy.v1", mode: "debug", approval_mode: "full", full_activation: "active" });
    if (failure === "bad projection") initial.execution_permission.approval_mode = "auto";
    if (failure === "stored authority") initial.execution_permission.capability_grant = true;
    if (failure === "missing activation") delete initial.execution_permission.full_activation;
    const fixture = install(initial);
    await expect(fixture.client.getThreadExecutionPermission(fixture.threadID)).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  });
});

// Use the same explicit locale boundary as the real application.
function render(ui: ReactNode) {
  window.localStorage.setItem("prayu.locale.v1", "zh-CN");
  return renderComponent(ui, { wrapper: LocaleProvider });
}
