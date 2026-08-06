import {
  ChevronRight,
  BookOpenCheck,
  ClipboardList,
  FileDiff,
  GitBranch,
  ListChecks,
  Play,
  ShieldCheck,
} from "lucide-react";
import type { RunDetailView } from "../api/types";
import { formatDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { StatusBadge } from "./common";
import type { ReceiptReviewFacts, ReceiptReviewNavigationTarget } from "./receipt-review-navigation";

export type CodeJourneyDestination = "overview" | "actions" | "diffs" | "repository" |
  "verify" | "handoff";

interface JourneyStage {
  id: string;
  label: string;
  state: string;
  destination: CodeJourneyDestination;
  icon: typeof ClipboardList;
}

const maxJourneyReceiptReviews = 3;

export function CodeJourney({ detail, receiptReviewFacts, receiptReviewFactsState = "ready",
  onNavigate, onOpenReceiptReview }: {
  detail: RunDetailView;
  receiptReviewFacts?: ReceiptReviewFacts;
  receiptReviewFactsState?: "loading" | "ready" | "unavailable";
  onNavigate: (destination: CodeJourneyDestination) => void;
  onOpenReceiptReview: (target: ReceiptReviewNavigationTarget) => void;
}) {
  const { t } = useLocale();
  const queued = detail.operator_steering.pending + detail.operator_steering.prepared;
  const planState = detail.mode.phase === "deliver" ? "deliver" :
    detail.plan_delivery?.selection ? "selected" :
      detail.plan_delivery?.operator_choice_needed ? "choice required" :
        detail.plan_delivery?.proposal ? "proposed" : "pending";
  const stages: JourneyStage[] = [
    { id: "scope", label: t("范围", "Scope"), state: detail.mission.workspace_id ? "bound" : "unbound",
      destination: "repository", icon: GitBranch },
    { id: "plan", label: t("计划", "Plan"), state: planState,
      destination: "overview", icon: ClipboardList },
    { id: "execute", label: t("排队与执行", "Queue and execute"),
      state: queued > 0 ? `${queued} queued` : detail.run.status,
      destination: "overview", icon: Play },
    { id: "review", label: t("审阅", "Review"), state: "per-file",
      destination: "actions", icon: ListChecks },
    { id: "verify", label: t("验证与报告", "Verify and report"), state: "inspect",
      destination: "verify", icon: ShieldCheck },
    { id: "handoff", label: t("交接", "Handoff"), state: "regenerable",
      destination: "handoff", icon: BookOpenCheck },
  ];
  return <section aria-label={t("代码交付流程", "Code delivery journey")} className="code-journey">
    <header className="projection-heading">
      <div><FileDiff aria-hidden="true" size={17} /><h2>{t("代码流程", "Code Journey")}</h2></div>
      <div><StatusBadge status={detail.mode.surface} />
        <StatusBadge status={detail.mode.phase} /></div>
    </header>
    <div className="code-journey-list">
      {stages.map(({ id, label, state, destination, icon: Icon }, index) =>
        <div className="code-journey-stage" key={id}>
          <span className="journey-index">{index + 1}</span>
          <Icon aria-hidden="true" size={16} />
          <strong>{label}</strong>
          <StatusBadge status={state} />
          <button aria-label={t(`打开${label}`, `Open ${label}`)} className="icon-button"
            onClick={() => onNavigate(destination)} title={t(`打开${label}`, `Open ${label}`)} type="button">
            <ChevronRight aria-hidden="true" size={16} />
          </button>
        </div>)}
    </div>
    <section aria-label={t("收据审阅审计事实", "Receipt review audit facts")} className="journey-audit-facts">
      <header><div><strong>{t("收据审阅审计", "Receipt review audit")}</strong><StatusBadge status="metadata only" />
        <StatusBadge status="non-authorizing" /></div>
        {receiptReviewFacts && <span>{t(`${receiptReviewFacts.metadata_confirmed_count} 已确认 / ${receiptReviewFacts.metadata_disputed_count} 有争议`, `${receiptReviewFacts.metadata_confirmed_count} confirmed / ${receiptReviewFacts.metadata_disputed_count} disputed`)}</span>}
      </header>
      {receiptReviewFactsState === "loading" && <p>{t("正在加载有界审计事实", "Loading bounded audit facts")}</p>}
      {receiptReviewFactsState === "unavailable" && <p>{t("审计事实不可用", "Audit facts unavailable")}</p>}
      {receiptReviewFactsState === "ready" && receiptReviewFacts?.references.length === 0 &&
        <p>{t("没有收据审阅事实", "No receipt review facts")}</p>}
      {receiptReviewFactsState === "ready" && receiptReviewFacts &&
        receiptReviewFacts.references.length > 0 && <ul>
          {receiptReviewFacts.references.slice(0, maxJourneyReceiptReviews).map((item) =>
            <li key={item.id}><span><strong>{shortID(item.receipt_id)}</strong>
              <small>{t("事件", "event")} {item.review_event_sequence} / {formatDate(item.reviewed_at)}</small></span>
              <StatusBadge status={item.decision.replaceAll("_", " ")} />
              <button aria-label={t(`在验证页打开收据审阅 ${item.id}`, `Open receipt review ${item.id} in Verify`)}
                className="icon-button" onClick={() => onOpenReceiptReview(item)}
                title={t("在验证页打开这条收据审阅", "Open exact receipt review in Verify")} type="button">
                <ChevronRight aria-hidden="true" size={15} />
              </button></li>)}
        </ul>}
      {receiptReviewFacts && (receiptReviewFacts.returned_count > maxJourneyReceiptReviews ||
        receiptReviewFacts.truncated) &&
        <footer>{t(`显示 ${Math.min(maxJourneyReceiptReviews,
          receiptReviewFacts.references.length)} / ${receiptReviewFacts.returned_count}`,
          `Showing ${Math.min(maxJourneyReceiptReviews,
            receiptReviewFacts.references.length)} of ${receiptReviewFacts.returned_count}`)}
          {receiptReviewFacts.truncated && <StatusBadge status="source truncated" />}</footer>}
    </section>
    <footer>
      <span>{t("Go 控制平面", "Go control plane")}</span>
      <span>{t("独立变更操作", "Independent mutations")}</span>
      <button className="compact-command" onClick={() => onNavigate("diffs")} type="button">
        <FileDiff aria-hidden="true" size={14} />{t("打开差异", "Open diffs")}
      </button>
    </footer>
  </section>;
}
