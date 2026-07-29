import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, LoaderCircle, ShieldAlert, TerminalSquare } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type {
  ControlledCommandProposalReviewRequestView,
  ControlledCommandProposalView,
} from "../api/types";
import { formatDate, shortID } from "../lib/format";
import { EmptyState, ErrorState, LoadingState, StatusBadge } from "./common";

type ReviewDecision = "approve" | "deny";

const commandLabels: Record<string, string> = {
  "git-status": "Git status",
  "git-diff-check": "Git diff check",
  "go-version": "Go version",
  "powershell-workspace-list": "Workspace listing",
};

export function ControlledCommandProposalPanel({ client, runID }: {
  client: CyberAgentClient;
  runID: string;
}) {
  const queryClient = useQueryClient();
  const operationKeys = useRef(new Map<string, string>());
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const [evidence, setEvidence] = useState<Record<string, string>>({});
  const query = useQuery({
    queryKey: ["run", runID, "command-proposals"],
    queryFn: ({ signal }) => client.controlledCommandProposals(runID, signal),
    enabled: client.hasControlledCommandProposalControl && runID !== "",
  });
  const mutation = useMutation({
    mutationFn: ({ proposal, decision, reason, intent }: {
      proposal: ControlledCommandProposalView;
      decision: ReviewDecision;
      reason: string;
      intent: string;
    }) => {
      let operationKey = operationKeys.current.get(intent);
      if (!operationKey) {
        operationKey = `web-command-proposal-${globalThis.crypto.randomUUID()}`;
        operationKeys.current.set(intent, operationKey);
      }
      const body: ControlledCommandProposalReviewRequestView = {
        version: "controlled_command_proposal_review.v1",
        decision,
        reason: reason || (decision === "approve"
          ? "Operator approved the exact fixed Go command"
          : "Operator denied the fixed Go command"),
        confirm_execution: decision === "approve",
      };
      return client.reviewControlledCommandProposal(
        runID, proposal.id, body, operationKey,
      );
    },
    onSuccess: (result, variables) => {
      operationKeys.current.delete(variables.intent);
      if (result.untrusted_evidence) {
        setEvidence((current) => ({ ...current,
          [variables.proposal.id]: result.untrusted_evidence! }));
      }
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "command-proposals"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "events"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID] });
    },
  });

  if (!client.hasControlledCommandProposalControl) {
    return null;
  }
  if (query.isLoading) {
    return <LoadingState label="Loading fixed command proposals" />;
  }
  if (query.isError || !query.data) {
    return <ErrorState error={query.error} />;
  }

  const decide = (proposal: ControlledCommandProposalView, decision: ReviewDecision) => {
    if (decision === "approve" && !globalThis.confirm(
      `Execute the fixed Go command "${commandLabels[proposal.kind] ?? proposal.kind}" once?`,
    )) {
      return;
    }
    const reason = (reasons[proposal.id] ?? "").trim();
    mutation.mutate({ proposal, decision, reason,
      intent: `${proposal.id}:${proposal.fingerprint}:${decision}:${reason}` });
  };

  return (
    <section className="approval-queue command-proposal-queue"
      aria-label="Controlled command proposals">
      <header className="approval-queue-header">
        <div><TerminalSquare aria-hidden="true" size={16} />
          <strong>Fixed command proposals</strong></div>
        <span>{query.data.items.length}</span>
      </header>
      <div className="approval-boundary-line">
        <span>Go-owned templates only</span>
        <span>One approval, one execution</span>
        <span>Network and persistence: off</span>
      </div>
      {query.data.items.length === 0 ? <EmptyState>No fixed command proposals</EmptyState> : (
        <div className="approval-list">
          {query.data.items.map((proposal) => {
            const pending = !proposal.review;
            const busy = mutation.isPending &&
              mutation.variables?.proposal.id === proposal.id;
            const status = proposal.result?.status ?? proposal.review?.decision ?? "pending";
            return (
              <article className="approval-row command-proposal-row" key={proposal.id}>
                <div className="approval-row-main">
                  <span>
                    <strong>{commandLabels[proposal.kind] ?? proposal.kind}</strong>
                    <small>{proposal.permission_mode} / revision {proposal.permission_revision}</small>
                  </span>
                  <code>{shortID(proposal.id)}</code>
                  <StatusBadge status={status} />
                  <time dateTime={proposal.created_at}>{formatDate(proposal.created_at)}</time>
                </div>
                <p className="command-proposal-purpose">{proposal.purpose}</p>
                {proposal.relative_path && <code className="command-proposal-path">
                  {proposal.relative_path}
                </code>}
                {pending && <div className="approval-actions">
                  <input aria-label={`Review reason for ${commandLabels[proposal.kind] ?? proposal.kind}`}
                    disabled={busy} maxLength={1024}
                    onChange={(event) => setReasons((current) => ({ ...current,
                      [proposal.id]: event.target.value }))}
                    placeholder="Optional review reason" value={reasons[proposal.id] ?? ""} />
                  <button className="command-button danger" disabled={busy}
                    onClick={() => decide(proposal, "deny")} type="button">
                    {busy && mutation.variables?.decision === "deny"
                      ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                      : <Ban aria-hidden="true" size={15} />}Deny
                  </button>
                  <button className="command-button" disabled={busy}
                    onClick={() => decide(proposal, "approve")} type="button">
                    {busy && mutation.variables?.decision === "approve"
                      ? <LoaderCircle aria-hidden="true" className="spin" size={15} />
                      : <Check aria-hidden="true" size={15} />}Approve and execute
                  </button>
                </div>}
                {(evidence[proposal.id] || proposal.untrusted_evidence) && (
                  <div className="command-proposal-evidence">
                    <strong><ShieldAlert aria-hidden="true" size={14} />
                      Untrusted command evidence</strong>
                    <pre>{evidence[proposal.id] ?? proposal.untrusted_evidence}</pre>
                  </div>
                )}
              </article>
            );
          })}
        </div>
      )}
      {mutation.isError && <div className="inline-warning" role="alert">
        {mutation.error instanceof Error
          ? mutation.error.message
          : "Controlled command review failed"}
      </div>}
    </section>
  );
}
