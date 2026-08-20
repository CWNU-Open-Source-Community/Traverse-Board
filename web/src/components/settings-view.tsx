import { useEffect, useMemo, useRef, useState, type CSSProperties,
  type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Ban,
  CircleUserRound,
  Cpu,
  Info,
  LoaderCircle,
  Keyboard,
  Languages,
  Layers3,
  Moon,
  PackageSearch,
  Palette,
  PlugZap,
  RefreshCw,
  Search,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Sun,
} from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { ExtensionMCPServerView, ExtensionPluginInstallationView,
  HealthView } from "../api/types";
import { applyPrayuTheme, readPrayuTheme, type PrayuTheme } from "../lib/appearance";
import { useLocale } from "../lib/locale";
import { applyRunNavigationMode, readRunNavigationMode,
  type RunNavigationMode } from "../lib/run-navigation";
import { PrayuBrand } from "./prayu-brand";
import { RunPermissionSettings } from "./run-permission-settings";
import { SafeWebReadinessPanel } from "./safe-web-readiness";
import { SidebarResizeHandle, clampSidebarWidth, defaultSidebarWidth } from "./workbench-frame";

export type SettingsCapability = {
  id: string;
  label: string;
  enabled: boolean;
};

type SettingsSection = "profile" | "general" | "permissions" | "appearance" |
  "workspace" | "shortcuts" | "extensions" | "about";
type Density = "comfortable" | "compact";

const densityStorageKey = "prayu.ui-density";
const settingsSidebarWidthStorageKey = "prayu.settings.sidebar.width.v1";

function readDensity(): Density {
  if (typeof window === "undefined") return "comfortable";
  try {
    return window.localStorage.getItem(densityStorageKey) === "compact"
      ? "compact" : "comfortable";
  } catch {
    return "comfortable";
  }
}

function persistDensity(density: Density) {
  try {
    window.localStorage.setItem(densityStorageKey, density);
  } catch {
    // Display preferences must never block the workbench.
  }
}

function readSettingsSidebarWidth(): number {
  if (typeof window === "undefined") return defaultSidebarWidth;
  try {
    const stored = Number(window.localStorage.getItem(settingsSidebarWidthStorageKey));
    return Number.isFinite(stored) && stored > 0
      ? clampSidebarWidth(stored) : defaultSidebarWidth;
  } catch {
    return defaultSidebarWidth;
  }
}

function WebSkillInstall({ client }: { client: CyberAgentClient }) {
  const { t } = useLocale();
  const inputRef = useRef<HTMLInputElement>(null);
  const [message, setMessage] = useState("");
  const install = useMutation({
    mutationFn: (file: File) => file.arrayBuffer().then((buffer) => {
      let binary = "";
      const bytes = new Uint8Array(buffer);
      const chunk = 0x8000;
      for (let i = 0; i < bytes.length; i += chunk) {
        binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
      }
      return client.installSkillPackage({
        version: "skill_package_installation.v1", archive_base64: btoa(binary),
        surface: "code", confirm_untrusted: true,
      }, `web-skill-install-${globalThis.crypto.randomUUID()}`);
    }),
    onSuccess: () => setMessage(t("Skill 包已安装", "Skill package installed")),
  });
  return <div className="settings-web-skill">
    <input accept="application/zip" hidden ref={inputRef} type="file"
      onChange={(event) => {
        const file = event.target.files?.[0];
        if (file) void install.mutate(file);
        event.currentTarget.value = "";
      }} />
    <button className="settings-action" disabled={install.isPending}
      onClick={() => inputRef.current?.click()} type="button">
      {install.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={15} /> : <PackageSearch aria-hidden="true" size={15} />}
      {t("安装 Skill 包", "Install Skill package")}
    </button>
    {message && <span className="projection-placeholder">{message}</span>}
    {install.error && <span className="inline-warning">{install.error instanceof Error ? install.error.message : t("安装失败", "Install failed")}</span>}
  </div>;
}

