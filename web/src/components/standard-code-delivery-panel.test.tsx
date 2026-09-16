import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { APIRequestError, type CyberAgentClient } from "../api/client";
import type { ArtifactView } from "../api/types";
import { formatDate } from "../lib/format";
import { standardCodeDeliveryFixture } from "../test/standard-code-delivery";
import { StandardCodeDeliveryPanel } from "./standard-code-delivery-panel";

function observedReport() {
  const report = standardCodeDeliveryFixture();
  return { ...report, observation: { observed_at: "2026-09-08T02:30:00Z", revision_sha256: report.final_checkpoint.revision_sha256 } };
}
function artifact(): ArtifactView {
  return { id: "artifact-stdout", run_id: "run-1", session_id: "session-1", workspace_id: "workspace-1",
    source_id: "verification-1", sha256: "a".repeat(64), size_bytes: 12, stream: "stdout", redacted: true,
    tool_name: "command_runtime", encoding: "utf-8", mime: "text/plain; charset=utf-8", kind: "tool_output", created_at: "2026-08-26T08:00:00Z" };
}

function readableReport() {
  return { ...observedReport(), output_sources: [{ job_id: "verification-1", artifact_id: "artifact-stdout",
    status: "available" as const, thread_id: "thread-1", activity_ref: "command-1" }] };
}

function savedOutput(content = "已保存的中文正文\n[REDACTED:secret]") {
  return { version: "thread_activity_artifact.v1", activity_ref: "command-1", artifact_ref: "artifact-stdout",
    stream: "stdout", mime: "text/plain; charset=utf-8", content, sha256: "b".repeat(64),
    size_bytes: new TextEncoder().encode(content).byteLength, redacted: true, truncated: false,
    untrusted: true, instruction_authorized: false };
}

