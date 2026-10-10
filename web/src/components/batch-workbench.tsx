import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { APIRequestError, type APIClient } from "../api/client";
import type { BatchWorkbenchPrepareRequestView, BatchWorkbenchView, ChildTaskProposalView } from "../api/types";
import { useLocale } from "../lib/locale";
import { ErrorState, LoadingState } from "./common";
import "./batch-workbench.css";

const version = "batch-delivery-workbench.v1";
type PrepareIntent = { key: string; body: BatchWorkbenchPrepareRequestView };
type ChildIntent = { key: string; kind: "owner" | "execute"; generation: number; retry: boolean };

function storageKey(client: APIClient, runID: string, suffix: string) {
  return `universal-code.batch-workbench.v1:${client.baseURL}:${runID}:${suffix}`;
}
function retain(key: string, intent: PrepareIntent | ChildIntent | null) {
  try { if (intent) sessionStorage.setItem(key, JSON.stringify(intent)); else sessionStorage.removeItem(key); } catch { /* In-memory request still retains the original identity. */ }
}
function readIntent<T>(key: string, validate: (value: unknown) => boolean): T | null {
  try { const value: unknown = JSON.parse(sessionStorage.getItem(key) ?? "null"); return validate(value) ? value as T : null; } catch { return null; }
}
function isKey(value: unknown): value is { key: string } {
  return Boolean(value && typeof value === "object" && "key" in value && typeof value.key === "string" &&
    /^web-batch-[a-z]+-[a-f0-9-]{36}$/u.test(value.key));
}
function isPrepareIntent(value: unknown): boolean {
  if (!isKey(value) || !("body" in value) || !value.body || typeof value.body !== "object") return false;
  const body = value.body as Partial<BatchWorkbenchPrepareRequestView>;
  return body.version === version && body.confirm === true && typeof body.proposal_id === "string" &&
    Array.isArray(body.tasks) && body.tasks.length > 0 && body.tasks.length <= 2 && body.tasks.every((task, index) =>
      task.ordinal === index + 1 && Array.isArray(task.ownership_hints) && task.ownership_hints.length <= 32 &&
      task.ownership_hints.every((hint) => typeof hint.path === "string" && ["file", "directory"].includes(hint.kind)) &&
      Array.isArray(task.validations) && task.validations.length <= 16);
}
function isChildIntent(value: unknown): boolean {
  return isKey(value) && "generation" in value && Number.isSafeInteger(value.generation) &&
    Number(value.generation) >= 1 && Number(value.generation) <= 8 && "kind" in value &&
    ["owner", "execute"].includes(String(value.kind)) && "retry" in value && typeof value.retry === "boolean";
}

