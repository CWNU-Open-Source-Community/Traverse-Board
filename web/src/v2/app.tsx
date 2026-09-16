import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { QueryClient, QueryClientProvider, useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Folder } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { ThreadDetailView, ThreadView, WorkspaceView } from "../api/types";
import { useConnectionStore } from "../state/connection";
import { V2Composer } from "./components/composer";
import { v2FileReferenceKey, type V2FileReference } from "./components/file-context";
import { V2Conversation } from "./components/conversation";
import { V2ConfirmDialog } from "./components/dialog";
import { V2NetworkScopeControl, type V2NetworkMode } from "./components/network-scope-control";
import { V2Settings } from "./components/settings";
import { V2SettingsSidebar, V2Sidebar, type V2SettingsSection } from "./components/sidebar";
import { V2Titlebar } from "./components/titlebar";
import { createV2Client } from "./client-session";
import { v2QueryKeys } from "./query-keys";
import { registerV2RecoveredTurn, useV2RestoreTurns, useV2ThreadTurn, v2TurnFailed } from "./use-thread-turn";
import { V2WorkspaceStart } from "./components/workspace-start";
import { useV2Navigation } from "./navigation";
import { V2InspectorTools, V2InspectorHome } from "./components/inspector-tools";
import { readDensity } from "../components/shared-settings-panels";
import { V2RecoveryProvider, useV2PersistentState, useV2PersistenceWarning, useV2RecoveryStore } from "./recovery-storage";
import { useV2Drafts } from "./recovery-session";
import { readV2Route } from "./navigation";
import { useV2CreationRecovery } from "./recovery-creation";
import type { WorkspaceImageAttachment } from "../api/image-attachments";
import { fileAttachmentIdentities, type WorkspaceFileAttachment } from "../api/file-attachments";
import { v2AttachmentReferenceKey } from "./attachment-keys";
import { v2ImageReferenceKey } from "./components/image-input";
import { assertV2DraftVersion, getV2DraftDocument, readV2Draft, requireV2DraftVersion, useV2DraftDocument, v2DraftScope } from "./draft-context";
import type { V2DraftVersion } from "./draft-version";
import { V2DraftConflict } from "./components/draft-conflict";
import { V2PhasePicker, type V2WorkPhase } from "./components/phase-picker";

type CreationAttempt = Map<string, string>;
type NewThreadOptions = { networkMode: V2NetworkMode; allowedTargets: string[];
  modelRoute: { provider: string; model: string } | null };
import "./styles.css";
import "./shared-shell.css";

