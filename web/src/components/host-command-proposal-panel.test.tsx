import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import { HostCommandProposalPanel } from "./host-command-proposal-panel";

const proposal = {
  id: "host-command-proposal-1",
  protocol_version: "host_command_proposal.v1",
  policy_version: "host_command_policy.v1",
  run_id: "run-1",
  mission_id: "mission-1",
  session_id: "session-1",
  workspace_id: "workspace-1",
  executable_path: "C:\\Program Files\\Go\\bin\\go.exe",
  executable_sha256: "a".repeat(64),
  argv: ["test", "./internal/application"],
  working_directory: "D:\\GitProjects\\Prayu",
  environment_policy: "sanitized_host_environment.v1",
  environment_keys: ["PATH", "SYSTEMROOT"],
  environment_sha256: "b".repeat(64),
  network_intent: "host",
  timeout_milliseconds: 120_000,
  purpose: "Run the focused application tests",
  spec_fingerprint: "c".repeat(64),
  permission_mode: "approval",
  permission_revision: 3,
  operator_review_required: true,
  non_sandboxed: true,
  automatic_retry_allowed: false,
  instruction_authorized: false,
  execution_authorized: false,
  capability_grant: false,
  fingerprint: "d".repeat(64),
  created_at: "2026-08-09T00:00:00Z",
  evidence_instruction_authorized: false,
};

const riskProposal = {
  ...proposal,
  id: "risk-escalation-1",
  protocol_version: "risk_escalation.v1",
  policy_version: "risk_escalation_policy.v1",
  permission_mode: "workspace_access",
  permission_revision: 4,
  state: "waiting_approval",
  supervisor_turn: 2,
  supervisor_tool_call_id: "call-1",
  tool_invocation_id: "invocation-1",
  mode_snapshot_id: "mode-1",
  mode_revision: 2,
  interaction_snapshot_id: "interaction-1",
  interaction_revision: 3,
  execution_profile_snapshot_id: "profile-1",
  execution_profile_revision: 3,
  permission_snapshot_id: "permission-1",
  workspace_root_fingerprint: "e".repeat(64),
  capability_generation: "f".repeat(64),
  scope_fingerprint: "1".repeat(64),
  risk_kinds: ["credential", "host_path", "network", "policy_denial"],
  network_targets: ["proxy.golang.org:443"],
  network_purpose: "Download the declared module",
  credential_kinds: ["system_proxy"],
  host_paths: ["C:\\ProgramData\\go\\cache"],
  policy_code: "workspace_network_denied",
  policy_reason: "Workspace network is denied",
  max_output_bytes: 64 * 1024 * 1024,
  active_process_limit: 32,
  process_memory_bytes: 2 * 1024 * 1024 * 1024,
  approval_id: "approval-1",
  approval_status: "pending",
};

