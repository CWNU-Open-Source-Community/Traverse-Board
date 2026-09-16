import { useEffect, useMemo, useRef, useState, type CSSProperties,
  type ReactNode } from "react";
import {
  ArrowLeft,
  Archive,
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
import type { HealthView } from "../api/types";
import { applyPrayuTheme, readPrayuTheme, type PrayuTheme } from "../lib/appearance";
import { useLocale } from "../lib/locale";
import { applyRunNavigationMode, readRunNavigationMode,
  type RunNavigationMode } from "../lib/run-navigation";
import { PrayuBrand } from "./prayu-brand";
import { ArchivedThreadsSettings } from "./archived-threads-settings";
import { ThreadPermissionSettings } from "./thread-permission-settings";
import { SafeWebReadinessPanel } from "./safe-web-readiness";
import { SidebarResizeHandle, clampSidebarWidth, defaultSidebarWidth } from "./workbench-frame";
import { AboutSettings, ExtensionSettings, ShortcutSettings, WebSkillInstall,
  persistDensity, readDensity, type Density } from "./shared-settings-panels";

export type SettingsCapability = {
  id: string;
  label: string;
  enabled: boolean;
};

type SettingsSection = "profile" | "general" | "permissions" | "appearance" |
  "workspace" | "shortcuts" | "extensions" | "archived" | "about";
const settingsSidebarWidthStorageKey = "prayu.settings.sidebar.width.v1";

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

export function SettingsView({
  capabilities,
  client,
  desktop,
  health,
  selectedRunID,
  selectedThreadID,
  onBack,
  onOpenModels,
  onOpenSkills,
}: {
  capabilities: SettingsCapability[];
  client: CyberAgentClient;
  desktop: boolean;
  health: HealthView | null;
  selectedRunID: string;
  selectedThreadID: string;
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
    id: "extensions" as const,
    label: t("Code Intel、MCP 与 Plugin", "Code Intel, MCP and Plugins"), icon: PlugZap,
  };
  const archivedNavigation = {
    id: "archived" as const, label: t("已归档的聊天", "Archived chats"), icon: Archive,
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
        <nav aria-label={t("针路簿设置", "Traverse Board settings")}>
          {visibleNavigation.map(({ id, label, icon: Icon }) => (
            <button className={section === id ? "active" : ""} key={id}
              onClick={() => setSection(id)} type="button">
              <Icon aria-hidden="true" size={16} /><span>{label}</span>
            </button>
          ))}
        </nav>
        <span className="settings-group-label">{t("集成", "Integrations")}</span>
        <nav aria-label={t("针路簿集成", "Traverse Board integrations")}>
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
        <span className="settings-group-label">{t("归档", "Archive")}</span>
        <nav aria-label={t("针路簿归档", "Traverse Board archive")}>
          <button className={section === "archived" ? "active" : ""}
            onClick={() => setSection("archived")} type="button">
            <Archive aria-hidden="true" size={16} /><span>{archivedNavigation.label}</span>
          </button>
        </nav>
      </aside>
      <SidebarResizeHandle onChange={resizeSidebar} value={sidebarWidth} />
      <main className="settings-main">
        <header className="settings-header">
          <strong>{section === "extensions" ? extensionNavigation.label :
            section === "archived" ? archivedNavigation.label :
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
          {section === "permissions" && <ThreadPermissionSettings client={client}
            key={selectedThreadID || "no-thread"}
            threadID={selectedThreadID} />}
          {section === "appearance" && <AppearanceSettings density={density} theme={theme}
            onDensityChange={setDensity} onThemeChange={setTheme} />}
          {section === "workspace" && <WorkbenchSettings mode={runNavigationMode}
            onModeChange={setRunNavigationMode} />}
          {section === "shortcuts" && <ShortcutSettings />}
          {section === "extensions" && <ExtensionSettings client={client}
            selectedRunID={selectedRunID} />}
          {section === "archived" && <ArchivedThreadsSettings client={client} />}
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
        <h1>{t("针路簿", "Traverse Board")}</h1>
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
