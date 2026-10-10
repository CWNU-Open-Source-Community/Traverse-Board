import type { BatchWorkbenchView, ChildTaskProposalView } from "../api/types";

export function batchWorkbenchFixture(status = "acknowledged", generation = 1): BatchWorkbenchView {
  const now = "2026-10-10T00:00:00Z";
  const base = "a".repeat(40);
  return {
    protocol_version: "batch-delivery-workbench.v1", worker_available: true, replayed: false,
    children: [{ ordinal: 1, generation, owner_available: true, executing: false, outcome_unresolved: false }],
    snapshot: { protocol_version: "batch-delivery.v1",
      plan: { id: "batch-1", run_id: "run-1", proposal_id: "proposal-1", root_agent_id: "agent-root",
        workspace_id: "workspace-1", status: "active", base_commit: base, source_branch: "main", created_by: "operator",
        created_at: now, updated_at: now,
        spec: { version: "batch-delivery.v1", tasks: [{ ordinal: 1,
          ownership_hints: [{ path: "internal/parser/a.go", kind: "file" }], dependency_ordinals: [],
          budget: { turn_limit: 2, token_limit: 1024, timeout_millis: 120000 },
          validations: [{ id: "diff", kind: "git_diff_check", scope: "." }], expected_artifacts: [] }],
          contract: { require_clean: true, require_independent_review: true, require_all_validations: true,
            max_changed_files: 128, max_diff_bytes: 8388608 } } },
      children: [{ workspace: { plan_id: "batch-1", ordinal: 1, agent_id: "agent-child", generation, status,
        branch: "codex/batch/task-1", base_commit: base, head_commit: base,
        tool_profile: { version: "batch-delivery-tools.v1", workspace_list: true, workspace_read: true,
          workspace_search: true, workspace_change: true, workspace_apply: true, git_status: true,
          git_diff: true, git_commit: true, workspace_delete: false, shell: false, process: false,
          network: false, credentials: false, debug_terminal: false, approvals: false, spawn_children: false },
        lease_expires_at: "2026-10-10T01:00:00Z", last_heartbeat_at: now, created_at: now, updated_at: now },
        mailbox: [] }], merge_steps: [] },
  };
}

export function batchProposalFixture(): ChildTaskProposalView {
  return { id: "proposal-1", run_id: "run-1", root_agent_id: "agent-root", status: "approved", surface: "core",
    fanout_tier: "", requested_by: "operator", created_at: "2026-10-10T00:00:00Z",
    tasks: [{ ordinal: 1, title: "Parser regression", goal: "Add the bounded parser regression", skills: ["model.chat"],
      turn_limit: 2, token_limit: 1024, timeout_millis: 120000, input_refs: [], dependency_ordinals: [], surface_hint: "core" }],
    assignments: [{ ordinal: 1, admitted_agent_id: "agent-child", status: "admitted", surface: "core", fanout_tier: "",
      turn_limit: 2, token_limit: 1024, timeout_millis: 120000 }] };
}
