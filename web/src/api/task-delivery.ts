import type { CyberAgentClient } from "./client";
import type { components } from "./schema";

export type ThreadReview = components["schemas"]["ThreadReview"];
export type ThreadReviewChange = components["schemas"]["ThreadReviewChange"];
export type ThreadGitState = components["schemas"]["ThreadGitState"];
export type ThreadGitSpec = components["schemas"]["ThreadGitSpec"];
export type ThreadGitPreview = components["schemas"]["ThreadGitPreview"];
export type ThreadGitResult = components["schemas"]["ThreadGitResult"];
export type ThreadGitExecuteRequest = components["schemas"]["ThreadGitExecuteRequest"];
export type PullRequestDiscovery = components["schemas"]["ThreadPullRequestDiscovery"];
export type PullRequestPreview = components["schemas"]["ThreadPullRequestPreviewResult"];
export type PullRequestResult = components["schemas"]["ThreadPullRequestResult"];
export type PullRequestRefresh = components["schemas"]["ThreadPullRequestRefreshResult"];
export type PullRequestPreviewRequest = components["schemas"]["ThreadPullRequestPreviewRequest"];

export async function discoverPullRequest(client: CyberAgentClient, threadID: string, connectionID: string, base: string, signal?: AbortSignal): Promise<PullRequestDiscovery> {
  const value = await client.get<PullRequestDiscovery>(`${path(threadID)}/pull-request`, { connection_id: connectionID, ...(base ? { base_branch: base } : {}) }, signal);
  identity(value.context, threadID);
  if (value.version !== "thread_pull_request.v1" || value.connection_id !== connectionID || !Array.isArray(value.pull_requests)) throw new Error("PR 来源不匹配，无法确认。");
  return value;
}
export async function previewPullRequest(client: CyberAgentClient, threadID: string, body: PullRequestPreviewRequest): Promise<PullRequestPreview> {
  const value = await client.postControl<PullRequestPreview>(`${path(threadID)}/pull-request/preview`, body, body.operation_key);
  if (value.version !== "thread_pull_request.v1" || !Array.isArray(value.existing_pull_requests)) throw new Error("PR 预览无法确认，请核对原请求。");
  if (value.preview) identity(value.preview, threadID);
  return value;
}
function pullRequestResult(value: PullRequestResult, threadID: string): PullRequestResult {
  identity(value, threadID);
  if (value.version !== "thread_pull_request.v1" || !["not_received", "proposed", "unknown", "created", "failed"].includes(value.state)) throw new Error("PR 创建结果未知，请核对原请求。");
  if (value.preview) identity(value.preview, threadID);
  return value;
}
export async function observePullRequest(client: CyberAgentClient, threadID: string, key: string, signal?: AbortSignal): Promise<PullRequestResult> {
  return pullRequestResult(await client.get<PullRequestResult>(`${path(threadID)}/pull-request/request`, { operation_key: key }, signal), threadID);
}
export async function createPullRequest(client: CyberAgentClient, threadID: string, operationID: string, approvalID: string, key: string): Promise<PullRequestResult> {
  return pullRequestResult(await client.postControl<PullRequestResult>(`${path(threadID)}/pull-request/create`, {
    version: "thread_pull_request.v1", operation_id: operationID, approval_id: approvalID,
  }, key), threadID);
}
export async function refreshPullRequest(client: CyberAgentClient, threadID: string, connectionID: string, number: number): Promise<PullRequestRefresh> {
  const value = await client.postControl<PullRequestRefresh>(`${path(threadID)}/pull-request/refresh`, {
    version: "thread_pull_request.v1", connection_id: connectionID, pull_request: number,
  }, `pr-refresh-${crypto.randomUUID()}`);
  identity(value, threadID);
  if (value.version !== "thread_pull_request.v1" || !value.snapshot || value.snapshot.identity.number !== number || !Array.isArray(value.omissions)) throw new Error("刷新返回的 PR 对象不匹配。");
  return value;
}

const path = (id: string) => `/threads/${encodeURIComponent(id)}`;
function identity(value: unknown, threadID: string): asserts value is Record<string, unknown> {
  if (!value || typeof value !== "object" || !("thread_id" in value) || value.thread_id !== threadID) {
    throw new Error("返回的审阅对象与当前任务不一致，请刷新核对。");
  }
}
export async function readThreadReview(client: CyberAgentClient, threadID: string, signal?: AbortSignal): Promise<ThreadReview> {
  const value = await client.get<ThreadReview>(`${path(threadID)}/review`, {}, signal);
  identity(value, threadID);
  if (!value.target || !value.revision || !Array.isArray(value.applied_changes) || !Array.isArray(value.unapplied_changes) ||
    !Array.isArray(value.checks) || !Array.isArray(value.reasons) || !Number.isFinite(Date.parse(value.observed_at))) {
    throw new Error("任务审阅数据不完整，无法确认当前变化。");
  }
  return value;
}
export async function readThreadGit(client: CyberAgentClient, threadID: string, signal?: AbortSignal): Promise<ThreadGitState> {
  const value = await client.get<ThreadGitState>(`${path(threadID)}/git`, {}, signal);
  identity(value, threadID);
  if (value.version !== "thread_git.v1" || !Array.isArray(value.changes) || !Array.isArray(value.remotes) || !Array.isArray(value.branches)) {
    throw new Error("Git 状态不完整，请重新读取。");
  }
  return value;
}
export async function previewThreadGit(client: CyberAgentClient, threadID: string, runID: string, spec: ThreadGitSpec): Promise<ThreadGitPreview> {
  const value = await client.postControl<ThreadGitPreview>(`${path(threadID)}/git/preview`,
    { version: "thread_git.v1", run_id: runID, spec }, `git-preview-${crypto.randomUUID()}`);
  identity(value, threadID);
  if (value.version !== "thread_git.v1" || value.run_id !== runID || !value.preview_fingerprint || !value.spec) {
    throw new Error("Git 预览身份不完整，不能执行。");
  }
  return value;
}
function gitResult(value: ThreadGitResult, threadID: string): ThreadGitResult {
  identity(value, threadID);
  if (value.version !== "thread_git.v1" || !["not_received", "completed", "unknown"].includes(value.state)) {
    throw new Error("Git 操作结果无法确认，请保留原请求并核对。");
  }
  return value;
}
export async function executeThreadGit(client: CyberAgentClient, threadID: string, body: ThreadGitExecuteRequest): Promise<ThreadGitResult> {
  return gitResult(await client.postControl<ThreadGitResult>(`${path(threadID)}/git/execute`, body, body.operation_key), threadID);
}
export async function observeThreadGit(client: CyberAgentClient, threadID: string, key: string, signal?: AbortSignal): Promise<ThreadGitResult> {
  return gitResult(await client.get<ThreadGitResult>(`${path(threadID)}/git/requests/${encodeURIComponent(key)}`, {}, signal), threadID);
}
