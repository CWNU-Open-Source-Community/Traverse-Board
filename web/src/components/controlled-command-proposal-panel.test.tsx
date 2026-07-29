import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import { ControlledCommandProposalPanel } from "./controlled-command-proposal-panel";

const proposal = {
  id: "command-proposal-1",
  protocol_version: "controlled_command_proposal.v1",
  policy_version: "controlled_command_proposal_policy.v1",
  run_id: "run-1",
  mission_id: "mission-1",
  session_id: "session-1",
  workspace_id: "workspace-1",
  kind: "git-status",
  timeout_milliseconds: 30_000,
  purpose: "Inspect the current repository state",
  permission_mode: "conservative",
  permission_revision: 1,
  operator_review_required: true,
  instruction_authorized: false,
  execution_authorized: false,
  capability_grant: false,
  fingerprint: "a".repeat(64),
  created_at: "2026-07-29T00:00:00Z",
  evidence_instruction_authorized: false,
};

describe("ControlledCommandProposalPanel", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("requires confirmation and sends only the exact structured review", async () => {
    const user = userEvent.setup();
    const reviewed = {
      ...proposal,
      review: { id: "review-1", decision: "approve", reviewed_by: "http_control_operator",
        reason: "Operator approved the exact fixed Go command",
        single_use_execution_authorized: true, capability_grant: false,
        created_at: "2026-07-29T00:01:00Z" },
      result: { id: "result-1", status: "completed", source_kind: "go_command_result",
        source_ref: "message-1", content_sha256: "b".repeat(64),
        instruction_authorized: false, raw_output_persisted: false,
        automatic_retry_allowed: false, created_at: "2026-07-29T00:01:01Z" },
      untrusted_evidence: "UNTRUSTED GO COMMAND RESULT\nstdout_begin\nclean\nstdout_end",
    };
    const review = vi.fn().mockResolvedValue(reviewed);
    const client = {
      hasControlledCommandProposalControl: true,
      controlledCommandProposals: vi.fn().mockResolvedValue({
        items: [proposal], page: { limit: 100 }, requestID: "request-1",
      }),
      reviewControlledCommandProposal: review,
    } as unknown as CyberAgentClient;
    vi.stubGlobal("confirm", vi.fn().mockReturnValue(true));
    renderPanel(client);

    expect(await screen.findByText("Git status")).toBeInTheDocument();
    expect(screen.queryByText(/powershell -Command/i)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Approve and execute" }));
    await waitFor(() => expect(review).toHaveBeenCalledTimes(1));
    expect(review.mock.calls[0]?.slice(0, 3)).toEqual([
      "run-1",
      "command-proposal-1",
      {
        version: "controlled_command_proposal_review.v1",
        decision: "approve",
        reason: "Operator approved the exact fixed Go command",
        confirm_execution: true,
      },
    ]);
    expect(await screen.findByText("Untrusted command evidence")).toBeInTheDocument();
    expect(screen.getByText(/stdout_begin/)).toBeInTheDocument();
  });

  it("does not execute when the operator cancels confirmation", async () => {
    const user = userEvent.setup();
    const review = vi.fn();
    const client = {
      hasControlledCommandProposalControl: true,
      controlledCommandProposals: vi.fn().mockResolvedValue({
        items: [proposal], page: { limit: 100 }, requestID: "request-1",
      }),
      reviewControlledCommandProposal: review,
    } as unknown as CyberAgentClient;
    vi.stubGlobal("confirm", vi.fn().mockReturnValue(false));
    renderPanel(client);
    await user.click(await screen.findByRole("button", { name: "Approve and execute" }));
    expect(review).not.toHaveBeenCalled();
  });
});

function renderPanel(client: CyberAgentClient) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false },
    mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}>
    <ControlledCommandProposalPanel client={client} runID="run-1" />
  </QueryClientProvider>);
}
