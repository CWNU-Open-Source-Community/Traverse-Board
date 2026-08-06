import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CyberAgentClient } from "../api/client";
import type { RunDetailView, RunExecutionProfileView } from "../api/types";
import { ExecutionProfilePanel } from "./run-permission-settings";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

function detail(profile: RunExecutionProfileView["profile"] = "preview"): RunDetailView {
  return {
    run: { id: "run-1", mission_id: "mission-1", session_id: "session-1", status: "paused",
      config: { model_route: "mock/model", interactive: true },
      budget: { max_turns: 2, max_tokens: 0, max_tool_calls: 10, max_cost_usd: 0, timeout_seconds: 0 },
      created_at: "2026-07-17T00:00:00Z", updated_at: "2026-07-17T00:00:00Z" },
    mission: { id: "mission-1", goal: "test execution profile", profile: "code", workspace_id: "workspace-1",
      scope: { workspace_id: "workspace-1", network_mode: "disabled", allowed_targets: [] },
      created_at: "2026-07-17T00:00:00Z", updated_at: "2026-07-17T00:00:00Z" },
    mode: { protocol_version: "run_mode.v1", revision: 1, surface: "code", phase: "deliver",
      profile: "code", scope: { workspace_id: "workspace-1", network_mode: "disabled", allowed_targets: [] },
      policy_version: "mode_policy.v1", requested_by: "test", reason: "test",
      created_at: "2026-07-17T00:00:00Z", capability_grant: false },
    execution_profile: {
      protocol_version: "run_execution_profile.v1", revision: 1, profile,
      backend: profile === "preview" ? "noop" : profile,
      approval_policy: profile === "preview" ? "none" : "always",
      filesystem_scope: profile === "preview" ? "none" : "workspace",
      network_scope: "disabled", risk_tier: profile === "preview" ? "minimal" : "elevated",
      required_gate: profile === "preview" ? "none" : "docker_production_start_gate",
      policy_version: "execution_profile_policy.v1", created_at: "2026-07-17T00:00:00Z",
      process_enabled: false,
      execution_authorized: false, capability_grant: false,
    },
    execution_permission: {
      protocol_version: "run_execution_permission.v1", revision: 1,
      mode: "conservative", approval_policy: "fixed_templates",
      command_scope: "fixed_templates", filesystem_scope: "workspace_guarded",
      network_scope: "disabled", persistent_terminal: false,
      background_process: false, agent_terminal_input: false, risk_tier: "minimal",
      required_gate: "conservative_control",
      policy_version: "execution_permission_policy.v1", operator_confirmed: false,
      runtime_gate_available: true,
      runtime: { operator_approval_enabled: false, danger_full_access_enabled: false,
        debug_maximum_access_enabled: false },
      created_at: "2026-07-17T00:00:00Z", process_enabled: false,
      execution_authorized: false, capability_grant: false,
    },
    operator_steering: { pending: 0, prepared: 0, committed: 0, cancelled: 0, messages: [] },
    tool_usage: { consumed: 0, limit: 10, remaining: 10 },
  } as unknown as RunDetailView;
}

describe("ExecutionProfilePanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("keeps selection read-only without a distinct control token", () => {
    render(<QueryClientProvider client={new QueryClient()}>
      <ExecutionProfilePanel client={new CyberAgentClient("read-token")} detail={detail()} />
    </QueryClientProvider>);
    expect(screen.getByText("只读连接")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Docker/ })).toBeDisabled();
  });

  it("submits one control request and adopts the returned non-authorizing profile", async () => {
    const selected = detail("docker").execution_profile;
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-profile",
      data: { execution_profile: selected, replayed: false },
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const queryClient = new QueryClient();
    queryClient.setQueryData(["run", "run-1"], detail());
    const user = userEvent.setup();
    render(<QueryClientProvider client={queryClient}>
      <ExecutionProfilePanel
        client={new CyberAgentClient("read-token", "/api/v1", "control-token", {
          runControlEnabled: true,
        })}
        detail={detail()}
      />
    </QueryClientProvider>);

    await user.click(screen.getByRole("button", { name: /Docker/ }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/runs/run-1/execution-profile");
    expect(init.headers).toMatchObject({ Authorization: "Bearer control-token" });
    expect(queryClient.getQueryData<RunDetailView>(["run", "run-1"])?.execution_profile.profile)
      .toBe("docker");
  });
});
