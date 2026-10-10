import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { APIClient } from "../api/client";
import type { RunDetailView } from "../api/types";
import { capabilityReadinessFixture, patchCapabilityReadiness } from "../test/capability-readiness";
import { DockerSandboxPanel } from "./docker-sandbox-panel";

function renderPanel(client: APIClient) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } })}><DockerSandboxPanel client={client} /></QueryClientProvider>);
}

it("allows inspecting an existing admission through a read-only connection", async () => {
  const user = userEvent.setup();
  const getDockerSandboxStatus = vi.fn().mockResolvedValue({ state: "running", decision: "allowed" });
  const startDockerSandbox = vi.fn();
  renderPanel({ hasControl: false, getDockerSandboxStatus, startDockerSandbox } as unknown as APIClient);

  await user.click(screen.getByText("Advanced: exact sandbox manifests and historical admissions"));
  await user.type(screen.getByLabelText("Admission ID"), "admission-1");
  await screen.findByText("allowed");
  await user.click(screen.getByRole("button", { name: "Refresh sandbox state" }));
  await waitFor(() => expect(getDockerSandboxStatus).toHaveBeenLastCalledWith("admission-1", expect.any(AbortSignal)));
  expect(screen.queryByRole("button", { name: "Start" })).not.toBeInTheDocument();
  expect(startDockerSandbox).not.toHaveBeenCalled();
});

it("refreshes the original admission after a lost start response without starting again", async () => {
  const user = userEvent.setup();
  const getDockerSandboxStatus = vi.fn().mockResolvedValue({ state: "running", decision: "allowed" });
  const startDockerSandbox = vi.fn().mockRejectedValue(new Error("Start response lost"));
  renderPanel({ hasControl: true, getDockerSandboxStatus, startDockerSandbox } as unknown as APIClient);

  await user.click(screen.getByText("Advanced: exact sandbox manifests and historical admissions"));
  await user.type(screen.getByLabelText("Admission ID"), "admission-1");
  await screen.findByText("allowed");
  await user.click(screen.getByRole("button", { name: "Start" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Start response lost");
  const beforeRefresh = getDockerSandboxStatus.mock.calls.length;
  await user.click(screen.getByRole("button", { name: "Refresh sandbox state" }));
  await waitFor(() => expect(getDockerSandboxStatus).toHaveBeenCalledTimes(beforeRefresh + 1));
  expect(getDockerSandboxStatus).toHaveBeenLastCalledWith("admission-1", expect.any(AbortSignal));
  expect(startDockerSandbox).toHaveBeenCalledTimes(1);
});

it("reads environment state without repeating a write and gives daemon-specific next steps", async () => {
  const user = userEvent.setup();
  const getDockerEnvironment = vi.fn().mockResolvedValue({ protocol_version: "docker_environment.v1", feature_enabled: true,
    image_configured: true, image_digest: `sha256:${"a".repeat(64)}`, restart_required: true,
    readiness: { ready: false, daemon_reachable: false, image_profile_safe: false, reason_code: "daemon_unreachable" } });
  const admitDockerSandbox = vi.fn();
  renderPanel({ hasControl: false, getDockerEnvironment, admitDockerSandbox } as unknown as APIClient);
  expect(await screen.findByText(/Start Docker Engine/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Refresh environment" }));
  await waitFor(() => expect(getDockerEnvironment).toHaveBeenCalledTimes(2));
  expect(admitDockerSandbox).not.toHaveBeenCalled();
  expect(screen.getByText("Advanced: exact sandbox manifests and historical admissions").closest("details")).not.toHaveAttribute("open");
});

it("prepares the current project through Docker preset intent and exact trust confirmation", async () => {
  const user = userEvent.setup();
  const detail = { run: { id: "run-1", status: "paused" }, mode: { phase: "deliver" } } as RunDetailView;
  const readiness = patchCapabilityReadiness(capabilityReadinessFixture(), "presets", "standard_code", {
    selectable: true, runtime_available: true, blocked_by: [], remediation: [], restart_required: false });
  const getDockerEnvironment = vi.fn().mockResolvedValue({ protocol_version: "docker_environment.v1", feature_enabled: true,
    image_configured: true, image_digest: `sha256:${"a".repeat(64)}`, restart_required: false,
    readiness: { ready: true, daemon_reachable: true, image_profile_safe: true } });
  const configureStandardCode = vi.fn().mockResolvedValueOnce({ status: "blocked", run_id: "run-1", action: "configure", backend_intent: "docker",
    trust_required: true, trust_digest: "a".repeat(64), next_steps: ["confirm_workspace_trust"], docker_readiness: { available: true } })
    .mockResolvedValueOnce({ status: "configured", run_id: "run-1", next_steps: [], docker_readiness: { available: true } });
  const client = { hasControl: true, hasStandardCodePreset: true, getDockerEnvironment, configureStandardCode,
    get: vi.fn().mockResolvedValue(detail), runCapabilityReadiness: vi.fn().mockResolvedValue(readiness) } as unknown as APIClient;
  render(<QueryClientProvider client={new QueryClient()}><DockerSandboxPanel client={client} runID="run-1" threadID="thread-1" /></QueryClientProvider>);
  await user.click(await screen.findByRole("button", { name: /Check and configure Docker/ }));
  await waitFor(() => expect(configureStandardCode).toHaveBeenCalledTimes(1));
  expect(configureStandardCode.mock.calls[0]![2]).toMatchObject({ backend_intent: "docker", confirm_workspace_trust: false });
  expect(await screen.findByText(/Confirm the reviewed Workspace source digest/)).toHaveTextContent("a".repeat(64));
  await user.click(screen.getByRole("button", { name: "Confirm" }));
  await waitFor(() => expect(configureStandardCode).toHaveBeenCalledTimes(2));
  expect(configureStandardCode.mock.calls[1]![2]).toMatchObject({ backend_intent: "docker", confirm_workspace_trust: true, expected_trust_digest: "a".repeat(64) });
  expect(configureStandardCode.mock.calls[1]![0]).toBe("run-1");
});
