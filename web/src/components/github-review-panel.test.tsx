import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import type { GitHubReviewWriteReviewResultView } from "../api/types";
import { standardCodeDeliveryFixture } from "../test/standard-code-delivery";
import { GitHubReviewPanel } from "./github-review-panel";

const oid = "1".repeat(40);
const digest = "a".repeat(64);
const now = "2026-08-21T10:00:00Z";

function connection() {
  return {
    protocol_version: "github-review-connection.v1", id: "connection-1",
    repository: { host: "github.com", owner: "acme", name: "widget",
      full_name: "acme/widget", private: false },
    credential: { name: "prayu-github-app", kind: "github_app_device" },
    client_id: "Iv1.client", network: { host: "github.com", api_host: "api.github.com",
      oauth_host: "github.com", allowed_log_hosts: [], read_enabled: true,
      write_enabled: true },
    enabled: true, generation: 1, created_at: now, updated_at: now,
  };
}

function projection() {
  return {
    protocol_version: "github-review-api.v1", run_id: "run-1", connection: connection(),
    credential: { protocol_version: "github-review-provider.v1",
      credential: connection().credential, store_kind: "test", store_available: true,
      configured: true, refreshable: true },
    snapshots: [{ protocol_version: "github-review-snapshot.v1", id: "snapshot-1",
      identity: { repository: connection().repository, number: 118, node_id: "PR_node",
        state: "open", merged: false, draft: false, base_ref: "main", base_sha: oid,
        head_ref: "feature", head_sha: "2".repeat(40), merge_base_sha: oid,
        updated_at: now },
      capability: { protocol_version: "github-review-capability.v1", generation: digest,
        api_host: "api.github.com", api_version: "2026-03-10", account_login: "octocat",
        installation_id: 1, repository: connection().repository,
        credential: connection().credential, permissions: { pull_requests: "write" },
        read: true, reply: true, resolve: true, review: true, request_reviewer: true,
        push: false, logs: false, captured_at: now },
      title: { text: "Review provider", truncated: false, original_bytes: 15 },
      body: { text: "", truncated: false, original_bytes: 0 }, author: "octocat",
      requested_reviewers: [], files: [], reviews: [], threads: [], loose_comments: [],
      check_suites: [], check_runs: [], jobs: [], artifacts: [], pagination: [],
      state: "verified", omissions: [], fingerprint: digest, fetched_at: now }],
    evidence: [], writes: [], standard_code_delivery: standardCodeDeliveryFixture(),
  };
}

describe("GitHubReviewPanel", () => {
  it("retains an exact approved write across repository-panel remounts", async () => {
    const user = userEvent.setup();
    const onOpenDelivery = vi.fn();
    const retained = { protocol_version: "github-review-api.v1",
      preview: { protocol_version: "github-review-write.v1", operation: "submit_review",
        approval_fingerprint: digest }, operation: { id: "write-1" },
      approval: { ID: "approval-1" }, replayed: false };
    const executeGitHubWrite = vi.fn().mockResolvedValue({ protocol_version: "github-review-api.v1",
      operation: { id: "write-1" }, receipt: { status: "succeeded" }, replayed: false });
    renderPanel({ hasGitHubReviewControl: true,
      githubReviewConnections: vi.fn().mockResolvedValue([{ connection: connection(),
        credential: projection().credential }]),
      githubReviewProjection: vi.fn().mockResolvedValue(projection()),
      executeGitHubWrite } as unknown as CyberAgentClient,
    retained as unknown as GitHubReviewWriteReviewResultView, onOpenDelivery);

    expect(await screen.findByText(digest)).toBeInTheDocument();
    expect(screen.getByText("Delivery truth")).toBeInTheDocument();
    expect(screen.getByText("f".repeat(64))).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open delivery" }));
    expect(onOpenDelivery).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Execute approved write" }));
    await waitFor(() => expect(executeGitHubWrite).toHaveBeenCalledWith(
      "run-1", "write-1", "approval-1"));
  });
});

function renderPanel(client: CyberAgentClient,
  retainedReview?: GitHubReviewWriteReviewResultView | null,
  onOpenDelivery: () => void = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  return render(<QueryClientProvider client={queryClient}>
    <GitHubReviewPanel client={client} onOpenApprovals={vi.fn()}
      onOpenDelivery={onOpenDelivery}
      retainedReview={retainedReview} runID="run-1" />
  </QueryClientProvider>);
}