export function BatchPreparation({ client, runID, usedProposalIDs, onReviewProposals }: {
  client: APIClient; runID: string; usedProposalIDs: string[]; onReviewProposals: () => void;
}) {
  const { t } = useLocale();
  const cache = useQueryClient();
  const retentionKey = storageKey(client, runID, "prepare");
  const [intent, setIntent] = useState(() => readIntent<PrepareIntent>(retentionKey, isPrepareIntent));
  const [selected, setSelected] = useState(intent?.body.proposal_id ?? "");
  const query = useQuery({ queryKey: ["run", runID, "child-tasks"],
    queryFn: ({ signal }) => client.getRunChildTaskProposals(runID, signal) });
  const action = useMutation({ mutationFn: (request: PrepareIntent) =>
    client.prepareBatchWorkbench(runID, request.body, request.key),
    onSuccess: (result) => {
      retain(retentionKey, null); setIntent(null);
      cache.setQueryData(["run", runID, "batch-workbench", result.snapshot.plan.id], result);
      void cache.invalidateQueries({ queryKey: ["run", runID, "batch-deliveries"] });
    },
  });
  const candidates = query.data?.items.filter((proposal) => proposal.surface === "core" &&
    proposal.status === "approved" && proposal.tasks.length <= 2 &&
    proposal.assignments.length === proposal.tasks.length &&
    proposal.assignments.every((assignment) => assignment.status === "admitted") && !usedProposalIDs.includes(proposal.id)) ?? [];
  const proposal = candidates.find((candidate) => candidate.id === selected) ?? candidates[0];
  const submit = (body: BatchWorkbenchPrepareRequestView) => {
    const request = { key: `web-batch-prepare-${crypto.randomUUID()}`, body };
    retain(retentionKey, request); setIntent(request); action.mutate(request);
  };
  return <section className="batch-workbench-preparation" aria-label={t("准备独立任务", "Prepare isolated tasks")}>
    <div className="section-heading"><h3>{t("准备独立任务", "Prepare isolated tasks")}</h3>
      <button type="button" className="compact-command" onClick={onReviewProposals}>{t("审阅子任务提案", "Review child proposals")}</button></div>
    <p>{t("选择已准入的子任务，填写负责的项目路径。准备完成后，逐项启动、检查交付并合并。",
      "Choose admitted child tasks and assign their project paths. After preparation, start each task, inspect its delivery, and merge.")}</p>
    <p>{t("每次启动处理最多 4 个小型文本文件。为任务选择完整内容可放入一次指令的负责路径。",
      "Each start handles up to 4 small text files. Choose owned paths whose complete contents fit one instruction.")}</p>
    {query.isLoading && <LoadingState label={t("读取子任务", "Reading child tasks")} />}
    {query.isError && <><ErrorState error={query.error} /><button type="button" className="compact-command" onClick={() => void query.refetch()}>{t("重新读取子任务", "Reload child tasks")}</button></>}
    {intent ? <div className="batch-workbench-recovery" role="status">
      <p>{t("原准备请求已保存。选择“核对准备结果”，继续确认这次请求。", "The original preparation request is saved. Choose Check preparation result to confirm this request.")}</p>
      <ul>{intent.body.tasks.map((task) => <li key={task.ordinal}>#{task.ordinal}: {task.ownership_hints.map((hint) => hint.path).join(", ")}</li>)}</ul>
      <button type="button" className="command-button" disabled={action.isPending} onClick={() => action.mutate(intent)}>{t("核对准备结果", "Check preparation result")}</button>
      {action.error instanceof APIRequestError && action.error.status >= 400 && action.error.status < 500 &&
        <button type="button" className="compact-command" onClick={() => { retain(retentionKey, null); setIntent(null); action.reset(); }}>{t("调整准备请求", "Revise preparation request")}</button>}
    </div> : proposal ? <>
      <label>{t("子任务提案", "Child task proposal")}<select value={proposal.id} onChange={(event) => setSelected(event.target.value)}>
        {candidates.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.tasks.map((task) => task.title).join(" / ")}</option>)}
      </select></label>
      <BatchPreparationForm key={proposal.id} proposal={proposal} hostValidation={client.hasBatchDeliveryHostValidation}
        pending={action.isPending} onPrepare={submit} />
    </> : !query.isLoading && !query.isError && <p>{t("先在“审阅子任务提案”中批准并准入 1–2 个独立任务，再回来准备负责路径。", "Approve and admit 1–2 isolated tasks in Review child proposals, then return to assign their paths.")}</p>}
    {action.isError && <p className="inline-warning" role="alert">{action.error instanceof Error ? action.error.message : t("准备结果读取失败", "Preparation result could not be read")}</p>}
  </section>;
}

