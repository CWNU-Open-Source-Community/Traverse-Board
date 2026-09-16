import { createElement, type ReactNode } from "react";
import { cleanup, renderHook } from "@testing-library/react";
import { DraftDocument, type DraftSnapshot, type DraftScope } from "./draft-document";
import { V2RecoveryProvider, useV2RecoveryStore, type V2RecoveryStore } from "./recovery-storage";

const scope: DraftScope = { key: "thread:thread-one", workspaceID: "workspace-one" };
const file = (id: string) => ({ id, path: `${id}.md`, digest: "a".repeat(64), partial: false, redacted: false });
const image = (id: string, workspaceID = scope.workspaceID) => ({ id, workspace_id: workspaceID,
  sha256: (id === "image-a" ? "b" : "c").repeat(64), mime_type: "image/png" as const, byte_size: 48, width: 2, height: 3, name: `${id}.png` });
const snapshot = (text: string, files = [file("file-a")], images = [image("image-a")]): DraftSnapshot => ({ text, files, images });

function store(scopeID = "database-one"): V2RecoveryStore {
  return renderHook(() => useV2RecoveryStore(), { wrapper: ({ children }: { children: ReactNode }) =>
    createElement(V2RecoveryProvider, { client: { baseURL: "/api/v1" }, scopeID, children }) }).result.current!;
}
function documents() {
  const firstStore = store();
  const secondStore = store();
  return { firstStore, secondStore, first: new DraftDocument(firstStore), second: new DraftDocument(secondStore) };
}
function rawBranches() {
  return Object.keys(localStorage).filter((key) => decodeURIComponent(key).includes("draft-document:"))
    .map((key) => ({ key, value: JSON.parse(localStorage.getItem(key)!).value }));
}

