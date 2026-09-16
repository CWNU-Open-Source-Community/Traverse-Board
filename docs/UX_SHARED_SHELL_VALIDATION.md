# 统一应用框架、Inspector 与设置：实施验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-11，Asia/Hong_Kong。用户明确要求“开始重构吧”后的实施记录。实施树为 `<workspace>`，分支 `codex/ux-journey-convergence`，HEAD `15c399c8940aeb81d3c45bb0de96759b480d02c2`。包含此前 A–S 的未提交修改，本阶段未提交、推送、合并或发布，也未改后端/API/数据库协议。原工作树只同步工作必读。

## 交付结果

- App 只挂载一套 V2 应用框架。顶栏固定提供“对话 / Inspector”，切换保留当前任务；无任务时 Inspector 提供选择提示和历史运行/会话入口。旧 `/legacy/threads/...`、`/legacy/runs/...`、`/legacy/sessions/...` 适配进同一个框架。
- 两个视图共用 V2 设置。进入设置的任务、视图和诊断资源保存在路由中，“返回应用”回到原处。当前任务权限、全局模型路由/价格、扩展登记范围分别明确；无法读取任务范围时不会降为全局。
- Inspector 改为紧凑标题、实际执行状态、事件筛选/查找和按需详情。支持全部/工具/审批/异常、已加载范围搜索、历史 Run 选择、加载更早记录和回到最新；复用原虚拟列表、分页、阅读锚点及公开结构化详情。保留精确身份和真实来源，不用相似文本或 tool round 猜测用户工作轮。
- Inspector 按需展开与对话共用的消息编辑器，保留草稿和引用；审批、停止、提交结果未知、原请求确认共用原控制器。待处理提示独立于事件过滤器。底部 Host 区只保留未处理、已批准无结果或续跑未结清项，历史结果可在记录中查看。
- 旧有效能力继续可达：运行/会话诊断、定时任务、Code Intel/MCP/Plugin、桌面 Skill 预览/安装、网页 Skill 确认、全局模型路由/价格、关于/断开连接。设置没有第二套供应商凭据编辑器。旧独立 ThreadWorkspace/SettingsView 不再挂载；高级 Run/Session 内容仍复用原组件，并非全量重写所有诊断子面板。
- Inspector 密度接上原有本地偏好，刷新初始化同一个存储键；改变间距而不缩小字体。运行记录导航偏好作用于高级 Run 页的标签范围。

## 验收证据

全部原始资料位于 [本阶段证据目录]（本地保留证据，未公开：`output/playwright/ux-shared-shell`）。

| 项目 | 实际结果与范围 |
| --- | --- |
| 前端关键回归 | `final-tests.log`：唯一集合 22 文件、185 项通过，exit 0。覆盖导航、引用/草稿、未知请求原 key、精确资源范围、Inspector/原虚拟列表、详情绑定、设置及迁入能力。随后仅补密度初始化与 CSS，相关 2 文件 25 项通过（`density-regressions.log`），两组不相加。 |
| TypeScript 与生产构建 | `build-5.log` 的 `tsc -b && vite build` exit 0。最终 `index-b8IzWfGl.js`，SHA-256 `d993ccaf25fc5f7b16fc8d125064a858bee3b16b7cedeeddf152210a56fa382c`。Vite 大块体积提示仍存在，没有据此宣称性能问题全部解决。 |
| 实际往返 | `browser-layout-2.log`：正常输入中文未发送草稿→Inspector→筛选/选记录→设置→原 Inspector→对话，草稿、选择和筛选保留，单一框架，无 API 写请求。1440×960 记录主体约 651px 高，无区域重叠。引用与未知提交由真实 Conversation/use-thread-turn 组件回归覆盖，未在产品中另发模型请求制造未知状态。 |
| 布局与主题 | `settings-and-sizes-2.log`：设置常规、扩展、Skill、全局路由/价格、权限、关于；Inspector 深色 1440/1036/800/390×905，浅色 1440×960 与 390×905；窄屏设置和详情出口可用，单一框架、无文档横向溢出，当前可见文字未含替换字符。已目视关键截图，不是所有页面×主题×尺寸全组合，也不是万能乱码检测。 |
| 密度与键盘 | `density-detail-2.log`：正常设置将列表垂直 padding 从 10px 改为 6px、gap 从 5px 改为 3px，字号均 13px；刷新重新连接后仍为紧凑。390px 的 Escape 关闭详情后焦点回选中行，无溢出。结束恢复舒展/浅色。 |
| 真实工具详情 | `inspector-tool-detail-1440-final.png` 与 `inspector-tool-detail-390.png`：读取原 S 的 workspace_read 公开详情，显示实际路径、行范围和完成结果；没有执行新工具。 |
| 高级能力入口 | `resource-journey-1.log`：运行记录、会话记录、定时任务通过正常界面可达，单一框架、无横向溢出；运行/会话→设置→返回保持精确资源地址。仅检查入口/读取，未执行其中控制操作。 |
| 旧地址与刷新 | `legacy-refresh-1.log`：直接旧 Thread 地址进入新 Inspector 并切回原对话；直接旧 Run 地址不继承之前任务；定时任务预选 Run 在刷新后保持。其余 legacy/非法地址有组件/路由测试。 |
| 存量数据 | `readonly-comparison.json`：原 S 独立 SQLite 370 表、558 行的完整行哈希集合前后完全一致；README.md/user-note.txt 两文件 SHA 不变。验收驾驶监听到的 API 写请求均为空。本阶段没有模型请求、批准、Apply、Host 命令、任务发送或真实配置更改。覆盖的是隔离 S 数据库和两文件，不是用户生产库/全盘。 |
| 格式检查 | `final-diff-check-2.log`：web/src 的 git diff --check exit 0，仅既有换行提示。首次发现迁移后的旧 settings-view 尾部多空行，已仅清除该空行。此不可达旧组件的空白清理发生在原生包构建后，不改变嵌入 dist 或程序行为，无需重编。 |