export function SettingsView({
  capabilities,
  client,
  desktop,
  health,
  selectedRunID,
  onBack,
  onOpenModels,
  onOpenSkills,
}: {
  capabilities: SettingsCapability[];
  client: CyberAgentClient;
  desktop: boolean;
  health: HealthView | null;
  selectedRunID: string;
  onBack: () => void;
  onOpenModels: () => void;
  onOpenSkills: () => void;
}) {
  const { t } = useLocale();
  const navigation: Array<{ id: SettingsSection; label: string; icon: typeof Settings }> = [
    { id: "general", label: t("常规", "General"), icon: Settings },
    { id: "profile", label: t("个人资料", "Profile"), icon: CircleUserRound },
    { id: "permissions", label: t("权限", "Permissions"), icon: ShieldCheck },
    { id: "appearance", label: t("外观", "Appearance"), icon: Palette },
    { id: "workspace", label: t("工作台", "Workbench"), icon: SlidersHorizontal },
    { id: "shortcuts", label: t("键盘快捷键", "Keyboard shortcuts"), icon: Keyboard },
    { id: "about", label: t("关于", "About"), icon: Info },
  ];
  const extensionNavigation = {
    id: "extensions" as const, label: t("MCP 与 Plugin", "MCP and Plugins"), icon: PlugZap,
  };
  const [section, setSection] = useState<SettingsSection>("general");
  const [query, setQuery] = useState("");
  const [density, setDensity] = useState<Density>(readDensity);
  const [theme, setTheme] = useState<PrayuTheme>(readPrayuTheme);
  const [runNavigationMode, setRunNavigationMode] =
    useState<RunNavigationMode>(readRunNavigationMode);
  const [sidebarWidth, setSidebarWidth] = useState(readSettingsSidebarWidth);
  const visibleNavigation = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return navigation.filter((item) => !normalized ||
      `${item.label} ${item.id}`.toLocaleLowerCase().includes(normalized));
  }, [navigation, query]);

  useEffect(() => {
    document.documentElement.dataset.prayuDensity = density;
    persistDensity(density);
  }, [density]);

  useEffect(() => {
    applyPrayuTheme(theme);
  }, [theme]);

  useEffect(() => {
    applyRunNavigationMode(runNavigationMode);
  }, [runNavigationMode]);

  const resizeSidebar = (value: number) => {
    const normalized = clampSidebarWidth(value);
    setSidebarWidth(normalized);
    try {
      window.localStorage.setItem(settingsSidebarWidthStorageKey, String(normalized));
    } catch {
      // Window geometry remains usable when browser storage is unavailable.
    }
  };

  return (
    <div className="settings-shell"
      style={{ "--prayu-settings-sidebar-width": `${sidebarWidth}px` } as CSSProperties}>
      <aside className="settings-sidebar">
        <button className="settings-back" onClick={onBack} type="button">
          <ArrowLeft aria-hidden="true" size={17} />{t("返回应用", "Back to app")}
        </button>
        <label className="settings-search">
          <Search aria-hidden="true" size={15} />
          <input aria-label={t("搜索设置", "Search settings")} onChange={(event) => setQuery(event.target.value)}
            placeholder={t("搜索设置...", "Search settings...")} type="search" value={query} />
        </label>
        <span className="settings-group-label">{t("个人", "Personal")}</span>
        <nav aria-label={t("Prayu 设置", "Prayu settings")}>
          {visibleNavigation.map(({ id, label, icon: Icon }) => (
            <button className={section === id ? "active" : ""} key={id}
              onClick={() => setSection(id)} type="button">
              <Icon aria-hidden="true" size={16} /><span>{label}</span>
            </button>
          ))}
        </nav>
        <span className="settings-group-label">{t("集成", "Integrations")}</span>
        <nav aria-label={t("Prayu 集成", "Prayu integrations")}>
          <button className={section === "extensions" ? "active" : ""}
            onClick={() => setSection("extensions")} type="button">
            <PlugZap aria-hidden="true" size={16} />
            <span>{extensionNavigation.label}</span>
          </button>
          <button onClick={onOpenModels} type="button">
            <Cpu aria-hidden="true" size={16} /><span>{t("模型与配置", "Models and providers")}</span>
          </button>
          <button onClick={onOpenSkills} type="button">
            <PackageSearch aria-hidden="true" size={16} /><span>{t("Skill 包", "Skill packages")}</span>
          </button>
          {!desktop && client.hasSkillInstallation && <WebSkillInstall client={client} />}
        </nav>
      </aside>
      <SidebarResizeHandle onChange={resizeSidebar} value={sidebarWidth} />
      <main className="settings-main">
        <header className="settings-header">
          <strong>{section === "extensions" ? extensionNavigation.label :
            navigation.find((item) => item.id === section)?.label}</strong>
          <div>
            <button className="settings-action" onClick={onOpenModels} type="button">
              <Cpu aria-hidden="true" size={15} />{t("模型", "Models")}
            </button>
            {desktop && <button className="settings-action" onClick={onOpenSkills} type="button">
              <PackageSearch aria-hidden="true" size={15} />Skill
            </button>}
          </div>
        </header>
        <div className="settings-scroll">
          {section === "profile" && <ProfileSettings capabilities={capabilities}
            desktop={desktop} health={health} client={client} />}
          {section === "general" && <GeneralSettings capabilities={capabilities}
            desktop={desktop} health={health} />}
          {section === "permissions" && <RunPermissionSettings client={client}
            runID={selectedRunID} />}
          {section === "appearance" && <AppearanceSettings density={density} theme={theme}
            onDensityChange={setDensity} onThemeChange={setTheme} />}
          {section === "workspace" && <WorkbenchSettings mode={runNavigationMode}
            onModeChange={setRunNavigationMode} />}
          {section === "shortcuts" && <ShortcutSettings />}
          {section === "extensions" && <ExtensionSettings client={client}
            selectedRunID={selectedRunID} />}
          {section === "about" && <AboutSettings desktop={desktop} health={health} />}
        </div>
      </main>
    </div>
  );
}

