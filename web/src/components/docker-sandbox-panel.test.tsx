import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { APIClient } from "../api/client";
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
