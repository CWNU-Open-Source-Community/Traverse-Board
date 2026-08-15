import {
  Bot,
  Check,
  ChevronRight,
  CircleDot,
  FilePenLine,
  Globe,
  ListChecks,
  MessageSquareText,
  Network,
  ShieldCheck,
  UserRound,
  Wrench,
} from "lucide-react";
import type { PublicModelStreamSnapshot, RunActivityItemView, RunActivityView } from "../api/types";
import type { PublicModelStreamStatus } from "../hooks/use-public-model-stream";
import { formatDate } from "../lib/format";
import { SafeMarkdown } from "./safe-markdown";

const sourceLabels: Record<RunActivityItemView["source"], string> = {
  harness: "Harness 事件",
  model: "模型公开更新",
  operator: "用户",
};

const statusLabels: Record<string, string> = {
  approved: "已批准",
  blocked: "已阻止",
  cancelled: "已取消",
  cancelling: "取消中",
  completed: "已完成",
  denied: "已拒绝",
  expired: "已超时",
  failed: "失败",
  pending: "待处理",
  running: "进行中",
  satisfied: "已满足",
  selected: "已选择",
  superseded: "已替换",
  waiting: "等待中",
};

function ActivityIcon({ kind, source }: Pick<RunActivityItemView, "kind" | "source">) {
  if (source === "model") return <Bot aria-hidden="true" size={16} />;
  if (source === "operator") return <UserRound aria-hidden="true" size={16} />;
  switch (kind) {
  case "approval":
    return <ShieldCheck aria-hidden="true" size={16} />;
  case "file_change":
    return <FilePenLine aria-hidden="true" size={16} />;
  case "plan":
    return <ListChecks aria-hidden="true" size={16} />;
  case "tool_call":
    return <Wrench aria-hidden="true" size={16} />;
  case "model_call":
    return <MessageSquareText aria-hidden="true" size={16} />;
  case "dependency":
    return <Network aria-hidden="true" size={16} />;
  case "browser":
    return <Globe aria-hidden="true" size={16} />;
  default:
    return <CircleDot aria-hidden="true" size={15} />;
  }
}

export function RunActivityTimeline({ activity, liveCommentary = null,
  liveStatus = "stopped", streamError = "" }: {
  activity: RunActivityView;
  liveCommentary?: PublicModelStreamSnapshot | null;
  liveStatus?: PublicModelStreamStatus;
  streamError?: string;
}) {
  if (activity.private_reasoning_included) {
    return (
      <section aria-label="Run 活动" className="run-activity">
        <div className="inline-warning">
          活动投影已拒绝：服务端声明其中包含模型私有推理。
        </div>
      </section>
    );
  }
  const provisional = liveCommentary?.content_kind === "tool_commentary" &&
    liveCommentary.text.trim() &&
    !hasDurablePublicUpdate(activity.items, liveCommentary)
    ? provisionalActivityItem(activity, liveCommentary, liveStatus) : null;
  return (
    <section aria-label="Run 活动" className="run-activity">
      <header className="run-activity-header">
        <div>
          <h2>活动</h2>
          <p>公开模型更新与 Go 记录的执行事实</p>
        </div>
        <span className="run-activity-safety"
          title="这里只展示公开摘要与白名单事件，不展示或推断模型私有思维链">
          <ShieldCheck aria-hidden="true" size={15} />
          不包含私有思维链
        </span>
      </header>
      {streamError && <div className="inline-warning">活动流连接：{streamError}</div>}
      {activity.truncated && (
        <div className="run-activity-window">
          当前显示最近一段活动，已读取到事件 #{activity.through_sequence}
        </div>
      )}
      {activity.items.length === 0 && !provisional ? (
        <div className="run-activity-empty">
          <MessageSquareText aria-hidden="true" size={21} />
          <span>还没有公开活动</span>
        </div>
      ) : (
        <ol className="run-activity-list">
          {groupActivityItems(activity.items).map((entry) => entry.type === "message" ?
            <ActivityMessage item={entry.item} key={entry.item.id} /> :
            <HarnessDisclosure items={entry.items} key={entry.id} />)}
          {provisional && <ActivityMessage item={provisional} key={provisional.id} provisional />}
        </ol>
      )}
    </section>
  );
}

