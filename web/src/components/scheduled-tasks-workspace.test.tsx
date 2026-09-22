import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import type { ScheduledJobCreateRequestView, ScheduledJobView } from "../api/types";
import { ScheduledTasksWorkspace } from "./scheduled-tasks-workspace";

describe("ScheduledTasksWorkspace", () => {
  it("creates only a bounded read-only schedule and uses a memory-only operation key", async () => {
    const user = userEvent.setup();
    const createScheduledJob = vi.fn().mockResolvedValue({
      protocol_version: "scheduled-job-control.v1", action: "create", replayed: false,
      execution_started: false, authority_bypass: false, job: scheduledJob(),
    });
    renderWorkspace({
      hasScheduledJobControl: true, hasScheduledJobWorker: true,
      listScheduledJobs: vi.fn().mockResolvedValue({ protocol_version: "scheduled-job.v1", items: [] }),
      createScheduledJob, getScheduledJob: vi.fn(), transitionScheduledJob: vi.fn(),
      diagnosticBundle: vi.fn(),
    });

    await screen.findByText("No scheduled Runs");
    await user.click(screen.getByRole("button", { name: "Create and observe" }));
    await waitFor(() => expect(createScheduledJob).toHaveBeenCalledTimes(1));
    expect(createScheduledJob.mock.calls[0]?.[0]).toBe("run-1");
    expect(createScheduledJob.mock.calls[0]?.[1]).toMatchObject({
      version: "scheduled-job.v1", execution_mode: "read_only", confirm_repair: false,
      max_model_calls: 0, stop_on_target_terminal: true,
      observation_consent_version: 1,
      retry: { max_attempts: 3, initial_backoff_seconds: 5, max_backoff_seconds: 60 },
    });
    const body = createScheduledJob.mock.calls[0]?.[1] as ScheduledJobCreateRequestView;
    const secondsUntilDeadline = Math.floor(
      (new Date(body.deadline_at).getTime() - Date.now()) / 1_000);
    expect(Math.abs(body.max_elapsed_seconds - secondsUntilDeadline)).toBeLessThan(3);
    expect(createScheduledJob.mock.calls[0]?.[2]).toMatch(/^web-scheduled-job-create-/);
  });

  it("pauses by exact revision and keeps diagnostics as a redacted export", async () => {
    const user = userEvent.setup();
    const job = scheduledJob();
    const transitionScheduledJob = vi.fn().mockResolvedValue({
      protocol_version: "scheduled-job-control.v1", action: "pause", replayed: false,
      execution_started: false, authority_bypass: false, job: { ...job, status: "paused" },
    });
    const diagnosticBundle = vi.fn().mockResolvedValue({ protocol_version: "diagnostic-bundle.v1" });
    vi.stubGlobal("URL", { ...URL, createObjectURL: vi.fn().mockReturnValue("blob:test"),
      revokeObjectURL: vi.fn() });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    renderWorkspace({
      hasScheduledJobControl: true, hasScheduledJobWorker: false,
      listScheduledJobs: vi.fn().mockResolvedValue({ protocol_version: "scheduled-job.v1", items: [job] }),
      getScheduledJob: vi.fn().mockResolvedValue({ protocol_version: "scheduled-job.v1",
        snapshot: { job, rounds: [], notifications: [] } }),
      createScheduledJob: vi.fn(), transitionScheduledJob, diagnosticBundle,
    });

    await user.click(await screen.findByRole("button", { name: /scheduled-job-1/i }));
    await user.click(await screen.findByRole("button", { name: "Pause" }));
    await waitFor(() => expect(transitionScheduledJob).toHaveBeenCalledWith(
      "run-1", "scheduled-job-1", "pause",
      { version: "scheduled-job-control.v1", expected_revision: 4 }, expect.any(String)));
    await user.click(await screen.findByRole("button", { name: "Export diagnostics" }));
    await waitFor(() => expect(diagnosticBundle).toHaveBeenCalledWith("run-1"));
    expect(click).toHaveBeenCalled();
    click.mockRestore();
    vi.unstubAllGlobals();
  });

  it("reuses the exact create request after a lost response and starts a new intent only after editing", async () => {
    const user = userEvent.setup();
    const createScheduledJob = vi.fn().mockRejectedValue(new Error("Connection lost; outcome unknown"));
    renderWorkspace({ hasScheduledJobControl: true, hasScheduledJobWorker: true,
      listScheduledJobs: vi.fn().mockResolvedValue({ protocol_version: "scheduled-job.v1", items: [] }),
      createScheduledJob,
    });
    const submit = screen.getByRole("button", { name: "Create and observe" });
    await user.click(submit);
    await screen.findByText("Connection lost; outcome unknown");
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.now() + 10_000);
    await user.click(submit);
    await waitFor(() => expect(createScheduledJob).toHaveBeenCalledTimes(2));
    expect(createScheduledJob.mock.calls[1]).toEqual(createScheduledJob.mock.calls[0]);
    await user.selectOptions(screen.getByLabelText("Notifications"), "silent");
    await user.click(submit);
    await waitFor(() => expect(createScheduledJob).toHaveBeenCalledTimes(3));
    expect(createScheduledJob.mock.calls[2]?.[2]).not.toBe(createScheduledJob.mock.calls[0]?.[2]);
    expect(createScheduledJob.mock.calls[2]?.[1]).toMatchObject({ notification: "silent" });
    now.mockRestore();
  });

  it("confirms one legacy paused schedule without resuming it or claiming a stopped worker is running", async () => {
    const user = userEvent.setup();
    const job = { ...scheduledJob(), status: "paused" as const, observation_consent_version: 0 };
    const enabledJob = { ...job, observation_consent_version: 1 };
    const enableScheduledJobObservation = vi.fn().mockRejectedValueOnce(new Error("Observation response lost"))
      .mockResolvedValue({ job: enabledJob });
    const transitionScheduledJob = vi.fn();
    renderWorkspace({
      hasScheduledJobControl: true, hasScheduledJobWorker: true,
      runtimeCapabilities: vi.fn().mockResolvedValue({ scheduled_job_worker: { enabled: true, state: "stopped" } }),
      listScheduledJobs: vi.fn().mockResolvedValue({ protocol_version: "scheduled-job.v1", items: [job] }),
      getScheduledJob: vi.fn().mockResolvedValue({ protocol_version: "scheduled-job.v1",
        snapshot: { job, rounds: [], notifications: [] } }),
      enableScheduledJobObservation, transitionScheduledJob,
    });
    await screen.findByText(/Observer is stopped or shutting down/);
    expect(screen.queryByText(/Observer is running/)).not.toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: /scheduled-job-1/i }));
    await user.click(await screen.findByRole("button", { name: "Confirm automatic desktop observation" }));
    await screen.findByText("Observation response lost");
    await user.click(await screen.findByRole("button", { name: "Confirm automatic desktop observation" }));
    await waitFor(() => expect(enableScheduledJobObservation).toHaveBeenCalledTimes(2));
    expect(enableScheduledJobObservation.mock.calls[1]).toEqual(enableScheduledJobObservation.mock.calls[0]);
    expect(enableScheduledJobObservation).toHaveBeenCalledWith(
      "run-1", "scheduled-job-1", { version: "scheduled-job-control.v1", expected_revision: 4,
        observation_consent_version: 1 }, expect.stringMatching(/^web-scheduled-job-enable-observation-/));
    expect(transitionScheduledJob).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Resume" })).toBeInTheDocument();
  });

  it("identifies an explicitly launched all-job worker without claiming a legacy job is waiting", async () => {
    const job = { ...scheduledJob(), observation_consent_version: 0 };
    renderWorkspace({
      hasScheduledJobControl: true, hasScheduledJobWorker: true,
      runtimeCapabilities: vi.fn().mockResolvedValue({ scheduled_job_worker: {
        enabled: true, state: "running", selection_scope: "all_jobs",
      } }),
      listScheduledJobs: vi.fn().mockResolvedValue({ protocol_version: "scheduled-job.v1", items: [job] }),
    });
    await screen.findByText(/Explicit scheduling of all jobs, including legacy jobs/);
    expect(screen.queryByText("Desktop observation needs confirmation")).not.toBeInTheDocument();
  });
});

