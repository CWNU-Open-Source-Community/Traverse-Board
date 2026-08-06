import { useLocale } from "../lib/locale";

type PrayuBrandVariant = "compact" | "hero" | "icon";

export function PrayuBrand({ className = "", variant = "compact" }: {
  className?: string;
  variant?: PrayuBrandVariant;
}) {
  const { t } = useLocale();
  const iconOnly = variant === "icon";
  return (
    <span aria-label="Prayu" className={`prayu-brand prayu-brand-${variant} ${className}`.trim()}
      role="img">
      <span aria-hidden="true" className="prayu-brand-icon">
        <span className="prayu-brand-glyph">P</span>
        <span className="prayu-brand-glint" />
      </span>
      {!iconOnly && <span aria-hidden="true" className="prayu-brand-copy">
        <strong>Prayu</strong>
        <small>{t("Agent 工作台", "Agent Workbench")}</small>
      </span>}
    </span>
  );
}
