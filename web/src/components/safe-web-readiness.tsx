import { useQuery } from "@tanstack/react-query";
import type { CyberAgentClient } from "../api/client";
import { useLocale } from "../lib/locale";
import { ErrorState, LoadingState, StatusBadge } from "./common";

const SafeWebReadinessProduct = "chrome";

export function SafeWebReadinessPanel({ client }: { client: CyberAgentClient }) {
  const { t } = useLocale();
  const query = useQuery({
    queryKey: ["safe-web-readiness", SafeWebReadinessProduct],
    queryFn: ({ signal }) => client.safeWebReadiness(SafeWebReadinessProduct, signal),
    retry: false,
  });
  if (query.isLoading) {
    return <LoadingState label={t("正在检查 Safe Web readiness", "Checking Safe Web readiness")} />;
  }
  if (query.isError || !query.data) {
    return <ErrorState error={query.error} />;
  }
  const readiness = query.data;
  return (
    <div className="safe-web-readiness" aria-label={t("Safe Web readiness", "Safe Web readiness")}>
      <StatusBadge
        status={readiness.ready ? "ready" : (readiness.blocking_reason ?? "blocked")}
      />
      {!readiness.ready && (
        <p className="safe-web-readiness-blocking-reason">
          {t("阻塞原因", "Blocking reason")}: {readiness.blocking_reason}
        </p>
      )}
    </div>
  );
}
