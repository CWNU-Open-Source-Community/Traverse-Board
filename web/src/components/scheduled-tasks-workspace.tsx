import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Download, LoaderCircle, Pause, Play, Plus, RefreshCw,
  Square } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { ScheduledJobCreateRequestView, ScheduledJobObservationRequestView, ScheduledJobView } from "../api/types";
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
  const pendingCreate = useRef<{ client: CyberAgentClient; intent: string; runID: string;
    body: ScheduledJobCreateRequestView; key: string } | null>(null);
  const pendingObservation = useRef<{ client: CyberAgentClient; runID: string; jobID: string;
    body: ScheduledJobObservationRequestView; key: string } | null>(null);

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
  const health = useQuery({
    queryKey: ["scheduled-jobs-worker-health"],
    queryFn: ({ signal }) => client.runtimeCapabilities(signal),
    refetchInterval: 5_000,
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
      const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
      const intent = JSON.stringify([runID.trim(), kind, anchorAt, deadlineAt,
        intervalMinutes, maxRounds, notification, timezone]);
      const previous = pendingCreate.current;
      if (previous?.client === client && previous.intent === intent) {
        const result = await client.createScheduledJob(previous.runID, previous.body, previous.key);
        if (pendingCreate.current === previous) pendingCreate.current = null;
        return result;
      }
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
        throw new Error(t("请填写有效的 Run、时间与轮次上限", "Enter a valid Run, time window, and round limit"));
      }
      const schedule: ScheduledJobCreateRequestView["schedule"] = {
        kind, timezone, anchor_at: anchor.toISOString(), misfire_policy: "run_once",
        ...(kind === "periodic" ? { interval_seconds: interval * 60 } : {}),
      };
      const body: ScheduledJobCreateRequestView = {
        version: "scheduled-job.v1", schedule, deadline_at: deadline.toISOString(),
        stop_on_target_terminal: true, max_rounds: rounds, max_model_calls: 0,
        max_elapsed_seconds: elapsedSeconds,
        retry: { max_attempts: 3, initial_backoff_seconds: 5, max_backoff_seconds: 60 },
        notification, execution_mode: "read_only", confirm_repair: false,
        observation_consent_version: 1,
      };
      const attempt = { client, intent, runID: runID.trim(), body, key: operationKey("create") };
      pendingCreate.current = attempt;
      const result = await client.createScheduledJob(attempt.runID, attempt.body, attempt.key);
      if (pendingCreate.current === attempt) pendingCreate.current = null;
      return result;
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
  const enableObservation = useMutation({
    mutationFn: async (job: ScheduledJobView) => {
      const previous = pendingObservation.current;
      const attempt = previous?.client === client && previous.runID === job.owner_run_id &&
        previous.jobID === job.id && previous.body.expected_revision === job.revision ? previous : {
          client, runID: job.owner_run_id, jobID: job.id,
          body: { version: "scheduled-job-control.v1" as const, expected_revision: job.revision,
            observation_consent_version: 1 }, key: operationKey("enable-observation"),
        };
      pendingObservation.current = attempt;
      const result = await client.enableScheduledJobObservation(attempt.runID, attempt.jobID,
        attempt.body, attempt.key);
      if (pendingObservation.current === attempt) pendingObservation.current = null;
      return result;
    },
    onSuccess: (result) => refresh(result.job),
  });

  const error = create.error ?? transition.error ?? enableObservation.error ?? bundle.error ??
    list.error ?? detail.error ?? health.error;
  const jobs = list.data?.items ?? [];
  const worker = health.data?.scheduled_job_worker;
  const workerLabel = health.isError
    ? t("观察器状态暂时无法确认", "Observer status could not be confirmed")
    : !worker ? t("正在检查观察器状态…", "Checking observer status…")
      : !worker.enabled ? t("本次启动未启用观察器", "Observer is disabled for this launch")
        : worker.state === "running" ? worker.selection_scope === "confirmed_read_only"
          ? t("观察器运行中", "Observer is running") : t("调度器运行中", "Scheduler is running")
          : worker.state === "ready" ? t("观察器正在启动", "Observer is starting")
            : t("观察器已停止或正在退出", "Observer is stopped or shutting down");
  const scopeLabel = worker?.selection_scope === "confirmed_read_only"
    ? t("仅观察已确认的只读计划", "Observes confirmed read-only schedules")
    : worker?.selection_scope === "all_jobs"
      ? t("显式全计划调度（包含旧计划）", "Explicit scheduling of all jobs, including legacy jobs")
      : worker?.enabled ? t("调度范围尚未确认", "Scheduling scope is unknown") : "";
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
        <div><h1>{t("定时观察", "Scheduled observations")}</h1>
          <small>{workerLabel}{scopeLabel && ` · ${scopeLabel}`}</small>
        </div>
        <button aria-label={t("刷新定时 Run", "Refresh scheduled Runs")}
          className="compact-command" disabled={list.isFetching}
          onClick={() => { void list.refetch(); void health.refetch(); }} type="button">
          <RefreshCw aria-hidden="true" className={list.isFetching ? "spin" : ""} size={14} />
          {t("刷新", "Refresh")}
        </button>
      </header>

      <p>{t("定时查看任务状态并记录变化，不调用模型。创建后，每次打开应用都会继续已确认的观察计划；退出应用期间暂停观察。",
        "Check task status and record changes without model calls. Confirmed schedules continue when you reopen the app; observation stops while the app is closed.")}</p>
      {worker?.selection_scope === "all_jobs" && <p>{t(
        "当前以显式启动参数调度全部计划，旧计划也可能执行。此页新建的计划仍仅作只读观察。",
        "This explicitly launched worker schedules all jobs, including legacy jobs. New schedules created here remain read-only observations.")}</p>}

      <div className="scheduled-summary" aria-label={t("定时 Run 摘要", "Scheduled Run summary")}>
        <span>{t("全部", "Total")} <strong>{jobs.length}</strong></span>
        <span>{t("活动", "Active")} <strong>{counts.active}</strong></span>
        <span>{t("已停止", "Stopped")} <strong>{counts.stopped}</strong></span>
        <span>{t("执行模式", "Execution")} <strong>{t("只读", "Read-only")}</strong></span>
      </div>

      <form className="scheduled-create-form" onSubmit={submit}>
        <div className="scheduled-form-heading"><Plus aria-hidden="true" size={15} />
          <strong>{t("新建观察计划", "Create an observation schedule")}</strong></div>
        <label>{t("目标 Run ID", "Target Run ID")}
          <input aria-label={t("目标 Run ID", "Target Run ID")} maxLength={256}
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
          {t("创建并观察", "Create and observe")}
        </button>
      </form>

      {!client.hasScheduledJobControl && <p className="inline-warning">{t(
        "本次启动未开放定时 Run 控制；列表和诊断仍保持只读。",
        "Scheduled Run control was not enabled for this launch; listing and diagnostics remain read-only.")}</p>}
      {error && <p className="inline-warning" role="alert">
        {error instanceof Error ? error.message : t("定时 Run 操作失败", "Scheduled Run operation failed")}
      </p>}

      <div className="scheduled-task-layout">
        <div className="scheduled-task-list" aria-label={t("定时 Run 列表", "Scheduled Run list")}>
          {jobs.map((job) => <button className={job.id === selectedJobID ? "selected" : ""}
            key={job.id} onClick={() => setSelectedJobID(job.id)} type="button">
            <span><strong>{job.id}</strong><span className="status-badge">{job.status}</span></span>
            <small>{job.spec.schedule.kind} · {formatTime(job.next_wake_at, locale)}</small>
            <small>{t("目标", "Target")} {job.owner_run_id}</small>
            {worker?.selection_scope === "confirmed_read_only" && job.status === "active" && job.observation_consent_version !== 1 &&
              <small>{t("普通桌面观察尚未确认", "Desktop observation needs confirmation")}</small>}
          </button>)}
          {!list.isLoading && jobs.length === 0 && <div className="utility-empty-state">
            <CalendarClock aria-hidden="true" size={25} />
            <strong>{t("暂无定时 Run", "No scheduled Runs")}</strong>
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
              {selected.observation_consent_version !== 1 &&
                selected.spec.execution_mode === "read_only" && selected.spec.max_model_calls === 0 &&
                ["active", "paused"].includes(selected.status) &&
                <button className="compact-command"
                  disabled={!client.hasScheduledJobControl || enableObservation.isPending || transition.isPending}
                  onClick={() => enableObservation.mutate(selected)} type="button">
                  <Play aria-hidden="true" size={13} />{t("确认普通桌面的自动观察", "Confirm automatic desktop observation")}</button>}
              {selected.status === "active" && <button className="compact-command"
                disabled={!client.hasScheduledJobControl || transition.isPending || enableObservation.isPending}
                onClick={() => transition.mutate({ job: selected, action: "pause" })} type="button">
                <Pause aria-hidden="true" size={13} />{t("暂停", "Pause")}</button>}
              {selected.status === "paused" && <button className="compact-command"
                disabled={!client.hasScheduledJobControl || transition.isPending || enableObservation.isPending}
                onClick={() => transition.mutate({ job: selected, action: "resume" })} type="button">
                <Play aria-hidden="true" size={13} />{t("恢复", "Resume")}</button>}
              {!['completed', 'failed', 'cancelled', 'exhausted'].includes(selected.status) &&
                <button className="compact-command danger"
                  disabled={!client.hasScheduledJobControl || transition.isPending || enableObservation.isPending}
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
            <strong>{t("选择定时 Run 查看执行窗口", "Select a scheduled Run to inspect its execution window")}</strong>
          </div>}
        </div>
      </div>
    </section>
  );
}