beforeEach(() => localStorage.clear());
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("independent single-writer draft branches", () => {
  it("preserves two same-base writes as complete snapshots across independent stores and a refresh", () => {
    const { first, second } = documents();
    const original = snapshot("原始草稿");
    first.read(scope, original);
    second.read(scope, original);
    const left = first.update(scope, snapshot("窗口 A", [file("file-a")], [image("image-a")]), null);
    const right = second.update(scope, snapshot("窗口 B", [file("file-b")], [image("image-b")]), null);
    expect(left.ref?.branchID).not.toBe(right.ref?.branchID);
    const reloaded = new DraftDocument(store()).read(scope, original);
    expect(reloaded.conflict).toBe(true);
    expect(reloaded.persisted).toBe(true);
    expect(reloaded.heads.map(({ snapshot }) => snapshot).sort((a, b) => a.text.localeCompare(b.text))).toEqual([
      snapshot("窗口 A", [file("file-a")], [image("image-a")]), snapshot("窗口 B", [file("file-b")], [image("image-b")]),
    ]);
    expect(rawBranches()).toHaveLength(2);
  });

  it("writes one key per editing epoch and keeps all original legacy keys unchanged", () => {
    const { firstStore, first } = documents();
    firstStore.write("draft:thread:thread-one", "旧稿");
    firstStore.write("files:legacy", [file("file-a")]);
    const before = new Map(Object.keys(localStorage).map((key) => [key, localStorage.getItem(key)]));
    first.read(scope, snapshot("旧稿"));
    const one = first.update(scope, { text: "第一笔" });
    const two = first.update(scope, { text: "第二笔" }, one.ref);
    expect(two.ref).toEqual({ branchID: one.ref!.branchID, seq: 2 });
    expect(rawBranches()).toHaveLength(1);
    before.forEach((value, key) => expect(localStorage.getItem(key)).toBe(value));
  });

  it("notifies scoped subscribers and allows an idle window to adopt the sole full snapshot", () => {
    const { first, second } = documents();
    first.read(scope, snapshot("旧稿"));
    second.read(scope, snapshot("旧稿"));
    const changed = vi.fn();
    const unsubscribe = second.subscribe(scope, changed);
    first.update(scope, snapshot("远端新稿", [file("file-b")], [image("image-b")]));
    const branchKey = rawBranches()[0].key;
    window.dispatchEvent(new StorageEvent("storage", { key: branchKey, newValue: localStorage.getItem(branchKey) }));
    expect(changed).toHaveBeenCalled();
    expect(second.read(scope).snapshot).toEqual(snapshot("远端新稿", [file("file-b")], [image("image-b")]));
    unsubscribe();
    changed.mockClear();
    window.dispatchEvent(new StorageEvent("storage", { key: branchKey }));
    expect(changed).not.toHaveBeenCalled();
  });

  it("retains transitive ancestors while an older writer's next sequence remains a conflict", () => {
    const { first, second } = documents();
    first.read(scope, snapshot("最初"));
    const a = first.update(scope, { text: "A1" });
    const oldCapture = first.capture(scope);
    second.read(scope);
    const b = second.update(scope, { text: "B1 基于 A1" });
    first.read(scope);
    const continued = first.update(scope, { text: "A 继续 B1" }, b.ref);
    expect(continued.conflict).toBe(false);
    expect(continued.heads).toHaveLength(1);
    const record = rawBranches().find(({ value }) => value.branchID === continued.ref!.branchID)!.value;
    expect(record.bases[a.ref!.branchID]).toBe(1);
    expect(record.bases[b.ref!.branchID]).toBe(1);
    first.updateCaptured(oldCapture, { text: "迟到 A2，不知道 B1" });
    const current = second.read(scope);
    expect(current.conflict).toBe(true);
    expect(current.heads.map(({ snapshot }) => snapshot.text)).toEqual(expect.arrayContaining(["A 继续 B1", "迟到 A2，不知道 B1"]));
  });

  it("rejects selection of stale heads without deleting or rewriting either branch", () => {
    const { first, second } = documents();
    first.read(scope, snapshot("base")); second.read(scope, snapshot("base"));
    first.update(scope, { text: "A" });
    const b = second.update(scope, { text: "B" });
    const seen = first.read(scope);
    second.update(scope, { text: "B 最新" }, b.ref);
    const before = [...Object.entries(localStorage)];
    expect(() => first.resolve(scope, seen.headToken, seen.heads[0].ref)).toThrow("版本已经变化");
    expect([...Object.entries(localStorage)]).toEqual(before);
  });

  it("keeps a write arriving between resolution's read and write as a separate conflict", () => {
    const { firstStore, first, second } = documents();
    first.read(scope, snapshot("base")); second.read(scope, snapshot("base"));
    first.update(scope, { text: "A" });
    const b = second.update(scope, { text: "B" });
    const seen = first.read(scope);
    const write = firstStore.write.bind(firstStore);
    vi.spyOn(firstStore, "write").mockImplementation((key, value) => {
      second.update(scope, { text: "B 在确认途中输入" }, b.ref);
      write(key, value);
    });
    first.resolve(scope, seen.headToken, seen.heads.find(({ snapshot }) => snapshot.text === "A")!.ref);
    const refreshed = new DraftDocument(store()).read(scope);
    expect(refreshed.conflict).toBe(true);
    expect(refreshed.heads.map(({ snapshot }) => snapshot.text)).toEqual(expect.arrayContaining(["A", "B 在确认途中输入"]));
    expect(rawBranches()).toHaveLength(3);
  });

  it("keeps late images on the original editing epoch after explicit version selection", () => {
    const { first, second } = documents();
    first.read(scope, snapshot("base", [], [])); second.read(scope, snapshot("base", [], []));
    first.update(scope, snapshot("A 正文", [], []));
    const uploading = first.capture(scope);
    second.update(scope, snapshot("B 正文", [], [image("image-b")]));
    const conflict = first.read(scope);
    const selected = conflict.heads.find(({ snapshot }) => snapshot.text === "B 正文")!;
    const resolved = first.resolve(scope, conflict.headToken, selected.ref);
    const late = first.updateCaptured(uploading, (previous) => ({ ...previous, images: [image("image-a")] }));
    expect(late.ref).toEqual(resolved.ref);
    expect(late.snapshot).toEqual(snapshot("B 正文", [], [image("image-b")]));
    const refreshed = new DraftDocument(store()).read(scope);
    expect(refreshed.conflict).toBe(true);
    expect(refreshed.heads.map(({ snapshot }) => snapshot)).toEqual(expect.arrayContaining([
      snapshot("A 正文", [], [image("image-a")]), snapshot("B 正文", [], [image("image-b")]),
    ]));
  });

  it("settles only the sent exact version after a reload and preserves same text with different images", () => {
    const { first, second } = documents();
    first.read(scope, snapshot("base")); second.read(scope, snapshot("base"));
    const sent = first.update(scope, snapshot("同一正文", [file("file-a")], [image("image-a")]));
    second.update(scope, snapshot("同一正文", [file("file-b")], [image("image-b")]));
    const reloaded = new DraftDocument(store());
    const settled = reloaded.settle(scope, sent.ref!);
    expect(settled.heads.map(({ snapshot }) => snapshot)).toEqual(expect.arrayContaining([
      snapshot("", [], []), snapshot("同一正文", [file("file-b")], [image("image-b")]),
    ]));
    const count = rawBranches().length;
    reloaded.settle(scope, sent.ref!);
    expect(rawBranches()).toHaveLength(count);
  });

  it("does not clear newer edits or attachments when an older submission is accepted", () => {
    const { first } = documents();
    first.read(scope, snapshot("base"));
    const sent = first.update(scope, snapshot("发送正文"));
    const newer = first.update(scope, snapshot("下一条", [file("file-b")], [image("image-b")]), sent.ref);
    expect(new DraftDocument(store()).settle(scope, sent.ref!).snapshot).toEqual(newer.snapshot);
    expect(rawBranches()).toHaveLength(1);
  });

  it("keeps a sender's racing next revision when settlement writes its clearing descendant", () => {
    const { first, secondStore, second } = documents();
    first.read(scope, snapshot("base"));
    const sent = first.update(scope, { text: "已发送" });
    const write = secondStore.write.bind(secondStore);
    vi.spyOn(secondStore, "write").mockImplementation((key, value) => {
      first.update(scope, { text: "下一条" }, sent.ref);
      write(key, value);
    });
    second.settle(scope, sent.ref!);
    expect(new DraftDocument(store()).read(scope).heads.map(({ snapshot }) => snapshot.text)).toContain("下一条");
  });

  it("retains unsaved conflicting snapshots in memory without claiming refresh durability", () => {
    const { first, second } = documents();
    first.read(scope, snapshot("base")); second.read(scope, snapshot("base"));
    first.update(scope, { text: "A 已保存" });
    const storageWrite = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new DOMException("quota", "QuotaExceededError"); });
    const failed = second.update(scope, { text: "B 未保存" });
    expect(failed.persisted).toBe(false);
    expect(failed.error).toBeTruthy();
    expect(failed.heads.map(({ snapshot }) => snapshot.text)).toEqual(expect.arrayContaining(["A 已保存", "B 未保存"]));
    expect(second.read(scope).persisted).toBe(false);
    storageWrite.mockRestore();
    expect(new DraftDocument(store()).read(scope).snapshot.text).toBe("A 已保存");
    expect(second.update(scope, { text: "B 重新保存" }, failed.ref).persisted).toBe(true);
  });

  it("preserves malformed records and isolates their failure to that draft scope", () => {
    const { firstStore, first } = documents();
    const invalidKey = `draft-document:${JSON.stringify([scope.key, scope.workspaceID])}:invalid`;
    firstStore.write(invalidKey, { corrupt: true });
    const original = Object.entries(localStorage).find(([key]) => decodeURIComponent(key).includes(invalidKey))!;
    expect(first.read(scope, snapshot("本窗口保留")).persisted).toBe(false);
    expect(first.update(scope, { text: "仍可编辑" }).error).toBeTruthy();
    expect(localStorage.getItem(original[0])).toBe(original[1]);
    const other = { key: "thread:thread-two", workspaceID: scope.workspaceID };
    first.read(other, snapshot("其他任务"));
    expect(first.update(other, { text: "其他任务正常保存" }).persisted).toBe(true);
  });

  it("isolates backend and workspace identities and rejects foreign-scope images without slicing them", () => {
    const first = new DraftDocument(store("database-one"));
    first.read(scope, snapshot("第一库")); first.update(scope, { text: "第一库保存" });
    expect(new DraftDocument(store("database-two")).read(scope).snapshot.text).toBe("");
    const other = { key: "thread:thread-one", workspaceID: "workspace-two" };
    expect(first.read(other).snapshot.text).toBe("");
    expect(() => first.update(other, { images: [image("image-a")] })).toThrow("草稿版本记录异常");
    const original = first.read(scope);
    expect(() => first.update(scope, { files: Array.from({ length: 5 }, (_, index) => file(`f${index}`)) })).toThrow();
    expect(first.read(scope).ref).toEqual(original.ref);
  });

  it("expires superseded UI references during sustained typing while keeping captured async input usable", () => {
    const { first } = documents();
    first.read(scope, snapshot("", [], []));
    const original = first.update(scope, { text: "第一笔" });
    const captured = first.capture(scope, original.ref);
    let latest = original;
    for (let index = 0; index < 200; index++) latest = first.update(scope, { text: `持续输入 ${"字".repeat(index)}` }, latest.ref);
    expect(() => first.capture(scope, original.ref)).toThrow("版本已经变化");
    expect(first.capture(scope, latest.ref).snapshot).toEqual(latest.snapshot);
    const completed = first.updateCaptured(captured, (current) => ({ ...current, images: [image("image-a")] }));
    expect(completed.snapshot.text).toBe(latest.snapshot.text);
    expect(completed.snapshot.images).toEqual([image("image-a")]);
    expect(completed.conflict).toBe(false);
    expect(new DraftDocument(store()).read(scope).snapshot).toEqual(completed.snapshot);
    expect(rawBranches()).toHaveLength(1);
  });
});
