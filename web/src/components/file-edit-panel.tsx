import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronRight, FileCheck2, FileDiff, FileText, History,
  LoaderCircle, PanelRightClose, X } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { FileEditPreviewView, FileEditReviewRequestView, OperationReceiptView } from "../api/types";
import { formatBytes, formatDate } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";
import { OperationReceipt } from "./operation-receipt";
import { FileProposalRecovery } from "./file-proposal-recovery";
import { parseUnifiedDiff, type UnifiedDiff as ParsedUnifiedDiff } from "./unified-diff";

export function FileEditPanel({ client, runID }: { client: CyberAgentClient; runID: string }) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [receipts, setReceipts] = useState<Record<string, OperationReceiptView>>({});
  const [recoveryEditID, setRecoveryEditID] = useState("");
  const [selectedEditID, setSelectedEditID] = useState("");
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
  const parsedDiffs = new Map(query.data.items.map((edit) => [edit.id, parseUnifiedDiff(edit.diff)]));
  const additions = [...parsedDiffs.values()].reduce((total, diff) => total + diff.additions, 0);
  const deletions = [...parsedDiffs.values()].reduce((total, diff) => total + diff.deletions, 0);
  const selectedEdit = query.data.items.find((edit) => edit.id === selectedEditID);
  return <section className="file-edit-panel" aria-label={t("文件编辑预览", "File edit previews")}>
    <header className="projection-heading">
      <div><FileDiff aria-hidden="true" size={17} /><h2>{t("差异审阅", "Diff review")}</h2></div>
      <span>{t("已编辑", "Edited")} {query.data.items.length}{query.data.truncated ? "+" : ""} {t("个文件", "files")}
        <b className="diff-additions">+{additions}</b><b className="diff-deletions">-{deletions}</b></span>
    </header>
    <div aria-label={t("多文件变更集", "Multi-file change set")} className="file-change-set-summary">
      <span>{t("待审", "Pending")} <strong>{changeSet.proposed_count}</strong></span>
      <span>{t("已批准", "Approved")} <strong>{changeSet.approved_count}</strong></span>
      <span>{t("已应用", "Applied")} <strong>{changeSet.applied_count}</strong></span>
      {(changeSet.denied_count > 0 || changeSet.failed_count > 0) &&
        <span>{t("拒绝 / 失败", "Denied / failed")} <strong>{changeSet.denied_count} / {changeSet.failed_count}</strong></span>}
      <div className="file-change-set-policy">
        {partial && <StatusBadge status="partial" />}
        <span>{formatBytes(changeSet.total_diff_bytes)} / {t("逐文件授权", "per-file authority")}</span>
      </div>
    </div>
    <div className={`file-review-workspace${selectedEdit ? " is-reviewing" : ""}`}>
      <div className="file-edit-list" aria-label={t("已更改文件", "Changed files")}>
      {query.data.items.map((edit) => {
        const diff = parsedDiffs.get(edit.id) ?? parseUnifiedDiff("");
        return <button aria-pressed={edit.id === selectedEditID} className="file-edit-row"
          key={edit.id} onClick={() => setSelectedEditID(edit.id)} type="button">
          <FileText aria-hidden="true" size={15} />
          <code>{edit.path}</code>
          <span className="file-edit-counts"><b className="diff-additions">+{diff.additions}</b>
            <b className="diff-deletions">-{diff.deletions}</b></span>
          {edit.secrets_redacted && <span>{t("已脱敏", "redacted")}</span>}
          <StatusBadge status={edit.status} />
          <ChevronRight aria-hidden="true" size={15} />
        </button>;
      })}
      </div>
      {selectedEdit && <FileReviewDrawer applyEnabled={query.data.apply_enabled}
        client={client} diff={parsedDiffs.get(selectedEdit.id) ?? parseUnifiedDiff("")}
        edit={selectedEdit} onApply={() => apply.mutate({ editID: selectedEdit.id })}
        onClose={() => setSelectedEditID("")} onRecover={() => setRecoveryEditID(selectedEdit.id)}
        onReview={(action) => review.mutate({ editID: selectedEdit.id, action })}
        receipt={receipts[selectedEdit.id]} reviewing={review.isPending && review.variables?.editID === selectedEdit.id}
        reviewAction={review.variables?.action} applying={apply.isPending && apply.variables?.editID === selectedEdit.id} />}
    </div>
    {recoveryEditID && <FileProposalRecovery client={client} editID={recoveryEditID}
      onClose={() => setRecoveryEditID("")} runID={runID} />}
    {(review.isError || apply.isError) && <div className="inline-warning" role="alert">
      {operationError instanceof Error ? operationError.message : t("文件编辑操作失败", "File edit operation failed")}
    </div>}
  </section>;
}

