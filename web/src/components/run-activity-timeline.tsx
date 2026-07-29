import {
  Bot,
  Check,
  CircleDot,
  FilePenLine,
  ListChecks,
  MessageSquareText,
  ShieldCheck,
  UserRound,
  Wrench,
} from "lucide-react";
import type { RunActivityItemView, RunActivityView } from "../api/types";
import { formatDate } from "../lib/format";

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
  failed: "失败",
  pending: "待处理",
  running: "进行中",
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
  default:
    return <CircleDot aria-hidden="true" size={15} />;
  }
}

export function RunActivityTimeline({ activity, streamError = "" }: {
  activity: RunActivityView;
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
      {activity.items.length === 0 ? (
        <div className="run-activity-empty">
          <MessageSquareText aria-hidden="true" size={21} />
          <span>还没有公开活动</span>
        </div>
      ) : (
        <ol className="run-activity-list">
          {activity.items.map((item) => (
            <li className={`run-activity-item source-${item.source}`} key={item.id}>
              <span className="run-activity-marker">
                <ActivityIcon kind={item.kind} source={item.source} />
              </span>
              <div className="run-activity-body">
                <div className="run-activity-meta">
                  <span className={`run-activity-source source-${item.source}`}>
                    {item.verifiable && <Check aria-hidden="true" size={12} />}
                    {sourceLabels[item.source]}
                  </span>
                  {item.status && (
                    <span className={`run-activity-status status-${item.status}`}>
                      {statusLabels[item.status] ?? item.status}
                    </span>
                  )}
                  <time dateTime={item.created_at}>{formatDate(item.created_at)}</time>
                  <span className="run-activity-sequence">#{item.sequence}</span>
                </div>
                <h3>{item.title}</h3>
                {item.detail && <div className="run-activity-detail">{item.detail}</div>}
                {item.source === "model" && (
                  <small>模型公开生成，可能包含判断；执行记录以 Harness 事件为准。</small>
                )}
                {item.source === "operator" && (
                  <small>{item.instruction_authorized ? "已授权的用户输入" : "非指令证据"}</small>
                )}
              </div>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
