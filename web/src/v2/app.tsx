import { useEffect, useId, useMemo, useRef, useState, type RefObject } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Folder, Microscope, X } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { ThreadDetailView, ThreadView, WorkspaceView } from "../api/types";
import { useConnectionStore } from "../state/connection";
import { useModalFocusTrap } from "../hooks/use-modal-focus-trap";
import { openLegacyInspector } from "../legacy-route";
import { V2Composer } from "./components/composer";
import { V2Conversation } from "./components/conversation";
import { V2ConfirmDialog } from "./components/dialog";
import { V2NetworkScopeControl, type V2NetworkMode } from "./components/network-scope-control";
import { V2Settings } from "./components/settings";
import { V2SettingsSidebar, V2Sidebar, type V2SettingsSection } from "./components/sidebar";
import { V2Titlebar } from "./components/titlebar";
import { createV2Client } from "./client-session";
import { v2QueryKeys } from "./query-keys";
import "./styles.css";

function InspectorDrawer({ client, threadID, open, onClose, returnFocusRef }: {
  client: CyberAgentClient;
  threadID: string;
  open: boolean;
  onClose: () => void;
  returnFocusRef: RefObject<HTMLElement | null>;
}) {
  const titleID = useId();
  const closeRef = useRef<HTMLButtonElement>(null);
  const drawerRef = useModalFocusTrap<HTMLElement>(open, onClose, false, closeRef, {
    isolateBackground: true,
    returnFocusRef,
  });
  const query = useQuery({
    queryKey: ["v2", "inspector", threadID],
    queryFn: ({ signal }) => client.get<ThreadDetailView>(
      `/threads/${encodeURIComponent(threadID)}`, {}, signal),
    enabled: open && Boolean(threadID),
  });
  if (!open) return null;
  return <div className="v2-inspector-backdrop" role="presentation" onMouseDown={(event) => {
    if (event.target === event.currentTarget) onClose();
  }}><aside aria-labelledby={titleID} aria-modal="true" className="v2-inspector-drawer"
    ref={drawerRef} role="dialog" tabIndex={-1}>
    <header><div><Microscope aria-hidden="true" size={17} /><strong id={titleID}>Inspector</strong></div>
      <button aria-label="关闭 Inspector" onClick={onClose} ref={closeRef} type="button">
        <X aria-hidden="true" size={16} /></button></header>
    {!threadID ? <p>先打开一个对话。</p> : query.isLoading ? <p>正在读取内部状态…</p>
      : query.isError || !query.data ? <p role="alert">无法读取 Inspector 数据</p>
        : <div className="v2-inspector-facts">
          <label>Thread ID<code>{query.data.thread.id}</code></label>
          <label>Mission ID<code>{query.data.mission.id}</code></label>
          <label>Current Run<code>{query.data.active_run?.id ?? query.data.last_run.id}</code></label>
          <label>Run attempts<strong>{query.data.runs.length}</strong></label>
          <label>Composer state<strong>{query.data.thread.composer_state}</strong></label>
        </div>}
    <button className="v2-open-legacy" onClick={() => {
      onClose();
      openLegacyInspector(threadID);
    }} type="button">打开完整 Harness Inspector</button>
  </aside></div>;
}

