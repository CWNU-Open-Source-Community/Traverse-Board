import { useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArchiveRestore, BookOpen, Monitor, Search, Trash2, X } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import type { ProviderDefinitionView, ThreadView, WorkspaceView } from "../../api/types";
import { applyPrayuTheme, readPrayuTheme, type PrayuTheme } from "../../lib/appearance";
import { useModalFocusTrap } from "../../hooks/use-modal-focus-trap";
import { v2QueryKeys } from "../query-keys";
import type { V2SettingsSection } from "./sidebar";
import { V2ConfirmDialog } from "./dialog";
import { V2ModelSettings, type V2ModelProviderPreset } from "./model-settings";
import {
  v2ModelProviderPresets,
  type V2ConfiguredModelProviderPreset,
} from "./model-provider-presets";
import { V2PermissionControl } from "./permission-control";
import { V2ProviderSettings } from "./provider-settings";
import { V2RuntimeCapabilityControl } from "./runtime-capability-control";
import { V2ExecutionSettings } from "./execution-settings";
import { desktopBridgeAvailable } from "../../lib/desktop-bridge";
import { useLocale } from "../../lib/locale";
import { ShortcutSettings } from "../../components/shared-settings-panels";
import { ModelAvailabilitySettings } from "../../components/model-availability-dialog";
import { V2AboutSettings, V2ExtensionSettings, V2InspectorPreferences, V2SkillSettings } from "./advanced-settings";

function SettingRow({ title, detail, children }: { title: string; detail: string; children: React.ReactNode }) {
  return <div className="v2-setting-row"><div><strong>{title}</strong><span>{detail}</span></div>
    <div className="v2-setting-value">{children}</div></div>;
}

function FontLicenseControl() {
  const [open, setOpen] = useState(false);
  const [license, setLicense] = useState("");
  const [error, setError] = useState("");
  const triggerRef = useRef<HTMLButtonElement>(null);
  const close = () => setOpen(false);
  const dialogRef = useModalFocusTrap<HTMLElement>(open, close, false, undefined, {
    isolateBackground: true,
    returnFocusRef: triggerRef,
  });

  useEffect(() => {
    if (!open || license) return;
    const controller = new AbortController();
    setError("");
    void fetch("/licenses/HarmonyOS-Sans.txt", { cache: "no-store", signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const content = await response.text();
        if (!content.includes("HarmonyOS Sans Fonts License Agreement")) {
          throw new Error("许可文本格式无效");
        }
        setLicense(content);
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted) return;
        setError(reason instanceof Error ? reason.message : "无法读取许可文本");
      });
    return () => controller.abort();
  }, [license, open]);

  return <>
    <button className="v2-setting-link" onClick={() => setOpen(true)} ref={triggerRef}
      type="button">查看许可</button>
    {open && <div className="v2-overlay" onMouseDown={(event) => {
      if (event.target === event.currentTarget) close();
    }} role="presentation">
      <section aria-labelledby="v2-font-license-title" aria-modal="true"
        className="v2-dialog v2-license-dialog" ref={dialogRef} role="dialog" tabIndex={-1}>
        <header><span><BookOpen aria-hidden="true" size={17} /></span>
          <h2 id="v2-font-license-title">HarmonyOS Sans Fonts 许可</h2>
          <button aria-label="关闭许可" onClick={close} type="button"><X aria-hidden="true" size={16} /></button>
        </header>
        <div aria-live="polite" className="v2-license-content">
          {!license && !error && <p>正在读取随软件发布的许可文本…</p>}
          {error && <p role="alert">无法读取许可文本：{error}</p>}
          {license && <pre>{license}</pre>}
        </div>
        <footer><button onClick={close} type="button">关闭</button></footer>
      </section>
    </div>}
  </>;
}

function GeneralSettings({ threadID, workspaces, onPermissions }: {
  threadID: string;
  workspaces: WorkspaceView[];
  onPermissions: () => void;
}) {
  const { locale, setLocale } = useLocale();
  return <>
    <h1>常规</h1>
    <p className="v2-settings-lead">应用偏好由对话和 Inspector 共用。任务的执行环境、信任与访问范围在「当前任务权限」中管理。</p>
    <section className="v2-settings-section"><h2>常规</h2>
      <div className="v2-settings-card">
        <SettingRow detail="新对话中选择项目；桌面应用可直接打开本机文件夹" title="项目">
          <span>{workspaces.length} 个已加载项目</span>
        </SettingRow>
        <SettingRow detail="主界面使用简体中文；部分高级面板可使用 English，完整双语界面尚未提供。" title="语言">
          <div className="v2-setting-segmented" role="group" aria-label="高级面板语言">
            <button aria-pressed={locale === "zh-CN"} onClick={() => setLocale("zh-CN")} type="button">中文</button>
            <button aria-pressed={locale === "en-US"} onClick={() => setLocale("en-US")} type="button">English（部分）</button>
          </div>
        </SettingRow>
        <SettingRow detail={threadID ? "仅管理当前打开的任务，不作为其他项目的默认权限。" : "先打开一个对话，再查看它的任务权限。"} title="当前任务权限">
          <button className="v2-setting-link" aria-label="管理当前任务权限" disabled={!threadID}
            onClick={onPermissions} type="button">管理</button>
        </SettingRow>
        <SettingRow detail="中文界面使用 HarmonyOS Sans Fonts；完整许可文本随软件发布。" title="第三方字体">
          <FontLicenseControl />
        </SettingRow>
      </div>
    </section>
  </>;
}

