import { useQuery } from "@tanstack/react-query";
import { ArrowRight, FolderOpen, GitBranch, GitPullRequest, RefreshCw, ShieldCheck } from "lucide-react";
import type { APIClient } from "../../api/client";
import { readThreadReview, type ThreadReview, type ThreadReviewChange } from "../../api/task-delivery";
import type { RunDetailView } from "../../api/types";
import { ErrorState, LoadingState, StatusBadge } from "../../components/common";
import type { FileEditReviewTarget } from "../../components/file-edit-panel";
import { PlanDeliveryPanel } from "../../components/run-workspace";
import { ReviewDiff } from "./review-diff";
import "./task-delivery.css";

const currentLabels: Record<string, string> = { matches: "与此记录一致", changed: "此后已有修改", missing: "当前文件不存在",
  unavailable: "无法核对当前文件", other_target: "属于其他执行目录" };
const freshness: Record<string, string> = { current: "适用于当前版本", stale: "代码已变化，需重验", unbound: "未绑定代码版本", unavailable: "无法核对版本" };
export const taskReviewKey = (threadID: string) => ["thread", threadID, "task-review"] as const;

export function isCheckPassed(outcome: string): boolean {
  const normalized = outcome.toLowerCase().trim();
  return normalized === "pass" || normalized === "passed" || normalized === "success";
}

export function isCheckFailed(outcome: string): boolean {
  const normalized = outcome.toLowerCase().trim();
  return normalized === "fail" || normalized === "failed" || normalized === "failure" || normalized === "error";
}

