import { useEffect, useMemo, useReducer, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, File, LoaderCircle, X } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import { maximumFileAttachments, maximumFileBytes, type WorkspaceFileAttachment } from "../../api/file-attachments";
import { v2AttachmentReferenceKey } from "../attachment-keys";
import { useV2DraftDocument } from "../draft-context";
import type { DraftCapture } from "../draft-document";
import { useV2RecoveryStore } from "../recovery-storage";
import "./file-input.css";

export function useV2AttachmentReferences(workspaceID: string, threadID: string) {
  const draft = useV2DraftDocument(workspaceID, threadID);
  const queries = useQueryClient();
  const key = v2AttachmentReferenceKey(workspaceID, threadID);
  const query = useQuery<WorkspaceFileAttachment[]>({ queryKey: key, queryFn: () => [], enabled: false, initialData: [], gcTime: Infinity });
  const update = (change: (files: WorkspaceFileAttachment[]) => WorkspaceFileAttachment[]) => {
    if (draft) draft.document.update(draft.scope, (current) => ({ ...current, attachments: change(current.attachments ?? []) }), draft.state.ref);
    else queries.setQueryData<WorkspaceFileAttachment[]>(key, (current) => change(current ?? []));
  };
  return { attachments: draft ? draft.state.snapshot.attachments ?? [] : query.data, update, draft };
}

