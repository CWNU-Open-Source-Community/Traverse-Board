import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CyberAgentClient } from "../api/client";
import type { GitAdvancedReviewResultView } from "../api/types";
import { GitAdvancedPanel } from "./git-advanced-panel";

const oid = "1".repeat(40);
const otherOID = "2".repeat(40);
const sha = "a".repeat(64);
const now = "2026-08-20T10:00:00Z";

function binding() {
  return {
    protocol_version: "git-advanced.v1", repository_sha256: sha,
    common_dir_sha256: sha, head: oid, branch: "main", index_sha256: sha,
    worktree_sha256: sha, status_sha256: sha, stash_sha256: sha,
    sequence_sha256: sha, upstream_ref: "refs/remotes/origin/main", upstream_oid: otherOID,
    detached: false, object_format: "sha1", captured_at: now,
  };
}

function preview(operation = "stash_apply") {
  return {
    protocol_version: "git-advanced-preview.v1", id: "preview-1", operation,
    spec: { protocol_version: "git-advanced.v1", operation, stash_oid: oid,
      restore_index: true }, binding: binding(),
    capability: { protocol_version: "git-advanced-capability.v1", enabled: true,
      generation: sha, managed_root_sha256: sha, max_hunks: 200, max_paths: 200,
      max_commits: 128, operations: [operation], captured_at: now },
    hunks: [], files: [{ path: "tracked.txt", change: "worktree_modified", destructive: true }],
    conflict: { active: false, files: [], can_continue: false, can_skip: false, can_abort: false },
    recovery: { required: true, checkpoint_level: "full", restore_action: "restore",
      incomplete_reasons: [] }, target: oid, summary: "Apply exact stash object",
    blocked_reasons: [], approval_fingerprint: sha, permission_snapshot_id: "permission-1",
    permission_revision: 2, lease_generation: 3, created_at: now,
  };
}

function projection() {
  return {
    protocol_version: "git-advanced-api.v1", run_id: "run-1", workspace_id: "workspace-1",
    authority: { protocol_version: "git-advanced-authority.v1", executable: true,
      lease_active: true, lease_expires_at: "2026-08-20T11:00:00Z",
      permission_revision: 2, permission_snapshot_id: "permission-1",
      scope: { capability_generation: sha, lease_generation: 3 } },
    capability: preview().capability, binding: binding(),
    conflict: { active: true, kind: "rebase", can_continue: true, can_skip: true,
      can_abort: true, files: [{ path: "conflict.txt", base_oid: oid,
        ours_oid: otherOID, theirs_oid: "3".repeat(40) }] },
    stashes: [{ oid, base_commit: otherOID, index_commit: "3".repeat(40),
      untracked_commit: "4".repeat(40), subject: "WIP exact stash",
      files: [{ path: "tracked.txt", change: "index_modified", destructive: false }] }],
    worktrees: [{ id: "worktree-1", protocol_version: "git-managed-worktree.v1",
      run_id: "run-1", workspace_id: "workspace-1", repository_sha256: sha,
      common_dir_sha256: sha, name: "review", path_sha256: sha, branch: "review/branch",
      head: oid, locked: false, present: true, generation: 1,
      created_operation_id: "operation-create", last_operation_id: "operation-create",
      created_at: now, updated_at: now,
      path: "C:\\private\\must-not-render" }],
    operations: [],
  };
}

