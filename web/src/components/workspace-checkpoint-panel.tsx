import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  GitFork,
  History,
  Plus,
  Redo2,
  RotateCcw,
  Search,
  ShieldCheck,
  Undo2,
} from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type {
  WorkspaceCheckpointForkView,
  WorkspaceCheckpointRestoreView,
  WorkspaceCheckpointTimelineView,
  WorkspaceCheckpointView,
} from "../api/types";
import { formatBytes, formatDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { useConnectionStore } from "../state/connection";
import { ErrorState, LoadingState, StatusBadge } from "./common";

type RestoreAction = "rewind" | "undo" | "redo";

interface PreviewIntent {
  runID: string;
  action: RestoreAction;
  targetCheckpointID: string;
  expectedCurrentID: string;
  operationKey: string;
  result: WorkspaceCheckpointRestoreView;
}

interface PendingRestore {
  intent: PreviewIntent;
  state: "pending" | "unknown";
  error?: string;
}

const restoreIntentKey = (runID: string) => ["run", runID, "workspace-restore-intent"] as const;

const reversibleKinds = new Set(["file_tool", "command_batch", "git_mutation", "agent_merge"]);

export function WorkspaceCheckpointPanel({ client, runID, runStatus, variant = "inspector", onChanged }: {
  client: CyberAgentClient;
  runID: string;
  runStatus: string;
  variant?: "inspector" | "conversation";
  onChanged?: () => void;
}) {
  const { t } = useLocale();
  const allowFork = variant === "inspector";
  const checkpointLabel = (checkpoint: WorkspaceCheckpointView) => {
    if (allowFork) return checkpoint.title || checkpoint.trigger;
    const triggers: Record<string, string> = { file_tool: "文件修改", command_batch: "命令执行",
      git_mutation: "Git 操作", agent_merge: "合并修改", manual: "手动保存" };
    return `${checkpoint.title || triggers[checkpoint.trigger] || "项目检查点"}${checkpoint.phase === "before"
      ? " · 修改前" : checkpoint.phase === "after" ? " · 修改后" : ""}`;
  };
  const queryClient = useQueryClient();
  const selectRun = useConnectionStore((state) => state.selectRun);
  const timelineKey = ["run", runID, "workspace-checkpoints"] as const;
  const [selectedID, setSelectedID] = useState("");
  const [title, setTitle] = useState("");
  const [preview, setPreview] = useState<PreviewIntent | null>(null);
  const intentQuery = useQuery<PendingRestore | null>({ queryKey: restoreIntentKey(runID),
    queryFn: () => null, enabled: false, initialData: null, gcTime: Infinity });
  const pendingRestore = intentQuery.data;
  const [restoreOutcome, setRestoreOutcome] = useState<"completed" | "failed" | null>(null);
  const previewSequence = useRef(0);
  const selectionRef = useRef({ runID, currentID: "", selectedID });
  const [forkName, setForkName] = useState("");
  const [forkBranch, setForkBranch] = useState("");
  const [forkGoal, setForkGoal] = useState("");

  const timeline = useQuery({
    queryKey: timelineKey,
    queryFn: ({ signal }) => client.get<WorkspaceCheckpointTimelineView>(
      `/runs/${encodeURIComponent(runID)}/workspace-checkpoints`, { limit: 200 }, signal),
    enabled: Boolean(runID),
  });
  const currentID = timeline.data?.current?.current_checkpoint_id ?? "";
  selectionRef.current = { runID, currentID, selectedID };
  const checkpoints = timeline.data?.checkpoints ?? [];

  useEffect(() => {
    if (selectedID && checkpoints.some((checkpoint) => checkpoint.id === selectedID)) return;
    setSelectedID(currentID || checkpoints[0]?.id || "");
  }, [checkpoints, currentID, selectedID]);

  useEffect(() => {
    previewSequence.current += 1;
    setPreview(null);
  }, [runID, currentID, selectedID]);
  useEffect(() => setRestoreOutcome(null), [runID]);

  const selected = checkpoints.find((checkpoint) => checkpoint.id === selectedID);
  const transactions = timeline.data?.transactions ?? [];
  const undoSource = transactions.find((transaction) =>
    transaction.status === "completed" && transaction.after_checkpoint_id === currentID &&
    reversibleKinds.has(transaction.kind));
  const completedUndo = transactions.find((transaction) => transaction.kind === "undo" &&
    transaction.status === "completed" && transaction.after_checkpoint_id === currentID);
  const redoSource = completedUndo
    ? transactions.find((transaction) => transaction.id === completedUndo.trigger_receipt_id)
    : undefined;
  const checkpointByID = useMemo(() => new Map(checkpoints.map((checkpoint) =>
    [checkpoint.id, checkpoint])), [checkpoints]);

  const refresh = () => queryClient.invalidateQueries({ queryKey: timelineKey });
  const createCheckpoint = useMutation({
    mutationFn: () => {
      const operationKey = `desktop-checkpoint-${globalThis.crypto.randomUUID()}`;
      return client.postControl<WorkspaceCheckpointView>(
        `/runs/${encodeURIComponent(runID)}/workspace-checkpoints`, {
          operation_key: operationKey,
          title: title.trim() || undefined,
        }, operationKey);
    },
    onSuccess: (checkpoint) => {
      setTitle("");
      setSelectedID(checkpoint.id);
      setPreview(null);
      void refresh();
    },
  });

  const previewRestore = useMutation({
    mutationFn: ({ action, targetCheckpointID, requestedRunID, expectedCurrentID, selectionID, sequence }: {
      action: RestoreAction;
      targetCheckpointID: string;
      requestedRunID: string;
      expectedCurrentID: string;
      selectionID: string;
      sequence: number;
    }) => client.postControl<WorkspaceCheckpointRestoreView>(
      `/runs/${encodeURIComponent(requestedRunID)}/workspace-checkpoints/preview`, {
        target_checkpoint_id: targetCheckpointID,
        expected_current_checkpoint_id: expectedCurrentID,
      }, `desktop-workspace-preview-${globalThis.crypto.randomUUID()}`).then((result) => {
        if (result.before.run_id !== requestedRunID || result.preview.target_checkpoint_id !== targetCheckpointID ||
          result.preview.expected_current_checkpoint_id !== expectedCurrentID) throw new Error("恢复预览与所选执行或检查点不匹配，请重新预览。");
        return { runID: requestedRunID, expectedCurrentID, selectionID, sequence, action, targetCheckpointID,
          operationKey: `desktop-workspace-${action}-${globalThis.crypto.randomUUID()}`, result };
      }),
    onSuccess: (result) => {
      const selection = selectionRef.current;
      if (result.sequence !== previewSequence.current || result.runID !== selection.runID ||
        result.expectedCurrentID !== selection.currentID || result.selectionID !== selection.selectedID ||
        queryClient.getQueryData(restoreIntentKey(result.runID))) return;
      setPreview(result);
    },
  });

  const applyRestore = useMutation({
    mutationFn: (intent: PreviewIntent) => {
      const operationKey = intent.operationKey;
      const path = intent.action === "rewind" ? "rewind" : intent.action;
      const body = intent.action === "rewind" ? {
        target_checkpoint_id: intent.targetCheckpointID,
        expected_current_checkpoint_id: intent.expectedCurrentID,
        operation_key: operationKey,
        confirm: true,
      } : {
        expected_current_checkpoint_id: intent.expectedCurrentID,
        operation_key: operationKey,
        confirm: true,
      };
      return client.postControl<WorkspaceCheckpointRestoreView>(
        `/runs/${encodeURIComponent(intent.runID)}/workspace-checkpoints/${path}`,
        body, operationKey);
    },
    onSuccess: (result, intent) => {
      const transaction = result.transaction;
      const status = transaction?.status;
      const terminal = result.confirmed && transaction?.run_id === intent.runID &&
        transaction.target_checkpoint_id === intent.targetCheckpointID &&
        transaction.expected_current_checkpoint_id === intent.expectedCurrentID && transaction.kind === intent.action &&
        (status === "completed" || status === "failed");
      const matchingIntent = queryClient.getQueryData<PendingRestore | null>(restoreIntentKey(intent.runID))
        ?.intent.operationKey === intent.operationKey;
      queryClient.setQueryData<PendingRestore | null>(restoreIntentKey(intent.runID), (current) => {
        if (current?.intent.operationKey !== intent.operationKey) return current;
        return terminal ? null : { intent, state: "unknown", error: "恢复结果尚未给出已完成或已失败的事务状态，请按原请求确认。" };
      });
      if (matchingIntent && selectionRef.current.runID === intent.runID) {
        setPreview((current) => current?.operationKey === intent.operationKey ? null : current);
        setRestoreOutcome(terminal ? status as "completed" | "failed" : null);
        onChanged?.();
      }
    },
    onError: (error, intent) => {
      queryClient.setQueryData<PendingRestore | null>(restoreIntentKey(intent.runID), (current) =>
        current?.intent.operationKey === intent.operationKey ? { intent, state: "unknown", error: humanError(error) } : current);
    },
    onSettled: (_result, _error, intent) => {
      void queryClient.invalidateQueries({ queryKey: ["run", intent.runID] });
    },
  });

  const fork = useMutation({
    mutationFn: () => {
      const operationKey = `desktop-workspace-fork-${globalThis.crypto.randomUUID()}`;
      return client.postControl<WorkspaceCheckpointForkView>(
        `/runs/${encodeURIComponent(runID)}/workspace-checkpoints/fork`, {
          target_checkpoint_id: selectedID,
          expected_current_checkpoint_id: currentID,
          operation_key: operationKey,
          workspace_name: forkName.trim(),
          branch: forkBranch.trim(),
          goal: forkGoal.trim() || undefined,
          confirm: true,
        }, operationKey);
    },
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["runs"] });
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      selectRun(result.run.id);
    },
  });

  const previewAction = (action: RestoreAction) => {
    if (queryClient.getQueryData(restoreIntentKey(runID))) return;
    let targetCheckpointID = selectedID;
    if (action === "undo") targetCheckpointID = undoSource?.before_checkpoint_id ?? "";
    if (action === "redo") targetCheckpointID = redoSource?.after_checkpoint_id ?? "";
    if (targetCheckpointID) {
      setRestoreOutcome(null);
      setPreview(null);
      previewRestore.mutate({ action, targetCheckpointID, requestedRunID: runID,
        expectedCurrentID: currentID, selectionID: selectedID, sequence: ++previewSequence.current });
    }
  };

  const submitRestore = (intent: PreviewIntent) => {
    const existing = queryClient.getQueryData<PendingRestore | null>(restoreIntentKey(intent.runID));
    if (existing?.state === "pending" || (existing && existing.intent.operationKey !== intent.operationKey)) return;
    queryClient.setQueryData<PendingRestore>(restoreIntentKey(intent.runID), { intent, state: "pending" });
    setRestoreOutcome(null);
    applyRestore.mutate(intent);
  };

  const confirmRestore = () => {
    if (pendingRestore || !preview || preview.runID !== runID || preview.result.preview.conflicts.length > 0 || preview.result.preview.truncated ||
      preview.result.preview.recovery_level === "unavailable" ||
      preview.result.preview.expected_current_checkpoint_id !== currentID) return;
    const label = restoreActionLabel(t, preview.action);
    if (globalThis.confirm(t(
      `确认执行${label}？这会作为一次新的、可审计的 Workspace 写入。`,
      `Confirm ${label}? This creates a new, auditable Workspace write.`,
    ))) submitRestore(preview);
  };

  const submitFork = (event: FormEvent) => {
    event.preventDefault();
    if (!selected || selected.recovery_level === "unavailable" || !forkName.trim() ||
      !forkBranch.trim()) return;
    if (globalThis.confirm(t(
      "确认从该检查点创建独立 Run、Git 分支和 worktree？旧权限与进程不会继承。",
      "Create an independent Run, Git branch, and worktree from this checkpoint? Old authority and processes are not inherited.",
    ))) fork.mutate();
  };

  if (timeline.isLoading && !pendingRestore) return <LoadingState label={t("加载 Workspace 检查点", "Loading Workspace checkpoints")} />;
  if ((timeline.isError || !timeline.data) && !pendingRestore) return <div><ErrorState error={timeline.error} />
    <button onClick={() => void timeline.refetch()} type="button">{t("重试检查点", "Retry checkpoints")}</button></div>;

  const restoreBlocked = !client.hasWorkspaceCheckpointControl || runStatus !== "paused" || Boolean(pendingRestore);
  const previewConflicts = preview?.result.preview.conflicts ?? [];

  return <div className="workspace-checkpoint-panel">
    <section className="checkpoint-safety-boundary">
      <ShieldCheck aria-hidden="true" size={18} />
      <div><strong>{t("恢复是新的受控写入", "Restore is a new controlled write")}</strong>
        <p>{!allowFork ? "这是项目快照恢复，会核对整个项目和 Git 暂存区。快照之后的外部修改（包括无关新增文件）可能阻止恢复；请保留这些内容并先处理冲突，再重新预览。确认时会再次校验，不会强制覆盖。" : t(
          "原历史不可改写。确认时会重新校验 paused Run、当前权限、Workspace identity、Git index 与外部漂移；不会 hard reset 或批量删除未跟踪文件。",
          "History stays immutable. Confirmation rechecks the paused Run, current authority, Workspace identity, Git index, and external drift; it never hard-resets or blanket-deletes untracked files.",
        )}</p></div>
    </section>

    <section className="checkpoint-toolbar" aria-label={t("检查点操作", "Checkpoint actions")}>
      <form onSubmit={(event) => {
        event.preventDefault();
        if (client.hasWorkspaceCheckpointControl && !pendingRestore) createCheckpoint.mutate();
      }}>
        <input aria-label={t("检查点标题", "Checkpoint title")} maxLength={512}
          onChange={(event) => setTitle(event.target.value)}
          placeholder={t("可选：重构前", "Optional: before refactor")} value={title} />
        <button className="command-button" disabled={!client.hasWorkspaceCheckpointControl || Boolean(pendingRestore) ||
          createCheckpoint.isPending} type="submit">
          <Plus aria-hidden="true" size={14} />{t("立即检查点", "Checkpoint now")}
        </button>
      </form>
      <div className="checkpoint-action-row">
        <button className="compact-command" disabled={restoreBlocked || !undoSource ||
          previewRestore.isPending} onClick={() => previewAction("undo")} type="button">
          <Undo2 aria-hidden="true" size={13} />{allowFork ? t("预览 Undo", "Preview undo") : "预览撤销最近改动"}
        </button>
        <button className="compact-command" disabled={restoreBlocked || !redoSource?.after_checkpoint_id ||
          previewRestore.isPending} onClick={() => previewAction("redo")} type="button">
          <Redo2 aria-hidden="true" size={13} />{allowFork ? t("预览 Redo", "Preview redo") : "预览重做"}
        </button>
        <button className="compact-command" disabled={restoreBlocked || !selected ||
          selected.id === currentID || selected.recovery_level === "unavailable" ||
          previewRestore.isPending} onClick={() => previewAction("rewind")} type="button">
          <Search aria-hidden="true" size={13} />{allowFork ? t("预览 Rewind", "Preview rewind") : "预览恢复到所选版本"}
        </button>
      </div>
      {!client.hasWorkspaceCheckpointControl && <p className="inline-warning">{t(
        "当前连接只能浏览时间线；创建、预览与恢复需要控制令牌。",
        "This connection can browse only; create, preview, and restore require a control token.",
      )}</p>}
      {client.hasWorkspaceCheckpointControl && runStatus !== "paused" && <p className="inline-warning">{!allowFork ? "请先暂停任务，再预览恢复操作。" : t(
        "Undo、Redo、Rewind 和 Fork 仅在 Run 已暂停且没有活动执行租约时开放。",
        "Undo, redo, rewind, and Fork require a paused Run with no active execution lease.",
      )}</p>}
      {!allowFork && <p>恢复写入还受任务权限限制：保守模式可预览，但不能确认恢复。请在对话输入区核对权限；这里不会自动扩大权限。</p>}
    </section>

    <div className="checkpoint-layout">
      <section className="checkpoint-timeline" aria-label={t("Workspace 时间线", "Workspace timeline")}>
        <header><History aria-hidden="true" size={17} /><strong>{t("不可变时间线", "Immutable timeline")}</strong>
          <small>{timeline.data?.storage_usage.checkpoint_count ?? 0} · {formatBytes(timeline.data?.storage_usage.blob_bytes ?? 0)}</small></header>
        {checkpoints.length === 0 && <p>{t("尚无检查点。", "No checkpoints yet.")}</p>}
        {checkpoints.map((checkpoint) => <button aria-pressed={checkpoint.id === selectedID}
          className={checkpoint.id === selectedID ? "selected" : ""} key={checkpoint.id}
          onClick={() => setSelectedID(checkpoint.id)} type="button">
          <span><strong>{checkpointLabel(checkpoint)}</strong>
            <small>{formatDate(checkpoint.created_at)} · {shortID(checkpoint.id)}</small></span>
          <span className="checkpoint-badges">
            {checkpoint.id === currentID && <em>{t("当前", "Current")}</em>}
            <StatusBadge status={checkpoint.recovery_level} />
          </span>
        </button>)}
      </section>

      <section className="checkpoint-detail">
        {!selected ? <p>{t("选择检查点查看详情。", "Select a checkpoint for details.")}</p> : <>
          <header><div><strong>{checkpointLabel(selected) || t("Workspace 检查点", "Workspace checkpoint")}</strong>
            <code>{selected.id}</code></div><StatusBadge status={selected.recovery_level} /></header>
          <details open={allowFork}><summary>{t("技术详情", "Technical details")}</summary><dl>
            <div><dt>{t("来源", "Source")}</dt><dd>{selected.trigger} / {selected.phase}</dd></div>
            <div><dt>{t("收据", "Receipt")}</dt><dd><code>{selected.trigger_receipt_id}</code></dd></div>
            <div><dt>{t("Attempt", "Attempt")}</dt><dd>{selected.attempt_id ? shortID(selected.attempt_id) : "—"}</dd></div>
            <div><dt>{t("Capability generation", "Capability generation")}</dt>
              <dd>{selected.capability_generation ? shortID(selected.capability_generation) : "—"}</dd></div>
            <div><dt>{t("Git", "Git")}</dt><dd>{shortID(selected.base_commit)} · {selected.branch || "detached"}</dd></div>
            <div><dt>{t("清单", "Manifest")}</dt><dd>{selected.entry_count} · {formatBytes(selected.stored_bytes)}</dd></div>
          </dl></details>
          {selected.incomplete_reasons.length > 0 && <div className="checkpoint-incomplete">
            <AlertTriangle aria-hidden="true" size={15} /><span><strong>{t("不完整范围", "Incomplete scope")}</strong>
              {selected.incomplete_reasons.map((reason) => <small key={reason}>{reason}</small>)}</span>
          </div>}
        </>}
      </section>
    </div>

    {(previewRestore.error || createCheckpoint.error || fork.error) &&
      <div className="inline-warning">{humanError(previewRestore.error ||
        createCheckpoint.error || fork.error)}</div>}
    {restoreOutcome === "completed" && <p role="status">{t("项目恢复已完成。请结合当前差异确认内容，历史编辑记录会保留。", "Workspace restore completed. Check the current diff; historical edit records are retained.")}</p>}
    {restoreOutcome === "failed" && <p role="alert">{t("已确认本次恢复失败。请检查当前项目内容和冲突，处理后重新预览；失败不代表所有内容都未改变。", "This restore is confirmed failed. Check current files and conflicts, then preview again; failure does not imply that no files changed.")}</p>}
    {pendingRestore && <section className="checkpoint-preview" aria-label={t("待确认的恢复操作", "Unresolved restore operation")}>
      <p role="status">{pendingRestore.state === "pending" ? t("正在确认恢复结果…", "Confirming restore outcome…")
        : t("恢复结果尚未确认。请按原请求确认后，再开始新的恢复操作。", "The restore outcome is unknown. Confirm the original request before starting another restore.")}</p>
      <p>{restoreActionLabel(t, pendingRestore.intent.action)} · <code>{pendingRestore.intent.targetCheckpointID}</code></p>
      {pendingRestore.error && <p role="alert">{pendingRestore.error}</p>}
      <button className="command-button" disabled={!client.hasWorkspaceCheckpointControl || pendingRestore.state === "pending"}
        onClick={() => submitRestore(pendingRestore.intent)} type="button">{t("确认上次恢复", "Confirm previous restore")}</button>
    </section>}

    {!pendingRestore && preview && <section className="checkpoint-preview" aria-label={t("恢复预览", "Restore preview")}>
      <header><RotateCcw aria-hidden="true" size={17} /><div><strong>{restoreActionLabel(t, preview.action)}</strong>
        <small>{preview.result.preview.changes.length} {t("项影响", "changes")} · {preview.result.preview.recovery_level}</small></div></header>
      {preview.result.preview.index_changed && <p className="inline-warning">{t(
        "Git index 将发生变化；确认时会再次校验完整 index hash。",
        "The Git index will change and its complete hash will be rechecked on confirmation.",
      )}</p>}
      {previewConflicts.length > 0 && <div className="checkpoint-conflicts">
        <strong>{t("检测到外部冲突，已停止", "External conflicts detected; restore is blocked")}</strong>
        {previewConflicts.map((conflict, index) => <p key={`${conflict.kind}:${conflict.path}:${index}`}>
          <code>{conflict.path || conflict.kind}</code> · {conflict.reason}</p>)}
      </div>}
      {(preview.result.preview.truncated || preview.result.preview.recovery_level === "unavailable") &&
        <p className="inline-warning" role="alert">{t("预览不完整或当前内容不可恢复，不能确认写入。请核对冲突与不完整范围。", "The preview is incomplete or recovery is unavailable. Check conflicts and incomplete scope before restoring.")}</p>}
      <div className="checkpoint-change-list">
        {preview.result.preview.changes.map((change) => <div key={`${change.kind}:${change.path}`}>
          <code>{change.path}</code><span>{change.kind}{change.previous_path ? ` ← ${change.previous_path}` : ""}</span>
          <StatusBadge status={change.recoverable ? "recoverable" : "unavailable"} />
          {change.reason && <small>{change.reason}</small>}
        </div>)}
        {preview.result.preview.changes.length === 0 && <p>{t("目标与当前 Workspace 无差异。", "No Workspace changes are required.")}</p>}
      </div>
      <button className="command-button danger" disabled={restoreBlocked || applyRestore.isPending ||
        previewConflicts.length > 0 || preview.result.preview.truncated ||
        preview.result.preview.recovery_level === "unavailable" || preview.result.preview.expected_current_checkpoint_id !== currentID}
        onClick={confirmRestore} type="button">
        <RotateCcw aria-hidden="true" size={14} />{t("确认执行", "Confirm")} {restoreActionLabel(t, preview.action)}
      </button>
    </section>}

    {allowFork && <section className="checkpoint-fork">
      <header><GitFork aria-hidden="true" size={17} /><div><strong>{t("从此处 Fork", "Fork from here")}</strong>
        <small>{t("新 Run / branch / worktree；权限、凭据、租约和进程全部重置", "New Run / branch / worktree; authority, credentials, leases, and processes reset")}</small></div></header>
      <form onSubmit={submitFork}>
        <input aria-label={t("新 Workspace 名称", "New Workspace name")} maxLength={256}
          onChange={(event) => setForkName(event.target.value)} placeholder={t("Workspace 名称", "Workspace name")} value={forkName} />
        <input aria-label={t("新 Git 分支", "New Git branch")} maxLength={255}
          onChange={(event) => setForkBranch(event.target.value)} placeholder="codex/checkpoint-fork" value={forkBranch} />
        <input aria-label={t("新 Run 目标", "New Run goal")} maxLength={2048}
          onChange={(event) => setForkGoal(event.target.value)} placeholder={t("可选新目标", "Optional new goal")} value={forkGoal} />
        <button className="command-button" disabled={restoreBlocked || fork.isPending || !selected ||
          selected.recovery_level === "unavailable" || !forkName.trim() || !forkBranch.trim()}
          type="submit">
          <GitFork aria-hidden="true" size={14} />{t("确认 Fork", "Confirm Fork")}
        </button>
      </form>
    </section>}
  </div>;
}

function restoreActionLabel(t: (chinese: string, english: string) => string,
  action: RestoreAction): string {
  if (action === "undo") return t("撤销", "Undo");
  if (action === "redo") return t("重做", "Redo");
  return "Rewind";
}

function humanError(value: unknown): string {
  if (value instanceof Error && value.message.includes("workspace restore is not authorized")) {
    return "当前任务的执行阶段或权限不允许恢复。请返回对话核对权限；保守模式仅支持预览。项目内容未因此次拒绝而改变。";
  }
  return value instanceof Error ? value.message : String(value ?? "Unknown error");
}
