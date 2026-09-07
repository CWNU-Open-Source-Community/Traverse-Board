import { ArrowLeft, ArrowRight, Minus, PanelLeft, Square, X } from "lucide-react";
import { desktopBridgeAvailable, desktopIsMacPlatform } from "../../lib/desktop-bridge";
import {
  closeDesktopWindow,
  minimiseDesktopWindow,
  toggleDesktopWindowMaximised,
} from "../../lib/desktop-window";

export function V2Titlebar({ sidebarVisible, onToggleSidebar, onBack, canGoBack = false }: {
  sidebarVisible: boolean;
  onToggleSidebar: () => void;
  onBack: () => void;
  canGoBack?: boolean;
}) {
  const desktop = desktopBridgeAvailable();
  const mac = desktopIsMacPlatform();
  return <header className={`v2-titlebar${mac ? " is-mac" : ""}`}>
    {!mac && <div className="v2-titlebar-navigation" data-v2-no-drag="true">
      <button aria-label={sidebarVisible ? "隐藏侧栏" : "显示侧栏"}
        aria-pressed={sidebarVisible} onClick={onToggleSidebar} type="button">
        <PanelLeft aria-hidden="true" size={16} />
      </button>
      <button aria-label="返回" disabled={!canGoBack} onClick={onBack} type="button">
        <ArrowLeft aria-hidden="true" size={17} />
      </button>
      <button aria-label="前进" disabled type="button">
        <ArrowRight aria-hidden="true" size={17} />
      </button>
    </div>}
    <nav aria-label="应用菜单" className="v2-application-menu" data-v2-no-drag="true">
      <button type="button">文件</button><button type="button">编辑</button>
      <button type="button">视图</button><button type="button">帮助</button>
    </nav>
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
