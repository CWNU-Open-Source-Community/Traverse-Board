# ADR 0078: Desktop Permission Center And Native Acrylic

- Status: Accepted
- Date: 2026-07-28
- Scope: Desktop D1-UX10 and the schema-v88 operator surface

## Context

Schema v88 introduced four Run execution permission ceilings, but the first
React integration placed permission controls beside ordinary Run content.
That made three independent concepts easy to confuse:

1. the permission ceiling (`conservative|approval|full_access|debug`);
2. the interaction shape (`preview|controlled|debug|cyber`);
3. the execution environment (`preview|local|docker`).

The Settings surface also needed the same resizable navigation behavior as the
workbench. Its earlier image-backed visual treatment did not react to the
actual desktop behind the window and could make decorative opacity look like
native glass.

## Decision

Prayu Desktop adds a dedicated `权限` Settings page. It is the single operator
surface for all three Run-scoped execution dimensions. The page consumes the
existing Go-owned HTTP/OpenAPI controls; it does not introduce a renderer-only
permission state or a second authorization path.

The page requires an exact selected Run. With no Run selected it renders an
empty state and cannot change a global default. Elevated permission choices
retain their exact confirmation step and are disabled when the current
process-local startup capabilities do not support them. The UI never claims
that a selected mode has granted execution authority.

The Settings navigation reuses the workbench resize component:

```text
minimum: 232 px
default: 286 px
maximum: 420 px
```

The width is a local presentation preference only. Pointer drag, keyboard
adjustment, Home/End, and double-click reset do not call Go or change any Run
state.

On Windows, the Wails shell uses the native Acrylic backdrop with a transparent
WebView and translucent window. React supplies light and dark glass tokens
instead of a full-window background image. Selected navigation and segmented
controls use a high-contrast rounded white surface; inactive controls remain
translucent. The selected light/dark preference is stored locally and also
updates the native Wails window theme.

The native compositor remains authoritative. Acrylic may become more opaque
when Windows disables transparency, under remote/low-power conditions, or
while a window is maximized. Prayu accepts that platform fallback rather than
imitating wallpaper blur with a screenshot.

Renderer integrity, asset restrictions, navigation blocking, and the
in-process Go API remain unchanged. Native Acrylic is a presentation feature,
not a browser, filesystem, process, credential, or network capability.

## Consequences

- Permission controls have one discoverable home without becoming global
  authority.
- Permission, interaction, and environment remain visibly independent and are
  still validated together by Go.
- Settings and workbench sidebars share one bounded, accessible resize
  behavior.
- Light and dark appearances react to the real Windows desktop instead of an
  embedded background image.
- Moving the window can change the sampled blur because Windows composes the
  actual wallpaper behind it.
- Reduced-transparency and maximized fallbacks remain readable but may not show
  visible wallpaper color.
- No installer, registry key, startup entry, or service is introduced.

## Verification

React tests cover permission empty/loading/error/confirmation states, runtime
capability closure, interaction/profile compatibility, Settings navigation,
theme selection, and desktop-window theme calls. Go tests cover the shared
permission service, CLI, HTTP control, OpenAPI, Desktop bootstrap, and native
Acrylic options without weakening renderer integrity.

The Windows executable was also exercised as a real non-maximized desktop
window. The verification confirmed wallpaper-dependent Acrylic, light/dark
switching, the dedicated permission page, rounded selected states, and a
Settings sidebar drag from 286 px to 350 px. This manual check controlled only
the temporary Prayu process and did not interact with or terminate Codex.

## 中文结论

Prayu 桌面端把权限上限、交互方式和执行环境统一放进独立的“权限”设置页，
但它们仍是三个由 Go 共同校验的维度。页面必须绑定当前 Run；没有选中 Run
时只显示空状态。切换按钮只表达操作者意图，不能直接授予进程、终端、网络或
文件权限。

设置侧栏复用工作台的 232-420 px 有界拖拽。Windows 窗口改用原生 Acrylic
和透明 WebView，浅色、深色以及圆角白色选中态由 CSS 令牌提供，不再把背景图
伪装成毛玻璃。透明度关闭、远程会话或最大化时，Windows 可以回退为更不透明的
表面；Prayu 保持可读性，不用桌面截图模拟模糊。
