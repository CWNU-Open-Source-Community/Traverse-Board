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
  const query = useQuery({
    queryKey: ["run", runID, "host-command-proposals"],
    queryFn: ({ signal }) => client.hostCommandProposals(runID, signal),
    enabled: client.hasHostCommandProposalControl && runID !== "",
  });
  const mutation = useMutation({
    mutationFn: ({ proposal, decision, reason, intent }: {
      proposal: HostCommandProposalView;
      decision: ReviewDecision;
      reason: string;
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
          ? t("操作者核对并批准了这条非沙箱宿主机命令",
            "Operator verified and approved the non-sandboxed host command")
          : t("操作者拒绝了这条非沙箱宿主机命令",
            "Operator denied the non-sandboxed host command")),
        confirm_execution: decision === "approve",
      };
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

  const decide = (proposal: HostCommandProposalView, decision: ReviewDecision) => {
    if (decision === "approve" && (!verified[proposal.id] || !globalThis.confirm(t(
      "确认仅执行一次这条非沙箱宿主机命令？该进程可访问主机网络。",
      "Execute this non-sandboxed host command once? The process can access the host network.",
    )))) {
      return;
    }
    const reason = (reasons[proposal.id] ?? "").trim();
    mutation.mutate({ proposal, decision, reason,
      intent: `${proposal.id}:${proposal.spec_fingerprint}:${decision}:${reason}` });
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
        <strong>{t("高风险：非沙箱宿主机命令，可访问主机网络",
          "High risk: non-sandboxed host command with host network access")}</strong>
      </div>
      <div className="approval-boundary-line">
        <span>{t("仅限审批档 Run", "Approval-mode Runs only")}</span>
        <span>{t("一次审批，仅执行一次", "One approval, one execution")}</span>
        <span>{t("持久终端与自动重试：关闭", "Persistent terminal and automatic retry: off")}</span>
      </div>
      {query.data.items.length === 0 ? <EmptyState>{t("没有宿主机命令提案", "No host command proposals")}</EmptyState> : (
        <div className="approval-list">
          {query.data.items.map((proposal) => {
            const pending = !proposal.review;
            const busy = mutation.isPending && mutation.variables?.proposal.id === proposal.id;
            const status = proposal.result?.status ?? proposal.review?.decision ?? "pending";
            return (
              <article className="approval-row command-proposal-row" key={proposal.id}>
                <div className="approval-row-main">
                  <span><strong>{t("宿主机命令", "Host command")}</strong>
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
                {pending && <>
                  <label className="host-command-verification">
                    <input checked={verified[proposal.id] ?? false} disabled={busy}
                      onChange={(event) => setVerified((current) => ({ ...current,
                        [proposal.id]: event.target.checked }))} type="checkbox" />
                    <span>{t("我已核对可执行文件 SHA、参数、目录和主机网络访问",
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
                      onClick={() => decide(proposal, "approve")} type="button">
                      {busy && mutation.variables?.decision === "approve"
                        ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                        : <Check aria-hidden="true" size={15} />}{t("批准并执行一次", "Approve and execute once")}
                    </button>
                  </div>
                </>}
                {(evidence[proposal.id] || proposal.untrusted_evidence) && (
                  <div className="command-proposal-evidence">
                    <strong><ShieldAlert aria-hidden="true" size={14} />
                      {t("不可信宿主机命令证据", "Untrusted host command evidence")}</strong>
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
