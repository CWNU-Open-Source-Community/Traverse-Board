import { createRef } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { APIClient } from "../../api/client";
import type { ThreadDetailView } from "../../api/types";
import { V2TaskReview } from "./task-review";

vi.mock("../../lib/locale", () => ({ useLocale: () => ({ t: (chinese: string) => chinese }) }));

const sha = "a".repeat(64);
const oid = "b".repeat(40);
function detail(): ThreadDetailView {
  const current = { id: "run-current", status: "running" };
  return { thread: { id: "thread-1", title: "审阅工具", workspace_id: "source-current" },
    active_run: current, last_run: current,
    runs: [{ ordinal: 1, run: { id: "run-history", status: "completed" } },
      { ordinal: 2, run: current }] } as ThreadDetailView;
}
function gitProjection(runID: string) {
  return { run_id: runID, workspace_id: `${runID}-workspace`,
    authority: { executable: true, permission_revision: 2,
      scope: { capability_generation: sha, lease_generation: 3 } },
    binding: { head: oid, branch: `${runID}-branch`, detached: false,
      repository_sha256: sha, index_sha256: sha, worktree_sha256: sha,
      status_sha256: sha, stash_sha256: sha, sequence_sha256: sha },
    conflict: { active: false, files: [] }, stashes: [], worktrees: [], operations: [] };
}
function client() {
  return {
    get: vi.fn().mockRejectedValue(new Error("Overview is outside this fixture")),
    hasBatchDeliveryControl: true, hasBatchDeliveryHostValidation: false,
    hasGitAdvancedControl: true, hasGitHubReviewControl: true,
    hasUIEvidence: false, uiEvidenceUnavailableReason: "ui_evidence_disabled",
    getRunBatchDeliveries: vi.fn().mockResolvedValue({ items: [] }),
    gitAdvancedProjection: vi.fn((runID: string) => Promise.resolve(gitProjection(runID))),
    githubReviewConnections: vi.fn().mockResolvedValue([{ connection: { id: "connection-1",
      repository: { full_name: "fixture/project" } }, credential: { configured: true } }]),
    githubReviewProjection: vi.fn((runID: string) => Promise.resolve({ run_id: runID,
      connection: { network: { write_enabled: true }, credential: { name: "fixture-reference" } },
      snapshots: [{ id: "snapshot-exact", identity: { number: 1, head_sha: oid },
        capability: { generation: sha }, title: { text: "Fixture PR" },
        jobs: [], files: [], threads: [], check_runs: [], fetched_at: "2026-10-07T00:00:00Z" }],
      evidence: [], writes: [], standard_code_delivery: { receipt_sha256: sha,
        diff: { changed_count: 0 }, verifications: [], status: "passed" },
    })),
    uiEvidence: vi.fn().mockResolvedValue([]),
    approvalQueue: vi.fn().mockResolvedValue({ items: [] }),
    controlledCommandProposals: vi.fn().mockResolvedValue({ items: [] }),
    hostCommandProposals: vi.fn().mockResolvedValue({ items: [] }),
    reviewGitAdvanced: vi.fn((runID: string) => Promise.resolve({
      run_id: runID, workspace_id: `${runID}-workspace`,
      preview: { id: "preview-exact", operation: "stash_create", summary: "Exact selected Run",
        blocked_reasons: [], files: [], hunks: [], recovery: { required: false, incomplete_reasons: [] } },
      operation: { id: "operation-exact" }, approval: { ID: "approval-exact" },
    })),
    executeGitAdvanced: vi.fn().mockResolvedValue({ receipt: { status: "succeeded" } }),
    reviewGitHubWrite: vi.fn().mockResolvedValue({ preview: { approval_fingerprint: sha },
      operation: { id: "github-write-exact" }, approval: { ID: "github-approval-exact" } }),
    executeGitHubWrite: vi.fn().mockResolvedValue({ receipt: { status: "succeeded" } }),
    runCapabilityReadiness: vi.fn().mockRejectedValue(new Error("Readiness is outside this fixture")),
    standardCodeDelivery: vi.fn().mockRejectedValue(new Error("Delivery is outside this fixture")),
  };
}
function renderReview(api: ReturnType<typeof client>, value = detail()) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const node = (next: ThreadDetailView) => <QueryClientProvider client={queryClient}>
    <V2TaskReview client={api as unknown as APIClient} detail={next} working={false}
      returnFocusRef={createRef()} onClose={vi.fn()} onRequestChange={vi.fn()} />
  </QueryClientProvider>;
  return { ...render(node(value)), node };
}
async function chooseTool(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(within(screen.getByRole("group", { name: "选择交付工具" }))
    .getByRole("button", { name: new RegExp(`^${name}`, "u") }));
}

