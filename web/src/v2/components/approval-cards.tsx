import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, Globe2, LoaderCircle, ShieldAlert } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type { ApprovalQueueItemView } from "../../api/types";
import { v2QueryKeys } from "../query-keys";

export function V2ApprovalCards({ client, runID, threadID }: {
  client: CyberAgentClient;
  runID: string;
  threadID: string;
}) {
  const queryClient = useQueryClient();
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const operationKeys = useRef(new Map<string, string>());
  const query = useQuery({
    queryKey: v2QueryKeys.approvals(runID),
    queryFn: ({ signal }) => client.approvalQueue(runID, signal),
    enabled: Boolean(runID) && client.hasApprovalControl,
    refetchInterval: 2_000,
  });
  const mutation = useMutation({
    mutationFn: ({ item, action }: { item: ApprovalQueueItemView;
      action: "approve_once" | "approve_for_thread" | "deny" }) => {
      const intent = `${item.id}:${action}:${reasons[item.id] ?? ""}`;
      let operationKey = operationKeys.current.get(intent);
      if (!operationKey) {
        operationKey = `v2-approval-${globalThis.crypto.randomUUID()}`;
        operationKeys.current.set(intent, operationKey);
      }
      return client.decideApproval(runID, item.id, {
        version: "approval_control.v1",
        action,
        ...(action === "deny" && reasons[item.id]?.trim()
          ? { reason: reasons[item.id].trim() } : {}),
      }, operationKey);
    },
    onSuccess: () => {
      operationKeys.current.clear();
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.approvals(runID) });
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(threadID) });
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.transcript(threadID) });
    },
  });
  if (!query.data?.items.length) return null;
  return <section aria-label="需要你的批准" className="v2-approval-stack">
    {query.data.items.map((item) => {
      const busy = mutation.isPending && mutation.variables?.item.id === item.id;
      const webFetch = item.tool_name === "web_fetch" && Boolean(item.canonical_url) &&
        Boolean(item.exact_target);
      const status = String(item.status);
      const recovering = status === "approved" || status === "denied";
      const canApprove = client.hasApprovalControl && item.allowed_actions.includes("approve_once");
      const canApproveForThread = client.hasApprovalControl && webFetch &&
        item.allowed_actions.includes("approve_for_thread");
      const canDeny = client.hasApprovalControl && item.allowed_actions.includes("deny");
      return <article className="v2-approval-card" key={item.id}>
        <header><span>{webFetch ? <Globe2 aria-hidden="true" size={17} />
          : <ShieldAlert aria-hidden="true" size={17} />}</span>
          <div><strong>{recovering ? "恢复上次网页读取" : webFetch
            ? "允许读取这个网站？" : "需要你的批准"}</strong>
            <small>{recovering ? status === "approved" ? "已允许，等待恢复" : "已拒绝，等待恢复"
              : webFetch ? item.exact_target : item.tool_name}</small></div></header>
        {recovering ? <p>上次决定已经持久化，但执行在恢复前中断。继续只会重放同一决定，
          不会扩大权限，也不会改写你的选择。</p>
          : webFetch ? <p>Agent 想读取 <code>{item.canonical_url}</code>。网页内容始终按不可信证据处理；
          批准不会开放私网、非 HTTPS、命令网络或 Provider 凭据。</p>
          : <p>Agent 想要执行一项超出当前安全模板的操作。批准只对这一次请求有效。</p>}
        {canDeny && !recovering && <input aria-label={`${item.tool_name} 的拒绝原因`}
          disabled={busy} maxLength={2048} onChange={(event) => setReasons((current) => ({
            ...current, [item.id]: event.target.value,
          }))} placeholder="拒绝原因（可选）" value={reasons[item.id] ?? ""} />}
        <footer>{canDeny && <button className="secondary" disabled={busy}
          onClick={() => mutation.mutate({ item, action: "deny" })} type="button">
          {busy && mutation.variables?.action === "deny" ? <LoaderCircle className="spin" size={15} />
            : <Ban aria-hidden="true" size={15} />}{recovering ? "继续恢复" : "拒绝"}</button>}
          {canApprove && <button className="primary" disabled={busy}
            onClick={() => mutation.mutate({ item, action: "approve_once" })} type="button">
            {busy && mutation.variables?.action === "approve_once" ? <LoaderCircle className="spin" size={15} />
              : <Check aria-hidden="true" size={15} />}{recovering ? "继续恢复"
                : webFetch ? "允许一次" : "仅批准一次"}</button>}
          {canApproveForThread && <button className="primary" disabled={busy}
            onClick={() => mutation.mutate({ item, action: "approve_for_thread" })} type="button">
            {busy && mutation.variables?.action === "approve_for_thread"
              ? <LoaderCircle className="spin" size={15} />
              : <Check aria-hidden="true" size={15} />}{recovering ? "继续恢复"
                : "本对话允许"}</button>}</footer>
        {mutation.isError && mutation.variables?.item.id === item.id &&
          <p className="v2-inline-error" role="alert">{mutation.error instanceof Error
            ? mutation.error.message : "审批失败"}</p>}
      </article>;
    })}
  </section>;
}
