import { useQuery } from "@tanstack/react-query";
import { AlarmClock, FileDiff, ListChecks, MessageSquareMore, RefreshCw, ShieldCheck } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { OperatorActionItemView } from "../api/types";
import { formatDate } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";

const actionPresentation: Record<OperatorActionItemView["kind"], {
  chinese: string;
  english: string;
  icon: typeof ListChecks;
}> = {
  steering_pending: { chinese: "已排队的操作者输入", english: "Queued operator input", icon: MessageSquareMore },
  approval_pending: { chinese: "审批审阅", english: "Approval review", icon: ShieldCheck },
  file_edit_review: { chinese: "文件编辑审阅", english: "File edit review", icon: FileDiff },
  file_edit_apply: { chinese: "已批准编辑待应用", english: "Approved edit ready", icon: FileDiff },
  wake_due: { chinese: "定时唤醒已到期", english: "Scheduled wake due", icon: AlarmClock },
};

export function OperatorActionCenter({ client, runID, onNavigate }: {
  client: CyberAgentClient;
  runID: string;
  onNavigate: (destination: OperatorActionItemView["destination"]) => void;
}) {
  const { t } = useLocale();
  const query = useQuery({
    queryKey: ["run", runID, "operator-actions"],
    queryFn: ({ signal }) => client.operatorActionCenter(runID, signal),
    enabled: Boolean(runID),
  });

  return <section className="operator-action-center" aria-label={t("操作者操作中心", "Operator action center")}>
    <header className="operator-list-header">
      <div><ListChecks aria-hidden="true" size={16} /><h2>{t("操作者操作", "Operator actions")}</h2></div>
      <div>
        {query.data?.truncated && <StatusBadge status="truncated" />}
        <button aria-label={t("刷新操作者操作", "Refresh operator actions")} className="icon-button"
          disabled={query.isFetching} onClick={() => void query.refetch()}
          title={t("刷新操作", "Refresh actions")} type="button">
          <RefreshCw aria-hidden="true" className={query.isFetching ? "spin" : ""} size={15} />
        </button>
      </div>
    </header>
    {query.isLoading && <LoadingState label={t("正在加载操作者操作", "Loading operator actions")} />}
    {query.isError && <ErrorState error={query.error} />}
    {query.data?.items.length === 0 && <EmptyState>{t("没有待处理的操作者操作", "No operator action is pending")}</EmptyState>}
    {query.data && query.data.items.length > 0 && <div className="operator-action-list">
      {query.data.items.map((item) => {
        const presentation = actionPresentation[item.kind];
        const Icon = presentation.icon;
        return <button key={item.id} onClick={() => onNavigate(item.destination)} type="button">
          <Icon aria-hidden="true" size={16} />
          <span><strong>{t(presentation.chinese, presentation.english)}</strong><code>{item.id}</code></span>
          <StatusBadge status={item.state} />
          <time dateTime={item.due_at ?? item.available_at}>
            {formatDate(item.due_at ?? item.available_at)}
          </time>
        </button>;
      })}
    </div>}
  </section>;
}
