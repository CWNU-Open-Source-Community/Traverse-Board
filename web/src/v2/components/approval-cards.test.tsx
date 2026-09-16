import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CyberAgentClient } from "../../api/client";
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

function renderCards(item: ApprovalQueueItemView, previewOverrides = {}) {
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
  const approvalPreview = vi.fn().mockResolvedValue({
    protocol_version: "approval_queue.v1", run_id: "run-1", approval_id: item.id,
    proposal_id: item.proposal_id, tool_name: item.tool_name, workspace_id: item.workspace_id,
    effect: item.tool_name === "web_fetch" ? "fetch_public_https" : "dry_run",
    working_directory: item.tool_name === "web_fetch" ? "" : ".",
    fields: item.tool_name === "web_fetch"
      ? [{ name: "url", value: item.canonical_url }]
      : [{ name: "command", value: "echo inspection-only" }],
    source_current: true, redacted: false, truncated: false, ...previewOverrides,
  });
  const client = { hasApprovalControl: true, approvalQueue, decideApproval, approvalPreview,
  } as unknown as CyberAgentClient;
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  render(<QueryClientProvider client={queryClient}>
    <V2ApprovalCards client={client} runID="run-1" threadID="thread-1" />
  </QueryClientProvider>);
  return { decideApproval, approvalPreview };
}

describe("V2ApprovalCards", () => {
  it("renders a real create-file preview through the strict client and keeps approval in diff review", async () => {
    const item = pending({ id: "approval-20260910072057-6583f54ed917",
      proposal_id: "edit-99e4b9e0fc83e37efdda6745e8835851", workspace_id: "workspace-1",
      tool_name: "create_file", action_class: "workspace_write", allowed_actions: [],
      canonical_url: undefined, exact_target: undefined });
    const preview = { protocol_version: "approval_queue.v1", run_id: "run-1", approval_id: item.id,
      proposal_id: item.proposal_id, tool_name: "create_file", workspace_id: item.workspace_id,
      effect: "file_review_required", working_directory: ".", fields: [
        { name: "operation", value: "create" }, { name: "path", value: "package.json" }],
      source_current: true, redacted: false, truncated: false };
    const queue = { protocol_version: "approval_queue.v1", run_id: "run-1", items: [item],
      truncated: false, process_execution_enabled: false, session_grant_created: false, capability_grant: false };
    const fetchMock = vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify({ version: "api.v1",
      request_id: "preview-create", data: String(url).endsWith("/preview") ? preview : queue }),
      { status: 200, headers: { "Content-Type": "application/json" } })));
    vi.stubGlobal("fetch", fetchMock);
    const client = new CyberAgentClient("test-read", "/api/v1", "test-control");
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <V2ApprovalCards client={client} runID="run-1" threadID="thread-1" />
    </QueryClientProvider>);
    expect(await screen.findByText("package.json")).toBeInTheDocument();
    expect(screen.getByText(/批准、差异审阅和写入请使用任务的「审阅改动」入口/)).toBeInTheDocument();
    expect(screen.queryByText("无法核对操作内容，暂不能批准。")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "仅批准一次" })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.every(([url]) => String(url).includes("/runs/run-1/approvals"))).toBe(true);
  });

  it("offers one-time, conversation, and deny choices for a new public HTTPS host", async () => {
    const user = userEvent.setup();
    const { decideApproval } = renderCards(pending());

    expect(await screen.findByText("允许读取这个网站？")).toBeInTheDocument();
    expect(screen.getByText("arxiv.org")).toBeInTheDocument();
    expect(await screen.findByText("https://arxiv.org/abs/2608.13637")).toBeInTheDocument();
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

    expect(await screen.findByText("批准这次模拟执行？")).toBeInTheDocument();
    expect(screen.getByText("echo inspection-only")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "批准模拟一次" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "本对话允许" })).not.toBeInTheDocument();
  });

  it("blocks stale previews while keeping denial available", async () => {
    const { decideApproval } = renderCards(pending(), { source_current: false });
    expect(await screen.findByText("操作已变化或不再等待批准。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "允许一次" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "本对话允许" })).toBeDisabled();
    await userEvent.setup().click(screen.getByRole("button", { name: "拒绝" }));
    await waitFor(() => expect(decideApproval).toHaveBeenCalledWith("run-1", pending().id,
      { version: "approval_control.v1", action: "deny" }, expect.any(String)));
  });

  it("requires a matching proposal preview and offers retry after a loading error", async () => {
    const { approvalPreview } = renderCards(pending(), { proposal_id: "another-proposal" });
    expect(await screen.findByText("无法核对操作内容，暂不能批准。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "允许一次" })).toBeDisabled();
    await userEvent.setup().click(screen.getByRole("button", { name: "重试操作预览" }));
    await waitFor(() => expect(approvalPreview).toHaveBeenCalledTimes(2));
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
