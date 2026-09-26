import { useEffect, useMemo, useReducer, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { APIRequestError, type CyberAgentClient } from "../../api/client";
import { inspectQueueCancellation, inspectQueueRevision, provesQueueRevisionUnchanged, readQueuedMessages, reviseQueuedMessage, type QueueBinding, type QueuedMessage } from "../../api/queued-messages";
import { getV2DraftDocument } from "../draft-context";
import type { DraftDocument } from "../draft-document";
import { useV2RecoveryStore, type V2RecoveryStore } from "../recovery-storage";
import { closeQueueEdit, queueEditVisible, reopenQueueEdit, queueEditKey, threadQueueEditPrefix, queueEditScope, threadQueueOperationPrefix, readQueueEdits, readQueueOperations,
  saveQueueOperation, settleQueueOperation, type QueueEdit, type QueueOperation } from "../queue-recovery";
import { v2QueryKeys } from "../query-keys";
import { V2DraftConflict } from "./draft-conflict";
import { V2ImagePreview } from "./image-input";
import { V2FileAttachments } from "./file-input";
import "./queued-messages.css";

const explanation = (error: unknown) => error instanceof Error ? error.message : "操作暂未完成，内容已保留。";
const knownRejection = (error: unknown) => error instanceof APIRequestError && [400, 401, 403, 404, 409, 412, 413, 422].includes(error.status);
const empty = (text = "") => ({ text, files: [], images: [] });

export function V2QueuedMessages(props: QueueBinding & { client: CyberAgentClient; running: boolean }) {
  // A late response belongs to the captured task and cannot replace another editor.
  return <QueuePanel key={JSON.stringify([props.client.baseURL, props.threadID, props.runID, props.sessionID, props.workspaceID])} {...props} />;
}
function QueuePanel({ client, running, ...binding }: QueueBinding & { client: CyberAgentClient; running: boolean }) {
  const queryClient = useQueryClient();
  const store = useV2RecoveryStore();
  const document = useMemo(() => store ? getV2DraftDocument(store) : null, [store]);
  const [, refresh] = useReducer((count: number) => count + 1, 0);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState<string[]>([]);
  const query = useQuery({ queryKey: [...v2QueryKeys.thread(binding.threadID), "queued-messages", client.baseURL, binding.runID, binding.sessionID],
    queryFn: ({ signal }) => readQueuedMessages(client, binding, signal), retry: false,
    refetchInterval: (current) => running && !current.state.error ? 2000 : false });
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
    setNotice(operation.kind === "revise" ? "修改已保存。" : "消息已撤回。");
    await invalidate();
  };
  const observeOperation = async (operation: QueueOperation): Promise<boolean> => {
    if (!store) return false;
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
    let applied = false;
    try {
      if (observe) {
        if (!await observeOperation(operation)) setNotice("尚未查到这次操作的最终记录。编辑稿已保留，可以继续核对或重试原操作。");
        return;
      }
      if (operation.kind === "revise") {
        await reviseQueuedMessage(client, operation);
      } else {
        const receipt = await client.cancelSessionSteering(operation.sessionID, operation.messageID,
          { version: "session_steering_cancellation.v1", reason: "用户从待处理消息列表撤回" }, operation.operationKey);
        if (receipt.run_id !== operation.runID) throw new Error("撤回结果的任务来源不同，请核对原消息。");
      }
      applied = true; await finish(operation);
    } catch (error) {
      if (!applied && !observe && operation.kind === "revise" && await provesQueueRevisionUnchanged(error, operation)) {
        try {
          settleQueueOperation(store, operation, "unchanged"); refresh();
          setNotice("规范化后的正文与当前消息相同，未产生修订。编辑稿已保留，可继续修改。");
        } catch (failure) { setNotice(`结果记录暂未保存，原操作和编辑稿已保留。${explanation(failure)}`); }
        return;
      }
      if (!applied && !observe && knownRejection(error)) {
        // An HTTP refusal only describes this transport attempt. Another
        // window may already have committed the same immutable operation.
        try { if (await observeOperation(operation)) return; } catch { /* Keep the original journal and draft. */ }
      }
      setNotice(`${operation.kind === "revise" ? "保存" : "撤回"}结果待确认。${explanation(error)}`);
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
  if (!query.isError && !items.length && !edits.length && !operations.length && !notice && !recoveryError) return null;
  return <section className="v2-queued-messages" aria-label="待处理消息">
    <header><strong>待处理 {query.data?.pending ?? "—"} 条{Boolean(query.data?.prepared) && ` · 正在处理 ${query.data?.prepared} 条`}</strong>
      <button type="button" onClick={() => void query.refetch()} disabled={query.isFetching}>刷新列表</button></header>
    {query.isError && <p role="alert">暂时无法读取完整队列。已有消息仍可在对话记录中查看。{explanation(query.error)}</p>}
    {recoveryError && <p role="alert">{recoveryError}</p>}
    {notice && <p role="status">{notice}</p>}
    <ol>{items.map((message) => <li key={message.id}>
      <div className="v2-queued-message-heading"><strong>消息 {message.sequence} · {message.delivery_mode === "steer" ? "更新当前任务" : "下一轮处理"}</strong><span>{message.prepared ? "正在处理，无法修改或撤回" : "等待处理"}</span></div>
      <details><summary><span className="v2-queued-message-preview">{message.content || "附件消息"}</span><span>查看全文</span></summary>
        <pre tabIndex={0}>{message.content || "（没有文字）"}</pre>
        {message.content_redacted && <p>正文包含已隐藏内容。编辑时请填写完整的新正文。</p>}
      </details>
      <V2ImagePreview client={client} images={message.images} /><V2FileAttachments client={client} attachments={message.attachments} />
      <div className="v2-queued-message-actions">
        <button type="button" disabled={!message.can_edit || !client.hasSessionSteeringControl || !store || !!recoveryError ||
          recoveredIDs.has(message.id) || busy.includes(message.id) || operations.some((operation) => operation.messageID === message.id)} onClick={() => startEdit(message)}>编辑</button>
        <button type="button" disabled={!message.can_cancel || !client.hasSessionSteeringControl || !store || !!recoveryError ||
          busy.includes(message.id) || operations.some((operation) => operation.messageID === message.id)} onClick={() => submit({ ...binding,
          version: "queue_operation.v1", kind: "cancel", operationKey: crypto.randomUUID(), messageID: message.id,
          expectedRevision: message.revision, oldSHA256: message.content_sha256, content: "" })}>撤回</button>
      </div>
    </li>)}</ol>
    {document && store && edits.map((edit) => <QueueEditor key={queueEditKey(edit)} edit={edit} document={document} store={store}
      client={client} current={edit.runID === binding.runID && edit.sessionID === binding.sessionID ? items.find((message) => message.id === edit.message.id) : undefined} refresh={refresh}
      previousRun={edit.runID !== binding.runID || edit.sessionID !== binding.sessionID}
      queueKnown={query.isSuccess} locked={!!recoveryError || !query.isSuccess || operations.some((operation) => operation.messageID === edit.message.id) || busy.includes(edit.message.id)}
      submit={submit} startEdit={startEdit} />)}
    {operations.map((operation) => <div className="v2-queued-message-pending" key={operation.operationKey}>
      <p>{operation.runID !== binding.runID && "上轮操作 · "}{operation.kind === "revise" ? "修改保存结果待确认" : "撤回结果待确认"} · 消息 {operation.messageID}</p>
      <button type="button" disabled={busy.includes(operation.messageID)} onClick={() => void perform(operation, true)}>{operation.kind === "revise" ? "核对保存结果" : "核对撤回结果"}</button>
      <button type="button" disabled={busy.includes(operation.messageID) || !client.hasSessionSteeringControl}
        onClick={() => void perform(operation)}>{operation.kind === "revise" ? "重试原保存" : "继续撤回"}</button>
    </div>)}
  </section>;
}

function QueueEditor({ edit, current, client, document, store, locked, queueKnown, previousRun, refresh, submit, startEdit }: {
  edit: QueueEdit; current?: QueuedMessage; client: CyberAgentClient; document: DraftDocument; store: V2RecoveryStore;
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
