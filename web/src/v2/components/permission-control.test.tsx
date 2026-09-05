import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../../api/client";
import type { ThreadExecutionPermissionControlView,
  ThreadExecutionPermissionView } from "../../api/types";
import { V2PermissionControl } from "./permission-control";

function permission(overrides: Partial<ThreadExecutionPermissionView> = {}):
ThreadExecutionPermissionView {
  return {
    thread_id: "thread-1", protocol_version: "thread_execution_permission.v1",
    revision: 1, mode: "conservative", approval_policy: "fixed_templates",
    command_scope: "fixed_templates", filesystem_scope: "workspace_guarded",
    network_scope: "disabled", persistent_terminal: false, background_process: false,
    agent_terminal_input: false, risk_tier: "minimal",
    required_gate: "conservative_control", policy_version: "execution_permission_policy.v1",
    operator_confirmed: false, runtime_gate_available: true,
    runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
      danger_full_access_enabled: true, debug_maximum_access_enabled: true },
    capability_matrix: { workspace_read: true, workspace_write: true,
      sandboxed_command_runtime: false, unsandboxed_host_process: false,
      network_access: false, credential_access: false, user_home_access: false,
      persistent_user_terminal: false, persistent_agent_terminal: false,
      full_cdp: false, out_of_scope_policy: "denied" },
    created_at: "2026-08-29T00:00:00Z", process_enabled: false,
    execution_authorized: false, capability_grant: false,
    applies_to_current_run: true, applies_to_future_successor_runs: true,
    ...overrides,
  };
}

function control(executionPermission = permission()): ThreadExecutionPermissionControlView {
  return { execution_permission: executionPermission, current_run_id: "run-1",
    current_run_effect: "applied", current_run_mode: executionPermission.mode,
    current_run_synchronized: true, replayed: false };
}

function renderPermission(initial = control(), changed = control(permission({
  mode: "full_access", risk_tier: "high", operator_confirmed: true, revision: 2,
}))) {
  const getThreadExecutionPermission = vi.fn().mockResolvedValueOnce(initial).mockResolvedValue(changed);
  const changeThreadExecutionPermission = vi.fn().mockResolvedValue(changed);
  const client = { hasExecutionPermissionControl: true, getThreadExecutionPermission,
    changeThreadExecutionPermission } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  render(<QueryClientProvider client={queryClient}>
    <V2PermissionControl client={client} threadID="thread-1" />
  </QueryClientProvider>);
  return { getThreadExecutionPermission, changeThreadExecutionPermission };
}

