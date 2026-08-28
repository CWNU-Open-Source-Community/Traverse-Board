import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CyberAgentClient } from "../api/client";
import { ThreadPermissionSettings, type ThreadExecutionPermissionControlView,
  type ThreadExecutionPermissionView } from "./thread-permission-settings";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

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
    created_at: "2026-08-28T00:00:00Z", process_enabled: false,
    execution_authorized: false, capability_grant: false,
    applies_to_current_run: true, applies_to_future_successor_runs: true,
    ...overrides,
  };
}

function control(overrides: Partial<ThreadExecutionPermissionControlView> = {}):
ThreadExecutionPermissionControlView {
  return { execution_permission: permission(), current_run_id: "run-1",
    current_run_effect: "applied", current_run_mode: "conservative",
    current_run_synchronized: true, replayed: false, ...overrides };
}

function renderPermission(client: CyberAgentClient, threadID = "thread-1") {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  return render(<QueryClientProvider client={queryClient}>
    <ThreadPermissionSettings client={client} threadID={threadID} />
  </QueryClientProvider>);
}

function controlClient(result = control()) {
  const client = new CyberAgentClient("read", "/api/v1", "control", {
    executionPermissionControlEnabled: true,
  });
  vi.spyOn(client, "get").mockResolvedValue(result);
  return client;
}

describe("ThreadPermissionSettings", () => {
  afterEach(() => vi.restoreAllMocks());

  it("uses a Thread-first empty state and explains the conservative default", () => {
    const client = controlClient();
    renderPermission(client, "");
    expect(screen.getByText("从侧栏打开一个 Thread")).toBeInTheDocument();
    expect(screen.getByText(/新 Thread 默认使用保守模式/)).toBeInTheDocument();
    expect(screen.queryByText("选择一个 Run")).not.toBeInTheDocument();
    expect(client.get).not.toHaveBeenCalled();
  });

  it("shows one setting for the current and future Runs without requiring an active Run", async () => {
    const client = controlClient(control({ current_run_id: undefined,
      current_run_effect: "no_active_run", current_run_mode: undefined,
      current_run_synchronized: false,
      execution_permission: permission({ applies_to_current_run: false }) }));
    renderPermission(client);
    expect(await screen.findByText("设置一次，应用到整个 Thread")).toBeInTheDocument();
    expect(screen.getByText(/当前没有活动 Run；此设置会用于该 Thread 的下一个及后续 Run/))
      .toBeInTheDocument();
    expect(screen.getByText("当前与后续 Run")).toBeInTheDocument();
  });

  it("reports when a running Run was safely paused and updated", async () => {
    const client = controlClient(control({ current_run_effect: "paused_and_applied" }));
    renderPermission(client);
    expect(await screen.findByText(/当前 Run 已安全暂停并应用；后续 Run 会继承此设置/))
      .toBeInTheDocument();
  });

  it("disables levels whose runtime gate was not enabled at startup", async () => {
    const client = controlClient(control({ execution_permission: permission({
      runtime: { workspace_sandbox_enabled: false, operator_approval_enabled: true,
        danger_full_access_enabled: false, debug_maximum_access_enabled: false },
    }) }));
    const user = userEvent.setup();
    renderPermission(client);
    expect(await screen.findByRole("button", { name: /工作区执行/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /工作区执行/ }))
      .toHaveTextContent("启动时不可用");
    await user.click(screen.getByText("高级风险权限"));
    expect(screen.getByRole("button", { name: /完全访问/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /调试模式/ })).toBeDisabled();
  });

  it.each([
    ["工作区执行", "workspace_access", "confirm_workspace_access"],
    ["用户审批", "approval", "confirm_user_approval"],
    ["完全访问", "full_access", "confirm_danger_full_access"],
    ["调试模式", "debug", "confirm_debug_access"],
  ] as const)("requires confirmation and sends the exact %s acknowledgement",
    async (label, mode, confirmation) => {
      const client = controlClient();
      const post = vi.spyOn(client, "postControl").mockResolvedValue(control({
        execution_permission: permission({ mode, operator_confirmed: true, revision: 2 }),
      }));
      const user = userEvent.setup();
      renderPermission(client);
      await screen.findByText("设置一次，应用到整个 Thread");
      if (mode === "full_access" || mode === "debug") {
        await user.click(screen.getByText("高级风险权限"));
      }
      await user.click(screen.getByRole("button", { name: new RegExp(label) }));
      expect(post).not.toHaveBeenCalled();
      await user.click(screen.getByRole("button", { name: "确认" }));
      await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
      expect(post).toHaveBeenCalledWith(
        "/threads/thread-1/execution-permission",
        { mode, reason: "settings Thread execution permission selection",
          [confirmation]: true },
        expect.stringMatching(/^settings-thread-execution-permission-/u),
      );
    });
});
