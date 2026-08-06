import { useQuery } from "@tanstack/react-query";
import { GitBranch, RefreshCw } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";

export function RepositoryStatePanel({ client, workspaceID }: {
  client: CyberAgentClient;
  workspaceID: string;
}) {
  const { t } = useLocale();
  const query = useQuery({
    queryKey: ["workspace", workspaceID, "repository-state"],
    queryFn: ({ signal }) => client.repositoryState(workspaceID, signal),
    enabled: Boolean(workspaceID),
  });
  if (!workspaceID) return <EmptyState>{t("此 Run 未绑定工作区", "No Workspace is bound to this Run")}</EmptyState>;
  if (query.isLoading) return <LoadingState label={t("正在加载仓库状态", "Loading repository state")} />;
  if (query.isError || !query.data) return <ErrorState error={query.error} />;
  const state = query.data;
  if (!state.available) {
    return <section aria-label={t("仓库状态", "Repository state")} className="repository-state-panel">
      <header className="projection-heading">
        <div><GitBranch aria-hidden="true" size={17} /><h2>{t("仓库", "Repository")}</h2></div>
        <button aria-label={t("刷新仓库状态", "Refresh repository state")} className="icon-button"
          disabled={query.isFetching} onClick={() => void query.refetch()}
          title={t("刷新", "Refresh")} type="button"><RefreshCw aria-hidden="true" size={15} /></button>
      </header>
      <EmptyState>{t("已登记工作区根目录中没有 Git 仓库", "No Git repository at the registered Workspace root")}</EmptyState>
    </section>;
  }
  return <section aria-label={t("仓库状态", "Repository state")} className="repository-state-panel">
    <header className="projection-heading">
      <div><GitBranch aria-hidden="true" size={17} /><h2>{t("仓库", "Repository")}</h2></div>
      <div>
        {state.truncated && <StatusBadge status="truncated" />}
        <StatusBadge status={state.clean ? "clean" : "changed"} />
        <button aria-label={t("刷新仓库状态", "Refresh repository state")} className="icon-button"
          disabled={query.isFetching} onClick={() => void query.refetch()}
          title={t("刷新", "Refresh")} type="button"><RefreshCw aria-hidden="true" size={15} /></button>
      </div>
    </header>
    <div className="repository-reference">
      <span>{state.detached ? "detached" : state.branch || "unborn"}</span>
      {state.head && <code>{state.head}</code>}
      <small>{t("只读 / 本地元数据", "read-only / local metadata")}</small>
    </div>
    <div aria-label={t("仓库变更计数", "Repository change counts")} className="repository-counts">
      <div><span>{t("已暂存", "Staged")}</span><strong>{state.staged_count}</strong></div>
      <div><span>{t("工作树", "Worktree")}</span><strong>{state.worktree_count}</strong></div>
      <div><span>{t("未跟踪", "Untracked")}</span><strong>{state.untracked_count}</strong></div>
      <div><span>{t("冲突", "Conflicts")}</span><strong>{state.conflicted_count}</strong></div>
    </div>
    {state.changes.length === 0 ? <EmptyState>{t("工作树干净", "Working tree is clean")}</EmptyState> :
      <div className="repository-change-list" role="list">
        {state.changes.map((change) => <div key={change.path} role="listitem">
          <code>{change.path}</code>
          <span>{change.staging}</span>
          <span>{change.worktree}</span>
        </div>)}
      </div>}
  </section>;
}