## 实测中纠正的问题与失败记录

首个生产 UI 暴露旧 grid 与新 Inspector flex 布局冲突，造成导航重叠；已改为按区域分配高度。窄屏隐藏整个应用菜单使顶部设置入口消失，已保留设置按钮。独立审查发现运行/会话全局浏览、网页断开连接和定时任务目标刷新有迁移遗漏，均已补齐；密度只保存却无视觉效果、刷新不初始化也已修正并实测。

组件首测的失败包含状态标签优先级、网页 `verification_unavailable` 漏进异常筛选、窄屏选中清除之前回焦等真实问题，已修正；也保留并区分测试 fixture 能力/类型、重复文本断言等测试问题。首次浏览器驾驶错误的持久监听导致 CLI Session closed、旧 ref、窄屏脚本提前读取异步焦点，不作产品崩溃声明；最终脚本等待详情真正关闭后验证焦点。原生静态核验首次错误假定 `go version -m` 必返回 metadata 字段，改用保存的真实构建输入与 PE 字节验证，未伪造工具返回。

## Windows 试用包

[下载或打开所在文件：TraverseBoard-UX-Shared-Shell.exe]（本地保留证据，未公开：`output/playwright/ux-shared-shell/TraverseBoard-UX-Shared-Shell.exe`）。普通 GUI 本地预览，版本 `v0.1.0-ux-shared-shell-preview`、Modified=true，大小 103,814,144 B，SHA-256 `defc3aad2909523c4ed7534d445e9ca2f65e880f3381a43d1b56f624a843b03b`。

Go production desktop 构建通过；PE 为 Windows amd64 GUI 子系统，PerMonitorV2/asInvoker manifest、15 图标和全部 116 个前端文件名称/正文静态验证通过，包含最终 JS。详情见 [原生产物报告]（本地保留证据，未公开：`output/playwright/ux-shared-shell/native-preview-validation.md`）。未启动该包，未覆盖旧 S 包、未触碰用户原生窗口或安全设置；不是签名发布。

## 仍未完成的范围

- Windows/WebView2 原生窗口的真实高 DPI、多屏、Acrylic 仍按用户此前安排留待试用；浏览器检查与 manifest 正确不能替代。
- 高级 Run/Session 内部部分旧布局、内部术语、静态生命周期标签仍保留。新主 Inspector 的实时状态已统一，不能扩大为所有高级页状态表达均已重做。
- 旧组件源码和共用样式并未全删；目前优先切断旧独立应用入口、保留真实能力，后续删除需按可达性单独处理。
- 草稿/选中记录保留针对同一页面生命周期的导航，不承诺跨应用关闭或刷新持久化。密度/主题则沿用原本地存储。
- 搜索仅已加载记录；没有全历史全文索引，没有新增按用户 Turn 分组的后端关联。完整双语、完整键盘/屏幕阅读器、真实 LSP、包体/全场景性能及原 F01–F14 清单仍不在本阶段完成声明内。
- 本轮未用用户配置执行模型修改、插件禁用或真实 Skill 安装；迁入这些写流程的行为以定向组件测试为证，不能称都已真实外部联调。

## 交接现场

专用预览为 `127.0.0.1:18876`，Node PID145012；代理到原 S 隔离 API `127.0.0.1:18874`、PID150464，S 模型夹具 PID125240/18875 保持空闲。后续使用前重新核对 PID/路径及实际 JS，不按旧 PID 盲停。Playwright `uxt` 留在原 S Thread 的浅色/舒展 Inspector，1440×960、侧栏展开、无草稿、筛选清除。uxq/uxreal 与用户窗口未作为本阶段驾驶对象。子助手全部冻结；没有未结束的构建或验收任务。
