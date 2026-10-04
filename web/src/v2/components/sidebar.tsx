import { useMemo, useState } from "react";
import { Archive, ArchiveRestore, ArrowLeft, Box, Cpu, Folder, MessagesSquare, RefreshCw,
  Info, Keyboard, PackageSearch, Palette, PlugZap, Search, Settings, ShieldCheck, SquarePen, X } from "lucide-react";
import type { ThreadView, WorkspaceView } from "../../api/types";

export type V2SettingsSection = "general" | "permissions" | "appearance" | "voice" |
  "models" | "plugins" | "browser" | "hooks" | "git" | "environment" |
  "worktrees" | "keyboard" | "inspector" | "archived" | "extensions" | "skills" |
  "advanced-models" | "about" | "shortcuts";

export function V2Sidebar({ threads, workspaces, selectedThreadID, searchOpen, onSearchOpen,
  onNewConversation, onOpenModels, onSelectThread, onOpenSettings, onArchive,
  onOpenInspector, inspectorActive = false,
  hasMore = false, loading = false, loadingMore = false, loadFailed = false, onLoadMore, onRefresh }: {
  threads: ThreadView[];
  workspaces: WorkspaceView[];
  selectedThreadID: string;
  searchOpen: boolean;
  onSearchOpen: (open: boolean) => void;
  onNewConversation: () => void;
  onOpenModels: () => void;
  onSelectThread: (threadID: string) => void;
  onOpenSettings: () => void;
  onArchive: (thread: ThreadView) => void;
  onOpenInspector?: () => void;
  inspectorActive?: boolean;
  hasMore?: boolean; loading?: boolean; loadingMore?: boolean; loadFailed?: boolean;
  onLoadMore?: () => void; onRefresh?: () => void;
}) {
  const [search, setSearch] = useState("");
  const normalized = search.trim().toLocaleLowerCase();
  const visible = useMemo(() => threads.filter((thread) => !normalized ||
    thread.title.toLocaleLowerCase().includes(normalized)), [normalized, threads]);
  const workspaceNames = useMemo(() => new Map(workspaces.map((workspace) =>
    [workspace.id, workspace.name])), [workspaces]);
  const grouped = useMemo(() => {
    const result = new Map<string, ThreadView[]>();
    for (const thread of visible) {
      const id = thread.workspace_id ?? "";
      const group = result.get(id);
      if (group) group.push(thread);
      else result.set(id, [thread]);
    }
    return [...result.entries()];
  }, [visible]);
  const duplicateNames = useMemo(() => {
    const counts = new Map<string, number>();
    for (const { name } of workspaces) counts.set(name, (counts.get(name) ?? 0) + 1);
    return new Set([...counts].filter(([, count]) => count > 1).map(([name]) => name));
  }, [workspaces]);

  return <aside className="v2-sidebar">
    <div className="v2-sidebar-brand"><strong>Universal Code</strong>
      {onRefresh && <button aria-label="刷新对话列表" onClick={onRefresh} type="button"><RefreshCw aria-hidden="true" size={15} /></button>}
      <button aria-label="搜索" onClick={() => onSearchOpen(!searchOpen)} type="button">
        {searchOpen ? <X aria-hidden="true" size={16} /> : <Search aria-hidden="true" size={16} />}
      </button></div>
    <nav aria-label="对话导航" className="v2-sidebar-actions">
      <button onClick={onNewConversation} type="button"><SquarePen aria-hidden="true" size={16} />
        <span>新对话</span></button>
      <button onClick={onOpenModels} type="button"><Cpu aria-hidden="true" size={16} />
        <span>接入模型</span></button>
    </nav>
    {searchOpen && <label className="v2-sidebar-search"><Search aria-hidden="true" size={15} />
      <input aria-label="搜索对话" autoFocus onChange={(event) => setSearch(event.target.value)}
        placeholder="搜索已加载的对话标题…" type="search" value={search} /></label>}
    {searchOpen && <p className="v2-history-scope">搜索{threads.length}条已加载标题，不含消息正文
      {loading ? "；正在读取" : loadFailed ? "；本次加载未完成，请重试"
        : hasMore ? "；更早记录可继续加载" : ""}。归档对话在设置中查看。</p>}
    <div className="v2-thread-scroll">
      {loading && <p className="v2-history-scope" role="status">正在加载对话…</p>}
      {!loading && !loadFailed && grouped.length === 0 && <div className="v2-sidebar-empty"><MessagesSquare size={16} />
        {normalized ? "已加载的标题中没有匹配项" : "暂无对话"}</div>}
      {grouped.map(([workspaceID, workspaceThreads]) => {
        const name = workspaceID ? workspaceNames.get(workspaceID) ?? "工作区" : "本地任务";
        const shortID = workspaceID.length > 16 ? `…${workspaceID.slice(-6)}` : workspaceID;
        const label = workspaceID && (duplicateNames.has(name) || !workspaceNames.has(workspaceID))
          ? `${name} · ${shortID}` : name;
        return <section aria-label={`项目 ${label}`} className="v2-thread-group" key={workspaceID}>
        <header title={workspaceID || name}><Folder aria-hidden="true" size={15} /><span>{label}</span></header>
        {workspaceThreads.map((thread) => <div className={`v2-thread-row-shell${selectedThreadID === thread.id
          ? " is-selected" : ""}`} key={thread.id}>
          <button className="v2-thread-row" onClick={() => onSelectThread(thread.id)} type="button">
            <span>{thread.title}</span><i className={`state-${thread.composer_state}`} />
          </button>
          <button aria-label={`归档 ${thread.title}`} className="v2-thread-more"
            onClick={() => onArchive(thread)} title="归档此对话" type="button">
            <Archive aria-hidden="true" size={15} />
          </button>
        </div>)}
      </section>; })}
      {loadFailed && <p className="v2-history-scope" role="alert">对话列表加载失败，已有记录仍可打开。
        <button onClick={hasMore ? onLoadMore : onRefresh} type="button">重试加载对话</button></p>}
      {hasMore && <button className="v2-load-history" disabled={loadingMore}
        onClick={onLoadMore} type="button">{loadingMore ? "正在加载更早对话…" : "加载更早对话"}</button>}
    </div>
    <div className="v2-sidebar-footer">
      {onOpenInspector && <button aria-pressed={inspectorActive} onClick={onOpenInspector} type="button">
        <Box aria-hidden="true" size={16} />Inspector</button>}
      <button onClick={onOpenSettings} type="button"><Settings aria-hidden="true" size={16} />设置</button>
    </div>
  </aside>;
}

