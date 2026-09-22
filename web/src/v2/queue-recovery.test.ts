import { createElement, type ReactNode } from "react";
import { cleanup, renderHook } from "@testing-library/react";
import { DraftDocument } from "./draft-document";
import { V2RecoveryProvider, useV2RecoveryStore, type V2RecoveryStore } from "./recovery-storage";
import { closeQueueEdit, queueEditVisible, queueEditScope, readQueueOperations, saveQueueOperation, type QueueEdit, type QueueOperation } from "./queue-recovery";
const edit: QueueEdit = { version: "queue_edit.v1", threadID: "thread-a", runID: "run-a", sessionID: "session-a", workspaceID: "workspace-a",
  message: { id: "message-a", sequence: 1, status: "pending", prepared: false, content: "原文", content_sha256: "a".repeat(64),
    content_redacted: false, revision: 0, created_at: "2026-09-22T00:00:00Z", images: [], attachments: [], can_edit: true, can_cancel: true } };
const scope = queueEditScope(edit);
function store(): V2RecoveryStore {
  return renderHook(() => useV2RecoveryStore(), { wrapper: ({ children }: { children: ReactNode }) =>
    createElement(V2RecoveryProvider, { client: { baseURL: "/api/v1" }, scopeID: "queue-multitab", children }) }).result.current!;
}
beforeEach(() => localStorage.clear());
afterEach(cleanup);
it("does not clear another window's later queue edit or the ordinary conversation draft", () => {
  const firstStore = store(), secondStore = store(), first = new DraftDocument(firstStore), second = new DraftDocument(secondStore);
  const sent = first.update(scope, { text: "待保存版本" }, null);
  const ordinaryScope = { workspaceID: edit.workspaceID, key: `thread:${edit.threadID}` };
  first.update(ordinaryScope, { text: "主输入框正文" }, null);
  const seen = second.read(scope); second.update(scope, { text: "另一个窗口的新稿" }, seen.ref);
  closeQueueEdit(firstStore, first, edit, sent.ref!);
  const recovered = new DraftDocument(store()).read(scope);
  expect(recovered.snapshot.text).toBe("另一个窗口的新稿");
  expect(queueEditVisible(firstStore, edit, recovered)).toBe(true);
  expect(first.read(ordinaryScope).snapshot.text).toBe("主输入框正文");
});
it("retains concurrent queue drafts as conflicts instead of choosing the last write", () => {
  const firstStore = store(), secondStore = store(), first = new DraftDocument(firstStore), second = new DraftDocument(secondStore);
  first.read(scope); second.read(scope);
  const original = first.update(scope, { text: "窗口甲" }, null);
  second.update(scope, { text: "窗口乙" }, null);
  expect(first.read(scope).conflict).toBe(true);
  closeQueueEdit(firstStore, first, edit, original.ref!);
  const recovered = new DraftDocument(store()).read(scope);
  expect(recovered.heads.some((head) => head.snapshot.text === "窗口乙")).toBe(true);
  expect(queueEditVisible(firstStore, edit, recovered)).toBe(true);
});
it("only closes the captured ref when another window advances during the close read", () => {
  const firstStore = store(), secondStore = store(), first = new DraftDocument(firstStore), second = new DraftDocument(secondStore);
  const original = first.update(scope, { text: "原版本" }, null);
  const read = first.read.bind(first);
  vi.spyOn(first, "read").mockImplementationOnce((requestedScope) => {
    const before = read(requestedScope);
    const next = second.read(scope); second.update(scope, { text: "关闭过程中写入的新版本" }, next.ref);
    return before;
  });
  closeQueueEdit(firstStore, first, edit, original.ref!);
  const recovered = new DraftDocument(store()).read(scope);
  expect(recovered.snapshot.text).toBe("关闭过程中写入的新版本");
  expect(queueEditVisible(firstStore, edit, recovered)).toBe(true);
});
it("persists original operation payloads independently of later editor text and refuses key reuse", () => {
  const storage = store();
  const operation: QueueOperation = { ...edit, version: "queue_operation.v1", kind: "revise", operationKey: "operation-key-one",
    messageID: edit.message.id, expectedRevision: 0, oldSHA256: edit.message.content_sha256, content: "原请求正文", draftRef: { branchID: "branch-a", seq: 1 } };
  saveQueueOperation(storage, operation);
  expect(readQueueOperations(store(), edit)).toEqual([operation]);
  expect(() => saveQueueOperation(storage, { ...operation, content: "别的正文" })).toThrow(/不能覆盖/u);
  expect(readQueueOperations(store(), edit)[0].content).toBe("原请求正文");
});
