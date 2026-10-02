import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { CyberAgentClient } from "../../api/client";
import type { ThreadExecutionPermissionControlView } from "../../api/types";
import { v2QueryKeys } from "../query-keys";
import { V2ApprovalModeControl } from "./approval-mode-control";
import type { ApprovalModeSelectionRequest } from "./approval-mode-contract";
import { browserCDPQueryKey, V2BrowserCDPControl } from "./browser-cdp-control";
import { V2RunNetworkAuthorityControl } from "./run-network-authority-control";

function effectCopy(result: ThreadExecutionPermissionControlView): string {
  if (result.current_run_effect === "deferred") return "当前执行保持不变，选择用于下一次执行";
  if (result.execution_permission.approval_mode === "full" && result.execution_permission.full_activation !== "active") {
    return "完全访问偏好已保存；当前进程尚未激活";
  }
  if (result.current_run_effect === "paused_and_applied") return "已安全暂停当前执行并应用";
  if (result.current_run_effect === "applied" || result.current_run_synchronized) return "已应用到当前和后续执行";
  return "已用于此对话的后续执行";
}

type PermissionControlProps = {
  client: CyberAgentClient;
  threadID: string;
  variant?: "menu" | "settings";
  onOpenModelSettings?: () => void;
};

export function V2PermissionControl(props: PermissionControlProps) {
  // Target changes discard confirmation/pending/error UI. An already submitted
  // mutation keeps its original instance and can only update that Thread's cache.
  return <ThreadPermissionControl key={props.threadID} {...props} />;
}

function ThreadPermissionControl({ client, threadID, variant = "menu", onOpenModelSettings }: PermissionControlProps) {
  const queryClient = useQueryClient();
  const [networkOpen, setNetworkOpen] = useState(false);
  const query = useQuery({
    queryKey: v2QueryKeys.permission(threadID),
    queryFn: ({ signal }) => client.getThreadExecutionPermission(threadID, signal),
    enabled: Boolean(threadID), staleTime: 10_000,
  });
  const mutation = useMutation({
    mutationFn: (request: ApprovalModeSelectionRequest) => client.changeThreadExecutionPermission(threadID, {
      mode: request.mode, confirm_full: request.confirmFull, reason: "Thread approval preference selection",
    }, `thread-approval-preference-${globalThis.crypto.randomUUID()}`),
    onSuccess: (result) => {
      queryClient.setQueryData(v2QueryKeys.permission(threadID), result);
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(threadID) });
      if (result.current_run_id) {
        void queryClient.invalidateQueries({ queryKey: ["run", result.current_run_id] });
        void queryClient.invalidateQueries({ queryKey: browserCDPQueryKey(result.current_run_id) });
      }
    },
  });
  if (!threadID) return <p className="v2-settings-empty">先从侧栏打开一个对话。</p>;
  if (query.isPending) return <span role="status">正在读取权限…</span>;
  if (query.isError || !query.data) return <p role="alert">无法读取权限设置</p>;
  const permission = query.data.execution_permission;
  return <div className={`v2-permission-host is-${variant}`}>
    <V2ApprovalModeControl mode={permission.approval_mode} fullActivation={permission.full_activation}
      fullUnavailableReason={permission.full_unavailable_reason} pending={mutation.isPending}
      disabled={!client.hasExecutionPermissionControl} variant={variant}
      error={mutation.isError ? mutation.error instanceof Error ? mutation.error.message : "权限更新失败" : undefined}
      onRequestChange={(request) => { mutation.reset(); mutation.mutate(request); }} />
    {variant === "settings" && <>
      <p role="status">{effectCopy(query.data)}</p>
      <V2BrowserCDPControl client={client} permissionMode={permission.mode}
        executionRuntimeAvailable={permission.full_activation === "active"}
        runID={query.data.current_run_id ?? ""} />
    </>}
    <details className="v2-permission-network" open={networkOpen}
      onToggle={(event) => setNetworkOpen(event.currentTarget.open)}>
      <summary>网页访问与搜索</summary>
      {networkOpen && <V2RunNetworkAuthorityControl key={`${threadID}:${query.data.current_run_id ?? ""}`}
        client={client} runID={query.data.current_run_id ?? ""} threadID={threadID}
        onOpenModelSettings={onOpenModelSettings} />}
    </details>
  </div>;
}
