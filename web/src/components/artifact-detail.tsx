import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { CyberAgentClient } from "../api/client";
import { formatBytes, formatDate } from "../lib/format";
import { useLocale } from "../lib/locale";
import { ErrorState, KeyValue, LoadingState } from "./common";

export function ArtifactDetail({ client, id, expected, children }: {
  client: CyberAgentClient;
  id: string;
  expected?: { runID: string; sourceID: string; sha256: string; sizeBytes: number; stream: string };
  children?: ReactNode;
}) {
  const { t } = useLocale();
  const query = useQuery({ queryKey: ["artifact", id], queryFn: ({ signal }) => client.getArtifact(id, signal) });
  if (query.isLoading) return <LoadingState label={t("加载产物详情", "Loading Artifact details")} />;
  const item = query.data;
  const mismatched = item && expected && (item.run_id !== expected.runID || item.source_id !== expected.sourceID ||
    item.sha256 !== expected.sha256 || item.size_bytes !== expected.sizeBytes || item.stream !== expected.stream);
  if (query.isError || !item || mismatched) return <div>
    <ErrorState error={mismatched ? new Error(t("产物记录与报告的执行、来源或摘要不匹配。", "Artifact metadata does not match the report's execution, source, or digest.")) : query.error} />
    <button disabled={query.isFetching} onClick={() => void query.refetch()} type="button">{t("重试产物记录", "Retry artifact metadata")}</button>
  </div>;
  const metadata = <dl className="detail-grid">
    <KeyValue label="ID" value={item.id} />
    <KeyValue label="Run" value={item.run_id} />
    <KeyValue label={t("Run 内 Session", "Run-local Session")} value={item.session_id} />
    <KeyValue label="Workspace" value={item.workspace_id} />
    <KeyValue label={t("类型", "Kind")} value={item.kind} />
    <KeyValue label={t("来源", "Source")} value={item.source_id} />
    <KeyValue label={t("工具", "Tool")} value={item.tool_name} />
    <KeyValue label={t("流", "Stream")} value={item.stream} />
    <KeyValue label="MIME" value={item.mime} />
    <KeyValue label={t("编码", "Encoding")} value={item.encoding} />
    <KeyValue label={t("大小", "Size")} value={formatBytes(item.size_bytes)} />
    <KeyValue label={t("已脱敏", "Redacted")} value={item.redacted ? t("是", "yes") : t("否", "no")} />
    <KeyValue label="SHA-256" value={<code>{item.sha256}</code>} />
    <KeyValue label={t("创建时间", "Created")} value={formatDate(item.created_at)} />
  </dl>;
  return children ? <>
    <details><summary>{t("输出来源记录", "Output source record")}</summary>{metadata}</details>
    {children}
  </> : metadata;
}