function WorkbenchSettings({ mode, onModeChange }: {
  mode: RunNavigationMode;
  onModeChange: (mode: RunNavigationMode) => void;
}) {
  const { t } = useLocale();
  return <section className="settings-page-section">
    <h1>{t("工作台", "Workbench")}</h1>
    <div className="appearance-setting-row">
      <div><strong>{t("Run 顶部导航", "Run top navigation")}</strong>
        <span>{t("高级诊断入口", "Advanced diagnostics")}</span></div>
      <div className="prayu-segmented" role="group"
        aria-label={t("Run 顶部导航", "Run top navigation")}>
        <button aria-pressed={mode === "compact"} onClick={() => onModeChange("compact")}
          type="button">{t("精简", "Compact")}</button>
        <button aria-pressed={mode === "diagnostic"}
          onClick={() => onModeChange("diagnostic")} type="button">
          {t("完整", "Diagnostic")}
        </button>
      </div>
    </div>
  </section>;
}

function ProfileSettings({ capabilities, desktop, health, client }: {
  capabilities: SettingsCapability[];
  desktop: boolean;
  health: HealthView | null;
  client: CyberAgentClient;
}) {
  const { t } = useLocale();
  const enabled = capabilities.filter((capability) => capability.enabled);
  return (
    <div className="profile-settings">
      <section className="profile-identity">
        <PrayuBrand className="profile-avatar" variant="icon" />
        <h1>Prayu</h1>
        <p>@local-operator <span>{t("本地", "Local")}</span></p>
      </section>
      <dl className="profile-metrics">
        <div><dt>{t("数据结构", "Schema")}</dt><dd>v{health?.schema_version ?? "-"}</dd></div>
        <div><dt>API</dt><dd>{health?.api_version ?? "api.v1"}</dd></div>
        <div><dt>{t("版本", "Version")}</dt><dd>{health?.app_version ?? "dev"}</dd></div>
        <div><dt>{t("控制能力", "Capabilities")}</dt><dd>{enabled.length}/{capabilities.length}</dd></div>
        <div><dt>{t("运行界面", "Surface")}</dt><dd>{desktop ? t("桌面端", "Desktop") : t("网页端", "Web")}</dd></div>
      </dl>
      <section className="capability-activity" aria-label={t("能力状态", "Capability status")}>
        <header>
          <div><h2>{t("能力状态", "Capability status")}</h2><span>{t(`${enabled.length} 项已启用`, `${enabled.length} enabled`)}</span></div>
          <span className="capability-legend"><i />{t("启用", "Enabled")}</span>
        </header>
        <div className="capability-grid">
          {capabilities.map((capability) => <span aria-label={`${capability.label}: ${capability.enabled ? t("启用", "enabled") : t("关闭", "disabled")}`}
            className={capability.enabled ? "enabled" : ""} key={capability.id}
            role="img" title={`${capability.label}: ${capability.enabled ? t("启用", "enabled") : t("关闭", "disabled")}`} />)}
        </div>
      </section>
      <div className="profile-detail-columns">
        <section>
          <h2>{t("运行时", "Runtime")}</h2>
          <dl className="settings-values">
            <div><dt>{t("状态", "Status")}</dt><dd>{health?.status === "ok" ? t("正常", "Ready") : t("连接中", "Connecting")}</dd></div>
            <div><dt>{t("控制平面", "Control plane")}</dt><dd>Go</dd></div>
            <div><dt>{t("界面", "Interface")}</dt><dd>React / Vite</dd></div>
            <div><dt>{t("本地存储", "Local store")}</dt><dd>SQLite</dd></div>
          </dl>
        </section>
        <section>
          <h2>{t("Safe Web", "Safe Web")}</h2>
          <SafeWebReadinessPanel client={client} />
        </section>
        <section>
          <h2>{t("当前能力", "Active capabilities")}</h2>
          <ul className="enabled-capability-list">
            {enabled.slice(0, 5).map((capability) => <li key={capability.id}>
              <ShieldCheck aria-hidden="true" size={15} />
              <span>{capability.label}</span>
            </li>)}
            {enabled.length === 0 && <li><SlidersHorizontal aria-hidden="true" size={15} />{t("只读模式", "Read-only mode")}</li>}
          </ul>
        </section>
      </div>
    </div>
  );
}

