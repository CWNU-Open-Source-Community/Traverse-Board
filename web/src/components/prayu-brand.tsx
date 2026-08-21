import { useLocale } from "../lib/locale";
import brandMark from "../assets/traverse-board-mark.png";

type PrayuBrandVariant = "compact" | "hero" | "icon";

export function PrayuBrand({ className = "", variant = "compact" }: {
  className?: string;
  variant?: PrayuBrandVariant;
}) {
  const { t } = useLocale();
  const iconOnly = variant === "icon";
  const displayName = variant === "hero" ? "Traverse Board · 针路簿" : t("针路簿", "Traverse Board");
  return (
    <span aria-label={displayName} className={`prayu-brand prayu-brand-${variant} ${className}`.trim()}
      role="img">
      <img alt="" aria-hidden="true" className="prayu-brand-icon" src={brandMark} />
      {!iconOnly && <span aria-hidden="true" className="prayu-brand-copy">
        <strong>{variant === "hero" ? <><span>Traverse Board</span><span>针路簿</span></> : displayName}</strong>
        <small>{t("Agent 工作台", "Agent Workbench")}</small>
      </span>}
    </span>
  );
}
