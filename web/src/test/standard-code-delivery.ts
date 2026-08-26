import type { StandardCodeDeliveryView } from "../api/types";

const digest = "a".repeat(64);
const revision = "b".repeat(64);
const now = "2026-08-26T08:00:00Z";

export function standardCodeDeliveryFixture(): StandardCodeDeliveryView {
  return {
    id: "standard-code-delivery-test",
    protocol_version: "standard_code_delivery.v1",
    operation_key_sha256: "c".repeat(64),
    request_fingerprint: "d".repeat(64),
    status: "passed",
    receipt_status: "passed",
    verified: true,
    binding: {
      run_id: "run-1", mission_id: "mission-1", session_id: "session-1",
      source_workspace_id: "workspace-1", drydock_workspace_id: "drydock-workspace-1",
      drydock_id: "drydock-1", drydock_generation: 2,
      preset_operation_sha256: "e".repeat(64), permission_snapshot_id: "permission-1",
      permission_revision: 3, backend: "local", backend_generation_sha256: "f".repeat(64),
      capability_generation_sha256: "1".repeat(64), supervisor_mutation_epoch: 4,
    },
    base_commit: "1".repeat(40),
    head_commit: "2".repeat(40),
    diff: {
      sha256: "3".repeat(64), bytes: 128, changed_count: 1, tracked_count: 1,
      committed_count: 0, index_count: 1, worktree_count: 0, untracked_count: 0,
      conflict_count: 0, redacted_count: 0,
      files: [{ path: "internal/example.go", path_sha256: "4".repeat(64), tracked: true,
        committed: false, index_changed: true, worktree_changed: false, untracked: false,
        conflicted: false, path_redacted: false,
        file_url: "/api/v1/workspaces/drydock-workspace-1/explore?path=internal%2Fexample.go" }],
    },
    final_checkpoint: {
      id: "checkpoint-final", manifest_sha256: "5".repeat(64), index_sha256: "6".repeat(64),
      root_fingerprint: "7".repeat(64), root_path_sha256: "8".repeat(64),
      head_commit: "2".repeat(40), branch_sha256: "9".repeat(64), revision_sha256: revision,
      recovery_level: "complete", incomplete_reason_sha256: [], created_at: now,
    },
    verifications: [{
      job_id: "verification-1", conclusion: "passed", reason_code: "verification_passed",
      state: "completed", exit_code: 0, spec_sha256: "a".repeat(64),
      executable_sha256: "b".repeat(64), environment_sha256: "c".repeat(64),
      permission_revision: 3, backend: "local", backend_generation_sha256: "f".repeat(64),
      checkpoint_id: "checkpoint-final", revision_sha256: revision, current_revision: true,
      retry_count: 1, stdout_sha256: digest, stderr_sha256: "0".repeat(64),
      stdout_observed_bytes: 12, stderr_observed_bytes: 0, output_truncated: false,
      tree_reaped: true,
      artifacts: [{ id: "artifact-stdout", stream: "stdout", sha256: digest,
        size_bytes: 12, redacted: true, url: "/api/v1/artifacts/artifact-stdout" }],
      started_at: now, completed_at: now,
    }],
    reasons: [{ code: "verification_passed", provenance_sha256: "e".repeat(64) }],
    uncovered_items: [],
    links: {
      self: "/api/v1/runs/run-1/standard-code-delivery",
      checkpoint: "/api/v1/runs/run-1/workspace-checkpoints?checkpoint_id=checkpoint-final",
      checkpoint_timeline: "/api/v1/runs/run-1/workspace-checkpoints",
      undo: "/api/v1/runs/run-1/workspace-checkpoints/undo",
      rewind: "/api/v1/runs/run-1/workspace-checkpoints/rewind",
      fork: "/api/v1/runs/run-1/workspace-checkpoints/fork",
    },
    safeguards: {
      automatic_commit: false, automatic_push: false, automatic_merge: false,
      source_overwrite: false, raw_environment_stored: false, raw_output_stored: false,
      private_reasoning_stored: false, absolute_paths_exposed: false,
    },
    receipt_sha256: "f".repeat(64), event_sequence: 42, created_at: now,
  };
}
