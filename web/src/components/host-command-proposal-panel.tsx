import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { APIClient } from "../api/client";
import type { ApprovalContinuationView, HostCommandProposalView } from "../api/types";
import { formatDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";
import { ApprovalContinuationNotice } from "./approval-continuation-notice";
import { SavedHostCommandOutput } from "./saved-host-command-output";

export function HostCommandProposalPanel({ client, runID, threadID = "", compact = false }: {
  client: APIClient; runID: string; threadID?: string; compact?: boolean;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const [continuations, setContinuations] = useState<Record<string, ApprovalContinuationView>>({});
  const query = useQuery({ queryKey: ["run", runID, "host-command-proposals"],
    queryFn: ({ signal }) => client.hostCommandProposals(runID, signal),
    enabled: runID !== "" });
  const mutation = useMutation({
    mutationFn: (proposal: HostCommandProposalView) => client.resumeHostCommandProposal(proposal.run_id, proposal.id),
    onSuccess: (result) => { if (result.continuation) setContinuations((current) => ({ ...current,
      [`${result.run_id}:${result.id}`]: result.continuation! })); },
    onSettled: (_result, _error, proposal) => {
      void queryClient.invalidateQueries({ queryKey: ["run", proposal.run_id] });
      if (threadID) void queryClient.invalidateQueries({ queryKey: ["v2", "thread", threadID] });
    },
  });
  if (query.isLoading) return <LoadingState />;
  if (!query.data) return <ErrorState error={query.error} />;
  const items = compact ? query.data.items.filter((proposal) => {
    const continuation = continuations[`${runID}:${proposal.id}`] ?? proposal.continuation;
    return (!proposal.review && !proposal.result) || proposal.uncertain ||
      (proposal.review?.decision === "approve" && !proposal.result) ||
      Boolean(continuation && continuation.state !== "completed");
  }) : query.data.items;
  if (threadID && !items.length && !mutation.isError) return null;
  return <section className="approval-queue command-proposal-queue host-command-proposal-queue"
    aria-label={t("历史宿主机命令", "Historical host commands")}>
    <header className="approval-queue-header"><strong>{t("历史宿主机命令", "Historical host commands")}</strong></header>
    {!items.length && <EmptyState>{t("没有历史记录", "No saved history")}</EmptyState>}
    {items.map((proposal) => {
      const risk = proposal.protocol_version === "risk_escalation.v1";
      const settled = Boolean(proposal.result || proposal.review?.decision === "deny" ||
        (risk && (proposal.uncertain || proposal.invalidation_reason || proposal.approval_status === "denied")));
      const continuation = continuations[`${runID}:${proposal.id}`] ?? proposal.continuation;
      return <article className="approval-row command-proposal-row" key={proposal.id}>
        <div className="approval-row-main"><strong>{proposal.purpose}</strong><code>{shortID(proposal.id)}</code>
          <StatusBadge status={proposal.state ?? proposal.result?.status ?? proposal.review?.decision ?? "retired"} /></div>
        <HostCommandOutcome client={client} proposal={proposal} />
        <ApprovalContinuationNotice continuation={continuation} />
        {settled && client.hasRunExecution && <button className="command-button" type="button"
          disabled={mutation.isPending} onClick={() => mutation.mutate(proposal)}>
          {t("从已保存结果继续", "Continue from saved outcome")}</button>}
        <details><summary>{t("命令与历史授权记录", "Command and historical authorization")}</summary>
                <dl className="host-command-envelope">
                  <dt>{t("可执行文件", "Executable")}</dt><dd><code>{proposal.executable_path}</code></dd>
                  <dt>SHA-256</dt><dd><code>{proposal.executable_sha256}</code></dd>
                  <dt>{t("参数", "Arguments")}</dt><dd><ol aria-label={t("参数列表", "Argument list")}>
                    {proposal.argv.map((argument, index) => <li key={`${index}:${argument}`}>
                      <code>{argument}</code>
                    </li>)}</ol></dd>
                  <dt>{t("工作目录", "Working directory")}</dt><dd><code>{proposal.working_directory}</code></dd>
                  <dt>{t("网络", "Network")}</dt><dd><code>{proposal.network_intent}</code></dd>
                  <dt>{t("超时", "Timeout")}</dt><dd>{proposal.timeout_milliseconds} ms</dd>
                  <dt>{t("环境策略", "Environment policy")}</dt><dd><code>{proposal.environment_policy}</code></dd>
                  <dt>{t("环境变量名", "Environment keys")}</dt><dd>
                    {proposal.environment_keys.length > 0
                      ? proposal.environment_keys.map((key) => <code className="host-command-env-key" key={key}>{key}</code>)
                      : t("无", "None")}
                  </dd>
                  <dt>{t("环境摘要", "Environment digest")}</dt><dd><code>{proposal.environment_sha256}</code></dd>
                  <dt>{t("执行规范指纹", "Execution spec fingerprint")}</dt><dd><code>{proposal.spec_fingerprint}</code></dd>
                </dl>
{risk && <>
                  <dl className="host-command-envelope">
                    <dt>{t("风险类别", "Risk kinds")}</dt><dd>
                      {(proposal.risk_kinds ?? []).map((kind) =>
                        <code className="host-command-env-key" key={kind}>{kind}</code>)}
                    </dd>
                    {proposal.network_targets && <>
                      <dt>{t("网络目标", "Network targets")}</dt><dd>
                        {proposal.network_targets.map((target) =>
                          <code className="host-command-env-key" key={target}>{target}</code>)}
                      </dd>
                      <dt>{t("网络用途", "Network purpose")}</dt><dd>{proposal.network_purpose}</dd>
                    </>}
                    {proposal.credential_kinds && <>
                      <dt>{t("凭据类别（不含值）", "Credential kinds (no values)")}</dt><dd>
                        {proposal.credential_kinds.map((kind) =>
                          <code className="host-command-env-key" key={kind}>{kind}</code>)}
                      </dd>
                    </>}
                    {proposal.host_paths && <>
                      <dt>{t("宿主路径", "Host paths")}</dt><dd>
                        {proposal.host_paths.map((path) =>
                          <code className="host-command-env-key" key={path}>{path}</code>)}
                      </dd>
                    </>}
                    {proposal.policy_code && <>
                      <dt>{t("策略拒绝", "Policy refusal")}</dt>
                      <dd><code>{proposal.policy_code}</code> — {proposal.policy_reason}</dd>
                    </>}
                    {proposal.requested_tool && <>
                      <dt>{t("非白名单工具", "Non-whitelisted tool")}</dt>
                      <dd><code>{proposal.requested_tool}</code></dd>
                    </>}
                    {proposal.other_risk_reason && <>
                      <dt>{t("其他高风险原因", "Other high-risk reason")}</dt>
                      <dd>{proposal.other_risk_reason}</dd>
                    </>}
                    <dt>{t("所有者", "Owner")}</dt>
                    <dd><code>{proposal.run_id}</code> / <code>{proposal.mission_id}</code> /
                      <code>{proposal.session_id}</code> / <code>{proposal.workspace_id}</code></dd>
                    <dt>{t("Supervisor 调用", "Supervisor call")}</dt>
                    <dd>{t("轮次", "turn")} {proposal.supervisor_turn} /
                      <code>{proposal.supervisor_tool_call_id}</code> /
                      <code>{proposal.tool_invocation_id}</code></dd>
                    <dt>{t("模式快照", "Mode snapshot")}</dt>
                    <dd><code>{proposal.mode_snapshot_id}</code> / {proposal.mode_revision}</dd>
                    <dt>{t("交互快照", "Interaction snapshot")}</dt>
                    <dd><code>{proposal.interaction_snapshot_id}</code> / {proposal.interaction_revision}</dd>
                    <dt>{t("执行档快照", "Execution profile snapshot")}</dt>
                    <dd><code>{proposal.execution_profile_snapshot_id}</code> /
                      {proposal.execution_profile_revision}</dd>
                    <dt>{t("权限快照", "Permission snapshot")}</dt>
                    <dd><code>{proposal.permission_snapshot_id}</code> / {proposal.permission_revision}</dd>
                    <dt>{t("Workspace 根指纹", "Workspace root fingerprint")}</dt>
                    <dd><code>{proposal.workspace_root_fingerprint}</code></dd>
                    <dt>{t("能力代际", "Capability generation")}</dt>
                    <dd><code>{proposal.capability_generation}</code></dd>
                    <dt>{t("风险范围指纹", "Risk scope fingerprint")}</dt>
                    <dd><code>{proposal.scope_fingerprint}</code></dd>
                    <dt>{t("资源上限", "Resource limits")}</dt>
                    <dd>{proposal.max_output_bytes} B / {proposal.active_process_limit}
                      {t(" 个进程", " processes")} / {proposal.process_memory_bytes} B</dd>
                    <dt>{t("审批", "Approval")}</dt>
                    <dd><code>{proposal.approval_id}</code> / {proposal.approval_status}</dd>
                    {proposal.grant_id && <>
                      <dt>{t("当前 Run 授权", "Current-Run grant")}</dt>
                      <dd><code>{proposal.grant_id}</code> / {t("代际", "generation")}
                        {proposal.grant_generation} / {proposal.grant_uses_remaining}
                        / {proposal.grant_max_uses} {t("次剩余", "uses remaining")}
                        {proposal.grant_expires_at && <> / {formatDate(proposal.grant_expires_at)}</>}
                        {proposal.grant_consumption_id && <>
                          <br /><code>{proposal.grant_consumption_id}</code></>}
                      </dd>
                    </>}
                  </dl>
</>}
        </details>
      </article>;
    })}
    {(query.isError || mutation.isError) && <div role="alert"><ErrorState error={mutation.error ?? query.error} />
      <button className="command-button" type="button" onClick={() => void query.refetch()}>
        {t("刷新命令记录", "Refresh command records")}</button></div>}
  </section>;
}

function HostCommandOutcome({ client, proposal }: {
  client: APIClient; proposal: HostCommandProposalView;
}) {
  const { t } = useLocale();
  const [opened, setOpened] = useState(false);
  const receipt = proposal.receipt;
  const output = useQuery({
    queryKey: ["run", proposal.run_id, "host-command-output", proposal.id, proposal.result?.id],
    queryFn: async ({ signal }) => {
      const saved = await client.hostCommandProposal(proposal.run_id, proposal.id, signal);
      if (saved.run_id !== proposal.run_id || saved.id !== proposal.id || saved.session_id !== proposal.session_id ||
        saved.workspace_id !== proposal.workspace_id || saved.spec_fingerprint !== proposal.spec_fingerprint ||
        saved.review?.id !== proposal.review?.id || saved.result?.id !== proposal.result?.id ||
        saved.receipt?.request_id !== receipt?.request_id ||
        saved.result?.content_sha256 !== proposal.result?.content_sha256) {
        throw new Error(t("保存输出与当前命令收据不匹配。", "Saved output does not match this command receipt."));
      }
      return saved;
    },
    enabled: opened && Boolean(receipt),
  });
  if (!receipt && !proposal.review && !proposal.uncertain) return <p>{t("历史提案已退役，没有执行结果。", "Historical proposal retired without an execution result.")}</p>;
  if (!receipt) return <p role={proposal.review?.decision === "approve" ? "status" : undefined}>
    {proposal.review?.decision === "deny" ? t("提案已拒绝，没有执行结果。", "Proposal denied; no execution result.") :
      t("审批已通过，执行结果待确认。请刷新命令记录核对状态，再决定下一步。",
        "Approved; the execution result needs confirmation. Refresh command records to check the state before proceeding.")}</p>;
  return <div className="command-proposal-evidence host-command-outcome">
    <p>{t("已记录执行结果，退出码", "Execution result recorded, exit code")} {receipt.exit_code}</p>
    {receipt.cancelled && <p>{t("命令已取消。", "The command was cancelled.")}</p>}
    {receipt.timed_out && <p>{t("命令已超时。", "The command timed out.")}</p>}
    {(receipt.stdout_truncated || receipt.stderr_truncated || receipt.output_limit_exceeded) &&
      <p>{t("输出达到保存上限，当前显示已保存的部分。", "Output reached the storage limit. The saved portion is displayed here.")}</p>}
    <button className="command-button" type="button" onClick={() => setOpened((current) => !current)}>
      {opened ? t("收起已保存输出", "Hide saved output") : t("查看已保存输出", "Read saved output")}</button>
    {opened && <>
      {output.isLoading && <LoadingState />}
      {output.isError && <><ErrorState error={output.error} /><button className="command-button" type="button"
        onClick={() => void output.refetch()}>{t("重试读取输出", "Retry reading output")}</button></>}
      {output.isSuccess && <SavedHostCommandOutput detail={output.data} />}
    </>}
  </div>;
}
