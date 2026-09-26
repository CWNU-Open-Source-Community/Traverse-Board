import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, BookOpen, CircleEllipsis, FileDiff, Folder, LoaderCircle, Microscope, ShieldCheck, PanelTop } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { remarkCjkAutolinks } from "../../components/remark-cjk-autolinks";
import { APIRequestError, type CyberAgentClient } from "../../api/client";
import type { ThreadDetailView, ThreadTranscriptItemView, ThreadView, WorkspaceView } from "../../api/types";
import { usePublicModelStream } from "../../hooks/use-public-model-stream";
import { useRunEventStream } from "../../hooks/use-run-event-stream";
import { threadActivityLabel } from "../../lib/thread-activity-label";
import { projectThreadNarrative, type NarrativeEntry } from "../projection/narrative";
import { narrativeRepresentsFailedSubmission, recoveryRepresentsNotice } from "../projection/failure-feedback";
import { v2QueryKeys } from "../query-keys";
import { ControlledCommandProposalPanel } from "../../components/controlled-command-proposal-panel";
import { HostCommandProposalPanel } from "../../components/host-command-proposal-panel";
import { v2FileReferenceKey, type V2FileReference } from "./file-context";
import { imageIdentities, type WorkspaceImageAttachment } from "../../api/image-attachments";
import { fileAttachmentIdentities, type WorkspaceFileAttachment } from "../../api/file-attachments";
import { V2FileAttachments } from "./file-input";
import { V2QueuedMessages } from "./queued-messages";
import { V2AgentBrowser } from "./agent-browser";
import { v2AttachmentReferenceKey } from "../attachment-keys";
import { v2ImageReferenceKey, V2ImagePreview } from "./image-input";
import { V2ApplicationPreview } from "./application-preview";
import { V2TaskReview } from "./task-review";
import { V2ThreadContext } from "./thread-context";
import { V2ThreadPlanControl } from "./thread-plan";
import { useV2ThreadSubmissions, useV2ThreadTurn, V2SubmissionError, V2RecoveredSubmissionError, removeV2Submission, v2TurnFailed, v2TurnOutcomeKnown, v2TurnWasNotQueued, type V2TurnInput } from "../use-thread-turn";
import { inspectV2SteeringRequest, inspectV2TurnRequest } from "../recovery-api";
import { useV2RecoveryStore } from "../recovery-storage";
import { settleRecoveryTurn } from "../recovery-session";
import { assertV2DraftVersion, useV2DraftDocument } from "../draft-context";
import type { V2DraftVersion } from "../draft-version";
import { V2DraftConflict } from "./draft-conflict";
import { V2ApprovalCards } from "./approval-cards";
import { V2ActivityGroup } from "./activity-detail";
import { V2Composer } from "./composer";
import { V2ThreadRunRecovery } from "./thread-run-recovery";
import { V2Inspector } from "./inspector";
import { useV2ThreadExecution, V2ThreadExecutionControl, V2PausedThreadControl } from "./thread-execution-control";

function Narrative({ client, entries, threadID }: {
  client: CyberAgentClient;
  entries: NarrativeEntry[];
  threadID: string;
}) {
  return <ol className="v2-narrative">
    {entries.map((entry) => {
      if (entry.kind === "user") return <li className="v2-user-turn" key={entry.id}>
        <div>{entry.text}<V2ImagePreview client={client} images={entry.images ?? []} />
          <V2FileAttachments client={client} attachments={entry.attachments ?? []} />{entry.status === "cancelled" &&
          <small className="v2-message-status">已取消，不会继续处理</small>}
          {entry.status === "pending" && <small className="v2-message-status">已接收</small>}
          {entry.provisional && !entry.status && <small className="v2-message-status">正在发送…</small>}</div></li>;
      if (entry.kind === "assistant") return <li aria-live={entry.provisional ? "polite" : undefined}
        className={`v2-assistant-turn${entry.provisional ? " is-provisional" : ""}`} key={entry.id}>
        <ReactMarkdown remarkPlugins={[remarkGfm, remarkCjkAutolinks]}>{entry.text}</ReactMarkdown></li>;
      if (entry.kind === "activity") return <li className="v2-activity-turn" key={entry.id}>
        <V2ActivityGroup client={client} entry={entry} threadID={threadID} /></li>;
      return <li className={`v2-notice tone-${entry.tone}`} key={entry.id}>{entry.text}</li>;
    })}
  </ol>;
}