function BatchPreparationForm({ proposal, hostValidation, pending, onPrepare }: {
  proposal: ChildTaskProposalView; hostValidation: boolean; pending: boolean;
  onPrepare: (body: BatchWorkbenchPrepareRequestView) => void;
}) {
  const { t } = useLocale();
  const [paths, setPaths] = useState<Record<number, string>>({});
  const [kinds, setKinds] = useState<Record<number, "file" | "directory">>({});
  const [goScopes, setGoScopes] = useState<Record<number, string>>({});
  const [npmScopes, setNpmScopes] = useState<Record<number, string>>({});
  const [confirmed, setConfirmed] = useState(false);
  const hasPaths = proposal.tasks.every((task) => (paths[task.ordinal] ?? "").trim());
  return <form className="batch-workbench-form" onSubmit={(event) => {
    event.preventDefault();
    if (!confirmed || !hasPaths || pending) return;
    onPrepare({ version, proposal_id: proposal.id, confirm: true, tasks: proposal.tasks.map((task) => ({
      ordinal: task.ordinal, ownership_hints: (paths[task.ordinal] ?? "").split(/\r?\n/u).map((path) => path.trim()).filter(Boolean)
        .map((path) => ({ path, kind: kinds[task.ordinal] ?? "file" })),
      validations: [{ id: "diff", kind: "git_diff_check", scope: "." },
        ...(goScopes[task.ordinal]?.trim() ? [{ id: "go", kind: "go_test", scope: goScopes[task.ordinal].trim() }] : []),
        ...(npmScopes[task.ordinal]?.trim() ? [{ id: "npm", kind: "npm_test", scope: npmScopes[task.ordinal].trim() }] : [])],
    })) });
  }}>
    {proposal.tasks.map((task) => <fieldset key={task.ordinal} disabled={pending}>
      <legend>#{task.ordinal} {task.title}</legend><p>{task.goal}</p>
      <p className="projection-placeholder">{t(`${task.turn_limit} 回合，${task.token_limit} Tokens，${Math.ceil(task.timeout_millis / 60_000)} 分钟`,
        `${task.turn_limit} turns, ${task.token_limit} tokens, ${Math.ceil(task.timeout_millis / 60_000)} minutes`)}</p>
      {task.dependency_ordinals.length > 0 && <p>{t(`等待任务 ${task.dependency_ordinals.join(", ")} 验收后开始`, `Starts after task ${task.dependency_ordinals.join(", ")} is accepted`)}</p>}
      <label>{t("负责路径（每行一个）", "Owned paths (one per line)")}<textarea rows={3} required value={paths[task.ordinal] ?? ""}
        placeholder="internal/parser/example.go" onChange={(event) => setPaths({ ...paths, [task.ordinal]: event.target.value })} /></label>
      <label>{t("路径类型", "Path type")}<select value={kinds[task.ordinal] ?? "file"} onChange={(event) => setKinds({ ...kinds, [task.ordinal]: event.target.value as "file" | "directory" })}>
        <option value="file">{t("文件", "File")}</option><option value="directory">{t("目录与其中的文件", "Directory and its files")}</option>
      </select></label>
      <p>{t("交付时检查完整 Git 差异与干净提交。", "Delivery checks the complete Git diff and clean commits.")}</p>
      {hostValidation && <details><summary>{t("添加 Go/npm 验证", "Add Go/npm validation")}</summary>
        <p>{t("这些验证会在本机执行子任务提交的代码。", "These validations run the child’s committed code on this computer.")}</p>
        <label>{t("Go 验证范围", "Go validation scope")}<input value={goScopes[task.ordinal] ?? ""} placeholder="internal/parser" onChange={(event) => setGoScopes({ ...goScopes, [task.ordinal]: event.target.value })} /></label>
        <label>{t("npm 项目路径", "npm project path")}<input value={npmScopes[task.ordinal] ?? ""} placeholder="web" onChange={(event) => setNpmScopes({ ...npmScopes, [task.ordinal]: event.target.value })} /></label>
      </details>}
    </fieldset>)}
    <label className="checkbox-row"><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} />
      {t("已核对任务、负责路径和验证范围，准备独立工作区", "I reviewed the tasks, owned paths, and validations; prepare independent workspaces")}</label>
    <button type="submit" className="command-button" disabled={pending || !confirmed || !hasPaths}>{t("准备独立任务", "Prepare isolated tasks")}</button>
  </form>;
}