function AppearanceSettings() {
  const [theme, setTheme] = useState<PrayuTheme>(() => readPrayuTheme());
  const select = (next: PrayuTheme) => { setTheme(next); applyPrayuTheme(next); };
  return <><h1>外观</h1><section className="v2-settings-section"><h2>主题与材质</h2>
    <div className="v2-settings-card v2-appearance-card">
      <div className="v2-theme-options" role="group" aria-label="应用主题">
        {(["light", "dark", "glass"] as const).map((option) => <button aria-pressed={theme === option}
          key={option} onClick={() => select(option)} type="button">
          <span className={`v2-theme-preview theme-${option}`}><i /><i /></span>
          <strong>{option === "light" ? "浅色" : option === "dark" ? "深色" : "透明液态玻璃"}</strong>
        </button>)}</div>
      <p>玻璃模式复用 Traverse Board 的高斯模糊、透明材质与原生 Windows Acrylic；降低透明度偏好会自动回退到不透明表面。</p>
    </div></section></>;
}

function ModelSettingsPage({ client, initialAdvancedOpen = false, prepareForDraft = false,
  setupToken = "", onModelReady }: {
  client: CyberAgentClient; initialAdvancedOpen?: boolean; prepareForDraft?: boolean;
  setupToken?: string;
  onModelReady?: (token: string, definition: ProviderDefinitionView) => void;
}) {
  const [selected, setSelected] = useState<V2ConfiguredModelProviderPreset | null>(null);
  const [copilotOpen, setCopilotOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(initialAdvancedOpen);
  useEffect(() => { if (initialAdvancedOpen) setAdvancedOpen(true); }, [initialAdvancedOpen]);
  const selectedIDRef = useRef("");
  const copilotTriggerRef = useRef<HTMLButtonElement | null>(null);
  const credentials = useQuery({
    queryKey: ["v2", "provider-credentials"],
    queryFn: ({ signal }) => client.providerCredentialStatuses(signal),
    enabled: Boolean(client.hasProviderCredentials),
  });
  const configuredProviderIDs = useMemo(() => new Set(
    (credentials.data?.items ?? []).filter((status) => status.configured)
      .map((status) => status.provider),
  ), [credentials.data?.items]);
  const presets = useMemo<V2ConfiguredModelProviderPreset[]>(() =>
    v2ModelProviderPresets.map((preset) => preset.kind === "account"
      ? { ...preset, setup: { kind: "account", connected: false,
        accountName: "GitHub Copilot" } }
      : { ...preset, setup: { kind: "api_key",
        configured: configuredProviderIDs.has(preset.id) } }),
  [configuredProviderIDs]);

  const restoreCatalogFocus = () => {
    const selectedID = selectedIDRef.current;
    setSelected(null);
    requestAnimationFrame(() => document.querySelector<HTMLButtonElement>(
      `[data-model-provider-id="${selectedID}"]`,
    )?.focus());
  };
  const selectPreset = (catalogPreset: V2ModelProviderPreset, trigger: HTMLButtonElement) => {
    const preset = presets.find((candidate) => candidate.id === catalogPreset.id);
    if (!preset) return;
    if (preset.kind === "account") {
      copilotTriggerRef.current = trigger;
      setCopilotOpen(true);
      return;
    }
    selectedIDRef.current = preset.id;
    setSelected(preset);
  };

  const advancedPanel = <section className="v2-model-advanced">
      <button aria-expanded={advancedOpen} className="v2-model-advanced-toggle"
        onClick={() => setAdvancedOpen((open) => !open)} type="button">
        高级模型设置：默认模型、能力诊断与费用预算
      </button>
      {advancedOpen && <><p>新对话可在输入区直接选模型；这里管理未单独选择时的默认值。
        价格表只用于启用金额上限的任务。</p>
        <ModelAvailabilitySettings client={client} /></>}
    </section>;

  if (selected?.draft) {
    return <>{initialAdvancedOpen && advancedPanel}
      <V2ProviderSettings client={client} initialPreset={selected.draft}
        onExit={restoreCatalogFocus} prepareForDraft={prepareForDraft}
        onReady={(definition) => onModelReady?.(setupToken, definition)} /></>;
  }

  return <>
    <V2ModelSettings client={client} onSelectPreset={selectPreset} presets={presets} />
    {advancedPanel}
    <V2ConfirmDialog confirmLabel="知道了"
      description="GitHub Copilot 使用 GitHub/Copilot 账户与订阅席位，不是通用 API Key 接口。Traverse 会把它作为独立的账户连接器接入；当前版本尚未完成 Copilot SDK 登录，因此不会把 PAT 或任意 Base URL 冒充为 Copilot 推理凭据。"
      onCancel={() => setCopilotOpen(false)} onConfirm={() => setCopilotOpen(false)}
      open={copilotOpen} returnFocusRef={copilotTriggerRef}
      title="GitHub Copilot 需要账户连接" />
  </>;
}

function ArchivedSettings({ client, onOpenThread }: { client: CyberAgentClient; onOpenThread?: (id: string) => void }) {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [deleteCandidate, setDeleteCandidate] = useState<ThreadView | null>(null);
  const query = useInfiniteQuery({
    queryKey: v2QueryKeys.threads("archived"),
    queryFn: ({ signal, pageParam }) => client.getPage<ThreadView>("/threads",
      { limit: 100, status: "archived" }, pageParam, signal),
    initialPageParam: "", getNextPageParam: (last) => last.page.next_cursor || undefined,
  });
  const transition = useMutation({
    mutationFn: ({ thread, action }: { thread: ThreadView; action: "restore" | "delete" }) =>
      client.transitionThread(thread.id, action, {
        version: "thread_lifecycle.v1", expected_version: thread.version,
      }, `v2-archived-${action}-${globalThis.crypto.randomUUID()}`),
    onSuccess: () => {
      setDeleteCandidate(null);
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("archived") });
      void queryClient.invalidateQueries({ queryKey: v2QueryKeys.threads("active") });
    },
  });
  const normalized = search.trim().toLocaleLowerCase();
  const loaded = useMemo(() => [...new Map(query.data?.pages.flatMap(({ items }) => items)
    .map((thread) => [thread.id, thread])).values()], [query.data]);
  const threads = useMemo(() => loaded.filter((thread) => !normalized ||
    thread.title.toLocaleLowerCase().includes(normalized)), [normalized, loaded]);
  return <><h1>已归档的聊天</h1><p className="v2-settings-lead">归档会从侧栏隐藏对话，但保留消息、执行记录与审计证据。</p>
    <label className="v2-archive-search"><Search aria-hidden="true" size={15} />
      <input aria-label="搜索已归档的聊天" onChange={(event) => setSearch(event.target.value)}
        placeholder="搜索已归档的聊天" type="search" value={search} /></label>
    <p className="v2-history-scope">搜索{loaded.length}条已加载的归档标题，不含消息正文。
      {query.isLoading ? "正在读取列表。" : query.isError ? "本次加载未完成，请重试。"
        : query.hasNextPage ? "可以继续加载更早记录。" : "当前列表已加载完毕。"}</p>
    <div className="v2-archive-list">
      {query.isLoading && <p>正在加载…</p>}
      {query.isError && <p role="alert">无法读取归档列表。<button onClick={() => void (query.isFetchNextPageError
        ? query.fetchNextPage() : query.refetch())} type="button">重试归档列表</button></p>}
      {!query.isLoading && !query.isError && threads.length === 0 && <div className="v2-archive-empty">
        {normalized ? "已加载的标题中没有匹配项" : "暂无已归档的聊天"}</div>}
      {threads.map((thread) => <article key={thread.id}><div><strong>{onOpenThread
        ? <button className="link-button" aria-label={`打开 ${thread.title}`} onClick={() => onOpenThread(thread.id)}
          type="button">{thread.title}</button> : thread.title}</strong>
        <span>{thread.archived_at ? new Date(thread.archived_at).toLocaleString() : "已归档"}</span></div>
        <button disabled={!client.hasThreadControl || transition.isPending}
          onClick={() => transition.mutate({ thread, action: "restore" })} type="button">
          <ArchiveRestore aria-hidden="true" size={15} />取消归档</button>
        <button aria-label={`删除 ${thread.title}`} className="danger"
          disabled={!client.hasThreadControl || transition.isPending}
          onClick={() => setDeleteCandidate(thread)} type="button"><Trash2 aria-hidden="true" size={15} />删除</button>
      </article>)}</div>
    {query.hasNextPage && <button className="v2-load-history" disabled={query.isFetchingNextPage}
      onClick={() => void query.fetchNextPage()} type="button">
      {query.isFetchingNextPage ? "正在加载…" : "加载更早归档"}</button>}
    {transition.isError && <p className="v2-inline-error" role="alert">{transition.error instanceof Error
      ? transition.error.message : "更新归档状态失败"}</p>}
    <V2ConfirmDialog busy={transition.isPending} confirmLabel="删除" danger
      description="此对话会从产品列表中删除；底层审计记录仍按项目保留策略保存。"
      onCancel={() => setDeleteCandidate(null)} onConfirm={() => deleteCandidate &&
        transition.mutate({ thread: deleteCandidate, action: "delete" })}
      open={Boolean(deleteCandidate)} title="删除已归档的聊天" />
  </>;
}