export function V2Conversation({ client, threadID, workspaces, onArchive, onManageModels,
  onOpenInspector, draft: legacyDraft, onDraftChange: legacyDraftChange, view = "conversation", onOpenTool, onOpenInspectorHome, onOpenWorktree }: {
  client: CyberAgentClient;
  threadID: string;
  workspaces: WorkspaceView[];
  onArchive: (thread: ThreadView) => void;
  onOpenInspector: (returnFocus: HTMLElement | null) => void;
  onManageModels: () => void;
  draft?: string;
  onDraftChange?: (content: string, expected?: string) => void;
  view?: "conversation" | "inspector";
  onOpenTool?: (tool: "run" | "session" | "schedule", resourceID?: string) => void;
  onOpenInspectorHome?: () => void;
  onOpenWorktree?: (workspace: WorkspaceView) => void;
}) {
  const queryClient = useQueryClient();
  const recovery = useV2RecoveryStore();
  const checkedRecovery = useRef(new Set<string>());
  const [checking, setChecking] = useState<string[]>([]);
  const [observations, setObservations] = useState<Record<string, string>>({});
  const [menuOpen, setMenuOpen] = useState(false);
  const [reviewOpen, setReviewOpen] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [contextOpen, setContextOpen] = useState(false);
  const contextTrigger = useRef<HTMLButtonElement>(null);
  const previewTrigger = useRef<HTMLButtonElement>(null);
  const [inspectorComposerOpen, setInspectorComposerOpen] = useState(false);
  const composerContainerRef = useRef<HTMLDivElement>(null);
  const [composerFocusRequest, setComposerFocusRequest] = useState<{ threadID: string } | null>(null);
  const [confirmedSubmission, setConfirmedSubmission] = useState<V2TurnInput | null>(null);
  const [deliveryMode, setDeliveryMode] = useState<"next_turn" | "steer">("next_turn");
  const reviewTrigger = useRef<HTMLButtonElement>(null);
  const turn = useV2ThreadTurn(client);
  const submissions = useV2ThreadSubmissions(threadID);
  const confirmSubmission = async (input: V2TurnInput): Promise<"accepted" | "rejected" | "unresolved"> => {
    // Older connectors without the read contract retain their explicit-key
    // retry behavior. A durable recovery scope always uses the read-only API.
    if (!recovery) {
      try {
        await turn.mutateAsync(input);
        setConfirmedSubmission(input);
        if (draft === (input.draft ?? input.content)) onDraftChange?.("", draft);
        return "accepted";
      } catch (error) {
        if (!v2TurnOutcomeKnown(error)) throw new V2SubmissionError(input, error);
        setConfirmedSubmission(input);
        return "rejected";
      }
    }
    setChecking((current) => [...current, input.operationKey]);
    try {
      const result = input.deliveryMode === "steer"
        ? await inspectV2SteeringRequest(client, input)
        : await inspectV2TurnRequest(client, input);
      if (result.state === "not_received" || (result.state === "received" && !result.message_id)) {
        setObservations((current) => ({ ...current, [input.operationKey]: result.state === "not_received"
          ? "服务端暂未找到原提交。可以继续核对，或主动发送原消息；不会自动重发。"
          : "原提交已登记，但消息尚未入队。可以继续核对，或主动继续提交原消息。" }));
        return "unresolved";
      }
      const accepted = result.state !== "rejected" &&
        !(input.deliveryMode === "steer" && result.message_status === "cancelled");
      settleRecoveryTurn(recovery, input, accepted);
      if (accepted && !input.draftVersion) queryClient.setQueryData<V2FileReference[]>(v2FileReferenceKey(input.workspaceID, input.threadID),
        (current) => current?.filter(({ id }) => !input.files?.some((file) => file.id === id)));
      if (accepted && !input.draftVersion) queryClient.setQueryData<WorkspaceImageAttachment[]>(v2ImageReferenceKey(input.workspaceID, input.threadID),
        (current) => current?.filter(({ id }) => !input.images?.some((image) => image.id === id)));
      if (accepted && !input.draftVersion) queryClient.setQueryData<WorkspaceFileAttachment[]>(v2AttachmentReferenceKey(input.workspaceID, input.threadID),
        (current) => current?.filter((file) => !input.attachments?.some((sent) => sent.id === file.id &&
          sent.workspace_id === file.workspace_id && sent.sha256 === file.sha256 && sent.byte_size === file.byte_size)));
      removeV2Submission(queryClient, input);
      setConfirmedSubmission(input);
      if (accepted && !managedDraft && !input.draftVersion && draft === (input.draft ?? input.content)) onDraftChange?.("", draft);
      await Promise.allSettled([
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(input.threadID) }),
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.transcript(input.threadID) }),
      ]);
      return accepted ? "accepted" : "rejected";
    } catch (error) {
      setObservations((current) => ({ ...current, [input.operationKey]: "暂时无法读取原提交结果，原消息和新草稿均已保留。" }));
      throw new V2SubmissionError(input, error);
    } finally { setChecking((current) => current.filter((key) => key !== input.operationKey)); }
  };
  useEffect(() => {
    for (const { input, error } of submissions) {
      if (!(error instanceof V2RecoveredSubmissionError) || checkedRecovery.current.has(input.operationKey)) continue;
      checkedRecovery.current.add(input.operationKey);
      void confirmSubmission(input).catch(() => undefined);
    }
  }, [submissions]);
  const executionQuery = useV2ThreadExecution(client, threadID);
  const scrollRef = useRef<HTMLDivElement>(null);
  const menuTriggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const firstMenuItemRef = useRef<HTMLButtonElement>(null);
  const olderScrollAnchorRef = useRef<{ height: number; top: number } | null>(null);
  const readingThreadRef = useRef("");
  const followLatestRef = useRef(true);
  const [hasNewContent, setHasNewContent] = useState(false);
  const optimistic = useMemo(() => submissions.filter(({ pending }) => pending).map(({ input }) => ({
    id: input.operationKey, text: input.content, images: input.images, attachments: input.attachments, createdAt: input.createdAt,
  })), [submissions]);
  const turnSubmitting = submissions.some(({ pending }) => pending);
  const reconciling = submissions.some((submission) => submission.reconciling);
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
  const managedDraft = useV2DraftDocument(detailQuery.data?.thread.workspace_id ?? "", threadID);
  const draft = managedDraft?.state.snapshot.text ?? legacyDraft;
  const onDraftChange = managedDraft?.changeText ?? legacyDraftChange;
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
      void queryClient.invalidateQueries({ queryKey: ["run", streamRunID] });
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
      .map((entry) => JSON.stringify([entry.text, imageIdentities(entry.images), fileAttachmentIdentities(entry.attachments)])));
    const pending: NarrativeEntry[] = optimistic.filter(({ text, images, attachments }) => !existingUserText.has(JSON.stringify([text, imageIdentities(images), fileAttachmentIdentities(attachments)])))
      .map((entry) => ({ id: entry.id, kind: "user", text: entry.text,
        images: entry.images, attachments: entry.attachments, createdAt: entry.createdAt, provisional: true }));
    return [...durableNarrative, ...pending];
  }, [durableNarrative, optimistic, transcriptItems]);
  const visibleNarrative = useMemo(() => narrative.filter((entry) =>
    !recoveryRepresentsNotice(entry, detailQuery.data?.recovery)), [narrative, detailQuery.data?.recovery]);
  const failedSubmissions = submissions.filter(({ pending, error }) => !pending && error);
  const submissionNotices = failedSubmissions.filter(({ error }) => !(error instanceof APIRequestError &&
    error.turnFailed && narrativeRepresentsFailedSubmission(durableNarrative, threadID, error.turnFailure)));
  useLayoutEffect(() => {
    if (view !== "conversation") { readingThreadRef.current = ""; return; }
    const element = scrollRef.current;
    if (!element || transcriptQuery.isLoading) return;
    if (readingThreadRef.current !== threadID) {
      readingThreadRef.current = threadID;
      const saved = queryClient.getQueryData<{ top: number; following: boolean }>(["v2", "reading", threadID]);
      followLatestRef.current = saved?.following ?? true;
      element.scrollTop = saved && !saved.following ? saved.top : element.scrollHeight;
      setHasNewContent(false);
      return;
    }
    const anchor = olderScrollAnchorRef.current;
    if (anchor) {
      element.scrollTop = anchor.top + (element.scrollHeight - anchor.height);
      olderScrollAnchorRef.current = null;
      return;
    }
    if (followLatestRef.current) element.scrollTop = element.scrollHeight;
    else setHasNewContent(true);
  }, [narrative.length, detailQuery.data?.active_run?.status, liveSnapshot?.revision,
    transcriptQuery.data?.pages.length, transcriptQuery.isLoading, threadID, queryClient, view]);

  useEffect(() => {
    setMenuOpen(false);
    setReviewOpen(false);
    setPreviewOpen(false);
    setContextOpen(false);
    setComposerFocusRequest(null);
    olderScrollAnchorRef.current = null;
    setDeliveryMode("next_turn");
  }, [threadID, view]);
  useEffect(() => {
    if (!composerFocusRequest || composerFocusRequest.threadID !== threadID || reviewOpen || previewOpen || contextOpen ||
      (view === "inspector" && !inspectorComposerOpen)) return;
    const frame = requestAnimationFrame(() => {
      const container = composerContainerRef.current;
      if (container?.dataset.threadId === composerFocusRequest.threadID && !container.hidden) {
        container.querySelector<HTMLTextAreaElement>("textarea")?.focus();
      }
      setComposerFocusRequest((current) => current === composerFocusRequest ? null : current);
    });
    return () => cancelAnimationFrame(frame);
  }, [composerFocusRequest, threadID, reviewOpen, previewOpen, contextOpen, view, inspectorComposerOpen]);
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
    <strong>无法打开对话</strong><span>请检查服务连接，然后重试。已有输入会保留。</span>
    <button onClick={() => void detailQuery.refetch()} type="button">重新打开</button>
    <details><summary>查看原因</summary>{detailQuery.error instanceof Error
      ? detailQuery.error.message : "请求未返回对话数据"}</details></div>;
  const detail = detailQuery.data;
  const currentRun = detail.active_run ?? detail.last_run;
  const workspace = workspaces.find(({ id }) => id === detail.thread.workspace_id);
  const runActive = executionQuery.data ? executionQuery.data.state !== "idle"
    : Boolean(detail.active_run && ["preparing", "running"].includes(detail.active_run.status));
  const modelActive = Boolean(liveSnapshot) &&
    (publicStream.status === "live" || publicStream.status === "finalizing");
  const working = turnSubmitting || modelActive || (!executionQuery.isError &&
    (executionQuery.data?.state === "running" || executionQuery.data?.state === "stopping"));
  const canSteer = executionQuery.data?.state === "running" ||
    (!executionQuery.data && detail.active_run?.status === "running");
  const activityLabel = threadActivityLabel({ threadID, execution: executionQuery.data,
    readable: client.hasThreadExecutionRead === true, error: executionQuery.isError });
  const fileReferenceUnavailableReason = currentRun.status === "waiting_approval"
      ? "当前任务正在等待批准，暂时不能新增文件引用。请先处理待批准的操作。"
      : working || runActive ? modelActive || activityLabel === "正在工作" || activityLabel === "正在停止"
        ? "项目内文件引用需等执行结束后发送；补充文字和上传附件可以排队。"
        : turnSubmitting ? "消息正在提交，暂时不能新增文件引用。已有输入会保留。"
          : activityLabel === "停止未完成" ? "停止尚未完成，暂时不能新增文件引用。请先重试停止。"
            : "活动状态尚未确认，暂时不能新增文件引用。已有输入会保留。"
        : undefined;

  const send = async (content: string, files?: V2FileReference[], images?: WorkspaceImageAttachment[], draftVersion?: V2DraftVersion, attachments?: WorkspaceFileAttachment[]) => {
    if (deliveryMode === "steer" && !canSteer) {
      throw new Error("当前任务已停止或不再运行。纠正草稿已保留；请选择“下一轮处理”发送。");
    }
    if (deliveryMode === "steer" && (files?.length || images?.length || attachments?.length)) {
      throw new Error("更新当前任务目前只支持文字。附件和引用已保留，请选择“下一轮处理”发送完整消息。");
    }
    const fingerprint = JSON.stringify([deliveryMode, content, files ?? [], imageIdentities(images), fileAttachmentIdentities(attachments)]);
    let replaced = submissions.findLast(({ error }) => v2TurnOutcomeKnown(error))?.input.operationKey;
    // The user's new request waits for unknown earlier submissions to settle.
    // Confirmation uses the original payload and key, never the edited draft.
    for (const { input } of submissions.filter(({ error }) => error && !v2TurnOutcomeKnown(error))) {
      const outcome = await confirmSubmission(input);
      if (outcome === "accepted" && JSON.stringify([input.deliveryMode ?? "next_turn", input.content, input.files ?? [], imageIdentities(input.images), fileAttachmentIdentities(input.attachments)]) === fingerprint) return;
      if (outcome === "rejected") replaced = input.operationKey;
      if (outcome === "unresolved") {
        if (recovery) {
          if (JSON.stringify([input.deliveryMode ?? "next_turn", input.content, input.files ?? [], imageIdentities(input.images), fileAttachmentIdentities(input.attachments)]) === fingerprint) { await turn.mutateAsync(input); return; }
          throw new V2SubmissionError(input, new Error("原提交尚未确认。新草稿已保留，请先核对或继续提交原消息。"));
        }
        replaced = input.operationKey;
      }
    }
    if (managedDraft && draftVersion) assertV2DraftVersion(managedDraft, draftVersion,
      { text: draft ?? content, files: files ?? [], images: images ?? [], attachments });
    const operationKey = `v2-thread-turn-${globalThis.crypto.randomUUID()}`;
    const input: V2TurnInput = { threadID, workspaceID: detail.thread.workspace_id ?? "", content,
	  ...(deliveryMode === "steer" ? { deliveryMode: "steer" as const, sessionID: currentRun.session_id } : {}),
      ...(draft !== undefined ? { draft } : {}),
      operationKey, createdAt: new Date().toISOString(), ...(files?.length ? { files } : {}),
      ...(images?.length ? { images } : {}),
      ...(attachments?.length ? { attachments } : {}),
      ...(draftVersion ? { draftVersion } : {}),
      ...(replaced ? { replacesOperationKey: replaced } : {}) };
    try {
      await turn.mutateAsync(input);
    } catch (error) {
      // The client validates this sealed reference against this request's Thread.
      // Its input was committed even though execution failed. Let the Composer
      // retire only this submitted draft/files; the failed mutation and durable
      // explanation stay visible. Original-key confirmations use the path above.
      if (error instanceof APIRequestError && error.turnFailed === true &&
        error.turnFailure?.thread_id === input.threadID) return;
      throw new V2SubmissionError(input, error);
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

  const stateLabel = activityLabel === "正在停止" || activityLabel === "停止未完成" ? activityLabel
    : modelActive || activityLabel === "正在工作" ? "正在工作"
      : reconciling ? "正在核对提交" : turnSubmitting ? "正在发送消息" : activityLabel === "等待新消息"
        ? currentRun.status === "waiting_approval" ? "等待批准" : currentRun.status === "paused" ? "已暂停" : activityLabel
        : activityLabel;
  const appendDraftAndReveal = (content: string) => {
    if (content) onDraftChange?.(draft?.trim() ? `${draft}\n\n${content}` : content);
    if (view === "inspector") setInspectorComposerOpen(true);
    setReviewOpen(false); setPreviewOpen(false); setContextOpen(false);
    setComposerFocusRequest({ threadID });
  };
  return <section className={`v2-conversation${view === "inspector" ? " is-inspector" : ""}`}>
    <header className="v2-conversation-header">
      <div><Folder aria-hidden="true" size={17} /><strong>{detail.thread.title}</strong>
        <span className={working ? "v2-working-state" : "v2-execution-label"} role="status">
          {working && <i />}{stateLabel}</span>
      </div>
      <div className="v2-header-actions">
        <V2ThreadExecutionControl client={client} execution={executionQuery.data}
          threadID={threadID} />
        <button aria-label="查看上下文" className="v2-review-trigger" onClick={() => setContextOpen(true)}
          ref={contextTrigger} type="button"><BookOpen aria-hidden="true" size={16} /><span>上下文</span></button>
        <button aria-label="应用预览" className="v2-review-trigger" onClick={() => setPreviewOpen(true)}
          ref={previewTrigger} type="button"><PanelTop aria-hidden="true" size={16} /><span>应用预览</span></button>
        <button aria-label="审阅改动" className="v2-review-trigger" onClick={() => setReviewOpen(true)}
          ref={reviewTrigger} type="button"><FileDiff aria-hidden="true" size={16} /><span>审阅改动</span></button>
        <button aria-expanded={menuOpen} aria-haspopup="menu" aria-label="对话操作"
          onClick={() => setMenuOpen((value) => !value)} ref={menuTriggerRef} type="button">
          <CircleEllipsis aria-hidden="true" size={18} />
        </button>
        {menuOpen && <div className="v2-thread-menu" ref={menuRef} role="menu">
          <button disabled={detail.thread.status !== "active" || !client.hasThreadControl} onClick={() => {
            setMenuOpen(false);
            menuTriggerRef.current?.focus();
            onArchive(detail.thread);
          }} ref={firstMenuItemRef} role="menuitem" type="button">
            <Archive aria-hidden="true" size={15} />归档对话</button>
          {view !== "inspector" && <button onClick={() => { setMenuOpen(false); onOpenInspector(menuTriggerRef.current); }}
            role="menuitem" type="button">
            <Microscope aria-hidden="true" size={15} />打开 Inspector</button>}
        </div>}
      </div>
    </header>
    {view === "inspector" && (onOpenTool || onOpenInspectorHome) && <nav className="v2-inspector-advanced" aria-label="高级检查">
      {onOpenInspectorHome && <button onClick={onOpenInspectorHome} type="button">全部运行与会话</button>}
      {onOpenTool && <details><summary>诊断工具</summary><div>
        <button onClick={() => onOpenTool("run", currentRun.id)} type="button">运行诊断与工具</button>
        {currentRun.session_id && <button onClick={() => onOpenTool("session", currentRun.session_id)}
          type="button">会话上下文</button>}
        <button onClick={() => onOpenTool("schedule", currentRun.id)} type="button">定时观察</button>
      </div></details>}
    </nav>}
    {contextOpen && <V2ThreadContext client={client} threadID={threadID} detail={detail}
      onClose={() => setContextOpen(false)} onRequestChange={appendDraftAndReveal} returnFocusRef={contextTrigger} />}
    {previewOpen && <V2ApplicationPreview key={currentRun.id} client={client} runID={currentRun.id} threadID={threadID}
      onClose={() => setPreviewOpen(false)} returnFocusRef={previewTrigger} onRequestStart={() => {
        const request = "请检查当前项目的启动方式，使用受管理的后台命令启动开发服务，等待就绪后给出确切的本机预览地址；保留启动输出和可停止的任务标识。如果启动失败，请报告实际错误。";
        appendDraftAndReveal(request);
      }} />}
    {reviewOpen && <V2TaskReview client={client} detail={detail} working={working}
      onOpenWorktree={onOpenWorktree}
      onClose={() => setReviewOpen(false)} returnFocusRef={reviewTrigger} onRequestChange={appendDraftAndReveal} />}
    {view === "inspector" && !transcriptQuery.isLoading && <V2Inspector client={client} key={threadID}
      detail={detail} threadID={threadID} durableItems={transcriptItems}
      liveSnapshot={liveSnapshot} liveStatus={publicStream.status}
      hasOlder={Boolean(transcriptQuery.hasNextPage)} isFetchingOlder={transcriptQuery.isFetchingNextPage}
      onLoadOlder={() => void transcriptQuery.fetchNextPage()} />}
    <div className="v2-conversation-scroll" ref={scrollRef} onScroll={(event) => {
      if (view !== "conversation") return;
      const element = event.currentTarget;
      const following = element.scrollHeight - element.clientHeight - element.scrollTop <= 48;
      followLatestRef.current = following;
      if (following) setHasNewContent(false);
      queryClient.setQueryData(["v2", "reading", threadID], { top: element.scrollTop, following });
    }}>
      <div className="v2-conversation-content">
        {detail.thread.status === "archived" && <p className="v2-notice" role="status">
          此对话已归档，仍可查看记录。请在设置的「已归档的聊天」中取消归档后继续发送。
        </p>}
        {transcriptQuery.isLoading && <div className="v2-transcript-loading"><LoaderCircle className="spin" size={16} />
          正在整理工作记录…</div>}
        {view === "conversation" && transcriptQuery.hasNextPage && <div className="v2-transcript-loading">
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
        {transcriptQuery.isError && !transcriptQuery.isFetchNextPageError && <div className="v2-notice tone-warning" role="alert">
          工作记录加载失败，不能据此判断任务是否已完成。
          <button onClick={() => void transcriptQuery.refetch()} type="button">重试工作记录</button>
        </div>}
        {!client.hasThreadExecutionRead && <div className="v2-notice" role="status">
          当前连接不提供 Agent 活动状态。对话与工作记录仍可查看。
        </div>}
        {executionQuery.isError && <div className="v2-notice tone-warning" role="alert">
          暂时无法确认 Agent 的当前活动。此读取失败不会关闭对话。
          <button onClick={() => void executionQuery.refetch()} type="button">刷新执行状态</button>
        </div>}
        {view === "conversation" && !transcriptQuery.isLoading && !transcriptQuery.isError && narrative.length === 0 && <div className="v2-transcript-empty">
          <span><ShieldCheck aria-hidden="true" size={18} /></span><p>{detail.thread.status === "archived"
            ? "此归档对话没有公开进展记录。" : "任务已经创建。Agent 的公开进展会出现在这里。"}</p></div>}
        {view === "conversation" && <Narrative client={client} entries={visibleNarrative} threadID={threadID} />}
        {view === "conversation" && <V2AgentBrowser client={client} runID={currentRun.id} running={working || runActive} />}
        {submissionNotices.map(({ input, error }) =>
          <div className="v2-notice tone-warning" key={input.operationKey} role="alert">
            <p>{v2TurnWasNotQueued(error) ? "这条消息未入队，草稿与引用已保留。请重新选择、移除引用或处理当前限制后重试。"
              : v2TurnFailed(error) ? "本轮执行未完成，失败记录与已完成的修改会保留。可以直接发送“继续”或补充要求。"
              : observations[input.operationKey] ?? "暂时无法确认这条消息的结果。草稿与引用已保留；发送新要求时会先核对原提交，也可以重试核对。"}</p>
            <details><summary>查看原因</summary>{error instanceof Error ? error.message : "请求失败"}</details>
            {!v2TurnOutcomeKnown(error) &&
              <button disabled={turnSubmitting || checking.includes(input.operationKey)} onClick={() => {
                void confirmSubmission(input).catch(() => undefined);
              }} type="button">{checking.includes(input.operationKey) ? "正在核对…" : "重试核对"}</button>}
            {recovery && observations[input.operationKey] && !v2TurnOutcomeKnown(error) &&
              <button disabled={turnSubmitting || checking.includes(input.operationKey)} onClick={() => {
                void turn.mutateAsync(input).then(() => {
                  if (!managedDraft && !input.draftVersion && draft === (input.draft ?? input.content)) onDraftChange?.("", draft);
                }).catch(() => undefined);
              }} type="button">继续提交原消息</button>}
            {onDraftChange && <button disabled={draft?.trim() === input.content.trim()}
              onClick={() => onDraftChange(draft?.trim() ? `${draft}\n\n${input.content}` : input.content)}
              type="button">{draft?.trim() ? "添加到草稿" : "放回输入框"}</button>}
          </div>)}
        {(eventStream.error || publicStream.error) && working && <div className="v2-notice tone-warning"
          role="status">实时进度暂不可用；持久工作记录仍会继续同步。</div>}
        {client.hasApprovalControl && detail.active_run && <V2ApprovalCards client={client}
          runID={currentRun.id} threadID={threadID} />}
        {detail.active_run && <div className="v2-command-approvals">
          <ControlledCommandProposalPanel client={client} runID={currentRun.id} threadID={threadID} />
          <HostCommandProposalPanel client={client} runID={currentRun.id} threadID={threadID} compact={view === "inspector"} />
        </div>}
      </div>
    </div>
    {view === "conversation" && hasNewContent && <button className="v2-jump-latest" onClick={() => {
      followLatestRef.current = true;
      if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
      setHasNewContent(false);
    }} type="button">有新内容 · 回到最新</button>}
    <div className="v2-composer-dock">
      {managedDraft && <V2DraftConflict key={threadID} client={client} workspaceID={detail.thread.workspace_id ?? ""}
        state={managedDraft.state} onResolve={(token, ref) => { managedDraft.document.resolve(managedDraft.scope, token, ref); }} />}
      {currentRun.status === "paused" && <V2PausedThreadControl client={client} threadID={threadID} runID={currentRun.id} />}
      {executionQuery.data?.state === "stop_failed" && <p role="alert">
        停止尚未完成。请重试停止，确认后再发送；已受理的要求会保留。
      </p>}
      {currentRun.session_id && <V2QueuedMessages client={client} threadID={threadID} runID={currentRun.id} sessionID={currentRun.session_id}
        workspaceID={detail.thread.workspace_id ?? ""} running={working || runActive} />}
      {reconciling ? <p className="v2-composer-caption" role="status">正在核对上次提交，避免重复执行。可以继续编辑，核对完成后再发送。</p>
        : executionQuery.data?.state === "stopping" ? <p className="v2-composer-caption" role="status">正在停止执行。可以继续编辑，停止完成后再发送。</p>
        : working && <p className="v2-composer-caption">{deliveryMode === "steer"
          ? "文字纠正会进入当前任务后续模型请求；已经开始的操作会保留。"
          : "消息将在下一轮处理；受理不代表已经执行。"}</p>}
      {working && draft && onDraftChange && submissions.some(({ input, pending }) =>
        pending && input.content === draft.trim()) && <button className="v2-composer-chip"
        onClick={() => onDraftChange("")} type="button">编写下一条</button>}
      {detail.recovery && <V2ThreadRunRecovery recovery={detail.recovery}
        approvalSaved={narrative.some((entry) => recoveryRepresentsNotice(entry, detail.recovery))} />}
      {executionQuery.data?.state === "idle" && executionQuery.data.last_turn_interrupted &&
        <p className="v2-composer-caption" role="status">上次执行已中断。可以发送新消息继续，已完成的修改会保留。</p>}
      {view === "inspector" && <button className="v2-inspector-composer-toggle"
        aria-expanded={inspectorComposerOpen} onClick={() => setInspectorComposerOpen((open) => !open)} type="button">
        {inspectorComposerOpen ? "收起消息编辑器" : draft ? "继续编辑草稿" : "补充消息"}</button>}
      <div className="v2-shared-composer" ref={composerContainerRef} data-thread-id={threadID} hidden={view === "inspector" && !inspectorComposerOpen}>
	  {(canSteer || deliveryMode === "steer") && <label className="v2-composer-caption">发送方式
	    <select aria-label="发送方式" value={deliveryMode} onChange={(event) => setDeliveryMode(event.target.value as "next_turn" | "steer")}>
	      <option value="next_turn">下一轮处理</option>
	      <option value="steer">更新当前任务（仅文字）</option>
	    </select>
	  </label>}
      <V2Composer client={client} disabled={!client.hasThreadControl ||
        detail.thread.status !== "active"}
        submitDisabled={reconciling || executionQuery.data?.state === "stopping" ||
          executionQuery.data?.state === "stop_failed"}
        draft={draft} onDraftChange={onDraftChange}
        threadControls={(hasUnsentDraft) => <V2ThreadPlanControl client={client} threadID={threadID} runID={currentRun.id}
          active={detail.thread.status === "active"} working={working || turnSubmitting || reconciling}
          hasUnsentDraft={hasUnsentDraft}
          onRequestChange={appendDraftAndReveal} />}
        confirmedSubmission={confirmedSubmission}
        presentedSubmissionErrors={failedSubmissions.map(({ input }) => input)}
        fileReferenceUnavailableReason={fileReferenceUnavailableReason}
        key={threadID}
        onManageModels={onManageModels} onSubmit={send} onWorkspaceChange={() => undefined}
        placeholder="输入消息…" runActive={runActive}
        runID={currentRun.id} threadID={threadID} workspaceID={detail.thread.workspace_id ?? ""}
        workspaces={workspaces} />
      <small className="v2-composer-caption">{workspace?.name ?? "本地工作区"} · Enter 发送，Shift + Enter 换行</small>
      </div>
    </div>
  </section>;
}