export function BatchWorkbenchControls({ client, runID, planID, ordinal }: {
  client: APIClient; runID: string; planID: string; ordinal: number;
}) {
  const { t } = useLocale();
  const cache = useQueryClient();
  const retentionKey = storageKey(client, runID, `${planID}/${ordinal}`);
  const [intent, setIntent] = useState(() => readIntent<ChildIntent>(retentionKey, isChildIntent));
  const query = useQuery({ queryKey: ["run", runID, "batch-workbench", planID],
    queryFn: ({ signal }) => client.getBatchWorkbench(runID, planID, signal),
    refetchInterval: (state) => state.state.data?.children.some((child) => child.executing) ? 1500 : false });
  const invalidate = (result: BatchWorkbenchView) => {
    cache.setQueryData(["run", runID, "batch-workbench", planID], result);
    void cache.invalidateQueries({ queryKey: ["run", runID, "batch-delivery", planID] });
    void cache.invalidateQueries({ queryKey: ["run", runID, "batch-deliveries"] });
    void cache.invalidateQueries({ queryKey: ["run", runID, "agent-graph"] });
  };
  const action = useMutation({ mutationFn: (request: ChildIntent) => request.kind === "owner"
    ? client.recoverBatchWorkbenchOwner(runID, planID, ordinal, { version, confirm: true, retry: request.retry, expected_generation: request.generation }, request.key)
    : client.executeBatchWorkbenchChild(runID, planID, ordinal, { version, confirm: true, expected_generation: request.generation }, request.key),
    onSuccess: (result) => { retain(retentionKey, null); setIntent(null); invalidate(result); },
    onError: () => { void query.refetch(); },
  });
  if (query.isLoading) return <p role="status">{t("读取任务准备状态…", "Reading task readiness…")}</p>;
  if (query.isError || !query.data) return <div><ErrorState error={query.error} /><button type="button" className="compact-command" onClick={() => void query.refetch()}>{t("重新读取任务状态", "Reload task status")}</button></div>;
  const child = query.data.children.find((item) => item.ordinal === ordinal);
  const delivery = query.data.snapshot.children.find((item) => item.workspace.ordinal === ordinal);
  if (!child || !delivery) return null;
  const status = delivery.workspace.status;
  const active = ["dispatched", "acknowledged", "working", "question"].includes(status);
  const waiting = query.data.snapshot.plan.spec.tasks[ordinal - 1].dependency_ordinals.some((dependency) =>
    !query.data?.snapshot.children.some((item) => item.workspace.ordinal === dependency && ["accepted", "merged"].includes(item.workspace.status)));
  const submit = (kind: ChildIntent["kind"], retry = false) => {
    const request = { kind, retry, generation: child.generation, key: `web-batch-${kind}-${crypto.randomUUID()}` };
    retain(retentionKey, request); setIntent(request); action.mutate(request);
  };
  return <div className="batch-workbench-controls">
    {!query.data.worker_available && <p>{t("任务已准备。连接当前运行时的批量执行能力后，可在这里启动子任务。", "Tasks are prepared. Connect the current runtime’s batch execution capability to start a child here.")}</p>}
    {child.executing && <p role="status">{t("子任务正在独立工作区执行。完成后检查交付结果。", "The child is working in its isolated workspace. Inspect the delivery when it completes.")}</p>}
    {active && !child.executing && <p>{t("每次启动使用一次已计费的 Agent 回合，按负责路径中的最多 4 个小型文本文件生成修改并提交。", "Each start uses one accounted Agent turn to revise and commit up to 4 small text files in the owned paths.")}</p>}
    {child.outcome_unresolved && <p>{t("上次执行结果需要核对。刷新交付记录，或恢复新的代次后继续。", "The previous execution needs confirmation. Refresh its delivery record, or recover a new generation to continue.")}</p>}
    {waiting && <p>{t("先验收依赖任务，再启动这个任务。", "Accept the dependency task before starting this task.")}</p>}
    {intent ? <div role="status"><p>{t("原操作已保存。核对结果会沿用这次请求。", "The original operation is saved. Checking the result keeps this request’s identity.")}</p>
      <button type="button" className="command-button" disabled={action.isPending} onClick={() => action.mutate(intent)}>{t("核对任务操作结果", "Check child operation result")}</button>
      {action.error instanceof APIRequestError && action.error.status >= 400 && action.error.status < 500 &&
        <button type="button" className="compact-command" onClick={() => { retain(retentionKey, null); setIntent(null); action.reset(); }}>{t("查看当前任务并继续", "Continue from the current task")}</button>}
    </div> : <>
      {active && child.owner_available && !child.outcome_unresolved && <button type="button" className="command-button"
        disabled={action.isPending || child.executing || !query.data.worker_available || waiting}
        onClick={() => submit("execute")}>{t("启动子任务并返回交付", "Start child and return delivery")}</button>}
      {active && (!child.owner_available || child.outcome_unresolved) && child.generation < 8 &&
        <button type="button" className="command-button" disabled={action.isPending || child.executing} onClick={() => submit("owner")}>{t("恢复任务所有权", "Recover child ownership")}</button>}
      {status === "changes_requested" && child.generation < 8 && <>
        <p>{t("审阅意见已保存。准备返工后，启动子任务按意见修改并重新交付。", "Review feedback is saved. Prepare rework, then start the child to revise and return a new delivery.")}</p>
        <button type="button" className="command-button" disabled={action.isPending || child.executing} onClick={() => submit("owner", true)}>{t("按审阅意见准备返工", "Prepare rework from review feedback")}</button>
      </>}
    </>}
    {action.isPending && <p role="status">{t("正在处理子任务操作…", "Processing child operation…")}</p>}
    {action.isError && <p className="inline-warning" role="alert">{action.error instanceof Error ? action.error.message : t("操作结果读取失败", "Operation result could not be read")}</p>}
    <button type="button" className="compact-command" disabled={query.isFetching} onClick={() => void query.refetch()}>{t("刷新任务进度", "Refresh child progress")}</button>
  </div>;
}