function GeneralSettings({ capabilities, desktop, health }: {
  capabilities: SettingsCapability[];
  desktop: boolean;
  health: HealthView | null;
}) {
  const { locale, setLocale, t } = useLocale();
  return <section className="settings-page-section">
    <h1>{t("常规", "General")}</h1>
    <div className="appearance-setting-row settings-language-row">
      <div><strong><Languages aria-hidden="true" size={16} />{t("语言", "Language")}</strong>
        <span>{t("界面语言", "Interface language")}</span></div>
      <div className="prayu-segmented" role="group" aria-label={t("界面语言", "Interface language")}>
        <button aria-pressed={locale === "zh-CN"} onClick={() => setLocale("zh-CN")}
          type="button">中文</button>
        <button aria-pressed={locale === "en-US"} onClick={() => setLocale("en-US")}
          type="button">English</button>
      </div>
    </div>
    <dl className="settings-row-list">
      <div><dt>{t("连接状态", "Connection")}</dt><dd><span className="settings-online-dot" />{health?.status === "ok" ? t("正常", "Ready") : t("连接中", "Connecting")}</dd></div>
      <div><dt>{t("运行界面", "Surface")}</dt><dd>{desktop ? t("Windows 桌面端", "Windows Desktop") : t("网页控制台", "Web console")}</dd></div>
      <div><dt>{t("控制能力", "Control capabilities")}</dt><dd>{capabilities.filter((item) => item.enabled).length} / {capabilities.length}</dd></div>
      <div><dt>{t("数据边界", "Data boundary")}</dt><dd>{t("本地优先", "Local-first")}</dd></div>
    </dl>
  </section>;
}