describe("StandardCodeDeliveryPanel", () => {
  it.each(["HTTP API endpoint was not found", "Standard Code delivery report was not found"])(
    "does not turn an ambiguous 404 into a claim that no report exists (%s)", async (message) => {
      const read = vi.fn().mockRejectedValue(new APIRequestError(message, "NOT_FOUND", 404));
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      render(<QueryClientProvider client={queryClient}><StandardCodeDeliveryPanel client={{
        standardCodeDelivery: read, hasControl: false, hasStandardCodePreset: false,
      } as unknown as CyberAgentClient} runID="run-1" onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} /></QueryClientProvider>);
      expect(await screen.findByText(/A report may not have been generated, or the reporting endpoint may be disabled/)).toBeInTheDocument();
      expect(screen.queryByText(/No delivery report is available/)).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Refresh delivery report" })).toBeEnabled();
      expect(read).toHaveBeenCalledWith("run-1", expect.any(AbortSignal));
    });

  it("keeps a readable failed historical report visible without preset control authority", async () => {
    const report = observedReport();
    const read = vi.fn().mockResolvedValue({ ...report, status: "failed", receipt_status: "failed", verified: false,
      verifications: report.verifications.map((verification) => ({ ...verification, status: "failed", exit_code: 1 })) });
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <StandardCodeDeliveryPanel client={{ standardCodeDelivery: read, hasControl: false, hasStandardCodePreset: false,
      } as unknown as CyberAgentClient} runID="run-1" onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} />
    </QueryClientProvider>);
    expect(await screen.findByText("The last checked revision was not verified")).toBeInTheDocument();
    expect(screen.getByText("Recorded conclusion").nextElementSibling).toHaveTextContent("failed");
    expect(screen.getByText("internal/example.go")).toBeInTheDocument();
    expect(read).toHaveBeenCalledWith("run-1", expect.any(AbortSignal));
  });

  it("opens the exact reported output metadata and the available recovery options", async () => {
    const user = userEvent.setup();
    const standardCodeDelivery = vi.fn().mockResolvedValue(observedReport());
    const getArtifact = vi.fn().mockResolvedValue(artifact());
    const onOpenCheckpoints = vi.fn();
    const onOpenFile = vi.fn();
    const client = { standardCodeDelivery, getArtifact } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}>
      <StandardCodeDeliveryPanel client={client} runID="run-1"
        onOpenCheckpoints={onOpenCheckpoints}
        onOpenFile={onOpenFile} />
    </QueryClientProvider>);

    expect(await screen.findByText("The last checked revision was verified")).toBeInTheDocument();
    expect(screen.getByText("internal/example.go")).toBeInTheDocument();
    expect(screen.getAllByText("f".repeat(64)).length).toBeGreaterThan(0);
    expect(screen.getByText("completed · exit 0 · retries 1")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "internal/example.go" }));
    expect(onOpenFile).toHaveBeenCalledWith("internal/example.go", "drydock-workspace-1");
    expect(getArtifact).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Inspect 1 output records" }));
    const outputs = screen.getByRole("region", { name: "Output records referenced by this report" });
    expect(await within(outputs).findByText("artifact-stdout")).toBeInTheDocument();
    expect(getArtifact).toHaveBeenCalledWith("artifact-stdout", expect.any(AbortSignal));
    expect(within(outputs).getByText(/This service did not provide a readable source/)).toBeInTheDocument();
    expect(within(outputs).getByText("verification-1", { selector: "dd" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "View workspace recovery options" }));
    expect(onOpenCheckpoints).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Fork" })).not.toBeInTheDocument();
    expect(screen.queryByText(/C:\\Users|\/home\/|very-secret-delivery-value/i))
      .not.toBeInTheDocument();
  });

  it.each([new Error("connection lost"), new APIRequestError("report unavailable", "NOT_FOUND", 404)])(
    "keeps a failed refresh historical without presenting cached passed data as current (%s)", async (error) => {
      const user = userEvent.setup();
      const standardCodeDelivery = vi.fn().mockResolvedValueOnce(observedReport()).mockRejectedValue(error);
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      render(<QueryClientProvider client={queryClient}><StandardCodeDeliveryPanel
        client={{ standardCodeDelivery } as unknown as CyberAgentClient} runID="run-1"
        onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} /></QueryClientProvider>);
      expect(await screen.findByText("The last checked revision was verified")).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "Refresh delivery report" }));
      const heading = await screen.findByText("The latest check failed; the current revision is unconfirmed");
      expect(heading.closest("section")).toHaveClass("delivery-truth-unknown");
      expect(screen.queryByText("The last checked revision was verified")).not.toBeInTheDocument();
      expect(screen.queryByText(/A report may not have been generated/)).not.toBeInTheDocument();
      expect(screen.getByText("internal/example.go")).toBeInTheDocument();
      expect(screen.getByText("Recorded conclusion").nextElementSibling).toHaveTextContent("passed");
      expect(screen.getByText("Last revision check").nextElementSibling).toHaveTextContent(formatDate(observedReport().observation.observed_at));
    });

  it("distinguishes a stale observation from the original passed receipt", async () => {
    const report = observedReport();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><StandardCodeDeliveryPanel client={{
      standardCodeDelivery: vi.fn().mockResolvedValue({ ...report, status: "stale", verified: false,
        observation: { ...report.observation, revision_sha256: "0".repeat(64), reason_code: "workspace_modified_after_verification" } }),
    } as unknown as CyberAgentClient} runID="run-1" onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} /></QueryClientProvider>);
    expect(await screen.findByText("The report is stale; the current revision is not verified")).toBeInTheDocument();
    expect(screen.getByText("Workspace changed after the report was recorded")).toBeInTheDocument();
    expect(screen.getByText("Recorded conclusion").nextElementSibling).toHaveTextContent("passed");
    expect(screen.getByText("Reported")).toBeInTheDocument();
  });

  it("does not assert a current pass without an observation and rejects output metadata from another job", async () => {
    const user = userEvent.setup();
    const getArtifact = vi.fn().mockResolvedValue({ ...artifact(), source_id: "unrelated-job" });
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <StandardCodeDeliveryPanel client={{ standardCodeDelivery: vi.fn().mockResolvedValue(standardCodeDeliveryFixture()),
        getArtifact } as unknown as CyberAgentClient} runID="run-1" onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} />
    </QueryClientProvider>);
    expect(await screen.findByText("The current revision has not been checked")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Inspect 1 output records" }));
    expect(await screen.findByText(/Artifact metadata does not match/)).toBeInTheDocument();
    expect(screen.queryByText("unrelated-job")).not.toBeInTheDocument();
    getArtifact.mockResolvedValue(artifact());
    await user.click(screen.getByRole("button", { name: "Retry artifact metadata" }));
    await waitFor(() => expect(screen.getByText("artifact-stdout")).toBeInTheDocument());
  });

  it("lazily reads separately redacted output in the report and retries without changing its conclusion", async () => {
    const user = userEvent.setup();
    const threadActivityArtifact = vi.fn().mockRejectedValueOnce(new Error("temporary read failure"))
      .mockResolvedValueOnce({ ...savedOutput(), truncated: true });
    const getArtifact = vi.fn().mockResolvedValue(artifact());
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <StandardCodeDeliveryPanel client={{ standardCodeDelivery: vi.fn().mockResolvedValue(readableReport()),
        getArtifact, threadActivityArtifact } as unknown as CyberAgentClient} runID="run-1"
        onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} />
    </QueryClientProvider>);
    await screen.findByText("The last checked revision was verified");
    expect(getArtifact).not.toHaveBeenCalled();
    expect(threadActivityArtifact).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Inspect 1 output records" }));
    const read = await screen.findByRole("button", { name: /Read saved stdout/ });
    expect(threadActivityArtifact).not.toHaveBeenCalled();
    await user.click(read);
    expect(await screen.findByRole("alert")).toHaveTextContent("Saved output could not be loaded.");
    expect(screen.getByText("Recorded conclusion").nextElementSibling).toHaveTextContent("passed");
    await user.click(screen.getByRole("button", { name: "Retry output" }));
    expect(await screen.findByText(/已保存的中文正文/)).toBeInTheDocument();
    expect(screen.getByText(/Capture limit reached; uncaptured output is unavailable/)).toBeInTheDocument();
    expect(screen.getByText(/may have a different size and digest/)).toBeInTheDocument();
    expect(threadActivityArtifact).toHaveBeenCalledTimes(2);
    expect(threadActivityArtifact).toHaveBeenLastCalledWith("thread-1", "command-1", "artifact-stdout", expect.any(AbortSignal));
  });

  it("does not read output when its source record fails the report's exact binding", async () => {
    const user = userEvent.setup();
    const threadActivityArtifact = vi.fn();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <StandardCodeDeliveryPanel client={{ standardCodeDelivery: vi.fn().mockResolvedValue(readableReport()),
        getArtifact: vi.fn().mockResolvedValue({ ...artifact(), source_id: "another-job" }),
        threadActivityArtifact } as unknown as CyberAgentClient} runID="run-1"
        onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} />
    </QueryClientProvider>);
    await user.click(await screen.findByRole("button", { name: "Inspect 1 output records" }));
    expect(await screen.findByText(/Artifact metadata does not match/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Read saved/ })).not.toBeInTheDocument();
    expect(threadActivityArtifact).not.toHaveBeenCalled();
  });

  it("keeps unavailable sources metadata-only without inventing a readable activity", async () => {
    const user = userEvent.setup();
    const threadActivityArtifact = vi.fn();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <StandardCodeDeliveryPanel client={{ standardCodeDelivery: vi.fn().mockResolvedValue({ ...observedReport(),
        output_sources: [{ job_id: "verification-1", artifact_id: "artifact-stdout", status: "metadata_only", reason: "output_not_public" }] }),
        getArtifact: vi.fn().mockResolvedValue(artifact()), threadActivityArtifact } as unknown as CyberAgentClient}
        runID="run-1" onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} />
    </QueryClientProvider>);
    await user.click(await screen.findByRole("button", { name: "Inspect 1 output records" }));
    expect(await screen.findByText(/not eligible for public display/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Read saved/ })).not.toBeInTheDocument();
    expect(threadActivityArtifact).not.toHaveBeenCalled();
  });

  it("does not display a previous Run's late output after opening another report", async () => {
    const user = userEvent.setup();
    let finishOld!: (value: unknown) => void;
    const threadActivityArtifact = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve; }))
      .mockResolvedValueOnce({ ...savedOutput("另一任务的新输出"), activity_ref: "command-2", artifact_ref: "artifact-2" });
    const other = readableReport();
    const otherReport = { ...other, id: "report-2", binding: { ...other.binding, run_id: "run-2" },
      verifications: [{ ...other.verifications[0], job_id: "verification-2", artifacts: [{ ...other.verifications[0].artifacts[0], id: "artifact-2" }] }],
      output_sources: [{ job_id: "verification-2", artifact_id: "artifact-2", status: "available", thread_id: "thread-2", activity_ref: "command-2" }] };
    const client = { standardCodeDelivery: vi.fn((id: string) => Promise.resolve(id === "run-1" ? readableReport() : otherReport)),
      getArtifact: vi.fn((id: string) => Promise.resolve(id === "artifact-stdout" ? artifact() :
        { ...artifact(), id: "artifact-2", run_id: "run-2", source_id: "verification-2" })), threadActivityArtifact } as unknown as CyberAgentClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = (runID: string) => <QueryClientProvider client={queryClient}><StandardCodeDeliveryPanel client={client}
      runID={runID} onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} /></QueryClientProvider>;
    const rendered = render(view("run-1"));
    await user.click(await screen.findByRole("button", { name: "Inspect 1 output records" }));
    await user.click(await screen.findByRole("button", { name: /Read saved stdout/ }));
    await waitFor(() => expect(threadActivityArtifact).toHaveBeenCalledTimes(1));
    rendered.rerender(view("run-2"));
    await user.click(await screen.findByRole("button", { name: "Inspect 1 output records" }));
    await user.click(await screen.findByRole("button", { name: /Read saved stdout/ }));
    expect(await screen.findByText("另一任务的新输出")).toBeInTheDocument();
    await act(async () => finishOld(savedOutput("旧任务迟到的正文")));
    expect(screen.queryByText("旧任务迟到的正文")).not.toBeInTheDocument();
    expect(screen.getByText("另一任务的新输出")).toBeInTheDocument();
    expect(threadActivityArtifact.mock.calls[1]?.slice(0, 3)).toEqual(["thread-2", "command-2", "artifact-2"]);
  });

  it("keeps two jobs and their stdout/stderr readers separate within the same report", async () => {
    const user = userEvent.setup();
    let finishFirst!: (value: unknown) => void;
    const threadActivityArtifact = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { finishFirst = resolve; }))
      .mockResolvedValueOnce({ ...savedOutput("第二条命令的标准错误"), stream: "stderr", activity_ref: "command-2", artifact_ref: "artifact-stderr" });
    const report = readableReport();
    const secondArtifact = { ...report.verifications[0].artifacts[0], id: "artifact-stderr", stream: "stderr" as const };
    const bothJobs = { ...report, verifications: [...report.verifications,
      { ...report.verifications[0], job_id: "verification-2", artifacts: [secondArtifact] }],
      output_sources: [...report.output_sources,
        { job_id: "verification-2", artifact_id: "artifact-stderr", status: "available", thread_id: "thread-1", activity_ref: "command-2" }] };
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <StandardCodeDeliveryPanel client={{ standardCodeDelivery: vi.fn().mockResolvedValue(bothJobs),
        getArtifact: vi.fn((id: string) => Promise.resolve(id === "artifact-stdout" ? artifact() :
          { ...artifact(), id: "artifact-stderr", source_id: "verification-2", stream: "stderr" })),
        threadActivityArtifact } as unknown as CyberAgentClient} runID="run-1" onOpenCheckpoints={vi.fn()} onOpenFile={vi.fn()} />
    </QueryClientProvider>);
    const buttons = await screen.findAllByRole("button", { name: "Inspect 1 output records" });
    await user.click(buttons[0]!);
    await user.click(await screen.findByRole("button", { name: /Read saved stdout/ }));
    await waitFor(() => expect(threadActivityArtifact).toHaveBeenCalledTimes(1));
    await user.click(buttons[1]!);
    expect(threadActivityArtifact).toHaveBeenCalledTimes(1);
    await user.click(await screen.findByRole("button", { name: /Read saved stderr/ }));
    expect(await screen.findByText("第二条命令的标准错误")).toBeInTheDocument();
    await act(async () => finishFirst(savedOutput("第一条命令迟到的标准输出")));
    expect(screen.queryByText("第一条命令迟到的标准输出")).not.toBeInTheDocument();
    expect(screen.getByText("第二条命令的标准错误")).toBeInTheDocument();
    expect(threadActivityArtifact.mock.calls[1]?.slice(0, 3)).toEqual(["thread-1", "command-2", "artifact-stderr"]);
  });
});