describe("HostCommandProposalPanel", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows the exact envelope and requires explicit verification before approval", async () => {
    const user = userEvent.setup();
    const reviewed = {
      ...proposal,
      review: { id: "review-1", decision: "approve", reviewed_by: "http_control_operator",
        reason: "Operator verified and approved the non-sandboxed host command",
        single_use_execution_authorized: true, capability_grant: false,
        created_at: "2026-08-09T00:01:00Z" },
      result: { id: "result-1", status: "completed", source_kind: "go_command_result",
        source_ref: "message-1", content_sha256: "e".repeat(64),
        instruction_authorized: false, raw_output_persisted: false,
        automatic_retry_allowed: false, created_at: "2026-08-09T00:01:01Z" },
      untrusted_evidence: "UNTRUSTED HOST COMMAND RESULT\nstdout_begin\nok\nstdout_end",
    };
    const review = vi.fn().mockResolvedValue(reviewed);
    const client = {
      hasHostCommandProposalControl: true,
      hostCommandProposals: vi.fn().mockResolvedValue({
        items: [proposal], page: { limit: 100 }, requestID: "request-1",
      }),
      reviewHostCommandProposal: review,
    } as unknown as CyberAgentClient;
    vi.stubGlobal("confirm", vi.fn().mockReturnValue(true));
    renderPanel(client);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "High risk: non-sandboxed host calls may involve network",
    );
    expect(screen.getByText(proposal.executable_path)).toBeInTheDocument();
    expect(screen.getByText(proposal.executable_sha256)).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Argument list" }).children).toHaveLength(2);
    expect(screen.getByText(proposal.working_directory)).toBeInTheDocument();
    expect(screen.queryByText("go test ./internal/application")).not.toBeInTheDocument();

    const approve = screen.getByRole("button", { name: "Approve and execute once" });
    expect(approve).toBeDisabled();
    await user.click(screen.getByRole("checkbox", {
      name: "I verified the executable SHA, arguments, directory, and host network access",
    }));
    expect(approve).toBeEnabled();
    await user.click(approve);

    await waitFor(() => expect(review).toHaveBeenCalledTimes(1));
    expect(review.mock.calls[0]?.slice(0, 3)).toEqual([
      "run-1",
      "host-command-proposal-1",
      {
        version: "host_command_review.v1",
        decision: "approve",
        reason: "Operator verified and approved the non-sandboxed host command",
        confirm_execution: true,
      },
    ]);
    expect(await screen.findByText("Untrusted host command evidence")).toBeInTheDocument();
  });

  it("shows exact escalation bindings and lets the operator choose a bounded Run grant", async () => {
    const user = userEvent.setup();
    const reviewed = {
      ...riskProposal,
      state: "completed",
      approval_status: "approved",
      operator_review_required: false,
      execution_authorized: true,
      capability_grant: true,
      grant_id: "grant-1",
      grant_generation: 1,
      grant_max_uses: 2,
      grant_uses_remaining: 1,
      grant_expires_at: "2026-08-09T00:03:00Z",
      grant_consumption_id: "consumption-1",
      review: { id: "approval-1", decision: "approve", reviewed_by: "http_control_operator",
        reason: "Operator approved bounded scope", single_use_execution_authorized: false,
        capability_grant: true, created_at: "2026-08-09T00:01:00Z" },
      result: { id: "result-1", status: "completed", source_kind: "go_command_result",
        source_ref: "message-1", content_sha256: "2".repeat(64),
        instruction_authorized: false, raw_output_persisted: false,
        automatic_retry_allowed: false, created_at: "2026-08-09T00:01:01Z" },
      untrusted_evidence: "UNTRUSTED APPROVED RISK ESCALATION RESULT\nstdout_begin\nok\nstdout_end",
    };
    const review = vi.fn().mockResolvedValue(reviewed);
    const client = {
      hasHostCommandProposalControl: true,
      hostCommandProposals: vi.fn().mockResolvedValue({
        items: [riskProposal], page: { limit: 100 }, requestID: "request-1",
      }),
      reviewHostCommandProposal: review,
    } as unknown as CyberAgentClient;
    vi.stubGlobal("confirm", vi.fn().mockReturnValue(true));
    renderPanel(client);

    expect(await screen.findByText("Risk escalation")).toBeInTheDocument();
    expect(screen.getByText("proxy.golang.org:443")).toBeInTheDocument();
    expect(screen.getByText("system_proxy")).toBeInTheDocument();
    expect(screen.getByText("C:\\ProgramData\\go\\cache")).toBeInTheDocument();
    expect(screen.getByText("workspace_network_denied")).toBeInTheDocument();
    expect(screen.getByText(riskProposal.scope_fingerprint)).toBeInTheDocument();

    await user.click(screen.getByRole("checkbox", {
      name: "I verified the command, every risk kind and target, snapshot bindings, and resource limits",
    }));
    await user.clear(screen.getByRole("spinbutton", { name: "Current-Run grant seconds" }));
    await user.type(screen.getByRole("spinbutton", { name: "Current-Run grant seconds" }), "120");
    await user.clear(screen.getByRole("spinbutton", { name: "Current-Run grant maximum uses" }));
    await user.type(screen.getByRole("spinbutton", { name: "Current-Run grant maximum uses" }), "2");
    await user.click(screen.getByRole("button", { name: "Grant exact scope to current Run" }));

    await waitFor(() => expect(review).toHaveBeenCalledTimes(1));
    expect(review.mock.calls[0]?.slice(0, 3)).toEqual([
      "run-1",
      "risk-escalation-1",
      {
        version: "host_command_review.v1",
        decision: "approve",
        authorization: "run_scope",
        reason: "Operator verified and approved the exact risk escalation scope",
        confirm_execution: true,
        grant_ttl_seconds: 120,
        grant_max_uses: 2,
      },
    ]);
    expect(await screen.findByText("Untrusted approved risk escalation evidence"))
      .toBeInTheDocument();
  });

  it("remains hidden without the host command control capability", () => {
    const client = { hasHostCommandProposalControl: false } as CyberAgentClient;
    const { container } = renderPanel(client);
    expect(container).toBeEmptyDOMElement();
  });

  it("keeps a saved approval with no execution result visible after an HTTP failure", async () => {
    const approved = { ...proposal, review: approvedReview() };
    const queue = vi.fn().mockResolvedValueOnce({ items: [proposal], page: { limit: 100 } })
      .mockResolvedValue({ items: [approved], page: { limit: 100 } });
    const review = vi.fn().mockRejectedValue(new Error("host command execution did not return a valid sealed result"));
    const client = { hasHostCommandProposalControl: true, hostCommandProposals: queue,
      reviewHostCommandProposal: review } as unknown as CyberAgentClient;
    vi.stubGlobal("confirm", vi.fn().mockReturnValue(true));
    renderPanel(client, "thread-1");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "Approve and execute once" }));
    expect(await screen.findByText("Approved; execution is unconfirmed. This does not establish whether the command ran or succeeded."))
      .toBeInTheDocument();
    expect(screen.getByText("host command execution did not return a valid sealed result")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve and execute once" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Refresh command records" }));
    await waitFor(() => expect(queue.mock.calls.length).toBeGreaterThanOrEqual(3));
    expect(review).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Approved; execution is unconfirmed. This does not establish whether the command ran or succeeded."))
      .toBeInTheDocument();
  });

  it.each([{ exit: 7, cancelled: false }, { exit: 0, cancelled: true }])(
    "retains an exact failed command receipt and reads saved output in the conversation: $exit/$cancelled", async ({ exit, cancelled }) => {
      const recorded = recordedProposal(exit, cancelled);
      const detail = vi.fn().mockResolvedValue({ ...recorded,
        untrusted_evidence: "UNTRUSTED HOST COMMAND RESULT\nstderr_begin\nactual command output\nstderr_end" });
      const review = vi.fn();
      const client = { hasHostCommandProposalControl: true,
        hostCommandProposals: vi.fn().mockResolvedValue({ items: [recorded], page: { limit: 100 } }),
        hostCommandProposal: detail, reviewHostCommandProposal: review } as unknown as CyberAgentClient;
      renderPanel(client, "thread-1");
      expect(await screen.findByText(`Execution result recorded, exit code ${exit}`)).toBeInTheDocument();
      if (cancelled) expect(screen.getByText("The command was cancelled.")).toBeInTheDocument();
      expect(detail).not.toHaveBeenCalled();
      await userEvent.setup().click(screen.getByRole("button", { name: "Read saved output" }));
      expect(await screen.findByText(/actual command output/)).toBeInTheDocument();
      expect(detail).toHaveBeenCalledWith("run-1", proposal.id, expect.any(AbortSignal));
      expect(review).not.toHaveBeenCalled();
    });

  it("retains the failed continuation notice after the saved review is refreshed", async () => {
    const denied = { ...proposal, review: { ...approvedReview(), decision: "deny", single_use_execution_authorized: false } };
    const queue = vi.fn().mockResolvedValueOnce({ items: [proposal], page: { limit: 100 } })
      .mockResolvedValue({ items: [denied], page: { limit: 100 } });
    const review = vi.fn().mockResolvedValue({ ...denied, continuation: { state: "failed",
      replayed: false, model_called: true, tool_called: false, error_code: "FAILED_PRECONDITION" } });
    const client = { hasHostCommandProposalControl: true, hostCommandProposals: queue,
      reviewHostCommandProposal: review } as unknown as CyberAgentClient;
    renderPanel(client, "thread-1");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Deny" }));
    await waitFor(() => expect(queue.mock.calls.length).toBeGreaterThanOrEqual(2));
    expect(screen.getByText("Review saved, but subsequent execution failed. Check the records and continue in this conversation."))
      .toBeInTheDocument();
    expect(screen.getByText("Proposal denied; no execution result.")).toBeInTheDocument();
    expect(screen.queryByText("Host command review failed")).not.toBeInTheDocument();
  });

  it("does not display saved output from a different execution binding", async () => {
    const recorded = recordedProposal(7, false);
    const detail = vi.fn().mockResolvedValue({ ...recorded, session_id: "other-session",
      untrusted_evidence: "wrong execution output" });
    const client = { hasHostCommandProposalControl: true,
      hostCommandProposals: vi.fn().mockResolvedValue({ items: [recorded], page: { limit: 100 } }),
      hostCommandProposal: detail } as unknown as CyberAgentClient;
    renderPanel(client, "thread-1");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Read saved output" }));
    expect(await screen.findByText("Saved output does not match this command receipt.")).toBeInTheDocument();
    expect(screen.queryByText("wrong execution output")).not.toBeInTheDocument();
  });

  it.each(["UX_Q_HOST_ACTUAL_OUTPUT \uFFFD", "UX_Q_HOST_ACTUAL_OUTPUT 中文"])(
    "explains damaged saved Host text without altering it or retrying execution: %s", async (output) => {
      const recorded = recordedProposal(7, false);
      const detail = vi.fn().mockResolvedValue({ ...recorded, untrusted_evidence: output });
      const review = vi.fn();
      const client = { hasHostCommandProposalControl: true,
        hostCommandProposals: vi.fn().mockResolvedValue({ items: [recorded], page: { limit: 100 } }),
        hostCommandProposal: detail, reviewHostCommandProposal: review } as unknown as CyberAgentClient;
      renderPanel(client, "thread-1");
      await userEvent.setup().click(await screen.findByRole("button", { name: "Read saved output" }));
      const saved = await screen.findByText(output);
      expect(saved.tagName).toBe("PRE");
      expect(saved.textContent).toBe(output);
      expect(Boolean(screen.queryByText("Some characters could not be decoded correctly; the displayed text may be incomplete.")))
        .toBe(output.includes("\uFFFD"));
      expect(screen.getByText("Execution result recorded, exit code 7")).toBeInTheDocument();
      expect(detail).toHaveBeenCalledTimes(1);
      expect(review).not.toHaveBeenCalled();
    });
});

