import { useQuery } from "@tanstack/react-query";
import type { APIClient } from "../api/client";
import { useLocale } from "../lib/locale";
import { ErrorState, LoadingState, StatusBadge } from "./common";

const SafeWebReadinessProduct = "chrome";

export function SafeWebReadinessPanel({ client }: { client: APIClient }) {
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
    return <div><ErrorState error={query.error} />
      <button className="settings-action" disabled={query.isFetching} onClick={() => void query.refetch()} type="button">
        {t("重新检查网页环境", "Check web environment again")}</button></div>;
  }
  const readiness = query.data;
  return (
    <div className="safe-web-readiness" aria-label={t("Safe Web readiness", "Safe Web readiness")}>
      <StatusBadge
        status={readiness.ready ? "ready" : (readiness.blocking_reason ?? "blocked")}
      />
      <p>{readiness.ready ? t("网页隔离检查已通过。浏览器操作遵循任务的网页访问范围与审批。", "Web isolation checks passed. Browser operations follow the task's web scope and approvals.")
        : readiness.blocking_reason === "review_missing" || readiness.blocking_reason === "review_not_accepted"
          ? t("浏览器隔离证据待审查。在服务运行环境中完成审查后，重新检查此处状态。", "Browser isolation evidence needs review. Complete the review in the service environment, then check this status again.")
          : readiness.blocking_reason === "evidence_not_passed"
            ? t("浏览器隔离检查需要修复。处理服务诊断中的失败项，重新采集证据并审查后，再检查状态。", "Browser isolation checks need repair. Resolve failed service diagnostics, collect and review new evidence, then check this status again.")
            : t("为当前浏览器和运行环境重新采集隔离证据，并完成审查后重新检查状态。", "Collect isolation evidence for the current browser and environment, complete its review, then check this status again.")}</p>
      {!readiness.ready && (
        <p className="safe-web-readiness-blocking-reason">
          {t("阻塞原因", "Blocking reason")}: {readiness.blocking_reason}
        </p>
      )}
      <button className="settings-action" disabled={query.isFetching} onClick={() => void query.refetch()} type="button">
        {query.isFetching ? t("正在检查…", "Checking…") : t("重新检查网页环境", "Check web environment again")}</button>
    </div>
  );
}
