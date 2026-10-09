import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { APIRequestError, type APIClient } from "../api/client";
import type { GitHubReviewConnectionView, GitHubReviewWriteReviewResultView, GitHubReviewWriteSpecView } from "../api/types";
import { standardCodeDeliveryFixture } from "../test/standard-code-delivery";
import { GitHubReviewPanel } from "./github-review-panel";

const oid = "1".repeat(40);
const digest = "a".repeat(64);
const now = "2026-08-21T10:00:00Z";

function connection(): GitHubReviewConnectionView {
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

function projection(selected = connection()) {
  return {
    protocol_version: "github-review-api.v1", run_id: "run-1", connection: selected,
    credential: { protocol_version: "github-review-provider.v1",
      credential: selected.credential, store_kind: "test", store_available: true,
      configured: true, refreshable: true },
    snapshots: [{ protocol_version: "github-review-snapshot.v1", id: "snapshot-1",
      identity: { repository: selected.repository, number: 118, node_id: "PR_node",
        state: "open", merged: false, draft: false, base_ref: "main", base_sha: oid,
        head_ref: "feature", head_sha: "2".repeat(40), merge_base_sha: oid,
        updated_at: now },
      capability: { protocol_version: "github-review-capability.v1", generation: digest,
        api_host: "api.github.com", api_version: "2026-03-10", account_login: "octocat",
        installation_id: 1, repository: selected.repository,
        credential: selected.credential, permissions: { pull_requests: "write" },
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

function credentialView(selected = connection(), configured = true) {
  return { protocol_version: "github-review-api.v1", connection: selected,
    credential: { ...projection(selected).credential, configured } };
}

function threadProjection(resolved = false) {
  const value = projection();
  return { ...value, snapshots: [{ ...value.snapshots[0], threads: [{
    id: "thread-current", resolved, outdated: false, path: "src/pagination.ts", line: 7,
    comments: [{ node_id: "comment-1", author: "reviewer", created_at: now, updated_at: now,
      body: { text: "Please retain the final page.", truncated: false, original_bytes: 29 },
      position: { path: "src/pagination.ts", line: 7, side: "RIGHT" } }],
  }] }] };
}

function reviewedOperation(spec: GitHubReviewWriteSpecView) {
  const value = reviewedWrite();
  const nextPreview = { ...value.preview, operation: spec.operation, target_id: spec.target_id,
    review_event: spec.review_event, body_summary: spec.body, reviewers: spec.reviewers };
  return { ...value, preview: nextPreview, operation: { ...value.operation, preview: nextPreview } };
}

function reviewedWrite(selected = connection(), runID = "run-1"): GitHubReviewWriteReviewResultView {
  const preview = { protocol_version: "github-review-write.v1", operation: "submit_review",
    approval_fingerprint: digest, identity: projection(selected).snapshots[0].identity,
    credential: selected.credential, capability_generation: digest };
  return { protocol_version: "github-review-api.v1", preview,
    operation: { id: "write-1", run_id: runID, connection_id: selected.id, preview },
    approval: { ID: "approval-1" }, replayed: false } as GitHubReviewWriteReviewResultView;
}

function mockClient(selected = [connection()], overrides: Partial<APIClient> = {}): APIClient {
  return { hasGitHubReviewControl: true,
    githubReviewConnections: vi.fn().mockImplementation(async () => selected.map((item) => credentialView(item))),
    githubReviewCredential: vi.fn().mockImplementation(async (id: string) => credentialView(selected.find((item) => item.id === id))),
    githubReviewProjection: vi.fn().mockImplementation(async (_runID: string, id: string, number: number) => {
      const value = projection(selected.find((item) => item.id === id));
      return { ...value, snapshots: value.snapshots.filter((item) => !number || item.identity.number === number) };
    }),
    reviewGitHubWrite: vi.fn().mockImplementation(async (runID: string, body: { connection_id: string }) =>
      reviewedWrite(selected.find((item) => item.id === body.connection_id), runID)),
    ...overrides } as unknown as APIClient;
}

async function createPreview(user: ReturnType<typeof userEvent.setup>) {
  await waitFor(() => expect(screen.getByRole("button", { name: "Create exact preview" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Create exact preview" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Execute approved write" })).toBeEnabled());
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise; reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

describe("GitHubReviewPanel", () => {
  it.each([
    { operation: "reply", resolved: false },
    { operation: "resolve", resolved: false },
    { operation: "unresolve", resolved: true },
  ])("previews $operation for a discussion from the exact current PR", async ({ operation, resolved }) => {
    const user = userEvent.setup();
    const source = threadProjection(resolved);
    const reviewGitHubWrite = vi.fn().mockImplementation(async (_runID: string, body: { spec: GitHubReviewWriteSpecView }) =>
      reviewedOperation(body.spec));
    const executeGitHubWrite = vi.fn();
    renderPanel(mockClient([connection()], { githubReviewProjection: vi.fn().mockResolvedValue(source),
      reviewGitHubWrite, executeGitHubWrite }));
    await user.selectOptions(await screen.findByLabelText("Review action"), operation);
    expect(screen.getByRole("button", { name: "Create exact preview" })).toBeDisabled();
    await user.selectOptions(screen.getByLabelText("Target discussion"), "thread-current");
    if (operation === "reply") await user.type(screen.getByLabelText("Reply body"), "Fixed with a boundary test.");
    await user.click(screen.getByRole("button", { name: "Create exact preview" }));
    await waitFor(() => expect(reviewGitHubWrite).toHaveBeenCalledTimes(1));
    const request = reviewGitHubWrite.mock.calls[0][1];
    expect(request).toMatchObject({ connection_id: "connection-1", snapshot_id: "snapshot-1",
      spec: { operation, target_id: "thread-current", reviewers: [],
        identity: source.snapshots[0].identity, capability_generation: digest } });
    expect(request.spec.review_event).toBeUndefined();
    expect(request.spec.body).toBe(operation === "reply" ? "Fixed with a boundary test." : undefined);
    expect(await screen.findByRole("button", { name: "Open approvals" })).toBeEnabled();
    expect(executeGitHubWrite).not.toHaveBeenCalled();
  });

  it("retains a reply across approval navigation when overall review permission is absent", async () => {
    const user = userEvent.setup();
    const source = threadProjection();
    source.snapshots[0].capability.review = false;
    const reviewGitHubWrite = vi.fn().mockImplementation(async (_runID: string, body: { spec: GitHubReviewWriteSpecView }) =>
      reviewedOperation(body.spec));
    const executeGitHubWrite = vi.fn().mockResolvedValue({ receipt: { status: "succeeded" } });
    const client = mockClient([connection()], { githubReviewProjection: vi.fn().mockResolvedValue(source),
      reviewGitHubWrite, executeGitHubWrite });
    const retainedChanges = vi.fn();
    const first = renderPanel(client, undefined, vi.fn(), retainedChanges);
    await user.selectOptions(await screen.findByLabelText("Review action"), "reply");
    await user.selectOptions(screen.getByLabelText("Target discussion"), "thread-current");
    await user.type(screen.getByLabelText("Reply body"), "Boundary fixed.");
    await createPreview(user);
    const retained = retainedChanges.mock.calls.at(-1)![0];
    first.unmount();
    renderPanel(client, retained);
    await waitFor(() => expect(screen.getByRole("button", { name: "Execute approved write" })).toBeEnabled());
    expect(screen.getByText("Boundary fixed.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Execute approved write" }));
    await waitFor(() => expect(executeGitHubWrite).toHaveBeenCalledWith("run-1", "write-1", "approval-1"));
  });

  it("normalizes reviewer names and requires a separate approval before requesting them", async () => {
    const user = userEvent.setup();
    const reviewGitHubWrite = vi.fn().mockImplementation(async (_runID: string, body: { spec: GitHubReviewWriteSpecView }) =>
      reviewedOperation(body.spec));
    const executeGitHubWrite = vi.fn();
    renderPanel(mockClient([connection()], { reviewGitHubWrite, executeGitHubWrite }));
    await user.selectOptions(await screen.findByLabelText("Review action"), "request_reviewer");
    expect(screen.getByRole("button", { name: "Create exact preview" })).toBeDisabled();
    await user.type(screen.getByLabelText("Reviewer usernames"), "zoe, ada zoe");
    await createPreview(user);
    expect(reviewGitHubWrite.mock.calls[0][1].spec).toMatchObject({ operation: "request_reviewer", reviewers: ["ada", "zoe"] });
    expect(reviewGitHubWrite.mock.calls[0][1].spec.body).toBeUndefined();
    expect(executeGitHubWrite).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText("Reviewer usernames"), ", @invalid");
    expect(screen.getByRole("button", { name: "Create exact preview" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
  });

  it("shows discussion evidence for a read-only connection without exposing write controls", async () => {
    const selected = { ...connection(), network: { ...connection().network, write_enabled: false } };
    renderPanel(mockClient([selected], { githubReviewProjection: vi.fn().mockResolvedValue({ ...threadProjection(), connection: selected }) }));
    expect(await screen.findByText("Please retain the final page.")).toBeInTheDocument();
    expect(screen.queryByLabelText("Review action")).not.toBeInTheDocument();
  });

  it("does not offer resolve for an already resolved discussion or unsupported reviewer requests", async () => {
    const user = userEvent.setup();
    const source = threadProjection(true);
    source.snapshots[0].capability.request_reviewer = false;
    renderPanel(mockClient([connection()], { githubReviewProjection: vi.fn().mockResolvedValue(source) }));
    await user.selectOptions(await screen.findByLabelText("Review action"), "resolve");
    expect(screen.getByRole("option", { name: "Request reviewers" })).toBeDisabled();
    expect(screen.getByRole("option", { name: /src\/pagination.ts:7/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Create exact preview" })).toBeDisabled();
  });

  it("retains the original exact preview across approval-panel unmount and remount", async () => {
    const user = userEvent.setup();
    const onOpenDelivery = vi.fn();
    const onRetainedReviewChange = vi.fn();
    const executeGitHubWrite = vi.fn().mockResolvedValue({ protocol_version: "github-review-api.v1",
      operation: { id: "write-1" }, receipt: { status: "succeeded" }, replayed: false });
    const client = mockClient([connection()], { executeGitHubWrite });
    const first = renderPanel(client, undefined, onOpenDelivery, onRetainedReviewChange);
    await createPreview(user);
    const retained = onRetainedReviewChange.mock.calls.at(-1)![0] as GitHubReviewWriteReviewResultView;
    await user.click(screen.getByRole("button", { name: "Open approvals" }));
    first.unmount();
    renderPanel(client, retained, onOpenDelivery);

    expect(await screen.findByText(digest)).toBeInTheDocument();
    expect(screen.getByLabelText("PR number")).toHaveValue(118);
    expect(screen.getByText("Delivery truth")).toBeInTheDocument();
    expect(screen.getByText("f".repeat(64))).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open delivery" }));
    expect(onOpenDelivery).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Execute approved write" }));
    await waitFor(() => expect(executeGitHubWrite).toHaveBeenCalledWith(
      "run-1", "write-1", "approval-1"));
  });

  it("edits the selected connection with its generation and preserves existing settings", async () => {
    const user = userEvent.setup();
    const selected = { ...connection(), generation: 7,
      repository: { ...connection().repository, private: true },
      network: { ...connection().network, allowed_log_hosts: ["logs.acme.example"] } };
    const configureGitHubReview = vi.fn().mockImplementation(async (body) => ({
      protocol_version: "github-review-api.v1", replayed: false,
      connection: { ...selected, credential: body.credential, generation: body.expected_generation + 1,
        network: { ...selected.network, write_enabled: body.write_enabled } },
    }));
    renderPanel(mockClient([selected], { configureGitHubReview }));

    expect(await screen.findByDisplayValue("acme/widget")).toHaveAttribute("readonly");
    expect(screen.getByLabelText("Credential reference")).toHaveValue("prayu-github-app");
    expect(screen.getByLabelText("GitHub App Client ID")).toHaveValue("Iv1.client");
    await user.click(screen.getByRole("checkbox", { name: "Allow per-call approved write-back" }));
    await user.click(screen.getByRole("button", { name: "Update connection" }));
    await waitFor(() => expect(configureGitHubReview).toHaveBeenCalledWith({
      connection_id: selected.id, expected_generation: 7, repository: selected.repository,
      credential: selected.credential, client_id: selected.client_id,
      allowed_log_hosts: ["logs.acme.example"], write_enabled: false, enabled: true,
    }));
    expect(await screen.findByText("Connection settings saved.")).toBeInTheDocument();
    expect(screen.getByText("Editing acme/widget; settings version 8.")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Allow per-call approved write-back" }));
    await user.click(screen.getByRole("button", { name: "Update connection" }));
    await waitFor(() => expect(configureGitHubReview).toHaveBeenLastCalledWith(expect.objectContaining({ expected_generation: 8 })));
  });

  it("preserves non-device credential kind and disabled connection configuration", async () => {
    const user = userEvent.setup();
    const selected = { ...connection(), enabled: false, client_id: undefined,
      credential: { name: "existing-pat", kind: "fine_grained_pat" } };
    const configureGitHubReview = vi.fn().mockResolvedValue({ connection: { ...selected, generation: 2 } });
    renderPanel(mockClient([selected], { configureGitHubReview }));
    await screen.findByDisplayValue("existing-pat");
    expect(screen.queryByRole("button", { name: "Device sign-in" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("GitHub App Client ID")).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Update connection" }));
    await waitFor(() => expect(configureGitHubReview).toHaveBeenCalledWith(expect.objectContaining({
      credential: selected.credential, client_id: undefined, enabled: false, expected_generation: 1,
    })));
  });

  it("keeps explicit new-connection mode and creates with generation zero", async () => {
    const user = userEvent.setup();
    const configureGitHubReview = vi.fn().mockResolvedValue({ connection: { ...connection(), id: "new-connection" } });
    renderPanel(mockClient([connection()], { configureGitHubReview }));
    await screen.findByDisplayValue("acme/widget");
    await user.selectOptions(screen.getByLabelText("GitHub connection"), "");
    await user.click(screen.getByRole("button", { name: "Refresh connection and remote PR" }));
    expect(screen.getByLabelText("GitHub connection")).toHaveValue("");
    expect(screen.getByLabelText("Repository")).toHaveValue("");
    await user.type(screen.getByLabelText("Repository"), "other/repository");
    await user.type(screen.getByLabelText("GitHub App Client ID"), "Iv1.new");
    await user.click(screen.getByRole("button", { name: "Create connection" }));
    await waitFor(() => expect(configureGitHubReview).toHaveBeenCalledWith({
      repository: { host: "github.com", owner: "other", name: "repository", full_name: "other/repository", private: false },
      credential: { name: "prayu-github-app", kind: "github_app_device" }, client_id: "Iv1.new",
      allowed_log_hosts: [], write_enabled: false, enabled: true, expected_generation: 0,
    }));
  });

  it("preserves a conflicted draft until the operator reloads the latest version", async () => {
    const user = userEvent.setup();
    const latest = { ...connection(), generation: 9, client_id: "Iv1.latest" };
    const configureGitHubReview = vi.fn().mockRejectedValueOnce(new APIRequestError("generation mismatch", "CONFLICT", 409))
      .mockResolvedValue({ connection: { ...latest, generation: 10 } });
    const githubReviewCredential = vi.fn().mockResolvedValueOnce(credentialView()).mockResolvedValue(credentialView(latest));
    renderPanel(mockClient([connection()], { configureGitHubReview, githubReviewCredential }));
    await screen.findByDisplayValue("acme/widget");
    await user.clear(screen.getByLabelText("GitHub App Client ID"));
    await user.type(screen.getByLabelText("GitHub App Client ID"), "Iv1.draft");
    await user.click(screen.getByRole("button", { name: "Update connection" }));
    expect(await screen.findByText(/Your draft is preserved/)).toBeInTheDocument();
    expect(screen.getByLabelText("GitHub App Client ID")).toHaveValue("Iv1.draft");
    expect(screen.queryByText("Connection settings saved.")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update connection" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Reload latest settings" }));
    expect(await screen.findByDisplayValue("Iv1.latest")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Update connection" }));
    await waitFor(() => expect(configureGitHubReview).toHaveBeenLastCalledWith(expect.objectContaining({ expected_generation: 9 })));
  });

  it.each(["success", "failure"])("does not replace a newly selected connection with an old save %s", async (result) => {
    const user = userEvent.setup();
    const second = { ...connection(), id: "connection-2", client_id: "Iv1.second",
      repository: { ...connection().repository, owner: "other", full_name: "other/widget" } };
    const request = deferred<{ connection: GitHubReviewConnectionView }>();
    const configureGitHubReview = vi.fn().mockReturnValue(request.promise);
    renderPanel(mockClient([connection(), second], { configureGitHubReview }));
    await screen.findByDisplayValue("acme/widget");
    await user.click(screen.getByRole("button", { name: "Update connection" }));
    await user.selectOptions(screen.getByLabelText("GitHub connection"), second.id);
    await act(async () => {
      if (result === "success") request.resolve({ connection: { ...connection(), generation: 2 } });
      else request.reject(new Error("old save failed"));
    });
    expect(screen.getByLabelText("GitHub connection")).toHaveValue(second.id);
    expect(screen.getByLabelText("GitHub App Client ID")).toHaveValue("Iv1.second");
    expect(screen.queryByText("Connection settings saved.")).not.toBeInTheDocument();
    expect(screen.queryByText("old save failed")).not.toBeInTheDocument();
  });

  it("confirms local credential deletion, supports cancellation, and refreshes signed-out state", async () => {
    const user = userEvent.setup();
    let signedIn = true;
    const disconnectGitHubReview = vi.fn().mockImplementation(async () => {
      signedIn = false; return credentialView(connection(), false);
    });
    const onRetainedReviewChange = vi.fn();
    renderPanel(mockClient([connection()], {
      githubReviewCredential: vi.fn().mockImplementation(async () => credentialView(connection(), signedIn)),
      githubReviewConnections: vi.fn().mockImplementation(async () => [credentialView(connection(), signedIn)]),
      disconnectGitHubReview,
      beginGitHubReviewDeviceFlow: vi.fn().mockResolvedValue({ session_id: "device-1", user_code: "DEVICE-CODE", verification_uri: "https://github.com/login/device" }),
    }), undefined, vi.fn(), onRetainedReviewChange);
    await createPreview(user);
    await user.click(screen.getByRole("button", { name: "Device sign-in" }));
    expect(await screen.findByText("DEVICE-CODE")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete local credential…" }));
    expect(screen.getByRole("dialog", { name: "Delete local GitHub credential" })).toBeInTheDocument();
    expect(screen.getByText(/this does not revoke authorization on GitHub/)).toBeInTheDocument();
    expect(disconnectGitHubReview).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByText("DEVICE-CODE")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete local credential…" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete local credential" }));
    await waitFor(() => expect(disconnectGitHubReview).toHaveBeenCalledWith("connection-1"));
    expect(await screen.findByText("Local credential deleted for this connection.")).toBeInTheDocument();
    expect(await screen.findByText("No local credential is configured.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete local credential…" })).toBeDisabled();
    expect(screen.queryByText("DEVICE-CODE")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
    expect(onRetainedReviewChange).toHaveBeenCalledWith(null);
    expect(screen.getByLabelText("Repository")).toHaveValue("acme/widget");
  });

  it("reports deletion failure without claiming the credential was deleted", async () => {
    const user = userEvent.setup();
    const disconnectGitHubReview = vi.fn().mockRejectedValue(new Error("credential store unavailable"));
    renderPanel(mockClient([connection()], { disconnectGitHubReview }));
    await screen.findByText("Local credential is configured.");
    await user.click(screen.getByRole("button", { name: "Delete local credential…" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete local credential" }));
    expect(await screen.findByText("credential store unavailable")).toBeInTheDocument();
    expect(screen.getByText("Local credential is configured.")).toBeInTheDocument();
    expect(screen.queryByText("Local credential deleted for this connection.")).not.toBeInTheDocument();
  });

  it("gates credential deletion on process control and credential store capability", async () => {
    const disconnectGitHubReview = vi.fn();
    const { unmount } = renderPanel(mockClient([connection()], { hasGitHubReviewControl: false, disconnectGitHubReview }));
    expect(screen.queryByRole("button", { name: "Delete local credential…" })).not.toBeInTheDocument();
    unmount();
    const unavailable = credentialView();
    unavailable.credential.store_available = false;
    renderPanel(mockClient([connection()], { githubReviewCredential: vi.fn().mockResolvedValue(unavailable), disconnectGitHubReview }));
    await screen.findByText("Local credential is configured.");
    expect(screen.getByRole("button", { name: "Delete local credential…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Device sign-in" })).toBeDisabled();
    expect(disconnectGitHubReview).not.toHaveBeenCalled();
  });

  it("ignores device authorization completed after selecting another connection", async () => {
    const user = userEvent.setup();
    const request = deferred<{ session_id: string; user_code: string; verification_uri: string }>();
    const second = { ...connection(), id: "connection-2" };
    renderPanel(mockClient([connection(), second], { beginGitHubReviewDeviceFlow: vi.fn().mockReturnValue(request.promise) }));
    await screen.findByText("Local credential is configured.");
    await user.click(screen.getByRole("button", { name: "Device sign-in" }));
    await user.selectOptions(screen.getByLabelText("GitHub connection"), second.id);
    await act(async () => request.resolve({ session_id: "old-device", user_code: "OLD-CODE", verification_uri: "https://github.com/login/device" }));
    expect(screen.queryByText("OLD-CODE")).not.toBeInTheDocument();
  });

  it("discards a pending exact preview when the selected snapshot changes", async () => {
    const user = userEvent.setup();
    const request = deferred<GitHubReviewWriteReviewResultView>();
    const client = mockClient([connection()], { reviewGitHubWrite: vi.fn().mockReturnValue(request.promise) });
    const { queryClient } = renderPanel(client);
    await screen.findByText("Local credential is configured.");
    await user.click(screen.getByRole("button", { name: "Create exact preview" }));
    const next = projection();
    next.snapshots[0].id = "snapshot-2";
    next.snapshots[0].identity.head_sha = "3".repeat(40);
    await act(async () => queryClient.setQueryData(["run", "run-1", "github-review", "connection-1", 0], next));
    await act(async () => request.resolve(reviewedWrite()));
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
  });

  it("rejects a retained write belonging to another Run", async () => {
    const user = userEvent.setup();
    const executeGitHubWrite = vi.fn();
    const onRetainedReviewChange = vi.fn();
    const client = mockClient([connection()], { executeGitHubWrite });
    const first = renderPanel(client, undefined, vi.fn(), onRetainedReviewChange);
    await createPreview(user);
    const retained = onRetainedReviewChange.mock.calls.at(-1)![0] as GitHubReviewWriteReviewResultView;
    first.unmount();
    renderPanel(client, { ...retained, operation: { ...retained.operation, run_id: "other-run" } });
    await screen.findByText("Local credential is configured.");
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
    expect(executeGitHubWrite).not.toHaveBeenCalled();
  });

  it("keeps the draft visible after refresh failure and blocks preparing a write from the old snapshot", async () => {
    const user = userEvent.setup();
    let refreshFailed = false;
    const client = mockClient([connection()], {
      githubReviewConnections: vi.fn().mockImplementation(async () => {
        if (refreshFailed) throw new Error("connection refresh failed");
        return [credentialView()];
      }),
      fetchGitHubReview: vi.fn().mockRejectedValue(new Error("snapshot refresh failed")),
    });
    renderPanel(client);
    await createPreview(user);
    refreshFailed = true;
    await user.clear(screen.getByLabelText("GitHub App Client ID"));
    await user.type(screen.getByLabelText("GitHub App Client ID"), "Iv1.unsaved");
    await user.click(screen.getByRole("button", { name: "Refresh connection and remote PR" }));
    expect(await screen.findByText("snapshot refresh failed")).toBeInTheDocument();
    expect(await screen.findByText("connection refresh failed")).toBeInTheDocument();
    expect(screen.getByLabelText("GitHub App Client ID")).toHaveValue("Iv1.unsaved");
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create exact preview" })).toBeDisabled();
  });

  it("does not retain a usable reviewed write when refreshing credential status fails", async () => {
    const user = userEvent.setup();
    let refreshFailed = false;
    const client = mockClient([connection()], {
      githubReviewCredential: vi.fn().mockImplementation(async () => {
        if (refreshFailed) throw new Error("credential refresh failed");
        return credentialView();
      }),
    });
    const { queryClient } = renderPanel(client);
    await createPreview(user);
    refreshFailed = true;
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ["github-review", "credential"] }); });
    expect(await screen.findByText("credential refresh failed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create exact preview" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete local credential…" })).toBeDisabled();
  });

  it("signs out other cached connections sharing the deleted credential reference", async () => {
    const user = userEvent.setup();
    const second = { ...connection(), id: "connection-2",
      repository: { ...connection().repository, owner: "other", full_name: "other/widget" } };
    const request = deferred<ReturnType<typeof credentialView>>();
    let signedIn = true;
    const client = mockClient([connection(), second], {
      disconnectGitHubReview: vi.fn().mockReturnValue(request.promise),
      githubReviewCredential: vi.fn().mockImplementation(async (id: string) => credentialView(id === second.id ? second : connection(), signedIn)),
      githubReviewConnections: vi.fn().mockImplementation(async () => [connection(), second].map((item) => credentialView(item, signedIn))),
    });
    const { queryClient } = renderPanel(client);
    await screen.findByText("Local credential is configured.");
    await user.click(screen.getByRole("button", { name: "Delete local credential…" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete local credential" }));
    await user.selectOptions(screen.getByLabelText("GitHub connection"), second.id);
    await screen.findByText("Local credential is configured.");
    signedIn = false;
    await act(async () => request.resolve(credentialView(connection(), false)));
    expect(await screen.findByText("No local credential is configured.")).toBeInTheDocument();
    expect(screen.getByLabelText("GitHub connection")).toHaveValue(second.id);
    expect(screen.queryByText("Local credential deleted for this connection.")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create exact preview" })).toBeDisabled();
    expect(queryClient.getQueryData<ReturnType<typeof credentialView>>(["github-review", "credential", second.id])?.credential.configured).toBe(false);
  });

  it("does not let an old client reload overwrite the same Run's new client state or cache", async () => {
    const user = userEvent.setup();
    const request = deferred<ReturnType<typeof credentialView>>();
    const oldClient = mockClient([connection()], {
      githubReviewCredential: vi.fn().mockResolvedValueOnce(credentialView()).mockReturnValue(request.promise),
    });
    const newer = { ...connection(), generation: 10, client_id: "Iv1.current-client" };
    const newClient = mockClient([newer]);
    const { queryClient, rerenderPanel } = renderPanel(oldClient);
    await screen.findByText("Local credential is configured.");
    await user.click(screen.getByRole("button", { name: "Reload latest settings" }));
    rerenderPanel(newClient);
    await screen.findByDisplayValue("Iv1.current-client");
    await act(async () => request.resolve(credentialView({ ...connection(), generation: 2, client_id: "Iv1.old-response" })));
    expect(screen.getByLabelText("GitHub App Client ID")).toHaveValue("Iv1.current-client");
    expect(screen.queryByText("Latest settings loaded. Review them before saving.")).not.toBeInTheDocument();
    expect(queryClient.getQueryData<ReturnType<typeof credentialView>>(["github-review", "credential", connection().id])?.connection.generation).toBe(10);
  });

  it("ignores a pending preview after its review draft changes", async () => {
    const user = userEvent.setup();
    const request = deferred<GitHubReviewWriteReviewResultView>();
    renderPanel(mockClient([connection()], { reviewGitHubWrite: vi.fn().mockReturnValue(request.promise) }));
    await screen.findByText("Local credential is configured.");
    await user.click(screen.getByRole("button", { name: "Create exact preview" }));
    await user.type(screen.getByLabelText("Review body"), "Newer draft");
    await act(async () => request.resolve(reviewedWrite()));
    expect(screen.getByLabelText("Review body")).toHaveValue("Newer draft");
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
  });

  it("rejects a retained API result that has no original frontend source binding", async () => {
    const executeGitHubWrite = vi.fn();
    renderPanel(mockClient([connection()], { executeGitHubWrite }), reviewedWrite());
    await screen.findByText("Local credential is configured.");
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
    expect(executeGitHubWrite).not.toHaveBeenCalled();
  });

  it("rejects the original preview after approval-panel remount sees a different snapshot with the same HEAD and capability", async () => {
    const user = userEvent.setup();
    let source = projection();
    const executeGitHubWrite = vi.fn();
    const client = mockClient([connection()], {
      githubReviewProjection: vi.fn().mockImplementation(async (_runID: string, _id: string, number: number) => ({
        ...source, snapshots: source.snapshots.filter((item) => !number || item.identity.number === number),
      })), executeGitHubWrite,
    });
    const onRetainedReviewChange = vi.fn();
    const first = renderPanel(client, undefined, vi.fn(), onRetainedReviewChange);
    await createPreview(user);
    const retained = onRetainedReviewChange.mock.calls.at(-1)![0] as GitHubReviewWriteReviewResultView;
    await user.click(screen.getByRole("button", { name: "Open approvals" }));
    first.unmount();
    source = { ...source, snapshots: [{ ...source.snapshots[0], id: "snapshot-2" }] };
    renderPanel(client, retained);
    await screen.findByText("Local credential is configured.");
    expect(screen.getByLabelText("PR number")).toHaveValue(118);
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
    expect(executeGitHubWrite).not.toHaveBeenCalled();
  });

  it("rejects the original preview after approval-panel remount sees a newer connection generation", async () => {
    const user = userEvent.setup();
    const selected = [connection()];
    const executeGitHubWrite = vi.fn();
    const client = mockClient(selected, { executeGitHubWrite });
    const onRetainedReviewChange = vi.fn();
    const first = renderPanel(client, undefined, vi.fn(), onRetainedReviewChange);
    await createPreview(user);
    const retained = onRetainedReviewChange.mock.calls.at(-1)![0] as GitHubReviewWriteReviewResultView;
    await user.click(screen.getByRole("button", { name: "Open approvals" }));
    first.unmount();
    selected[0] = { ...selected[0], generation: 2, client_id: "Iv1.updated",
      network: { ...selected[0].network, allowed_log_hosts: ["updated.example"] } };
    renderPanel(client, retained);
    await screen.findByDisplayValue("Iv1.updated");
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
    expect(executeGitHubWrite).not.toHaveBeenCalled();
  });

  it("rejects a retained preview when remounted with a different API client", async () => {
    const user = userEvent.setup();
    const onRetainedReviewChange = vi.fn();
    const first = renderPanel(mockClient(), undefined, vi.fn(), onRetainedReviewChange);
    await createPreview(user);
    const retained = onRetainedReviewChange.mock.calls.at(-1)![0] as GitHubReviewWriteReviewResultView;
    first.unmount();
    const executeGitHubWrite = vi.fn();
    renderPanel(mockClient([connection()], { executeGitHubWrite }), retained);
    await screen.findByText("Local credential is configured.");
    expect(screen.queryByRole("button", { name: "Execute approved write" })).not.toBeInTheDocument();
    expect(executeGitHubWrite).not.toHaveBeenCalled();
  });

  it("restores the reviewed PR rather than the connection's newer snapshot for a different PR", async () => {
    const user = userEvent.setup();
    let source = projection();
    const githubReviewProjection = vi.fn().mockImplementation(async (_runID: string, _id: string, number: number) => ({
      ...source, snapshots: source.snapshots.filter((item) => !number || item.identity.number === number),
    }));
    const executeGitHubWrite = vi.fn().mockResolvedValue({ operation: { id: "write-1" } });
    const client = mockClient([connection()], { githubReviewProjection, executeGitHubWrite });
    const onRetainedReviewChange = vi.fn();
    const first = renderPanel(client, undefined, vi.fn(), onRetainedReviewChange);
    await createPreview(user);
    const retained = onRetainedReviewChange.mock.calls.at(-1)![0] as GitHubReviewWriteReviewResultView;
    await user.click(screen.getByRole("button", { name: "Open approvals" }));
    first.unmount();
    source = { ...source, snapshots: [{ ...source.snapshots[0], id: "snapshot-other-pr",
      identity: { ...source.snapshots[0].identity, number: 119, node_id: "PR_119", head_sha: "3".repeat(40) },
      fetched_at: "2026-08-21T11:00:00Z" }, ...source.snapshots] };
    renderPanel(client, retained);
    await waitFor(() => expect(screen.getByRole("button", { name: "Execute approved write" })).toBeEnabled());
    expect(screen.getByLabelText("PR number")).toHaveValue(118);
    expect(githubReviewProjection).toHaveBeenLastCalledWith("run-1", "connection-1", 118, expect.any(AbortSignal));
    await user.click(screen.getByRole("button", { name: "Execute approved write" }));
    await waitFor(() => expect(executeGitHubWrite).toHaveBeenCalledWith("run-1", "write-1", "approval-1"));
  });

  it("closes credential deletion confirmation if it observes a newer connection generation", async () => {
    const user = userEvent.setup();
    const disconnectGitHubReview = vi.fn();
    const { queryClient } = renderPanel(mockClient([connection()], { disconnectGitHubReview }));
    await screen.findByText("Local credential is configured.");
    await user.click(screen.getByRole("button", { name: "Delete local credential…" }));
    expect(screen.getByText(/local credential currently used by connection connection-1 \(acme\/widget\)/)).toBeInTheDocument();
    const next = credentialView({ ...connection(), generation: 2, credential: { name: "updated-reference", kind: "github_app_device" } });
    await act(async () => queryClient.setQueryData(["github-review", "credential", "connection-1"], next));
    expect(await screen.findByText("Connection settings changed. Reload the latest settings before deleting its local credential.")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(disconnectGitHubReview).not.toHaveBeenCalled();
  });
});

function renderPanel(client: APIClient,
  retainedReview?: GitHubReviewWriteReviewResultView | null,
  onOpenDelivery: () => void = vi.fn(),
  onRetainedReviewChange = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  const panel = (selectedClient: APIClient) => <QueryClientProvider client={queryClient}>
    <GitHubReviewPanel client={selectedClient} onOpenApprovals={vi.fn()}
      onOpenDelivery={onOpenDelivery}
      retainedReview={retainedReview} onRetainedReviewChange={onRetainedReviewChange} runID="run-1" />
  </QueryClientProvider>;
  const result = render(panel(client));
  return { queryClient, ...result, rerenderPanel: (nextClient: APIClient) => result.rerender(panel(nextClient)) };
}
