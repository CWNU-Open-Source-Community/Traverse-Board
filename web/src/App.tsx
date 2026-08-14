import { useEffect, useMemo, useState, type CSSProperties } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  LogOut,
  Minus,
  PanelLeft,
  RefreshCw,
  Settings,
  Square,
  X,
} from "lucide-react";
import { CyberAgentClient } from "./api/client";
import { ConnectionGate } from "./components/connection-gate";
import { DesktopSkillPreviewDialog } from "./components/desktop-skill-preview";
import { ModelAvailabilityDialog, ModelAvailabilityWorkspace } from "./components/model-availability-dialog";
import { RunCreationDialog } from "./components/run-creation-dialog";
import { ResourceSidebar, type WorkbenchSection } from "./components/resource-sidebar";
import { RunWorkspace } from "./components/run-workspace";
import { SessionWorkspace } from "./components/session-workspace";
import { SettingsView, type SettingsCapability } from "./components/settings-view";
import { EmptyConversation, SidebarResizeHandle, UtilityWorkspace,
  WorkbenchFrame, clampSidebarWidth, defaultSidebarWidth,
  type NewRunDraft } from "./components/workbench-frame";
import { desktopBridgeAvailable, desktopIsMacPlatform } from "./lib/desktop-bridge";
import { useLocale } from "./lib/locale";
import { closeDesktopWindow, minimiseDesktopWindow,
  toggleDesktopWindowMaximised } from "./lib/desktop-window";
import { useConnectionStore } from "./state/connection";

const sidebarWidthStorageKey = "prayu.sidebar.width.v1";

export default function App() {
  const token = useConnectionStore((state) => state.token);
  const controlToken = useConnectionStore((state) => state.controlToken);
  const runControlEnabled = useConnectionStore((state) => state.runControlEnabled);
  const executionPermissionControlEnabled = useConnectionStore(
    (state) => state.executionPermissionControlEnabled);
  const browserCDPPermissionControlEnabled = useConnectionStore(
    (state) => state.browserCDPPermissionControlEnabled);
  const fullCDPDebugEnabled = useConnectionStore((state) => state.fullCDPDebugEnabled);
  const operatorApprovalEnabled = useConnectionStore((state) => state.operatorApprovalEnabled);
  const dangerFullAccessEnabled = useConnectionStore((state) => state.dangerFullAccessEnabled);
  const debugMaximumAccessEnabled = useConnectionStore((state) => state.debugMaximumAccessEnabled);
  const runCreationEnabled = useConnectionStore((state) => state.runCreationEnabled);
  const sessionMessageEnabled = useConnectionStore((state) => state.sessionMessageEnabled);
  const sessionSteeringControlEnabled = useConnectionStore(
    (state) => state.sessionSteeringControlEnabled);
  const runLifecycleEnabled = useConnectionStore((state) => state.runLifecycleEnabled);
  const runExecutionEnabled = useConnectionStore((state) => state.runExecutionEnabled);
  const planDeliveryControlEnabled = useConnectionStore(
    (state) => state.planDeliveryControlEnabled);
  const approvalControlEnabled = useConnectionStore((state) => state.approvalControlEnabled);
  const controlledCommandProposalControlEnabled = useConnectionStore(
    (state) => state.controlledCommandProposalControlEnabled);
  const hostCommandProposalControlEnabled = useConnectionStore(
    (state) => state.hostCommandProposalControlEnabled);
  const modelControlEnabled = useConnectionStore((state) => state.modelControlEnabled);
  const providerCredentialEnabled = useConnectionStore((state) => state.providerCredentialEnabled);
  const fileEditReviewEnabled = useConnectionStore((state) => state.fileEditReviewEnabled);
  const fileEditProposalEnabled = useConnectionStore((state) => state.fileEditProposalEnabled);
  const fileEditApplyEnabled = useConnectionStore((state) => state.fileEditApplyEnabled);
  const runWakeControlEnabled = useConnectionStore((state) => state.runWakeControlEnabled);
  const runWakeExecutionEnabled = useConnectionStore((state) => state.runWakeExecutionEnabled);
  const runWakeWorkerEnabled = useConnectionStore((state) => state.runWakeWorkerEnabled);
  const skillInstallationEnabled = useConnectionStore((state) => state.skillInstallationEnabled);
  const evidenceAttachmentEnabled = useConnectionStore((state) => state.evidenceAttachmentEnabled);
  const verificationEvidenceEnabled = useConnectionStore(
    (state) => state.verificationEvidenceEnabled);
  const embeddedAnalyzerExecutionEnabled = useConnectionStore(
    (state) => state.embeddedAnalyzerExecutionEnabled);
  if (!token) {
    return <ConnectionGate />;
  }
  return <ConnectedWorkbench token={token} controlToken={controlToken}
    runControlEnabled={runControlEnabled} runCreationEnabled={runCreationEnabled}
    executionPermissionControlEnabled={executionPermissionControlEnabled}
    browserCDPPermissionControlEnabled={browserCDPPermissionControlEnabled}
    fullCDPDebugEnabled={fullCDPDebugEnabled}
    operatorApprovalEnabled={operatorApprovalEnabled}
    dangerFullAccessEnabled={dangerFullAccessEnabled}
    debugMaximumAccessEnabled={debugMaximumAccessEnabled}
    sessionMessageEnabled={sessionMessageEnabled}
    sessionSteeringControlEnabled={sessionSteeringControlEnabled}
    runLifecycleEnabled={runLifecycleEnabled} runExecutionEnabled={runExecutionEnabled}
    planDeliveryControlEnabled={planDeliveryControlEnabled}
    approvalControlEnabled={approvalControlEnabled} modelControlEnabled={modelControlEnabled}
    controlledCommandProposalControlEnabled={controlledCommandProposalControlEnabled}
    hostCommandProposalControlEnabled={hostCommandProposalControlEnabled}
    providerCredentialEnabled={providerCredentialEnabled}
    fileEditReviewEnabled={fileEditReviewEnabled} fileEditApplyEnabled={fileEditApplyEnabled}
    fileEditProposalEnabled={fileEditProposalEnabled}
    runWakeControlEnabled={runWakeControlEnabled}
    runWakeExecutionEnabled={runWakeExecutionEnabled}
    runWakeWorkerEnabled={runWakeWorkerEnabled}
    skillInstallationEnabled={skillInstallationEnabled}
    evidenceAttachmentEnabled={evidenceAttachmentEnabled}
    verificationEvidenceEnabled={verificationEvidenceEnabled}
    embeddedAnalyzerExecutionEnabled={embeddedAnalyzerExecutionEnabled} />;
}