function approvedReview() {
  return { id: "review-1", decision: "approve", reviewed_by: "http_control_operator", reason: "Exact command approved",
    single_use_execution_authorized: true, capability_grant: false, created_at: "2026-08-09T00:01:00Z" };
}

function recordedProposal(exit: number, cancelled: boolean) {
  return { ...proposal, review: approvedReview(),
    result: { id: "result-1", status: "failed", content_sha256: "e".repeat(64) },
    receipt: { request_id: "host-exec-1", exit_code: exit, cancelled, timed_out: false,
      stdout_truncated: false, stderr_truncated: false, output_limit_exceeded: false } };
}

function renderPanel(client: CyberAgentClient, threadID = "", compact = false) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false },
    mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}>
    <HostCommandProposalPanel client={client} runID="run-1" threadID={threadID} compact={compact} />
  </QueryClientProvider>);
}

it("keeps uncertain approvals and failed continuations actionable in the compact Inspector", async () => {
  const settled = { ...recordedProposal(7, false), id: "settled", purpose: "Settled command" };
  const uncertain = { ...proposal, id: "uncertain", purpose: "Uncertain command", review: approvedReview() };
  const failed = { ...recordedProposal(0, false), id: "failed-continuation", purpose: "Failed continuation",
    continuation: { state: "failed", replayed: false, model_called: true, tool_called: false } };
  const review = vi.fn();
  const client = { hasHostCommandProposalControl: true, reviewHostCommandProposal: review,
    hostCommandProposals: vi.fn().mockResolvedValue({ items: [settled, uncertain, failed], page: { limit: 100 } }) } as unknown as CyberAgentClient;
  renderPanel(client, "thread-1", true);
  expect(await screen.findByText("Uncertain command")).toBeInTheDocument();
  expect(screen.getByText("Failed continuation")).toBeInTheDocument();
  expect(screen.queryByText("Settled command")).not.toBeInTheDocument();
  expect(screen.getByText("Review saved, but subsequent execution failed. Check the records and continue in this conversation.")).toBeInTheDocument();
  expect(review).not.toHaveBeenCalled();
});
