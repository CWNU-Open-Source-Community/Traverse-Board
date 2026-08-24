import { useRef, useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CircleX, LoaderCircle, Play, SendHorizontal, Square } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { RunView, SessionMessageControlRequestView,
  SessionMessageControlView, OperatorSteeringQueueView,
  RunExecutionControlView } from "../api/types";
import { StatusBadge } from "./common";
import { AgentComposerControls } from "./agent-composer-controls";
import { WorkspaceAttachmentDialog } from "./workspace-attachment-dialog";
import { useLocale } from "../lib/locale";
import { submitComposerOnEnter } from "../lib/composer-keyboard";
import { usePublicModelStream, type PublicModelStreamState } from "../hooks/use-public-model-stream";

const maximumContentBytes = 16 * 1024;

interface RetryIntent {
  fingerprint: string;
  lifecycleKey: string;
  messageKey: string;
  executionKey: string;
}

interface PhaseRetryIntent {
  fingerprint: string;
  lifecycleKey: string;
  phaseKey: string;
}

interface SessionTurnResult {
  submission: SessionMessageControlView;
  execution: RunExecutionControlView | null;
}

export function SessionSteeringQueue({ client, sessionID, state, run = null }: {
  client: CyberAgentClient;
  sessionID: string;
  state: OperatorSteeringQueueView | null;
  run?: RunView | null;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const retryKeys = useRef(new Map<string, string>());
  const cancellation = useMutation({
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
  const continueIntent = useRef<{ fingerprint: string; lifecycleKey: string;
    executionKey: string } | null>(null);
  const continuation = useMutation({
    mutationFn: async (intent: NonNullable<typeof continueIntent.current>) => {
      if (!run || !client.hasRunExecution) {
        throw new Error(t("当前 Run 不支持继续执行", "Run execution is unavailable"));
      }
      if ((run.status === "created" || run.status === "paused") && client.hasRunLifecycle) {
        await client.controlRunLifecycle(run.id, {
          version: "run_lifecycle_control.v1",
          action: run.status === "created" ? "start" : "resume",
        }, intent.lifecycleKey);
      }
      return client.executeRun(run.id, {
        version: "run_execution_handoff.v1",
        max_steps: 1,
      }, intent.executionKey);
    },
    onSuccess: (result) => {
      continueIntent.current = null;
      void queryClient.invalidateQueries({ queryKey: ["run", result.run_id] });
      void queryClient.invalidateQueries({ queryKey: ["session", sessionID] });
      void queryClient.invalidateQueries({ queryKey: ["activity"] });
      void queryClient.invalidateQueries({ queryKey: ["events"] });
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
    cancellation.mutate({ messageID, key });
  };
  const continueQueued = () => {
    if (!run) return;
    const fingerprint = `${run.id}:${pending.map((message) => message.id).join(":")}`;
    if (continueIntent.current?.fingerprint !== fingerprint) {
      continueIntent.current = {
        fingerprint,
        lifecycleKey: `web-session-queue-lifecycle-${globalThis.crypto.randomUUID()}`,
        executionKey: `web-session-queue-execution-${globalThis.crypto.randomUUID()}`,
      };
    }
    continuation.mutate(continueIntent.current);
  };
  const canContinue = Boolean(run && client.hasRunExecution &&
    (run.status === "running" || ((run.status === "created" || run.status === "paused") &&
      client.hasRunLifecycle)));
  return (
    <section aria-label={t("排队的 Session 消息", "Queued Session messages")} className="session-steering-queue">
      <div className="session-steering-heading">
        <span>{t("等待下一个安全边界", "Queued for next safe boundary")}</span><strong>{pending.length}</strong>
        {canContinue && <button className="compact-command" disabled={continuation.isPending ||
          cancellation.isPending} onClick={continueQueued} type="button">
          {continuation.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={14} /> :
            <Play aria-hidden="true" size={13} />}
          {t("继续处理", "Continue")}
        </button>}
      </div>
      {pending.map((message) => (
        <div className="session-steering-item" key={message.id}>
          <span>#{message.sequence}</span><StatusBadge status={message.status} />
          <button aria-label={t(`取消排队消息 ${message.sequence}`, `Cancel queued message ${message.sequence}`)}
            className="icon-button compact" disabled={cancellation.isPending || continuation.isPending}
            onClick={() => cancel(message.id)} title={t("取消排队消息", "Cancel queued message")} type="button">
            {cancellation.isPending && cancellation.variables?.messageID === message.id ?
              <LoaderCircle aria-hidden="true" className="spin" size={15} /> :
              <CircleX aria-hidden="true" size={15} />}
          </button>
        </div>
      ))}
      {(cancellation.isError || continuation.isError) && <div className="connection-error" role="alert">
        {errorMessage(cancellation.error ?? continuation.error)}
      </div>}
    </section>
  );
}

export function SessionComposer({ client, sessionID, run, workspaceID = "", contextTokens = 0,
  contextPartial = false, phase, onOpenPlugins, publicModelStream }: {
  client: CyberAgentClient;
  sessionID: string;
  run: RunView | null;
  workspaceID?: string;
  contextTokens?: number;
  contextPartial?: boolean;
  phase?: "plan" | "deliver";
  onOpenPlugins?: () => void;
  publicModelStream?: PublicModelStreamState;
}) {
  const { t } = useLocale();
  const [content, setContent] = useState("");
  const [attachmentsOpen, setAttachmentsOpen] = useState(false);
  const [lastResult, setLastResult] = useState<SessionMessageControlView | null>(null);
  const [lastExecution, setLastExecution] = useState<RunExecutionControlView | null>(null);
  const [turnStopped, setTurnStopped] = useState(false);
  const turnAbort = useRef<AbortController | null>(null);
  const retryIntent = useRef<RetryIntent | null>(null);
  const phaseRetryIntent = useRef<PhaseRetryIntent | null>(null);
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: async ({ request, intent, controller }: {
      request: SessionMessageControlRequestView;
      intent: RetryIntent;
      controller: AbortController;
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
        }, intent.lifecycleKey, controller.signal);
      }
      const submission = await client.submitSessionMessage(sessionID, request, intent.messageKey,
        controller.signal);
      if (!autoExecute) {
        return { submission, execution: null };
      }
      const execution = await client.executeRun(activeRun.id, {
        version: "run_execution_handoff.v1",
        max_steps: 1,
      }, intent.executionKey, controller.signal);
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
      void queryClient.invalidateQueries({
        queryKey: ["run", result.submission.run_id, "activity"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["run", result.submission.run_id, "events"],
      });
    },
    onSettled: (_result, _error, variables) => {
      if (turnAbort.current === variables.controller) turnAbort.current = null;
    },
  });
  const localPublicStream = usePublicModelStream(client, run?.id ?? "",
    Boolean(!publicModelStream && run && mutation.isPending && client.hasRunExecution));
  const publicStream = publicModelStream ?? localPublicStream;
  const publicToolItems = publicStream.snapshot?.items.filter((item) =>
    item.type === "tool_call") ?? [];
  const phaseMutation = useMutation({
    mutationFn: async ({ selected, intent }: { selected: boolean; intent: PhaseRetryIntent }) => {
      if (!run || !client.hasPlanDelivery) {
        throw new Error(t("当前 Run 不支持计划模式切换", "Plan mode control is unavailable for this Run"));
      }
      if (run.status === "running") {
        await client.controlRunLifecycle(run.id, {
          version: "run_lifecycle_control.v1", action: "pause",
        }, intent.lifecycleKey);
      }
      if (selected) {
        return client.enterPlanMode(run.id, { version: "plan_delivery_control.v1" },
          intent.phaseKey);
      }
      return client.enterPlanDelivery(run.id, { version: "plan_delivery_control.v1" },
        intent.phaseKey);
    },
    onSuccess: () => {
      phaseRetryIntent.current = null;
    },
    onSettled: () => {
      if (run) {
        void queryClient.invalidateQueries({ queryKey: ["run", run.id] });
        void queryClient.invalidateQueries({ queryKey: ["run", run.id, "events"] });
      }
    },
  });
  const cancelKeys = useRef(new Map<string, string>());
  const cancelMutation = useMutation({
    mutationFn: async () => {
      const snapshot = publicStream.snapshot;
      if (!snapshot || !run) {
        throw new Error(t("当前没有可取消的模型调用", "No model call is available to cancel"));
      }
      const fingerprint = `${snapshot.call.attempt_id}:${snapshot.call.model_attempt}`;
      let key = cancelKeys.current.get(fingerprint);
      if (!key) {
        key = `web-run-cancel-call-${globalThis.crypto.randomUUID()}`;
        cancelKeys.current.set(fingerprint, key);
      }
      return client.cancelModelCall(run.id, {
        attempt_id: snapshot.call.attempt_id,
        model_attempt: snapshot.call.model_attempt,
        reason: "operator stopped provisional model response",
      }, key);
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
  const busy = mutation.isPending || phaseMutation.isPending;
  const ready = mutable && contentBytes > 0 && !contentTooLarge && !busy;

  const changePlanMode = (selected: boolean) => {
    if (!run || !client.hasPlanDelivery || phaseMutation.isPending ||
      !["created", "running", "paused"].includes(run.status)) {
      return;
    }
    const fingerprint = `${run.id}:${phase ?? "unknown"}:${selected ? "plan" : "deliver"}`;
    if (phaseRetryIntent.current?.fingerprint !== fingerprint) {
      phaseRetryIntent.current = {
        fingerprint,
        lifecycleKey: `web-plan-mode-lifecycle-${globalThis.crypto.randomUUID()}`,
        phaseKey: `web-plan-mode-transition-${globalThis.crypto.randomUUID()}`,
      };
    }
    phaseMutation.mutate({ selected, intent: phaseRetryIntent.current });
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!ready) {
      return;
    }
    setTurnStopped(false);
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
    const controller = new AbortController();
    turnAbort.current = controller;
    mutation.mutate({ request, intent: retryIntent.current, controller });
  };

  const stopForegroundExecution = () => {
    if (!mutation.isPending || !turnAbort.current) return;
    setTurnStopped(true);
    if (retryIntent.current) {
      retryIntent.current = {
        ...retryIntent.current,
        executionKey: `web-session-turn-execution-${globalThis.crypto.randomUUID()}`,
      };
    }
    turnAbort.current.abort("operator stopped foreground Run execution");
  };

  const changeContent = (value: string) => {
    setContent(value);
    setLastResult(null);
    setLastExecution(null);
    setTurnStopped(false);
    mutation.reset();
  };

  return <>
    {mutation.isPending && !publicModelStream && <section aria-label={t("临时模型回复", "Provisional model reply")}
      aria-live="polite" className="public-model-stream">
      <header>
        <div><span className="public-model-stream-dot" />
          <strong>{t("模型公开回复", "Public model reply")}</strong>
          {publicStream.snapshot && <span>{publicStream.snapshot.call.provider} / {publicStream.snapshot.call.model}</span>}
        </div>
        {publicStream.snapshot && client.hasControl ?
          <button className="compact-command danger" disabled={cancelMutation.isPending ||
            publicStream.snapshot.call.cancel_requested} onClick={() => cancelMutation.mutate()}
            type="button">
            {cancelMutation.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={14} /> :
              <Square aria-hidden="true" size={13} />}
            {publicStream.snapshot.call.cancel_requested
              ? t("正在停止", "Stopping") : t("停止", "Stop")}
          </button> : client.hasRunExecution && <button className="compact-command danger"
            onClick={stopForegroundExecution} type="button">
            <Square aria-hidden="true" size={13} />{t("停止", "Stop")}
          </button>}
      </header>
      {publicToolItems.length > 0 && <div aria-label={t("正在准备的工具调用", "Preparing tool calls")}
        className="public-model-tool-list">
        {publicToolItems.map((item) => <div className="public-model-tool-item" key={item.id}>
          <strong>{item.tool_name}</strong>
          <span>{item.status === "in_progress"
            ? t("正在准备调用", "Preparing call")
            : item.status === "ready_for_validation"
              ? t("参数已就绪，等待验证", "Arguments ready; awaiting validation")
              : item.status === "completed"
                ? t("已准备，正在提交验证", "Prepared; submitting for validation")
                : item.status === "cancelled"
                  ? t("准备已取消", "Preparation cancelled")
                  : t("准备失败", "Preparation failed")}
            {item.argument_bytes ? ` · ${item.argument_bytes} ${t("字节", "bytes")}` : ""}</span>
        </div>)}
      </div>}
      <p>{publicStream.snapshot?.text || (publicStream.status === "reconnecting"
        ? t("连接中断，正在重新连接…", "Connection interrupted, reconnecting…")
        : t("正在等待模型输出…", "Waiting for model output…"))}</p>
      <footer>
        <span>{publicStream.snapshot?.message_complete || publicStream.status === "finalizing"
          ? t("正在验证并提交回复", "Validating and committing reply")
          : t("临时内容，完成验证前不会写入历史", "Provisional; not stored before validation")}</span>
        {publicStream.error && <span className="connection-error">{publicStream.error}</span>}
        {cancelMutation.isError && <span className="connection-error">{errorMessage(cancelMutation.error)}</span>}
      </footer>
    </section>}
    <form className="session-composer" onSubmit={submit}>
      <textarea aria-label={t("Session 消息", "Session message")} autoComplete="off"
        disabled={!mutable || busy} onChange={(event) => changeContent(event.target.value)}
        onKeyDown={submitComposerOnEnter}
        placeholder={t("向这个 Run 发送消息", "Message this Run")} rows={3} spellCheck value={content} />
      <AgentComposerControls client={client} contextPartial={contextPartial}
        contextTokens={contextTokens}
        planMode={phase === "plan"}
        onPlanModeChange={phase && client.hasPlanDelivery ? changePlanMode : undefined}
        onOpenFiles={client.hasEvidenceAttachment && workspaceID
          ? () => setAttachmentsOpen(true) : undefined}
        onOpenPlugins={onOpenPlugins} route={run.config?.model_route ?? "code"}
        status={<div className="session-composer-state" aria-live="polite">
          {!mutable && <><StatusBadge status={run.status} /><span>{t("Run 当前不可用", "Run unavailable")}</span></>}
          {contentTooLarge && <span className="connection-error">{t("消息超过 16384 个 UTF-8 字节", "Message exceeds 16384 UTF-8 bytes")}</span>}
          {turnStopped && <span className="connection-error">{t(
            "执行已停止；已提交的消息仍在队列中，可点击继续处理。",
            "Execution stopped; the submitted message remains queued and can be continued.")}</span>}
          {mutation.isError && !turnStopped && <span className="connection-error">{errorMessage(mutation.error)}</span>}
          {phaseMutation.isPending && <span>{t("正在切换计划模式", "Changing Plan mode")}</span>}
          {phaseMutation.isError && <span className="connection-error">{errorMessage(phaseMutation.error)}</span>}
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