function ConnectedWorkbench({ token, controlToken, runControlEnabled, runCreationEnabled,
  executionPermissionControlEnabled, operatorApprovalEnabled, dangerFullAccessEnabled,
  debugMaximumAccessEnabled, browserCDPPermissionControlEnabled, fullCDPDebugEnabled,
  sessionMessageEnabled, sessionSteeringControlEnabled, runLifecycleEnabled,
  runExecutionEnabled, planDeliveryControlEnabled, approvalControlEnabled,
  controlledCommandProposalControlEnabled,
  hostCommandProposalControlEnabled,
  modelControlEnabled, providerCredentialEnabled, fileEditReviewEnabled,
  fileEditProposalEnabled, fileEditApplyEnabled, runWakeControlEnabled,
  runWakeExecutionEnabled, runWakeWorkerEnabled, skillInstallationEnabled,
  evidenceAttachmentEnabled, verificationEvidenceEnabled,
  embeddedAnalyzerExecutionEnabled }: {
  token: string;
  controlToken: string;
  runControlEnabled: boolean;
  runCreationEnabled: boolean;
  executionPermissionControlEnabled: boolean;
  browserCDPPermissionControlEnabled: boolean;
  fullCDPDebugEnabled: boolean;
  operatorApprovalEnabled: boolean;
  dangerFullAccessEnabled: boolean;
  debugMaximumAccessEnabled: boolean;
  sessionMessageEnabled: boolean;
  sessionSteeringControlEnabled: boolean;
  runLifecycleEnabled: boolean;
  runExecutionEnabled: boolean;
  planDeliveryControlEnabled: boolean;
  approvalControlEnabled: boolean;
  controlledCommandProposalControlEnabled: boolean;
  hostCommandProposalControlEnabled: boolean;
  modelControlEnabled: boolean;
  providerCredentialEnabled: boolean;
  fileEditReviewEnabled: boolean;
  fileEditProposalEnabled: boolean;
  fileEditApplyEnabled: boolean;
  runWakeControlEnabled: boolean;
  runWakeExecutionEnabled: boolean;
  runWakeWorkerEnabled: boolean;
  skillInstallationEnabled: boolean;
  evidenceAttachmentEnabled: boolean;
  verificationEvidenceEnabled: boolean;
  embeddedAnalyzerExecutionEnabled: boolean;
}) {
  const { t } = useLocale();
  const [surface, setSurface] = useState<"workspace" | "settings">("workspace");
  const [sidebarVisible, setSidebarVisible] = useState(true);
  const [skillPreviewOpen, setSkillPreviewOpen] = useState(false);
  const [runCreationOpen, setRunCreationOpen] = useState(false);
  const [runDraft, setRunDraft] = useState<Partial<NewRunDraft>>({});
  const [modelsOpen, setModelsOpen] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(readSidebarWidth);
  const [workspaceSection, setWorkspaceSection] = useState<Exclude<WorkbenchSection, "new-task">>(
    "conversation");
  const desktop = desktopBridgeAvailable();
  const macTitlebar = desktopIsMacPlatform();
  const client = useMemo(() => new CyberAgentClient(token, undefined, controlToken, {
    runControlEnabled, runCreationEnabled, sessionMessageEnabled,
    executionPermissionControlEnabled, operatorApprovalEnabled,
    dangerFullAccessEnabled, debugMaximumAccessEnabled,
    browserCDPPermissionControlEnabled, fullCDPDebugEnabled,
    sessionSteeringControlEnabled,
    runLifecycleEnabled, runExecutionEnabled,
    planDeliveryControlEnabled, approvalControlEnabled, modelControlEnabled,
    controlledCommandProposalControlEnabled,
    hostCommandProposalControlEnabled,
    providerCredentialEnabled, fileEditReviewEnabled, fileEditProposalEnabled,
    fileEditApplyEnabled, runWakeControlEnabled, runWakeExecutionEnabled,
    runWakeWorkerEnabled, skillInstallationEnabled, evidenceAttachmentEnabled,
    verificationEvidenceEnabled,
    embeddedAnalyzerExecutionEnabled,
  }), [token, controlToken, runControlEnabled, runCreationEnabled,
    executionPermissionControlEnabled, operatorApprovalEnabled,
    dangerFullAccessEnabled, debugMaximumAccessEnabled,
    browserCDPPermissionControlEnabled, fullCDPDebugEnabled, sessionMessageEnabled,
    sessionSteeringControlEnabled, runLifecycleEnabled, runExecutionEnabled,
    planDeliveryControlEnabled, approvalControlEnabled, modelControlEnabled,
    controlledCommandProposalControlEnabled,
    hostCommandProposalControlEnabled,
    providerCredentialEnabled, fileEditReviewEnabled, fileEditProposalEnabled,
    fileEditApplyEnabled, runWakeControlEnabled, runWakeExecutionEnabled,
    runWakeWorkerEnabled, skillInstallationEnabled, evidenceAttachmentEnabled,
    verificationEvidenceEnabled, embeddedAnalyzerExecutionEnabled]);
  const queryClient = useQueryClient();
  const health = useConnectionStore((state) => state.health);
  const setHealth = useConnectionStore((state) => state.setHealth);
  const disconnect = useConnectionStore((state) => state.disconnect);
  const resourceKind = useConnectionStore((state) => state.resourceKind);
  const selectedRunID = useConnectionStore((state) => state.selectedRunID);
  const selectedSessionID = useConnectionStore((state) => state.selectedSessionID);
  const healthQuery = useQuery({
    queryKey: ["health"],
    queryFn: ({ signal }) => client.health(signal),
    initialData: health ?? undefined,
    refetchInterval: 30_000,
  });
  const settingsCapabilities = useMemo<SettingsCapability[]>(() => [
    { id: "run-control", label: t("执行档位", "Execution profile"), enabled: runControlEnabled },
    { id: "permission-control", label: t("权限档位", "Permission profile"), enabled: executionPermissionControlEnabled },
    { id: "operator-approval", label: t("用户审批", "Operator approval"), enabled: operatorApprovalEnabled },
    { id: "full-access", label: t("完全访问", "Full access"), enabled: dangerFullAccessEnabled },
    { id: "debug-access", label: t("调试权限", "Debug access"), enabled: debugMaximumAccessEnabled },
    { id: "run-creation", label: t("创建任务", "Create task"), enabled: runCreationEnabled },
    { id: "session-message", label: t("会话消息", "Session messages"), enabled: sessionMessageEnabled },
    { id: "steering", label: t("队列引导", "Queue steering"), enabled: sessionSteeringControlEnabled },
    { id: "lifecycle", label: t("Run 生命周期", "Run lifecycle"), enabled: runLifecycleEnabled },
    { id: "execution", label: t("有界执行", "Bounded execution"), enabled: runExecutionEnabled },
    { id: "plan-delivery", label: t("计划交付", "Plan delivery"), enabled: planDeliveryControlEnabled },
    { id: "approval", label: t("审批", "Approvals"), enabled: approvalControlEnabled },
    { id: "command-proposals", label: t("固定命令审批", "Fixed command approval"),
      enabled: controlledCommandProposalControlEnabled },
    { id: "host-command-proposals", label: t("宿主机命令审批", "Host command approval"),
      enabled: hostCommandProposalControlEnabled },
    { id: "model", label: t("模型配置", "Model configuration"), enabled: modelControlEnabled },
    { id: "credentials", label: t("系统凭证", "System credentials"), enabled: providerCredentialEnabled },
    { id: "edit-review", label: t("编辑审阅", "Edit review"), enabled: fileEditReviewEnabled },
    { id: "edit-proposal", label: t("编辑提案", "Edit proposals"), enabled: fileEditProposalEnabled },
    { id: "edit-apply", label: t("应用编辑", "Apply edits"), enabled: fileEditApplyEnabled },
    { id: "wake", label: t("Wake 队列", "Wake queue"), enabled: runWakeControlEnabled },
    { id: "wake-execution", label: t("Wake 执行", "Wake execution"), enabled: runWakeExecutionEnabled },
    { id: "wake-worker", label: t("Wake 工作进程", "Wake worker"), enabled: runWakeWorkerEnabled },
    { id: "skill-install", label: t("Skill 安装", "Skill installation"), enabled: skillInstallationEnabled },
    { id: "evidence", label: t("证据挂载", "Evidence attachment"), enabled: evidenceAttachmentEnabled },
    { id: "verification", label: t("验证证据", "Verification evidence"), enabled: verificationEvidenceEnabled },
    { id: "embedded-analyzer", label: t("内置分析器", "Embedded analyzer"), enabled: embeddedAnalyzerExecutionEnabled },
  ], [approvalControlEnabled, controlledCommandProposalControlEnabled,
    hostCommandProposalControlEnabled,
    dangerFullAccessEnabled, debugMaximumAccessEnabled,
    evidenceAttachmentEnabled, executionPermissionControlEnabled, fileEditApplyEnabled,
    fileEditProposalEnabled, fileEditReviewEnabled, modelControlEnabled,
    operatorApprovalEnabled, planDeliveryControlEnabled, providerCredentialEnabled, runControlEnabled,
    runCreationEnabled, runExecutionEnabled, runLifecycleEnabled,
    runWakeControlEnabled, runWakeExecutionEnabled, runWakeWorkerEnabled,
    sessionMessageEnabled, sessionSteeringControlEnabled, skillInstallationEnabled,
    verificationEvidenceEnabled, embeddedAnalyzerExecutionEnabled, t]);

  useEffect(() => {
    if (healthQuery.data) {
      setHealth(healthQuery.data);
    }
  }, [healthQuery.data, setHealth]);

  const leave = () => {
    setSurface("workspace");
    setWorkspaceSection("conversation");
    setSkillPreviewOpen(false);
    setRunCreationOpen(false);
    setModelsOpen(false);
    queryClient.clear();
    disconnect();
  };

  const openRunCreation = (draft: Partial<NewRunDraft> = {}) => {
    setSurface("workspace");
    setWorkspaceSection("conversation");
    setRunDraft(draft);
    setRunCreationOpen(true);
  };

  const resizeSidebar = (value: number) => {
    const normalized = clampSidebarWidth(value);
    setSidebarWidth(normalized);
    try {
      localStorage.setItem(sidebarWidthStorageKey, String(normalized));
    } catch {
      // Window geometry remains usable when browser storage is unavailable.
    }
  };

  const navigateWorkspace = (section: WorkbenchSection) => {
    setSurface("workspace");
    if (section === "new-task") {
      openRunCreation();
      return;
    }
    setWorkspaceSection(section);
    if (section === "plugins" && desktop) setSkillPreviewOpen(true);
  };

  const selectedResourceID = resourceKind === "run" ? selectedRunID : selectedSessionID;
  const panelTitle = workspaceSection === "conversation"
    ? selectedResourceID
      ? `${resourceKind === "run" ? t("任务", "Task") : t("对话", "Conversation")} / ${selectedResourceID.slice(0, 18)}`
      : t("Prayu 工作台", "Prayu Workbench")
    : workspaceSection === "pull-requests" ? t("拉取请求", "Pull requests")
      : workspaceSection === "models" ? t("模型切换", "Models")
        : workspaceSection === "schedule" ? t("自动定时", "Scheduled tasks") : t("插件", "Plugins");

  const workspaceContent = workspaceSection === "models"
    ? <ModelAvailabilityWorkspace client={client} />
    : workspaceSection === "pull-requests" || workspaceSection === "schedule" ||
      workspaceSection === "plugins"
      ? <UtilityWorkspace kind={workspaceSection}
        onOpenPlugins={desktop ? () => setSkillPreviewOpen(true) : undefined} />
      : selectedResourceID
        ? resourceKind === "run"
          ? <RunWorkspace client={client} key={selectedRunID}
            onOpenPlugins={desktop ? () => setSkillPreviewOpen(true) : undefined}
            runID={selectedRunID} />
          : <SessionWorkspace client={client} key={selectedSessionID}
            onOpenPlugins={desktop ? () => setSkillPreviewOpen(true) : undefined}
            sessionID={selectedSessionID} />
        : <EmptyConversation client={client} creationEnabled={runCreationEnabled}
          onCreateRun={openRunCreation}
          onOpenPlugins={desktop ? () => setSkillPreviewOpen(true) : undefined} />;

  return (
    <>
      <div className={`app-shell prayu-shell ${surface === "settings" ? "settings-mode" : "workspace-mode"}`}>
        <header className={`topbar prayu-titlebar${macTitlebar ? " mac-titlebar" : ""}`}>
          <div className="titlebar-navigation">
            <button aria-label={t("显示或隐藏侧栏", "Show or hide sidebar")} className="titlebar-icon" disabled={surface === "settings"}
              onClick={() => setSidebarVisible((visible) => !visible)} title={t("显示或隐藏侧栏", "Show or hide sidebar")} type="button">
              <PanelLeft aria-hidden="true" size={16} />
            </button>
            <button aria-label={t("返回工作台", "Back to workbench")} className="titlebar-icon" disabled={surface === "workspace"}
              onClick={() => setSurface("workspace")} title={t("返回工作台", "Back to workbench")} type="button">
              <ArrowLeft aria-hidden="true" size={16} />
            </button>
            <button aria-label={t("前进", "Forward")} className="titlebar-icon" disabled title={t("前进", "Forward")} type="button">
              <ArrowRight aria-hidden="true" size={16} />
            </button>
            <nav aria-label={t("应用菜单", "Application menu")} className="titlebar-menu">
              <button disabled={!runCreationEnabled} onClick={() => openRunCreation()}
                title={t("新建任务", "New task")} type="button">{t("文件", "File")}</button>
              <button onClick={() => navigateWorkspace("models")} title={t("模型与 Provider", "Models and providers")}
                type="button">{t("编辑", "Edit")}</button>
              <button disabled={surface === "settings"}
                onClick={() => setSidebarVisible((visible) => !visible)} title={t("切换侧栏", "Toggle sidebar")} type="button">{t("视图", "View")}</button>
              <button onClick={() => setSurface("settings")} title={t("设置与关于", "Settings and about")} type="button">{t("帮助", "Help")}</button>
            </nav>
          </div>
          <div className="topbar-actions">
            <span className={`health-indicator ${healthQuery.isError ? "offline" : "online"}`}>
              <i />{healthQuery.isError ? t("API 错误", "API error") : `api.v1 / schema ${healthQuery.data?.schema_version ?? "-"}`}
            </span>
            <button aria-label={t("刷新", "Refresh")} className="icon-button" disabled={healthQuery.isFetching} onClick={() => void healthQuery.refetch()} title={t("刷新", "Refresh")} type="button">
              <RefreshCw aria-hidden="true" className={healthQuery.isFetching ? "spin" : ""} size={16} />
            </button>
            <button aria-label={t("设置", "Settings")} className="icon-button" onClick={() => setSurface("settings")} title={t("设置", "Settings")} type="button">
              <Settings aria-hidden="true" size={16} />
            </button>
            {!desktop && <button aria-label={t("断开连接", "Disconnect")} className="icon-button" onClick={leave} title={t("断开连接", "Disconnect")} type="button"><LogOut aria-hidden="true" size={16} /></button>}
            {desktop && <div aria-label={t("窗口控制", "Window controls")} className="desktop-window-controls" role="group">
              <button aria-label={t("最小化", "Minimize")} onClick={minimiseDesktopWindow} title={t("最小化", "Minimize")} type="button">
                <Minus aria-hidden="true" size={15} />
              </button>
              <button aria-label={t("最大化或还原", "Maximize or restore")} onClick={toggleDesktopWindowMaximised}
                title={t("最大化或还原", "Maximize or restore")} type="button">
                <Square aria-hidden="true" size={12} />
              </button>
              <button aria-label={t("关闭", "Close")} className="desktop-window-close" onClick={closeDesktopWindow}
                title={t("关闭", "Close")} type="button">
                <X aria-hidden="true" size={16} />
              </button>
            </div>}
          </div>
        </header>
        {surface === "workspace" ? <div className={`shell-body ${sidebarVisible ? "" : "sidebar-hidden"}`}
          style={{ "--prayu-sidebar-width": `${sidebarWidth}px` } as CSSProperties}>
          {sidebarVisible && <ResourceSidebar client={client}
            activeSection={runCreationOpen ? "new-task" : workspaceSection}
            onCreateRun={runCreationEnabled ? openRunCreation : undefined}
            onNavigate={navigateWorkspace}
            onOpenSettings={() => setSurface("settings")} />}
          {sidebarVisible && <SidebarResizeHandle onChange={resizeSidebar} value={sidebarWidth} />}
          <WorkbenchFrame client={client} desktop={desktop} resourceKind={resourceKind}
            runID={selectedRunID} sessionID={selectedSessionID}
            title={panelTitle}>
            {workspaceContent}
          </WorkbenchFrame>
        </div> : <SettingsView capabilities={settingsCapabilities} client={client} desktop={desktop}
          health={healthQuery.data ?? health ?? null} onBack={() => setSurface("workspace")}
          onOpenModels={() => setModelsOpen(true)}
          onOpenSkills={() => setSkillPreviewOpen(true)} selectedRunID={selectedRunID} />}
      </div>
      <DesktopSkillPreviewDialog installationEnabled={skillInstallationEnabled}
        open={skillPreviewOpen} onClose={() => setSkillPreviewOpen(false)} />
      <ModelAvailabilityDialog client={client} open={modelsOpen}
        onClose={() => setModelsOpen(false)} />
      <RunCreationDialog client={client} open={runCreationOpen}
        initialGoal={runDraft.goal} initialPhase={runDraft.phase}
        onClose={() => setRunCreationOpen(false)} />
    </>
  );
}

function readSidebarWidth(): number {
  try {
    const stored = Number(localStorage.getItem(sidebarWidthStorageKey));
    return Number.isFinite(stored) && stored > 0
      ? clampSidebarWidth(stored) : defaultSidebarWidth;
  } catch {
    return defaultSidebarWidth;
  }
}
