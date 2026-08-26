import { useEffect, useMemo, useRef, useState, type Dispatch, type FormEvent,
  type MutableRefObject, type SetStateAction } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ChevronDown, LoaderCircle, MessagesSquare, SendHorizontal,
  ShieldCheck } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type {
  RunDetailView,
  RunExecutionControlView,
  ThreadDetailView,
  ThreadMessageControlRequestView,
  ThreadMessageControlView,
  ThreadTranscriptItemView,
} from "../api/types";
import { usePagedResource } from "../hooks/use-paged-resource";
import { usePublicModelStream } from "../hooks/use-public-model-stream";
import { useRunEventStream } from "../hooks/use-run-event-stream";
import { submitComposerOnEnter } from "../lib/composer-keyboard";
import { formatDate, formatNumber, shortID } from "../lib/format";
import { useLocale } from "../lib/locale";
import { ErrorState, KeyValue, LoadingState, StatusBadge } from "./common";
import { ApprovalPanel } from "./approval-panel";
import { RunControlPanel } from "./run-workspace";
import { ThreadTranscript } from "./thread-transcript";

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
  const transcriptQuery = usePagedResource<ThreadTranscriptItemView>(client,
    ["thread", threadID, "transcript"],
    `/threads/${encodeURIComponent(threadID)}/transcript`,
    { limit: 100 }, Boolean(threadID));
  const durableItems = useMemo(() => [...(transcriptQuery.data?.pages ?? [])]
    .reverse().flatMap((page) => page.items), [transcriptQuery.data]);
  const [pendingItems, setPendingItems] = useState<ThreadTranscriptItemView[]>([]);
  const refreshTimer = useRef<number | null>(null);
  useEffect(() => {
    setPendingItems([]);
    if (refreshTimer.current !== null) {
      window.clearTimeout(refreshTimer.current);
      refreshTimer.current = null;
    }
  }, [threadID]);

  if (!threadID) {
    return <div className="workspace-empty"><MessagesSquare aria-hidden="true" size={24} />
      <h1>{t("选择一个 Thread（任务）", "Select a Thread")}</h1></div>;
  }
  if (detailQuery.isLoading) return <LoadingState label={t("加载 Thread", "Loading Thread")} />;
  if (detailQuery.isError || !detailQuery.data) return <ErrorState error={detailQuery.error} />;
  const detail = detailQuery.data;
  const currentRun = detail.active_run ?? detail.last_run;
  return <ThreadWorkspaceReady client={client} currentRunID={currentRun.id}
    detail={detail} durableItems={durableItems} pendingItems={pendingItems}
    refreshTimer={refreshTimer} setPendingItems={setPendingItems}
    transcriptQuery={transcriptQuery} />;
}

