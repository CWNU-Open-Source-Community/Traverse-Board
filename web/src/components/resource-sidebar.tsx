import { useEffect, useMemo, useState } from "react";
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
  X,
} from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { RunView, SessionView } from "../api/types";
import { usePagedResource } from "../hooks/use-paged-resource";
import { formatCompactDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { useConnectionStore } from "../state/connection";
import { ErrorState, LoadMoreButton, LoadingState } from "./common";
import { PrayuBrand } from "./prayu-brand";

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
  const kind = useConnectionStore((state) => state.resourceKind);
  const selectedRunID = useConnectionStore((state) => state.selectedRunID);
  const selectedSessionID = useConnectionStore((state) => state.selectedSessionID);
  const selectRun = useConnectionStore((state) => state.selectRun);
  const selectSession = useConnectionStore((state) => state.selectSession);

  const runsQuery = usePagedResource<RunView>(client, ["runs"], "/runs", { limit: 50 }, true);
  const sessionsQuery = usePagedResource<SessionView>(client, ["sessions"], "/sessions",
    { limit: 50 }, true);
  const runs = useMemo(() => runsQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [runsQuery.data]);
  const sessions = useMemo(() => sessionsQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [sessionsQuery.data]);
  const normalizedSearch = search.trim().toLowerCase();
  const visibleRuns = runs.filter((run) => !normalizedSearch ||
    `${run.id} ${run.mission_id} ${run.status}`.toLowerCase().includes(normalizedSearch));
  const visibleSessions = sessions.filter((session) => !normalizedSearch ||
    `${session.id} ${session.title} ${session.route}`.toLowerCase().includes(normalizedSearch));

  useEffect(() => {
    if (kind === "run" && !runsQuery.isLoading && !runsQuery.isFetching &&
      !runs.some((run) => run.id === selectedRunID)) {
      if (runs[0]) selectRun(runs[0].id);
      else if (sessions[0]) selectSession(sessions[0].id);
    }
  }, [kind, runs, runsQuery.isFetching, runsQuery.isLoading, selectRun, selectedRunID,
    selectSession, sessions]);

  useEffect(() => {
    if (kind === "session" && !sessionsQuery.isLoading && !sessionsQuery.isFetching &&
      !sessions.some((session) => session.id === selectedSessionID)) {
      if (sessions[0]) selectSession(sessions[0].id);
      else if (runs[0]) selectRun(runs[0].id);
    }
  }, [kind, runs, selectRun, selectSession, selectedSessionID, sessions,
    sessionsQuery.isFetching, sessionsQuery.isLoading]);

  const selectConversation = (select: () => void) => {
    select();
    onNavigate?.("conversation");
  };
  const refreshHistory = () => void Promise.all([runsQuery.refetch(), sessionsQuery.refetch()]);
  const historyBusy = runsQuery.isFetching || sessionsQuery.isFetching;

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
            <button className={`resource-row sidebar-history-row ${selectedSessionID === session.id &&
              activeSection === "conversation" ? "selected" : ""}`} key={session.id}
              onClick={() => selectConversation(() => selectSession(session.id))} type="button">
              <MessagesSquare aria-hidden="true" size={15} />
              <span className="sidebar-history-copy">
                <strong>{session.title}</strong>
                <small>{session.route} · {formatCompactDate(session.updated_at)}</small>
              </span>
              <i aria-label={session.status} className={`history-status status-${session.status}`} />
            </button>
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
                <small>{shortID(run.id)} · {formatCompactDate(run.updated_at)}</small>
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
    </aside>
  );
}