describe("V2PermissionControl", () => {
  it("requires explicit confirmation and sends the exact full-access acknowledgement", async () => {
    const user = userEvent.setup();
    const controls = renderPermission();
    const trigger = await screen.findByRole("button", { name: "保守模式" });
    await user.click(trigger);
    const options = screen.getByRole("group", { name: "对话执行权限" });
    await user.click(within(options).getByRole("button", { name: /完全访问/u }));

    expect(controls.changeThreadExecutionPermission).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });
    expect(within(dialog).getByText(/完整 CDP 默认开启，可单独关闭/u)).toBeInTheDocument();
    expect(within(dialog).getByText(/当前执行必须已暂停并处于静止边界/u)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "启用完全访问" }));

    await waitFor(() => expect(controls.changeThreadExecutionPermission).toHaveBeenCalledWith(
      "thread-1",
      { mode: "full_access", reason: "v2 Thread permission selection",
        confirm_danger_full_access: true },
      expect.stringMatching(/^v2-thread-permission-/u),
    ));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(trigger).toHaveFocus();
  });

  it("returns focus to the permission chip when a popover confirmation is cancelled", async () => {
    const user = userEvent.setup();
    renderPermission();
    const trigger = await screen.findByRole("button", { name: "保守模式" });
    await user.click(trigger);
    await user.click(within(screen.getByRole("group", { name: "对话执行权限" }))
      .getByRole("button", { name: /完全访问/u }));
    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });

    await user.click(within(dialog).getByRole("button", { name: "取消" }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("disables permission levels whose runtime gates are unavailable", async () => {
    const user = userEvent.setup();
    renderPermission(control(permission({ runtime: {
      workspace_sandbox_enabled: false, operator_approval_enabled: true,
      danger_full_access_enabled: false, debug_maximum_access_enabled: false,
    } })));
    await user.click(await screen.findByRole("button", { name: "保守模式" }));
    const options = screen.getByRole("group", { name: "对话执行权限" });
    expect(within(options).getByRole("button", { name: /工作区访问/u })).toBeDisabled();
    expect(within(options).getByRole("button", { name: /完全访问/u })).toBeDisabled();
    expect(within(options).getByRole("button", { name: /调试模式/u })).toBeDisabled();
  });

  it("can reactivate a persisted Full choice after a safe cold start", async () => {
    const user = userEvent.setup();
    const initial = control(permission({
      mode: "full_access", approval_policy: "none", command_scope: "arbitrary_stateless",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "danger_full_access", operator_confirmed: true,
      runtime_gate_available: false,
      runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
        danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    }));
    const activated = control(permission({
      mode: "full_access", approval_policy: "none", command_scope: "arbitrary_stateless",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "danger_full_access", operator_confirmed: true, revision: 3,
      runtime_gate_available: true,
      runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
        danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    }));
    const controls = renderPermission(initial, activated);

    const trigger = await screen.findByRole("button", { name: "完全访问" });
    await user.click(trigger);
    expect(screen.getByText("已选择 · 当前进程未授权")).toBeInTheDocument();
    const selected = within(screen.getByRole("group", { name: "对话执行权限" }))
      .getByRole("button", { name: "完全访问" });
    expect(selected).toHaveAttribute("aria-pressed", "true");
    expect(selected).toBeEnabled();
    expect(selected).toHaveTextContent("已保存 · 暂停且静止后确认激活");

    await user.click(selected);
    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });
    await user.click(within(dialog).getByRole("button", { name: "启用完全访问" }));

    await waitFor(() => expect(controls.changeThreadExecutionPermission).toHaveBeenCalledWith(
      "thread-1",
      { mode: "full_access", reason: "v2 Thread permission selection",
        confirm_danger_full_access: true },
      expect.stringMatching(/^v2-thread-permission-/u),
    ));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await user.click(trigger);
    expect(await screen.findByText("已应用到当前和后续执行")).toBeInTheDocument();
  });

  it("requires Full Access confirmation when a persisted Debug choice is cold", async () => {
    const user = userEvent.setup();
    const initial = control(permission({
      mode: "debug", approval_policy: "none", command_scope: "arbitrary_persistent",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "debug_maximum_access", operator_confirmed: true,
      persistent_terminal: true, background_process: true, agent_terminal_input: true,
      runtime_gate_available: false,
      runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
        danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    }));
    const activated = control(permission({
      mode: "full_access", approval_policy: "none", command_scope: "arbitrary_stateless",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "danger_full_access", operator_confirmed: true, revision: 3,
      runtime_gate_available: true,
      runtime: { workspace_sandbox_enabled: true, operator_approval_enabled: true,
        danger_full_access_enabled: true, debug_maximum_access_enabled: false },
    }));
    const controls = renderPermission(initial, activated);

    await user.click(await screen.findByRole("button", { name: "调试模式" }));
    await user.click(within(screen.getByRole("group", { name: "对话执行权限" }))
      .getByRole("button", { name: "完全访问" }));

    expect(controls.changeThreadExecutionPermission).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "要开启完全访问权限吗？" });
    await user.click(within(dialog).getByRole("button", { name: "启用完全访问" }));
    await waitFor(() => expect(controls.changeThreadExecutionPermission).toHaveBeenCalledWith(
      "thread-1",
      { mode: "full_access", reason: "v2 Thread permission selection",
        confirm_danger_full_access: true },
      expect.stringMatching(/^v2-thread-permission-/u),
    ));
  });

  it("drops a high-risk task to conservative mode immediately without another dialog", async () => {
    const user = userEvent.setup();
    const initial = control(permission({
      mode: "full_access", approval_policy: "none", command_scope: "arbitrary_stateless",
      filesystem_scope: "host_full", network_scope: "host", risk_tier: "high",
      required_gate: "danger_full_access", operator_confirmed: true,
    }));
    const changed = control(permission({ revision: 3 }));
    const controls = renderPermission(initial, changed);
    await user.click(await screen.findByRole("button", { name: "完全访问" }));

    await user.click(screen.getByRole("button", { name: /立即降为保守模式/u }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(controls.changeThreadExecutionPermission).toHaveBeenCalledWith(
      "thread-1",
      { mode: "conservative", reason: "v2 Thread permission selection" },
      expect.stringMatching(/^v2-thread-permission-/u),
    ));
  });
});