function AppearanceSettings({ density, theme, onDensityChange, onThemeChange }: {
  density: Density;
  theme: PrayuTheme;
  onDensityChange: (density: Density) => void;
  onThemeChange: (theme: PrayuTheme) => void;
}) {
  const { t } = useLocale();
  return <section className="settings-page-section">
    <h1>{t("外观", "Appearance")}</h1>
    <div className="appearance-setting-row">
      <div><strong>{t("外观模式", "Theme")}</strong><span>{t("颜色与材质", "Color and material")}</span></div>
      <div className="prayu-segmented appearance-theme-picker" role="group" aria-label={t("外观模式", "Theme")}>
        <button aria-pressed={theme === "light"} onClick={() => onThemeChange("light")}
          type="button"><Sun aria-hidden="true" size={14} />{t("浅色", "Light")}</button>
        <button aria-pressed={theme === "dark"} onClick={() => onThemeChange("dark")}
          type="button"><Moon aria-hidden="true" size={14} />{t("深色", "Dark")}</button>
        <button aria-pressed={theme === "glass"} onClick={() => onThemeChange("glass")}
          type="button"><Layers3 aria-hidden="true" size={14} />{t("透明玻璃", "Glass")}</button>
      </div>
    </div>
    <div className="appearance-setting-row">
      <div><strong>{t("界面密度", "Interface density")}</strong><span>{t("工作台内容间距", "Workspace spacing")}</span></div>
      <div className="prayu-segmented" role="group" aria-label={t("界面密度", "Interface density")}>
        <button aria-pressed={density === "comfortable"}
          onClick={() => onDensityChange("comfortable")} type="button">{t("舒展", "Comfortable")}</button>
        <button aria-pressed={density === "compact"}
          onClick={() => onDensityChange("compact")} type="button">{t("紧凑", "Compact")}</button>
      </div>
    </div>
  </section>;
}

function ShortcutSettings() {
  const { t } = useLocale();
  return <section className="settings-page-section">
    <h1>{t("键盘快捷键", "Keyboard shortcuts")}</h1>
    <dl className="shortcut-list">
      <div><dt>{t("打开命令面板", "Open command palette")}</dt><dd><kbd>Ctrl</kbd><kbd>K</kbd></dd></div>
      <div><dt>{t("关闭对话框或预览", "Close dialog or preview")}</dt><dd><kbd>Esc</kbd></dd></div>
      <div><dt>{t("选择上一项", "Select previous item")}</dt><dd><kbd>↑</kbd></dd></div>
      <div><dt>{t("选择下一项", "Select next item")}</dt><dd><kbd>↓</kbd></dd></div>
      <div><dt>{t("确认当前操作", "Confirm current action")}</dt><dd><kbd>Enter</kbd></dd></div>
    </dl>
  </section>;
}

function AboutSettings({ desktop, health }: { desktop: boolean; health: HealthView | null }) {
  const { t } = useLocale();
  return <section className="settings-page-section about-prayu">
    <PrayuBrand className="about-mark" variant="icon" />
    <h1>Prayu</h1>
    <p>{t("本地优先的 AI Agent 工作台", "Local-first AI Agent Workbench")}</p>
    <dl className="settings-row-list">
      <div><dt>{t("应用版本", "Application version")}</dt><dd>{health?.app_version ?? "dev"}</dd></div>
      <div><dt>API 协议</dt><dd>{health?.api_version ?? "api.v1"}</dd></div>
      <div><dt>{t("数据库", "Database")}</dt><dd>schema v{health?.schema_version ?? "-"}</dd></div>
      <div><dt>{t("运行界面", "Surface")}</dt><dd>{desktop ? t("桌面端", "Desktop") : t("网页端", "Web")}</dd></div>
    </dl>
  </section>;
}

type ExtensionAction =
  | { kind: "refresh-mcp"; server: ExtensionMCPServerView }
  | { kind: "disable-mcp"; server: ExtensionMCPServerView }
  | { kind: "disable-plugin"; installation: ExtensionPluginInstallationView };

