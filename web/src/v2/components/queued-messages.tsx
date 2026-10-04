import { useEffect, useId, useMemo, useReducer, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronUp, RefreshCw } from "lucide-react";
import { APIRequestError, type APIClient } from "../../api/client";
import { inspectQueueCancellation, inspectQueuePromotion, inspectQueueRevision, promoteQueuedMessage, provesQueueRevisionUnchanged,
  readQueuedMessages, reviseQueuedMessage, type QueueBinding, type QueuePromotionInput, type QueuedMessage } from "../../api/queued-messages";
import { getV2DraftDocument } from "../draft-context";
import type { DraftDocument } from "../draft-document";
import { useV2RecoveryStore, type V2RecoveryStore } from "../recovery-storage";
import { closeQueueEdit, queueEditVisible, reopenQueueEdit, queueEditKey, threadQueueEditPrefix, queueEditScope, threadQueueOperationPrefix, readQueueEdits, readQueueOperations,
  saveQueueOperation, settleQueueOperation, type QueueEdit, type QueueOperation } from "../queue-recovery";
import { v2QueryKeys } from "../query-keys";
import { V2DraftConflict } from "./draft-conflict";
import { V2QueuedMessageRow, V2QueueMessageDetails } from "./queued-message-row";
import "./queued-messages.css";

const explanation = (error: unknown) => error instanceof Error ? error.message : "操作暂未完成，内容已保留。";
const knownRejection = (error: unknown) => error instanceof APIRequestError && [400, 401, 403, 404, 409, 412, 413, 422].includes(error.status);
const empty = (text = "") => ({ text, files: [], images: [] });
const promotionInput = (operation: QueueOperation): QueuePromotionInput => {
  if (!operation.expectedAttemptID || !operation.expectedExecutionID) throw new Error("原引导操作的执行身份不完整，原记录已保留。");
  return { ...operation, expectedAttemptID: operation.expectedAttemptID, expectedExecutionID: operation.expectedExecutionID };
};

export function useV2QueuedMessagesQuery(client: APIClient, binding: QueueBinding | null, running: boolean) {
  return useQuery({ queryKey: [...v2QueryKeys.thread(binding?.threadID ?? ""), "queued-messages", client.baseURL,
    binding?.runID ?? "", binding?.sessionID ?? "", binding?.workspaceID ?? ""],
    queryFn: ({ signal }) => {
      if (!binding) throw new Error("尚未确认队列所属的任务。");
      return readQueuedMessages(client, binding, signal);
    }, enabled: Boolean(binding), retry: false,
    refetchInterval: (current) => running && !current.state.error ? 2000 : false });
}

