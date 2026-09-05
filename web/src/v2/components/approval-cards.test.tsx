import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../../api/client";
import type { ApprovalQueueItemView } from "../../api/types";
import { V2ApprovalCards } from "./approval-cards";

function pending(overrides: Partial<ApprovalQueueItemView> = {}): ApprovalQueueItemView {
  return {
    action_class: "public_https_fetch",
    allowed_actions: ["approve_once", "approve_for_thread", "deny"],
    canonical_url: "https://arxiv.org/abs/2608.13637",
    capability_grant: false,
    created_at: "2026-09-02T00:00:00Z",
    exact_target: "arxiv.org",
    id: "approval-web-fetch-1",
    mode: "per_call",
    process_execution_enabled: false,
    proposal_id: "web-fetch-authorization-1",
    run_id: "run-1",
    session_id: "session-1",
    status: "pending",
    tool_name: "web_fetch",
    updated_at: "2026-09-02T00:00:00Z",
    version: 1,
    workspace_id: "",
    ...overrides,
  };
}

function renderCards(item: ApprovalQueueItemView) {
  const approvalQueue = vi.fn().mockResolvedValue({
    protocol_version: "approval_queue.v1", run_id: "run-1", items: [item],
    truncated: false, process_execution_enabled: false,
    session_grant_created: false, capability_grant: false,
  });
  const decideApproval = vi.fn().mockResolvedValue({
    version: "approval_control.v1", run_id: "run-1", approval_id: item.id,
    proposal_id: item.proposal_id, tool_name: item.tool_name,
    action: "approve_for_thread", status: "approved", replayed: false,
    process_execution_enabled: false, shell_execution_enabled: false,
    docker_execution_enabled: false, workspace_write_applied: false,
    session_grant_created: false, capability_grant: false,
    execution_resumed: true, retry_completed: true,
  });
  const client = { hasApprovalControl: true, approvalQueue, decideApproval,
  } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  render(<QueryClientProvider client={queryClient}>
    <V2ApprovalCards client={client} runID="run-1" threadID="thread-1" />
  </QueryClientProvider>);
  return { decideApproval };
}

describe("V2ApprovalCards", () => {
  it("offers one-time, conversation, and deny choices for a new public HTTPS host", async () => {
    const user = userEvent.setup();
    const { decideApproval } = renderCards(pending());

    expect(await screen.findByText("允许读取这个网站？")).toBeInTheDocument();
    expect(screen.getByText("arxiv.org")).toBeInTheDocument();
    expect(screen.getByText("https://arxiv.org/abs/2608.13637")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "允许一次" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "拒绝" })).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "本对话允许" }));
    await waitFor(() => expect(decideApproval).toHaveBeenCalledWith(
      "run-1", "approval-web-fetch-1", {
        version: "approval_control.v1", action: "approve_for_thread",
      }, expect.stringMatching(/^v2-approval-/u),
    ));
  });

  it("does not offer a conversation-wide grant for ordinary approvals", async () => {
    renderCards(pending({
      action_class: "shell", allowed_actions: ["approve_once", "deny"],
      canonical_url: undefined, exact_target: undefined, tool_name: "shell",
    }));

    expect(await screen.findByText("需要你的批准")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "仅批准一次" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "本对话允许" })).not.toBeInTheDocument();
  });

  it("replays a persisted web decision without offering a different choice", async () => {
    const user = userEvent.setup();
    const recovered = { ...pending(), allowed_actions: ["approve_for_thread"],
      status: "approved" } as unknown as ApprovalQueueItemView;
    const { decideApproval } = renderCards(recovered);

    expect(await screen.findByText("恢复上次网页读取")).toBeInTheDocument();
    expect(screen.getByText("已允许，等待恢复")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "拒绝" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "允许一次" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "继续恢复" }));
    await waitFor(() => expect(decideApproval).toHaveBeenCalledWith(
      "run-1", "approval-web-fetch-1", {
        version: "approval_control.v1", action: "approve_for_thread",
      }, expect.stringMatching(/^v2-approval-/u),
    ));
  });
});
