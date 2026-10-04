import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { APIRequestError, CyberAgentClient } from "../api/client";
import type { RunDetailView } from "../api/types";
import { capabilityReadinessFixture, patchCapabilityReadiness } from
  "../test/capability-readiness";
import { BrowserCDPPermissionPanel, ExecutionPermissionPanel,
  RunPermissionSettings, StandardCodeReadinessPanel } from "./run-permission-settings";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

const baseDetail = {
  run: { id: "run-1", mission_id: "mission-1", session_id: "session-1", status: "paused",
    config: { model_route: "mock/model", interactive: true },
    budget: { max_turns: 2, max_tokens: 0, max_tool_calls: 10, max_cost_usd: 0,
      timeout_seconds: 0 }, created_at: "2026-07-27T00:00:00Z",
    updated_at: "2026-07-27T00:00:00Z" },
  mission: { id: "mission-1", goal: "test permission mode", profile: "code",
    workspace_id: "workspace-1", scope: { workspace_id: "workspace-1",
      network_mode: "disabled", allowed_targets: [] },
    created_at: "2026-07-27T00:00:00Z", updated_at: "2026-07-27T00:00:00Z" },
  mode: { protocol_version: "run_mode.v1", revision: 1, surface: "code", phase: "deliver",
    profile: "code", scope: { workspace_id: "workspace-1", network_mode: "disabled",
      allowed_targets: [] }, policy_version: "mode_policy.v1", requested_by: "test",
    reason: "test", created_at: "2026-07-27T00:00:00Z", capability_grant: false },
  execution_profile: { protocol_version: "run_execution_profile.v1", revision: 1,
    profile: "local", backend: "local", approval_policy: "always",
    filesystem_scope: "workspace", network_scope: "disabled", risk_tier: "high",
    required_gate: "local_os_sandbox_gate", policy_version: "execution_profile_policy.v1",
    created_at: "2026-07-27T00:00:00Z", process_enabled: false,
    execution_authorized: false, capability_grant: false },
  operator_steering: { pending: 0, prepared: 0, committed: 0, cancelled: 0, messages: [] },
  tool_usage: { consumed: 0, limit: 10, remaining: 10 },
} as const;

function detail(): RunDetailView {
  return {
    ...baseDetail,
    execution_interaction: {
      protocol_version: "run_execution_interaction.v1", revision: 1,
      mode: "preview", surface: "code", execution_profile: "local",
      execution_profile_revision: 1, workspace_trust: "untrusted",
      command_form: "none", persistent_terminal: false, user_input_available: false,
      agent_input_default: false, network_scope: "disabled", required_gate: "none",
      policy_version: "execution_interaction_policy.v1", operator_confirmed: false,
      created_at: "2026-07-27T00:00:00Z", process_enabled: false,
      execution_authorized: false, capability_grant: false,
    },
    execution_permission: {
      protocol_version: "run_execution_permission.v1", revision: 1,
      // The current Go reader preserves legacy mode while projecting the UI fields.
      mode: "conservative", approval_mode: "ask", full_activation: "inactive",
      approval_policy: "fixed_templates",
      command_scope: "fixed_templates", filesystem_scope: "workspace_guarded",
      network_scope: "disabled", persistent_terminal: false, background_process: false,
      agent_terminal_input: false, risk_tier: "minimal",
      required_gate: "conservative_control",
      policy_version: "execution_permission_policy.v1", operator_confirmed: false,
      runtime_gate_available: true,
      runtime: { workspace_sandbox_enabled: false,
        operator_approval_enabled: true, danger_full_access_enabled: true,
         },
      created_at: "2026-07-27T00:00:00Z", process_enabled: false,
      execution_authorized: false, capability_grant: false,
    },
    browser_cdp_permission: {
      protocol_version: "run_browser_cdp_permission.v1", revision: 1,
      mode: "restricted", navigate_allowed: true, dom_snapshot_allowed: true,
      screenshot_allowed: true, request_capture_allowed: false,
      request_mutation_allowed: false, request_replay_allowed: false,
      cookie_access_allowed: false, arbitrary_method_allowed: false,
      risk_tier: "minimal", required_gate: "browser_cdp_control",
      policy_version: "browser_cdp_permission_policy.v1", operator_confirmed: false,
      runtime_gate_available: true,
      runtime: { control_enabled: true, full_debug_enabled: true,
        execution_debug_selected: false },
      created_at: "2026-07-27T00:00:00Z", transport_enabled: false,
      browser_start_authorized: false, runtime_authorized: false, capability_grant: false,
    },
  } as unknown as RunDetailView;
}

