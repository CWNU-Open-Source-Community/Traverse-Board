import type { PublicModelStreamSnapshot, ThreadExecutionView, ThreadTranscriptItemView } from "../../api/types";
import type { PublicModelStreamStatus } from "../../hooks/use-public-model-stream";

export type AgentOrbMode = "waiting" | "searching" | "executing" | "composing";
export interface AgentActivity {
  label: string;
  mode?: AgentOrbMode;
  attention?: boolean;
}

interface ActivityInput {
  threadID: string;
  runID?: string;
  runStatus?: string;
  archived: boolean;
  execution?: ThreadExecutionView;
  executionReadable: boolean;
  executionError: boolean;
  submitting: boolean;
  reconciling: boolean;
  snapshot: PublicModelStreamSnapshot | null;
  streamStatus: PublicModelStreamStatus;
  progressError: boolean;
  transcript: ThreadTranscriptItemView[];
}

/** Presentation only. Never infer an execution from an open Run or generated tool arguments. */
export function projectAgentActivity(input: ActivityInput): AgentActivity | null {
  if (input.archived) return null;
  const execution = input.execution;
  if (input.executionError) return { label: "状态读取失败", attention: true };
  if (execution && execution.thread_id !== input.threadID) return { label: "活动状态未知" };
  if (execution?.state === "stopping") return { label: "正在停止" };
  if (execution?.state === "stop_failed") return { label: "停止未完成", attention: true };
  if (input.runStatus === "waiting_approval") return { label: "等待批准" };
  if (input.runStatus === "paused") return { label: "已暂停" };
  if (execution?.state !== "running") {
    if (input.reconciling) return { label: "正在核对提交" };
    if (input.submitting) return { label: "正在发送消息" };
    if (!input.executionReadable) return { label: "活动状态未提供" };
    if (!execution) return { label: "正在同步状态" };
    if (input.runStatus === "failed") return { label: "本轮未完成", attention: true };
    // Confirmed idle wins over a cached live snapshot, including after cancellation.
    return null;
  }
  if (input.runID && ["completed", "failed", "cancelled", "archived"].includes(input.runStatus ?? "")) {
    return input.runStatus === "failed" ? { label: "本轮未完成", attention: true } : null;
  }
  if (input.progressError) return { label: "实时进度暂不可用", attention: true };

  const snapshot = input.snapshot?.call.run_id === input.runID ? input.snapshot : null;
  if (snapshot?.call.cancel_requested) return { label: "正在停止" };
  const items = input.transcript.filter((item) => item.run_id === input.runID && item.durable && !item.provisional);
  // Durable sequence retains sub-millisecond ordering; Date.parse does not.
  const modelCalls = items.filter((item) => item.kind === "model_call");
  const latestModelSequence = Math.max(0, ...modelCalls.map((item) => item.sequence));
  const latestModelAt = Math.max(0, ...modelCalls.map((item) => Date.parse(item.created_at) || 0));
  const snapshotStartedAt = snapshot ? Date.parse(snapshot.call.started_at) : 0;
  const tools = new Map<string, ThreadTranscriptItemView>();
  for (const item of items) {
    if (item.kind !== "tool_call") continue;
    // Legacy event IDs differ at start/result and cannot establish tool pairing.
    const identity = item.durable_call_id || item.stream_call_id;
    if (!identity) continue;
    const previous = tools.get(identity);
    if (!previous || item.sequence >= previous.sequence) tools.set(identity, item);
  }
  const runningTool = [...tools.values()].filter((item) => item.stage === "running" &&
    item.sequence > latestModelSequence && (!snapshot || Date.parse(item.created_at) > snapshotStartedAt) &&
    (!snapshot || !item.attempt_id || item.attempt_id === snapshot.call.attempt_id))
    .sort((a, b) => b.sequence - a.sequence)[0];
  if (runningTool) {
    if (runningTool.activity_type === "search") return { mode: "searching", label: "正在搜索" };
    const label = runningTool.tool_name === "workspace_change" ? "正在准备修改"
      : runningTool.tool_name === "workspace_apply" ? "正在应用修改"
        : runningTool.activity_type === "read" ? "正在读取资料"
          : runningTool.activity_type === "verify" ? "正在运行检查" : "正在执行工具";
    return { mode: "executing", label };
  }
  if (snapshot && Date.parse(snapshot.updated_at) > latestModelAt &&
      input.streamStatus === "live" && !snapshot.message_complete && snapshot.text.trim() &&
      !snapshot.items.some((item) => item.type === "message" && ["completed", "failed", "cancelled"].includes(item.status))) {
    return { mode: "composing", label: snapshot.content_kind === "tool_commentary" ? "正在更新进展" : "正在回复" };
  }
  return { mode: "waiting", label: "正在工作" };
}
