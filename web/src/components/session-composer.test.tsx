import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import type { CyberAgentClient } from "../api/client";
import type { OperatorSteeringQueueView, RunView, SessionMessageControlView,
  SessionSteeringCancellationView, RunExecutionControlView,
  RunLifecycleControlView, PublicModelStreamSnapshot } from "../api/types";
import { SessionComposer, SessionSteeringQueue } from "./session-composer";
import { LocaleProvider } from "../lib/locale";

const result: SessionMessageControlView = {
  version: "session_message_submission.v1",
  run_id: "run-1",
  session_id: "sess-1",
  steering: {
    id: "steer-1",
    sequence: 3,
    status: "pending",
    prepared: false,
    created_at: "2026-07-18T00:00:00Z",
  },
  replayed: false,
  execution_started: false,
  model_called: false,
  tool_called: false,
  capability_grant: false,
};

const runningRun = { id: "run-1", status: "running" } as RunView;

const cancellationResult: SessionSteeringCancellationView = {
  version: "session_steering_cancellation.v1",
  run_id: "run-1", session_id: "sess-1", cancellation_id: "cancel-1",
  cancellation_kind: "operator", replayed: false,
  steering: {
    id: "steer-1", sequence: 3, status: "cancelled", prepared: false,
    created_at: "2026-07-18T00:00:00Z", cancelled_at: "2026-07-18T00:01:00Z",
  },
  execution_started: false, model_called: false, tool_called: false, capability_grant: false,
};

