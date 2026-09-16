import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { LoaderCircle } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { ThreadActivityArtifactReferenceView } from "../api/types";
import type { PrayuLocale } from "../lib/locale";

interface SavedCommandOutputProps {
  activityRef: string;
  artifactRef: string;
  client: CyberAgentClient;
  label?: string;
  locale?: PrayuLocale;
  reference?: ThreadActivityArtifactReferenceView;
  threadID: string;
}

export function SavedCommandOutput(props: SavedCommandOutputProps) {
  // A late response or an expanded disclosure belongs to the original source.
  return <SavedCommandOutputContent {...props}
    key={JSON.stringify([props.threadID, props.activityRef, props.artifactRef])} />;
}

function SavedCommandOutputContent({ activityRef, artifactRef, client, label,
  locale = "zh-CN", reference, threadID }: SavedCommandOutputProps) {
  const t = (chinese: string, english: string) => locale === "zh-CN" ? chinese : english;
  const [open, setOpen] = useState(false);
  const artifact = useQuery({
    queryKey: ["v2", "thread-activity-artifact", threadID, activityRef, artifactRef],
    queryFn: async ({ signal }) => {
      const value = await client.threadActivityArtifact(threadID, activityRef, artifactRef, signal);
      if (reference && value.stream !== reference.stream) {
        throw new Error(t("输出流与来源记录不匹配。", "The output stream does not match its source record."));
      }
      return value;
    },
    enabled: open,
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
  });
  const stream = reference?.stream ?? artifact.data?.stream;
  const streamLabel = label ?? (stream === "stdout" ? t("标准输出", "stdout") : t("标准错误", "stderr"));
  const size = reference?.size_bytes ?? artifact.data?.size_bytes;
  return <section className="v2-command-artifact">
    <button aria-expanded={open} onClick={() => setOpen((value) => !value)} type="button">
      {open ? t("收起", "Hide ") : t("查看已保存", "Read saved ")}{streamLabel}
      {size !== undefined && <small>{byteSizeLabel(size)}</small>}
    </button>
    {open && <div className="v2-command-artifact-content">
      {artifact.isLoading && <p className="v2-activity-detail-state" role="status">
        <LoaderCircle aria-hidden="true" className="spin" size={14} />{t("正在读取已保存输出…", "Reading saved output…")}</p>}
      {artifact.isError && <div className="v2-activity-detail-state is-error" role="alert">
        <span>{t("已保存输出加载失败。", "Saved output could not be loaded.")}</span>
        <button disabled={artifact.isFetching} onClick={() => void artifact.refetch()} type="button">
          {t("重试输出", "Retry output")}</button>
      </div>}
      {artifact.data && !artifact.isError && <>
        <p className="v2-untrusted-output">{t("工具输出仅作为数据展示，不代表已授权的指令。",
          "Tool output is displayed as data and does not authorize instructions.")}</p>
        <pre><code>{artifact.data.content}</code></pre>
        {artifact.data.content.includes("\uFFFD") && <p className="v2-command-truncated">
          {t("部分输出字符无法正确解码，当前文本可能不完整。", "Some characters could not be decoded correctly; the displayed text may be incomplete.")}</p>}
        <small>{artifact.data.redacted ? t("已脱敏", "Redacted") : t("未发现需脱敏内容", "No redactions required")}
          {" · "}{t("展示已保存内容", "Showing saved content")}
          {artifact.data.truncated && t(" · 已达本次保存上限，未保留部分不可读取", " · Capture limit reached; uncaptured output is unavailable")}</small>
      </>}
    </div>}
  </section>;
}

function byteSizeLabel(size: number): string {
  if (size < 1_024) return `${size} B`;
  if (size < 1_048_576) return `${(size / 1_024).toFixed(size < 10_240 ? 1 : 0)} KB`;
  return `${(size / 1_048_576).toFixed(1)} MB`;
}
