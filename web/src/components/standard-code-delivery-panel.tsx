import { useQuery } from "@tanstack/react-query";
import { FileCheck2, RefreshCw, RotateCcw, TestTube2 } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import { formatBytes, formatDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, KeyValue, LoadingState, StatusBadge } from "./common";

export function StandardCodeDeliveryPanel({ client, runID, onOpenArtifacts, onOpenCheckpoints,
  onOpenFile }: {
  client: CyberAgentClient;
  runID: string;
  onOpenArtifacts: () => void;
  onOpenCheckpoints: () => void;
  onOpenFile: (path: string) => void;
}) {
  const { t } = useLocale();
  const query = useQuery({
    queryKey: ["run", runID, "standard-code-delivery"],
    queryFn: ({ signal }) => client.standardCodeDelivery(runID, signal),
    enabled: Boolean(runID),
  });
  const report = query.data;
  return <section aria-label={t("Standard Code 交付", "Standard Code delivery")}
    className="standard-code-delivery-panel">
    <header className="projection-heading">
      <div><FileCheck2 aria-hidden="true" size={17} />
        <h2>{t("交付真实性", "Delivery truth")}</h2></div>
      <div>{report && <StatusBadge status={report.status} />}
        <button aria-label={t("刷新交付报告", "Refresh delivery report")} className="icon-button"
          disabled={query.isFetching} onClick={() => void query.refetch()} type="button">
          <RefreshCw aria-hidden="true" className={query.isFetching ? "spin" : ""} size={15} />
        </button></div>
    </header>
    {query.isLoading && <LoadingState label={t("加载交付报告", "Loading delivery report")} />}
    {query.isError && <ErrorState error={query.error} />}
    {report && <>
      <section className={`delivery-truth-summary delivery-truth-${report.status}`}>
        <div><strong>{report.verified ? t("已验证当前版本", "Current revision verified") :
          t("当前版本未验证", "Current revision is not verified")}</strong>
          <p>{t("状态只来自当前 Workspace revision 的终态命令证据。Agent 文本和 CI 名称不作为证明。",
            "Status comes only from terminal command evidence for the current Workspace revision. Agent prose and CI names are not proof.")}</p></div>
        <StatusBadge status={report.status} />
      </section>
      <dl className="handoff-grid delivery-truth-grid">
        <KeyValue label={t("受影响文件", "Affected files")} value={report.diff.changed_count} />
        <KeyValue label={t("已跟踪 / 未跟踪", "Tracked / untracked")}
          value={`${report.diff.tracked_count} / ${report.diff.untracked_count}`} />
        <KeyValue label={t("索引 / 工作树", "Index / worktree")}
          value={`${report.diff.index_count} / ${report.diff.worktree_count}`} />
        <KeyValue label={t("验证命令", "Verification commands")} value={report.verifications.length} />
        <KeyValue label={t("Diff 大小", "Diff size")} value={formatBytes(report.diff.bytes)} />
        <KeyValue label={t("冲突", "Conflicts")} value={report.diff.conflict_count} />
        <KeyValue label={t("恢复级别", "Recovery level")}
          value={<StatusBadge status={report.final_checkpoint.recovery_level} />} />
        <KeyValue label={t("记录时间", "Recorded") } value={formatDate(report.created_at)} />
      </dl>
      <div className="delivery-truth-identities">
        <div><span>{t("收据", "Receipt")}</span><code title={report.receipt_sha256}>{report.receipt_sha256}</code></div>
        <div><span>{t("Workspace revision", "Workspace revision")}</span>
          <code title={report.final_checkpoint.revision_sha256}>{report.final_checkpoint.revision_sha256}</code></div>
        <div><span>Diff SHA-256</span><code title={report.diff.sha256}>{report.diff.sha256}</code></div>
        <div><span>Checkpoint</span><code>{report.final_checkpoint.id}</code></div>
      </div>
      <div className="delivery-truth-columns">
        <section><h3>{t("受影响文件", "Affected files")}</h3>
          {report.diff.files.length === 0 ? <EmptyState>{t("没有文件变更", "No changed files")}</EmptyState> :
            <div className="delivery-truth-list">{report.diff.files.map((file) =>
              <div key={file.path_sha256}><span>{file.path ? <button className="link-button"
                onClick={() => onOpenFile(file.path!)} type="button">{file.path}</button> :
                <code>{t("路径已脱敏", "redacted path")} · {shortID(file.path_sha256)}</code>}
                <small>{[
                  file.committed && t("已提交", "committed"),
                  file.index_changed && t("索引", "index"),
                  file.worktree_changed && t("工作树", "worktree"),
                  file.untracked && t("未跟踪", "untracked"),
                  file.conflicted && t("冲突", "conflict"),
                ].filter(Boolean).join(" · ")}</small></span>
                <StatusBadge status={file.path_redacted ? "redacted" : "current"} /></div>)}</div>}
        </section>
        <section><h3><TestTube2 aria-hidden="true" size={14} />{t("验证命令", "Verification commands")}</h3>
          {report.verifications.length === 0 ? <EmptyState>{t("未运行验证", "No verification run")}</EmptyState> :
            <div className="delivery-truth-list">{report.verifications.map((verification) =>
              <div key={verification.job_id}><span><strong>{shortID(verification.job_id)}</strong>
                <small>{verification.state} · exit {verification.exit_code ?? "—"} · {t("重试", "retries")} {verification.retry_count}</small>
                {verification.output_truncated && <small className="inline-warning">{t("输出已截断", "output truncated")}</small>}
                {verification.artifacts.length > 0 && <button className="link-button"
                  onClick={onOpenArtifacts} type="button">{t(`打开 ${verification.artifacts.length} 个输出 Artifact`,
                    `Open ${verification.artifacts.length} output artifacts`)}</button>}</span>
                <StatusBadge status={verification.conclusion} /></div>)}</div>}
        </section>
      </div>
      {(report.reasons.length > 0 || report.uncovered_items.length > 0) &&
        <section className="delivery-truth-reasons"><h3>{t("结论与未覆盖项", "Conclusion and uncovered items")}</h3>
          <div>{report.reasons.map((reason) => <StatusBadge key={reason.provenance_sha256}
            label={reason.code.replaceAll("_", " ")} status={reason.code} />)}</div>
          {report.uncovered_items.map((item) => <p key={item.summary_sha256}>{item.summary}</p>)}
        </section>}
      <section className="delivery-truth-recovery"><div><RotateCcw aria-hidden="true" size={16} />
        <span><strong>{t("恢复入口", "Recovery entry points")}</strong>
          <small>{t("Checkpoint 仅覆盖记录的 Workspace 内容；Workspace 外副作用不在其承诺内。",
            "The Checkpoint covers recorded Workspace content only; effects outside it are not promised reversible.")}</small></span></div>
        <div><button className="compact-command" onClick={onOpenCheckpoints} type="button">Checkpoint</button>
          <button className="compact-command" onClick={onOpenCheckpoints} type="button">Undo</button>
          <button className="compact-command" onClick={onOpenCheckpoints} type="button">Rewind</button>
          <button className="compact-command" onClick={onOpenCheckpoints} type="button">Fork</button></div>
      </section>
      <p className="delivery-truth-boundary">{t(
        "本报告不会自动 commit、push、merge 或覆盖源文件，也不包含原始环境、无限输出、私有 reasoning 或绝对主机路径。",
        "This report does not automatically commit, push, merge, or overwrite source files, and contains no raw environment, unbounded output, private reasoning, or absolute host paths.")}</p>
    </>}
  </section>;
}