function ThreadWorkspaceReady({ client, currentRunID, detail, durableItems, pendingItems,
  refreshTimer, setPendingItems, transcriptQuery }: {
  client: CyberAgentClient;
  currentRunID: string;
  detail: ThreadDetailView;
  durableItems: ThreadTranscriptItemView[];
  pendingItems: ThreadTranscriptItemView[];
  refreshTimer: MutableRefObject<number | null>;
  setPendingItems: Dispatch<SetStateAction<ThreadTranscriptItemView[]>>;
  transcriptQuery: ReturnType<typeof usePagedResource<ThreadTranscriptItemView>>;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const runDetailQuery = useQuery({
    queryKey: ["run", currentRunID],
    queryFn: ({ signal }) => client.get<RunDetailView>(
      `/runs/${encodeURIComponent(currentRunID)}`, {}, signal),
    enabled: Boolean(currentRunID),
  });
  const eventStream = useRunEventStream(client, currentRunID);
  const liveStream = usePublicModelStream(client, currentRunID,
    Boolean(detail.active_run) && ["running", "preparing", "waiting_approval"]
      .includes(detail.active_run?.status ?? ""));
  const latestFrame = eventStream.frames.at(-1);
  useEffect(() => {
    if (!latestFrame || latestFrame.event.type === "model.delta" || refreshTimer.current !== null) {
      return;
    }
    refreshTimer.current = window.setTimeout(() => {
      refreshTimer.current = null;
      void queryClient.invalidateQueries({ queryKey: ["thread", detail.thread.id], exact: true });
      void queryClient.invalidateQueries({ queryKey: ["thread", detail.thread.id, "transcript"] });
      void queryClient.invalidateQueries({ queryKey: ["run", currentRunID], exact: true });
      void queryClient.invalidateQueries({ queryKey: ["run", currentRunID, "approvals"] });
    }, 100);
  }, [currentRunID, detail.thread.id, latestFrame, queryClient, refreshTimer]);
  useEffect(() => () => {
    if (refreshTimer.current !== null) {
      window.clearTimeout(refreshTimer.current);
      refreshTimer.current = null;
    }
  }, [currentRunID, refreshTimer]);
  useEffect(() => {
    const durableIdentities = new Set(durableItems.flatMap((item) =>
      [item.canonical_id, item.source_ref ?? ""]));
    setPendingItems((current) => current.filter((item) =>
      !durableIdentities.has(item.canonical_id)));
  }, [durableItems, setPendingItems]);
  const onQueued = (submission: ThreadMessageControlView, content: string) => {
    const existing = detail.runs.find((binding) => binding.run.id === submission.run_id)?.ordinal;
    const runOrdinal = existing ?? Math.max(0, ...detail.runs.map((binding) => binding.ordinal)) + 1;
    const steering = submission.steering;
    const item: ThreadTranscriptItemView = {
      version: "thread_transcript.v1", id: `pending:${steering.id}`,
      canonical_id: steering.id, run_id: submission.run_id, run_ordinal: runOrdinal,
      sequence: Number.MAX_SAFE_INTEGER - 3, activity_type: "message", stage: "started",
      kind: "operator_input", source: "operator", title: "用户消息已排队",
      detail: content, status: "pending", verifiable: true,
      instruction_authorized: true, source_ref: steering.id, provisional: true,
      durable: false, created_at: steering.created_at,
    };
    setPendingItems((current) => [...current.filter((entry) =>
      entry.canonical_id !== item.canonical_id), item]);
  };
  const deliveryCount = durableItems.filter((item) => item.activity_type === "delivery").length;

  return <div className="workspace-view thread-workspace">
    <header className="workspace-header">
      <div>
        <div className="workspace-kicker">Thread {shortID(detail.thread.id)}</div>
        <h1>{detail.thread.title}</h1>
        <div className="header-meta"><StatusBadge status={detail.thread.status} />
          <span>{t("稳定 Thread 身份", "Stable Thread identity")}</span>
          <span>{t("输入", "Composer")}: {detail.thread.composer_state}</span>
          {deliveryCount > 0 && <span>{t("交付", "Delivery")}: {deliveryCount}</span>}</div>
      </div>
    </header>
    <div className="session-summary thread-summary">
      <dl className="detail-grid">
        <KeyValue label={t("工作区", "Workspace")} value={detail.thread.workspace_id || "-"} />
        <KeyValue label={t("当前 Run", "Current Run")} value={shortID(currentRunID)} />
        <KeyValue label={t("Run 次数", "Run attempts")} value={formatNumber(detail.runs.length)} />
        <KeyValue label={t("更新时间", "Updated")} value={formatDate(detail.thread.updated_at)} />
      </dl>
    </div>
    <details className="thread-control-drawer" open={detail.active_run?.status === "waiting_approval"}>
      <summary><span><Activity aria-hidden="true" size={15} />
        {t("运行与审批控制", "Run and approval controls")}</span>
        <span><StatusBadge status={detail.active_run?.status ?? detail.last_run.status} />
          <ChevronDown aria-hidden="true" size={15} /></span></summary>
      <div className="thread-control-content">
        {runDetailQuery.isLoading && <LoadingState />}
        {runDetailQuery.isError && <ErrorState error={runDetailQuery.error} />}
        {runDetailQuery.data && <RunControlPanel client={client} detail={runDetailQuery.data}
          threadID={detail.thread.id} />}
        {detail.active_run?.status === "waiting_approval" &&
          <ApprovalPanel client={client} runID={currentRunID} threadID={detail.thread.id} />}
        <p className="thread-control-boundary"><ShieldCheck aria-hidden="true" size={13} />
          {t("暂停、恢复与审批继续使用 Go 控制面；此页面不持有执行权限。",
            "Pause, resume, and approval remain Go control-plane operations; this page holds no execution authority.")}</p>
      </div>
    </details>
    <div className="workspace-content thread-transcript-region">
      {transcriptQuery.isLoading && <LoadingState />}
      {transcriptQuery.isError && <ErrorState error={transcriptQuery.error} />}
      {!transcriptQuery.isLoading && !transcriptQuery.isError &&
        <ThreadTranscript durableItems={durableItems}
          hasOlder={Boolean(transcriptQuery.hasNextPage)}
          isFetchingOlder={transcriptQuery.isFetchingNextPage}
          liveSnapshot={liveStream.snapshot} liveStatus={liveStream.status}
          onLoadOlder={() => void transcriptQuery.fetchNextPage()} pendingItems={pendingItems}
          streamError={eventStream.error || liveStream.error} />}
    </div>
    <ThreadComposer client={client} detail={detail} onQueued={onQueued} />
  </div>;
}

function ThreadComposer({ client, detail, onQueued }: {
  client: CyberAgentClient;
  detail: ThreadDetailView;
  onQueued: (submission: ThreadMessageControlView, content: string) => void;
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
      onQueued(submission, request.content);
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
      void queryClient.invalidateQueries({
        queryKey: ["thread", detail.thread.id, "transcript"],
      });
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
    <textarea aria-label={t("继续此 Thread", "Continue this Thread")} disabled={mutation.isPending}
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
