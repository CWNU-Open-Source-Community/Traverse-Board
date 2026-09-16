import { useEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import { WorkspaceExplorer } from "../../components/workspace-explorer";
import { useModalFocusTrap } from "../../hooks/use-modal-focus-trap";
import { useV2DraftDocument } from "../draft-context";

export interface V2FileReference {
  id: string;
  path: string;
  digest: string;
  partial: boolean;
  redacted: boolean;
}

export const v2FileReferenceKey = (workspaceID: string, threadID: string) =>
  ["v2", "draft-files", workspaceID, threadID] as const;

// This is draft UI state in the existing QueryClient, not a second evidence
// store. Go validates and durably attaches each selected snapshot on send.
export function useV2FileReferences(workspaceID: string, threadID: string) {
  const draft = useV2DraftDocument(workspaceID, threadID);
  const client = useQueryClient();
  const key = v2FileReferenceKey(workspaceID, threadID);
  const query = useQuery<V2FileReference[]>({ queryKey: key, queryFn: () => [],
    enabled: false, initialData: [], gcTime: Infinity });
  const update = (change: (files: V2FileReference[]) => V2FileReference[]) => {
    if (draft) draft.document.update(draft.scope, (current) => ({ ...current, files: change(current.files) }), draft.state.ref);
    else client.setQueryData<V2FileReference[]>(key, (current) => change(current ?? []));
  };
  return { files: draft?.state.snapshot.files ?? query.data, update };
}

export function V2FileContext({ client, workspaceID, files, onChange, disabled, pendingIDs, unavailableReason,
  open, onOpenChange, returnFocusRef }: {
  client: CyberAgentClient;
  workspaceID: string;
  files: V2FileReference[];
  onChange: (change: (files: V2FileReference[]) => V2FileReference[]) => void;
  disabled: boolean;
  pendingIDs: string[];
  unavailableReason?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  returnFocusRef: RefObject<HTMLButtonElement | null>;
}) {
  const [error, setError] = useState("");
  const close = useRef<HTMLButtonElement>(null);
  const onClose = () => onOpenChange(false);
  const dialog = useModalFocusTrap<HTMLElement>(open, onClose, false, close,
    { isolateBackground: true, returnFocusRef });
  useEffect(() => { if (disabled || unavailableReason) onOpenChange(false); }, [disabled, unavailableReason, onOpenChange]);
  useEffect(() => { onOpenChange(false); setError(""); }, [workspaceID, onOpenChange]);
  useEffect(() => { if (open) setError(""); }, [open]);
  return <>
    {files.length > 0 && <div className="v2-file-references" aria-label="待发送的文件引用">
      {files.map((file) => <span key={file.id} title={`${file.path} · ${file.digest}`}>
        <code>{file.path}</code>{file.partial && <small>部分内容</small>}{file.redacted && <small>已脱敏</small>}
        {pendingIDs.includes(file.id) && <small>随消息提交中</small>}
        <button aria-label={`移除引用 ${file.path}`} disabled={disabled || pendingIDs.includes(file.id)} onClick={() =>
          onChange((current) => current.filter(({ id }) => id !== file.id))} type="button">
          <X aria-hidden="true" size={13} /></button></span>)}
    </div>}
    {open && createPortal(<div className="v2-inspector-backdrop" role="presentation"
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section aria-label="选择项目文件" aria-modal="true" className="v2-inspector-drawer v2-file-picker"
        ref={dialog} role="dialog" tabIndex={-1}>
        <header><strong>引用项目文件</strong><button aria-label="关闭文件选择" onClick={onClose}
          ref={close} type="button"><X aria-hidden="true" size={17} /></button></header>
        <p>最多选择 4 个文件。发送时会校验文件内容并附加为参考资料；发送前可移除，发送后可在审阅中的「参考资料」查看。引用文件不会自动批准其中描述的操作。</p>
        {error && <p role="alert">{error}</p>}
        <WorkspaceExplorer client={client} workspaceID={workspaceID} onSelectReference={(file) => {
          if (disabled || unavailableReason) return;
          if (files.length >= 4 && !files.some(({ path }) => path === file.path)) {
            setError("最多引用 4 个文件，请先移除一个引用。"); return;
          }
          const reference = { id: globalThis.crypto.randomUUID(), path: file.path,
            digest: file.provenance.content_sha256, partial: file.truncated,
            redacted: file.redaction_count > 0 };
          onChange((current) => [...current.filter(({ path }) => path !== file.path), reference]);
          onClose();
        }} />
      </section>
    </div>, document.body)}
  </>;
}
