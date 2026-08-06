import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CyberAgentClient } from "../api/client";
import type { RunDetailView } from "../api/types";
import { ExecutionInteractionPanel } from "./run-permission-settings";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

function detail(): RunDetailView {
  return {
    run: { id: "run-1", mission_id: "mission-1", session_id: "session-1",
      status: "paused", config: { model_route: "mock/model", interactive: true },
      budget: { max_turns: 2, max_tokens: 0, max_tool_calls: 10, max_cost_usd: 0,
        timeout_seconds: 0 }, created_at: "2026-07-28T00:00:00Z",
      updated_at: "2026-07-28T00:00:00Z" },
    mission: { id: "mission-1", goal: "test interaction", profile: "code",
      workspace_id: "workspace-1", scope: { workspace_id: "workspace-1",
        network_mode: "disabled", allowed_targets: [] },
      created_at: "2026-07-28T00:00:00Z", updated_at: "2026-07-28T00:00:00Z" },
    mode: { protocol_version: "run_mode.v1", revision: 1, surface: "code",
      phase: "deliver", profile: "code", scope: { workspace_id: "workspace-1",
        network_mode: "disabled", allowed_targets: [] }, policy_version: "mode_policy.v1",
      requested_by: "test", reason: "test", created_at: "2026-07-28T00:00:00Z",
      capability_grant: false },
    execution_profile: { protocol_version: "run_execution_profile.v1", revision: 2,
      profile: "local", backend: "local", approval_policy: "always",
      filesystem_scope: "workspace", network_scope: "disabled", risk_tier: "high",
      required_gate: "local_os_sandbox_gate", policy_version: "execution_profile_policy.v1",
      created_at: "2026-07-28T00:00:00Z", process_enabled: false,
      execution_authorized: false, capability_grant: false },
    execution_interaction: { protocol_version: "run_execution_interaction.v1",
      revision: 1, mode: "preview", surface: "code", execution_profile: "local",
      execution_profile_revision: 2, workspace_trust: "untrusted", command_form: "none",
      persistent_terminal: false, user_input_available: false, agent_input_default: false,
      network_scope: "disabled", required_gate: "none",
      policy_version: "execution_interaction_policy.v1", operator_confirmed: false,
      created_at: "2026-07-28T00:00:00Z", process_enabled: false,
      execution_authorized: false, capability_grant: false },
    operator_steering: { pending: 0, prepared: 0, committed: 0, cancelled: 0,
      messages: [] },
    tool_usage: { consumed: 0, limit: 10, remaining: 10 },
  } as unknown as RunDetailView;
}

describe("ExecutionInteractionPanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("disables Cyber when the Run is not on the cyber/docker boundary", () => {
    render(<QueryClientProvider client={new QueryClient()}>
      <ExecutionInteractionPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          runControlEnabled: true,
        })}
        detail={detail()}
      />
    </QueryClientProvider>);
    expect(screen.getByRole("button", { name: /Cyber/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /Code/ })).toBeEnabled();
  });

  it("requires trust confirmation before selecting controlled Code mode", async () => {
    const selected = {
      ...detail().execution_interaction, mode: "controlled" as const,
      workspace_trust: "trusted" as const, command_form: "structured_argv" as const,
      required_gate: "local_os_sandbox_gate" as const, operator_confirmed: true,
      revision: 2,
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: "api.v1", request_id: "req-interaction",
      data: { execution_interaction: selected, replayed: false },
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <ExecutionInteractionPanel
        client={new CyberAgentClient("read", "/api/v1", "control", {
          runControlEnabled: true,
        })}
        detail={detail()}
      />
    </QueryClientProvider>);

    await user.click(screen.getByRole("button", { name: /Code/ }));
    expect(fetchMock).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "确认" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toMatchObject({
      mode: "controlled", trust: "trusted", confirm_workspace_trust: true,
    });
  });
});
