import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import type { RunCreationControlView } from "../api/types";
import { useConnectionStore } from "../state/connection";
import * as desktopBridge from "../lib/desktop-bridge";
import { RunCreationDialog } from "./run-creation-dialog";

const created = {
  mission: { id: "mission-created", goal: "Create parser", profile: "code",
    workspace_id: "workspace-1", scope: { workspace_id: "workspace-1", network_mode: "disabled" },
    created_at: "2026-07-18T00:00:00Z", updated_at: "2026-07-18T00:00:00Z" },
  run: { id: "run-created", mission_id: "mission-created", session_id: "sess-created",
    status: "created", config: { model_route: "code", interactive: true },
    budget: { max_turns: 100, max_tool_calls: 100 },
    created_at: "2026-07-18T00:00:00Z", updated_at: "2026-07-18T00:00:00Z" },
  session: { id: "sess-created", workspace_id: "workspace-1", title: "Create parser",
    route: "code", status: "active", created_at: "2026-07-18T00:00:00Z",
    updated_at: "2026-07-18T00:00:00Z" },
  mode: { protocol_version: "run_mode.v1", revision: 1, surface: "code", phase: "deliver",
    profile: "code", scope: { workspace_id: "workspace-1", network_mode: "disabled" },
    policy_version: "mode_policy.v1", requested_by: "http_control", reason: "initial Run mode",
    created_at: "2026-07-18T00:00:00Z", capability_grant: false },
  replayed: false,
} as RunCreationControlView;

describe("RunCreationDialog", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    useConnectionStore.getState().disconnect();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("imports a native folder before creating a desktop Run", async () => {
    vi.spyOn(desktopBridge, "desktopWorkspaceImportEnabled").mockReturnValue(true);
    const importWorkspace = vi.spyOn(desktopBridge, "importDesktopWorkspace")
      .mockResolvedValue({ id: "ws-import-selected", name: "selected-project",
        created_at: "2026-08-13T01:02:03Z" });
    const createRun = vi.fn().mockResolvedValue(created);
    const getPage = vi.fn();
    const client = { getPage, createRun } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <RunCreationDialog client={client} open onClose={vi.fn()} />
    </QueryClientProvider>);

    await waitFor(() => expect(importWorkspace).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("selected-project")).toBeInTheDocument();
    expect(getPage).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText("Goal"), "Create parser");
    await user.click(screen.getByRole("button", { name: "Create Run" }));
    await waitFor(() => expect(createRun).toHaveBeenCalledTimes(1));
    expect(createRun.mock.calls[0]?.[0]).toMatchObject({
      workspace_id: "ws-import-selected",
      goal: "Create parser",
    });
  });

  it("reuses one in-memory idempotency key for an identical retry", async () => {
    vi.stubGlobal("crypto", { randomUUID: vi.fn().mockReturnValue("00000000-0000-4000-8000-000000000001") });
    const createRun = vi.fn()
      .mockRejectedValueOnce(new Error("connection interrupted"))
      .mockResolvedValueOnce(created);
    const client = {
      getPage: vi.fn().mockResolvedValue({ items: [{ id: "workspace-1", name: "Project",
        created_at: "2026-07-18T00:00:00Z" }], page: { limit: 100 }, requestID: "req-workspaces" }),
      createRun,
    } as unknown as CyberAgentClient;
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <RunCreationDialog client={client} open onClose={onClose} />
    </QueryClientProvider>);

    await waitFor(() => expect(screen.getByLabelText("Workspace")).toHaveValue("workspace-1"));
    await user.type(screen.getByLabelText("Goal"), "Create parser");
    await user.click(screen.getByRole("button", { name: "Create Run" }));
    await screen.findByText("connection interrupted");
    await user.click(screen.getByRole("button", { name: "Create Run" }));
    await waitFor(() => expect(createRun).toHaveBeenCalledTimes(2));

    const firstKey = createRun.mock.calls[0]?.[1];
    const secondKey = createRun.mock.calls[1]?.[1];
    expect(firstKey).toBe("web-run-create-00000000-0000-4000-8000-000000000001");
    expect(secondKey).toBe(firstKey);
    expect(useConnectionStore.getState().selectedRunID).toBe("run-created");
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it("blocks a multibyte goal above the Go UTF-8 byte limit", async () => {
    const createRun = vi.fn();
    const client = {
      getPage: vi.fn().mockResolvedValue({ items: [{ id: "workspace-1", name: "Project",
        created_at: "2026-07-18T00:00:00Z" }], page: { limit: 100 }, requestID: "req-workspaces" }),
      createRun,
    } as unknown as CyberAgentClient;
    render(<QueryClientProvider client={new QueryClient()}>
      <RunCreationDialog client={client} open onClose={vi.fn()} />
    </QueryClientProvider>);

    await waitFor(() => expect(screen.getByLabelText("Workspace")).toHaveValue("workspace-1"));
    fireEvent.change(screen.getByLabelText("Goal"), { target: { value: "界".repeat(1366) } });
    expect(screen.getByText("Goal exceeds 4096 UTF-8 bytes")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create Run" })).toBeDisabled();
    expect(createRun).not.toHaveBeenCalled();
  });

  it("prefills a task drafted in the workbench composer", async () => {
    const createRun = vi.fn().mockResolvedValue(created);
    const client = {
      getPage: vi.fn().mockResolvedValue({ items: [{ id: "workspace-1", name: "Project",
        created_at: "2026-07-18T00:00:00Z" }], page: { limit: 100 }, requestID: "req-workspaces" }),
      createRun,
    } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    render(<QueryClientProvider client={new QueryClient()}>
      <RunCreationDialog client={client} initialGoal="Audit the parser" initialPhase="plan"
        open onClose={vi.fn()} />
    </QueryClientProvider>);

    await waitFor(() => expect(screen.getByLabelText("Goal")).toHaveValue("Audit the parser"));
    expect(screen.queryByText("Phase")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create Run" }));
    await waitFor(() => expect(createRun).toHaveBeenCalledTimes(1));
    expect(createRun.mock.calls[0]?.[0]).toMatchObject({ phase: "plan" });
  });
});
