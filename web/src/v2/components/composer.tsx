import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { ArrowUp, LoaderCircle, Plus } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type { WorkspaceView } from "../../api/types";
import { V2ModelRouteControl, type V2PendingModelRoute } from "./model-route-control";
import { V2PermissionControl } from "./permission-control";
import { V2RunNetworkAuthorityControl } from "./run-network-authority-control";

const maximumContentBytes = 16 * 1024;

export function V2Composer({ client, threadID, runID = "", workspaceID, workspaces, disabled = false,
  placeholder = "输入消息…", newThreadControls, runActive = false, onManageModels,
  pendingModelRoute, onPendingModelRouteChange, onWorkspaceChange, onSubmit }: {
  client: CyberAgentClient;
  threadID: string;
  runID?: string;
  workspaceID: string;
  workspaces: WorkspaceView[];
  disabled?: boolean;
  placeholder?: string;
  newThreadControls?: ReactNode;
  runActive?: boolean;
  onManageModels?: () => void;
  pendingModelRoute?: V2PendingModelRoute | null;
  onPendingModelRouteChange?: (route: V2PendingModelRoute | null) => void;
  onWorkspaceChange: (workspaceID: string) => void;
  onSubmit: (content: string) => Promise<void>;
}) {
  const [content, setContent] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const normalized = content.trim();
  const byteLength = new TextEncoder().encode(normalized).byteLength;
  const ready = !disabled && !submitting && Boolean(workspaceID) && byteLength > 0 &&
    byteLength <= maximumContentBytes;

  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = "0px";
    textarea.style.height = `${Math.min(176, Math.max(34, textarea.scrollHeight))}px`;
  }, [content]);

  const submit = async (event?: FormEvent<HTMLFormElement>) => {
    event?.preventDefault();
    if (!ready) return;
    setSubmitting(true);
    setError("");
    try {
      await onSubmit(normalized);
      setContent("");
      textareaRef.current?.focus();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "消息发送失败");
    } finally {
      setSubmitting(false);
    }
  };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      void submit();
    }
  };

  return <form className="v2-composer" onSubmit={(event) => void submit(event)}>
    <div className="v2-composer-surface">
      <textarea aria-label={threadID ? "继续对话" : "开始新对话"} disabled={disabled || submitting}
        maxLength={maximumContentBytes} onChange={(event) => { setContent(event.target.value); setError(""); }}
        onKeyDown={onKeyDown} placeholder={placeholder} ref={textareaRef} rows={1} value={content} />
      <div className="v2-composer-footer">
        <div className="v2-composer-tools">
          <button aria-label="添加附件" className="v2-composer-icon" disabled
            title="当前 Thread API 尚未接受消息附件" type="button">
            <Plus aria-hidden="true" size={18} />
          </button>
          {!threadID && newThreadControls}
          {!threadID ? <label className="v2-workspace-picker">
            <span className="sr-only">工作区</span>
            <select aria-label="选择工作区" disabled={submitting || workspaces.length === 0}
              onChange={(event) => onWorkspaceChange(event.target.value)} value={workspaceID}>
              {workspaces.map((workspace) => <option key={workspace.id} value={workspace.id}>
                {workspace.name}
              </option>)}
            </select>
          </label> : <><V2PermissionControl client={client} threadID={threadID} />
            <V2RunNetworkAuthorityControl client={client} runID={runID} threadID={threadID}
              variant="menu" /></>}
        </div>
        <div className="v2-composer-actions">
          {onManageModels && (threadID || onPendingModelRouteChange) &&
            <V2ModelRouteControl client={client} onManageModels={onManageModels}
              onPendingRouteChange={onPendingModelRouteChange} pendingRoute={pendingModelRoute}
              runActive={runActive} threadID={threadID} />}
          <button aria-label="发送消息" className="v2-send-button" disabled={!ready} type="submit">
            {submitting ? <LoaderCircle aria-hidden="true" className="spin" size={18} />
              : <ArrowUp aria-hidden="true" size={19} />}
          </button>
        </div>
      </div>
    </div>
    {byteLength > maximumContentBytes && <p className="v2-composer-error">消息不能超过 16 KiB</p>}
    {error && <p className="v2-composer-error" role="alert">{error}</p>}
  </form>;
}