describe("SessionComposer", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("prayu.locale.v1", "en-US");
    sessionStorage.clear();
  });

  it("reuses an in-memory operation key after uncertain failure and clears on success", async () => {
    const submitSessionMessage = vi.fn()
      .mockRejectedValueOnce(new Error("response unavailable"))
      .mockResolvedValueOnce(result);
    const client = {
      hasSessionMessages: true,
      hasRunLifecycle: false,
      hasRunExecution: false,
      submitSessionMessage,
    } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderComposer(client, runningRun);

    await user.type(screen.getByLabelText("Session message"), "Review the latest diff");
    await user.click(screen.getByRole("button", { name: "Queue message" }));
    await screen.findByText("response unavailable");
    await user.click(screen.getByRole("button", { name: "Queue message" }));
    await screen.findByText("Queued #3");

    expect(submitSessionMessage).toHaveBeenCalledTimes(2);
    const first = submitSessionMessage.mock.calls[0];
    const second = submitSessionMessage.mock.calls[1];
    expect(first?.[0]).toBe("sess-1");
    expect(first?.[1]).toEqual({
      version: "session_message_submission.v1", content: "Review the latest diff",
    });
    expect(first?.[2]).toBe(second?.[2]);
    expect(screen.getByLabelText("Session message")).toHaveValue("");
    expect(Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index)))
      .toEqual(["prayu.locale.v1"]);
    expect(sessionStorage.length).toBe(0);
  });

  it("starts a new Run, commits the message, and executes one model turn", async () => {
    const controlRunLifecycle = vi.fn().mockResolvedValue({
      version: "run_lifecycle_control.v1", action: "start", expected_status: "created",
      applied_status: "running", run: { ...runningRun }, replayed: false,
      event_sequence_start: 1, event_sequence_end: 2, execution_started: false,
      model_called: false, tool_called: false, capability_grant: false,
    } as RunLifecycleControlView);
    const submitSessionMessage = vi.fn().mockResolvedValue(result);
    const executeRun = vi.fn().mockResolvedValue({
      version: "run_execution_handoff.v1", operation_id: "op-1", run_id: "run-1",
      session_id: "sess-1", max_steps: 1, status: "completed", run_status: "running",
      steps_completed: 1, stop_reason: "max_steps", selected_count: 1,
      committed_count: 1, pending_count: 0, prepared_count: 0, cancelled_count: 0,
      completion_event_sequence: 9, replayed: false, execution_started: true,
      model_called: true, tool_called: false, capability_grant: false,
    } as RunExecutionControlView);
    const client = {
      hasSessionMessages: true, hasRunLifecycle: true, hasRunExecution: true,
      controlRunLifecycle, submitSessionMessage, executeRun,
    } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderComposer(client, { id: "run-1", status: "created" } as RunView);

    await user.type(screen.getByLabelText("Session message"), "Inspect the repository");
    await user.click(screen.getByRole("button", { name: "Queue message" }));
    await screen.findByText("Model reply committed");

    expect(controlRunLifecycle).toHaveBeenCalledWith("run-1", {
      version: "run_lifecycle_control.v1", action: "start",
    }, expect.stringMatching(/^web-session-turn-lifecycle-/));
    expect(submitSessionMessage).toHaveBeenCalledWith("sess-1", {
      version: "session_message_submission.v1", content: "Inspect the repository",
    }, expect.stringMatching(/^web-session-message-/));
    expect(executeRun).toHaveBeenCalledWith("run-1", {
      version: "run_execution_handoff.v1", max_steps: 1,
    }, expect.stringMatching(/^web-session-turn-execution-/));
    expect(controlRunLifecycle.mock.invocationCallOrder[0]).toBeLessThan(
      submitSessionMessage.mock.invocationCallOrder[0]);
    expect(submitSessionMessage.mock.invocationCallOrder[0]).toBeLessThan(
      executeRun.mock.invocationCallOrder[0]);
  });

  it("shows a safe provisional reply and cancels its exact active attempt", async () => {
    let completeExecution: ((value: RunExecutionControlView) => void) | undefined;
    const execution = new Promise<RunExecutionControlView>((resolve) => {
      completeExecution = resolve;
    });
    const executionResult = {
      version: "run_execution_handoff.v1", operation_id: "op-1", run_id: "run-1",
      session_id: "sess-1", max_steps: 1, status: "completed", run_status: "running",
      steps_completed: 1, stop_reason: "max_steps", selected_count: 1,
      committed_count: 1, pending_count: 0, prepared_count: 0, cancelled_count: 0,
      completion_event_sequence: 9, replayed: false, execution_started: true,
      model_called: true, tool_called: false, capability_grant: false,
    } as RunExecutionControlView;
    const snapshot = {
      version: "model_public_stream.v1",
      call: {
        run_id: "run-1", session_id: "sess-1", attempt_id: "attempt-live",
        model_attempt: 2, transport_attempt: 1, max_attempts: 3, protocol_repair: 0,
        tool_round: 0, provider: "deepseek", model: "deepseek-chat",
        started_at: "2026-08-08T00:00:00Z", stream_chunks: 2, stream_bytes: 40,
        cancel_requested: false,
      },
      revision: 2, text: "Visible safe model answer", message_complete: false,
      provisional: true, updated_at: "2026-08-08T00:00:01Z",
    } as PublicModelStreamSnapshot;
    const cancelModelCall = vi.fn().mockResolvedValue({
      id: "cancel-1", run_id: "run-1", attempt_id: "attempt-live", model_attempt: 2,
      status: "pending", requested_at: "2026-08-08T00:00:02Z", replayed: false,
    });
    const client = {
      hasSessionMessages: true, hasRunLifecycle: true, hasRunExecution: true,
      hasControl: true, submitSessionMessage: vi.fn().mockResolvedValue(result),
      executeRun: vi.fn().mockReturnValue(execution),
      getPublicModelStream: vi.fn().mockResolvedValue(snapshot), cancelModelCall,
    } as unknown as CyberAgentClient;
    const user = userEvent.setup();
    renderComposer(client, runningRun);

    await user.type(screen.getByLabelText("Session message"), "Inspect this change");
    await user.click(screen.getByRole("button", { name: "Queue message" }));
    await screen.findByText("Visible safe model answer");
    await user.click(screen.getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(cancelModelCall).toHaveBeenCalledTimes(1));
    expect(cancelModelCall).toHaveBeenCalledWith("run-1", {
      attempt_id: "attempt-live", model_attempt: 2,
      reason: "operator stopped provisional model response",
    }, expect.stringMatching(/^web-run-cancel-call-/));

    completeExecution?.(executionResult);
    await screen.findByText("Model reply committed");
    expect(screen.queryByLabelText("Provisional model reply")).not.toBeInTheDocument();
  });

  it("sends with Enter while preserving Shift+Enter and IME composition", async () => {
    const submitSessionMessage = vi.fn().mockResolvedValue(result);
    const client = {
      hasSessionMessages: true,
      hasRunLifecycle: false,
      hasRunExecution: false,
      submitSessionMessage,
    } as unknown as CyberAgentClient;
    renderComposer(client, runningRun);
    const composer = screen.getByLabelText("Session message");
    fireEvent.change(composer, { target: { value: "Review the current branch" } });

    expect(fireEvent.keyDown(composer, { key: "Enter", shiftKey: true })).toBe(true);
    expect(fireEvent.keyDown(composer, { key: "Enter", isComposing: true })).toBe(true);
    expect(submitSessionMessage).not.toHaveBeenCalled();

    expect(fireEvent.keyDown(composer, { key: "Enter" })).toBe(false);
    await waitFor(() => expect(submitSessionMessage).toHaveBeenCalledTimes(1));
    expect(submitSessionMessage).toHaveBeenCalledWith("sess-1", {
      version: "session_message_submission.v1", content: "Review the current branch",
    }, expect.stringMatching(/^web-session-message-/));
  });

  it("enforces the UTF-8 byte limit before issuing a request", async () => {
    const submitSessionMessage = vi.fn();
    const client = {
      hasSessionMessages: true,
      hasRunLifecycle: false,
      hasRunExecution: false,
      submitSessionMessage,
    } as unknown as CyberAgentClient;
    renderComposer(client, runningRun);

    fireEvent.change(screen.getByLabelText("Session message"), {
      target: { value: "测".repeat(6000) },
    });
    expect(screen.getByText("Message exceeds 16384 UTF-8 bytes")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Queue message" })).toBeDisabled();
    expect(submitSessionMessage).not.toHaveBeenCalled();
  });

  it("renders only for an enabled Run-bound capability and fails closed by Run status", async () => {
    const disabled = {
      hasSessionMessages: false,
      submitSessionMessage: vi.fn(),
    } as unknown as CyberAgentClient;
    const { rerender } = renderComposer(disabled, runningRun);
    expect(screen.queryByLabelText("Session message")).not.toBeInTheDocument();

    const enabled = {
      hasSessionMessages: true,
      hasRunLifecycle: false,
      hasRunExecution: false,
      submitSessionMessage: vi.fn(),
    } as unknown as CyberAgentClient;
    rerender(withProvider(<SessionComposer client={enabled} sessionID="sess-1"
      run={{ ...runningRun, status: "created" }} />));
    await waitFor(() => expect(screen.getByLabelText("Session message")).toBeDisabled());
    expect(screen.getByText("Run unavailable")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Queue message" })).toBeDisabled();
  });
});

