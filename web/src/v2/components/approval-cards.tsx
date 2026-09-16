import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, Globe2, LoaderCircle, ShieldAlert } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type { ApprovalPreviewView, ApprovalQueueItemView } from "../../api/types";
import { v2QueryKeys } from "../query-keys";

type Action = "approve_once" | "approve_for_thread" | "deny";
const fieldLabels: Record<string, string> = { command: "命令", executable: "可执行文件",
  arguments: "参数（按顺序）", requested_backend: "提案环境", operation: "操作",
  parameters: "精确参数", summary: "影响", path: "文件", destination_path: "目标文件",
  url: "网址", host: "主机" };
const effectText: Record<ApprovalPreviewView["effect"], string> = {
  dry_run: "批准后只记录这一次模拟执行，不启动真实进程。拒绝会终止这份提案。",
  record_git_approval: "仅授权这份 Git 提案一次；执行前仍会核对仓库、权限和预览是否变化。此按钮不执行 Git 操作，拒绝后该提案不能执行。",
  file_review_required: "这里可以拒绝这份编辑。批准、差异审阅和写入请使用任务的「审阅改动」入口。",
  fetch_public_https: "允许一次仅覆盖本次读取；本对话允许仅覆盖此精确主机在当前对话内的公开 HTTPS 读取。拒绝会将本次读取的拒绝结果返回给 Agent。",
  unavailable: "此类操作暂不支持在这里批准。",
};

export function V2ApprovalCards({ client, runID, threadID }: {
  client: CyberAgentClient; runID: string; threadID: string;
}) {
  const queryClient = useQueryClient();
  const [notice, setNotice] = useState("");
  const query = useQuery({
    queryKey: v2QueryKeys.approvals(runID),
    queryFn: ({ signal }) => client.approvalQueue(runID, signal),
    enabled: Boolean(runID) && client.hasApprovalControl,
    refetchInterval: 2_000,
  });
  const decided = (message: string) => {
    setNotice(message);
    void queryClient.invalidateQueries({ queryKey: v2QueryKeys.approvals(runID) });
    void queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(threadID) });
    void queryClient.invalidateQueries({ queryKey: v2QueryKeys.transcript(threadID) });
  };
  return <>
    {notice && <p className="v2-notice" role="status">{notice}</p>}
    {query.isError && <div className="v2-notice tone-warning" role="alert">
      无法读取待审批操作，请重试后再作决定。
      <button onClick={() => void query.refetch()} type="button">重试审批队列</button>
    </div>}
    {Boolean(query.data?.items.length) && <section aria-label="需要你的批准" className="v2-approval-stack">
      {query.data?.items.map((item) => <ApprovalCard client={client} item={item} key={item.id}
        onDecided={decided} runID={runID} />)}
      {query.data?.truncated && <p role="status">待审批操作较多；处理后会继续显示其余操作。</p>}
    </section>}
  </>;
}