function FileReviewDrawer({ applyEnabled, applying, client, diff, edit, onApply, onClose,
  onRecover, onReview, receipt, reviewing, reviewAction }: {
    applyEnabled: boolean;
    applying: boolean;
    client: CyberAgentClient;
    diff: ParsedUnifiedDiff;
    edit: FileEditPreviewView;
    onApply: () => void;
    onClose: () => void;
    onRecover: () => void;
    onReview: (action: FileEditReviewRequestView["action"]) => void;
    receipt?: OperationReceiptView;
    reviewing: boolean;
    reviewAction?: FileEditReviewRequestView["action"];
  }) {
  const { t } = useLocale();
  return <aside aria-label={t(`审阅 ${edit.path}`, `Review ${edit.path}`)} className="file-review-drawer">
    <header>
      <div><code>{edit.path}</code><StatusBadge status={edit.status} /></div>
      <button aria-label={t("关闭审阅", "Close review")} className="icon-button" onClick={onClose}
        title={t("关闭审阅", "Close review")} type="button">
        <PanelRightClose aria-hidden="true" size={16} />
      </button>
    </header>
    <div className="file-review-meta">
      <span><b className="diff-additions">+{diff.additions}</b> <b className="diff-deletions">-{diff.deletions}</b></span>
      <span>{edit.secrets_redacted ? t("敏感内容已脱敏", "Sensitive content redacted") : t("未触发脱敏", "No redaction triggered")}</span>
      <time dateTime={edit.updated_at}>{formatDate(edit.updated_at)}</time>
    </div>
    <UnifiedDiffView diff={diff} />
    {receipt && <OperationReceipt receipt={receipt} />}
    <footer>
      <span>{t("应用权限", "Apply authority")}: {edit.apply_enabled ? t("就绪", "ready") : t("禁用", "disabled")}</span>
      <div>
        {client.hasFileEditProposals && edit.status === "proposed" &&
          <button aria-label={t(`恢复 ${edit.path}`, `Recover ${edit.path}`)} className="icon-button"
            disabled={reviewing || applying} onClick={onRecover}
            title={t("恢复持久化待审提案", "Recover durable pending proposal")} type="button">
            <History aria-hidden="true" size={15} />
          </button>}
        {client.hasFileEditApply && applyEnabled && edit.apply_enabled &&
          <button aria-label={t(`应用 ${edit.path}`, `Apply ${edit.path}`)} className="icon-button"
            disabled={applying || reviewing} onClick={onApply}
            title={t("应用已批准的文件编辑", "Apply approved file edit")} type="button">
            {applying ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
              : <FileCheck2 aria-hidden="true" size={15} />}
          </button>}
        {client.hasFileEditReview && edit.allowed_actions.includes("approve_intent") &&
          <button aria-label={t(`批准编辑意图 ${edit.path}`, `Approve intent ${edit.path}`)} className="icon-button"
            disabled={reviewing} onClick={() => onReview("approve_intent")}
            title={t("批准意图但暂不写入文件", "Approve intent without writing the file")} type="button">
            {reviewing && reviewAction === "approve_intent"
              ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
              : <Check aria-hidden="true" size={15} />}
          </button>}
        {client.hasFileEditReview && edit.allowed_actions.includes("deny") &&
          <button aria-label={t(`拒绝 ${edit.path}`, `Deny ${edit.path}`)} className="icon-button"
            disabled={reviewing} onClick={() => onReview("deny")}
            title={t("拒绝文件编辑", "Deny file edit")} type="button">
            {reviewing && reviewAction === "deny"
              ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
              : <X aria-hidden="true" size={15} />}
          </button>}
      </div>
    </footer>
  </aside>;
}

function UnifiedDiffView({ diff }: { diff: ParsedUnifiedDiff }) {
  return <div className="unified-diff" role="table">
    {diff.lines.map((line, index) => <div className={`unified-diff-line is-${line.kind}`}
      key={`${index}-${line.kind}`} role="row">
      <span aria-hidden="true" className="unified-diff-number">{line.oldLine ?? ""}</span>
      <span aria-hidden="true" className="unified-diff-number">{line.newLine ?? ""}</span>
      <span aria-hidden="true" className="unified-diff-marker">{line.marker}</span>
      <code>{line.text || " "}</code>
    </div>)}
  </div>;
}
