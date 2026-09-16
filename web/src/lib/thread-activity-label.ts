import type { ThreadExecutionView } from "../api/types";

/** Current Thread activity only; a Run's open lifecycle is not execution evidence. */
export function threadActivityLabel({ threadID, execution, readable, error = false }: {
  threadID: string;
  execution?: ThreadExecutionView;
  readable: boolean;
  error?: boolean;
}): string {
  if (!readable) return "活动状态未提供";
  if (error) return "状态读取失败";
  if (execution && execution.thread_id !== threadID) return "活动状态未知";
  if (!execution) return "正在同步状态";
  switch (execution.state) {
    case "running": return "正在工作";
    case "stopping": return "正在停止";
    case "stop_failed": return "停止未完成";
    case "idle": return "等待新消息";
    default: return "活动状态未知";
  }
}
