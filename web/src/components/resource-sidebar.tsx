import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  CalendarClock,
  CircleUserRound,
  Cpu,
  GitPullRequest,
  ListTree,
  MessagesSquare,
  PackageSearch,
  RefreshCw,
  Search,
  Settings,
  SquarePen,
  Trash2,
  X,
} from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { RunView, SessionView, ThreadView } from "../api/types";
import { usePagedResource } from "../hooks/use-paged-resource";
import { formatCompactDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { useConnectionStore } from "../state/connection";
import { ErrorState, LoadMoreButton, LoadingState } from "./common";
import { PrayuBrand } from "./prayu-brand";
import { useModalFocusTrap } from "../hooks/use-modal-focus-trap";

export type WorkbenchSection =
  | "conversation"
  | "new-task"
  | "pull-requests"
  | "models"
  | "schedule"
  | "plugins";

type NavigationSection = Exclude<WorkbenchSection, "conversation" | "new-task">;

const navigationItems: Array<{
  id: NavigationSection;
  label: [string, string];
  icon: typeof GitPullRequest;
}> = [
  { id: "pull-requests", label: ["拉取请求", "Pull requests"], icon: GitPullRequest },
  { id: "models", label: ["模型切换", "Models"], icon: Cpu },
  { id: "schedule", label: ["自动定时", "Scheduled tasks"], icon: CalendarClock },
  { id: "plugins", label: ["插件", "Plugins"], icon: PackageSearch },
];

export function ResourceSidebar({ client, activeSection, onCreateRun, onNavigate,
  onOpenSettings }: {
  client: CyberAgentClient;
  activeSection: WorkbenchSection;
  onCreateRun?: () => void;
  onNavigate?: (section: WorkbenchSection) => void;
  onOpenSettings?: () => void;
}) {
  const { t } = useLocale();
  const [search, setSearch] = useState("");
  const [searchOpen, setSearchOpen] = useState(false);
  const [archiveCandidate, setArchiveCandidate] = useState<SessionView | null>(null);
  const [threadArchiveCandidate, setThreadArchiveCandidate] = useState<ThreadView | null>(null);
  const [hiddenSessionIDs, setHiddenSessionIDs] = useState<Set<string>>(() => new Set());
  const queryClient = useQueryClient();
  const kind = useConnectionStore((state) => state.resourceKind);
  const selectedRunID = useConnectionStore((state) => state.selectedRunID);
  const selectedSessionID = useConnectionStore((state) => state.selectedSessionID);
  const selectedThreadID = useConnectionStore((state) => state.selectedThreadID);
  const selectRun = useConnectionStore((state) => state.selectRun);
  const selectSession = useConnectionStore((state) => state.selectSession);
  const selectThread = useConnectionStore((state) => state.selectThread);

  const threadsQuery = usePagedResource<ThreadView>(client, ["threads"], "/threads",
    { limit: 50, status: "active" }, true);
  const runsQuery = usePagedResource<RunView>(client, ["runs"], "/runs", { limit: 50 }, true);
  const sessionsQuery = usePagedResource<SessionView>(client, ["sessions"], "/sessions",
    { limit: 50 }, true);
  const runs = useMemo(() => runsQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [runsQuery.data]);
  const sessions = useMemo(() => sessionsQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [sessionsQuery.data]);
  const threads = useMemo(() => threadsQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [threadsQuery.data]);
  const activeSessions = useMemo(() => sessions.filter((session) =>
    session.status !== "archived" && !hiddenSessionIDs.has(session.id)), [hiddenSessionIDs, sessions]);
  const normalizedSearch = search.trim().toLowerCase();
  const visibleRuns = runs.filter((run) => !normalizedSearch ||
    `${run.id} ${run.mission_id} ${run.status}`.toLowerCase().includes(normalizedSearch));
  const visibleSessions = activeSessions.filter((session) => !normalizedSearch ||
    `${session.id} ${session.title} ${session.route}`.toLowerCase().includes(normalizedSearch));
  const visibleThreads = threads.filter((thread) => !normalizedSearch ||
    `${thread.id} ${thread.title} ${thread.last_run_id}`.toLowerCase().includes(normalizedSearch));

  const threadArchiveMutation = useMutation({
    mutationFn: (thread: ThreadView) => client.transitionThread(thread.id, "archive", {
      version: "thread_lifecycle.v1", expected_version: thread.version,
    }, `web-thread-archive-${globalThis.crypto.randomUUID()}`),
    onSuccess: (result) => {
      setThreadArchiveCandidate(null);
      if (kind === "thread" && selectedThreadID === result.thread.id) {
        const fallback = threads.find((thread) => thread.id !== result.thread.id);
        if (fallback) selectThread(fallback.id);
        else if (runs[0]) selectRun(runs[0].id);
        else if (activeSessions[0]) selectSession(activeSessions[0].id);
      }
      void queryClient.invalidateQueries({ queryKey: ["threads"] });
      void queryClient.invalidateQueries({ queryKey: ["thread", result.thread.id] });
    },
  });

  const archiveMutation = useMutation({
    mutationFn: (session: SessionView) => client.archiveSession(session.id, {
      version: "session_archive.v1",
      confirm: true,
    }),
    onSuccess: (result) => {
      setHiddenSessionIDs((current) => new Set(current).add(result.session_id));
      setArchiveCandidate(null);
      if (kind === "session" && selectedSessionID === result.session_id) {
        const fallbackSession = activeSessions.find((session) => session.id !== result.session_id);
        if (fallbackSession) selectSession(fallbackSession.id);
        else if (runs[0]) selectRun(runs[0].id);
      }
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      void queryClient.invalidateQueries({ queryKey: ["session", result.session_id] });
    },
  });
  const archiveDialogRef = useModalFocusTrap<HTMLElement>(Boolean(archiveCandidate),
    () => setArchiveCandidate(null), archiveMutation.isPending);
  const threadArchiveDialogRef = useModalFocusTrap<HTMLElement>(Boolean(threadArchiveCandidate),
    () => setThreadArchiveCandidate(null), threadArchiveMutation.isPending);

  useEffect(() => {
    if (kind === "thread" && !threadsQuery.isLoading && !threadsQuery.isFetching &&
      !threads.some((thread) => thread.id === selectedThreadID)) {
      if (threads[0]) selectThread(threads[0].id);
      else if (runs[0]) selectRun(runs[0].id);
      else if (activeSessions[0]) selectSession(activeSessions[0].id);
    }
  }, [activeSessions, kind, runs, selectRun, selectSession, selectedThreadID,
    selectThread, threads, threadsQuery.isFetching, threadsQuery.isLoading]);

  useEffect(() => {
    if (kind === "run" && !runsQuery.isLoading && !runsQuery.isFetching &&
      !runs.some((run) => run.id === selectedRunID)) {
      if (runs[0]) selectRun(runs[0].id);
      else if (activeSessions[0]) selectSession(activeSessions[0].id);
    }
  }, [kind, runs, runsQuery.isFetching, runsQuery.isLoading, selectRun, selectedRunID,
    selectSession, activeSessions]);

  useEffect(() => {
    if (kind === "session" && !sessionsQuery.isLoading && !sessionsQuery.isFetching &&
      !activeSessions.some((session) => session.id === selectedSessionID)) {
      if (activeSessions[0]) selectSession(activeSessions[0].id);
      else if (runs[0]) selectRun(runs[0].id);
    }
  }, [activeSessions, kind, runs, selectRun, selectSession, selectedSessionID,
    sessionsQuery.isFetching, sessionsQuery.isLoading]);

  const selectConversation = (select: () => void) => {
    select();
    onNavigate?.("conversation");
  };
  const refreshHistory = () => void Promise.all([
    threadsQuery.refetch(), runsQuery.refetch(), sessionsQuery.refetch(),
  ]);
  const historyBusy = threadsQuery.isFetching || runsQuery.isFetching || sessionsQuery.isFetching;

  return (
    <aside className="resource-sidebar prayu-sidebar">
      <div className="sidebar-brand">
        <PrayuBrand />
        <button aria-label={t("搜索历史对话", "Search conversation history")} className="sidebar-brand-action"
          onClick={() => setSearchOpen((open) => !open)} title={t("搜索历史对话", "Search conversation history")} type="button">
          {searchOpen ? <X aria-hidden="true" size={16} /> : <Search aria-hidden="true" size={16} />}
        </button>
      </div>

      <nav aria-label={t("工作台导航", "Workbench navigation")} className="sidebar-primary-navigation">
        {onCreateRun && <button className={activeSection === "new-task" ? "active" : ""}
          onClick={onCreateRun} type="button">
          <SquarePen aria-hidden="true" size={16} /><span>{t("新建任务", "New task")}</span>
        </button>}
        {navigationItems.map(({ id, label, icon: Icon }) => (
          <button aria-current={activeSection === id ? "page" : undefined}
            className={activeSection === id ? "active" : ""} key={id}
            onClick={() => onNavigate?.(id)} type="button">
            <Icon aria-hidden="true" size={16} /><span>{t(...label)}</span>
          </button>
        ))}
      </nav>

      {searchOpen && <label className="sidebar-history-search">
        <Search aria-hidden="true" size={14} />
        <input aria-label={t("搜索历史对话", "Search conversation history")} autoFocus onChange={(event) => setSearch(event.target.value)}
          placeholder={t("搜索对话与任务", "Search conversations and tasks")} type="search" value={search} />
      </label>}

      <div className="sidebar-history">
        <section aria-labelledby="thread-history-heading">
          <header className="sidebar-history-heading">
            <span id="thread-history-heading">{t("任务历史", "Task history")}</span>
            <button aria-label={t("刷新历史记录", "Refresh history")} disabled={historyBusy}
              onClick={refreshHistory} title={t("刷新历史记录", "Refresh history")} type="button">
              <RefreshCw aria-hidden="true" className={historyBusy ? "spin" : ""} size={13} />
            </button>
          </header>
          {threadsQuery.isLoading && <LoadingState label={t("加载任务历史", "Loading task history")} />}
          {threadsQuery.isError && <ErrorState error={threadsQuery.error} />}
          {!threadsQuery.isLoading && !threadsQuery.isError && visibleThreads.length === 0 &&
            <div className="sidebar-history-empty"><Archive aria-hidden="true" size={15} />
              {t("暂无任务", "No tasks")}</div>}
          {visibleThreads.map((thread) => <div className={`sidebar-history-row-shell ${
            selectedThreadID === thread.id && activeSection === "conversation" ? "selected" : ""}`}
            key={thread.id}>
            <button className="resource-row sidebar-history-row"
              onClick={() => selectConversation(() => selectThread(thread.id))} type="button">
              <MessagesSquare aria-hidden="true" size={15} />
              <span className="sidebar-history-copy"><strong>{thread.title}</strong>
                <small>{shortID(thread.last_run_id)} · {formatCompactDate(thread.updated_at)}</small></span>
              <i aria-label={thread.composer_state}
                className={`history-status status-${thread.composer_state}`} />
            </button>
            <button aria-label={`${t("归档任务", "Archive task")} ${thread.title}`}
              className="sidebar-history-delete" onClick={() => {
                threadArchiveMutation.reset();
                setThreadArchiveCandidate(thread);
              }} title={t("归档任务", "Archive task")} type="button">
              <Trash2 aria-hidden="true" size={13} />
            </button>
          </div>)}
          <LoadMoreButton hasNextPage={Boolean(threadsQuery.hasNextPage)}
            isFetching={threadsQuery.isFetchingNextPage}
            onClick={() => void threadsQuery.fetchNextPage()} />
        </section>

        <section aria-labelledby="session-history-heading">
          <header className="sidebar-history-heading">
            <span id="session-history-heading">{t("历史对话", "Conversation history")}</span>
            <button aria-label={t("刷新历史记录", "Refresh history")} disabled={historyBusy} onClick={refreshHistory}
              title={t("刷新历史记录", "Refresh history")} type="button">
              <RefreshCw aria-hidden="true" className={historyBusy ? "spin" : ""} size={13} />
            </button>
          </header>
          {sessionsQuery.isLoading && <LoadingState label={t("加载历史对话", "Loading conversations")} />}
          {sessionsQuery.isError && <ErrorState error={sessionsQuery.error} />}
          {!sessionsQuery.isLoading && !sessionsQuery.isError && visibleSessions.length === 0 &&
            <div className="sidebar-history-empty"><Archive aria-hidden="true" size={15} />{t("暂无对话", "No conversations")}</div>}
          {visibleSessions.map((session) => (
            <div className={`sidebar-history-row-shell ${selectedSessionID === session.id &&
              activeSection === "conversation" ? "selected" : ""}`} key={session.id}>
              <button className="resource-row sidebar-history-row"
                onClick={() => selectConversation(() => selectSession(session.id))} type="button">
                <MessagesSquare aria-hidden="true" size={15} />
                <span className="sidebar-history-copy">
                  <strong>{session.title}</strong>
                  <small>{session.route} · {formatCompactDate(session.created_at)}</small>
                </span>
                <i aria-label={session.status} className={`history-status status-${session.status}`} />
              </button>
              <button aria-label={`${t("删除对话", "Delete conversation")} ${session.title}`}
                className="sidebar-history-delete" onClick={() => {
                  archiveMutation.reset();
                  setArchiveCandidate(session);
                }} title={t("删除对话", "Delete conversation")} type="button">
                <Trash2 aria-hidden="true" size={13} />
              </button>
            </div>
          ))}
          <LoadMoreButton hasNextPage={Boolean(sessionsQuery.hasNextPage)}
            isFetching={sessionsQuery.isFetchingNextPage}
            onClick={() => void sessionsQuery.fetchNextPage()} />
        </section>

        <section aria-labelledby="run-history-heading">
          <header className="sidebar-history-heading">
            <span id="run-history-heading">{t("运行记录", "Run history")}</span><small>{visibleRuns.length}</small>
          </header>
          {runsQuery.isLoading && <LoadingState label={t("加载运行记录", "Loading runs")} />}
          {runsQuery.isError && <ErrorState error={runsQuery.error} />}
          {!runsQuery.isLoading && !runsQuery.isError && visibleRuns.length === 0 &&
            <div className="sidebar-history-empty"><Archive aria-hidden="true" size={15} />{t("暂无任务", "No tasks")}</div>}
          {visibleRuns.map((run) => (
            <button className={`resource-row sidebar-history-row ${selectedRunID === run.id &&
              activeSection === "conversation" ? "selected" : ""}`} key={run.id}
              onClick={() => selectConversation(() => selectRun(run.id))} type="button">
              <ListTree aria-hidden="true" size={15} />
              <span className="sidebar-history-copy">
                <strong>{t("任务", "Task")} {shortID(run.mission_id)}</strong>
                <small>{shortID(run.id)} · {formatCompactDate(run.created_at)}</small>
              </span>
              <i aria-label={run.status} className={`history-status status-${run.status}`} />
            </button>
          ))}
          <LoadMoreButton hasNextPage={Boolean(runsQuery.hasNextPage)}
            isFetching={runsQuery.isFetchingNextPage}
            onClick={() => void runsQuery.fetchNextPage()} />
        </section>
      </div>

      <button className="sidebar-profile" onClick={onOpenSettings} type="button">
        <CircleUserRound aria-hidden="true" size={21} />
        <span><strong>{t("本地操作者", "Local operator")}</strong><small>{t("设置与账户", "Settings and account")}</small></span>
        <Settings aria-hidden="true" size={15} />
      </button>

      {archiveCandidate && <div className="desktop-dialog-backdrop" role="presentation">
        <section aria-labelledby="archive-session-title" aria-modal="true"
          className="desktop-dialog archive-session-dialog" ref={archiveDialogRef}
          role="dialog" tabIndex={-1}>
          <header>
            <div><span className="dialog-icon"><Trash2 aria-hidden="true" size={17} /></span>
              <div><h2 id="archive-session-title">{t("删除对话", "Delete conversation")}</h2>
                <small>{archiveCandidate.title}</small></div></div>
            <button aria-label={t("关闭", "Close")} className="icon-button"
              disabled={archiveMutation.isPending} onClick={() => setArchiveCandidate(null)}
              type="button"><X aria-hidden="true" size={16} /></button>
          </header>
          <div className="desktop-dialog-body archive-session-copy">
            <p>{t("此对话将从历史列表中移除。Run、消息和审计记录仍会保留。",
              "This conversation will be removed from history. Its Run, messages, and audit records remain available.")}</p>
            {archiveMutation.isError && <p className="connection-error">{archiveMutation.error instanceof Error
              ? archiveMutation.error.message : t("删除对话失败", "Could not delete conversation")}</p>}
          </div>
          <footer>
            <span />
            <div className="desktop-dialog-actions">
              <button className="dialog-secondary" disabled={archiveMutation.isPending}
                onClick={() => setArchiveCandidate(null)} type="button">{t("取消", "Cancel")}</button>
              <button className="dialog-danger" disabled={archiveMutation.isPending}
                onClick={() => archiveMutation.mutate(archiveCandidate)} type="button">
                <Trash2 aria-hidden="true" size={15} />{t("删除", "Delete")}
              </button>
            </div>
          </footer>
        </section>
      </div>}
      {threadArchiveCandidate && <div className="desktop-dialog-backdrop" role="presentation">
        <section aria-labelledby="archive-thread-title" aria-modal="true"
          className="desktop-dialog archive-session-dialog" ref={threadArchiveDialogRef}
          role="dialog" tabIndex={-1}>
          <header><div><span className="dialog-icon"><Trash2 aria-hidden="true" size={17} /></span>
            <div><h2 id="archive-thread-title">{t("归档任务", "Archive task")}</h2>
              <small>{threadArchiveCandidate.title}</small></div></div>
            <button aria-label={t("关闭", "Close")} className="icon-button"
              disabled={threadArchiveMutation.isPending}
              onClick={() => setThreadArchiveCandidate(null)} type="button">
              <X aria-hidden="true" size={16} /></button></header>
          <div className="desktop-dialog-body archive-session-copy">
            <p>{t("任务会从活动历史中移除；其所有 Run、消息和审计记录仍保持绑定，可恢复或导出。",
              "The task leaves active history; every Run, message, and audit record stays bound for restore or export.")}</p>
            {threadArchiveMutation.isError && <p className="connection-error">
              {threadArchiveMutation.error instanceof Error ? threadArchiveMutation.error.message :
                t("归档任务失败", "Could not archive task")}</p>}
          </div>
          <footer><span /><div className="desktop-dialog-actions">
            <button className="dialog-secondary" disabled={threadArchiveMutation.isPending}
              onClick={() => setThreadArchiveCandidate(null)} type="button">{t("取消", "Cancel")}</button>
            <button className="dialog-danger" disabled={threadArchiveMutation.isPending}
              onClick={() => threadArchiveMutation.mutate(threadArchiveCandidate)} type="button">
              <Trash2 aria-hidden="true" size={15} />{t("归档", "Archive")}</button>
          </div></footer>
        </section>
      </div>}
    </aside>
  );
}
