import { useQuery } from "@tanstack/react-query";
import type { CyberAgentClient } from "../api/client";
import { formatDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";

export function ControlledCommandProposalPanel({ client, runID, threadID = "" }: {
  client: CyberAgentClient; runID: string; threadID?: string;
}) {
  const { t } = useLocale();
  const query = useQuery({ queryKey: ["run", runID, "command-proposals"],
    queryFn: ({ signal }) => client.controlledCommandProposals(runID, signal),
    enabled: runID !== "" });
  if (query.isLoading) return <LoadingState />;
  if (!query.data) return <ErrorState error={query.error} />;
  if (threadID && !query.data.items.length) return null;
  return <section className="approval-queue command-proposal-queue" aria-label={t("历史固定命令", "Historical fixed commands")}>
    <header className="approval-queue-header"><strong>{t("历史固定命令", "Historical fixed commands")}</strong></header>
    <p>{t("此处保留历史记录。新命令通过当前命令审批运行。", "Saved history. New commands use the current command approval flow.")}</p>
    {!query.data.items.length && <EmptyState>{t("没有历史记录", "No saved history")}</EmptyState>}
    {query.data.items.map((proposal) => <article className="approval-row command-proposal-row" key={proposal.id}>
      <div className="approval-row-main"><strong>{proposal.kind}</strong><code>{shortID(proposal.id)}</code>
        <StatusBadge status={proposal.result?.status ?? proposal.review?.decision ?? "retired"} />
        <time dateTime={proposal.created_at}>{formatDate(proposal.created_at)}</time></div>
      <p>{proposal.purpose}</p>{proposal.relative_path && <code>{proposal.relative_path}</code>}
      {proposal.review && <p>{proposal.review.reason}</p>}
      {proposal.untrusted_evidence && <pre>{proposal.untrusted_evidence}</pre>}
    </article>)}
  </section>;
}
