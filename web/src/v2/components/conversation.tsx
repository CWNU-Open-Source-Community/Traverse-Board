import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, CircleEllipsis, Folder, LoaderCircle, Microscope, ShieldCheck } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { CyberAgentClient } from "../../api/client";
import type { ThreadDetailView, ThreadTranscriptItemView, WorkspaceView } from "../../api/types";
import { usePublicModelStream } from "../../hooks/use-public-model-stream";
import { useRunEventStream } from "../../hooks/use-run-event-stream";
import { projectThreadNarrative, type NarrativeEntry } from "../projection/narrative";
import { v2QueryKeys } from "../query-keys";
import { V2ApprovalCards } from "./approval-cards";
import { V2ActivityGroup } from "./activity-detail";
import { V2Composer } from "./composer";
import { V2ThreadRunRecovery } from "./thread-run-recovery";

interface OptimisticMessage {
  id: string;
  text: string;
  createdAt: string;
  canonicalId?: string;
}

const noOptimisticMessages: OptimisticMessage[] = [];

function Narrative({ client, entries, threadID }: {
  client: CyberAgentClient;
  entries: NarrativeEntry[];
  threadID: string;
}) {
  return <ol className="v2-narrative">
    {entries.map((entry) => {
      if (entry.kind === "user") return <li className="v2-user-turn" key={entry.id}>
        <div>{entry.text}</div></li>;
      if (entry.kind === "assistant") return <li aria-live={entry.provisional ? "polite" : undefined}
        className={`v2-assistant-turn${entry.provisional ? " is-provisional" : ""}`} key={entry.id}>
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{entry.text}</ReactMarkdown></li>;
      if (entry.kind === "activity") return <li className="v2-activity-turn" key={entry.id}>
        <V2ActivityGroup client={client} entry={entry} threadID={threadID} /></li>;
      return <li className={`v2-notice tone-${entry.tone}`} key={entry.id}>{entry.text}</li>;
    })}
  </ol>;
}

