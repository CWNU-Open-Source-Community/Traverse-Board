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
      "High risk: non-sandboxed host command with host network access",
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

  it("remains hidden without the host command control capability", () => {
    const client = { hasHostCommandProposalControl: false } as CyberAgentClient;
    const { container } = renderPanel(client);
    expect(container).toBeEmptyDOMElement();
  });
});

function renderPanel(client: CyberAgentClient) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false },
    mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}>
    <HostCommandProposalPanel client={client} runID="run-1" />
  </QueryClientProvider>);
}