const bytesLabel = (bytes: number) => bytes < 1024 ? `${bytes} B` : bytes < 1024 * 1024 ?
  `${(bytes / 1024).toFixed(1)} KiB` : `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
function AttachmentCard({ client, file, onRemove, locked }: {
  client: CyberAgentClient; file: WorkspaceFileAttachment; onRemove?: () => void; locked: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const download = async () => {
    if (busy) return;
    setBusy(true); setError("");
    try {
      const blob = await client.downloadWorkspaceFile(file);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a"); link.href = url; link.download = file.name;
      document.body.append(link); link.click(); link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (failure) { setError(failure instanceof Error ? failure.message : "文件下载失败"); }
    finally { setBusy(false); }
  };
  return <div className="v2-file-attachment">
    <File size={20} aria-hidden="true" />
    <div className="v2-file-attachment-info"><strong title={file.name}>{file.name}</strong>
      <small className="v2-file-attachment-size">{bytesLabel(file.byte_size)}</small>
      {locked && <small>随原消息提交</small>}
      {error && <small role="alert">{error}</small>}
    </div>
    <button type="button" className="v2-composer-icon" aria-label={`下载文件 ${file.name}`} disabled={busy} onClick={() => void download()}>
      {busy ? <LoaderCircle size={16} className="spin" /> : <Download size={16} />}</button>
    {onRemove && <button type="button" className="v2-composer-icon" aria-label={`移除文件 ${file.name}`} disabled={locked} onClick={onRemove}><X size={16} /></button>}
  </div>;
}
export function V2FileAttachments({ client, attachments, onRemove, pendingIDs = [] }: {
  client: CyberAgentClient; attachments: WorkspaceFileAttachment[]; onRemove?: (id: string) => void; pendingIDs?: string[];
}) {
  if (!attachments.length) return null;
  return <div className="v2-file-attachments" aria-label={onRemove ? "待发送的文件" : "消息附件"}>
    {attachments.map((file) => <AttachmentCard key={`${file.workspace_id}:${file.id}:${file.sha256}`} client={client} file={file}
      locked={pendingIDs.includes(file.id)} onRemove={onRemove ? () => onRemove(file.id) : undefined} />)}
  </div>;
}

interface UploadAttempt { version: "v2_file_upload.v1"; operationKey: string; name: string; sha256: string; byteSize: number }
const validAttempt = (value: unknown): value is UploadAttempt => {
  if (!value || typeof value !== "object") return false;
  const attempt = value as UploadAttempt;
  return attempt.version === "v2_file_upload.v1" && /^v2-file-upload-[\w-]+$/u.test(attempt.operationKey) &&
    typeof attempt.name === "string" && attempt.name.length <= 640 && /^[a-f0-9]{64}$/u.test(attempt.sha256) &&
    Number.isSafeInteger(attempt.byteSize) && attempt.byteSize >= 0 && attempt.byteSize <= maximumFileBytes;
};
export function useV2FileInput({ client, workspaceID, threadID, disabled }: {
  client: CyberAgentClient; workspaceID: string; threadID: string; disabled: boolean;
}) {
  const references = useV2AttachmentReferences(workspaceID, threadID);
  const store = useV2RecoveryStore();
  const prefix = `file-upload:${JSON.stringify([workspaceID, threadID])}:`;
  const status = useMemo(() => ({ busy: false, error: "", attempts: new Map<string, UploadAttempt>() }), [prefix, store]);
  const [, refresh] = useReducer((count: number) => count + 1, 0);
  const reload = () => {
    if (!store) return;
    const entries = store.entries<unknown>(prefix); store.assertReadable(prefix);
    status.attempts.clear();
    for (const [key, value] of entries) {
      if (!validAttempt(value) || key !== prefix + value.operationKey) { store.reject(key); throw new Error("原文件上传记录无法读取，已保留原记录。"); }
      status.attempts.set(value.operationKey, value);
    }
  };
  useEffect(() => {
    const changed = () => { try { reload(); } catch (failure) { status.error = String(failure); } refresh(); };
    changed(); return store?.subscribePrefix?.(prefix, changed);
  }, [prefix, store]);
  const remember = (attempt: UploadAttempt) => {
    store?.write(prefix + attempt.operationKey, attempt); // Fail before POST if the original identity cannot be saved.
    status.attempts.set(attempt.operationKey, attempt); refresh();
  };
  const forget = (operationKey: string) => {
    try { store?.remove(prefix + operationKey); status.attempts.delete(operationKey); refresh(); }
    catch (failure) { status.error = String(failure); refresh(); }
  };
  const stillPending = (attempt: UploadAttempt) => {
    if (!store) return status.attempts.has(attempt.operationKey);
    const saved = store.read<unknown>(prefix + attempt.operationKey, null);
    store.assertReadable(prefix);
    return validAttempt(saved) && saved.operationKey === attempt.operationKey &&
      saved.sha256 === attempt.sha256 && saved.byteSize === attempt.byteSize;
  };
  const add = async (files: File[], batchCapture?: DraftCapture) => {
    if (disabled || !workspaceID || !files.length || status.busy) return;
    if (files.length + references.attachments.length + status.attempts.size > maximumFileAttachments) {
      status.error = "每条消息最多添加 4 个文件；请先处理待核对的附件。"; refresh(); return;
    }
    status.busy = true; status.error = ""; refresh();
    const failures: string[] = [];
    try {
      const captured = batchCapture ?? references.draft?.document.capture(references.draft.scope, references.draft.state.ref);
      for (const file of files) {
        if (file.size > maximumFileBytes) { failures.push(`${file.name}：超过 5 MiB，未加入。`); continue; }
        const bytes = await file.arrayBuffer();
        const sha256 = [...new Uint8Array(await crypto.subtle.digest("SHA-256", bytes))].map((byte) => byte.toString(16).padStart(2, "0")).join("");
        const attempt: UploadAttempt = { version: "v2_file_upload.v1", operationKey: `v2-file-upload-${crypto.randomUUID()}`, name: file.name, sha256, byteSize: file.size };
        remember(attempt);
        try {
          const attachment = await client.uploadWorkspaceFile(workspaceID, file, attempt.operationKey);
          if (!stillPending(attempt)) continue;
          if (captured && references.draft) {
            const next = references.draft.document.updateCaptured(captured, (current) => ({ ...current,
              attachments: (current.attachments ?? []).some(({ id }) => id === attachment.id) ? current.attachments : [...(current.attachments ?? []), attachment] }));
            if (next.error || !next.persisted) throw new Error(next.error || "附件尚未保存到草稿。");
          } else references.update((current) => current.some(({ id }) => id === attachment.id) ? current : [...current, attachment]);
          forget(attempt.operationKey);
        } catch (failure) { failures.push(`${file.name}：${failure instanceof Error ? failure.message : "上传结果未确认"}`); }
      }
    } catch (failure) { failures.push(failure instanceof Error ? failure.message : "附件未加入"); }
    finally { status.busy = false; status.error = failures.join("\n"); refresh(); }
  };
  const inspect = async (attempt: UploadAttempt) => {
    if (status.busy || disabled) return;
    status.busy = true; status.error = ""; refresh();
    // This is an explicit read of the original key, never an upload retry.
    try {
      const result = await client.inspectWorkspaceFileUpload(workspaceID, attempt.operationKey);
      if (!stillPending(attempt)) return;
      if (result.state !== "stored" || !result.attachment) throw new Error("尚未找到原上传结果；原请求仍可能稍后完成。可稍后核对，或不将该附件加入本条消息。");
      if (result.attachment.sha256 !== attempt.sha256 || result.attachment.byte_size !== attempt.byteSize) throw new Error("原上传结果与文件不符，未加入草稿。");
      if (references.attachments.length >= maximumFileAttachments && !references.attachments.some(({ id }) => id === result.attachment!.id)) throw new Error("文件数量已达上限，请先移除一个附件。");
      references.update((current) => current.some(({ id }) => id === result.attachment!.id) ? current : [...current, result.attachment!]);
      if (references.draft) {
        const next = references.draft.document.read(references.draft.scope);
        if (next.error || !next.persisted) throw new Error(next.error || "附件尚未保存到草稿。");
      }
      forget(attempt.operationKey);
    } catch (failure) { status.error = failure instanceof Error ? failure.message : "原上传结果尚未确认"; }
    finally { status.busy = false; refresh(); }
  };
  return { ...references, add, uploading: status.busy, error: status.error, pending: status.attempts.size > 0,
    notice: status.attempts.size > 0 && <div className="v2-attachment-pending" role="status">
      {[...status.attempts.values()].map((attempt) => <div key={attempt.operationKey}>
        <span>{attempt.name} · {status.busy ? "正在保存或核对" : "上传结果待核对"}</span>
        <button type="button" disabled={status.busy || disabled} onClick={() => void inspect(attempt)}>核对原上传</button>
        <button type="button" disabled={status.busy} onClick={() => forget(attempt.operationKey)}>不加入本条消息</button>
      </div>)}
    </div> };
}