it("loads advanced tools only after selection and binds every panel to the explicitly chosen historical Run", async () => {
  const api = client();
  const user = userEvent.setup();
  renderReview(api);
  expect(api.getRunBatchDeliveries).not.toHaveBeenCalled();
  expect(api.gitAdvancedProjection).not.toHaveBeenCalled();
  expect(api.githubReviewConnections).not.toHaveBeenCalled();
  expect(api.uiEvidence).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "更多交付工具" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "选择审阅的执行记录" }), "run-history");
  expect(api.getRunBatchDeliveries).not.toHaveBeenCalled();
  expect(api.gitAdvancedProjection).not.toHaveBeenCalled();
  expect(api.githubReviewConnections).not.toHaveBeenCalled();
  expect(api.uiEvidence).not.toHaveBeenCalled();

  await chooseTool(user, "批量交付");
  await screen.findByText("暂无 batch-delivery.v1 计划");
  expect(api.getRunBatchDeliveries).toHaveBeenCalledWith("run-history", expect.any(AbortSignal));
  expect(api.gitAdvancedProjection).not.toHaveBeenCalled();
  await chooseTool(user, "高级 Git");
  await screen.findByText("run-history-branch");
  expect(api.gitAdvancedProjection).toHaveBeenCalledWith("run-history", expect.any(AbortSignal));
  expect(api.githubReviewConnections).not.toHaveBeenCalled();
  await chooseTool(user, "GitHub 审阅");
  await waitFor(() => expect(api.githubReviewProjection).toHaveBeenCalledWith(
    "run-history", "connection-1", 0, expect.any(AbortSignal)));
  expect(api.uiEvidence).not.toHaveBeenCalled();
  await chooseTool(user, "UI 取证");
  await screen.findByText("尚未创建 UI 验证 Attempt");
  expect(api.uiEvidence).toHaveBeenCalledWith("run-history", expect.any(AbortSignal));
  await user.selectOptions(screen.getByRole("combobox", { name: "选择审阅的执行记录" }), "run-current");
  expect(screen.getByText(/选择工具后才会读取对应状态/)).toBeInTheDocument();
  expect(api.uiEvidence).toHaveBeenCalledTimes(1);
  await chooseTool(user, "UI 取证");
  await waitFor(() => expect(api.uiEvidence).toHaveBeenCalledWith("run-current", expect.any(AbortSignal)));
});

it("preserves the exact Git review through same-Run approval navigation and clears it on Run changes", async () => {
  const api = client();
  const user = userEvent.setup();
  renderReview(api);
  await user.click(screen.getByRole("button", { name: "更多交付工具" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "选择审阅的执行记录" }), "run-history");
  await chooseTool(user, "高级 Git");
  await user.click(await screen.findByRole("button", { name: "请求创建审批" }));
  await screen.findByText("approval-exact");
  expect(api.reviewGitAdvanced).toHaveBeenCalledWith("run-history", expect.objectContaining({
    scope: { capability_generation: sha, lease_generation: 3 },
    spec: expect.objectContaining({ operation: "stash_create" }),
  }));
  expect(api.executeGitAdvanced).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "打开审批" }));
  await screen.findByText("没有待处理审批");
  expect(api.approvalQueue).toHaveBeenCalledWith("run-history", expect.any(AbortSignal));
  await user.click(screen.getByRole("button", { name: "返回所选工具" }));
  await user.click(await screen.findByRole("button", { name: "执行已审批操作" }));
  await waitFor(() => expect(api.executeGitAdvanced).toHaveBeenCalledWith("run-history", {
    operation_id: "operation-exact", approval_id: "approval-exact",
    scope: { capability_generation: sha, lease_generation: 3 },
  }));
  await user.click(screen.getByRole("button", { name: "请求创建审批" }));
  await screen.findByText("approval-exact");
  await user.selectOptions(screen.getByRole("combobox", { name: "选择审阅的执行记录" }), "run-current");
  await chooseTool(user, "高级 Git");
  await screen.findByText("run-current-branch");
  expect(screen.queryByText("approval-exact")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "执行已审批操作" })).not.toBeInTheDocument();
  expect(api.executeGitAdvanced).toHaveBeenCalledTimes(1);
});

it("refuses to substitute the current Run when the selected historical Run disappears", async () => {
  const api = client();
  const user = userEvent.setup();
  const view = renderReview(api);
  await user.click(screen.getByRole("button", { name: "更多交付工具" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "选择审阅的执行记录" }), "run-history");
  await chooseTool(user, "批量交付");
  await screen.findByText("暂无 batch-delivery.v1 计划");
  const changed = detail();
  changed.runs = changed.runs.filter(({ run }) => run.id !== "run-history");
  view.rerender(view.node(changed));
  expect(screen.getByRole("alert")).toHaveTextContent("无法找到目标执行记录 run-history");
  expect(screen.queryByRole("group", { name: "选择交付工具" })).not.toBeInTheDocument();
  expect(api.getRunBatchDeliveries).toHaveBeenCalledTimes(1);
  expect(api.getRunBatchDeliveries.mock.calls[0][0]).toBe("run-history");
});

it("retains a GitHub write through approval and delivery pages without rebinding it to the current Run", async () => {
  const api = client();
  const user = userEvent.setup();
  renderReview(api);
  await user.click(screen.getByRole("button", { name: "更多交付工具" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "选择审阅的执行记录" }), "run-history");
  await chooseTool(user, "GitHub 审阅");
  await user.click(await screen.findByRole("button", { name: "生成精确预览" }));
  await screen.findByRole("button", { name: "执行已批准操作" });
  expect(api.reviewGitHubWrite).toHaveBeenCalledWith("run-history", expect.objectContaining({
    connection_id: "connection-1", snapshot_id: "snapshot-exact",
    spec: expect.objectContaining({ capability_generation: sha }),
  }));
  expect(api.executeGitHubWrite).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "打开审批" }));
  await screen.findByText("没有待处理审批");
  expect(api.approvalQueue).toHaveBeenCalledWith("run-history", expect.any(AbortSignal));
  await user.click(screen.getByRole("button", { name: "返回所选工具" }));
  await user.click(await screen.findByRole("button", { name: "打开交付页" }));
  expect(screen.getByRole("combobox", { name: "选择审阅的执行记录" })).toHaveValue("run-history");
  await user.click(screen.getByRole("button", { name: "更多交付工具" }));
  await chooseTool(user, "GitHub 审阅");
  await user.click(await screen.findByRole("button", { name: "执行已批准操作" }));
  await waitFor(() => expect(api.executeGitHubWrite).toHaveBeenCalledWith(
    "run-history", "github-write-exact", "github-approval-exact"));
});
