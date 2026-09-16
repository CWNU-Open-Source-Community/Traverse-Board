import { useEffect, useMemo, useReducer } from "react";
import { DraftDocument, draftSnapshotFingerprint, validDraftSnapshot, type DraftScope, type DraftSnapshot, type DraftState } from "./draft-document";
import { useV2RecoveryStore, type V2RecoveryStore } from "./recovery-storage";
import type { V2DraftVersion } from "./draft-version";

const documents = new WeakMap<V2RecoveryStore, DraftDocument>();
const migrations = new WeakMap<DraftDocument, Map<string, string | null>>();
export function getV2DraftDocument(store: V2RecoveryStore): DraftDocument {
  let document = documents.get(store);
  if (!document) { document = new DraftDocument(store); documents.set(store, document); }
  return document;
}

export const v2DraftScope = (workspaceID: string, threadID = ""): DraftScope =>
  ({ key: threadID ? `thread:${threadID}` : `new:${workspaceID}`, workspaceID });

export function readV2Draft(document: DraftDocument, store: V2RecoveryStore, scope: DraftScope): DraftState {
  const key = JSON.stringify([scope.key, scope.workspaceID]);
  let initialized = migrations.get(document);
  if (!initialized) { initialized = new Map(); migrations.set(document, initialized); }
  if (initialized.has(key)) {
    const state = document.read(scope);
    const error = initialized.get(key);
    return error ? { ...state, error, persisted: false } : state;
  }
  if (store.entries(`draft-document:${key}:`).length > 0) {
    initialized.set(key, null);
    return document.read(scope);
  }
  const threadID = scope.key.startsWith("thread:") ? scope.key.slice(7) : "";
  const suffix = JSON.stringify([scope.workspaceID, threadID]);
  const legacy = {
    text: store.read<unknown>(`draft:${scope.key}`, ""),
    files: store.read<unknown>(`files:${suffix}`, []),
    images: store.read<unknown>(`images:${suffix}`, []),
  };
  let invalid = false;
  let readError: string | null = null;
  try {
    store.assertReadable(`draft:${scope.key}`);
    store.assertReadable(`files:${suffix}`);
    store.assertReadable(`images:${suffix}`);
  } catch (error) { readError = error instanceof Error ? error.message : "旧草稿暂时无法读取，请保留当前页面。"; }
  if (typeof legacy.text !== "string") { store.reject(`draft:${scope.key}`); legacy.text = ""; invalid = true; }
  if (!validDraftSnapshot({ text: "", files: legacy.files, images: [] }, scope.workspaceID)) {
    store.reject(`files:${suffix}`); legacy.files = []; invalid = true;
  }
  if (!validDraftSnapshot({ text: "", files: [], images: legacy.images }, scope.workspaceID)) {
    store.reject(`images:${suffix}`); legacy.images = []; invalid = true;
  }
  // Legacy records remain untouched. Once a document exists it is the source
  // of truth; the three former keys are never mirrored back into it.
  const state = document.read(scope, validDraftSnapshot(legacy, scope.workspaceID) ? legacy : undefined);
  const error = readError ?? (invalid ? "旧草稿记录格式异常，已保留原记录。请保留当前页面和正文，暂不能发送。" : null);
  initialized.set(key, error);
  return error ? { ...state, error, persisted: false } : state;
}

export function useV2DraftDocument(workspaceID: string, threadID = "") {
  const store = useV2RecoveryStore();
  const document = useMemo(() => store ? getV2DraftDocument(store) : null, [store]);
  const scope = useMemo(() => v2DraftScope(workspaceID, threadID), [workspaceID, threadID]);
  const [, refresh] = useReducer((count: number) => count + 1, 0);
  useEffect(() => {
    if (!document || !workspaceID) return;
    const unsubscribe = document.subscribe(scope, refresh);
    refresh(); // Include writes between render and subscription.
    return unsubscribe;
  }, [document, scope, workspaceID]);
  if (!document || !store || !workspaceID) return null;
  const state = readV2Draft(document, store, scope);
  return { document, scope, state,
    changeText: (text: string, expected?: string) => {
      if (expected !== undefined && state.snapshot.text !== expected) return;
      document.update(scope, { text }, state.ref);
    },
  };
}

export type V2ManagedDraft = NonNullable<ReturnType<typeof useV2DraftDocument>>;
const sameRef = (left: DraftState["ref"], right: DraftState["ref"]) =>
  left?.branchID === right?.branchID && left?.seq === right?.seq;

// Synchronous local check immediately before a fresh request is journaled.
// Replaying an existing request uses its saved payload and version instead.
export function requireV2DraftVersion(draft: V2ManagedDraft, snapshot: DraftSnapshot): V2DraftVersion {
  if (draft.state.error) throw new Error(draft.state.error);
  let current = draft.document.read(draft.scope);
  if (current.conflict || !sameRef(current.ref, draft.state.ref) ||
      draftSnapshotFingerprint(current.snapshot) !== draftSnapshotFingerprint(snapshot)) {
    throw new Error("草稿已在其他窗口更新，请查看当前版本后再发送。");
  }
  if (!current.ref) current = draft.document.update(draft.scope, snapshot, null);
  if (!current.persisted || current.error || !current.ref) {
    throw new Error(current.error ?? "草稿尚未可靠保存，请保留当前页面后重试。");
  }
  return { scope: draft.scope, ref: current.ref };
}

export function assertV2DraftVersion(draft: V2ManagedDraft, version: V2DraftVersion, snapshot: DraftSnapshot): void {
  const current = draft.document.read(version.scope);
  if (version.scope.key !== draft.scope.key || version.scope.workspaceID !== draft.scope.workspaceID ||
      current.conflict || !current.persisted || current.error || !sameRef(current.ref, version.ref) ||
      draftSnapshotFingerprint(current.snapshot) !== draftSnapshotFingerprint(snapshot)) {
    throw new Error("草稿在提交前发生变化，请查看当前版本后再发送。原消息标识和新草稿均已保留。");
  }
}
