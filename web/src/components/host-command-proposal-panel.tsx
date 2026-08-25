import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, LoaderCircle, ShieldAlert, TerminalSquare } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type {
  HostCommandProposalReviewRequestView,
  HostCommandProposalView,
} from "../api/types";
import { formatDate, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";

type ReviewDecision = "approve" | "deny";
type ReviewAuthorization = "once" | "run_scope";

export function HostCommandProposalPanel({ client, runID }: {
  client: CyberAgentClient;
  runID: string;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const operationKeys = useRef(new Map<string, string>());
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const [verified, setVerified] = useState<Record<string, boolean>>({});
  const [evidence, setEvidence] = useState<Record<string, string>>({});
  const [grantTTLs, setGrantTTLs] = useState<Record<string, number>>({});
  const [grantUses, setGrantUses] = useState<Record<string, number>>({});
  const query = useQuery({
    queryKey: ["run", runID, "host-command-proposals"],
    queryFn: ({ signal }) => client.hostCommandProposals(runID, signal),
    enabled: client.hasHostCommandProposalControl && runID !== "",
  });
  const mutation = useMutation({
    mutationFn: ({ proposal, decision, authorization, reason, grantTTL, maxUses, intent }: {
      proposal: HostCommandProposalView;
      decision: ReviewDecision;
      authorization?: ReviewAuthorization;
      reason: string;
      grantTTL?: number;
      maxUses?: number;
      intent: string;
    }) => {
      let operationKey = operationKeys.current.get(intent);
      if (!operationKey) {
        operationKey = `web-host-command-proposal-${globalThis.crypto.randomUUID()}`;
        operationKeys.current.set(intent, operationKey);
      }
      const body: HostCommandProposalReviewRequestView = {
        version: "host_command_review.v1",
        decision,
        reason: reason || (decision === "approve"
          ? (proposal.protocol_version === "risk_escalation.v1"
            ? t("操作者核对并批准了精确的高风险升级范围",
              "Operator verified and approved the exact risk escalation scope")
            : t("操作者核对并批准了这条非沙箱宿主机命令",
              "Operator verified and approved the non-sandboxed host command"))
          : t("操作者拒绝了这条非沙箱宿主机命令",
            "Operator denied the non-sandboxed host command")),
        confirm_execution: decision === "approve",
      };
      if (authorization) body.authorization = authorization;
      if (authorization === "run_scope") {
        body.grant_ttl_seconds = grantTTL;
        body.grant_max_uses = maxUses;
      }
      return client.reviewHostCommandProposal(runID, proposal.id, body, operationKey);
    },
    onSuccess: (result, variables) => {
      operationKeys.current.delete(variables.intent);
      setVerified((current) => ({ ...current, [variables.proposal.id]: false }));
      if (result.untrusted_evidence) {
        setEvidence((current) => ({ ...current,
          [variables.proposal.id]: result.untrusted_evidence! }));
      }
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "host-command-proposals"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "events"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID] });
    },
  });

  if (!client.hasHostCommandProposalControl) {
    return null;
  }
  if (query.isLoading) {
    return <LoadingState label={t("正在加载宿主机命令提案", "Loading host command proposals")} />;
  }
  if (query.isError || !query.data) {
    return <ErrorState error={query.error} />;
  }

  const decide = (proposal: HostCommandProposalView, decision: ReviewDecision,
    authorization?: ReviewAuthorization) => {
    const risk = proposal.protocol_version === "risk_escalation.v1";
    const grantTTL = grantTTLs[proposal.id] ?? 300;
    const maxUses = grantUses[proposal.id] ?? 4;
    const approvalMessage = authorization === "run_scope"
      ? t(`确认在当前 Run 内授予这项精确风险范围 ${grantTTL} 秒、最多 ${maxUses} 次？本次调用会立即执行。`,
        `Grant this exact risk scope to the current Run for ${grantTTL} seconds and at most ${maxUses} uses? The current call executes immediately.`)
      : risk
        ? t("确认仅执行一次这项精确高风险调用？它不会授权其他调用。",
          "Execute this exact high-risk call once? It does not authorize any other call.")
        : t("确认仅执行一次这条非沙箱宿主机命令？该进程可访问主机网络。",
          "Execute this non-sandboxed host command once? The process can access the host network.");
    if (decision === "approve" && (!verified[proposal.id] ||
      !globalThis.confirm(approvalMessage))) {
      return;
    }
    const reason = (reasons[proposal.id] ?? "").trim();
    mutation.mutate({ proposal, decision, authorization, reason,
      grantTTL: authorization === "run_scope" ? grantTTL : undefined,
      maxUses: authorization === "run_scope" ? maxUses : undefined,
      intent: `${proposal.id}:${proposal.spec_fingerprint}:${decision}:${authorization ?? "legacy"}:${grantTTL}:${maxUses}:${reason}` });
  };

  return (
    <section className="approval-queue command-proposal-queue host-command-proposal-queue"
      aria-label={t("宿主机命令提案", "Host command proposals")}>
      <header className="approval-queue-header">
        <div><TerminalSquare aria-hidden="true" size={16} />
          <strong>{t("宿主机命令提案", "Host command proposals")}</strong></div>
        <span>{query.data.items.length}</span>
      </header>
      <div className="host-command-risk" role="alert">
        <ShieldAlert aria-hidden="true" size={17} />
        <strong>{t("高风险：非沙箱宿主机调用，可能涉及网络、凭据类别、宿主路径或策略拒绝",
          "High risk: non-sandboxed host calls may involve network, credential kinds, host paths, or policy refusal")}</strong>
      </div>
      <div className="approval-boundary-line">
        <span>{t("审批档或 Standard Code Workspace Access Run",
          "Approval-mode or Standard Code Workspace Access Runs")}</span>
        <span>{t("仅一次，或当前 Run 内精确限时/限次授权",
          "Exact once, or time/use-bounded scope in the current Run")}</span>
        <span>{t("持久终端与自动重试：关闭", "Persistent terminal and automatic retry: off")}</span>
      </div>
      {query.data.items.length === 0 ? <EmptyState>{t("没有宿主机命令提案", "No host command proposals")}</EmptyState> : (
        <div className="approval-list">
          {query.data.items.map((proposal) => {
            const risk = proposal.protocol_version === "risk_escalation.v1";
            const pending = risk
              ? proposal.state === "waiting_approval" && proposal.approval_status === "pending"
              : !proposal.review;
            const busy = mutation.isPending && mutation.variables?.proposal.id === proposal.id;
            const status = proposal.state ?? proposal.result?.status ?? proposal.review?.decision ?? "pending";
            const grantTTL = grantTTLs[proposal.id] ?? 300;
            const maxUses = grantUses[proposal.id] ?? 4;
            const validGrant = Number.isInteger(grantTTL) && grantTTL >= 1 && grantTTL <= 900 &&
              Number.isInteger(maxUses) && maxUses >= 1 && maxUses <= 8;
            return (
              <article className="approval-row command-proposal-row" key={proposal.id}>
                <div className="approval-row-main">
                  <span><strong>{risk ? t("高风险升级", "Risk escalation")
                    : t("宿主机命令", "Host command")}</strong>
                    <small>{proposal.permission_mode} / {t("修订", "revision")} {proposal.permission_revision}</small></span>
                  <code>{shortID(proposal.id)}</code>
                  <StatusBadge status={status} />
                  <time dateTime={proposal.created_at}>{formatDate(proposal.created_at)}</time>
                </div>
                <p className="command-proposal-purpose">{proposal.purpose}</p>
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
                  <div className="host-command-risk">
                    <ShieldAlert aria-hidden="true" size={15} />
                    <strong>{t("模型仅提出以下精确范围；操作者决定授权方式、时限与次数",
                      "The model proposed only this exact scope; the operator chooses authorization, TTL, and use count")}</strong>
                  </div>
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
                  {proposal.invalidation_reason && <div className="inline-warning" role="alert">
                    {t("提案或授权已失效：", "Proposal or grant invalidated: ")}
                    <code>{proposal.invalidation_reason}</code>
                  </div>}
                  {proposal.uncertain && <div className="inline-warning" role="alert">
                    {t("执行结果不确定：已永久禁止自动重试。",
                      "Execution outcome is uncertain; automatic retry is permanently disabled.")}
                  </div>}
                </>}
                {pending && <>
                  <label className="host-command-verification">
                    <input checked={verified[proposal.id] ?? false} disabled={busy}
                      onChange={(event) => setVerified((current) => ({ ...current,
                        [proposal.id]: event.target.checked }))} type="checkbox" />
                    <span>{risk
                      ? t("我已核对命令、所有风险类别与目标、快照绑定及资源上限",
                        "I verified the command, every risk kind and target, snapshot bindings, and resource limits")
                      : t("我已核对可执行文件 SHA、参数、目录和主机网络访问",
                        "I verified the executable SHA, arguments, directory, and host network access")}</span>
                  </label>
                  <div className="approval-actions">
                    <input aria-label={t("宿主机命令审阅原因", "Host command review reason")}
                      disabled={busy} maxLength={4096}
                      onChange={(event) => setReasons((current) => ({ ...current,
                        [proposal.id]: event.target.value }))}
                      placeholder={t("可选审阅原因", "Optional review reason")}
                      value={reasons[proposal.id] ?? ""} />
                    <button className="command-button danger" disabled={busy}
                      onClick={() => decide(proposal, "deny")} type="button">
                      {busy && mutation.variables?.decision === "deny"
                        ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                        : <Ban aria-hidden="true" size={15} />}{t("拒绝", "Deny")}
                    </button>
                    <button className="command-button" disabled={busy || !verified[proposal.id]}
                      onClick={() => decide(proposal, "approve", risk ? "once" : undefined)} type="button">
                      {busy && mutation.variables?.decision === "approve" &&
                        mutation.variables.authorization !== "run_scope"
                        ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                        : <Check aria-hidden="true" size={15} />}{risk
                        ? t("仅批准本次精确调用", "Approve exact call once")
                        : t("批准并执行一次", "Approve and execute once")}
                    </button>
                    {risk && <>
                      <label>{t("授权秒数", "Grant seconds")}
                        <input aria-label={t("当前 Run 授权秒数", "Current-Run grant seconds")}
                          disabled={busy} max={900} min={1}
                          onChange={(event) => setGrantTTLs((current) => ({ ...current,
                            [proposal.id]: Number(event.target.value) }))}
                          type="number" value={grantTTL} />
                      </label>
                      <label>{t("最多使用次数", "Maximum uses")}
                        <input aria-label={t("当前 Run 授权最多使用次数", "Current-Run grant maximum uses")}
                          disabled={busy} max={8} min={1}
                          onChange={(event) => setGrantUses((current) => ({ ...current,
                            [proposal.id]: Number(event.target.value) }))}
                          type="number" value={maxUses} />
                      </label>
                      <button className="command-button" disabled={busy || !verified[proposal.id] || !validGrant}
                        onClick={() => decide(proposal, "approve", "run_scope")} type="button">
                        {busy && mutation.variables?.authorization === "run_scope"
                          ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                          : <Check aria-hidden="true" size={15} />}
                        {t("授予当前 Run 精确范围", "Grant exact scope to current Run")}
                      </button>
                    </>}
                  </div>
                </>}
                {(evidence[proposal.id] || proposal.untrusted_evidence) && (
                  <div className="command-proposal-evidence">
                    <strong><ShieldAlert aria-hidden="true" size={14} />
                      {risk
                        ? t("不可信的已批准风险升级证据", "Untrusted approved risk escalation evidence")
                        : t("不可信宿主机命令证据", "Untrusted host command evidence")}</strong>
                    <pre>{evidence[proposal.id] ?? proposal.untrusted_evidence}</pre>
                  </div>
                )}
              </article>
            );
          })}
        </div>
      )}
      {mutation.isError && <div className="inline-warning" role="alert">
        {mutation.error instanceof Error ? mutation.error.message
          : t("宿主机命令审阅失败", "Host command review failed")}
      </div>}
    </section>
  );
}