function ExtensionSettings({ client, selectedRunID }: {
  client: CyberAgentClient;
  selectedRunID: string;
}) {
  const { t } = useLocale();
  const queryClient = useQueryClient();
  const inventory = useQuery({
    queryKey: ["extensions", selectedRunID],
    queryFn: ({ signal }) => client.extensionInventory(selectedRunID, signal),
  });
  const action = useMutation<unknown, Error, ExtensionAction>({
    mutationFn: (value: ExtensionAction) => {
      if (value.kind === "refresh-mcp") {
        return client.refreshMCPServer(value.server.id);
      }
      if (value.kind === "disable-mcp") {
        return client.reviewMCPServer(value.server.id, {
          version: "extension-control.v1", action: "disable",
          expected_descriptor_fingerprint: value.server.descriptor_fingerprint,
        });
      }
      return client.reviewPluginInstallation(value.installation.id, {
        version: "extension-control.v1", action: "disable",
        expected_package_fingerprint: value.installation.package_fingerprint,
        expected_generation: value.installation.generation,
        confirm_untrusted: false,
      });
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["extensions"] }),
  });
  return <section className="settings-page-section extension-settings">
    <header className="extension-heading">
      <div>
        <h1>{t("MCP 与 Plugin", "MCP and Plugins")}</h1>
        <p>{t("查看真实运行状态、固定能力指纹，并可立即关闭扩展。凭据只显示引用名。",
          "Inspect live state and pinned capability fingerprints, or disable an extension immediately. Credentials are shown by reference only.")}</p>
      </div>
      <button className="settings-action" disabled={inventory.isFetching}
        onClick={() => void inventory.refetch()} type="button">
        <RefreshCw aria-hidden="true" className={inventory.isFetching ? "spin" : ""} size={15} />
        {t("刷新", "Refresh")}
      </button>
    </header>
    {inventory.error && <p className="inline-warning">{inventory.error instanceof Error ?
      inventory.error.message : t("扩展状态读取失败", "Failed to read extension state")}</p>}
    {action.error && <p className="inline-warning">{action.error instanceof Error ?
      action.error.message : t("扩展操作失败", "Extension action failed")}</p>}
    <ExtensionCollection title="MCP Client" count={inventory.data?.mcp_servers.length ?? 0}>
      {inventory.data?.mcp_servers.map((server) => <MCPServerCard action={action}
        client={client} key={server.id} server={server} />)}
      {inventory.data && inventory.data.mcp_servers.length === 0 &&
        <ExtensionEmpty>{selectedRunID ?
          t("当前 Run / Workspace 没有 MCP Server。", "No MCP server is scoped to this Run / Workspace.") :
          t("选择一个 Run 以查看其 MCP Server。", "Select a Run to inspect its MCP servers.")}</ExtensionEmpty>}
    </ExtensionCollection>
    <ExtensionCollection title="Plugin" count={inventory.data?.plugins.length ?? 0}>
      {inventory.data?.plugins.map((installation) => <PluginCard action={action}
        client={client} installation={installation} key={installation.id} />)}
      {inventory.data && inventory.data.plugins.length === 0 &&
        <ExtensionEmpty>{t("尚未安装 Plugin。", "No Plugin is installed.")}</ExtensionEmpty>}
    </ExtensionCollection>
  </section>;
}

function ExtensionCollection({ title, count, children }: {
  title: string; count: number; children: ReactNode;
}) {
  return <section className="extension-collection">
    <header><h2>{title}</h2><span>{count}</span></header>
    <div className="extension-card-list">{children}</div>
  </section>;
}

