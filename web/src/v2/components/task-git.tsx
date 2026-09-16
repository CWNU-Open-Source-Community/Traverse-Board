import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { CyberAgentClient } from "../../api/client";
import type { WorkspaceView } from "../../api/types";
import { executeThreadGit, observeThreadGit, previewThreadGit, readThreadGit, type ThreadGitPreview, type ThreadGitResult, type ThreadGitSpec } from "../../api/task-delivery";
import { ErrorState, LoadingState, StatusLabel } from "../../components/common";
import { useV2PersistentState, useV2RecoveryStore } from "../recovery-storage";
import { ReviewDiff } from "./review-diff";
import { taskReviewKey } from "./task-overview";
import { gitFormRepositoryID, gitFormRevision, useGitFormState } from "./git-form-state";
import "./task-delivery.css";
import "./task-git.css";

const operations = { commit: "提交所选文件", stage: "暂存所选文件", unstage: "取消所选文件暂存", create_branch: "创建并切换分支", switch_branch: "切换已有分支", worktree_create: "创建独立工作目录", push_branch: "推送当前分支" };
type Operation = keyof typeof operations;
const directoryName = (path: string) => path.replace(/[\\/]+$/u, "").split(/[\\/]/u).pop() || path;
interface GitAttempt { threadID: string; runID: string; key: string; operation: Operation }
const validAttempt = (value: unknown, threadID: string): value is GitAttempt => Boolean(value && typeof value === "object" &&
  "threadID" in value && value.threadID === threadID && "runID" in value && typeof value.runID === "string" &&
  "key" in value && typeof value.key === "string" && /^task-git-[\w-]+$/u.test(value.key) &&
  "operation" in value && typeof value.operation === "string" && value.operation in operations);
export const taskGitKey = (threadID: string) => ["thread", threadID, "git"] as const;