function PlaceholderSettings({ section, onOpenLegacy }: {
  section: V2SettingsSection;
  onOpenLegacy: (returnFocus: HTMLElement) => void;
}) {
  const titles: Partial<Record<V2SettingsSection, string>> = {
    voice: "语音", keyboard: "键盘快捷键", plugins: "插件",
    browser: "浏览器", hooks: "钩子", git: "Git", environment: "环境", worktrees: "Worktrees",
  };
  return <><h1>{titles[section] ?? "设置"}</h1><section className="v2-settings-section">
    <div className="v2-settings-card v2-settings-placeholder"><Monitor aria-hidden="true" size={22} />
      <strong>该能力继续由现有控制平面管理</strong>
      <p>产品界面不会复制 Harness 设置。需要诊断或高级配置时，可打开 Inspector。</p>
      <button onClick={(event) => onOpenLegacy(event.currentTarget)}
        type="button">在 Inspector 中打开</button></div>
  </section></>;
}

export function V2Settings({ client, section, threadID, workspaces, onSelectSection,
  onOpenInspector, onOpenThread, prepareModelForDraft = false, modelSetupToken = "",
  onModelReady, desktop = desktopBridgeAvailable() }: {
  client: CyberAgentClient;
  section: V2SettingsSection;
  threadID: string;
  workspaces: WorkspaceView[];
  onSelectSection: (section: V2SettingsSection) => void;
  onOpenThread?: (id: string) => void;
  onOpenInspector: (returnFocus?: HTMLElement | null) => void;
  prepareModelForDraft?: boolean;
  modelSetupToken?: string;
  onModelReady?: (token: string, definition: ProviderDefinitionView) => void;
  desktop?: boolean;
}) {
  return <main className="v2-settings-main"><div className="v2-settings-toolbar" />
    <div className="v2-settings-scroll"><div className="v2-settings-content">
      {section === "general" && <GeneralSettings onPermissions={() => onSelectSection("permissions")}
        threadID={threadID} workspaces={workspaces} />}
      {section === "permissions" && <><h1>权限</h1><p className="v2-settings-lead">
        本页管理当前打开的对话。运行中提高权限不会改变当前执行，将从下一次执行生效；降低权限会撤销相应能力。
      </p>
        <section className="v2-settings-section"><h2>任务权限</h2>
          <V2PermissionControl client={client} threadID={threadID} variant="settings"
            onOpenModelSettings={() => onSelectSection("models")} />
        </section>
        <V2ExecutionSettings client={client} threadID={threadID} workspaces={workspaces} />
        <section className="v2-settings-section"><V2RuntimeCapabilityControl /></section></>}
      {section === "appearance" && <AppearanceSettings />}
      {section === "archived" && <ArchivedSettings client={client} onOpenThread={onOpenThread} />}
      {(section === "models" || section === "advanced-models") &&
        <ModelSettingsPage client={client} initialAdvancedOpen={section === "advanced-models"}
          prepareForDraft={prepareModelForDraft} setupToken={modelSetupToken}
          onModelReady={onModelReady} />}
      {(section === "extensions" || section === "plugins") && <V2ExtensionSettings client={client} threadID={threadID} />}
      {section === "skills" && <V2SkillSettings client={client} desktop={desktop} />}
      {section === "about" && <V2AboutSettings client={client} desktop={desktop} />}
      {(section === "shortcuts" || section === "keyboard") && <div className="v2-shared-settings">
        <ShortcutSettings /><p>这是已有快捷键的说明。方向键与 Enter 用于当前菜单或对话框；输入框中 Enter 发送、Shift+Enter 换行。</p>
      </div>}
      {section === "inspector" && <V2InspectorPreferences onOpenInspector={onOpenInspector} />}
      {!(["general", "permissions", "appearance", "archived", "models", "inspector", "extensions", "plugins",
        "skills", "advanced-models", "about", "shortcuts", "keyboard"] as V2SettingsSection[])
        .includes(section) && <PlaceholderSettings onOpenLegacy={onOpenInspector} section={section} />}
    </div></div>
  </main>;
}
