import { memo } from "react";
import { replaceEqualDeep } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { remarkCjkAutolinks } from "../../components/remark-cjk-autolinks";
import type { APIClient } from "../../api/client";
import type { NarrativeEntry } from "../projection/narrative";
import { V2ImagePreview } from "./image-input";
import { V2FileAttachments } from "./file-input";
import { V2ActivityGroup } from "./activity-detail";

interface NarrativeRowProps {
  client: APIClient;
  entry: NarrativeEntry;
  threadID: string;
}

const NarrativeRow = memo(function NarrativeRow({ client, entry, threadID }: NarrativeRowProps) {
  if (entry.kind === "user" && entry.status === "cancelled" && entry.promotedToMessageID) return <li className="v2-user-turn">
    <details className="v2-promoted-message-history">
      <summary title={entry.text}>排队消息已转为引导</summary>
      <div className="v2-promoted-message-content">{entry.text}<V2ImagePreview client={client} images={entry.images ?? []} />
        <V2FileAttachments client={client} attachments={entry.attachments ?? []} /></div>
    </details></li>;
  if (entry.kind === "user") return <li className="v2-user-turn">
    <div>{entry.text}<V2ImagePreview client={client} images={entry.images ?? []} />
      <V2FileAttachments client={client} attachments={entry.attachments ?? []} />{entry.status === "cancelled" &&
      <small className="v2-message-status">已取消，不会继续处理</small>}
      {entry.status === "pending" && <small className="v2-message-status">已接收</small>}
      {entry.deliveryMode === "steer" && entry.status !== "cancelled" && entry.status !== "pending" &&
        <small className="v2-message-status">已加入当前任务</small>}
      {entry.provisional && !entry.status && <small className="v2-message-status">正在发送…</small>}</div></li>;
  if (entry.kind === "assistant") return <li aria-live={entry.provisional ? "polite" : undefined}
    className={`v2-assistant-turn${entry.provisional ? " is-provisional" : ""}`}>
    <ReactMarkdown remarkPlugins={[remarkGfm, remarkCjkAutolinks]}>{entry.text}</ReactMarkdown></li>;
  if (entry.kind === "activity") return <li className="v2-activity-turn">
    <V2ActivityGroup client={client} entry={entry} threadID={threadID} /></li>;
  return <li className={`v2-notice tone-${entry.tone}`}>{entry.text}</li>;
}, (previous, next) => previous.client === next.client && previous.threadID === next.threadID &&
  // Live updates reuse durable entries by reference. A durable refresh can
  // rebuild a group: compare its complete public projection before skipping it.
  replaceEqualDeep(previous.entry, next.entry) === previous.entry);

export const V2Narrative = memo(function V2Narrative({ client, entries, threadID }: {
  client: APIClient;
  entries: NarrativeEntry[];
  threadID: string;
}) {
  return <ol className="v2-narrative">{entries.map((entry) =>
    <NarrativeRow client={client} entry={entry} key={entry.id} threadID={threadID} />)}</ol>;
});
