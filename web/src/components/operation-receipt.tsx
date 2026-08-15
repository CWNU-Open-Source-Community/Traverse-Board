import { CheckCircle2, History, ShieldAlert } from "lucide-react";
import type { OperationReceiptView } from "../api/types";
import { useLocale } from "../lib/locale";

export function OperationReceipt({ receipt }: { receipt: OperationReceiptView }) {
  const { t } = useLocale();
  const pending = receipt.cleanup_state === "pending_review";
  const failed = receipt.outcome === "failed";
  const warning = pending || failed;
  const Icon = warning ? ShieldAlert : receipt.replayed ? History : CheckCircle2;
  return <div className={`operation-receipt ${warning ? "receipt-warning" : ""}`}
    role={failed ? "alert" : "status"}>
    <Icon aria-hidden="true" size={15} />
    <div>
      <strong>{receipt.outcome}</strong>
      <span>{receipt.kind.replaceAll("_", " ")} / {t("持久化", "durable")}{receipt.replayed ? t(" / 已重放", " / replayed") : ""}</span>
      {(receipt.retry_strategy || receipt.recovery_action) && <small>{t("重试", "Retry")}: {receipt.retry_strategy || "-"} · {t("恢复", "Recovery")}: {receipt.recovery_action || "-"}</small>}
      {failed && <small>{t("相同操作键会重放这份持久化失败结果。", "The durable failed result will replay for the same operation key.")}</small>}
      {pending && <small>{t("暂存区等待清理；请在清理宽限期后使用相同操作键重试。", "Staging cleanup is pending. Retry the same operation after the cleanup grace period.")}</small>}
    </div>
  </div>;
}
