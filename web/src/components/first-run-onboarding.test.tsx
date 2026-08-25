import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { CyberAgentClient } from "../api/client";
import type { StandardCodePresetControlView } from "../api/types";
import { FirstRunOnboarding } from "./first-run-onboarding";

const desktopBridge = vi.hoisted(() => ({
  importWorkspace: vi.fn(),
}));

vi.mock("../lib/desktop-bridge", () => ({
  desktopWorkspaceImportEnabled: () => true,
  importDesktopWorkspace: desktopBridge.importWorkspace,
}));

vi.mock("./model-availability-dialog", () => ({
  ModelAvailabilityWorkspace: () => <div>Provider model controls</div>,
}));

function standardCodeResult(overrides: Partial<StandardCodePresetControlView> = {}):
  StandardCodePresetControlView {
  return {
    protocol_version: "standard_code_preset.v1", status: "blocked",
    workspace_id: "workspace-1", action: "configure", backend_intent: "auto",
    selected_backend: "local", selection_reason: "auto_local_ready",
    local_readiness: { backend: "local", available: true, blocked_by: [], remediation: [] },
    docker_readiness: { backend: "docker", available: false,
      blocked_by: ["docker_unavailable"], remediation: ["install_or_start_docker"] },
    blocked_by: ["workspace_untrusted"], next_steps: ["confirm_workspace_trust"],
    trust_required: true, trust_digest: "a".repeat(64), drydock_ready: false,
    network: "disabled", credentials: "none", replayed: false, capability_grant: false,
    ...overrides,
  };
}

function testClient(createStandardCode = vi.fn()) {
  return {
    modelAvailability: vi.fn().mockResolvedValue({
      providers: [{ name: "openai", status: "available" }],
      routes: [{ name: "code", available: true, harness_ready: true }],
    }),
    createStandardCode,
  } as unknown as CyberAgentClient;
}

function renderOnboarding(client: CyberAgentClient, onComplete = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  render(<QueryClientProvider client={queryClient}>
    <FirstRunOnboarding client={client} open onComplete={onComplete} onDismiss={vi.fn()} />
  </QueryClientProvider>);
  return onComplete;
}

