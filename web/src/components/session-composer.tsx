import { useRef, useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CircleX, LoaderCircle, SendHorizontal } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { RunView, SessionMessageControlRequestView,
  SessionMessageControlView, OperatorSteeringQueueView,
  RunExecutionControlView } from "../api/types";
import { StatusBadge } from "./common";
import { AgentComposerControls } from "./agent-composer-controls";
import { WorkspaceAttachmentDialog } from "./workspace-attachment-dialog";
import { useLocale } from "../lib/locale";

const maximumContentBytes = 16 * 1024;

interface RetryIntent {
  fingerprint: string;
  lifecycleKey: string;
  messageKey: string;
  executionKey: string;
}

interface SessionTurnResult {
  submission: SessionMessageControlView;
  execution: RunExecutionControlView | null;
}

export function SessionSteeringQueue({ client, sessionID, state }: {
  client: CyberAgentClient;
  sessionID: string;
  state: OperatorSteeringQueueView | null;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const retryKeys = useRef(new Map<string, string>());
  const mutation = useMutation({
    mutationFn: ({ messageID, key }: { messageID: string; key: string }) =>
      client.cancelSessionSteering(sessionID, messageID, {
        version: "session_steering_cancellation.v1",
        reason: "operator cancelled queued Session message",
      }, key),
    onSuccess: (result) => {
      retryKeys.current.delete(result.steering.id);
      void queryClient.invalidateQueries({ queryKey: ["run", result.run_id] });
      void queryClient.invalidateQueries({ queryKey: ["session", sessionID] });
    },
  });

  if (!client.hasSessionSteeringControl || !state) {
    return null;
  }
  const pending = state.messages.filter((message) =>
    message.status === "pending" && !message.prepared);
  if (pending.length === 0) {
    return null;
  }
  const cancel = (messageID: string) => {
    let key = retryKeys.current.get(messageID);
    if (!key) {
      key = `web-session-steering-cancel-${globalThis.crypto.randomUUID()}`;
      retryKeys.current.set(messageID, key);
    }
    mutation.mutate({ messageID, key });
  };
  return (
    <section aria-label={t("排队的 Session 消息", "Queued Session messages")} className="session-steering-queue">
      <div className="session-steering-heading">
        <span>{t("等待下一个安全边界", "Queued for next safe boundary")}</span><strong>{pending.length}</strong>
      </div>
      {pending.map((message) => (
        <div className="session-steering-item" key={message.id}>
          <span>#{message.sequence}</span><StatusBadge status={message.status} />
          <button aria-label={t(`取消排队消息 ${message.sequence}`, `Cancel queued message ${message.sequence}`)}
            className="icon-button compact" disabled={mutation.isPending}
            onClick={() => cancel(message.id)} title={t("取消排队消息", "Cancel queued message")} type="button">
            {mutation.isPending && mutation.variables?.messageID === message.id ?
              <LoaderCircle aria-hidden="true" className="spin" size={15} /> :
              <CircleX aria-hidden="true" size={15} />}
          </button>
        </div>
      ))}
      {mutation.isError && <div className="connection-error" role="alert">
        {errorMessage(mutation.error)}
      </div>}
    </section>
  );
}

export function SessionComposer({ client, sessionID, run, workspaceID = "", contextTokens = 0,
  contextPartial = false, onOpenPlugins }: {
  client: CyberAgentClient;
  sessionID: string;
  run: RunView | null;
  workspaceID?: string;
  contextTokens?: number;
  contextPartial?: boolean;
  onOpenPlugins?: () => void;
}) {
  const { t } = useLocale();
  const [content, setContent] = useState("");
  const [attachmentsOpen, setAttachmentsOpen] = useState(false);
  const [lastResult, setLastResult] = useState<SessionMessageControlView | null>(null);
  const [lastExecution, setLastExecution] = useState<RunExecutionControlView | null>(null);
  const retryIntent = useRef<RetryIntent | null>(null);
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: async ({ request, intent }: {
      request: SessionMessageControlRequestView;
      intent: RetryIntent;
    }): Promise<SessionTurnResult> => {
      const activeRun = run;
      if (!activeRun) {
        throw new Error(t("当前没有可执行的 Run", "No active Run is available"));
      }
      const autoExecute = client.hasRunLifecycle && client.hasRunExecution;
      if (autoExecute && (activeRun.status === "created" || activeRun.status === "paused")) {
        await client.controlRunLifecycle(activeRun.id, {
          version: "run_lifecycle_control.v1",
          action: activeRun.status === "created" ? "start" : "resume",
        }, intent.lifecycleKey);
      }
      const submission = await client.submitSessionMessage(sessionID, request, intent.messageKey);
      if (!autoExecute) {
        return { submission, execution: null };
      }
      const execution = await client.executeRun(activeRun.id, {
        version: "run_execution_handoff.v1",
        max_steps: 1,
      }, intent.executionKey);
      return { submission, execution };
    },
    onSuccess: (result) => {
      retryIntent.current = null;
      setContent("");
      setLastResult(result.submission);
      setLastExecution(result.execution);
      void queryClient.invalidateQueries({ queryKey: ["run", result.submission.run_id] });
      void queryClient.invalidateQueries({ queryKey: ["runs"] });
      void queryClient.invalidateQueries({ queryKey: ["session", sessionID] });
      void queryClient.invalidateQueries({ queryKey: ["activity"] });
      void queryClient.invalidateQueries({ queryKey: ["events"] });
    },
  });

  if (!client.hasSessionMessages || !run) {
    return null;
  }

  const normalized = content.trim();
  const contentBytes = new TextEncoder().encode(normalized).byteLength;
  const contentTooLarge = contentBytes > maximumContentBytes;
  const autoExecute = client.hasRunLifecycle && client.hasRunExecution;
  const mutable = run.status === "running" || run.status === "paused" ||
    (autoExecute && run.status === "created");
  const ready = mutable && contentBytes > 0 && !contentTooLarge && !mutation.isPending;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!ready) {
      return;
    }
    const request: SessionMessageControlRequestView = {
      version: "session_message_submission.v1",
      content: normalized,
    };
    const fingerprint = JSON.stringify({ sessionID, request });
    if (retryIntent.current?.fingerprint !== fingerprint) {
      retryIntent.current = {
        fingerprint,
        lifecycleKey: `web-session-turn-lifecycle-${globalThis.crypto.randomUUID()}`,
        messageKey: `web-session-message-${globalThis.crypto.randomUUID()}`,
        executionKey: `web-session-turn-execution-${globalThis.crypto.randomUUID()}`,
      };
    }
    mutation.mutate({ request, intent: retryIntent.current });
  };

  const changeContent = (value: string) => {
    setContent(value);
    setLastResult(null);
    setLastExecution(null);
    mutation.reset();
  };

  return <>
    <form className="session-composer" onSubmit={submit}>
      <textarea aria-label={t("Session 消息", "Session message")} autoComplete="off"
        disabled={!mutable || mutation.isPending} onChange={(event) => changeContent(event.target.value)}
        placeholder={t("向这个 Run 发送消息", "Message this Run")} rows={3} spellCheck value={content} />
      <AgentComposerControls client={client} contextPartial={contextPartial}
        contextTokens={contextTokens}
        onOpenFiles={client.hasEvidenceAttachment && workspaceID
          ? () => setAttachmentsOpen(true) : undefined}
        onOpenPlugins={onOpenPlugins} route={run.config?.model_route ?? "code"}
        status={<div className="session-composer-state" aria-live="polite">
          {!mutable && <><StatusBadge status={run.status} /><span>{t("Run 当前不可用", "Run unavailable")}</span></>}
          {contentTooLarge && <span className="connection-error">{t("消息超过 16384 个 UTF-8 字节", "Message exceeds 16384 UTF-8 bytes")}</span>}
          {mutation.isError && <span className="connection-error">{errorMessage(mutation.error)}</span>}
          {lastResult && <><StatusBadge status={lastResult.steering.status} />
            <span>{lastExecution?.model_called
              ? t("模型回复已提交", "Model reply committed")
              : `${t("已排队", "Queued")} #${lastResult.steering.sequence}`}
            {(lastResult.replayed || lastExecution?.replayed) ? t(" · 已重放", " · replayed") : ""}</span></>}
          {!contentTooLarge && !mutation.isError && !lastResult &&
            <span className="byte-count">{contentBytes} / {maximumContentBytes} {t("字节", "bytes")}</span>}
        </div>}
        trailing={<button aria-label={t("排队发送消息", "Queue message")} className="session-send-button" disabled={!ready}
          title={t("排队发送消息", "Queue message")} type="submit">
          {mutation.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={17} /> :
            <SendHorizontal aria-hidden="true" size={17} />}
        </button>} />
    </form>
    <WorkspaceAttachmentDialog client={client} onClose={() => setAttachmentsOpen(false)}
      open={attachmentsOpen} runID={run.id} workspaceID={workspaceID} />
  </>;
}

function errorMessage(value: unknown): string {
  return value instanceof Error && value.message.trim() ? value.message : "Session message failed";
}
