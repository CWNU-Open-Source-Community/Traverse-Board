import { useQuery } from "@tanstack/react-query";
import type { CyberAgentClient } from "../api/client";
import type { V2PendingModelRoute } from "./components/model-route-control";

export function useV2ImageCapability(client: CyberAgentClient, threadID: string,
  pendingRoute: V2PendingModelRoute | null | undefined, enabled: boolean, runActive = false) {
  const route = useQuery({ queryKey: ["v2", "thread", threadID, "model-route"],
    queryFn: ({ signal }) => client.threadModelRoute(threadID, signal),
    enabled: enabled && !!threadID && client.hasModelControl, staleTime: 15_000 });
  const catalog = useQuery({ queryKey: ["v2", "models", "available-routes"],
    queryFn: ({ signal }) => client.availableModelRoutes(signal),
    enabled: enabled && !threadID && client.hasModelControl, staleTime: 30_000 });
  const model = threadID ? route.data : catalog.data?.routes.find((item) => pendingRoute
    ? item.provider_id === pendingRoute.provider && item.model === pendingRoute.model
    : item.default_for_routes.includes("code"));
  const capability = model?.vision_capability;
  const state = capability?.state ?? "unknown";
  const loading = threadID ? route.isFetching : catalog.isFetching;
  const failed = threadID ? route.isError : catalog.isError;
  const pendingSwitch = !!threadID && runActive && route.data?.active_run_unchanged === true;
  const hint = loading ? "正在检查当前模型的图片能力…" : failed ? "暂时无法读取模型的图片能力；图片和草稿会保留。"
    : pendingSwitch ? "所选模型尚未应用到当前执行。请等本轮结束后再发送图片；图片与草稿会保留。"
    : state === "unsupported" ? "当前模型不支持图片。请切换模型，或移除图片后发送。"
      : state !== "supported" ? "当前模型尚未声明图片能力。请在模型设置中确认，或切换到支持图片的模型。"
        : capability?.source === "operator_declared" ? "将发送原始图片；图片能力来自你的模型配置，尚未做视觉理解验证。"
          : "当前模型支持图片，将随消息发送原始图像。";
  return { allowed: state === "supported" && !failed && !loading && !pendingSwitch, hint };
}
