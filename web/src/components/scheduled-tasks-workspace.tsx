import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Download, LoaderCircle, Pause, Play, Plus, RefreshCw,
  Square } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { ScheduledJobCreateRequestView, ScheduledJobView } from "../api/types";
import { useLocale } from "../lib/locale";

const scheduleListKey = ["scheduled-jobs"] as const;

function localDateTime(date: Date): string {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

function operationKey(action: string): string {
  const random = typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
    ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  return `web-scheduled-job-${action}-${random}`;
}

function formatTime(value: string | undefined, locale: string): string {
  if (!value) return "—";
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "medium" })
    .format(new Date(value));
}

function downloadBundle(runID: string, value: unknown): void {
  const url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], {
    type: "application/json",
  }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `cyberagent-diagnostics-${runID}.json`;
  anchor.click();
  URL.revokeObjectURL(url);
}

export function ScheduledTasksWorkspace({ client, initialRunID = "" }: {
  client: CyberAgentClient;
  initialRunID?: string;
}) {
  const { locale, t } = useLocale();
  const queryClient = useQueryClient();
  const [runID, setRunID] = useState(initialRunID);
  const [selectedJobID, setSelectedJobID] = useState("");
  const [kind, setKind] = useState<"once" | "periodic">("once");
  const [anchorAt, setAnchorAt] = useState(() => localDateTime(new Date(Date.now() + 300_000)));
  const [deadlineAt, setDeadlineAt] = useState(() =>
    localDateTime(new Date(Date.now() + 86_400_000)));
  const [intervalMinutes, setIntervalMinutes] = useState("15");
  const [maxRounds, setMaxRounds] = useState("12");
  const [notification, setNotification] =
    useState<ScheduledJobCreateRequestView["notification"]>("on_change");

  useEffect(() => {
    if (initialRunID) setRunID(initialRunID);
  }, [initialRunID]);

  const list = useQuery({
    queryKey: [...scheduleListKey, runID],
    queryFn: ({ signal }) => client.listScheduledJobs(runID, 100, signal),
    refetchInterval: client.hasScheduledJobWorker ? 5_000 : false,
  });
  const detail = useQuery({
    queryKey: ["scheduled-job", selectedJobID],
    queryFn: ({ signal }) => client.getScheduledJob(selectedJobID, signal),
    enabled: selectedJobID !== "",
    refetchInterval: client.hasScheduledJobWorker ? 5_000 : false,
  });
  const selected = detail.data?.snapshot.job ??
    list.data?.items.find((job) => job.id === selectedJobID);

  const refresh = async (job?: ScheduledJobView) => {
    await queryClient.invalidateQueries({ queryKey: scheduleListKey });
    if (job) {
      setSelectedJobID(job.id);
      await queryClient.invalidateQueries({ queryKey: ["scheduled-job", job.id] });
    }
  };
  const create = useMutation({
    mutationFn: async () => {
      const anchor = new Date(anchorAt);
      const deadline = new Date(deadlineAt);
      const interval = Number(intervalMinutes);
      const rounds = Number(maxRounds);
      const elapsedSeconds = Math.floor((deadline.getTime() - Date.now()) / 1_000);
      if (!runID.trim() || !Number.isFinite(anchor.getTime()) ||
        !Number.isFinite(deadline.getTime()) || deadline <= anchor ||
        elapsedSeconds < 1 || elapsedSeconds > 90 * 24 * 60 * 60 ||
        !Number.isSafeInteger(rounds) || rounds < 1 || rounds > 1_000 ||
        (kind === "periodic" && (!Number.isSafeInteger(interval) || interval < 1 ||
          interval > 30 * 24 * 60))) {
        throw new Error(t("请填写有效的任务、时间与轮次上限", "Enter a valid task, time window, and round limit"));
      }
      const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
      const schedule: ScheduledJobCreateRequestView["schedule"] = {
        kind, timezone, anchor_at: anchor.toISOString(), misfire_policy: "run_once",
        ...(kind === "periodic" ? { interval_seconds: interval * 60 } : {}),
      };
      return client.createScheduledJob(runID.trim(), {
        version: "scheduled-job.v1", schedule, deadline_at: deadline.toISOString(),
        stop_on_target_terminal: true, max_rounds: rounds, max_model_calls: 0,
        max_elapsed_seconds: elapsedSeconds,
        retry: { max_attempts: 3, initial_backoff_seconds: 5, max_backoff_seconds: 60 },
        notification, execution_mode: "read_only", confirm_repair: false,
      }, operationKey("create"));
    },
    onSuccess: (result) => refresh(result.job),
  });
  const transition = useMutation({
    mutationFn: async ({ job, action }: {
      job: ScheduledJobView;
      action: "pause" | "resume" | "cancel";
    }) => client.transitionScheduledJob(job.owner_run_id, job.id, action, {
      version: "scheduled-job-control.v1", expected_revision: job.revision,
    }, operationKey(action)),
    onSuccess: (result) => refresh(result.job),
  });
  const bundle = useMutation({
    mutationFn: (job: ScheduledJobView) => client.diagnosticBundle(job.owner_run_id),
    onSuccess: (value, job) => downloadBundle(job.owner_run_id, value),
  });

  const error = create.error ?? transition.error ?? bundle.error ?? list.error ?? detail.error;
  const jobs = list.data?.items ?? [];
  const workerLabel = client.hasScheduledJobWorker
    ? t("进程内调度器运行中", "Process-local scheduler running")
    : t("调度器未随本次启动启用", "Scheduler was not enabled for this launch");
  const createDisabled = !client.hasScheduledJobControl || create.isPending ||
    runID.trim() === "";
  const counts = useMemo(() => ({
    active: jobs.filter((job) => job.status === "active").length,
    stopped: jobs.filter((job) => ["completed", "failed", "cancelled", "exhausted"]
      .includes(job.status)).length,
  }), [jobs]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!createDisabled) create.mutate();
  };

  return (
    <section className="utility-workspace scheduled-tasks-workspace">
      <header>
        <CalendarClock aria-hidden="true" size={18} />
        <div><h1>{t("自动定时", "Scheduled tasks")}</h1>
          <small>{workerLabel} · {t("固定并发度 1，不会后台提权", "Concurrency 1; no background authority elevation")}</small>
        </div>
        <button aria-label={t("刷新计划任务", "Refresh scheduled tasks")}
          className="compact-command" disabled={list.isFetching}
          onClick={() => void list.refetch()} type="button">
          <RefreshCw aria-hidden="true" className={list.isFetching ? "spin" : ""} size={14} />
          {t("刷新", "Refresh")}
        </button>
      </header>

      <div className="scheduled-summary" aria-label={t("计划任务摘要", "Scheduled task summary")}>
        <span>{t("全部", "Total")} <strong>{jobs.length}</strong></span>
        <span>{t("活动", "Active")} <strong>{counts.active}</strong></span>
        <span>{t("已停止", "Stopped")} <strong>{counts.stopped}</strong></span>
        <span>{t("执行模式", "Execution")} <strong>{t("只读", "Read-only")}</strong></span>
      </div>

      <form className="scheduled-create-form" onSubmit={submit}>
        <div className="scheduled-form-heading"><Plus aria-hidden="true" size={15} />
          <strong>{t("新建有界监控", "Create bounded monitor")}</strong></div>
        <label>{t("目标任务 ID", "Target task ID")}
          <input aria-label={t("目标任务 ID", "Target task ID")} maxLength={256}
            onChange={(event) => setRunID(event.target.value)} placeholder="run-…" value={runID} />
        </label>
        <label>{t("调度类型", "Schedule type")}
          <select onChange={(event) => setKind(event.target.value as "once" | "periodic")}
            value={kind}>
            <option value="once">{t("单次", "Once")}</option>
            <option value="periodic">{t("周期", "Periodic")}</option>
          </select>
        </label>
        <label>{t("首次运行", "First run")}
          <input onChange={(event) => setAnchorAt(event.target.value)} type="datetime-local"
            value={anchorAt} />
        </label>
        {kind === "periodic" && <label>{t("间隔（分钟）", "Interval (minutes)")}
          <input min="1" onChange={(event) => setIntervalMinutes(event.target.value)}
            type="number" value={intervalMinutes} />
        </label>}
        <label>{t("硬截止时间", "Hard deadline")}
          <input onChange={(event) => setDeadlineAt(event.target.value)} type="datetime-local"
            value={deadlineAt} />
        </label>
        <label>{t("最多轮次", "Maximum rounds")}
          <input max="1000" min="1" onChange={(event) => setMaxRounds(event.target.value)}
            type="number" value={maxRounds} />
        </label>
        <label>{t("通知", "Notifications")}
          <select onChange={(event) => setNotification(
            event.target.value as ScheduledJobCreateRequestView["notification"])}
            value={notification}>
            <option value="on_change">{t("状态变化时", "On change")}</option>
            <option value="on_failure">{t("失败时", "On failure")}</option>
            <option value="all">{t("每轮", "Every round")}</option>
            <option value="silent">{t("静默", "Silent")}</option>
          </select>
        </label>
        <button className="command-button" disabled={createDisabled} type="submit">
          {create.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={14} />
            : <Plus aria-hidden="true" size={14} />}
          {t("创建只读计划", "Create read-only schedule")}
        </button>
      </form>

      {!client.hasScheduledJobControl && <p className="inline-warning">{t(
        "本次启动未开放计划任务控制；列表和诊断仍保持只读。",
        "Scheduled task control was not enabled for this launch; listing and diagnostics remain read-only.")}</p>}
      {error && <p className="inline-warning" role="alert">
        {error instanceof Error ? error.message : t("计划任务操作失败", "Scheduled task operation failed")}
      </p>}

      <div className="scheduled-task-layout">
        <div className="scheduled-task-list" aria-label={t("计划任务列表", "Scheduled task list")}>
          {jobs.map((job) => <button className={job.id === selectedJobID ? "selected" : ""}
            key={job.id} onClick={() => setSelectedJobID(job.id)} type="button">
            <span><strong>{job.id}</strong><span className="status-badge">{job.status}</span></span>
            <small>{job.spec.schedule.kind} · {formatTime(job.next_wake_at, locale)}</small>
            <small>{t("目标", "Target")} {job.owner_run_id}</small>
          </button>)}
          {!list.isLoading && jobs.length === 0 && <div className="utility-empty-state">
            <CalendarClock aria-hidden="true" size={25} />
            <strong>{t("暂无自动任务", "No scheduled tasks")}</strong>
          </div>}
        </div>

        <div className="scheduled-task-detail">
          {selected ? <>
            <header><div><strong>{selected.id}</strong>
              <small>{selected.owner_run_id}</small></div>
              <span className="status-badge">{selected.status}</span></header>
            <dl>
              <div><dt>{t("下次唤醒", "Next wake")}</dt><dd>{formatTime(selected.next_wake_at, locale)}</dd></div>
              <div><dt>{t("硬截止", "Deadline")}</dt><dd>{formatTime(selected.spec.deadline_at, locale)}</dd></div>
              <div><dt>{t("轮次", "Rounds")}</dt><dd>{selected.rounds_completed} / {selected.spec.max_rounds}</dd></div>
              <div><dt>{t("最近结果", "Latest result")}</dt><dd>{selected.last_result || selected.last_error_code || "—"}</dd></div>
            </dl>
            <div className="scheduled-task-actions">
              {selected.status === "active" && <button className="compact-command"
                disabled={!client.hasScheduledJobControl || transition.isPending}
                onClick={() => transition.mutate({ job: selected, action: "pause" })} type="button">
                <Pause aria-hidden="true" size={13} />{t("暂停", "Pause")}</button>}
              {selected.status === "paused" && <button className="compact-command"
                disabled={!client.hasScheduledJobControl || transition.isPending}
                onClick={() => transition.mutate({ job: selected, action: "resume" })} type="button">
                <Play aria-hidden="true" size={13} />{t("恢复", "Resume")}</button>}
              {!['completed', 'failed', 'cancelled', 'exhausted'].includes(selected.status) &&
                <button className="compact-command danger"
                  disabled={!client.hasScheduledJobControl || transition.isPending}
                  onClick={() => transition.mutate({ job: selected, action: "cancel" })} type="button">
                  <Square aria-hidden="true" size={12} />{t("取消", "Cancel")}</button>}
              <button className="compact-command" disabled={bundle.isPending}
                onClick={() => bundle.mutate(selected)} type="button">
                <Download aria-hidden="true" size={13} />{t("导出诊断包", "Export diagnostics")}</button>
            </div>
            {detail.data?.snapshot.notifications.length ? <section className="scheduled-notifications">
              <strong>{t("通知", "Notifications")}</strong>
              {detail.data.snapshot.notifications.map((notice) => <p key={notice.id}>
                <span className="status-badge">{notice.kind}</span>{notice.summary}</p>)}
            </section> : null}
          </> : <div className="utility-empty-state">
            <CalendarClock aria-hidden="true" size={25} />
            <strong>{t("选择任务查看执行窗口", "Select a task to inspect its execution window")}</strong>
          </div>}
        </div>
      </div>
    </section>
  );
}
