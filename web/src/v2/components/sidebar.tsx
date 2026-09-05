import { useMemo, useState } from "react";
import { Archive, ArchiveRestore, ArrowLeft, Box, ChevronDown, CircleUserRound, Code2, Cpu,
  Folder, GitBranch, Globe2, Keyboard, Link2, MessagesSquare, Mic2, MonitorCog, MoreHorizontal,
  Palette, Plug, Search, Settings, ShieldCheck, SquarePen, UserRound, X } from "lucide-react";
import type { ThreadView, WorkspaceView } from "../../api/types";

export type V2SettingsSection = "general" | "permissions" | "appearance" | "voice" |
  "models" | "plugins" | "browser" | "hooks" | "git" | "environment" |
  "worktrees" | "keyboard" | "inspector" | "archived";

export function V2Sidebar({ threads, workspaces, selectedThreadID, searchOpen, onSearchOpen,
  onNewConversation, onOpenModels, onSelectThread, onOpenSettings, onArchive }: {
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
}) {
  const [search, setSearch] = useState("");
  const [menuThreadID, setMenuThreadID] = useState("");
  const normalized = search.trim().toLocaleLowerCase();
  const visible = useMemo(() => threads.filter((thread) => !normalized ||
    thread.title.toLocaleLowerCase().includes(normalized)), [normalized, threads]);
  const workspaceNames = useMemo(() => new Map(workspaces.map((workspace) =>
    [workspace.id, workspace.name])), [workspaces]);
  const grouped = useMemo(() => {
    const result = new Map<string, ThreadView[]>();
    for (const thread of visible) {
      const label = thread.workspace_id ? workspaceNames.get(thread.workspace_id) ?? "工作区" : "本地任务";
      result.set(label, [...(result.get(label) ?? []), thread]);
    }
    return [...result.entries()];
  }, [visible, workspaceNames]);

  return <aside className="v2-sidebar">
    <div className="v2-sidebar-brand"><button type="button"><strong>Traverse</strong>
      <ChevronDown aria-hidden="true" size={14} /></button>
      <button aria-label="搜索" onClick={() => onSearchOpen(!searchOpen)} type="button">
        {searchOpen ? <X aria-hidden="true" size={16} /> : <Search aria-hidden="true" size={16} />}
      </button></div>
    <nav aria-label="对话导航" className="v2-sidebar-actions">
      <button onClick={onNewConversation} type="button"><SquarePen aria-hidden="true" size={16} />
        <span>新对话</span><kbd>Ctrl N</kbd></button>
      <button onClick={onOpenModels} type="button"><Cpu aria-hidden="true" size={16} />
        <span>接入模型</span></button>
      <button onClick={() => onSearchOpen(true)} type="button"><Search aria-hidden="true" size={16} />
        <span>搜索对话</span></button>
    </nav>
    {searchOpen && <label className="v2-sidebar-search"><Search aria-hidden="true" size={15} />
      <input aria-label="搜索对话" autoFocus onChange={(event) => setSearch(event.target.value)}
        placeholder="搜索对话…" type="search" value={search} /></label>}
    <div className="v2-thread-scroll">
      {grouped.length === 0 && <div className="v2-sidebar-empty"><MessagesSquare size={16} />暂无对话</div>}
      {grouped.map(([workspace, workspaceThreads]) => <section className="v2-thread-group" key={workspace}>
        <header><Folder aria-hidden="true" size={15} /><span>{workspace}</span></header>
        {workspaceThreads.map((thread) => <div className={`v2-thread-row-shell${selectedThreadID === thread.id
          ? " is-selected" : ""}`} key={thread.id}>
          <button className="v2-thread-row" onClick={() => onSelectThread(thread.id)} type="button">
            <span>{thread.title}</span><i className={`state-${thread.composer_state}`} />
          </button>
          <button aria-expanded={menuThreadID === thread.id} aria-haspopup="menu"
            aria-label={`${thread.title} 的操作`} className="v2-thread-more"
            onClick={() => setMenuThreadID((current) => current === thread.id ? "" : thread.id)} type="button">
            <MoreHorizontal aria-hidden="true" size={15} />
          </button>
          {menuThreadID === thread.id && <div className="v2-thread-row-menu" role="menu">
            <button onClick={() => { setMenuThreadID(""); onArchive(thread); }} role="menuitem" type="button">
              <Archive aria-hidden="true" size={14} />归档</button>
          </div>}
        </div>)}
      </section>)}
    </div>
    <div className="v2-sidebar-footer">
      <button onClick={onOpenSettings} type="button"><Settings aria-hidden="true" size={16} />设置</button>
    </div>
  </aside>;
}

const settingsGroups: Array<{ label: string; items: Array<{
  id: V2SettingsSection; label: string; icon: typeof Settings;
}> }> = [
  { label: "个人", items: [
    { id: "general", label: "常规", icon: Settings },
    { id: "models", label: "模型", icon: Cpu },
    { id: "permissions", label: "权限", icon: ShieldCheck },
    { id: "appearance", label: "外观", icon: Palette },
    { id: "voice", label: "语音", icon: Mic2 },
    { id: "keyboard", label: "键盘快捷键", icon: Keyboard },
  ] },
  { label: "集成", items: [
    { id: "plugins", label: "插件", icon: Plug },
    { id: "browser", label: "浏览器", icon: Globe2 },
  ] },
  { label: "编码", items: [
    { id: "hooks", label: "钩子", icon: Link2 },
    { id: "git", label: "Git", icon: GitBranch },
    { id: "environment", label: "环境", icon: MonitorCog },
    { id: "worktrees", label: "Worktrees", icon: Code2 },
    { id: "inspector", label: "Inspector", icon: Box },
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
        return <section key={group.label}><h2>{group.label}</h2>{items.map(({ id, label, icon: Icon }) =>
          <button aria-current={section === id ? "page" : undefined} className={section === id ? "is-active" : ""}
            key={id} onClick={() => onSelect(id)} type="button"><Icon aria-hidden="true" size={16} />{label}</button>)}</section>;
      })}
    </nav>
    <div className="v2-settings-archive"><span>已归档</span>
      <button aria-current={section === "archived" ? "page" : undefined}
        className={section === "archived" ? "is-active" : ""}
        onClick={() => onSelect("archived")} type="button">
        <ArchiveRestore aria-hidden="true" size={16} />已归档的聊天</button></div>
  </aside>;
}