type ActivityEntry =
  | { type: "message"; item: RunActivityItemView }
  | { type: "harness"; id: string; items: RunActivityItemView[] };

export function groupActivityItems(items: RunActivityItemView[]): ActivityEntry[] {
  const entries: ActivityEntry[] = [];
  for (const item of items) {
    if (item.source !== "harness") {
      entries.push({ type: "message", item });
      continue;
    }
    const previous = entries.at(-1);
    if (previous?.type === "harness" && previous.items.at(-1)?.kind === item.kind) {
      previous.items.push(item);
      continue;
    }
    entries.push({ type: "harness", id: `harness-${item.id}`, items: [item] });
  }
  return entries;
}

function ActivityMessage({ item, provisional = false }: {
  item: RunActivityItemView;
  provisional?: boolean;
}) {
  return (
    <li className={`run-activity-item source-${item.source}${provisional ? " is-provisional" : ""}`}>
      <span className="run-activity-marker"><ActivityIcon kind={item.kind} source={item.source} /></span>
      <div className="run-activity-body">
        <div className="run-activity-meta">
          <span className={`run-activity-source source-${item.source}`}>
            {item.verifiable && <Check aria-hidden="true" size={12} />}{sourceLabels[item.source]}
          </span>
          {provisional && <span className="run-activity-live"><span aria-hidden="true" />临时</span>}
          <time dateTime={item.created_at}>{formatDate(item.created_at)}</time>
          {!provisional && <span className="run-activity-sequence">#{item.sequence}</span>}
        </div>
        <h3>{item.title}</h3>
        {item.detail && (item.source === "model" ?
          <SafeMarkdown className="run-activity-detail">{item.detail}</SafeMarkdown> :
          <div className="run-activity-detail">{item.detail}</div>)}
        {item.source === "model" && <small>{provisional
          ? "临时公开进度；验证后由持久活动替换，不会写入对话历史。"
          : "模型公开生成，可能包含判断；执行记录以 Harness 事件为准。"}</small>}
        {item.source === "operator" &&
          <small>{item.instruction_authorized ? "已授权的用户输入" : "非指令证据"}</small>}
      </div>
    </li>
  );
}

function hasDurablePublicUpdate(items: RunActivityItemView[], snapshot: PublicModelStreamSnapshot): boolean {
  const expectedToolRound = snapshot.call.tool_round + 1;
  const expectedText = snapshot.text.trim();
  return items.some((item) => item.source === "model" && (
    (item.attempt_id === snapshot.call.attempt_id &&
      item.model_attempt === snapshot.call.model_attempt &&
      item.tool_round === expectedToolRound) ||
    (Boolean(expectedText) && item.created_at >= snapshot.call.started_at &&
      item.detail?.trim() === expectedText)
  ));
}

function provisionalActivityItem(activity: RunActivityView, snapshot: PublicModelStreamSnapshot,
  status: PublicModelStreamStatus): RunActivityItemView {
  const expectedToolRound = snapshot.call.tool_round + 1;
  return {
    id: `public-commentary-${snapshot.call.attempt_id}-${snapshot.call.model_attempt}-${expectedToolRound}`,
    sequence: activity.through_sequence + 1,
    kind: "model_update",
    source: "model",
    title: "Prayu",
    detail: snapshot.text.trim(),
    status: status === "finalizing" ? "completed" : "running",
    verifiable: false,
    instruction_authorized: false,
    created_at: snapshot.updated_at,
    attempt_id: snapshot.call.attempt_id,
    model_attempt: snapshot.call.model_attempt,
    tool_round: expectedToolRound,
  };
}

function HarnessDisclosure({ items }: { items: RunActivityItemView[] }) {
  const status = disclosureStatus(items);
  const first = items[0];
  const last = items[items.length - 1];
  if (!first || !last) return null;
  const rows = disclosureRows(items);
  const defaultOpen = ["blocked", "failed", "pending", "waiting"].includes(status);
  return (
    <li className="run-activity-item source-harness harness-disclosure-item">
      <details className={`run-activity-disclosure status-${status}`} open={defaultOpen}>
        <summary>
          <ChevronRight aria-hidden="true" className="disclosure-chevron" size={15} />
          <ActivityIcon kind={first.kind} source="harness" />
          <strong>{disclosureTitle(first.kind, rows.length)}</strong>
          {status && <span className={`run-activity-status status-${status}`}>
            {statusLabels[status] ?? status}
          </span>}
          <time dateTime={last.created_at}>{formatDate(last.created_at)}</time>
        </summary>
        <ol className="run-activity-disclosure-list">
          {rows.map((row) => <li key={row.id}>
            <span className={`disclosure-state status-${row.status}`} aria-hidden="true" />
            <div>
              <strong>{row.title}</strong>
              {row.detail && <p>{row.detail}</p>}
            </div>
            <span className="run-activity-sequence">#{row.sequence}</span>
          </li>)}
        </ol>
      </details>
    </li>
  );
}

function disclosureTitle(kind: RunActivityItemView["kind"], count: number): string {
  const suffix = count > 1 ? `${count} 项` : "";
  switch (kind) {
  case "tool_call": return `运行了 ${Math.max(1, count)} 个操作`;
  case "model_call": return count > 1 ? `模型调用 ${count} 次` : "模型调用";
  case "approval": return `审批记录${suffix ? ` · ${suffix}` : ""}`;
  case "file_change": return `文件更改${suffix ? ` · ${suffix}` : ""}`;
  case "plan": return `计划记录${suffix ? ` · ${suffix}` : ""}`;
  default: return `运行详情${suffix ? ` · ${suffix}` : ""}`;
  }
}

function disclosureStatus(items: RunActivityItemView[]): string {
  for (const status of ["failed", "blocked", "denied"]) {
    if (items.some((item) => item.status === status)) return status;
  }
  if (items.some((item) => item.title === "工具批次完成" && item.status === "completed")) {
    return "completed";
  }
  for (const status of ["pending", "waiting", "running", "completed"]) {
    if (items.some((item) => item.status === status)) return status;
  }
  return items.at(-1)?.status ?? "";
}

interface DisclosureRow {
  id: string;
  title: string;
  detail: string;
  status: string;
  sequence: number;
}

function disclosureRows(items: RunActivityItemView[]): DisclosureRow[] {
  if (items[0]?.kind !== "tool_call") {
    return items.map((item) => ({
      id: item.id, title: item.title, detail: item.detail ?? "",
      status: item.status ?? "", sequence: item.sequence,
    }));
  }
  const results = items.filter((item) => item.title === "工具结果已记录");
  if (results.length > 0) {
    return results.map((item) => ({
      id: item.id, title: item.detail || "工具操作", detail: "",
      status: item.status ?? "", sequence: item.sequence,
    }));
  }
  const batch = items.find((item) => item.title === "工具调用已请求");
  const tools = batch?.detail?.split("、").map((name) => name.trim()).filter(Boolean) ?? [];
  if (batch && tools.length > 0) {
    const status = disclosureStatus(items);
    return tools.map((title, index) => ({
      id: `${batch.id}-operation-${index}`, title, detail: "", status,
      sequence: batch.sequence,
    }));
  }
  return items.filter((item) => item.title !== "工具批次完成").map((item) => ({
    id: item.id, title: item.detail || item.title, detail: "",
    status: item.status ?? "", sequence: item.sequence,
  }));
}
