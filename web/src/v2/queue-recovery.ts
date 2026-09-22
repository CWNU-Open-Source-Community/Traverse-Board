import { queueIdentity, validQueuedMessage, type QueueBinding, type QueuedMessage } from "../api/queued-messages";
import type { DraftDocument, DraftRef, DraftScope, DraftState } from "./draft-document";
import type { V2RecoveryStore } from "./recovery-storage";

export interface QueueEdit extends QueueBinding { version: "queue_edit.v1"; message: QueuedMessage }
export interface QueueOperation extends QueueBinding {
  version: "queue_operation.v1"; kind: "revise" | "cancel"; operationKey: string;
  messageID: string; expectedRevision: number; oldSHA256: string; content: string; draftRef?: DraftRef;
}
export const queueEditScope = (edit: QueueEdit): DraftScope => ({ workspaceID: edit.workspaceID,
  key: `queue-edit:${edit.threadID}:${edit.runID}:${edit.sessionID}:${edit.message.id}:${edit.message.revision}` });
const part = (value: QueueBinding) => JSON.stringify([value.threadID, value.runID, value.sessionID, value.workspaceID]);
export const queueEditPrefix = (binding: QueueBinding) => `queue-editor:${part(binding)}:`;
export const threadQueueEditPrefix = (binding: QueueBinding) => `queue-editor:[${JSON.stringify(binding.threadID)},`;
export const queueEditKey = (edit: QueueEdit) => `${queueEditPrefix(edit)}${edit.message.id}:${edit.message.revision}`;
const editClosedKey = (edit: QueueEdit) => `queue-editor-closed:${queueEditKey(edit)}`;
export function queueEditVisible(store: V2RecoveryStore, edit: QueueEdit, state: DraftState) {
  const closed = store.read<unknown>(editClosedKey(edit), null); store.assertReadable(editClosedKey(edit));
  if (closed !== null && (!object(closed) || !queueIdentity(closed.branchID) || !Number.isSafeInteger(closed.seq) || Number(closed.seq) < 1)) return invalid(store, editClosedKey(edit));
  return state.conflict || !!state.error || !state.ref || !object(closed) ||
    closed.branchID !== state.ref.branchID || closed.seq !== state.ref.seq;
}
export function reopenQueueEdit(store: V2RecoveryStore, edit: QueueEdit) { store.write(editClosedKey(edit), null); }
export function closeQueueEdit(store: V2RecoveryStore, document: DraftDocument, edit: QueueEdit, ref: DraftRef) {
  const state = document.read(queueEditScope(edit));
  if (!state.persisted || state.error) throw new Error(state.error ?? "编辑稿尚未保存，请保留本页。");
  // Closing is a projection of the captured version, never a write to draft
  // content or to whichever newer head another window has written meanwhile.
  store.write(editClosedKey(edit), ref);
}
export const queueOperationPrefix = (binding: QueueBinding) => `queue-operation:${part(binding)}:`;
export const threadQueueOperationPrefix = (binding: QueueBinding) => `queue-operation:[${JSON.stringify(binding.threadID)},`;
export const queueOperationKey = (operation: QueueOperation) => `${queueOperationPrefix(operation)}${operation.operationKey}`;
const resultKey = (operation: QueueOperation) => `queue-result:${part(operation)}:${operation.operationKey}`;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);
const bindingMatches = (value: Record<string, unknown>, binding: QueueBinding) =>
  ["threadID", "runID", "sessionID", "workspaceID"].every((key) => value[key] === binding[key as keyof QueueBinding]);
const threadBindingMatches = (value: Record<string, unknown>, binding: QueueBinding) => value.threadID === binding.threadID &&
  value.workspaceID === binding.workspaceID && queueIdentity(value.runID) && queueIdentity(value.sessionID);
function invalid(store: V2RecoveryStore, key: string): never {
  store.reject(key); throw new Error("排队消息的本机恢复记录异常，原记录已保留；请保留当前编辑内容。");
}
export function readQueueEdits(store: V2RecoveryStore, binding: QueueBinding, previousRuns = false): QueueEdit[] {
  const prefix = previousRuns ? threadQueueEditPrefix(binding) : queueEditPrefix(binding);
  const entries = store.entries<unknown>(prefix); store.assertReadable(prefix);
  return entries.map(([key, value]) => {
    if (!object(value) || value.version !== "queue_edit.v1" || !(previousRuns ? threadBindingMatches(value, binding) : bindingMatches(value, binding)) ||
      !validQueuedMessage(value.message, binding.workspaceID)) return invalid(store, key);
    const edit = value as unknown as QueueEdit;
    if (key !== queueEditKey(edit)) return invalid(store, key);
    return edit;
  });
}
export function readQueueOperations(store: V2RecoveryStore, binding: QueueBinding, previousRuns = false): QueueOperation[] {
  const prefix = previousRuns ? threadQueueOperationPrefix(binding) : queueOperationPrefix(binding);
  const entries = store.entries<unknown>(prefix); store.assertReadable(prefix);
  return entries.flatMap(([key, value]) => {
    if (!object(value) || value.version !== "queue_operation.v1" || !(previousRuns ? threadBindingMatches(value, binding) : bindingMatches(value, binding)) ||
      !["revise", "cancel"].includes(String(value.kind)) || !queueIdentity(value.operationKey) || !queueIdentity(value.messageID) ||
      !Number.isSafeInteger(value.expectedRevision) || Number(value.expectedRevision) < 0 || typeof value.oldSHA256 !== "string" ||
      !/^[a-f0-9]{64}$/u.test(value.oldSHA256) || typeof value.content !== "string" ||
      new TextEncoder().encode(value.content).byteLength > 16384 || value.kind === "revise" &&
      (!object(value.draftRef) || !queueIdentity(value.draftRef.branchID) || !Number.isSafeInteger(value.draftRef.seq) || Number(value.draftRef.seq) < 1)) return invalid(store, key);
    const operation = value as unknown as QueueOperation;
    if (key !== queueOperationKey(operation)) return invalid(store, key);
    const result = store.read<unknown>(resultKey(operation), null); store.assertReadable(resultKey(operation));
    if (result !== null && result !== "applied" && result !== "obsolete" && result !== "unchanged") return invalid(store, resultKey(operation));
    return result === null ? [operation] : [];
  });
}
export function saveQueueOperation(store: V2RecoveryStore, operation: QueueOperation) {
  store.assertReadable(queueOperationPrefix(operation));
  const key = queueOperationKey(operation);
  const current = store.read<unknown>(key, null); store.assertReadable(key);
  if (current !== null && JSON.stringify(current) !== JSON.stringify(operation)) throw new Error("原操作内容已变化，不能覆盖后重发。");
  store.write(key, operation);
}
export function settleQueueOperation(store: V2RecoveryStore, operation: QueueOperation, result: "applied" | "obsolete" | "unchanged") {
  store.write(resultKey(operation), result);
}