export function V2QueuedMessages(props: QueueBinding & { client: APIClient; running: boolean; canPromote?: boolean }) {
  // A late response belongs to the captured task and cannot replace another editor.
  return <QueuePanel key={JSON.stringify([props.client.baseURL, props.threadID, props.runID, props.sessionID, props.workspaceID])} {...props} />;
}
function QueuePanel({ client, running, canPromote = false, ...binding }: QueueBinding & { client: APIClient; running: boolean; canPromote?: boolean }) {
  const queryClient = useQueryClient();
  const store = useV2RecoveryStore();
  const document = useMemo(() => store ? getV2DraftDocument(store) : null, [store]);
  const [, refresh] = useReducer((count: number) => count + 1, 0);
  const [noticeState, setNoticeState] = useState({ text: "", successful: false });
  const notice = noticeState.text;
  const setNotice = (text: string, successful = false) => setNoticeState({ text, successful });
  const [busy, setBusy] = useState<string[]>([]);
  const [collapsed, setCollapsed] = useState(false);
  const [details, setDetails] = useState<{ message: QueuedMessage; trigger: HTMLElement | null } | null>(null);
  const detailsID = useId();
  const listID = useId();
  const toggleRef = useRef<HTMLButtonElement>(null);
  const query = useV2QueuedMessagesQuery(client, binding, running);
  const bindingKey = JSON.stringify(binding);
  useEffect(() => {
    const disconnect = [threadQueueEditPrefix(binding), threadQueueOperationPrefix(binding), "queue-result:", "queue-editor-closed:"].map((prefix) => store?.subscribePrefix?.(prefix, refresh));
    return () => disconnect.forEach((stop) => stop?.());
  }, [store, bindingKey]);
  let edits: QueueEdit[] = [], operations: QueueOperation[] = [], recoveryError = "";
  try {
    if (store && document) {
      edits = readQueueEdits(store, binding, true).filter((edit) => {
        const state = document.read(queueEditScope(edit));
        return queueEditVisible(store, edit, state);
      });
      operations = readQueueOperations(store, binding, true);
    }
  } catch (error) { recoveryError = explanation(error); }
  const completedEmpty = query.isSuccess && !query.isFetching && query.data.items.length === 0 && !running &&
    !edits.length && !operations.length && !busy.length && !recoveryError;
  useEffect(() => {
    if (completedEmpty) setNoticeState((current) => current.successful ? { text: "", successful: false } : current);
  }, [completedEmpty, noticeState.successful]);
  const invalidate = async () => { await queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(binding.threadID) }); };
  const startEdit = (message: QueuedMessage) => {
    try {
      if (!store || !document) throw new Error("当前连接无法保存编辑稿，请先恢复本机存储连接。");
      const edit: QueueEdit = { ...binding, version: "queue_edit.v1", message };
      const key = queueEditKey(edit), scope = queueEditScope(edit);
      store.assertReadable(key);
      if (store.read<unknown>(key, null) === null) store.write(key, edit);
      const state = document.read(scope);
      if (!state.ref || !queueEditVisible(store, edit, state)) document.update(scope, empty(message.content), state.ref);
      reopenQueueEdit(store, edit);
      refresh(); setNotice("");
    } catch (error) { setNotice(explanation(error)); }
  };
  const finish = async (operation: QueueOperation) => {
    if (!store || !document) return;
    if (operation.kind === "revise" && operation.draftRef) {
      const edit = readQueueEdits(store, operation).find((candidate) => candidate.message.id === operation.messageID && candidate.message.revision === operation.expectedRevision);
      if (!edit) throw new Error("修改已保存，但原编辑稿记录暂不可读，请核对后再关闭。");
      closeQueueEdit(store, document, edit, operation.draftRef);
    }
    settleQueueOperation(store, operation, "applied"); refresh();
    setNotice(operation.kind === "revise" ? "修改已保存。" : operation.kind === "promote"
      ? "已转为引导。" : "消息已撤回。", true);
    await invalidate();
  };
  const finishRejectedPromotion = async (operation: QueueOperation) => {
    if (!store) return;
    settleQueueOperation(store, operation, "obsolete"); refresh();
    setNotice("这次引导未生效，原消息保留，可重新引导。");
    await invalidate();
  };
  const observeOperation = async (operation: QueueOperation): Promise<boolean> => {
    if (!store) return false;
    if (operation.kind === "promote") {
      const result = await inspectQueuePromotion(client, promotionInput(operation));
      if (result.state === "sealed") { await finish(operation); return true; }
      if (result.state === "rejected") { await finishRejectedPromotion(operation); return true; }
      // Local execution telemetry cannot prove whether another API instance
      // will still commit the captured request. Only durable source changes do.
      if (result.message.status !== "pending" || result.message.revision !== operation.expectedRevision) {
        settleQueueOperation(store, operation, "obsolete"); refresh();
        setNotice("原消息已变化或已处理，这次引导不会再执行。历史记录和本地编辑稿已保留。");
        await invalidate(); return true;
      }
      return false;
    }
    const result = operation.kind === "revise" ? await inspectQueueRevision(client, operation) : await inspectQueueCancellation(client, operation);
    if (result.state === "sealed") { await finish(operation); return true; }
    const obsolete = result.message.status !== "pending" || operation.kind === "revise" && result.message.revision > operation.expectedRevision;
    if (obsolete) {
      settleQueueOperation(store, operation, "obsolete"); refresh();
      setNotice(operation.kind === "revise" ? "原消息版本已变化或已处理，这次原版本修改不会再执行。编辑稿已保留。" : "消息已被其他操作撤回或已交给模型，这次撤回不会再执行。");
      await invalidate(); return true;
    }
    return false;
  };
  const perform = async (operation: QueueOperation, observe = false) => {
    if (!store || busy.includes(operation.messageID)) return;
    setBusy((current) => [...current, operation.messageID]); setNotice("");
    let resolved = false;
    try {
      if (observe) {
        if (!await observeOperation(operation)) setNotice(operation.kind === "promote"
          ? "尚未查到这次引导的最终记录。原操作已保留，可以继续核对或重试原引导。"
          : "尚未查到这次操作的最终记录。编辑稿已保留，可以继续核对或重试原操作。");
        return;
      }
      if (operation.kind === "revise") {
        await reviseQueuedMessage(client, operation);
      } else if (operation.kind === "promote") {
        const result = await promoteQueuedMessage(client, promotionInput(operation));
        if (result.rejected === true) { resolved = true; await finishRejectedPromotion(operation); return; }
      } else {
        const receipt = await client.cancelSessionSteering(operation.sessionID, operation.messageID,
          { version: "session_steering_cancellation.v1", reason: "用户从待处理消息列表撤回" }, operation.operationKey);
        if (receipt.run_id !== operation.runID) throw new Error("撤回结果的任务来源不同，请核对原消息。");
      }
      resolved = true; await finish(operation);
    } catch (error) {
      if (!resolved && !observe && operation.kind === "revise" && await provesQueueRevisionUnchanged(error, operation)) {
        try {
          settleQueueOperation(store, operation, "unchanged"); refresh();
          setNotice("规范化后的正文与当前消息相同，未产生修订。编辑稿已保留，可继续修改。");
        } catch (failure) { setNotice(`结果记录暂未保存，原操作和编辑稿已保留。${explanation(failure)}`); }
        return;
      }
      if (!resolved && !observe && knownRejection(error)) {
        // An HTTP refusal only describes this transport attempt. Another
        // window may already have committed the same immutable operation.
        try { if (await observeOperation(operation)) return; } catch { /* Keep the original journal and draft. */ }
      }
      setNotice(`${operation.kind === "revise" ? "保存" : operation.kind === "promote" ? "引导" : "撤回"}结果待确认。${explanation(error)}`);
    } finally { setBusy((current) => current.filter((id) => id !== operation.messageID)); refresh(); }
  };
  const submit = (operation: QueueOperation) => {
    try {
      if (!store) throw new Error("当前连接无法可靠保存操作记录。");
      if (readQueueOperations(store, binding, true).some((pending) => pending.messageID === operation.messageID)) throw new Error("请先确认这条消息的上次操作结果。");
      saveQueueOperation(store, operation); refresh(); void perform(operation);
    } catch (error) { setNotice(explanation(error)); }
  };
  const items = query.data?.items ?? [];
  const recoveredIDs = new Set(edits.map((edit) => edit.message.id));
  const promotionUnavailable = (message: QueuedMessage): string => {
    if (message.images.length || message.attachments.length) return "当前只支持文字引导；图片与附件保留在下一轮消息中。";
    if (message.delivery_mode === "steer") return "这条消息已经用于更新当前任务。";
    if (message.prepared) return "消息正在处理，无法转为引导。";
    if (!message.content.trim()) return "当前只支持将文字消息转为引导。";
    if (!client.hasSessionSteeringControl || !message.can_edit || !message.can_cancel) return "当前连接或消息状态不允许引导。";
    if (!store || recoveryError) return "本机操作记录暂不可用，请先恢复存储连接。";
    if (recoveredIDs.has(message.id)) return "请先保存或取消这条消息的本地编辑。";
    if (busy.includes(message.id) || operations.some((operation) => operation.messageID === message.id)) return "请先核对这条消息的上次操作结果。";
    if (!query.isSuccess || !query.data.current_attempt_id || !query.data.execution_id) return "尚未确认当前执行身份，请刷新队列后重试。";
    if (!canPromote) return "当前执行暂时无法接受引导，请刷新状态后重试。";
    return "";
  };
  const startPromotion = (message: QueuedMessage) => {
    const reason = promotionUnavailable(message);
    if (reason) { setNotice(reason); return; }
    submit({ ...binding, version: "queue_operation.v1", kind: "promote", operationKey: crypto.randomUUID(),
      messageID: message.id, expectedRevision: message.revision, oldSHA256: message.content_sha256, content: "",
      expectedAttemptID: query.data!.current_attempt_id, expectedExecutionID: query.data!.execution_id });
  };
  if (!query.isError && !items.length && !edits.length && !operations.length && !notice && !recoveryError && !details) return null;
  return <section className="v2-queued-messages" aria-label="待处理消息">
    <header>
      <button className="v2-queue-toggle" type="button" ref={toggleRef} aria-expanded={!collapsed} aria-controls={listID}
        title={collapsed ? "展开待处理消息" : "收起待处理消息"} onClick={() => setCollapsed((value) => !value)}>
        <span>待处理 {query.data?.pending ?? "—"} 条{Boolean(query.data?.prepared) && ` · 正在处理 ${query.data?.prepared} 条`}</span>
        {collapsed ? <ChevronDown size={14} aria-hidden="true" /> : <ChevronUp size={14} aria-hidden="true" />}
      </button>
      <button className="v2-queue-icon" type="button" aria-label="刷新列表" title="刷新列表"
        onClick={() => void query.refetch()} disabled={query.isFetching}><RefreshCw size={13} aria-hidden="true" /></button>
    </header>
    {query.isError && <p role="alert">暂时无法读取完整队列。已有消息仍可在对话记录中查看。{explanation(query.error)}</p>}
    {recoveryError && <p role="alert">{recoveryError}</p>}
    {notice && <p role="status">{notice}</p>}
    <ol id={listID} hidden={collapsed}>{!collapsed && items.map((message) => <V2QueuedMessageRow key={message.id}
      client={client} message={message} detailsID={detailsID} detailsOpen={details?.message.id === message.id}
      onDetails={(trigger) => setDetails({ message, trigger })}
      promotionUnavailable={promotionUnavailable(message)} onPromote={() => startPromotion(message)}
      editDisabled={!message.can_edit || !client.hasSessionSteeringControl || !store || !!recoveryError ||
        recoveredIDs.has(message.id) || busy.includes(message.id) || operations.some((operation) => operation.messageID === message.id)}
      cancelDisabled={!message.can_cancel || !client.hasSessionSteeringControl || !store || !!recoveryError ||
        busy.includes(message.id) || operations.some((operation) => operation.messageID === message.id)}
      onEdit={() => startEdit(message)} onCollapse={() => { setCollapsed(true); toggleRef.current?.focus(); }}
      onCancel={() => submit({ ...binding,
          version: "queue_operation.v1", kind: "cancel", operationKey: crypto.randomUUID(), messageID: message.id,
          expectedRevision: message.revision, oldSHA256: message.content_sha256, content: "" })} />)}</ol>
    {/* Recovery controls stay reachable even when the queue preview is collapsed. */}
    {document && store && edits.map((edit) => <QueueEditor key={queueEditKey(edit)} edit={edit} document={document} store={store}
      client={client} current={edit.runID === binding.runID && edit.sessionID === binding.sessionID ? items.find((message) => message.id === edit.message.id) : undefined} refresh={refresh}
      previousRun={edit.runID !== binding.runID || edit.sessionID !== binding.sessionID}
      queueKnown={query.isSuccess} locked={!!recoveryError || !query.isSuccess || operations.some((operation) => operation.messageID === edit.message.id) || busy.includes(edit.message.id)}
      submit={submit} startEdit={startEdit} />)}
    {operations.map((operation) => <div className="v2-queued-message-pending" key={operation.operationKey}>
      <p>{operation.runID !== binding.runID && "上轮操作 · "}{operation.kind === "revise" ? "修改保存结果待确认" : operation.kind === "promote" ? "引导结果待确认" : "撤回结果待确认"} · 消息 {operation.messageID}</p>
      <button type="button" disabled={busy.includes(operation.messageID)} onClick={() => void perform(operation, true)}>{operation.kind === "revise" ? "核对保存结果" : operation.kind === "promote" ? "核对引导结果" : "核对撤回结果"}</button>
      <button type="button" disabled={busy.includes(operation.messageID) || !client.hasSessionSteeringControl}
        onClick={() => void perform(operation)}>{operation.kind === "revise" ? "重试原保存" : operation.kind === "promote" ? "重试原引导" : "继续撤回"}</button>
    </div>)}
    {details && <V2QueueMessageDetails client={client} snapshot={details.message} detailsID={detailsID}
      current={query.isSuccess ? items.find((message) => message.id === details.message.id) : undefined} queueKnown={query.isSuccess}
      trigger={details.trigger} fallbackFocus={toggleRef} onClose={() => setDetails(null)} />}
  </section>;
}