function ApprovalCard({ client, item, runID, onDecided }: {
  client: CyberAgentClient; item: ApprovalQueueItemView; runID: string;
  onDecided: (message: string) => void;
}) {
  const [reason, setReason] = useState("");
  const operationKeys = useRef(new Map<string, string>());
  const preview = useQuery({
    queryKey: ["v2", "approval-preview", runID, item.id, item.version],
    queryFn: async ({ signal }) => {
      const value = await client.approvalPreview(runID, item.id, signal);
      if (value.proposal_id !== item.proposal_id || value.tool_name !== item.tool_name ||
        value.workspace_id !== item.workspace_id) throw new Error("审批预览与当前操作不一致，请刷新。");
      return value;
    },
    retry: false,
  });
  const webFetch = item.tool_name === "web_fetch";
  const recovering = item.status === "approved" || item.status === "denied";
  const mutation = useMutation({
    mutationFn: (action: Action) => {
      const denialReason = action === "deny" ? reason.trim() : "";
      const intent = `${item.id}:${action}:${denialReason}`;
      let key = operationKeys.current.get(intent);
      if (!key) {
        key = `v2-approval-${globalThis.crypto.randomUUID()}`;
        operationKeys.current.set(intent, key);
      }
      return client.decideApproval(runID, item.id, { version: "approval_control.v1", action,
        ...(denialReason ? { reason: denialReason } : {}) }, key);
    },
    onSuccess: (result, action) => {
      const next = webFetch ? result.retry_scheduled
        ? "后台已安排继续处理，可在工作记录中查看结果。"
        : "读取尚未恢复；可以发送消息让 Agent 继续。"
        : preview.data?.effect === "dry_run" && action !== "deny"
          ? "模拟执行已记录，没有启动真实进程。"
          : action !== "deny" ? "批准已记录，尚未执行该操作。" : "该提案不会获准执行。";
      onDecided(`${action === "deny" ? "已拒绝。" : "已批准。"}${next}`);
    },
    onError: () => { void preview.refetch(); },
  });
  const canApprove = preview.isSuccess && !preview.isFetching && preview.data.source_current &&
    !preview.data.truncated && !mutation.isPending;
  const dryRun = preview.data?.effect === "dry_run";
  return <article className="v2-approval-card">
    <header><span>{webFetch ? <Globe2 aria-hidden="true" size={17} />
      : <ShieldAlert aria-hidden="true" size={17} />}</span>
      <div><strong>{recovering ? "恢复上次网页读取" : webFetch ? "允许读取这个网站？"
        : dryRun ? "批准这次模拟执行？" : "需要你的批准"}</strong>
        <small>{recovering ? item.status === "approved" ? "已允许，等待恢复" : "已拒绝，等待恢复"
          : webFetch ? item.exact_target : item.tool_name}</small></div></header>
    {preview.isLoading && <p role="status">正在读取这次操作的精确预览…</p>}
    {preview.isError && <div role="alert"><p>无法核对操作内容，暂不能批准。</p>
      <button onClick={() => void preview.refetch()} type="button">重试操作预览</button></div>}
    {preview.data && <>
      <dl className="v2-approval-facts">
        {preview.data.working_directory && <div><dt>工作目录</dt><dd>提案绑定目录内 <code>{preview.data.working_directory}</code></dd></div>}
        {preview.data.fields.map((field, index) => <div key={`${field.name}:${index}`}>
          <dt>{fieldLabels[field.name] ?? field.name}</dt><dd><pre>{field.value}</pre></dd>
        </div>)}
      </dl>
      {preview.data.workspace_id && <details><summary>查看操作目录身份</summary><code>{preview.data.workspace_id}</code></details>}
      <p>{effectText[preview.data.effect]}</p>
      {preview.data.redacted && <p>敏感内容已脱敏；这里不会显示凭据值。</p>}
      {(!preview.data.source_current || preview.data.truncated) && <p role="alert">
        {preview.data.truncated ? "预览超过显示上限，不能据此批准。" : "操作已变化或不再等待批准。"}
        <button onClick={() => void preview.refetch()} type="button">刷新操作预览</button></p>}
    </>}
    {recovering && <p>上次决定已经保存。继续只会恢复同一决定，不会更改授权范围。</p>}
    {!recovering && item.allowed_actions.includes("deny") && <input
      aria-label={`${item.tool_name} 的拒绝原因`} disabled={mutation.isPending} maxLength={2048}
      onChange={(event) => setReason(event.target.value)} placeholder="拒绝原因（可选）" value={reason} />}
    <footer>
      {item.allowed_actions.includes("deny") && <button className="secondary" disabled={mutation.isPending}
        onClick={() => mutation.mutate("deny")} type="button"><Ban aria-hidden="true" size={15} />
        {recovering ? "继续恢复" : "拒绝"}</button>}
      {item.allowed_actions.includes("approve_once") && <button className="primary" disabled={!canApprove}
        onClick={() => mutation.mutate("approve_once")} type="button">
        {mutation.isPending ? <LoaderCircle className="spin" size={15} /> : <Check aria-hidden="true" size={15} />}
        {recovering ? "继续恢复" : dryRun ? "批准模拟一次" : webFetch ? "允许一次" : "仅批准一次"}</button>}
      {webFetch && item.allowed_actions.includes("approve_for_thread") && <button className="primary"
        disabled={!canApprove} onClick={() => mutation.mutate("approve_for_thread")} type="button">
        <Check aria-hidden="true" size={15} />{recovering ? "继续恢复" : "本对话允许"}</button>}
    </footer>
    {mutation.isError && <p className="v2-inline-error" role="alert">决定未确认，可以重试。
      {mutation.error instanceof Error ? mutation.error.message : "审批失败"}</p>}
  </article>;
}
