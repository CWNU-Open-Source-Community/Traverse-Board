import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, FileCheck2, FileDiff, History, LoaderCircle, X } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { FileEditReviewRequestView, OperationReceiptView } from "../api/types";
import { formatBytes, formatDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";
import { OperationReceipt } from "./operation-receipt";
import { FileProposalRecovery } from "./file-proposal-recovery";

export function FileEditPanel({ client, runID }: { client: CyberAgentClient; runID: string }) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [receipts, setReceipts] = useState<Record<string, OperationReceiptView>>({});
  const [recoveryEditID, setRecoveryEditID] = useState("");
  const applyKeys = useRef(new Map<string, string>());
  const query = useQuery({
    queryKey: ["run", runID, "file-edits"],
    queryFn: ({ signal }) => client.fileEditQueue(runID, signal),
  });
  const changeSetQuery = useQuery({
    queryKey: ["run", runID, "file-edit-change-set"],
    queryFn: ({ signal }) => client.fileEditChangeSet(runID, signal),
  });
  const review = useMutation({
    mutationFn: ({ editID, action }: { editID: string;
      action: FileEditReviewRequestView["action"] }) =>
      client.reviewFileEdit(runID, editID, { version: "file_edit_review.v1", action }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "file-edits"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "file-edit-change-set"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "events"] });
    },
  });
  const apply = useMutation({
    mutationFn: ({ editID }: { editID: string }) => {
      let operationKey = applyKeys.current.get(editID);
      if (!operationKey) {
        operationKey = `web-file-apply-${globalThis.crypto.randomUUID()}`;
        applyKeys.current.set(editID, operationKey);
      }
      return client.applyFileEdit(runID, editID, { version: "file_edit_apply.v1" },
        operationKey);
    },
    onSuccess: (result) => {
      applyKeys.current.delete(result.edit.id);
      setReceipts((current) => ({ ...current, [result.edit.id]: result.receipt }));
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "file-edits"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "file-edit-change-set"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "events"] });
    },
  });
  if (query.isLoading || changeSetQuery.isLoading) {
    return <LoadingState label={t("正在加载文件编辑预览", "Loading file edit previews")} />;
  }
  if (query.isError || !query.data) return <ErrorState error={query.error} />;
  if (changeSetQuery.isError || !changeSetQuery.data) {
    return <ErrorState error={changeSetQuery.error} />;
  }
  if (query.data.items.length === 0) return <EmptyState>{t("没有文件编辑提案", "No file edit proposals")}</EmptyState>;
  const operationError = apply.error ?? review.error;
  const changeSet = changeSetQuery.data;
  const partial = changeSet.applied_count > 0 &&
    changeSet.applied_count < changeSet.returned_count;
  return <section className="file-edit-panel" aria-label={t("文件编辑预览", "File edit previews")}>
    <header className="projection-heading">
      <div><FileDiff aria-hidden="true" size={17} /><h2>{t("差异审阅", "Diff review")}</h2></div>
      <span>{query.data.items.length}{query.data.truncated ? "+" : ""}</span>
    </header>
    <div aria-label={t("多文件变更集", "Multi-file change set")} className="file-change-set-summary">
      <div><span>{t("已提案", "Proposed")}</span><strong>{changeSet.proposed_count}</strong></div>
      <div><span>{t("已批准", "Approved")}</span><strong>{changeSet.approved_count}</strong></div>
      <div><span>{t("已应用", "Applied")}</span><strong>{changeSet.applied_count}</strong></div>
      <div><span>{t("已拒绝", "Denied")}</span><strong>{changeSet.denied_count}</strong></div>
      <div><span>{t("失败", "Failed")}</span><strong>{changeSet.failed_count}</strong></div>
      <div className="file-change-set-policy">
        {partial && <StatusBadge status="partial" />}
        <span>{formatBytes(changeSet.total_diff_bytes)} / {t("逐文件授权", "per-file authority")}</span>
      </div>
    </div>
    <div className="file-edit-list">
      {query.data.items.map((edit) => {
        const active = review.isPending && review.variables?.editID === edit.id;
        const applying = apply.isPending && apply.variables?.editID === edit.id;
        return <details className="file-edit-row" key={edit.id} open={edit.status === "proposed" || undefined}>
          <summary>
            <code>{edit.path}</code>
            <span>{shortID(edit.id)}</span>
            {edit.secrets_redacted && <span>{t("已脱敏", "redacted")}</span>}
            <StatusBadge status={edit.status} />
          </summary>
          <div className="file-edit-body">
            <pre>{edit.diff}</pre>
            {receipts[edit.id] && <OperationReceipt receipt={receipts[edit.id]} />}
            <footer>
              <time dateTime={edit.updated_at}>{formatDate(edit.updated_at)}</time>
              <span>{t("应用权限", "Apply authority")}: {edit.apply_enabled ? t("就绪", "ready") : t("禁用", "disabled")}</span>
              {client.hasFileEditProposals && edit.status === "proposed" &&
                <button aria-label={t(`恢复 ${edit.path}`, `Recover ${edit.path}`)} className="icon-button"
                  disabled={review.isPending || apply.isPending}
                  onClick={() => setRecoveryEditID(edit.id)}
                  title={t("恢复持久化待审提案", "Recover durable pending proposal")} type="button">
                  <History aria-hidden="true" size={15} />
                </button>}
              {client.hasFileEditApply && query.data.apply_enabled && edit.apply_enabled &&
                <button aria-label={t(`应用 ${edit.path}`, `Apply ${edit.path}`)} className="icon-button"
                  disabled={apply.isPending || review.isPending}
                  onClick={() => apply.mutate({ editID: edit.id })}
                  title={t("应用已批准的文件编辑", "Apply approved file edit")} type="button">
                  {applying ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                    : <FileCheck2 aria-hidden="true" size={15} />}
                </button>}
              {client.hasFileEditReview && edit.allowed_actions.length > 0 && <div>
                {edit.allowed_actions.includes("approve_intent") &&
                  <button aria-label={t(`批准编辑意图 ${edit.path}`, `Approve intent ${edit.path}`)} className="icon-button"
                    disabled={review.isPending}
                    onClick={() => review.mutate({ editID: edit.id, action: "approve_intent" })}
                    title={t("批准意图但暂不写入文件", "Approve intent without writing the file")} type="button">
                    {active && review.variables?.action === "approve_intent"
                      ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                      : <Check aria-hidden="true" size={15} />}
                  </button>}
                {edit.allowed_actions.includes("deny") &&
                  <button aria-label={t(`拒绝 ${edit.path}`, `Deny ${edit.path}`)} className="icon-button"
                    disabled={review.isPending}
                    onClick={() => review.mutate({ editID: edit.id, action: "deny" })}
                    title={t("拒绝文件编辑", "Deny file edit")} type="button">
                    {active && review.variables?.action === "deny"
                      ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                      : <X aria-hidden="true" size={15} />}
                  </button>}
              </div>}
            </footer>
          </div>
        </details>;
      })}
    </div>
    {recoveryEditID && <FileProposalRecovery client={client} editID={recoveryEditID}
      onClose={() => setRecoveryEditID("")} runID={runID} />}
    {(review.isError || apply.isError) && <div className="inline-warning" role="alert">
      {operationError instanceof Error ? operationError.message : t("文件编辑操作失败", "File edit operation failed")}
    </div>}
  </section>;
}
