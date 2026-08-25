import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CyberAgentClient } from "../api/client";
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
      mode: "conservative", approval_policy: "fixed_templates",
      command_scope: "fixed_templates", filesystem_scope: "workspace_guarded",
      network_scope: "disabled", persistent_terminal: false, background_process: false,
      agent_terminal_input: false, risk_tier: "minimal",
      required_gate: "conservative_control",
      policy_version: "execution_permission_policy.v1", operator_confirmed: false,
      runtime_gate_available: true,
      runtime: { workspace_sandbox_enabled: false,
        operator_approval_enabled: true, danger_full_access_enabled: true,
        debug_maximum_access_enabled: false },
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

describe("ExecutionPermissionPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("disables modes whose process-local startup gate is unavailable", () => {
    render(<QueryClientProvider client={new QueryClient()}>
      <ExecutionPermissionPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          executionPermissionControlEnabled: true,
        })}
        detail={detail()}
        readiness={capabilityReadinessFixture()}
      />
    </QueryClientProvider>);
    expect(screen.getByRole("button", { name: /调试/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /工作区执行/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /完全访问/ })).toBeEnabled();
  });

  it("confirms Workspace Access without implying a host fallback", async () => {
    const available = {
      ...detail(),
      execution_permission: {
        ...detail().execution_permission,
        runtime: {
          ...detail().execution_permission.runtime,
          workspace_sandbox_enabled: true,
        },
      },
    } as RunDetailView;
    const selected = {
      ...available.execution_permission,
      mode: "workspace_access" as const,
      approval_policy: "out_of_scope_exact_once" as const,
      command_scope: "sandboxed_workspace" as const,
      required_gate: "workspace_sandbox_adapter" as const,
      operator_confirmed: true,
      revision: 2,
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-workspace-access",
      data: { execution_permission: selected, replayed: false },
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    const readiness = patchCapabilityReadiness(capabilityReadinessFixture(),
      "permissions", "workspace_access", {
        selectable: true, runtime_available: false,
        blocked_by: ["sandbox_unproven"], remediation: ["verify_sandbox"],
        restart_required: false,
      });
    render(<QueryClientProvider client={new QueryClient()}>
      <ExecutionPermissionPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          executionPermissionControlEnabled: true,
        })}
        detail={available}
        readiness={readiness}
      />
    </QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: /工作区执行/ }));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByText(/宿主进程、持久终端和完整 CDP 均被拒绝/))
      .toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      mode: "workspace_access",
      reason: "settings execution permission selection",
      confirm_workspace_access: true,
    });
  });

  it("requires an inline confirmation before selecting full access", async () => {
    const selected = {
      ...detail().execution_permission, mode: "full_access" as const,
      approval_policy: "none" as const, command_scope: "arbitrary_stateless" as const,
      filesystem_scope: "host_full" as const, network_scope: "host" as const,
      risk_tier: "high" as const, required_gate: "danger_full_access" as const,
      operator_confirmed: true, revision: 2,
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-permission",
      data: { execution_permission: selected, replayed: false },
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
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
    await user.click(screen.getByRole("button", { name: /完全访问/ }));
    expect(fetchMock).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toMatchObject({
      mode: "full_access", confirm_danger_full_access: true,
    });
  });

  it("drops an elevated confirmation when the selected Run changes", async () => {
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

    await user.click(screen.getByRole("button", { name: /完全访问/ }));
    expect(screen.getByRole("button", { name: "确认" })).toBeInTheDocument();

    view.rerender(<QueryClientProvider client={queryClient}>
      <RunPermissionSettings client={client} runID="run-2" />
    </QueryClientProvider>);
    expect(screen.queryByRole("button", { name: "确认" })).not.toBeInTheDocument();
    expect(screen.getByText((_, element) =>
      element?.tagName === "SPAN" && element.textContent?.startsWith("run-2") === true,
    )).toBeInTheDocument();
  });
});

describe("StandardCodeReadinessPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

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
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-standard-code", data: blocked,
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <StandardCodeReadinessPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          runControlEnabled: true, standardCodePresetEnabled: true,
        })}
        detail={detail()}
        readiness={capabilityReadinessFixture()} />
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
        readiness={capabilityReadinessFixture()} />
    </QueryClientProvider>);

    await user.click(screen.getByRole("button", { name: /开始编码/ }));
    expect(await screen.findByText(/已创建新的 Code Run/)).toHaveTextContent("run-new-");
    expect(queryClient.getQueryData<RunDetailView>(["run", "run-1"])?.run.id)
      .toBe("run-1");
  });
});

describe("BrowserCDPPermissionPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows the sensitive warning and keeps full CDP disabled outside Debug", () => {
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
        mode: "debug" as const,
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
