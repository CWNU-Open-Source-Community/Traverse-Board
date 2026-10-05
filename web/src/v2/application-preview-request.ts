import type { QueryClient } from "@tanstack/react-query";

export const APPLICATION_PREVIEW_START_REQUEST = "请检查当前项目的启动方式，使用受管理的后台命令启动开发服务，等待就绪后给出确切的本机预览地址；保留启动输出和可停止的任务标识。如果启动失败，请报告实际错误。";

export interface ApplicationPreviewRequest {
  request: string;
  phase: "draft" | "submitting" | "accepted" | "failed" | "unconfirmed" | "rejected";
  operationKey?: string;
  runID?: string;
  messageID?: string;
}

export const applicationPreviewRequestKey = (threadID: string) =>
  ["v2", "thread", threadID, "application-preview-request"] as const;
export const applicationServicesQueryKey = (threadID: string) =>
  ["v2", "thread", threadID, "application-services"] as const;

// This is a presentation hint for one operator request. Jobs and their source
// messages come from Go; matching this text never establishes process ownership.
export function trackApplicationPreviewSubmission(queries: QueryClient,
  input: { threadID: string; content: string; operationKey: string },
  phase: ApplicationPreviewRequest["phase"],
  reference?: { runID?: string; messageID?: string },
  bindToDraft = false,
) {
  const key = applicationPreviewRequestKey(input.threadID);
  const current = queries.getQueryData<ApplicationPreviewRequest | null>(key);
  if (!current || (current.operationKey !== input.operationKey &&
    (!bindToDraft || phase !== "submitting" || !["draft", "rejected"].includes(current.phase) || !input.content.includes(current.request)))) return;
  queries.setQueryData<ApplicationPreviewRequest>(key, {
    ...current, phase, operationKey: input.operationKey, ...reference,
  });
}

export function applicationPreviewRequestCopy(request: ApplicationPreviewRequest, hasAssociatedCommand = false): string {
  switch (request.phase) {
    case "draft": return "启动要求已放回草稿，请确认后发送。";
    case "submitting": return "正在提交启动要求，尚未确认服务是否启动。";
    case "accepted": return hasAssociatedCommand ? "启动要求已受理，已找到关联的任务命令，可查看状态与输出。"
      : "启动要求已受理，正在等待受管理的后台服务记录。";
    case "failed": return "启动要求已受理，但本轮执行失败。请查看失败记录和启动输出。";
    case "unconfirmed": return "启动要求的提交结果尚未确认，请核对原提交。";
    case "rejected": return "启动要求未入队，草稿已保留。";
  }
}