function NewConversation({ client, workspaces, workspaceID, onWorkspaceChange, onCreated,
  onTurnSuccess, onManageModels, draft: legacyDraft, onDraftChange: legacyDraftChange, creationAttemptRef, options, onOptionsChange, moreProjects, onImported }: {
  client: CyberAgentClient;
  workspaces: WorkspaceView[];
  workspaceID: string;
  onWorkspaceChange: (workspaceID: string) => void;
  onImported: (workspace: WorkspaceView, currentDraft?: string) => void;
  onCreated: (thread: ThreadView, submittedDraft: string, files: V2FileReference[], images?: WorkspaceImageAttachment[], version?: V2DraftVersion, attachments?: WorkspaceFileAttachment[]) => void;
  onTurnSuccess: (threadID: string, submittedDraft: string) => void;
  onManageModels: () => void;
  draft: string;
  onDraftChange: (content: string, expected?: string) => void;
  creationAttemptRef: RefObject<CreationAttempt>;
  options: NewThreadOptions;
  onOptionsChange: (options: NewThreadOptions) => void;
  moreProjects?: React.ReactNode;
}) {
  const queryClient = useQueryClient();
  const turn = useV2ThreadTurn(client);
  const recovery = useV2RecoveryStore();
  const managedDraft = useV2DraftDocument(workspaceID);
  const draft = managedDraft?.state.snapshot.text ?? legacyDraft;
  const onDraftChange = managedDraft?.changeText ?? legacyDraftChange;
  const creationRecovery = useV2CreationRecovery(client, workspaceID, (thread, originalDraft, files, input) => {
    registerV2RecoveredTurn(queryClient, input);
    onCreated(thread, originalDraft, files, input.images, input.draftVersion, input.attachments);
  });
  const { networkMode, allowedTargets, modelRoute } = options;
  const [savedPhase, setSavedPhase] = useV2PersistentState<unknown>(`new-thread-phase:${workspaceID}`, "deliver");
  const phase: V2WorkPhase = savedPhase === "plan" ? "plan" : "deliver";
  const create = async (content: string, files: V2FileReference[] = [], images: WorkspaceImageAttachment[] = [], draftVersion?: V2DraftVersion, attachments: WorkspaceFileAttachment[] = []) => {
    if (phase === "plan" && !client.hasPlanDelivery) throw new Error("当前连接未启用计划确认。请检查连接，或选择直接执行。");
    const submittedDraft = draft;
    const request = {
      version: "thread_creation.v1",
      workspace_id: workspaceID,
      goal: content || (images.length ? "图片对话" : "文件对话"),
      profile: "code",
      surface: "code",
      phase,
      network_mode: networkMode,
      ...(networkMode === "allowlist" ? { allowed_targets: allowedTargets } : {}),
      ...(modelRoute ? { provider: modelRoute.provider, model: modelRoute.model } : {}),
    } as Parameters<CyberAgentClient["createThread"]>[0] & { provider?: string; model?: string };
    if (recovery) {
      if (managedDraft) {
        if (draftVersion) assertV2DraftVersion(managedDraft, draftVersion, { text: submittedDraft, files, images, attachments });
        else draftVersion = requireV2DraftVersion(managedDraft, { text: submittedDraft, files, images, attachments });
      }
      const intent = creationRecovery.prepare(request, content, files, submittedDraft, images, draftVersion, attachments);
      const thread = await creationRecovery.resolve(intent);
      const input = creationRecovery.handoff(intent, thread);
      const submission = turn.mutateAsync(input);
      onCreated(thread, submittedDraft, files, images, input.draftVersion, attachments);
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") });
      try { await submission; }
      catch (error) { if (v2TurnFailed(error) && !input.draftVersion) onTurnSuccess(thread.id, submittedDraft); throw error; }
      if (!input.draftVersion) onTurnSuccess(thread.id, submittedDraft);
      return;
    }
    const fingerprint = JSON.stringify([request, files, images, fileAttachmentIdentities(attachments)]);
    const operationID = creationAttemptRef.current.get(fingerprint) ?? globalThis.crypto.randomUUID();
    creationAttemptRef.current.set(fingerprint, operationID);
    const result = await client.createThread(request, `v2-thread-create-${operationID}`);
    const submission = turn.mutateAsync({ threadID: result.thread.id, workspaceID, content,
      ...(files.length ? { files } : {}),
      ...(images.length ? { images } : {}),
      ...(attachments.length ? { attachments } : {}),
      operationKey: `v2-thread-create-turn-${operationID}`, createdAt: new Date().toISOString() });
    onCreated(result.thread, submittedDraft, files, images, undefined, attachments);
    creationAttemptRef.current.delete(fingerprint);
    void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") });
    try { await submission; }
    catch (error) { if (v2TurnFailed(error)) onTurnSuccess(result.thread.id, submittedDraft); throw error; }
    onTurnSuccess(result.thread.id, submittedDraft);
  };
  return <section className="v2-new-conversation">
    <header><Folder aria-hidden="true" size={17} /><strong>新对话</strong>
      <V2WorkspaceStart client={client} onSelect={(workspace) => onImported(workspace, draft)} />{moreProjects}</header>
    <div className="v2-new-conversation-body"><div className="v2-new-copy">
      <span className="v2-orb" aria-hidden="true"><i /><i /></span>
      <h1>接下来要做什么？</h1><p>{workspaceID ? "描述目标，随时查看进展、补充消息或停止。"
        : "先接入项目。你也可以先写下需求，选择项目后继续。"}</p>
    </div></div>
    {creationRecovery.notice}
    <div className="v2-composer-dock">
      {managedDraft && <V2DraftConflict key={workspaceID} client={client} workspaceID={workspaceID} state={managedDraft.state}
        onResolve={(token, ref) => { managedDraft.document.resolve(managedDraft.scope, token, ref); }} />}
      <V2Composer client={client}
      draft={draft} onDraftChange={onDraftChange}
      disabled={!client.hasThreadControl} onSubmit={create}
      onManageModels={onManageModels} onPendingModelRouteChange={(route) =>
        onOptionsChange({ ...options, modelRoute: route })}
      pendingModelRoute={modelRoute}
      newThreadControls={<><V2PhasePicker phase={phase} onChange={setSavedPhase}
        disabled={!client.hasThreadControl} planAvailable={client.hasPlanDelivery} />
      <V2NetworkScopeControl disabled={!client.hasThreadControl}
        mode={networkMode} onChange={(nextMode, nextTargets) => {
          onOptionsChange({ ...options, networkMode: nextMode, allowedTargets: nextTargets });
        }} targets={allowedTargets} /></>}
      onWorkspaceChange={onWorkspaceChange} placeholder="输入任务或问题…" threadID=""
      workspaceID={workspaceID} workspaces={workspaces} />
      <small className="v2-composer-caption">{phase === "plan"
        ? "先分析并准备计划，确认后开始执行。可随时补充要求。"
        : "直接处理需求，操作仍遵循任务权限。可随时补充要求。"}</small></div>
  </section>;
}