describe("GitAdvancedPanel", () => {
  it("shows the fail-closed startup gate without issuing requests", () => {
    const gitAdvancedProjection = vi.fn();
    renderPanel({ hasGitAdvancedControl: false, gitAdvancedProjection } as unknown as CyberAgentClient);
    expect(screen.getByText(/did not explicitly enable Advanced Git/)).toBeInTheDocument();
    expect(gitAdvancedProjection).not.toHaveBeenCalled();
  });

  it("renders conflict stages, stash roles, managed path digests, and never a host path", async () => {
    renderPanel({ hasGitAdvancedControl: true,
      gitAdvancedProjection: vi.fn().mockResolvedValue(projection()) } as unknown as CyberAgentClient);
    expect(await screen.findByText("Conflict state")).toBeInTheDocument();
    expect(screen.getByText("base " + oid)).toBeInTheDocument();
    expect(screen.getByText("index " + "3".repeat(40))).toBeInTheDocument();
    expect(screen.getByText("untracked " + "4".repeat(40))).toBeInTheDocument();
    expect(screen.getByText(/path sha256/)).toBeInTheDocument();
    expect(screen.queryByText(/private.*must-not-render/)).not.toBeInTheDocument();
  });

  it("keeps review, approval navigation, and execution as separate explicit actions", async () => {
    const user = userEvent.setup();
    const reviewed = { protocol_version: "git-advanced-api.v1", run_id: "run-1",
      workspace_id: "workspace-1", preview: preview(), replayed: false,
      operation: { id: "operation-1" }, approval: { ID: "approval-1" } };
    const reviewGitAdvanced = vi.fn().mockResolvedValue(reviewed);
    const executeGitAdvanced = vi.fn().mockResolvedValue({ receipt: { status: "succeeded" } });
    const onOpenApprovals = vi.fn();
    renderPanel({ hasGitAdvancedControl: true,
      gitAdvancedProjection: vi.fn().mockResolvedValue(projection()),
      reviewGitAdvanced, executeGitAdvanced } as unknown as CyberAgentClient, onOpenApprovals);

    await user.click(await screen.findByRole("button", { name: "apply" }));
    await waitFor(() => expect(reviewGitAdvanced).toHaveBeenCalledTimes(1));
    expect(reviewGitAdvanced.mock.calls[0]?.[1]).toMatchObject({
      scope: { capability_generation: sha, lease_generation: 3 },
      spec: { protocol_version: "git-advanced.v1", operation: "stash_apply",
        stash_oid: oid, restore_index: true },
    });
    expect(await screen.findByText("Exact one-time approval")).toBeInTheDocument();
    expect(executeGitAdvanced).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Open approvals" }));
    expect(onOpenApprovals).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Execute approved operation" }));
    await waitFor(() => expect(executeGitAdvanced).toHaveBeenCalledWith("run-1", {
      operation_id: "operation-1", approval_id: "approval-1",
      scope: { capability_generation: sha, lease_generation: 3 },
    }));
  });

  it("resumes an exact reviewed operation after the repository panel remounts", async () => {
    const user = userEvent.setup();
    const reviewed = { protocol_version: "git-advanced-api.v1", run_id: "run-1",
      workspace_id: "workspace-1", preview: preview(), replayed: false,
      operation: { id: "operation-retained" }, approval: { ID: "approval-retained" } };
    const executeGitAdvanced = vi.fn().mockResolvedValue({ receipt: { status: "succeeded" } });
    renderPanel({ hasGitAdvancedControl: true,
      gitAdvancedProjection: vi.fn().mockResolvedValue(projection()),
      executeGitAdvanced } as unknown as CyberAgentClient, vi.fn(),
    reviewed as unknown as GitAdvancedReviewResultView);

    expect(await screen.findByText("Exact one-time approval")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Execute approved operation" }));
    await waitFor(() => expect(executeGitAdvanced).toHaveBeenCalledWith("run-1", {
      operation_id: "operation-retained", approval_id: "approval-retained",
      scope: { capability_generation: sha, lease_generation: 3 },
    }));
  });

  it("offers only the closed bisect controls for an active bisect sequence", async () => {
    const user = userEvent.setup();
    const bisectProjection = {
      ...projection(),
      conflict: { active: false, kind: "bisect", can_continue: true,
        can_skip: false, can_abort: true, files: [] },
      sequence: { id: "sequence-1", protocol_version: "git-advanced-sequence.v1",
        run_id: "run-1", workspace_id: "workspace-1", kind: "bisect", status: "active",
        repository_sha256: sha, original_head: otherOID, original_branch: "feature",
        sequencer_sha256: sha, current_head: oid, generation: 1,
        started_operation_id: "operation-start", last_operation_id: "operation-start",
        created_at: now, updated_at: now },
    };
    const reviewGitAdvanced = vi.fn().mockReturnValue(new Promise(() => undefined));
    renderPanel({ hasGitAdvancedControl: true,
      gitAdvancedProjection: vi.fn().mockResolvedValue(bisectProjection),
      reviewGitAdvanced } as unknown as CyberAgentClient);

    expect(await screen.findByRole("button", { name: "reset" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "continue" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "abort" })).not.toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "skip" })).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "reset" }));
    await waitFor(() => expect(reviewGitAdvanced).toHaveBeenCalledWith("run-1",
      expect.objectContaining({ spec: { protocol_version: "git-advanced.v1",
        operation: "bisect_reset", sequence_id: "sequence-1" } })));
  });
});

function renderPanel(client: CyberAgentClient, onOpenApprovals = vi.fn(),
  retainedReview?: GitAdvancedReviewResultView | null) {
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false }, mutations: { retry: false },
  } });
  return render(<QueryClientProvider client={queryClient}>
    <GitAdvancedPanel client={client} onOpenApprovals={onOpenApprovals}
      retainedReview={retainedReview} runID="run-1" />
  </QueryClientProvider>);
}
