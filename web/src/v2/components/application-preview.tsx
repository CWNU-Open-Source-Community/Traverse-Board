import { useEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, LoaderCircle, RefreshCw, X } from "lucide-react";
import { APIRequestError, type CyberAgentClient } from "../../api/client";
import type { RunDetailView } from "../../api/types";
import { localPreviewURL, parseApplicationPreview, type ApplicationPreview, type ApplicationPreviewElement } from "../../api/application-preview";
import { useModalFocusTrap } from "../../hooks/use-modal-focus-trap";
import { browserCDPQueryKey, fullCDPSessionQueryKey } from "./browser-cdp-control";
import { V2PermissionControl } from "./permission-control";
import "./application-preview.css";

export function V2ApplicationPreview({ client, runID, threadID, onClose, returnFocusRef, onRequestStart }: {
  client: CyberAgentClient; runID: string; threadID: string; onClose: () => void;
  returnFocusRef: RefObject<HTMLElement | null>; onRequestStart: () => void;
}) {
  const queries = useQueryClient();
  const closeButton = useRef<HTMLButtonElement>(null);
  const dialog = useModalFocusTrap<HTMLElement>(true, onClose, false, closeButton, { isolateBackground: true, returnFocusRef });
  const [target, setTarget] = useState("");
  const [product, setProduct] = useState<"edge" | "chrome">("edge");
  const [preview, setPreview] = useState<ApplicationPreview | null>(null);
  const [url, setURL] = useState("");
  const [error, setError] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const actionInFlight = useRef(false);
  const detail = useQuery({ queryKey: browserCDPQueryKey(runID),
    queryFn: ({ signal }) => client.get<RunDetailView>(`/runs/${encodeURIComponent(runID)}`, {}, signal), refetchInterval: 3000 });
  const session = useQuery({ queryKey: fullCDPSessionQueryKey(runID),
    queryFn: ({ signal }) => client.getFullCDPSession(runID, signal), enabled: client.hasFullCDPSessionControl,
    refetchOnMount: "always", refetchInterval: 1500 });
  const current = session.data?.session;
  const ready = current?.state === "ready";
  const active = ready || current?.state === "starting" || current?.state === "closing";
  const permission = detail.data?.execution_permission;
  const browserPermission = detail.data?.browser_cdp_permission;
  const eligible = (permission?.mode === "full_access" || permission?.mode === "debug") && permission.runtime_gate_available;
  const cdpEnabled = browserPermission?.mode === "full_debug" && browserPermission.runtime_gate_available;
  const refresh = useMutation({ retry: false, mutationFn: async () => {
    setError(""); setPreview(null);
    const sessionID = current?.session_id;
    if (!sessionID || !ready) throw new Error("预览浏览器尚未就绪。");
    return parseApplicationPreview(await client.postControl(`/runs/${encodeURIComponent(runID)}/full-cdp-session/preview`, {
      version: "full_cdp_preview.v1", expected_session_id: sessionID,
    }, `v2-preview-${crypto.randomUUID()}`), runID, sessionID);
  }, onSuccess: setPreview, onError: (failure) => setError(failure instanceof APIRequestError
    ? "无法读取当前页面，请检查预览是否仍在运行以及浏览器控制权限。" : failure.message) });
  const open = useMutation({ retry: false, mutationFn: () => client.openFullCDPSession(runID, {
    version: "full_cdp_session.v1", target: target.trim(), browser: { product, channel: "stable" },
    expected_execution_permission_revision: permission?.revision ?? 0,
    expected_browser_cdp_permission_revision: browserPermission?.revision ?? 0,
    confirm_full_cdp: true, reason: "operator opened project application preview",
  }, `v2-preview-open-${crypto.randomUUID()}`), onSuccess: (result) => { queries.setQueryData(fullCDPSessionQueryKey(runID), result); setError(""); },
  onError: (failure) => setError(failure.message) });
  const close = useMutation({ retry: false, mutationFn: () => client.closeFullCDPSession(runID, {
    version: "full_cdp_session_close.v1", expected_session_id: current?.session_id ?? "", reason: "operator_closed",
  }, `v2-preview-close-${crypto.randomUUID()}`), onMutate: () => { setPreview(null); setURL(""); },
  onSuccess: (result) => queries.setQueryData(fullCDPSessionQueryKey(runID), result), onError: (failure) => setError(failure.message) });
  const enable = useMutation({ retry: false, mutationFn: () => client.postControl(`/runs/${encodeURIComponent(runID)}/browser-cdp-permission`, {
    mode: "full_debug", confirm_full_cdp_debug: true, reason: "operator enabled project preview browser control",
  }, `v2-preview-enable-${crypto.randomUUID()}`), onSuccess: () => queries.invalidateQueries({ queryKey: browserCDPQueryKey(runID) }),
  onError: (failure) => setError(failure.message) });
  const act = useMutation({ retry: false, mutationFn: async ({ element, value }: { element: ApplicationPreviewElement; value?: string }) => {
    const observed = preview;
    if (!observed || actionInFlight.current) throw new Error("请先刷新当前页面。");
    actionInFlight.current = true;
    setPreview(null); setURL(""); setError("");
    try {
      return parseApplicationPreview(await client.postControl(`/runs/${encodeURIComponent(runID)}/full-cdp-session/preview-action`, {
        version: "full_cdp_preview_action.v1", expected_session_id: observed.session_id,
        expected_snapshot_id: observed.page.snapshot_id, action: value === undefined ? "click" : "type",
        selector: element.selector, ...(value === undefined ? {} : { value }),
      }, `v2-preview-action-${crypto.randomUUID()}`), runID, observed.session_id);
    } finally { actionInFlight.current = false; }
  }, onSuccess: (result) => { setPreview(result); setValues({}); },
  onError: (failure) => setError(`${failure instanceof APIRequestError
    ? "页面操作未确认完成，当前页面或授权状态可能已变化。" : failure.message} 请刷新观察结果后再决定下一步，操作不会自动重发。`) });
  useEffect(() => {
    if (!active) return;
    if (current?.browser?.product) setProduct(current.browser.product);
    const address = preview?.canonical_url ?? current?.target_origin;
    if (address) setTarget(address);
  }, [active, current?.browser?.product, current?.target_origin, preview?.canonical_url]);
  useEffect(() => {
    if (!ready || !preview || preview.session_id !== current?.session_id) { setURL(""); return; }
    const abort = new AbortController(); let objectURL = "";
    setURL("");
    void client.downloadVerifiedImage(`/runs/${encodeURIComponent(runID)}/full-cdp-session/preview-image?` +
      new URLSearchParams({ session_id: preview.session_id, sha256: preview.image.sha256 }), {
        mime_type: preview.image.media_type, byte_size: preview.image.bytes, sha256: preview.image.sha256,
      }, abort.signal).then((blob) => {
        if (abort.signal.aborted) return;
        objectURL = URL.createObjectURL(blob); setURL(objectURL);
      }).catch((failure) => { if (!abort.signal.aborted) { setPreview(null); setError(failure.message); } });
    return () => { abort.abort(); if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [preview, client, runID, current?.session_id, ready]);
  const lastSession = useRef("");
  useEffect(() => {
    if (!session.isFetching && !session.isError && ready && current?.session_id && lastSession.current !== current.session_id) {
      lastSession.current = current.session_id; refresh.mutate();
    }
    if (!ready) setPreview(null);
  }, [ready, current?.session_id, session.isFetching, session.isError]);
  const busy = refresh.isPending || open.isPending || close.isPending || act.isPending;
  const visibleElements = preview?.page.elements.filter((element) => element.type !== "hidden") ?? [];
  return createPortal(<div className="v2-preview-overlay">
    <section className="v2-app-preview" ref={dialog} role="dialog" aria-modal="true" aria-label="应用预览">
      <header><h2>应用预览</h2><button aria-label="收起应用预览" type="button" ref={closeButton} onClick={onClose}><X size={20} /></button></header>
      {!client.hasFullCDPSessionControl ? <p role="status">当前服务未提供应用预览，请在支持托管浏览器的桌面端或 API 服务中使用。</p> : <>
        <div className="v2-preview-address"><label>项目应用地址<input aria-label="项目应用地址" type="url" placeholder="http://127.0.0.1:3000" value={target}
          disabled={active || open.isPending} onChange={(event) => setTarget(event.target.value)} /></label>
          <label>浏览器<select aria-label="预览浏览器" value={product} disabled={active} onChange={(event) => setProduct(event.target.value as typeof product)}>
            <option value="edge">Microsoft Edge</option><option value="chrome">Google Chrome</option></select></label>
          {!active ? <button type="button" disabled={busy || !eligible || !cdpEnabled || !localPreviewURL(target.trim())} onClick={() => open.mutate()}><ExternalLink size={16} />打开应用</button>
            : <button type="button" disabled={busy || current?.state === "closing"} onClick={() => close.mutate()}>停止预览</button>}
        </div>
        {!active && <p className="v2-preview-help">填写开发服务显示的本机地址。打开后，当前任务可以操作这个独立浏览器；使用临时浏览器资料，关闭或到期后清理。
          <button type="button" onClick={onRequestStart}>让 Agent 启动项目应用</button></p>}
        {!eligible && <div className="v2-preview-help">当前预览需要已激活的完全访问或调试权限。<V2PermissionControl client={client} threadID={threadID} /></div>}
        {eligible && !cdpEnabled && <div className="v2-preview-help">浏览器控制目前关闭。启用后，当前任务能读取、操作此独立浏览器及其网络请求。
          <button disabled={enable.isPending || !client.hasBrowserCDPPermissionControl || !client.hasFullCDPDebug} type="button" onClick={() => enable.mutate()}>允许任务控制预览浏览器</button></div>}
        <div className="v2-preview-status" role="status"><span>{session.isError || detail.isError ? "无法读取预览状态" : current?.state === "ready" ? "预览已就绪" : current?.state === "starting" ? "正在启动浏览器…"
          : current?.state === "closing" ? "正在清理浏览器…" : current?.state === "failed" ? "浏览器启动失败" : "预览未运行"}</span>
          {current?.target_origin && <code>{current.target_origin}</code>}
          {ready && <button type="button" disabled={busy} onClick={() => refresh.mutate()}><RefreshCw size={15} />刷新页面预览</button>}</div>
        {error && <p role="alert" className="v2-composer-error">{error}</p>}
        {current?.state === "failed" && <p role="alert">{current.failure_code || "未能启动，请检查开发服务与浏览器是否可用。"}</p>}
        {ready && <div className="v2-preview-page">
          {busy && <p role="status"><LoaderCircle size={18} className="spin" />正在读取页面…</p>}
          {url && preview && <><p>{preview.page.title || "应用页面"}<small>观察于 {new Date(preview.captured_at).toLocaleTimeString()}</small></p>
            <img className="v2-preview-capture" src={url} alt={`应用页面：${preview.page.title || preview.canonical_url}`} />
            <details className="v2-preview-interactions"><summary>操作页面控件（{visibleElements.length}）</summary>
              <p>输入会追加到页面输入框的当前光标位置；只发送给本项目页面。操作后重新读取页面，观察过期时请先刷新。</p>
              {visibleElements.map((element) => {
                const editable = (element.tag === "input" && !["button", "submit", "reset", "checkbox", "radio", "file", "password", "hidden"].includes(element.type ?? "text")) || element.tag === "textarea";
                const blocked = element.disabled || ["password", "file", "hidden"].includes(element.type ?? "");
                const label = element.name || element.role || element.tag;
                return <div className="v2-preview-element" key={element.selector}><span>{label}</span>
                  {editable && <input aria-label={`页面输入 ${label}`} disabled={blocked || busy} value={values[element.selector] ?? ""}
                    onChange={(event) => setValues((currentValues) => ({ ...currentValues, [element.selector]: event.target.value }))} />}
                  <button type="button" disabled={blocked || busy || (editable && !(element.selector in values))}
                    onClick={() => act.mutate({ element, ...(editable ? { value: values[element.selector] } : {}) })}>{editable ? "输入到页面" : "点击"}</button></div>;
              })}</details>
            <details><summary>页面文字{preview.page.truncated ? "（部分）" : ""}</summary><pre>{preview.page.text}</pre></details></>}
        </div>}
        {current?.state === "closed" && <p role="status">预览已关闭，浏览器{current.process_tree_quiescent && current.profile_cleaned ? "和临时资料已清理。" : "清理状态尚未完整确认。"}</p>}
      </>}
    </section>
  </div>, document.body);
}