function MCPServerCard({ action, client, server }: {
  action: { isPending: boolean; mutate: (value: ExtensionAction) => void };
  client: CyberAgentClient;
  server: ExtensionMCPServerView;
}) {
  const { t } = useLocale();
  const refreshable = ["discovery_approved", "capabilities_pending", "enabled",
    "quarantined"].includes(server.state);
  const disableable = !["disabled", "revoked"].includes(server.state);
  return <article className="extension-card">
    <header><div><PlugZap aria-hidden="true" size={17} />
      <div><strong>{server.name}</strong><span>{server.id}</span></div></div>
      <ExtensionState state={server.state} /></header>
    <dl className="extension-facts">
      <div><dt>{t("传输", "Transport")}</dt><dd>{server.transport}</dd></div>
      <div><dt>{t("健康", "Health")}</dt><dd>{server.health}</dd></div>
      <div><dt>{t("范围", "Scope")}</dt><dd>{server.scope}</dd></div>
      <div><dt>{t("工具", "Tools")}</dt><dd>{server.capabilities.tools.length}</dd></div>
      <div><dt>{t("凭据引用", "Credential ref")}</dt><dd>{server.credential_ref || "—"}</dd></div>
      <div><dt>{t("来源", "Source")}</dt><dd>{server.source.kind}</dd></div>
    </dl>
    <p className="extension-target" title={server.target}>{server.target}</p>
    <Fingerprint label={t("能力指纹", "Capability fingerprint")}
      value={server.capabilities.fingerprint || server.descriptor_fingerprint} />
    <div className="extension-actions">
      <button className="settings-action" disabled={!client.hasExtensionControl ||
        !refreshable || action.isPending}
        onClick={() => action.mutate({ kind: "refresh-mcp", server })} type="button">
        <RefreshCw aria-hidden="true" size={14} />{t("重新发现", "Rediscover")}
      </button>
      <button className="settings-action danger" disabled={!client.hasExtensionControl ||
        !disableable || action.isPending}
        onClick={() => action.mutate({ kind: "disable-mcp", server })} type="button">
        <Ban aria-hidden="true" size={14} />{t("立即关闭", "Disable now")}
      </button>
    </div>
  </article>;
}

function PluginCard({ action, client, installation }: {
  action: { isPending: boolean; mutate: (value: ExtensionAction) => void };
  client: CyberAgentClient;
  installation: ExtensionPluginInstallationView;
}) {
  const { t } = useLocale();
  const disableable = !["disabled", "revoked", "rolled_back"].includes(installation.state);
  return <article className="extension-card">
    <header><div><PackageSearch aria-hidden="true" size={17} />
      <div><strong>{installation.manifest.name}</strong>
        <span>{installation.manifest.publisher} · v{installation.manifest.version}</span></div></div>
      <ExtensionState state={installation.state} /></header>
    <dl className="extension-facts">
      <div><dt>{t("签名", "Signature")}</dt><dd>{installation.signature_valid ?
        t("有效", "Valid") : installation.signature_present ? t("无效", "Invalid") :
          t("未签名", "Unsigned")}</dd></div>
      <div><dt>{t("来源", "Source")}</dt><dd>{installation.source.kind}</dd></div>
      <div><dt>{t("已启用", "Enabled")}</dt>
        <dd>{installation.enabled_capabilities.join(", ") || "—"}</dd></div>
      <div><dt>{t("代次", "Generation")}</dt><dd>{installation.generation}</dd></div>
    </dl>
    <p className="extension-target" title={installation.source.uri}>{installation.source.uri}</p>
    <Fingerprint label={t("包指纹", "Package fingerprint")}
      value={installation.package_fingerprint} />
    <div className="extension-actions">
      <button className="settings-action danger" disabled={!client.hasExtensionControl ||
        !disableable || action.isPending}
        onClick={() => action.mutate({ kind: "disable-plugin", installation })} type="button">
        <Ban aria-hidden="true" size={14} />{t("立即关闭", "Disable now")}
      </button>
    </div>
  </article>;
}

function ExtensionState({ state }: { state: string }) {
  return <span className={`extension-state state-${state}`}>{state.replaceAll("_", " ")}</span>;
}

function Fingerprint({ label, value }: { label: string; value: string }) {
  return <div className="extension-fingerprint"><span>{label}</span>
    <code title={value}>{value ? `${value.slice(0, 12)}…${value.slice(-8)}` : "—"}</code></div>;
}

function ExtensionEmpty({ children }: { children: ReactNode }) {
  return <p className="extension-empty">{children}</p>;
}