export function V2Workbench({ client }: { client: CyberAgentClient }) {
  const scopeID = useConnectionStore((state) => state.health?.data_store_id);
  const parentQueries = useQueryClient();
  const queries = useMemo(() => scopeID ? new QueryClient({ defaultOptions: parentQueries.getDefaultOptions() })
    : parentQueries, [scopeID, client.baseURL, parentQueries]);
  return <QueryClientProvider client={queries}><V2RecoveryProvider key={JSON.stringify([scopeID, client.baseURL])} client={client} scopeID={scopeID}>
    <V2WorkbenchContent client={client} />
  </V2RecoveryProvider></QueryClientProvider>;
}

function V2WorkbenchContent({ client }: { client: CyberAgentClient }) {
  useV2RestoreTurns();
  const recovery = useV2RecoveryStore();
  const persistenceWarning = useV2PersistenceWarning();
  useEffect(() => {
    document.documentElement.dataset.prayuDensity = readDensity();
  }, []);
  const queryClient = useQueryClient();
  const previousThreadID = useConnectionStore((state) => state.selectedThreadID);
  const selectThread = useConnectionStore((state) => state.selectThread);
  const navigation = useV2Navigation();
  const { route, navigate } = navigation;
  const selectedThreadID = route.threadID ?? "";
  const surface = route.section ? "settings" : "conversation";
  const view = route.view ?? "conversation";
  const settingsSection = route.section ?? "general";
  const newConversation = route.kind !== "thread";
  const [sidebarVisible, setSidebarVisible] = useState(() => !window.matchMedia?.("(max-width: 760px)").matches);
  const [searchOpen, setSearchOpen] = useState(false);
  const [storedWorkspace, setWorkspaceID] = useV2PersistentState<unknown>("workspace", "");
  const workspaceID = typeof storedWorkspace === "string" ? storedWorkspace : "";
  // An explicitly imported existing project may be outside the first list page.
  // Retain its pathless receipt so selection survives list refetches.
  const [importedWorkspaces, setImportedWorkspaces] = useState<WorkspaceView[]>([]);
  const [drafts, setDrafts] = useV2Drafts();
  const creationAttemptRef = useRef<CreationAttempt>(new Map());
  const [newThreadOptions, setNewThreadOptions] = useState<NewThreadOptions>({
    networkMode: "disabled", allowedTargets: [], modelRoute: null,
  });
  const draftKey = newConversation || !selectedThreadID ? `new:${workspaceID}` : `thread:${selectedThreadID}`;
  const updateDraft = (content: string, expected?: string) => setDrafts((current) =>
    expected !== undefined && (current[draftKey] ?? "") !== expected ? current
      : { ...current, [draftKey]: content });
  const selectWorkspace = (id: string) => {
    if (!workspaceID) setDrafts((current) => ({ ...current,
      [`new:${id}`]: current[`new:${id}`] ?? current["new:"] ?? "" }));
    setWorkspaceID(id);
  };
  const [archiveCandidate, setArchiveCandidate] = useState<ThreadView | null>(null);
  const workspacesQuery = useInfiniteQuery({
    queryKey: v2QueryKeys.workspaces,
    queryFn: ({ signal, pageParam }) => client.getPage<WorkspaceView>("/workspaces", { limit: 100 }, pageParam, signal),
    initialPageParam: "", getNextPageParam: (last) => last.page.next_cursor || undefined,
    staleTime: 30_000,
  });
  const threadsQuery = useInfiniteQuery({
    queryKey: v2QueryKeys.threads("active"),
    queryFn: ({ signal, pageParam }) => client.getPage<ThreadView>("/threads",
      { limit: 100, status: "active" }, pageParam, signal),
    initialPageParam: "", getNextPageParam: (last) => last.page.next_cursor || undefined,
    // Historical pages are refreshed on explicit refresh or durable changes,
    // not repeatedly polled while the user is browsing a large archive.
    refetchInterval: (query) => (query.state.data?.pages.length ?? 1) === 1 ? 4_000 : false,
  });
  const workspaces = useMemo(() => [...new Map([...importedWorkspaces,
    ...(workspacesQuery.data?.pages.flatMap(({ items }) => items) ?? [])]
    .map((workspace) => [workspace.id, workspace])).values()], [workspacesQuery.data, importedWorkspaces]);
  const threads = useMemo(() => [...new Map(threadsQuery.data?.pages.flatMap(({ items }) =>
    items).map((thread) => [thread.id, thread])).values()], [threadsQuery.data]);
  useEffect(() => {
    if (!workspaceID && workspaces[0]) {
      const id = workspaces[0].id;
      setDrafts((current) => ({ ...current, [`new:${id}`]: current[`new:${id}`] ?? current["new:"] ?? "" }));
      setWorkspaceID(id);
    }
  }, [workspaceID, workspaces]);
  useEffect(() => {
    if (route.kind !== "initial" || !threadsQuery.isSuccess) return;
    const saved = recovery?.read<unknown>("route", "");
    if (typeof saved === "string" && saved) {
      const restored = readV2Route(saved);
      if (restored.kind === "thread" || restored.kind === "new") { navigate(restored, true); return; }
    }
    const id = previousThreadID || threads[0]?.id;
    navigate(id ? { kind: "thread", threadID: id } : { kind: "new" }, true);
  }, [route.kind, previousThreadID, threads, threadsQuery.isSuccess, navigate, recovery]);
  useEffect(() => {
    if (route.kind === "initial" || route.kind === "invalid") return;
    try { recovery?.write("route", window.location.hash); } catch { /* Visible save notice. */ }
  }, [route, recovery]);
  useEffect(() => {
    if (route.kind !== "initial") selectThread(selectedThreadID);
  }, [route.kind, selectedThreadID, selectThread]);
  const archiveMutation = useMutation({
    mutationFn: (thread: ThreadView) => client.transitionThread(thread.id, "archive", {
      version: "thread_lifecycle.v1", expected_version: thread.version,
    }, `v2-thread-archive-${globalThis.crypto.randomUUID()}`),
    onSuccess: (result) => {
      const fallback = threads.find(({ id }) => id !== result.thread.id);
      if (selectedThreadID === result.thread.id) {
        navigate(fallback ? { kind: "thread", threadID: fallback.id } : { kind: "new" }, true);
      }
      setArchiveCandidate(null);
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") });
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("archived") });
    },
  });
  const closeNavigationSidebar = () => {
    if (window.matchMedia?.("(max-width: 760px)").matches) setSidebarVisible(false);
  };
  const openConversation = (threadID: string) => {
    navigate({ kind: "thread", threadID, ...(route.view ? { view: route.view } : {}) });
    closeNavigationSidebar();
  };
  const startNew = () => {
    const currentWorkspace = queryClient.getQueryData<ThreadDetailView>(
      v2QueryKeys.thread(selectedThreadID))?.thread.workspace_id ??
      threads.find(({ id }) => id === selectedThreadID)?.workspace_id;
    if (currentWorkspace) selectWorkspace(currentWorkspace);
    navigate({ kind: "new" });
    closeNavigationSidebar();
  };
  const setSettingsSection = (section: V2SettingsSection) => {
    navigate({ ...route, kind: selectedThreadID ? "thread" : "new",
      ...(selectedThreadID ? { threadID: selectedThreadID } : {}), section }, Boolean(route.section));
    closeNavigationSidebar();
  };
  const goBack = () => { navigation.back(); closeNavigationSidebar(); };
  const returnFromSettings = () => {
    navigate({ ...route, section: undefined }, true);
    closeNavigationSidebar();
  };
  const changeView = (next: "conversation" | "inspector") => {
    navigate({ kind: selectedThreadID ? "thread" : "new",
      ...(selectedThreadID ? { threadID: selectedThreadID } : {}),
      ...(next === "inspector" ? { view: "inspector" } : {}) });
    closeNavigationSidebar();
  };
  const openSettings = () => setSettingsSection("general");
  const openModels = () => setSettingsSection("models");
  const openInspector = () => changeView("inspector");
  const openTool = (tool: "run" | "session" | "schedule", resourceID?: string) => navigate({
    kind: selectedThreadID ? "thread" : "new", ...(selectedThreadID ? { threadID: selectedThreadID } : {}),
    view: "inspector", tool, ...(resourceID ? { resourceID } : {}),
  });

  return <div className={`v2-shell${sidebarVisible ? " has-sidebar" : " no-sidebar"}`}>
    <V2Titlebar canGoBack={navigation.canGoBack} onBack={surface === "settings" ? returnFromSettings : goBack}
      view={view} onChangeView={changeView}
      onNewConversation={startNew} onOpenSettings={openSettings}
      onToggleSidebar={() => setSidebarVisible((visible) => !visible)} sidebarVisible={sidebarVisible} />
    {sidebarVisible && <button aria-label="关闭侧栏" className="v2-sidebar-backdrop"
      onClick={() => setSidebarVisible(false)} type="button" />}
    <div className="v2-shell-body">
      {sidebarVisible && (surface === "settings" ? <V2SettingsSidebar onBack={returnFromSettings}
        onSelect={setSettingsSection} section={settingsSection} /> : <V2Sidebar
          onArchive={setArchiveCandidate} onNewConversation={startNew} onOpenModels={openModels}
          onOpenSettings={openSettings}
          onSearchOpen={setSearchOpen} onSelectThread={openConversation} searchOpen={searchOpen}
          hasMore={threadsQuery.hasNextPage} loading={threadsQuery.isLoading}
          loadingMore={threadsQuery.isFetchingNextPage} loadFailed={threadsQuery.isError}
          onLoadMore={() => void threadsQuery.fetchNextPage()} onRefresh={() => void threadsQuery.refetch()}
          selectedThreadID={newConversation ? "" : selectedThreadID} threads={threads} workspaces={workspaces} />)}
      <div className="v2-product-surface">
        {route.kind === "invalid" ? <div className="v2-notice" role="alert">任务地址无法识别。
          <button onClick={startNew} type="button">打开新对话</button></div>
          : surface === "settings" ? <V2Settings client={client} onOpenInspector={openInspector}
          onSelectSection={setSettingsSection} onOpenThread={openConversation} section={settingsSection}
          threadID={selectedThreadID} workspaces={workspaces} /> : route.tool
          ? <V2InspectorTools client={client} tool={route.tool} resourceID={route.resourceID}
            threadID={selectedThreadID} onBack={openInspector} onOpenSettings={setSettingsSection} />
          : view === "inspector" && !selectedThreadID
          ? <V2InspectorHome client={client} onOpenTool={openTool} onOpenSettings={setSettingsSection} />
          : newConversation || !selectedThreadID
          ? <NewConversation client={client} draft={drafts[draftKey] ?? ""} onDraftChange={updateDraft}
            creationAttemptRef={creationAttemptRef}
            options={newThreadOptions} onOptionsChange={setNewThreadOptions}
            onCreated={(thread, submittedDraft, files, images = [], version, attachments = []) => {
              const source = `new:${thread.workspace_id}`;
              const target = `thread:${thread.id}`;
              // Move the submitted version into the durable Thread's draft.
              // Anything typed or selected during creation stays in its project.
              if (!version) {
              setDrafts((current) => ({ ...current,
                [source]: current[source] === submittedDraft ? "" : current[source] ?? "",
                [target]: current[target] ?? submittedDraft }));
              queryClient.setQueryData<V2FileReference[]>(v2FileReferenceKey(thread.workspace_id ?? "", thread.id),
                (current) => [...(current ?? []), ...files.filter((file) =>
                  !current?.some(({ path }) => path === file.path))]);
              queryClient.setQueryData<V2FileReference[]>(v2FileReferenceKey(thread.workspace_id ?? "", ""),
                (current) => current?.filter(({ id }) => !files.some((file) => file.id === id)));
              queryClient.setQueryData<WorkspaceImageAttachment[]>(v2ImageReferenceKey(thread.workspace_id ?? "", thread.id),
                (current) => [...(current ?? []), ...images.filter((image) => !current?.some(({ id }) => id === image.id))]);
              queryClient.setQueryData<WorkspaceImageAttachment[]>(v2ImageReferenceKey(thread.workspace_id ?? "", ""),
                (current) => current?.filter(({ id }) => !images.some((image) => image.id === id)));
              queryClient.setQueryData<WorkspaceFileAttachment[]>(v2AttachmentReferenceKey(thread.workspace_id ?? "", thread.id),
                (current) => [...(current ?? []), ...attachments.filter((file) => !current?.some(({ id }) => id === file.id))]);
              queryClient.setQueryData<WorkspaceFileAttachment[]>(v2AttachmentReferenceKey(thread.workspace_id ?? "", ""),
                (current) => current?.filter(({ id }) => !attachments.some((file) => file.id === id)));
              }
              openConversation(thread.id);
              void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") });
            }} onTurnSuccess={(threadID, submittedDraft) => setDrafts((current) =>
              current[`thread:${threadID}`] === submittedDraft
                ? { ...current, [`thread:${threadID}`]: "" } : current)}
            onManageModels={openModels} onWorkspaceChange={selectWorkspace}
            onImported={(workspace, currentDraft) => {
              setImportedWorkspaces((current) => [...current.filter(({ id }) => id !== workspace.id), workspace]);
              if (recovery) {
                const document = getV2DraftDocument(recovery);
                const scope = v2DraftScope(workspace.id);
                const target = readV2Draft(document, recovery, scope);
                if (!target.ref && !target.snapshot.text && !target.snapshot.files.length && !target.snapshot.images.length && !target.snapshot.attachments?.length)
                  document.update(scope, { text: currentDraft ?? "" }, null);
              } else setDrafts((current) => ({ ...current,
                [`new:${workspace.id}`]: current[`new:${workspace.id}`] ?? currentDraft ?? current[draftKey] ?? "" }));
              selectWorkspace(workspace.id);
            }}
            moreProjects={workspacesQuery.hasNextPage && <button className="v2-composer-chip"
              disabled={workspacesQuery.isFetchingNextPage} onClick={() => void workspacesQuery.fetchNextPage()}
              type="button">加载更多项目</button>}
            workspaceID={workspaceID} workspaces={workspaces} />
          : <V2Conversation client={client} onArchive={setArchiveCandidate}
            onOpenWorktree={(workspace) => {
              setImportedWorkspaces((current) => [...current.filter(({ id }) => id !== workspace.id), workspace]);
              selectWorkspace(workspace.id);
              setNewThreadOptions({ networkMode: "disabled", allowedTargets: [], modelRoute: null });
              navigate({ kind: "new" });
              closeNavigationSidebar();
            }}
            view={view} onOpenTool={openTool}
            onOpenInspectorHome={() => navigate({ kind: "new", view: "inspector" })}
            draft={drafts[draftKey] ?? ""} onDraftChange={updateDraft}
            onManageModels={openModels} onOpenInspector={openInspector}
            threadID={selectedThreadID} workspaces={workspaces} />}
      </div>
    </div>
    {(workspacesQuery.isError || threadsQuery.isError) && <div className="v2-toast" role="alert">
      项目或对话列表加载失败，已有内容会保留。
      <button onClick={() => { void workspacesQuery.refetch(); void threadsQuery.refetch(); }} type="button">重试加载</button>
    </div>}
    {persistenceWarning && <div className="v2-toast" role={recovery ? "alert" : "status"}>{persistenceWarning}</div>}
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