function NewConversation({ client, workspaces, workspaceID, onWorkspaceChange, onCreated,
  onManageModels }: {
  client: CyberAgentClient;
  workspaces: WorkspaceView[];
  workspaceID: string;
  onWorkspaceChange: (workspaceID: string) => void;
  onCreated: (thread: ThreadView) => void;
  onManageModels: () => void;
}) {
  const queryClient = useQueryClient();
  const [networkMode, setNetworkMode] = useState<V2NetworkMode>("disabled");
  const [allowedTargets, setAllowedTargets] = useState<string[]>([]);
  const [modelRoute, setModelRoute] = useState<{ provider: string; model: string } | null>(null);
  const creationAttemptRef = useRef<{ fingerprint: string; operationID: string } | null>(null);
  const create = async (content: string) => {
    const request = {
      version: "thread_creation.v1",
      workspace_id: workspaceID,
      goal: content,
      profile: "code",
      surface: "code",
      phase: "deliver",
      network_mode: networkMode,
      ...(networkMode === "allowlist" ? { allowed_targets: allowedTargets } : {}),
      ...(modelRoute ? { provider: modelRoute.provider, model: modelRoute.model } : {}),
    } as Parameters<CyberAgentClient["createThread"]>[0] & { provider?: string; model?: string };
    const fingerprint = JSON.stringify(request);
    const operationID = creationAttemptRef.current?.fingerprint === fingerprint
      ? creationAttemptRef.current.operationID
      : globalThis.crypto.randomUUID();
    creationAttemptRef.current = { fingerprint, operationID };
    const result = await client.createThread(request, `v2-thread-create-${operationID}`);
    try {
      await client.submitThreadTurn(result.thread.id, {
        version: "thread_message_submission.v1", content,
      }, `v2-thread-create-turn-${operationID}`);
    } catch {
      // Thread creation already succeeded. Open that durable Thread even when its
      // first execution failed so the next explicit message continues it instead
      // of creating a duplicate Thread from the retained new-conversation draft.
    } finally {
      onCreated(result.thread);
      void Promise.allSettled([
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") }),
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.thread(result.thread.id) }),
        queryClient.invalidateQueries({ queryKey: v2QueryKeys.transcript(result.thread.id) }),
      ]);
    }
  };
  return <section className="v2-new-conversation">
    <header><Folder aria-hidden="true" size={17} /><strong>新对话</strong></header>
    <div className="v2-new-conversation-body"><div className="v2-new-copy">
      <span className="v2-orb" aria-hidden="true"><i /><i /></span>
      <h1>接下来要做什么？</h1><p>描述目标即可开始。运行、权限边界和执行续接由 Traverse 内核处理。</p>
    </div></div>
    <div className="v2-composer-dock"><V2Composer client={client}
      disabled={!client.hasThreadControl || !workspaceID} onSubmit={create}
      onManageModels={onManageModels} onPendingModelRouteChange={setModelRoute}
      pendingModelRoute={modelRoute}
      newThreadControls={<V2NetworkScopeControl disabled={!client.hasThreadControl}
        mode={networkMode} onChange={(nextMode, nextTargets) => {
          setNetworkMode(nextMode);
          setAllowedTargets(nextTargets);
        }} targets={allowedTargets} />}
      onWorkspaceChange={onWorkspaceChange} placeholder="输入任务或问题…" threadID=""
      workspaceID={workspaceID} workspaces={workspaces} />
      <small className="v2-composer-caption">第一条消息会创建对话并自动工作到自然停止边界</small></div>
  </section>;
}