export function V2Conversation({ client, threadID, workspaces, onArchive, onManageModels,
  onOpenInspector }: {
  client: CyberAgentClient;
  threadID: string;
  workspaces: WorkspaceView[];
  onArchive: () => void;
  onOpenInspector: (returnFocus: HTMLElement | null) => void;
  onManageModels: () => void;
}) {
  const queryClient = useQueryClient();
  const [menuOpen, setMenuOpen] = useState(false);
  const [optimisticByThread, setOptimisticByThread] = useState<Record<string, OptimisticMessage[]>>({});
  const [submittingByThread, setSubmittingByThread] = useState<Record<string, boolean>>({});
  const scrollRef = useRef<HTMLDivElement>(null);
  const menuTriggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const firstMenuItemRef = useRef<HTMLButtonElement>(null);
  const olderScrollAnchorRef = useRef<{ height: number; top: number } | null>(null);
  const optimistic = optimisticByThread[threadID] ?? noOptimisticMessages;
  const turnSubmitting = submittingByThread[threadID] ?? false;
  const detailQuery = useQuery({
    queryKey: v2QueryKeys.thread(threadID),
    queryFn: ({ signal }) => client.get<ThreadDetailView>(
      `/threads/${encodeURIComponent(threadID)}`, {}, signal),
    enabled: Boolean(threadID),
    refetchInterval: (query) => {
      const detail = query.state.data;
      if (detail?.active_run && ["preparing", "running", "waiting_approval"]
        .includes(detail.active_run.status)) return 1_500;
      return optimistic.length > 0 ? 750 : false;
    },
  });
  const transcriptQuery = useInfiniteQuery({
    queryKey: v2QueryKeys.transcript(threadID),
    queryFn: ({ pageParam, signal }) => client.getPage<ThreadTranscriptItemView>(
      `/threads/${encodeURIComponent(threadID)}/transcript`, { limit: 100 }, pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.page.next_cursor || undefined,
    enabled: Boolean(threadID),
    refetchInterval: detailQuery.data?.active_run &&
      ["preparing", "running", "waiting_approval"].includes(detailQuery.data.active_run.status)
      ? 1_200 : optimistic.length > 0 ? 750 : false,
  });
  const activeRun = detailQuery.data?.active_run;
  const streamRunID = activeRun?.id ?? "";
  const streamEnabled = Boolean(activeRun && ["preparing", "running", "waiting_approval"]
    .includes(activeRun.status));
  const eventStream = useRunEventStream(client, streamEnabled ? streamRunID : "");
  const publicStream = usePublicModelStream(client, streamRunID, streamEnabled);
  const liveSnapshot = publicStream.snapshot?.call.run_id === streamRunID
    ? publicStream.snapshot : null;
  const latestFrame = eventStream.frames.at(-1);
  const refreshTimer = useRef<number | null>(null);
  useEffect(() => {
    if (!latestFrame || latestFrame.run_id !== streamRunID ||
      latestFrame.event.type === "model.delta" || refreshTimer.current !== null) return;
    refreshTimer.current = window.setTimeout(() => {
      refreshTimer.current = null;
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") });
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(threadID), exact: true });
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.transcript(threadID) });
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.approvals(streamRunID) });
    }, 100);
  }, [latestFrame, queryClient, streamRunID, threadID]);
  useEffect(() => () => {
    if (refreshTimer.current !== null) {
      window.clearTimeout(refreshTimer.current);
      refreshTimer.current = null;
    }
  }, [streamRunID, threadID]);
  const transcriptItems = useMemo(() => {
    const seen = new Set<string>();
    return (transcriptQuery.data?.pages ?? []).slice().reverse().flatMap(({ items }) => items)
      .filter((item) => {
        const identity = item.id || `${item.run_id}:${item.sequence}:${item.canonical_id}`;
        if (seen.has(identity)) return false;
        seen.add(identity);
        return true;
      });
  }, [transcriptQuery.data?.pages]);
  const durableNarrative = useMemo(() => projectThreadNarrative(transcriptItems, {
    runId: streamRunID, snapshot: liveSnapshot, status: publicStream.status,
  }), [liveSnapshot, publicStream.status, streamRunID, transcriptItems]);
  const narrative = useMemo(() => {
    const existingUserText = new Set(durableNarrative.filter((entry) => entry.kind === "user")
      .map((entry) => entry.text));
    const durableIdentities = new Set(transcriptItems.flatMap((item) =>
      [item.id, item.canonical_id, item.source_ref ?? ""]));
    const pending: NarrativeEntry[] = optimistic.filter(({ text, canonicalId }) =>
      !(canonicalId && durableIdentities.has(canonicalId)) && !existingUserText.has(text))
      .map((entry) => ({ id: entry.id, kind: "user", text: entry.text,
        createdAt: entry.createdAt, provisional: true }));
    return [...durableNarrative, ...pending];
  }, [durableNarrative, optimistic, transcriptItems]);
  useEffect(() => {
    const durableText = new Set(durableNarrative.filter((entry) => entry.kind === "user")
      .map((entry) => entry.text));
    const durableIdentities = new Set(transcriptItems.flatMap((item) =>
      [item.id, item.canonical_id, item.source_ref ?? ""]));
    setOptimisticByThread((current) => {
      const entries = current[threadID] ?? noOptimisticMessages;
      const next = entries.filter(({ text, canonicalId }) =>
        !(canonicalId && durableIdentities.has(canonicalId)) && !durableText.has(text));
      if (next.length === entries.length) return current;
      const updated = { ...current };
      if (next.length > 0) updated[threadID] = next;
      else delete updated[threadID];
      return updated;
    });
  }, [durableNarrative, threadID, transcriptItems]);
  useLayoutEffect(() => {
    const element = scrollRef.current;
    if (!element) return;
    const anchor = olderScrollAnchorRef.current;
    if (anchor) {
      element.scrollTop = anchor.top + (element.scrollHeight - anchor.height);
      olderScrollAnchorRef.current = null;
      return;
    }
    element.scrollTop = element.scrollHeight;
  }, [narrative.length, detailQuery.data?.active_run?.status, liveSnapshot?.revision,
    transcriptQuery.data?.pages.length]);

  useEffect(() => {
    setMenuOpen(false);
    olderScrollAnchorRef.current = null;
  }, [threadID]);
  useEffect(() => {
    if (!menuOpen) return;
    firstMenuItemRef.current?.focus();
    const closeForOutsidePointer = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node) || menuRef.current?.contains(target) ||
        menuTriggerRef.current?.contains(target)) return;
      setMenuOpen(false);
    };
    const closeForEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      setMenuOpen(false);
      menuTriggerRef.current?.focus();
    };
    document.addEventListener("pointerdown", closeForOutsidePointer);
    document.addEventListener("keydown", closeForEscape);
    return () => {
      document.removeEventListener("pointerdown", closeForOutsidePointer);
      document.removeEventListener("keydown", closeForEscape);
    };
  }, [menuOpen]);

  if (detailQuery.isLoading) return <div className="v2-main-loading"><LoaderCircle className="spin" size={20} />
    <span>正在打开对话…</span></div>;
  if (detailQuery.isError || !detailQuery.data) return <div className="v2-main-error" role="alert">
    <strong>无法打开对话</strong><span>{detailQuery.error instanceof Error
      ? detailQuery.error.message : "未知错误"}</span></div>;
  const detail = detailQuery.data;
  const currentRun = detail.active_run ?? detail.last_run;
  const workspace = workspaces.find(({ id }) => id === detail.thread.workspace_id);
  const runActive = Boolean(detail.active_run && ["preparing", "running"].includes(detail.active_run.status));
  const modelActive = Boolean(liveSnapshot) &&
    (publicStream.status === "live" || publicStream.status === "finalizing");
  const working = turnSubmitting || modelActive;

  const send = async (content: string) => {
    const submissionThreadID = threadID;
    const id = `pending:${globalThis.crypto.randomUUID()}`;
    setSubmittingByThread((current) => ({ ...current, [submissionThreadID]: true }));
    setOptimisticByThread((current) => ({ ...current, [submissionThreadID]: [
      ...(current[submissionThreadID] ?? noOptimisticMessages),
      { id, text: content, createdAt: new Date().toISOString() },
    ] }));
    try {
      const submission = await client.submitThreadTurn(submissionThreadID, {
        version: "thread_message_submission.v1", content,
      }, `v2-thread-turn-${globalThis.crypto.randomUUID()}`);
      queryClient.setQueryData<ThreadDetailView>(v2QueryKeys.thread(submissionThreadID),
        (current) => current ? { ...current, thread: submission.thread } : current);
      setOptimisticByThread((current) => ({ ...current, [submissionThreadID]:
        (current[submissionThreadID] ?? noOptimisticMessages).map((entry) => entry.id === id
          ? { ...entry, canonicalId: submission.steering.id } : entry) }));
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") }),
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(submissionThreadID) }),
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.transcript(submissionThreadID) }),
      ]);
    } catch (error) {
      setOptimisticByThread((current) => {
        const next = (current[submissionThreadID] ?? noOptimisticMessages)
          .filter((entry) => entry.id !== id);
        const updated = { ...current };
        if (next.length > 0) updated[submissionThreadID] = next;
        else delete updated[submissionThreadID];
        return updated;
      });
      await Promise.allSettled([
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") }),
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(submissionThreadID) }),
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.transcript(submissionThreadID) }),
      ]);
      throw error;
    } finally {
      setSubmittingByThread((current) => {
        if (!current[submissionThreadID]) return current;
        const updated = { ...current };
        delete updated[submissionThreadID];
        return updated;
      });
    }
  };

  const loadOlderTranscript = async () => {
    const element = scrollRef.current;
    if (element) olderScrollAnchorRef.current = { height: element.scrollHeight, top: element.scrollTop };
    try {
      await transcriptQuery.fetchNextPage();
    } catch {
      olderScrollAnchorRef.current = null;
    }
  };

  return <section className="v2-conversation">
    <header className="v2-conversation-header">
      <div><Folder aria-hidden="true" size={17} /><strong>{detail.thread.title}</strong>
        {working && <span className="v2-working-state"><i />正在工作</span>}
        {detail.recovery && <span className="v2-recovery-state">可继续</span>}</div>
      <div className="v2-header-actions">
        <button aria-expanded={menuOpen} aria-haspopup="menu" aria-label="对话操作"
          onClick={() => setMenuOpen((value) => !value)} ref={menuTriggerRef} type="button">
          <CircleEllipsis aria-hidden="true" size={18} />
        </button>
        {menuOpen && <div className="v2-thread-menu" ref={menuRef} role="menu">
          <button onClick={() => {
            setMenuOpen(false);
            menuTriggerRef.current?.focus();
            onArchive();
          }} ref={firstMenuItemRef} role="menuitem" type="button">
            <Archive aria-hidden="true" size={15} />归档对话</button>
          <button onClick={() => { setMenuOpen(false); onOpenInspector(menuTriggerRef.current); }}
            role="menuitem" type="button">
            <Microscope aria-hidden="true" size={15} />打开 Inspector</button>
        </div>}
      </div>
    </header>
    <div className="v2-conversation-scroll" ref={scrollRef}>
      <div className="v2-conversation-content">
        {transcriptQuery.isLoading && <div className="v2-transcript-loading"><LoaderCircle className="spin" size={16} />
          正在整理工作记录…</div>}
        {transcriptQuery.hasNextPage && <div className="v2-transcript-loading">
          <button className="v2-composer-chip" disabled={transcriptQuery.isFetchingNextPage}
            onClick={() => void loadOlderTranscript()} type="button">
            {transcriptQuery.isFetchingNextPage
              ? <><LoaderCircle aria-hidden="true" className="spin" size={16} />正在加载更早记录…</>
              : "加载更早记录"}
          </button>
        </div>}
        {transcriptQuery.isFetchNextPageError && <div className="v2-notice tone-warning" role="alert">
          更早的工作记录加载失败，请重试。
        </div>}
        {!transcriptQuery.isLoading && narrative.length === 0 && <div className="v2-transcript-empty">
          <span><ShieldCheck aria-hidden="true" size={18} /></span><p>任务已经创建。Agent 的公开进展会出现在这里。</p></div>}
        <Narrative client={client} entries={narrative} threadID={threadID} />
        {(eventStream.error || publicStream.error) && working && <div className="v2-notice tone-warning"
          role="status">实时进度暂不可用；持久工作记录仍会继续同步。</div>}
        {client.hasApprovalControl && detail.active_run && <V2ApprovalCards client={client}
          runID={currentRun.id} threadID={threadID} />}
      </div>
    </div>
    <div className="v2-composer-dock">
      {detail.recovery && <V2ThreadRunRecovery recovery={detail.recovery} />}
      <V2Composer client={client} disabled={!client.hasThreadControl ||
        detail.thread.status !== "active"}
        key={threadID}
        onManageModels={onManageModels} onSubmit={send} onWorkspaceChange={() => undefined}
        placeholder="输入消息…" runActive={runActive}
        runID={currentRun.id} threadID={threadID} workspaceID={detail.thread.workspace_id ?? ""}
        workspaces={workspaces} />
      <small className="v2-composer-caption">{workspace?.name ?? "本地工作区"} · Enter 发送，Shift + Enter 换行</small>
    </div>
  </section>;
}
