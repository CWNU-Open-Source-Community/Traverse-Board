import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import type { CyberAgentClient } from "../api/client";
import type { RunDetailView, RunView, SessionDetailView,
  SessionMessageControlView } from "../api/types";
import { LocaleProvider } from "../lib/locale";
import { SessionWorkspace } from "./session-workspace";

const session = {
  id: "sess-1", workspace_id: "workspace-1", title: "Audit repository", route: "code",
  status: "active", created_at: "2026-08-12T00:00:00Z", updated_at: "2026-08-12T00:00:00Z",
} as const;

describe("SessionWorkspace", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("prayu.locale.v1", "en-US");
    sessionStorage.clear();
  });

  it("uses the fresh completed Run for a message-only client", async () => {
    const client = workspaceClient(run("paused"), run("completed"));

    renderWorkspace(client);

    await waitFor(() => expect(client.get).toHaveBeenCalledWith("/runs/run-1", {},
      expect.any(AbortSignal)));
    expect(await screen.findByText("completed")).toBeInTheDocument();
    expect(screen.getByRole("note")).toHaveTextContent("Session is the current Run's local context and authority boundary");
    expect(screen.getByLabelText("Run-local Session message")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Queue message" })).toBeDisabled();
    expect(client.controlRunLifecycle).not.toHaveBeenCalled();
    expect(client.executeRun).not.toHaveBeenCalled();
  });

  it("uses the fresh running Run when submitting a message", async () => {
    const client = workspaceClient(run("created"), run("running"));
    const user = userEvent.setup();

    renderWorkspace(client);

    const composer = await screen.findByLabelText("Run-local Session message");
    await waitFor(() => expect(composer).toBeEnabled());
    await user.type(composer, "Inspect the latest changes");
    await user.click(screen.getByRole("button", { name: "Queue message" }));

    await waitFor(() => expect(client.submitSessionMessage).toHaveBeenCalledWith("sess-1", {
      version: "session_message_submission.v1", content: "Inspect the latest changes",
    }, expect.stringMatching(/^web-session-message-/), expect.any(AbortSignal)));
    expect(client.controlRunLifecycle).not.toHaveBeenCalled();
    expect(client.executeRun).not.toHaveBeenCalled();
  });
});

function workspaceClient(staleRun: RunView, freshRun: RunView) {
  const submission = {
    version: "session_message_submission.v1", run_id: "run-1", session_id: "sess-1",
    steering: { id: "steer-1", sequence: 1, status: "pending", prepared: false,
      created_at: "2026-08-12T00:00:00Z" }, replayed: false, execution_started: false,
    model_called: false, tool_called: false, capability_grant: false,
  } as SessionMessageControlView;
  return {
    hasSessionMessages: true,
    hasSessionSteeringControl: false,
    hasPlanDelivery: false,
    hasRunLifecycle: false,
    hasRunExecution: false,
    hasEvidenceAttachment: false,
    get: vi.fn((path: string) => Promise.resolve(path === "/sessions/sess-1"
      ? { session, run: staleRun } as SessionDetailView
      : { run: freshRun, mode: { phase: "deliver" } } as RunDetailView)),
    getPage: vi.fn().mockResolvedValue({
      items: [], page: { limit: 100 }, requestID: "request-messages",
    }),
    submitSessionMessage: vi.fn().mockResolvedValue(submission),
    controlRunLifecycle: vi.fn(),
    executeRun: vi.fn(),
  } as unknown as CyberAgentClient & {
    get: ReturnType<typeof vi.fn>;
    submitSessionMessage: ReturnType<typeof vi.fn>;
    controlRunLifecycle: ReturnType<typeof vi.fn>;
    executeRun: ReturnType<typeof vi.fn>;
  };
}

function run(status: RunView["status"]): RunView {
  return {
    id: "run-1", mission_id: "mission-1", session_id: "sess-1", status,
    config: { model_route: "code", interactive: true }, budget: { max_turns: 10, max_tool_calls: 20 },
    created_at: "2026-08-12T00:00:00Z", updated_at: "2026-08-12T00:00:00Z",
  };
}

function renderWorkspace(client: CyberAgentClient) {
  return render(withProvider(<SessionWorkspace client={client} sessionID="sess-1" />));
}

function withProvider(node: ReactNode) {
  return <LocaleProvider><QueryClientProvider client={new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })}>{node}</QueryClientProvider></LocaleProvider>;
}