describe("SessionSteeringQueue", () => {
  it("cancels only pending metadata and reuses the in-memory retry key", async () => {
    const cancelSessionSteering = vi.fn()
      .mockRejectedValueOnce(new Error("response unavailable"))
      .mockResolvedValueOnce(cancellationResult);
    const client = {
      hasSessionSteeringControl: true,
      cancelSessionSteering,
    } as unknown as CyberAgentClient;
    const state = {
      pending: 1, prepared: 0, committed: 1, cancelled: 0,
      messages: [
        { id: "steer-1", sequence: 3, status: "pending", prepared: false,
          created_at: "2026-07-18T00:00:00Z" },
        { id: "steer-2", sequence: 2, status: "committed", created_at: "2026-07-18T00:00:00Z",
          committed_at: "2026-07-18T00:00:30Z", prepared: false },
      ],
    } as OperatorSteeringQueueView;
    const user = userEvent.setup();
    render(withProvider(<SessionSteeringQueue client={client} sessionID="sess-1" state={state} />));

    expect(screen.queryByRole("button", { name: "Cancel queued message 2" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel queued message 3" }));
    await screen.findByText("response unavailable");
    await user.click(screen.getByRole("button", { name: "Cancel queued message 3" }));
    await waitFor(() => expect(cancelSessionSteering).toHaveBeenCalledTimes(2));

    expect(cancelSessionSteering.mock.calls[0]?.[0]).toBe("sess-1");
    expect(cancelSessionSteering.mock.calls[0]?.[1]).toBe("steer-1");
    expect(cancelSessionSteering.mock.calls[0]?.[2]).toEqual({
      version: "session_steering_cancellation.v1",
      reason: "operator cancelled queued Session message",
    });
    expect(cancelSessionSteering.mock.calls[0]?.[3]).toBe(
      cancelSessionSteering.mock.calls[1]?.[3]);
    expect(Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index)))
      .toEqual(["prayu.locale.v1"]);
    expect(sessionStorage.length).toBe(0);
  });

  it("stays hidden without its distinct capability", () => {
    const client = { hasSessionSteeringControl: false } as CyberAgentClient;
    render(withProvider(<SessionSteeringQueue client={client} sessionID="sess-1" state={{
      pending: 1, prepared: 0, committed: 0, cancelled: 0,
      messages: [{ id: "steer-1", sequence: 1, status: "pending", prepared: false,
        created_at: "2026-07-18T00:00:00Z" }],
    }} />));
    expect(screen.queryByLabelText("Queued Session messages")).not.toBeInTheDocument();
  });

  it("does not offer cancellation for an already prepared message", () => {
    const client = { hasSessionSteeringControl: true } as CyberAgentClient;
    render(withProvider(<SessionSteeringQueue client={client} sessionID="sess-1" state={{
      pending: 0, prepared: 1, committed: 0, cancelled: 0,
      messages: [{ id: "steer-prepared", sequence: 1, status: "pending", prepared: true,
        created_at: "2026-07-18T00:00:00Z" }],
    }} />));
    expect(screen.queryByLabelText("Queued Session messages")).not.toBeInTheDocument();
  });
});

function renderComposer(client: CyberAgentClient, run: RunView | null) {
  return render(withProvider(<SessionComposer client={client} sessionID="sess-1" run={run} />));
}

function withProvider(node: ReactNode) {
  return <LocaleProvider><QueryClientProvider client={new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })}>{node}</QueryClientProvider></LocaleProvider>;
}