export function TaskOverview({ client, threadID, onFeedback, onGit, onPullRequest, onChecks, onReviewFile, currentRunDetail }: {
  client: APIClient; threadID: string; onFeedback: (context: string) => void; onGit?: () => void;
  onPullRequest?: () => void; onChecks?: () => void;
  onReviewFile?: (target: FileEditReviewTarget) => void;
  currentRunDetail?: RunDetailView;
}) {
  const query = useQuery({ queryKey: taskReviewKey(threadID), queryFn: ({ signal }) => readThreadReview(client, threadID, signal), refetchOnMount: "always" });
  const review = query.data;
  const feedback = (source: string) => onFeedback(`请根据以下任务审阅来源继续修改：\n${source}\n任务：${threadID}\n请核对当前文件，保留无关修改。\n具体要求：`);

  const currentRunChecks = review?.checks.filter((c) => c.run_id === review.current_run_id) ?? [];
  const historicalChecks = review?.checks.filter((c) => c.run_id !== review.current_run_id) ?? [];
  const totalChecks = review?.checks.length ?? 0;
  const passedChecksCount = review?.checks.filter((c) => isCheckPassed(c.outcome)).length ?? 0;
  const failedChecksCount = review?.checks.filter((c) => isCheckFailed(c.outcome)).length ?? 0;
  const allPassed = totalChecks > 0 && passedChecksCount === totalChecks;

  return <section className="v2-task-delivery" aria-label="整个任务的审阅">
    <div className="v2-delivery-heading"><div><h2>任务改动</h2><p className="v2-delivery-muted">先核对改动，再选择要提交的文件与交付流程。</p></div><button disabled={query.isFetching} onClick={() => void query.refetch()} type="button">
      <RefreshCw size={15} aria-hidden="true" />{query.isFetching ? "正在核对…" : "刷新当前状态"}</button></div>
    {query.isLoading && <LoadingState label="正在汇总任务改动并核对当前目录…" />}
    {query.isError && <ErrorState error={query.error} />}
    {review && <>
      {query.isError && <p role="alert">以下为上次读取的结果，当前状态尚未确认。</p>}
      <div className="v2-delivery-summary">
        <div className="v2-overview-location"><FolderOpen size={18} aria-hidden="true" />
          <strong>{review.target.root_path?.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || "目录待确认"}</strong>
          <span>{review.target.kind === "drydock" ? "隔离工作目录" : review.target.kind === "source" ? "项目来源目录" : "目录类型待确认"}</span>
          {review.revision.repository_kind === "none" ? <span>尚未使用 Git</span> : <span><GitBranch size={14} aria-hidden="true" />{review.revision.branch || "未确认分支"}</span>}
        </div>
        <div className="v2-overview-counts" aria-label="已记录的任务范围">
          <span><strong>{review.applied_changes.length}</strong> 条已应用编辑</span>
          <span><strong>{review.unapplied_changes.length}</strong> 条未应用提案</span>
          <span><strong>{totalChecks}</strong> 项检查记录{failedChecksCount > 0 ? `（${failedChecksCount} 项未通过）` : allPassed ? `（全部通过）` : ""}</span>
        </div>
        <details className="v2-delivery-details"><summary>目录与版本详情</summary><dl>
          <div><dt>实际执行目录</dt><dd>{review.target.root_path || "尚无法确认"}</dd></div>
          <div><dt>当前提交</dt><dd><code>{review.revision.head || "未确认提交"}</code></dd></div>
          <div><dt>读取时间</dt><dd>{new Date(review.observed_at).toLocaleString()} · 覆盖 {review.runs.length} / {review.total_runs} 次执行</dd></div></dl>
        </details>
        {review.partial && <p role="status">此审阅覆盖部分记录。请在执行记录和提交页面核对其余改动与检查。</p>}
        <p className="v2-delivery-muted">下方汇总已记录的文件编辑。命令、手动编辑等产生的其他变化，请在提交页面核对。</p>
        {(review.reasons.length > 0 || review.revision.reasons.length > 0) && <details><summary>审阅范围与缺失信息</summary>
          <ul>{[...new Set([...review.reasons, ...review.revision.reasons])].map((reason) => <li key={reason}>{reason}</li>)}</ul></details>}
      </div>
      <div className="v2-overview-next">
        <div className="v2-overview-next-label">
          <strong>交付与下一步操作</strong>
          <span>查看当前目录的全部改动，选择本次提交范围、进入 PR 或查看完整检查报告。</span>
        </div>
        <div className="v2-overview-next-actions">
          {onGit && <button className="v2-delivery-primary" onClick={onGit} type="button">
            选择文件并提交<ArrowRight size={15} aria-hidden="true" /></button>}
          {onPullRequest && <button className="v2-delivery-secondary" onClick={onPullRequest} type="button">
            <GitPullRequest size={15} aria-hidden="true" />PR 状态与交付</button>}
          {onChecks && <button className="v2-delivery-secondary" onClick={onChecks} type="button">
            <ShieldCheck size={15} aria-hidden="true" />完整检查与环境</button>}
        </div>
      </div>
      {currentRunDetail?.plan_delivery && <section className="v2-overview-plan-delivery" aria-label="交付计划">
        <h3>当前执行的交付计划</h3>
        <PlanDeliveryPanel client={client} detail={currentRunDetail}
          key={`plan:${review.current_run_id}`} state={currentRunDetail.plan_delivery} threadID={threadID} />
      </section>}
      <div className="v2-overview-review-grid"><section aria-label="任务编辑">
      <h3>尚未应用的提案 <span>({review.unapplied_changes.length})</span></h3>
      {!review.unapplied_changes.length && <p>没有已记录的待应用提案。</p>}
      {[...review.unapplied_changes].sort((left, right) => Number(right.run_id === review.current_run_id) - Number(left.run_id === review.current_run_id))
        .map((change) => <TaskChange key={`${change.run_id}:${change.edit_id}`} change={change} observedAt={review.observed_at}
          onFeedback={feedback} onReviewFile={onReviewFile} currentRunID={review.current_run_id} />)}
      <h3>已应用的编辑 <span>({review.applied_changes.length})</span></h3>
      {!review.applied_changes.length && <p>尚无已记录的应用编辑。工作目录的其他改动可在提交页面核对。</p>}
      {review.applied_changes.map((change) => <TaskChange key={`${change.run_id}:${change.edit_id}`} change={change} observedAt={review.observed_at} onFeedback={feedback} />)}
      </section><section aria-label="任务检查">
      <h3>检查结果 <span>({review.checks.length})</span></h3>
      {!review.checks.length && <p>尚无已记录的检查结果。</p>}
      {currentRunChecks.length > 0 && <div className="v2-delivery-checks-group">
        <h4>当前执行 <span>({currentRunChecks.length})</span></h4>
        <div className="v2-delivery-checks">{currentRunChecks.map((check) => <CheckCard key={`${check.run_id}:${check.source_kind}:${check.id}`}
          check={check} observedAt={review.observed_at} isFetching={query.isFetching || query.isError} onFeedback={feedback} />)}</div>
      </div>}
      {historicalChecks.length > 0 && <div className="v2-delivery-checks-group">
        <h4>历史执行 <span>({historicalChecks.length})</span></h4>
        <div className="v2-delivery-checks">{historicalChecks.map((check) => <CheckCard key={`${check.run_id}:${check.source_kind}:${check.id}`}
          check={check} observedAt={review.observed_at} isFetching={query.isFetching || query.isError} onFeedback={feedback} />)}</div>
      </div>}
      </section></div>
    </>}
  </section>;
}

