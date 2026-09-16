import { ArrowLeft, Minus, PanelLeft, Square, X } from "lucide-react";
import { desktopBridgeAvailable, desktopIsMacPlatform } from "../../lib/desktop-bridge";
import {
  closeDesktopWindow,
  minimiseDesktopWindow,
  toggleDesktopWindowMaximised,
} from "../../lib/desktop-window";

export function V2Titlebar({ sidebarVisible, onToggleSidebar, onBack, canGoBack = false,
  onNewConversation, onOpenSettings, view = "conversation", onChangeView }: {
  sidebarVisible: boolean;
  onToggleSidebar: () => void;
  onBack: () => void;
  canGoBack?: boolean;
  onNewConversation: () => void;
  onOpenSettings: () => void;
  view?: "conversation" | "inspector";
  onChangeView?: (view: "conversation" | "inspector") => void;
}) {
  const desktop = desktopBridgeAvailable();
  const mac = desktopIsMacPlatform();
  return <header className={`v2-titlebar${mac ? " is-mac" : ""}`}>
    <div className="v2-titlebar-navigation" data-v2-no-drag="true">
      <button aria-label={sidebarVisible ? "隐藏侧栏" : "显示侧栏"}
        aria-pressed={sidebarVisible} onClick={onToggleSidebar} type="button">
        <PanelLeft aria-hidden="true" size={16} />
      </button>
      <button aria-label="返回" disabled={!canGoBack} onClick={onBack} type="button">
        <ArrowLeft aria-hidden="true" size={17} />
      </button>
    </div>
    <nav aria-label="应用菜单" className="v2-application-menu" data-v2-no-drag="true">
      <button aria-label="创建新对话" onClick={onNewConversation} type="button">新对话</button>
      <button aria-label="打开设置" onClick={onOpenSettings} type="button">设置</button>
    </nav>
    {onChangeView && <div aria-label="工作视图" className="v2-view-switch" data-v2-no-drag="true" role="group">
      <button aria-label="对话视图" aria-pressed={view === "conversation"}
        onClick={() => onChangeView("conversation")} type="button">对话</button>
      <button aria-label="Inspector 视图" aria-pressed={view === "inspector"}
        onClick={() => onChangeView("inspector")} type="button">Inspector</button>
    </div>}
    <div className="v2-titlebar-drag-region" data-v2-drag="true" />
    {desktop && !mac && <div aria-label="窗口控制" className="v2-window-controls"
      data-v2-no-drag="true">
      <button aria-label="最小化" onClick={() => void minimiseDesktopWindow()} type="button">
        <Minus aria-hidden="true" size={16} />
      </button>
      <button aria-label="最大化" onClick={() => void toggleDesktopWindowMaximised()} type="button">
        <Square aria-hidden="true" size={13} />
      </button>
      <button aria-label="关闭" className="close" onClick={() => void closeDesktopWindow()} type="button">
        <X aria-hidden="true" size={17} />
      </button>
    </div>}
  </header>;
}