const settingsGroups: Array<{ label: string; items: Array<{
  id: V2SettingsSection; label: string; icon: typeof Settings;
}> }> = [
  { label: "应用", items: [
    { id: "general", label: "常规", icon: Settings },
    { id: "appearance", label: "外观", icon: Palette },
    { id: "shortcuts", label: "快捷键", icon: Keyboard },
    { id: "about", label: "关于", icon: Info },
  ] },
  { label: "模型与扩展", items: [
    { id: "models", label: "模型", icon: Cpu },
    { id: "extensions", label: "扩展与代码智能", icon: PlugZap },
    { id: "skills", label: "Skill 包", icon: PackageSearch },
  ] },
  { label: "任务与诊断", items: [
    { id: "permissions", label: "当前任务权限", icon: ShieldCheck },
    { id: "inspector", label: "Inspector 偏好与诊断", icon: Box },
  ] },
];

export function V2SettingsSidebar({ section, onBack, onSelect }: {
  section: V2SettingsSection;
  onBack: () => void;
  onSelect: (section: V2SettingsSection) => void;
}) {
  const [search, setSearch] = useState("");
  const normalized = search.trim().toLocaleLowerCase();
  return <aside className="v2-sidebar v2-settings-sidebar">
    <button className="v2-settings-back" onClick={onBack} type="button">
      <ArrowLeft aria-hidden="true" size={16} />返回应用</button>
    <label className="v2-settings-search"><Search aria-hidden="true" size={15} />
      <input aria-label="搜索设置" onChange={(event) => setSearch(event.target.value)}
        placeholder="搜索设置…" type="search" value={search} /></label>
    <nav aria-label="设置分类" className="v2-settings-nav">
      {settingsGroups.map((group) => {
        const items = group.items.filter((item) => !normalized || item.label.toLocaleLowerCase().includes(normalized));
        if (!items.length) return null;
        return <section key={group.label}><h2>{group.label}</h2>{items.map(({ id, label, icon: Icon }) => {
          const active = section === id || section === "advanced-models" && id === "models" ||
            section === "plugins" && id === "extensions" || section === "keyboard" && id === "shortcuts";
          return <button aria-current={active ? "page" : undefined}
            className={active ? "is-active" : ""} key={id} onClick={() => onSelect(id)}
            type="button"><Icon aria-hidden="true" size={16} />{label}</button>;
        })}</section>;
      })}
    </nav>
    <div className="v2-settings-archive"><span>已归档</span>
      <button aria-current={section === "archived" ? "page" : undefined}
        className={section === "archived" ? "is-active" : ""}
        onClick={() => onSelect("archived")} type="button">
        <ArchiveRestore aria-hidden="true" size={16} />已归档的聊天</button></div>
  </aside>;
}