export function V2Workbench({ client }: { client: CyberAgentClient }) {
  const queryClient = useQueryClient();
  const selectedThreadID = useConnectionStore((state) => state.selectedThreadID);
  const selectThread = useConnectionStore((state) => state.selectThread);
  const [surface, setSurface] = useState<"conversation" | "settings">("conversation");
  const [settingsSection, setSettingsSection] = useState<V2SettingsSection>("general");
  const [newConversation, setNewConversation] = useState(false);
  const [sidebarVisible, setSidebarVisible] = useState(() => !window.matchMedia?.("(max-width: 760px)").matches);
  const [searchOpen, setSearchOpen] = useState(false);
  const [workspaceID, setWorkspaceID] = useState("");
  const [archiveCandidate, setArchiveCandidate] = useState<ThreadView | null>(null);
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const inspectorReturnFocusRef = useRef<HTMLElement>(null);
  const workspacesQuery = useQuery({
    queryKey: v2QueryKeys.workspaces,
    queryFn: ({ signal }) => client.getPage<WorkspaceView>("/workspaces", { limit: 100 }, "", signal),
    staleTime: 30_000,
  });
  const threadsQuery = useQuery({
    queryKey: v2QueryKeys.threads("active"),
    queryFn: ({ signal }) => client.getPage<ThreadView>("/threads",
      { limit: 100, status: "active" }, "", signal),
    refetchInterval: 4_000,
  });
  const workspaces = workspacesQuery.data?.items ?? [];
  const threads = threadsQuery.data?.items ?? [];
  useEffect(() => {
    if (!workspaceID && workspaces[0]) setWorkspaceID(workspaces[0].id);
  }, [workspaceID, workspaces]);
  useEffect(() => {
    if (threadsQuery.isLoading || newConversation || surface !== "conversation") return;
    if (selectedThreadID && threads.some(({ id }) => id === selectedThreadID)) return;
    if (threads[0]) selectThread(threads[0].id);
    else setNewConversation(true);
  }, [newConversation, selectThread, selectedThreadID, surface, threads, threadsQuery.isLoading]);
  const archiveMutation = useMutation({
    mutationFn: (thread: ThreadView) => client.transitionThread(thread.id, "archive", {
      version: "thread_lifecycle.v1", expected_version: thread.version,
    }, `v2-thread-archive-${globalThis.crypto.randomUUID()}`),
    onSuccess: (result) => {
      const fallback = threads.find(({ id }) => id !== result.thread.id);
      if (selectedThreadID === result.thread.id) {
        if (fallback) selectThread(fallback.id);
        else { selectThread(""); setNewConversation(true); }
      }
      setArchiveCandidate(null);
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") });
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("archived") });
    },
  });
  const selectedThread = threads.find(({ id }) => id === selectedThreadID);
  const openConversation = (threadID: string) => {
    selectThread(threadID);
    setNewConversation(false);
    setSurface("conversation");
  };
  const startNew = () => {
    selectThread("");
    setNewConversation(true);
    setSurface("conversation");
  };
  const openSettings = () => { setSurface("settings"); setSettingsSection("general"); };
  const openModels = () => { setSurface("settings"); setSettingsSection("models"); };
  const openInspector = (returnFocus?: HTMLElement | null) => {
    inspectorReturnFocusRef.current = returnFocus ??
      (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    setInspectorOpen(true);
  };

  return <div className={`v2-shell${sidebarVisible ? " has-sidebar" : " no-sidebar"}`}>
    <V2Titlebar canGoBack={surface === "settings"} onBack={() => setSurface("conversation")}
      onToggleSidebar={() => setSidebarVisible((visible) => !visible)} sidebarVisible={sidebarVisible} />
    <div className="v2-shell-body">
      {sidebarVisible && (surface === "settings" ? <V2SettingsSidebar onBack={() => setSurface("conversation")}
        onSelect={setSettingsSection} section={settingsSection} /> : <V2Sidebar
          onArchive={setArchiveCandidate} onNewConversation={startNew} onOpenModels={openModels}
          onOpenSettings={openSettings}
          onSearchOpen={setSearchOpen} onSelectThread={openConversation} searchOpen={searchOpen}
          selectedThreadID={newConversation ? "" : selectedThreadID} threads={threads} workspaces={workspaces} />)}
      <div className="v2-product-surface">
        {surface === "settings" ? <V2Settings client={client} onOpenInspector={openInspector}
          onSelectSection={setSettingsSection} section={settingsSection}
          threadID={selectedThreadID} workspaces={workspaces} /> : newConversation || !selectedThreadID
          ? <NewConversation client={client} onCreated={(thread) => {
            queryClient.setQueryData(v2QueryKeys.threads("active"), (current: { items: ThreadView[] } | undefined) =>
              current ? { ...current, items: [thread, ...current.items] } : current);
            openConversation(thread.id);
            void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") });
          }} onManageModels={openModels} onWorkspaceChange={setWorkspaceID}
            workspaceID={workspaceID} workspaces={workspaces} />
          : <V2Conversation client={client} onArchive={() => selectedThread && setArchiveCandidate(selectedThread)}
            onManageModels={openModels} onOpenInspector={openInspector}
            threadID={selectedThreadID} workspaces={workspaces} />}
      </div>
    </div>
    <InspectorDrawer client={client} onClose={() => setInspectorOpen(false)}
      open={inspectorOpen} returnFocusRef={inspectorReturnFocusRef}
      threadID={selectedThreadID} />
    <V2ConfirmDialog busy={archiveMutation.isPending} confirmLabel="归档" description="此对话会从侧栏隐藏，但消息、执行记录和审计证据都会保留。你可以随时从设置中恢复。"
      onCancel={() => setArchiveCandidate(null)} onConfirm={() => archiveCandidate && archiveMutation.mutate(archiveCandidate)}
      open={Boolean(archiveCandidate)} title="归档这个对话？" />
    {archiveMutation.isError && <div className="v2-toast" role="alert">{archiveMutation.error instanceof Error
      ? archiveMutation.error.message : "归档失败"}</div>}
  </div>;
}

export function V2WorkbenchEntry() {
  const connection = useConnectionStore();
  const client = useMemo(() => createV2Client(connection), [connection]);
  return <V2Workbench client={client} />;
}