function CheckCard({ check, observedAt, isFetching, onFeedback }: {
  check: ThreadReview["checks"][number]; observedAt: string; isFetching: boolean; onFeedback: (context: string) => void;
}) {
  return <article key={`${check.run_id}:${check.source_kind}:${check.id}`}>
    <div><strong>{check.title}</strong><StatusBadge status={check.outcome} />
      <span className={`v2-delivery-freshness ${isFetching ? "unavailable" : check.revision_state}`}>
        {isFetching ? "上次检查记录，当前版本尚未确认" : freshness[check.revision_state] ?? "无法核对版本"}</span></div>
    <p>{check.revision_state === "unbound" ? "检查记录未绑定代码版本。请对当前代码重新运行检查，再确认结果。" : check.reason}</p>
    <details><summary>来源与时间</summary><p>{check.run_id} · {check.id} · {new Date(check.recorded_at).toLocaleString()}</p>
      {check.exit_code !== undefined && <p>实际退出码：{check.exit_code}</p>}</details>
    <button type="button" onClick={() => onFeedback(`检查：${check.title}\n结果：${check.outcome}\n版本状态：${check.revision_state}\n来源执行：${check.run_id}\n记录：${check.id}\n绑定版本：${check.recorded_revision_sha256 || "未保存"}\n读取时间：${observedAt}`)}>引用检查并继续修复</button>
  </article>;
}
function TaskChange({ change, observedAt, onFeedback, onReviewFile, currentRunID }: {
  change: ThreadReviewChange; observedAt: string; onFeedback: (context: string) => void;
  onReviewFile?: (target: FileEditReviewTarget) => void; currentRunID?: string;
}) {
  const source = `文件：${change.path}${change.destination_path ? ` → ${change.destination_path}` : ""}\n来源执行：${change.run_id}\n目录：${change.workspace_id}\n编辑记录：${change.edit_id}\n原版本：${change.original_sha256}\n提案版本：${change.proposed_sha256}\n当前观测版本：${change.current_sha256 || "不可用"}\n读取时间：${observedAt}`;
  return <article className="v2-delivery-change"><header><strong>{change.path}{change.destination_path && ` → ${change.destination_path}`}</strong>
    <StatusBadge status={change.status} /><span>{change.status !== "applied" && change.current_match === "changed" ? "当前内容尚不同于提案" : change.status !== "applied" && change.current_match === "matches" ? "当前内容与提案相同，仍以应用记录为准" : currentLabels[change.current_match] ?? "尚无法核对"}</span>
    {onReviewFile && <><span>{change.run_id === currentRunID ? "当前执行的提案" : "历史执行的提案"}</span>
      <button className="v2-delivery-primary" type="button" onClick={() => onReviewFile({ runID: change.run_id,
        editID: change.edit_id, workspaceID: change.workspace_id })}>查看并审阅</button></>}
    <button type="button" onClick={() => onFeedback(source)}>引用文件</button></header>
    <details><summary>查看这次编辑的差异</summary>
      {(change.diff_truncated || change.redacted) && <p>此差异{change.diff_truncated ? "已截断" : ""}{change.redacted ? "已脱敏" : ""}，只引用可见内容。</p>}
      {change.diff ? <ReviewDiff patch={change.diff} source={source} onFeedback={onFeedback} /> : <p>未保存可显示的文本差异。</p>}
      <details><summary>编辑来源</summary><pre>{source}</pre></details>
    </details></article>;
}