async function reachWorkspace() {
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByText("Provider model controls");
  await waitFor(() => expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByRole("heading", { name: "Choose a Workspace and describe the goal" });
}

describe("FirstRunOnboarding", () => {
  beforeEach(() => {
    desktopBridge.importWorkspace.mockReset().mockResolvedValue({
      id: "workspace-1", name: "project", created_at: "2026-08-25T00:00:00Z",
    });
  });

  it("requires an explicit folder choice and exact trust confirmation before creating a Run", async () => {
    const createStandardCode = vi.fn()
      .mockResolvedValueOnce(standardCodeResult())
      .mockResolvedValueOnce(standardCodeResult({
        status: "configured", run_id: "run-standard-code", trust_required: false,
        trust_digest: undefined, blocked_by: [], next_steps: [], drydock_ready: true,
      }));
    const onComplete = renderOnboarding(testClient(createStandardCode));

    expect(desktopBridge.importWorkspace).not.toHaveBeenCalled();
    await reachWorkspace();
    expect(desktopBridge.importWorkspace).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Choose working folder" }));
    await screen.findByText("project");
    fireEvent.change(screen.getByLabelText("Coding goal"), {
      target: { value: "Implement the parser" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Check and continue" }));

    await screen.findByRole("heading", { name: "Confirm Workspace trust" });
    expect(screen.getByText("a".repeat(64))).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm and create" })).toBeDisabled();
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "Confirm and create" }));

    await screen.findByRole("heading", { name: "Standard Code is ready" });
    expect(createStandardCode).toHaveBeenCalledTimes(2);
    expect(createStandardCode.mock.calls[0]?.[0]).toMatchObject({
      workspace_id: "workspace-1", goal: "Implement the parser",
      backend_intent: "auto", confirm_workspace_trust: false,
    });
    expect(createStandardCode.mock.calls[1]?.[0]).toMatchObject({
      backend_intent: "auto", confirm_workspace_trust: true,
      expected_trust_digest: "a".repeat(64),
    });
    fireEvent.click(screen.getByRole("button", { name: "Start coding" }));
    expect(onComplete).toHaveBeenCalledWith("run-standard-code");
  });

  it("shows Go-owned blocker remediation and permits an explicit Docker fallback", async () => {
    const blocked = standardCodeResult({
      selected_backend: undefined, selection_reason: undefined,
      local_readiness: { backend: "local", available: false,
        blocked_by: ["sandbox_unproven"], remediation: ["verify_sandbox"] },
      docker_readiness: { backend: "docker", available: true, blocked_by: [], remediation: [] },
      blocked_by: ["sandbox_unproven"], next_steps: ["select_docker"],
      trust_required: false, trust_digest: undefined,
    });
    const dockerTrust = standardCodeResult({
      backend_intent: "docker", selected_backend: "docker",
      selection_reason: "explicit_docker",
    });
    const createStandardCode = vi.fn()
      .mockResolvedValueOnce(blocked).mockResolvedValueOnce(dockerTrust);
    renderOnboarding(testClient(createStandardCode));
    await reachWorkspace();
    fireEvent.click(screen.getByRole("button", { name: "Choose working folder" }));
    await screen.findByText("project");
    fireEvent.change(screen.getByLabelText("Coding goal"), {
      target: { value: "Implement the parser" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Check and continue" }));

    await screen.findByText("Local sandbox is not verified");
    expect(screen.getByText("Repair and verify the local sandbox")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Use Docker" }));
    await screen.findByRole("heading", { name: "Confirm Workspace trust" });
    expect(createStandardCode.mock.calls[1]?.[0]).toMatchObject({
      backend_intent: "docker", confirm_workspace_trust: false,
    });
  });

  it("uses a fresh intent for an explicit backend readiness retry", async () => {
    const blocked = standardCodeResult({
      selected_backend: undefined, selection_reason: undefined,
      local_readiness: { backend: "local", available: false,
        blocked_by: ["backend_not_ready"], remediation: ["retry_backend_readiness"] },
      blocked_by: ["backend_not_ready"], next_steps: ["retry_readiness"],
      trust_required: false, trust_digest: undefined,
    });
    const createStandardCode = vi.fn().mockResolvedValue(blocked);
    renderOnboarding(testClient(createStandardCode));
    await reachWorkspace();
    fireEvent.click(screen.getByRole("button", { name: "Choose working folder" }));
    await screen.findByText("project");
    fireEvent.change(screen.getByLabelText("Coding goal"), {
      target: { value: "Implement the parser" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Check and continue" }));

    await screen.findByText("Execution backend is not ready");
    fireEvent.click(screen.getByRole("button", { name: "Retry check" }));
    await waitFor(() => expect(createStandardCode).toHaveBeenCalledTimes(2));
    expect(createStandardCode.mock.calls[0]?.[0]).toEqual(createStandardCode.mock.calls[1]?.[0]);
    expect(createStandardCode.mock.calls[0]?.[1]).not.toBe(createStandardCode.mock.calls[1]?.[1]);
  });

  it("keeps Chinese IME composition in the bounded goal without submitting early", async () => {
    const createStandardCode = vi.fn().mockResolvedValue(standardCodeResult());
    renderOnboarding(testClient(createStandardCode));
    await reachWorkspace();
    fireEvent.click(screen.getByRole("button", { name: "Choose working folder" }));
    await screen.findByText("project");

    const goal = screen.getByLabelText("Coding goal");
    fireEvent.compositionStart(goal);
    fireEvent.change(goal, { target: { value: "修复" } });
    expect(createStandardCode).not.toHaveBeenCalled();
    fireEvent.compositionEnd(goal, { data: "修复登录页面" });
    fireEvent.change(goal, { target: { value: "修复登录页面" } });

    expect(goal).toHaveValue("修复登录页面");
    expect(screen.getByRole("button", { name: "Check and continue" })).toBeEnabled();
    expect(createStandardCode).not.toHaveBeenCalled();
  });
});