function renderWorkspace(client: Partial<CyberAgentClient>) {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  return render(<QueryClientProvider client={queryClient}>
    <ScheduledTasksWorkspace client={{
      runtimeCapabilities: vi.fn().mockResolvedValue({ scheduled_job_worker: {
        enabled: Boolean(client.hasScheduledJobWorker), state: client.hasScheduledJobWorker ? "running" : "disabled",
        selection_scope: "confirmed_read_only",
      } }), ...client,
    } as CyberAgentClient} initialRunID="run-1" />
  </QueryClientProvider>);
}

function scheduledJob(): ScheduledJobView {
  return {
    id: "scheduled-job-1", owner_run_id: "run-1", owner_root_agent_id: "agent-root",
    status: "active", revision: 4, active_lease_generation: 0, rounds_completed: 0,
    consecutive_unchanged: 0, model_calls: 0, last_event_sequence: 0,
    observation_consent_version: 1,
    next_wake_at: "2026-08-20T11:00:00Z", created_by: "test",
    created_at: "2026-08-20T10:00:00Z", updated_at: "2026-08-20T10:00:00Z",
    spec: { version: "scheduled-job.v1", target_run_id: "run-1", execution_mode: "read_only",
      schedule: { kind: "once", timezone: "UTC", anchor_at: "2026-08-20T11:00:00Z",
        misfire_policy: "run_once" }, deadline_at: "2026-08-20T12:00:00Z",
      stop_on_target_terminal: true, max_rounds: 1, max_model_calls: 0,
      max_elapsed_seconds: 3600, retry: { max_attempts: 3, initial_backoff_seconds: 5,
        max_backoff_seconds: 60 }, notification: "on_change" },
  };
}
