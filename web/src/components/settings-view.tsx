import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { useMutation } from "@tanstack/react-query";
import {
  ArrowLeft,
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
import { RunPermissionSettings } from "./run-permission-settings";
import { SidebarResizeHandle, clampSidebarWidth, defaultSidebarWidth } from "./workbench-frame";

export type SettingsCapability = {
  id: string;
  label: string;
  enabled: boolean;
};

type SettingsSection = "profile" | "general" | "permissions" | "appearance" | "workspace" | "shortcuts" | "about";
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
          <strong>{navigation.find((item) => item.id === section)?.label}</strong>
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
            desktop={desktop} health={health} />}
          {section === "general" && <GeneralSettings capabilities={capabilities}
            desktop={desktop} health={health} />}
          {section === "permissions" && <RunPermissionSettings client={client}
            runID={selectedRunID} />}
          {section === "appearance" && <AppearanceSettings density={density} theme={theme}
            onDensityChange={setDensity} onThemeChange={setTheme} />}
          {section === "workspace" && <WorkbenchSettings mode={runNavigationMode}
            onModeChange={setRunNavigationMode} />}
          {section === "shortcuts" && <ShortcutSettings />}
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

function ProfileSettings({ capabilities, desktop, health }: {
  capabilities: SettingsCapability[];
  desktop: boolean;
  health: HealthView | null;
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