function standardCodeReadyReadiness() {
  return patchCapabilityReadiness(capabilityReadinessFixture(),
    "presets", "standard_code", {
      selectable: true, runtime_available: true, blocked_by: [], remediation: [],
      restart_required: false,
    });
}

describe("ExecutionPermissionPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("keeps only three preferences and disables Full with the exact host unavailability reason", async () => {
    const unavailable = detail();
    unavailable.execution_permission.full_activation = "unavailable";
    unavailable.execution_permission.full_unavailable_reason = "Full adapter unavailable: host policy; restart required";
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    render(<QueryClientProvider client={new QueryClient()}>
      <ExecutionPermissionPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          executionPermissionControlEnabled: true,
        })}
        detail={unavailable}
        readiness={capabilityReadinessFixture()}
      />
    </QueryClientProvider>);
    const choices = within(screen.getByRole("group", { name: "执行权限档位" }));
    expect(choices.getAllByRole("button")).toHaveLength(3);
    expect(choices.getByRole("button", { name: "请求批准" })).toBeDisabled();
    expect(choices.getByRole("button", { name: "请求批准" })).toHaveAttribute("aria-pressed", "true");
    expect(choices.getByRole("button", { name: "帮我批准" })).toBeEnabled();
    const full = choices.getByRole("button", { name: "完全访问权限" });
    expect(full).toBeDisabled();
    expect(full).toHaveAccessibleDescription(expect.stringContaining(
      unavailable.execution_permission.full_unavailable_reason));
    expect(screen.getByText(unavailable.execution_permission.full_unavailable_reason)).toBeVisible();
    await userEvent.setup().click(full);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("writes Auto as a preference without a Workspace or host authority grant", async () => {
    const selected = {
      ...detail().execution_permission,
      protocol_version: "run_execution_permission.v2", policy_version: "execution_permission_policy.v2",
      mode: "auto", approval_mode: "auto", approval_policy: "per_operation",
      command_scope: "per_operation", filesystem_scope: "per_operation", network_scope: "per_operation",
      required_gate: "operation_authority", revision: 2,
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-auto-preference",
      data: { execution_permission: selected, replayed: false },
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    render(<QueryClientProvider client={new QueryClient()}>
      <ExecutionPermissionPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          executionPermissionControlEnabled: true,
        })}
        detail={detail()}
        readiness={capabilityReadinessFixture()}
      />
    </QueryClientProvider>);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByText(/影响未知、敏感数据外发、破坏性或共享写入操作不会/u)).toBeVisible();
    await userEvent.setup().click(screen.getByRole("button", { name: "帮我批准" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/runs/run-1/execution-permission");
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("Authorization")).toBe("Bearer control");
    expect(new Headers(init.headers).get("Idempotency-Key")).toMatch(/^run-approval-preference-/u);
    expect(JSON.parse(String(init.body))).toEqual({
      mode: "auto", confirm_full: false, reason: "Run approval preference selection",
    });
  });

  it("requires an explicit confirmation before selecting Full and cancellation never writes", async () => {
    const selected = {
      ...detail().execution_permission,
      protocol_version: "run_execution_permission.v2", policy_version: "execution_permission_policy.v2",
      mode: "full", approval_mode: "full", full_activation: "active", approval_policy: "per_operation",
      command_scope: "per_operation", filesystem_scope: "per_operation", network_scope: "per_operation",
      risk_tier: "high", required_gate: "operation_authority", operator_confirmed: true, revision: 2,
    };
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-permission",
      data: { execution_permission: selected, replayed: false },
    }), { status: 202, headers: { "Content-Type": "application/json" } })));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <ExecutionPermissionPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          executionPermissionControlEnabled: true,
        })}
        detail={detail()}
        readiness={capabilityReadinessFixture()}
      />
    </QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "完全访问权限" }));
    expect(fetchMock).not.toHaveBeenCalled();
    let dialog = within(screen.getByRole("dialog", { name: "启用完全访问权限？" }));
    expect(dialog.getByText(/实际可用范围仍受操作系统、供应商和运行环境限制/u)).toBeVisible();
    await user.click(dialog.getByRole("button", { name: "取消" }));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "完全访问权限" }));
    dialog = within(screen.getByRole("dialog", { name: "启用完全访问权限？" }));
    await user.click(dialog.getByRole("button", { name: "确认启用" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/runs/run-1/execution-permission");
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("Authorization")).toBe("Bearer control");
    expect(JSON.parse(String(init.body))).toEqual({
      mode: "full", confirm_full: true, reason: "Run approval preference selection",
    });
  });

  it("drops an elevated confirmation when the selected Run changes", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const client = new CyberAgentClient("read", "/api/v1", "control", {
      runControlEnabled: true,
      executionPermissionControlEnabled: true,
    });
    const queryClient = new QueryClient({
      defaultOptions: { queries: { staleTime: Number.POSITIVE_INFINITY } },
    });
    const runOne = detail();
    const runTwo = {
      ...detail(),
      run: { ...detail().run, id: "run-2" },
    } as RunDetailView;
    queryClient.setQueryData(["run", "run-1"], runOne);
    queryClient.setQueryData(["run", "run-2"], runTwo);
    queryClient.setQueryData(["run", "run-1", "capability-readiness"],
      capabilityReadinessFixture("run-1"));
    queryClient.setQueryData(["run", "run-2", "capability-readiness"],
      capabilityReadinessFixture("run-2"));
    const user = userEvent.setup();
    const view = render(<QueryClientProvider client={queryClient}>
      <RunPermissionSettings client={client} runID="run-1" />
    </QueryClientProvider>);

    await user.click(screen.getByRole("button", { name: "完全访问权限" }));
    const oldConfirm = screen.getByRole("button", { name: "确认启用" });
    expect(oldConfirm).toBeInTheDocument();

    view.rerender(<QueryClientProvider client={queryClient}>
      <RunPermissionSettings client={client} runID="run-2" />
    </QueryClientProvider>);
    expect(screen.queryByRole("button", { name: "确认启用" })).not.toBeInTheDocument();
    await act(async () => { fireEvent.click(oldConfirm); });
    expect(fetchMock).not.toHaveBeenCalled();
    expect(queryClient.getQueryData(["run", "run-1"])).toEqual(runOne);
    expect(queryClient.getQueryData(["run", "run-2"])).toEqual(runTwo);
    expect(screen.getByText((_, element) =>
      element?.tagName === "SPAN" && element.textContent?.startsWith("run-2") === true,
    )).toBeInTheDocument();
  });

  it.each([
    ["read token only", "", true],
    ["control gate disabled", "control", false],
  ] as const)("cannot write any preference with %s", async (_name, controlToken, enabled) => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <ExecutionPermissionPanel client={new CyberAgentClient("read", "/api/v1", controlToken, {
        executionPermissionControlEnabled: enabled,
      })} detail={detail()} readiness={capabilityReadinessFixture()} />
    </QueryClientProvider>);
    const buttons = within(screen.getByRole("group", { name: "执行权限档位" })).getAllByRole("button");
    expect(buttons).toHaveLength(3);
    for (const button of buttons) {
      expect(button).toBeDisabled();
      await user.click(button);
    }
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("StandardCodeReadinessPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("keeps an already configured V2 task in Deliver without treating the preset tuple or current grant as configuration", async () => {
    const configured = { ...detail(), run: { ...detail().run, standard_code_preset_configured: true } };
    const readiness = standardCodeReadyReadiness();
    expect(readiness.presets[0]?.selected).toBe(false);
    expect(readiness.command_runtime.current_run_granted).toBe(false);
    const configureStandardCode = vi.fn();
    const client = { hasStandardCodePreset: true, configureStandardCode } as unknown as CyberAgentClient;
    const queryClient = new QueryClient();
    const content = (threadID?: string) => <QueryClientProvider client={queryClient}>
      <StandardCodeReadinessPanel client={client} detail={configured} readiness={readiness} threadID={threadID} />
    </QueryClientProvider>;
    const view = render(content("thread-1"));
    const delivering = screen.getByRole("button", { name: /交付中/ });
    expect(delivering).toBeDisabled();
    expect(delivering).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByRole("button", { name: /开始编码/ })).not.toBeInTheDocument();
    expect(screen.queryByText("受阻")).not.toBeInTheDocument();
    await userEvent.setup().click(delivering);
    expect(configureStandardCode).not.toHaveBeenCalled();
    view.rerender(content());
    expect(screen.getByRole("button", { name: /开始编码/ })).toBeEnabled();
  });

  it("shows protocol, installed adapter, backend readiness, and current Run grant separately", () => {
    const readiness = capabilityReadinessFixture();
    readiness.command_runtime = {
      protocol_available: true, adapter_installed: true, adapter_ready: true,
      current_run_granted: true, adapter_kind: "sandboxed_workspace",
      backend: "local_windows_sandbox",
    };
    render(<QueryClientProvider client={new QueryClient()}>
      <StandardCodeReadinessPanel
        client={new CyberAgentClient("read", "/api/v1")}
        detail={detail()}
        readiness={readiness} />
    </QueryClientProvider>);

    expect(screen.getByText("存在")).toBeInTheDocument();
    expect(screen.getByText("已安装")).toBeInTheDocument();
    expect(screen.getByText("就绪")).toBeInTheDocument();
    expect(screen.getByText("已授予")).toBeInTheDocument();
    expect(screen.getByText("sandboxed_workspace · local_windows_sandbox"))
      .toBeInTheDocument();
  });

  it("uses the atomic preset endpoint and requires exact Workspace source confirmation", async () => {
    const trustDigest = "a".repeat(64);
    const blocked = {
      action: "configure", backend_intent: "auto",
      blocked_by: ["workspace_untrusted"], capability_grant: false,
      credentials: "none",
      docker_readiness: { backend: "docker", available: false,
        blocked_by: ["docker_unavailable"], remediation: ["install_or_start_docker"] },
      drydock_ready: false,
      local_readiness: { backend: "local", available: true,
        blocked_by: [], remediation: [] },
      network: "disabled", next_steps: ["confirm_workspace_trust"],
      protocol_version: "standard_code_preset.v1", replayed: false,
      run_id: "run-1", selected_backend: "local",
      selection_reason: "auto_local_ready", status: "blocked",
      trust_digest: trustDigest, trust_required: true, workspace_id: "workspace-1",
    };
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-standard-code", data: blocked,
    }), { status: 202, headers: { "Content-Type": "application/json" } })));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <StandardCodeReadinessPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          runControlEnabled: true, standardCodePresetEnabled: true,
        })}
        detail={detail()}
        readiness={standardCodeReadyReadiness()} />
    </QueryClientProvider>);

    await user.click(screen.getByRole("button", { name: /开始编码/ }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [firstURL, firstInit] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(firstURL).toContain("/runs/run-1/standard-code/preset");
    expect(JSON.parse(String(firstInit.body))).toEqual({
      version: "standard_code_preset.v1", backend_intent: "auto",
      confirm_workspace_trust: false,
    });
    expect(screen.getByText("确认工作区来源")).toBeInTheDocument();
    expect(screen.getByText(new RegExp(trustDigest))).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    const [, secondInit] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(JSON.parse(String(secondInit.body))).toEqual({
      version: "standard_code_preset.v1", backend_intent: "auto",
      confirm_workspace_trust: true, expected_trust_digest: trustDigest,
    });
    expect(new Headers(firstInit.headers).get("Idempotency-Key"))
      .not.toBe(new Headers(secondInit.headers).get("Idempotency-Key"));

    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    const [, thirdInit] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(new Headers(secondInit.headers).get("Idempotency-Key"))
      .toBe(new Headers(thirdInit.headers).get("Idempotency-Key"));
  });

  it("keeps pause-and-configure visibly incomplete until the lease is released", async () => {
    const running = { ...detail(), run: { ...detail().run, status: "running" as const } };
    const readiness = patchCapabilityReadiness(capabilityReadinessFixture(),
      "presets", "standard_code", {
        selectable: false, runtime_available: false,
        blocked_by: ["run_not_quiescent", "execution_lease_active"],
        remediation: ["pause_run", "wait_for_execution_lease"], restart_required: false,
      });
    const waiting = {
      action: "pause_and_configure", backend_intent: "auto",
      blocked_by: ["execution_lease_active"], capability_grant: false,
      credentials: "none",
      docker_readiness: { backend: "docker", available: false,
        blocked_by: ["docker_unavailable"], remediation: ["install_or_start_docker"] },
      drydock_ready: false,
      local_readiness: { backend: "local", available: true,
        blocked_by: [], remediation: [] },
      network: "disabled", next_steps: ["wait_for_quiescence"],
      protocol_version: "standard_code_preset.v1", replayed: false,
      run_id: "run-1", selected_backend: "local",
      selection_reason: "auto_local_ready", status: "waiting_for_pause",
      trust_required: false, workspace_id: "workspace-1",
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-standard-code-waiting", data: waiting,
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <StandardCodeReadinessPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          runControlEnabled: true, standardCodePresetEnabled: true,
        })}
        detail={running as RunDetailView} readiness={readiness} />
    </QueryClientProvider>);

    const start = screen.getByRole("button", { name: /暂停并开始编码/ });
    expect(start).toBeEnabled();
    expect(start).toHaveTextContent("暂时锁定");
    await user.click(start);
    expect(await screen.findByText("暂停尚未完成")).toBeInTheDocument();
    expect(screen.getByText(/执行租约释放前不会提交 Standard Code 配置/))
      .toBeInTheDocument();
    expect(fetchMock.mock.calls[0]?.[0])
      .toContain("/runs/run-1/standard-code/pause-and-configure");
  });

  it("preserves the incompatible source Run when Go creates a new Code Run", async () => {
    const original = detail();
    const successor = detail();
    successor.run = { ...successor.run, id: "run-new-code" };
    const configured = {
      action: "configure", backend_intent: "auto", blocked_by: [],
      capability_grant: false, credentials: "none",
      docker_readiness: { backend: "docker", available: false,
        blocked_by: ["docker_unavailable"], remediation: ["install_or_start_docker"] },
      drydock_ready: true,
      local_readiness: { backend: "local", available: true,
        blocked_by: [], remediation: [] },
      network: "disabled", next_steps: [], protocol_version: "standard_code_preset.v1",
      replayed: false, run_id: "run-new-code", selected_backend: "local",
      selection_reason: "auto_local_ready", status: "configured",
      trust_required: false, workspace_id: "workspace-1",
      run: successor.run, mode: successor.mode,
      execution_profile: successor.execution_profile,
      execution_interaction: successor.execution_interaction,
      execution_permission: successor.execution_permission,
      browser_cdp_permission: successor.browser_cdp_permission,
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-standard-code-successor", data: configured,
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const queryClient = new QueryClient();
    queryClient.setQueryData(["run", "run-1"], original);
    const user = userEvent.setup();
    render(<QueryClientProvider client={queryClient}>
      <StandardCodeReadinessPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          runControlEnabled: true, standardCodePresetEnabled: true,
        })}
        detail={original}
        readiness={standardCodeReadyReadiness()} />
    </QueryClientProvider>);

    await user.click(screen.getByRole("button", { name: /开始编码/ }));
    expect(await screen.findByText(/已创建新的 Code Run/)).toHaveTextContent("run-new-");
    expect(queryClient.getQueryData<RunDetailView>(["run", "run-1"])?.run.id)
      .toBe("run-1");
  });

  it("retains the original unknown configuration across Run changes and unmounts without cross-scope callbacks", async () => {
    let finishFirst!: (value: unknown) => void;
    const configureStandardCode = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { finishFirst = resolve; }))
      .mockRejectedValueOnce(new Error("configuration response lost"));
    const client = { hasStandardCodePreset: true, configureStandardCode } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const second = { ...detail(), run: { ...detail().run, id: "run-2" } };
    const content = (value: RunDetailView, threadID: string) => <QueryClientProvider client={queryClient}>
      <StandardCodeReadinessPanel client={client} detail={value} readiness={standardCodeReadyReadiness()} threadID={threadID} />
    </QueryClientProvider>;
    const user = userEvent.setup();
    const view = render(content(detail(), "thread-1"));
    await user.click(screen.getByRole("button", { name: /开始编码/ }));
    view.rerender(content(second, "thread-2"));
    await user.click(screen.getByRole("button", { name: /开始编码/ }));
    await screen.findByText(/configuration response lost/);
    const original = configureStandardCode.mock.calls[1];
    expect(screen.getByRole("button", { name: /开始编码/ })).toBeDisabled();
    const secondIntent = queryClient.getQueryData(["run", "run-2", "standard-code-preset-intent"]);
    view.unmount();
    const configured = (value: RunDetailView) => ({ status: "configured", run_id: value.run.id, run: value.run,
      mode: value.mode, execution_profile: value.execution_profile, execution_interaction: value.execution_interaction,
      execution_permission: value.execution_permission, browser_cdp_permission: value.browser_cdp_permission,
      network: "disabled", credentials: "none", next_steps: [], docker_readiness: { available: false } });
    invalidate.mockClear();
    await act(async () => finishFirst(configured(detail())));
    expect(queryClient.getQueryData(["run", "run-2", "standard-code-preset-intent"])).toEqual(secondIntent);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["run", "run-1"] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["v2", "thread", "thread-1", "permission"] });
    expect(invalidate).not.toHaveBeenCalledWith({ queryKey: ["run", "run-2"] });
    expect(invalidate).not.toHaveBeenCalledWith({ queryKey: ["v2", "thread", "thread-2"] });
    configureStandardCode.mockResolvedValueOnce(configured(second));
    render(content({ ...second, run: { ...second.run, standard_code_preset_configured: true } }, "thread-2"));
    expect(screen.getByRole("button", { name: /交付中/ })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "确认上次编码配置" }));
    await waitFor(() => expect(configureStandardCode).toHaveBeenCalledTimes(3));
    expect(configureStandardCode.mock.calls[2]).toEqual(original);
    await waitFor(() => expect(screen.queryByRole("button", { name: "确认上次编码配置" })).not.toBeInTheDocument());
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["v2", "thread", "thread-2", "permission"] });
  });

  it.each([true, undefined] as const)("only restarts an invalidated configuration after explicit recheck (marker %s)", async (marker) => {
    const trust = { status: "blocked", run_id: "run-1", action: "configure", backend_intent: "auto", trust_required: true,
      trust_digest: "a".repeat(64), next_steps: ["confirm_workspace_trust"], docker_readiness: { available: false },
      network: "disabled", credentials: "none" };
    const configureStandardCode = vi.fn().mockResolvedValueOnce(trust)
      .mockRejectedValueOnce(new APIRequestError("Configuration changed", "CONFLICT", 409, "request-1", undefined, marker))
      .mockResolvedValueOnce({ ...trust, trust_digest: "b".repeat(64) });
    const client = { hasStandardCodePreset: true, configureStandardCode } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const content = <QueryClientProvider client={queryClient}><StandardCodeReadinessPanel
      client={client} detail={detail()} readiness={standardCodeReadyReadiness()} threadID="thread-1" /></QueryClientProvider>;
    const user = userEvent.setup();
    const view = render(content);
    await user.click(screen.getByRole("button", { name: /开始编码/ }));
    await user.click(await screen.findByRole("button", { name: "确认" }));
    const retryLabel = marker ? "重新核对编码配置" : "确认上次编码配置";
    await screen.findByRole("button", { name: retryLabel });
    const original = configureStandardCode.mock.calls[1];
    expect(original[2]).toMatchObject({ confirm_workspace_trust: true, expected_trust_digest: "a".repeat(64) });
    expect(screen.getByRole("button", { name: /开始编码/ })).toBeDisabled();
    view.unmount();
    render(content);
    await user.click(screen.getByRole("button", { name: retryLabel }));
    await waitFor(() => expect(configureStandardCode).toHaveBeenCalledTimes(3));
    const retry = configureStandardCode.mock.calls[2];
    if (marker) {
      expect(retry[0]).toBe(original[0]);
      expect(retry[2]).toEqual({ version: "standard_code_preset.v1", backend_intent: "auto", confirm_workspace_trust: false });
      expect(retry[3]).not.toBe(original[3]);
    } else expect(retry).toEqual(original);
    await screen.findByText(new RegExp("b".repeat(64)));
    expect(configureStandardCode).toHaveBeenCalledTimes(3);
  });
});