export function TaskGit({ client, threadID, working, onFeedback, onPullRequest, onOpenWorktree }: {
  client: CyberAgentClient; threadID: string; working: boolean; onFeedback: (context: string) => void; onPullRequest: () => void;
  onOpenWorktree?: (workspace: WorkspaceView) => void;
}) {
  const queryClient = useQueryClient();
  const store = useV2RecoveryStore();
  const storageKey = `thread:${threadID}:git-attempt`;
  const [saved, setSaved] = useV2PersistentState<GitAttempt | null>(storageKey, null);
  const [legacyMessage, saveMessage] = useV2PersistentState(`thread:${threadID}:git-message`, "");
  const [legacyBranch, saveBranch] = useV2PersistentState(`thread:${threadID}:git-branch`, "");
  const [legacyWorktreeName, saveWorktreeName] = useV2PersistentState(`thread:${threadID}:git-worktree-name`, "");
  const [preview, setPreview] = useState<ThreadGitPreview | null>(null);
  const [result, setResult] = useState<ThreadGitResult | null>(null);
  const [localError, setLocalError] = useState("");
  const [checking, setChecking] = useState(false);
  const validSaved = saved !== null && validAttempt(saved, threadID) ? saved : null;
  const damaged = saved !== null && !validSaved;
  const state = useQuery({ queryKey: taskGitKey(threadID), queryFn: ({ signal }) => readThreadGit(client, threadID, signal), refetchOnMount: "always" });
  const form = useGitFormState(client.baseURL, threadID, state.data,
    { message: legacyMessage, branch: legacyBranch, worktreeName: legacyWorktreeName });
  const { operation, paths, message, branch, worktreeName, remote, credentialName } = form.value;
  const setOperation = (value: Operation) => form.update({ operation: value });
  const setPaths = (change: string[] | ((previous: string[]) => string[])) => form.update((previous) => ({ ...previous,
    paths: typeof change === "function" ? change(previous.paths) : change }));
  const setMessage = (value: string) => { form.update({ message: value }); saveMessage(value); };
  const setBranch = (value: string) => { form.update({ branch: value }); saveBranch(value); };
  const setWorktreeName = (value: string) => { form.update({ worktreeName: value }); saveWorktreeName(value); };
  const setRemote = (value: string) => form.update({ remote: value });
  const setCredentialName = (value: string) => form.update({ credentialName: value });
  const generation = useRef(0);
  const mounted = useRef(true);
  const preflight = useRef<AbortController | null>(null);
  const live = useRef({ threadID, repositoryID: form.repositoryID, form: JSON.stringify(form.value), working });
  live.current = { threadID, repositoryID: form.repositoryID, form: JSON.stringify(form.value), working };
  const previousRepository = useRef(form.repositoryID);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; generation.current++; preflight.current?.abort(); };
  }, []);
  useEffect(() => {
    if (previousRepository.current && form.repositoryID && previousRepository.current !== form.repositoryID) {
      generation.current++; preflight.current?.abort(); setPreview(null); setResult(null);
      setLocalError("当前任务的实际操作目录已变化。原目录的表单已保留，请在当前目录重新选择并预览。");
    }
    previousRepository.current = form.repositoryID;
  }, [form.repositoryID]);
  const credentials = useQuery({ queryKey: ["github-review", "connections"], queryFn: ({ signal }) => client.githubReviewConnections(false, signal), enabled: client.hasGitHubReviewControl && operation === "push_branch" });
  const openWorktree = useMutation({ mutationFn: (path: string) => client.importWorkspace(path),
    onSuccess: (value) => onOpenWorktree?.(value.workspace) });
  const observe = useQuery({ queryKey: ["thread", threadID, "git-request", validSaved?.key],
    queryFn: ({ signal }) => observeThreadGit(client, threadID, validSaved!.key, signal), enabled: Boolean(validSaved), retry: false, refetchOnMount: "always" });
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: taskGitKey(threadID) });
    void queryClient.invalidateQueries({ queryKey: taskReviewKey(threadID) });
  };
  const prepare = useMutation({ mutationFn: async (spec: ThreadGitSpec) => {
    const guard = beginCheck();
    const fresh = await readThreadGit(client, threadID, guard.signal);
    ensureCurrent(guard);
    queryClient.setQueryData(taskGitKey(threadID), fresh);
    if (gitFormRevision(fresh) !== gitFormRevision(state.data!)) {
      throw new Error("仓库内容、分支或目录已变化。已保留原表单，请核对当前状态后重新预览。");
    }
    if (!fresh.can_execute) throw new Error(fresh.blocked_reason || "当前仓库不能执行 Git 操作。");
    if (spec.paths?.some((path) => !fresh.changes.some((file) => file.path === path))) throw new Error("部分所选文件已不在当前列表，请核对后重新预览。");
    const value = await previewThreadGit(client, threadID, fresh.run_id, spec);
    ensureCurrent(guard);
    if (gitFormRevision(value) !== gitFormRevision(fresh)) throw new Error("生成预览期间仓库已变化，请刷新并重新预览。");
    form.update({ basis: gitFormRevision(fresh), previewed: true });
    setPreview(value);
    return value;
  } });
  const execute = useMutation({
    mutationFn: ({ reviewed, attempt }: { reviewed: ThreadGitPreview; attempt: GitAttempt; formSnapshot: string }) => executeThreadGit(client, threadID, {
      version: "thread_git.v1", run_id: reviewed.run_id, spec: reviewed.spec, operation_key: attempt.key,
      expected_preview_fingerprint: reviewed.preview_fingerprint, requested_by: "local-operator",
    }),
    onSuccess: (value, { attempt, formSnapshot }) => {
      setResult(value);
      queryClient.setQueryData(["thread", threadID, "git-request", attempt.key], value);
      if (value.state === "completed") {
        clearAttempt(attempt, formSnapshot);
      }
    },
    onSettled: (_data, _error, { attempt }) => {
      setPreview(null); invalidate();
      void queryClient.invalidateQueries({ queryKey: ["thread", threadID, "git-request", attempt.key] });
    },
  });
  function clearAttempt(attempt: GitAttempt, expectedForm = JSON.stringify(form.value)) {
    try {
      const persisted = store?.read<GitAttempt | null>(storageKey, null);
      if (persisted?.key === attempt.key) {
        store!.write(storageKey, null); setSaved(null);
        // Consume only the form that was confirmed; a later editor keeps its
        // selection and preview basis even when this response arrives late.
        form.update((current) => JSON.stringify(current) === expectedForm
          ? { ...current, paths: [], basis: "", previewed: false } : current);
      }
      else if (persisted) throw new Error("本机记录已变化，请先核对另一个窗口中的操作。");
    } catch (error) { setLocalError(error instanceof Error ? error.message : "无法更新原操作记录。"); }
  }
  function beginCheck() {
    preflight.current?.abort();
    preflight.current = new AbortController();
    return { ...live.current, generation: generation.current, signal: preflight.current.signal };
  }
  function ensureCurrent(guard: ReturnType<typeof beginCheck>) {
    if (!mounted.current || guard.signal.aborted || generation.current !== guard.generation ||
      guard.threadID !== live.current.threadID || guard.repositoryID !== live.current.repositoryID ||
      guard.form !== live.current.form || live.current.working) {
      throw new Error("页面或操作表单已变化，未执行原操作。请重新预览。");
    }
  }
  async function confirm() {
    if (!preview?.can_execute || !store || saved || execute.isPending || checking || prepare.isPending || working) return;
    const reviewed = preview;
    const guard = beginCheck();
    setChecking(true);
    try {
      const fresh = await readThreadGit(client, threadID, guard.signal);
      ensureCurrent(guard);
      queryClient.setQueryData(taskGitKey(threadID), fresh);
      if (!fresh.can_execute || gitFormRevision(fresh) !== gitFormRevision(reviewed)) {
        setPreview(null);
        throw new Error(fresh.blocked_reason || "仓库内容、分支或目录已变化，原确认已失效。请重新预览后再确认。");
      }
      if (reviewed.spec.paths?.some((path) => !fresh.changes.some((file) => file.path === path))) {
        throw new Error("部分所选文件已不在当前列表，原确认已失效。请重新选择并预览。");
      }
      const attempt: GitAttempt = { threadID, runID: reviewed.run_id, key: `task-git-${crypto.randomUUID()}`, operation: reviewed.spec.operation as Operation };
      store.assertReadable();
      if (store.read(storageKey, null)) throw new Error("本机已有待核对操作，请刷新后核对。");
      store.write(storageKey, attempt); setSaved(attempt); setResult(null); setLocalError("");
      execute.mutate({ reviewed, attempt, formSnapshot: guard.form });
    } catch (error) {
      if (mounted.current && live.current.threadID === guard.threadID && live.current.repositoryID === guard.repositoryID) {
        setPreview(null);
        setLocalError(`重新核对未完成，尚未执行。${error instanceof Error ? error.message : "请刷新后重试。"}`);
      }
    } finally { if (mounted.current) setChecking(false); }
  }
  const changed = () => { generation.current++; preflight.current?.abort(); setPreview(null); prepare.reset(); setResult(null); };
  const selectedRemote = remote || state.data?.remotes.find((item) => item.name === "origin" && item.url)?.url || state.data?.remotes.find((item) => item.url)?.url || "";
  const needsFiles = ["commit", "stage", "unstage"].includes(operation);
  const missingPaths = paths.filter((path) => !state.data?.changes.some((file) => file.path === path));
  const unavailableRemote = operation === "push_branch" && Boolean(selectedRemote) && !state.data?.remotes.some((item) => item.url === selectedRemote);
  const unavailableBranch = operation === "switch_branch" && Boolean(branch) && !state.data?.branches.includes(branch);
  const drifted = Boolean(state.data && form.value.basis && form.value.basis !== gitFormRevision(state.data));
  const mutationBlocked = working || !client.hasControl || !state.data?.can_execute || state.isError || state.isFetching || Boolean(saved) || execute.isPending || checking;
  const shownResult = validSaved ? observe.data : result;
  const chooseOperation = (next: Operation) => { if (next !== operation) { changed(); setOperation(next); } };
  const operationLocked = Boolean(saved) || execute.isPending || prepare.isPending || checking;
  return <section className="v2-task-delivery v2-task-git" aria-label="任务 Git 流程">
    <div className="v2-delivery-heading"><h2>Git</h2><button type="button" disabled={state.isFetching} onClick={() => { changed(); void state.refetch(); }}>刷新仓库</button></div>
    {state.isLoading && <LoadingState label="正在读取实际工作目录的 Git 状态…" />}
    {state.isError && <ErrorState error={state.error} />}
    {state.data && <>
      <div className="v2-delivery-summary v2-git-location" aria-label="当前 Git 目录">
        <div className="v2-git-location-title"><strong>{directoryName(state.data.repository_root)}</strong>
          <span>{state.data.workspace_id === state.data.source_workspace_id ? "项目目录" : "隔离目录"}</span></div>
        <dl className="v2-git-location-facts">
          <div><dt>当前分支</dt><dd>{state.data.branch || "分离的 HEAD"}</dd></div>
          <div><dt>当前提交</dt><dd>{state.data.head_oid.slice(0, 8) || "尚无提交"}</dd></div>
        </dl>
        <details className="v2-git-evidence"><summary>完整位置与版本</summary><dl>
          <div><dt>操作目录</dt><dd>{state.data.repository_root}</dd></div>
          <div><dt>完整提交</dt><dd>{state.data.head_oid || "尚无提交"}</dd></div>
          <div><dt>目录身份</dt><dd>{state.data.workspace_id}</dd></div>
          {state.data.workspace_id !== state.data.source_workspace_id && <div><dt>来源项目身份</dt><dd>{state.data.source_workspace_id}</dd></div>}
        </dl></details>
      {state.data.truncated && <p role="status">文件列表已截断，只能选择当前已显示的文件。</p>}
      {!state.data.can_execute && <p role="status">当前不能操作：{state.data.blocked_reason || "仓库或执行环境尚未就绪。"}</p>}
      {working && <p role="status">请先停止当前任务执行，再修改 Git 状态。</p>}</div>
      {!store && <p role="status">当前连接尚未提供本机恢复身份，无法保存提交或推送的待核对请求。</p>}
      {damaged && <p role="alert">本机 Git 操作记录异常，已保留原记录；未执行新操作。</p>}
      {validSaved && <section className="v2-delivery-operation" aria-label="上次 Git 操作">
        <strong>核对上次{operations[validSaved.operation]}</strong>
        <p>{execute.isPending ? "请求正在处理。" : observe.isFetching ? "正在只读核对原操作…" : shownResult?.state === "completed" ? "已确认原操作完成。" : shownResult?.state === "not_received" ? "服务端没有收到原操作，可以重新预览后再执行。" : "结果尚未确认，暂不能开始新操作。"}</p>
        {observe.isError && <ErrorState error={observe.error} />}
        <button type="button" disabled={execute.isPending || observe.isFetching} onClick={() => void observe.refetch()}>只读核对原操作</button>
        {!execute.isPending && !observe.isFetching && !observe.isError && ["not_received", "completed"].includes(observe.data?.state ?? "") &&
          <button type="button" onClick={() => { setResult(observe.data!); clearAttempt(validSaved); invalidate(); }}>确认结果并继续</button>}
      </section>}
      {shownResult && <div role="status"><p>{shownResult.state === "completed" ? `已确认${operations[(shownResult.spec?.operation as Operation) ?? operation] ?? "操作"}完成` : shownResult.state === "unknown" ? "原操作结果尚未确认" : "原操作未收到"}</p>
        {(shownResult.commit_oid || shownResult.remote_oid || shownResult.operation_id) && <details className="v2-git-evidence"><summary>查看操作记录</summary>
          {shownResult.commit_oid && <p>提交：{shownResult.commit_oid}</p>}{shownResult.remote_oid && <p>远端提交：{shownResult.remote_oid}</p>}
          {shownResult.operation_id && <p>操作标识：{shownResult.operation_id}</p>}
          <p>{shownResult.receipt_saved ? "执行收据已保存。" : "执行收据尚未保存；是否完成以上方对原操作的核对状态为准。"}</p>
        </details>}
        {shownResult.reason && <p>{shownResult.reason}</p>}
        {shownResult.worktree_path && <><p>独立目录：{shownResult.worktree_path}</p><p>当前任务仍使用原目录。新目录从已提交版本创建，不包含本任务未提交的修改；在此开始新任务时不会复制当前任务的权限。</p>
          {onOpenWorktree && <button type="button" disabled={!client.hasWorkspaceImport || openWorktree.isPending} onClick={() => openWorktree.mutate(shownResult.worktree_path!)}>在此目录开始新任务</button>}</>}
        {shownResult.state === "completed" && shownResult.spec?.operation === "push_branch" && <button type="button" onClick={onPullRequest}>继续创建或查看 PR</button>}
      </div>}
      {localError && <p role="alert">{localError}</p>}
      {openWorktree.isError && <ErrorState error={openWorktree.error} />}
      {execute.isError && <p role="alert">操作响应未能确认，已保留原请求。{execute.error.message}</p>}
      {drifted && <p className="v2-git-form-notice" role="status">仓库版本已变化。文件选择和输入仍保留，请核对当前状态并重新预览；旧确认不能继续执行。</p>}
      {!drifted && form.value.previewed && !preview && <p className="v2-git-form-notice" role="status">已保留上次操作表单。预览确认不会随页面恢复，请重新预览本次操作。</p>}
      <form className="v2-delivery-form" onSubmit={(event) => {
        event.preventDefault(); if (mutationBlocked || prepare.isPending || (needsFiles && missingPaths.length) || unavailableRemote || unavailableBranch) return;
        const spec: ThreadGitSpec = { operation,
          ...(needsFiles ? { paths } : {}), ...(operation === "commit" ? { message } : {}),
          ...(["create_branch", "switch_branch", "worktree_create"].includes(operation) ? { branch } : {}),
          ...(operation === "worktree_create" ? { worktree_name: worktreeName } : {}),
          ...(operation === "push_branch" ? { branch: state.data!.branch, remote_url: selectedRemote, credential_name: credentialName } : {}),
        }; setLocalError(""); setPreview(null); prepare.mutate(spec);
      }}>
        <div className="v2-git-actions" role="group" aria-label="Git 操作">
          <button type="button" className="v2-git-main-action" aria-pressed={operation === "commit"} disabled={operationLocked} onClick={() => chooseOperation("commit")}>提交</button>
          <button type="button" aria-pressed={operation === "push_branch"} disabled={operationLocked} onClick={() => chooseOperation("push_branch")}>推送</button>
          <details className="v2-git-more-actions"><summary>分支、独立目录与暂存</summary>
            <div role="group" aria-label="分支与独立目录">{(["create_branch", "switch_branch", "worktree_create"] as const).map((value) =>
              <button type="button" key={value} aria-pressed={operation === value} disabled={operationLocked} onClick={() => chooseOperation(value)}>{operations[value]}</button>)}</div>
            <div role="group" aria-label="暂存管理">{(["stage", "unstage"] as const).map((value) =>
              <button type="button" key={value} aria-pressed={operation === value} disabled={operationLocked} onClick={() => chooseOperation(value)}>{operations[value]}</button>)}</div>
          </details>
        </div>
        <h3>{operations[operation]}</h3>
        {needsFiles && <p>以下包含手动编辑和其他任务的修改。仅处理你选中的文件，其他已暂存内容会保留。</p>}
        {needsFiles && missingPaths.length > 0 && <div className="v2-git-form-notice" role="alert"><p>以下已选文件不在当前可用列表中；可能已提交、移除或因列表截断未显示，不能直接用于本次操作。</p>
          <ul>{missingPaths.map((path) => <li key={path}>{path}</li>)}</ul>
          <button type="button" disabled={operationLocked} onClick={() => { changed(); setPaths((previous) => previous.filter((path) => !missingPaths.includes(path))); }}>移除这些不可用选择</button>
        </div>}
        {needsFiles && <fieldset disabled={mutationBlocked || prepare.isPending}><legend>选择文件（{paths.length}）</legend>
          {!state.data.changes.length && <p>当前没有 Git 文件变化。</p>}
          <div className="v2-git-files">{state.data.changes.map((file) => <label key={file.path}><input type="checkbox" checked={paths.includes(file.path)}
            onChange={(event) => { changed(); setPaths((previous) => event.target.checked ? [...previous, file.path] : previous.filter((path) => path !== file.path)); }} />
            <span><strong>{file.path}</strong><small>暂存 <StatusLabel status={file.staging || "—"} /> · 工作目录 <StatusLabel status={file.worktree || "—"} /></small></span></label>)}</div>
        </fieldset>}
        {operation === "commit" && <label>提交说明<textarea rows={3} required maxLength={4096} disabled={Boolean(saved) || prepare.isPending} value={message} onChange={(event) => { changed(); setMessage(event.target.value); }} /></label>}
        {["create_branch", "worktree_create"].includes(operation) && <label>新分支名称<input required value={branch} disabled={Boolean(saved) || prepare.isPending} onChange={(event) => { changed(); setBranch(event.target.value); }} placeholder="feature/my-change" /></label>}
        {operation === "worktree_create" && <><label>独立目录名称<input required value={worktreeName} disabled={Boolean(saved) || prepare.isPending} onChange={(event) => { changed(); setWorktreeName(event.target.value); }} placeholder="my-change" /></label>
          <p>从当前已提交版本创建独立分支和 worktree 目录。当前任务保持原目录与权限；创建后可在新目录开始新任务，权限需独立确认。</p></>}
        {operation === "switch_branch" && <label>目标分支<select required value={branch} disabled={Boolean(saved) || prepare.isPending} onChange={(event) => { changed(); setBranch(event.target.value); }}>
          <option value="">选择分支</option>{unavailableBranch && <option value={branch} disabled>原选择已不可用：{branch}</option>}{state.data.branches.filter((name) => name !== state.data!.branch).map((name) => <option key={name}>{name}</option>)}</select></label>}
        {operation === "push_branch" && <><label>推送目标<select required value={selectedRemote} disabled={Boolean(saved) || prepare.isPending} onChange={(event) => { changed(); setRemote(event.target.value); }}>
          {unavailableRemote && <option value={selectedRemote} disabled>原推送目标已不可用：{selectedRemote}</option>}
          {!state.data.remotes.some((item) => item.url) && <option value="">没有可用的推送目标</option>}{state.data.remotes.map((item) => <option key={`${item.name}:${item.url}`} value={item.url || `unavailable:${item.name}`} disabled={!item.url}>{item.name} · {item.url || item.blocked_reason || "此远端暂不支持"}</option>)}</select></label>
          <label>凭据名称<input list={`task-git-credentials-${threadID}`} value={credentialName} disabled={Boolean(saved) || prepare.isPending} onChange={(event) => { changed(); setCredentialName(event.target.value); }} placeholder="选择或填写已保存的 GitHub 凭据名称" /></label>
          <datalist id={`task-git-credentials-${threadID}`}>{credentials.data?.filter((item) => item.credential.configured && item.connection.credential.kind !== "github_app_device").map((item) => <option value={item.connection.credential.name} key={item.connection.id}>{item.connection.repository.full_name}</option>)}</datalist>
          <button type="button" onClick={onPullRequest}>到 PR 页面接入 GitHub</button>
          <p>只推送当前分支已提交的代码，工作目录中尚未提交的修改不包含在推送中。</p></>}
        <p className="v2-git-limits">内置 Git 操作不运行本地 hooks，提交不生成 GPG 或 SSH 签名。项目要求的本地检查与签名需另行完成。</p>
        {(unavailableRemote || unavailableBranch) && <p role="alert">原目标已不可用，请明确选择当前有效的目标后重新预览。</p>}
        <button type="submit" className="v2-delivery-primary" disabled={mutationBlocked || prepare.isPending || (needsFiles && (!paths.length || missingPaths.length > 0)) || unavailableRemote || unavailableBranch}>{prepare.isPending ? "正在生成预览…" : "预览本次操作"}</button>
      </form>
      {prepare.isError && <ErrorState error={prepare.error} />}
      {preview && <section className="v2-git-preview" aria-label="Git 操作确认">
        <h3>确认{operations[preview.spec.operation as Operation] ?? "Git 操作"}</h3>
        <p>目录：{directoryName(preview.repository_root)}</p><p>分支：{preview.branch || "分离的 HEAD"} · 基准：{preview.head_oid.slice(0, 8) || "尚无提交"}</p>
        {preview.spec.branch && <p>目标分支：{preview.spec.branch}</p>}{preview.spec.remote_url && <p>目标远端：{preview.spec.remote_url}</p>}
        {preview.target_commit_oid && <p>目标提交：{preview.target_commit_oid.slice(0, 8)}</p>}
        {preview.commit_author && <p>提交作者：{preview.commit_author.name} &lt;{preview.commit_author.email}&gt;</p>}
        {preview.spec.message && <pre>{preview.spec.message}</pre>}
        {preview.spec.operation === "commit" && <p>提交说明末尾会附加 Traverse-Operation 操作标识，用于断线后核对原提交。</p>}
        {preview.spec.paths?.length ? <div className="v2-git-selected"><p>本次所选文件（{preview.spec.paths.length}）</p><ul>{preview.spec.paths.map((path) => <li key={path}>{path}</li>)}</ul></div> : null}
        <details className="v2-git-evidence"><summary>完整位置与预览记录</summary><dl>
          <div><dt>操作目录</dt><dd>{preview.repository_root}</dd></div>
          <div><dt>完整基准</dt><dd>{preview.head_oid || "尚无提交"}</dd></div>
          {preview.target_commit_oid && <div><dt>完整目标提交</dt><dd>{preview.target_commit_oid}</dd></div>}
          <div><dt>预览指纹</dt><dd>{preview.preview_fingerprint}</dd></div>
        </dl></details>
        {preview.diff && <ReviewDiff patch={preview.diff} source={`Git 预览：${preview.preview_fingerprint}\n分支：${preview.branch}\n提交：${preview.head_oid}\n目录：${preview.repository_root}`} onFeedback={onFeedback} />}
        {!preview.can_execute && <p role="alert">{preview.blocked_reason || "此预览当前不能执行。"}</p>}
        <p>确认后仅执行这份预览；内容或分支变化时需要重新预览。</p>
        <button type="button" className="v2-delivery-primary" disabled={mutationBlocked || prepare.isPending || !store || !preview.can_execute || Boolean(state.data && gitFormRevision(preview) !== gitFormRevision(state.data))} onClick={() => void confirm()}>{checking ? "正在重新核对仓库…" : execute.isPending ? "正在执行…" : `确认${operations[preview.spec.operation as Operation] ?? "执行"}`}</button>
      </section>}
    </>}
  </section>;
}
