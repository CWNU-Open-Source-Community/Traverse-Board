import { useEffect, useMemo, useState, type FormEvent } from "react";
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
  action: RestoreAction;
  targetCheckpointID: string;
  result: WorkspaceCheckpointRestoreView;
}

const reversibleKinds = new Set(["file_tool", "command_batch", "git_mutation", "agent_merge"]);

export function WorkspaceCheckpointPanel({ client, runID, runStatus }: {
  client: CyberAgentClient;
  runID: string;
  runStatus: string;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const selectRun = useConnectionStore((state) => state.selectRun);
  const timelineKey = ["run", runID, "workspace-checkpoints"] as const;
  const [selectedID, setSelectedID] = useState("");
  const [title, setTitle] = useState("");
  const [preview, setPreview] = useState<PreviewIntent | null>(null);
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
  const checkpoints = timeline.data?.checkpoints ?? [];

  useEffect(() => {
    if (selectedID && checkpoints.some((checkpoint) => checkpoint.id === selectedID)) return;
    setSelectedID(currentID || checkpoints[0]?.id || "");
  }, [checkpoints, currentID, selectedID]);

  useEffect(() => {
    setPreview(null);
  }, [currentID, selectedID]);

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
    mutationFn: ({ action, targetCheckpointID }: {
      action: RestoreAction;
      targetCheckpointID: string;
    }) => client.postControl<WorkspaceCheckpointRestoreView>(
      `/runs/${encodeURIComponent(runID)}/workspace-checkpoints/preview`, {
        target_checkpoint_id: targetCheckpointID,
        expected_current_checkpoint_id: currentID,
      }, `desktop-workspace-preview-${globalThis.crypto.randomUUID()}`).then((result) => ({
        action,
        targetCheckpointID,
        result,
      })),
    onSuccess: setPreview,
  });

  const applyRestore = useMutation({
    mutationFn: (intent: PreviewIntent) => {
      const operationKey = `desktop-workspace-${intent.action}-${globalThis.crypto.randomUUID()}`;
      const path = intent.action === "rewind" ? "rewind" : intent.action;
      const body = intent.action === "rewind" ? {
        target_checkpoint_id: intent.targetCheckpointID,
        expected_current_checkpoint_id: intent.result.preview.expected_current_checkpoint_id,
        operation_key: operationKey,
        confirm: true,
      } : {
        expected_current_checkpoint_id: intent.result.preview.expected_current_checkpoint_id,
        operation_key: operationKey,
        confirm: true,
      };
      return client.postControl<WorkspaceCheckpointRestoreView>(
        `/runs/${encodeURIComponent(runID)}/workspace-checkpoints/${path}`,
        body, operationKey);
    },
    onSuccess: () => {
      setPreview(null);
      void refresh();
      void queryClient.invalidateQueries({ queryKey: ["run", runID] });
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
    let targetCheckpointID = selectedID;
    if (action === "undo") targetCheckpointID = undoSource?.before_checkpoint_id ?? "";
    if (action === "redo") targetCheckpointID = redoSource?.after_checkpoint_id ?? "";
    if (targetCheckpointID) previewRestore.mutate({ action, targetCheckpointID });
  };

  const confirmRestore = () => {
    if (!preview || preview.result.preview.conflicts.length > 0 ||
      preview.result.preview.expected_current_checkpoint_id !== currentID) return;
    const label = restoreActionLabel(t, preview.action);
    if (globalThis.confirm(t(
      `确认执行${label}？这会作为一次新的、可审计的 Workspace 写入。`,
      `Confirm ${label}? This creates a new, auditable Workspace write.`,
    ))) applyRestore.mutate(preview);
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

  if (timeline.isLoading) return <LoadingState label={t("加载 Workspace 检查点", "Loading Workspace checkpoints")} />;
  if (timeline.isError || !timeline.data) return <ErrorState error={timeline.error} />;

  const restoreBlocked = !client.hasWorkspaceCheckpointControl || runStatus !== "paused";
  const previewConflicts = preview?.result.preview.conflicts ?? [];

  return <div className="workspace-checkpoint-panel">
    <section className="checkpoint-safety-boundary">
      <ShieldCheck aria-hidden="true" size={18} />
      <div><strong>{t("恢复是新的受控写入", "Restore is a new controlled write")}</strong>
        <p>{t(
          "原历史不可改写。确认时会重新校验 paused Run、当前权限、Workspace identity、Git index 与外部漂移；不会 hard reset 或批量删除未跟踪文件。",
          "History stays immutable. Confirmation rechecks the paused Run, current authority, Workspace identity, Git index, and external drift; it never hard-resets or blanket-deletes untracked files.",
        )}</p></div>
    </section>

    <section className="checkpoint-toolbar" aria-label={t("检查点操作", "Checkpoint actions")}>
      <form onSubmit={(event) => {
        event.preventDefault();
        if (client.hasWorkspaceCheckpointControl) createCheckpoint.mutate();
      }}>
        <input aria-label={t("检查点标题", "Checkpoint title")} maxLength={512}
          onChange={(event) => setTitle(event.target.value)}
          placeholder={t("可选：重构前", "Optional: before refactor")} value={title} />
        <button className="command-button" disabled={!client.hasWorkspaceCheckpointControl ||
          createCheckpoint.isPending} type="submit">
          <Plus aria-hidden="true" size={14} />{t("立即检查点", "Checkpoint now")}
        </button>
      </form>
      <div className="checkpoint-action-row">
        <button className="compact-command" disabled={restoreBlocked || !undoSource ||
          previewRestore.isPending} onClick={() => previewAction("undo")} type="button">
          <Undo2 aria-hidden="true" size={13} />{t("预览 Undo", "Preview undo")}
        </button>
        <button className="compact-command" disabled={restoreBlocked || !redoSource?.after_checkpoint_id ||
          previewRestore.isPending} onClick={() => previewAction("redo")} type="button">
          <Redo2 aria-hidden="true" size={13} />{t("预览 Redo", "Preview redo")}
        </button>
        <button className="compact-command" disabled={restoreBlocked || !selected ||
          selected.id === currentID || selected.recovery_level === "unavailable" ||
          previewRestore.isPending} onClick={() => previewAction("rewind")} type="button">
          <Search aria-hidden="true" size={13} />{t("预览 Rewind", "Preview rewind")}
        </button>
      </div>
      {!client.hasWorkspaceCheckpointControl && <p className="inline-warning">{t(
        "当前连接只能浏览时间线；创建、预览与恢复需要控制令牌。",
        "This connection can browse only; create, preview, and restore require a control token.",
      )}</p>}
      {client.hasWorkspaceCheckpointControl && runStatus !== "paused" && <p className="inline-warning">{t(
        "Undo、Redo、Rewind 和 Fork 仅在 Run 已暂停且没有活动执行租约时开放。",
        "Undo, redo, rewind, and Fork require a paused Run with no active execution lease.",
      )}</p>}
    </section>

    <div className="checkpoint-layout">
      <section className="checkpoint-timeline" aria-label={t("Workspace 时间线", "Workspace timeline")}>
        <header><History aria-hidden="true" size={17} /><strong>{t("不可变时间线", "Immutable timeline")}</strong>
          <small>{timeline.data.storage_usage.checkpoint_count} · {formatBytes(timeline.data.storage_usage.blob_bytes)}</small></header>
        {checkpoints.length === 0 && <p>{t("尚无检查点。", "No checkpoints yet.")}</p>}
        {checkpoints.map((checkpoint) => <button aria-pressed={checkpoint.id === selectedID}
          className={checkpoint.id === selectedID ? "selected" : ""} key={checkpoint.id}
          onClick={() => setSelectedID(checkpoint.id)} type="button">
          <span><strong>{checkpoint.title || checkpoint.trigger}</strong>
            <small>{formatDate(checkpoint.created_at)} · {shortID(checkpoint.id)}</small></span>
          <span className="checkpoint-badges">
            {checkpoint.id === currentID && <em>{t("当前", "Current")}</em>}
            <StatusBadge status={checkpoint.recovery_level} />
          </span>
        </button>)}
      </section>

      <section className="checkpoint-detail">
        {!selected ? <p>{t("选择检查点查看详情。", "Select a checkpoint for details.")}</p> : <>
          <header><div><strong>{selected.title || t("Workspace 检查点", "Workspace checkpoint")}</strong>
            <code>{selected.id}</code></div><StatusBadge status={selected.recovery_level} /></header>
          <dl>
            <div><dt>{t("来源", "Source")}</dt><dd>{selected.trigger} / {selected.phase}</dd></div>
            <div><dt>{t("收据", "Receipt")}</dt><dd><code>{selected.trigger_receipt_id}</code></dd></div>
            <div><dt>{t("Attempt", "Attempt")}</dt><dd>{selected.attempt_id ? shortID(selected.attempt_id) : "—"}</dd></div>
            <div><dt>{t("Capability generation", "Capability generation")}</dt>
              <dd>{selected.capability_generation ? shortID(selected.capability_generation) : "—"}</dd></div>
            <div><dt>{t("Git", "Git")}</dt><dd>{shortID(selected.base_commit)} · {selected.branch || "detached"}</dd></div>
            <div><dt>{t("清单", "Manifest")}</dt><dd>{selected.entry_count} · {formatBytes(selected.stored_bytes)}</dd></div>
          </dl>
          {selected.incomplete_reasons.length > 0 && <div className="checkpoint-incomplete">
            <AlertTriangle aria-hidden="true" size={15} /><span><strong>{t("不完整范围", "Incomplete scope")}</strong>
              {selected.incomplete_reasons.map((reason) => <small key={reason}>{reason}</small>)}</span>
          </div>}
        </>}
      </section>
    </div>

    {(previewRestore.error || applyRestore.error || createCheckpoint.error || fork.error) &&
      <div className="inline-warning">{humanError(previewRestore.error || applyRestore.error ||
        createCheckpoint.error || fork.error)}</div>}

    {preview && <section className="checkpoint-preview" aria-label={t("恢复预览", "Restore preview")}>
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
      <div className="checkpoint-change-list">
        {preview.result.preview.changes.map((change) => <div key={`${change.kind}:${change.path}`}>
          <code>{change.path}</code><span>{change.kind}{change.previous_path ? ` ← ${change.previous_path}` : ""}</span>
          <StatusBadge status={change.recoverable ? "recoverable" : "unavailable"} />
          {change.reason && <small>{change.reason}</small>}
        </div>)}
        {preview.result.preview.changes.length === 0 && <p>{t("目标与当前 Workspace 无差异。", "No Workspace changes are required.")}</p>}
      </div>
      <button className="command-button danger" disabled={restoreBlocked || applyRestore.isPending ||
        previewConflicts.length > 0 || preview.result.preview.expected_current_checkpoint_id !== currentID}
        onClick={confirmRestore} type="button">
        <RotateCcw aria-hidden="true" size={14} />{t("确认执行", "Confirm")} {restoreActionLabel(t, preview.action)}
      </button>
    </section>}

    <section className="checkpoint-fork">
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
    </section>
  </div>;
}

function restoreActionLabel(t: (chinese: string, english: string) => string,
  action: RestoreAction): string {
  if (action === "undo") return t("撤销", "Undo");
  if (action === "redo") return t("重做", "Redo");
  return "Rewind";
}

function humanError(value: unknown): string {
  return value instanceof Error ? value.message : String(value ?? "Unknown error");
}