describe("BrowserCDPPermissionPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows the sensitive warning and keeps full CDP disabled outside Debug", async () => {
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <BrowserCDPPermissionPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          browserCDPPermissionControlEnabled: true,
          fullCDPDebugEnabled: true,
        })}
        detail={detail()}
        readiness={capabilityReadinessFixture()}
      />
    </QueryClientProvider>);
    await user.click(screen.getByText("高级浏览器调试"));
    expect(screen.getByText("高度敏感权限")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /完整 CDP/ })).toBeDisabled();
    expect(screen.getByText(/浏览器启动、CDP 传输与运行时授权仍保持关闭/))
      .toBeInTheDocument();
  });

  it("requires confirmation and sends only the typed full-debug selection", async () => {
    const debugDetail = {
      ...detail(),
      execution_permission: {
        ...detail().execution_permission,
        mode: "debug" as const, approval_mode: "full" as const,
      },
      browser_cdp_permission: {
        ...detail().browser_cdp_permission,
        runtime: {
          ...detail().browser_cdp_permission.runtime,
          execution_debug_selected: true,
        },
      },
    } as RunDetailView;
    const selected = {
      ...debugDetail.browser_cdp_permission,
      mode: "full_debug" as const,
      request_capture_allowed: true, request_mutation_allowed: true,
      request_replay_allowed: true, cookie_access_allowed: true,
      arbitrary_method_allowed: true, risk_tier: "high" as const,
      required_gate: "full_cdp_debug" as const, operator_confirmed: true, revision: 2,
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-browser-cdp",
      data: { browser_cdp_permission: selected, replayed: false },
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    const readiness = patchCapabilityReadiness(capabilityReadinessFixture(),
      "browser_cdp_permissions", "full_debug", {
        selectable: true, runtime_available: true,
        blocked_by: [], remediation: [], restart_required: false,
      });
    render(<QueryClientProvider client={new QueryClient()}>
      <BrowserCDPPermissionPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          browserCDPPermissionControlEnabled: true,
          fullCDPDebugEnabled: true,
        })}
        detail={debugDetail}
        readiness={readiness}
      />
    </QueryClientProvider>);

    await user.click(screen.getByText("高级浏览器调试"));
    await user.click(screen.getByRole("button", { name: /完整 CDP/ }));
    expect(fetchMock).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/runs/run-1/browser-cdp-permission");
    expect(JSON.parse(String(init.body))).toEqual({
      mode: "full_debug",
      reason: "settings browser CDP permission selection",
      confirm_full_cdp_debug: true,
    });
  });
});
