import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import type { APIClient } from "../api/client";
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

it("keeps history visible without any command review actions", async () => {
 const client={controlledCommandProposals:vi.fn().mockResolvedValue({items:[proposal]})} as unknown as APIClient;
 renderPanel(client);
 expect(await screen.findByText(proposal.purpose)).toBeInTheDocument();
 expect(screen.queryByRole("button",{name:/approve|deny|execute/i})).not.toBeInTheDocument();
 expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});

function renderPanel(client: APIClient) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false },
    mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}>
    <ControlledCommandProposalPanel client={client} runID="run-1" />
  </QueryClientProvider>);
}