function QueueEditor({ edit, current, client, document, store, locked, queueKnown, previousRun, refresh, submit, startEdit }: {
  edit: QueueEdit; current?: QueuedMessage; client: APIClient; document: DraftDocument; store: V2RecoveryStore;
  locked: boolean; queueKnown: boolean; previousRun: boolean; refresh: () => void; submit: (operation: QueueOperation) => void; startEdit: (message: QueuedMessage) => void;
}) {
  const scope = useMemo(() => queueEditScope(edit), [edit.threadID, edit.runID, edit.sessionID, edit.message.id, edit.message.revision]);
  useEffect(() => document.subscribe(scope, refresh), [document, scope, refresh]);
  const state = document.read(scope);
  const [error, setError] = useState("");
  const stale = current?.revision !== edit.message.revision;
  const bytes = new TextEncoder().encode(state.snapshot.text.trim()).byteLength;
  const eligible = !!current?.can_edit && client.hasSessionSteeringControl && !stale && !locked && !state.conflict && !state.error && state.persisted &&
    state.snapshot.text.trim() !== edit.message.content && bytes <= 16384 && (bytes > 0 || edit.message.images.length > 0 || edit.message.attachments.length > 0);
  const save = () => {
    try {
      if (!eligible) return;
      const latest = document.read(scope);
      if (latest.conflict || latest.error || !latest.ref || latest.ref.branchID !== state.ref?.branchID || latest.ref.seq !== state.ref?.seq) throw new Error("编辑稿已变化，请核对后再保存。");
      submit({ ...edit, version: "queue_operation.v1", kind: "revise", messageID: edit.message.id, expectedRevision: edit.message.revision,
        oldSHA256: edit.message.content_sha256, content: latest.snapshot.text.trim(), draftRef: latest.ref, operationKey: crypto.randomUUID() });
    } catch (failure) { setError(explanation(failure)); }
  };
  return <div className="v2-queue-editor">
    {previousRun && <p>上轮编辑稿 · 原执行 {edit.runID}</p>}
    <label>编辑消息 {edit.message.sequence}<textarea aria-label={`编辑消息 ${edit.message.sequence}`} value={state.snapshot.text} rows={4}
      onChange={(event) => { document.update(scope, { text: event.target.value }, state.ref); refresh(); }} /></label>
    <V2DraftConflict state={state} client={client} workspaceID={edit.workspaceID} onResolve={(token, ref) => { document.resolve(scope, token, ref); refresh(); }} />
    {(!queueKnown || !current || current.prepared || stale) && <p role="status">{!queueKnown ? "尚未确认最新队列状态。" : !current ? "这条消息已离开待处理队列。" : current.prepared ? "这条消息正在处理。" : "这条消息已在其他窗口更新。"}本地编辑稿仍保留，可选中文字复制。</p>}
    {stale && current && <details><summary>查看当前排队正文</summary><pre>{current.content}</pre></details>}
    {bytes > 16384 && <p role="alert">正文超过 16 KiB，请缩短后保存。</p>}
    {error && <p role="alert">{error}</p>}
    <div className="v2-queued-message-actions"><button type="button" disabled={!eligible} onClick={save}>保存修改</button>
      <button type="button" disabled={locked || state.conflict || !state.ref} onClick={() => {
        try { if (state.ref) closeQueueEdit(store, document, edit, state.ref); refresh(); } catch (failure) { setError(explanation(failure)); }
      }}>取消编辑</button>
      {stale && current?.can_edit && <button type="button" disabled={locked || state.conflict || !state.ref} onClick={() => {
        try { if (state.ref) closeQueueEdit(store, document, edit, state.ref); startEdit(current); refresh(); } catch (failure) { setError(explanation(failure)); }
      }}>载入最新正文，替换编辑稿</button>}
    </div>
  </div>;
}
