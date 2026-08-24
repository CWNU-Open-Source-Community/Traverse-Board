import { useMemo, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, MessagesSquare, SendHorizontal } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type {
  RunDetailView,
  RunExecutionControlView,
  ThreadDetailView,
  ThreadMessageControlRequestView,
  ThreadMessageControlView,
  ThreadMessageView,
} from "../api/types";
import { usePagedResource } from "../hooks/use-paged-resource";
import { submitComposerOnEnter } from "../lib/composer-keyboard";
import { formatDate, formatNumber, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { EmptyState, ErrorState, KeyValue, LoadMoreButton, LoadingState, StatusBadge } from "./common";
import { SafeMarkdown } from "./safe-markdown";

const maximumContentBytes = 16 * 1024;

interface ThreadRetryIntent {
  fingerprint: string;
  messageKey: string;
  lifecycleKey: string;
  executionKey: string;
}

interface ThreadTurnResult {
  submission: ThreadMessageControlView;
  execution: RunExecutionControlView | null;
}

export function ThreadWorkspace({ client, threadID }: {
  client: CyberAgentClient;
  threadID: string;
}) {
  const { t } = useLocale();
  const detailQuery = useQuery({
    queryKey: ["thread", threadID],
    queryFn: ({ signal }) => client.get<ThreadDetailView>(
      `/threads/${encodeURIComponent(threadID)}`, {}, signal),
    enabled: Boolean(threadID),
  });
  const messagesQuery = usePagedResource<ThreadMessageView>(client,
    ["thread", threadID, "messages"],
    `/threads/${encodeURIComponent(threadID)}/messages`,
    { limit: 100, include_compacted: true }, Boolean(threadID));
  const messages = useMemo(() => messagesQuery.data?.pages
    .flatMap((page) => page.items) ?? [], [messagesQuery.data]);

  if (!threadID) {
    return <div className="workspace-empty"><MessagesSquare aria-hidden="true" size={24} />
      <h1>{t("选择一个任务", "Select a task")}</h1></div>;
  }
  if (detailQuery.isLoading) return <LoadingState label={t("加载任务", "Loading task")} />;
  if (detailQuery.isError || !detailQuery.data) return <ErrorState error={detailQuery.error} />;
  const detail = detailQuery.data;
  const currentRun = detail.active_run ?? detail.last_run;

  return <div className="workspace-view">
    <header className="workspace-header">
      <div>
        <div className="workspace-kicker">Thread {shortID(detail.thread.id)}</div>
        <h1>{detail.thread.title}</h1>
        <div className="header-meta"><StatusBadge status={detail.thread.status} />
          <span>{t("稳定任务身份", "Stable task identity")}</span>
          <span>{t("输入", "Composer")}: {detail.thread.composer_state}</span></div>
      </div>
    </header>
    <div className="session-summary">
      <dl className="detail-grid">
        <KeyValue label={t("工作区", "Workspace")} value={detail.thread.workspace_id || "-"} />
        <KeyValue label={t("当前 Run", "Current Run")} value={shortID(currentRun.id)} />
        <KeyValue label={t("Run 次数", "Run attempts")} value={formatNumber(detail.runs.length)} />
        <KeyValue label={t("更新时间", "Updated")} value={formatDate(detail.thread.updated_at)} />
      </dl>
    </div>
    <div className="workspace-content session-content">
      <div className="section-heading"><h2>{t("任务历史", "Task history")}</h2>
        <span>{formatNumber(messages.length)}</span></div>
      {messagesQuery.isLoading && <LoadingState />}
      {messagesQuery.isError && <ErrorState error={messagesQuery.error} />}
      {!messagesQuery.isLoading && !messagesQuery.isError && messages.length === 0 &&
        <EmptyState>{t("暂无消息", "No messages")}</EmptyState>}
      <div className="message-list">
        {messages.map((message) => <article className={`message-row role-${message.role}`}
          key={`${message.run_id}:${message.id}`}>
          <header><strong>{message.role}</strong><StatusBadge status={message.status} />
            <StatusBadge status={`run ${shortID(message.run_id)}`} />
            <span>{formatNumber(message.token_estimate)} {t("令牌", "tokens")}</span>
            {message.compacted && <StatusBadge status="compacted" />}
            <time dateTime={message.created_at}>{formatDate(message.created_at)}</time></header>
          {message.role === "assistant" ? <SafeMarkdown>{message.content}</SafeMarkdown> :
            <p>{message.content}</p>}
        </article>)}
      </div>
      <LoadMoreButton hasNextPage={Boolean(messagesQuery.hasNextPage)}
        isFetching={messagesQuery.isFetchingNextPage}
        onClick={() => void messagesQuery.fetchNextPage()} />
    </div>
    <ThreadComposer client={client} detail={detail} />
  </div>;
}

function ThreadComposer({ client, detail }: {
  client: CyberAgentClient;
  detail: ThreadDetailView;
}) {
  const { t } = useLocale();
  const [content, setContent] = useState("");
  const retryIntent = useRef<ThreadRetryIntent | null>(null);
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: async ({ request, intent }: {
      request: ThreadMessageControlRequestView;
      intent: ThreadRetryIntent;
    }): Promise<ThreadTurnResult> => {
      const submission = await client.submitThreadMessage(detail.thread.id, request,
        intent.messageKey);
      let runnableStatus = submission.successor_created ? "created" :
        detail.active_run?.id === submission.run_id ? detail.active_run.status : "";
      if (runnableStatus === "") {
        const resolved = await client.get<RunDetailView>(
          `/runs/${encodeURIComponent(submission.run_id)}`);
        runnableStatus = resolved.run.status;
      }
      if (runnableStatus === "waiting_approval" || runnableStatus === "preparing" ||
        !client.hasRunExecution) {
        return { submission, execution: null };
      }
      if ((runnableStatus === "created" || runnableStatus === "paused") &&
        client.hasRunLifecycle) {
        await client.controlRunLifecycle(submission.run_id, {
          version: "run_lifecycle_control.v1",
          action: runnableStatus === "created" ? "start" : "resume",
        }, intent.lifecycleKey);
      } else if (runnableStatus !== "running") {
        return { submission, execution: null };
      }
      const execution = await client.executeRun(submission.run_id, {
        version: "run_execution_handoff.v1", max_steps: 1,
      }, intent.executionKey);
      return { submission, execution };
    },
    onSuccess: (result) => {
      retryIntent.current = null;
      setContent("");
      void queryClient.invalidateQueries({ queryKey: ["threads"] });
      void queryClient.invalidateQueries({ queryKey: ["thread", detail.thread.id] });
      void queryClient.invalidateQueries({ queryKey: ["runs"] });
      void queryClient.invalidateQueries({ queryKey: ["run", result.submission.run_id] });
    },
  });
  if (!client.hasThreadControl || detail.thread.status !== "active") return null;
  const normalized = content.trim();
  const contentBytes = new TextEncoder().encode(normalized).byteLength;
  const ready = contentBytes > 0 && contentBytes <= maximumContentBytes && !mutation.isPending;
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!ready) return;
    const request: ThreadMessageControlRequestView = {
      version: "thread_message_submission.v1", content: normalized,
    };
    const fingerprint = `${detail.thread.id}:${JSON.stringify(request)}`;
    if (retryIntent.current?.fingerprint !== fingerprint) {
      retryIntent.current = {
        fingerprint,
        messageKey: `web-thread-message-${globalThis.crypto.randomUUID()}`,
        lifecycleKey: `web-thread-lifecycle-${globalThis.crypto.randomUUID()}`,
        executionKey: `web-thread-execution-${globalThis.crypto.randomUUID()}`,
      };
    }
    mutation.mutate({ request, intent: retryIntent.current });
  };
  return <form className="session-composer" onSubmit={submit}>
    <textarea aria-label={t("继续此任务", "Continue this task")} disabled={mutation.isPending}
      onChange={(event) => { setContent(event.target.value); mutation.reset(); }}
      onKeyDown={submitComposerOnEnter}
      placeholder={detail.active_run ? t("发送后继续当前 Run", "Send and continue the current Run") :
        t("此 Run 已结束；发送后会安全创建后继 Run", "This Run ended; sending creates a safe successor Run")}
      rows={3} value={content} />
    <div className="session-composer-footer">
      <div className="session-composer-state" aria-live="polite"><span>
        {detail.thread.composer_state === "waiting_approval" ?
          t("消息会排队等待审批完成", "The message will queue until approval resolves") :
          detail.active_run ? t("继续当前 Run", "Continue current Run") :
            t("创建无授权继承的后继 Run", "Create successor without inherited authority")}
      </span></div>
      <span className={`byte-count${contentBytes > maximumContentBytes ? " invalid" : ""}`}>
        {contentBytes} / {maximumContentBytes} {t("字节", "bytes")}
      </span>
      <button aria-label={t("发送", "Send")} className="composer-send-button" disabled={!ready}
        title={t("发送", "Send")} type="submit">
        {mutation.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={16} /> :
          <SendHorizontal aria-hidden="true" size={16} />}
      </button>
    </div>
    {contentBytes > maximumContentBytes && <p className="connection-error">
      {t("消息超过 16 KiB", "Message exceeds 16 KiB")}</p>}
    {mutation.isError && <p className="connection-error" role="alert">
      {mutation.error instanceof Error ? mutation.error.message : t("发送失败", "Send failed")}</p>}
  </form>;
}
