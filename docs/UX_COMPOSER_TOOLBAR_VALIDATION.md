# 输入工具栏收敛：实施与验证记录

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-12。状态：**本批工具栏实现、定向测试、最终浏览器验收、Windows 本地预览构建及现场收尾已完成。原生窗口等未验边界见末节。**

本报告只覆盖本次输入工具栏收敛。实施目录为 `<workspace>`，分支 `codex/ux-journey-convergence`，沿用开工时约 782 项已有 dirty；本批不 commit、push 或 release。依据为本对话工作必读 v2.05 的最新授权。证据根目录为 [ux-composer-toolbar](../output/playwright/ux-composer-toolbar/)。

## 1. 用户要求与实现

用户指出上传、引用、模式、计划、权限、网络和模型控件堆在一起，难以分辨主次。最后明确纠正：**默认直接处理不需要按钮或下拉；计划模式应为独立开关**，不能沿用更早“把计划模式放进＋菜单”的建议。

| 区域 | 当前实现 | 保留的行为边界 |
| --- | --- | --- |
| 添加内容 | 单一＋展开“添加图片或文件”“引用项目文件”，桌面能力可用时提供显式粘贴文件；已选项目引用移到输入内容区。 | 调用原上传、项目引用和粘贴管道。展开菜单不读剪贴板、不上传、不发送；不可用的引用保留原因。没有新增文件夹上传能力。 |
| 计划模式 | 独立 `type=button` 开关，以 `aria-pressed` 表示状态；没有“直接执行”按钮或工作方式下拉。已有任务调用原 `enter_plan` / `enter_deliver`；新任务创建前只保存本地方式。 | 切换不携带消息正文、不提交未发稿、不扩大权限。原服务端准备与状态检查继续生效。 |
| 方案与恢复入口 | 无方案、无原请求、无异常时不常驻第二个“计划”入口；有方案、待核对请求、损坏记录或错误时显示。 | 保留最新方案选择、确认时的明确消息、未发要求阻止确认、返回对话修改、精确原 key 核对与重试、跨任务迟到响应保护。 |
| 权限与网络 | 常驻图标反映真实权限档位；网页访问与搜索收进权限面板的折叠区，展开后复用原网络控件。 | 没有权限数据时不冒称保守模式；没有验证搜索就不声称已可用。原显式升权确认、降级与表单防冒泡保留，不自动授予网络或运行验证。 |
| 模型与发送 | 常驻模型简称，完整供应商、原模型 ID 与“推理强度：随模型”保留在可访问名称、提示和菜单；发送与模型保持独立对齐。 | 不新增不存在的思考档位、语音、目标调度或插件操作。原可选资格、更新、失败与焦点处理保留。 |
| 宽窄布局 | 输入内容与底栏分层，窄屏给次级行和弹出面板留空间；菜单可滚动，避免靠缩小字号挤满一行。 | 已在实际浏览器检查浅深色、320/390/900/1280 px；原生窗口与高 DPI 仍未验。 |

主要实现位于 `composer.tsx`、`composer-add-menu.tsx`、`file-context.tsx`、`phase-picker.tsx`、`thread-plan.tsx`、`permission-control.tsx`、`model-route-control.tsx` 和对应样式。没有新增 Go/API 权限或执行协议。

## 2. 定向测试与构建

以下是不同时间点的证据集合，**互有重叠，不相加为总测试数，也不称全仓 CI**。

| 检查 | 实际结果 | 证据 |
| --- | --- | --- |
| 相关前端联合 | 11 文件、84 项通过 | [related-final-1.log](../output/playwright/ux-composer-toolbar/related-final-1.log) |
| 添加菜单后续回归 | 1 文件、5 项通过，覆盖第二次点击关闭与 Shift+Tab、Tab、Escape、外部输入焦点、禁用项和非提交按钮 | [menu-final.log](../output/playwright/ux-composer-toolbar/menu-final.log) |
| 计划与主对话 | 2 文件、25 项通过；包含开启/关闭只发 mode 请求、有未发稿时外层 form 提交次数为 0、空态隐藏、错误回焦、无方案原 key 恢复、损坏记录保留、确认与首条消息 | [plan/tests-final.log](../output/playwright/ux-composer-toolbar/plan/tests-final.log)、[子项记录](../output/playwright/ux-composer-toolbar/plan/validation.md) |
| 权限、模型和网络 | 3 文件、31 项通过，保留权限确认/取消不误发、引用搜索不误发、模型选择及迟到结果焦点等回归 | [permission-model-tests-final-2.log](../output/playwright/ux-composer-toolbar/permission-model-tests-final-2.log)、[子项记录](../output/playwright/ux-composer-toolbar/permission-model-validation.md) |
| 类型检查 | `tsc -b --pretty false` 通过；包含后续整合检查 | [typecheck-2.log](../output/playwright/ux-composer-toolbar/typecheck-2.log)、[plan/typecheck.exit](../output/playwright/ux-composer-toolbar/plan/typecheck.exit) |
| Web 最终构建 | `tsc -b && vite build` exit 0，JS `index-pdCT2I5F.js` / CSS `index-B7zuVUIa.css` | [web-build-final-5.log](../output/playwright/ux-composer-toolbar/web-build-final-5.log)；先前 2/3/4 构建均保留，最后以第 5 次为准 |

联合测试日志包含测试环境 `HTMLCanvasElement.getContext` 未实现提示，但 84 项测试实际通过；不能据此宣称真实浏览器图片复制链在本批重测。Web 构建保留超过 500 kB 的 chunk 大小警告，没有把警告删除或称已完成性能优化。

早期失败保留：

- 计划旧实现红测为 4 失败、5 通过，确认旧下拉与新交互不符。第一轮联合另有 2 项 App 定位仍使用旧模型可访问名称；按真实新名称修改定位后通过，没有放宽发送或身份断言。
- 添加菜单第一轮发生 Tab 后焦点落到 body，见 [root-related-1.log](../output/playwright/ux-composer-toolbar/root-related-1.log)；修复后的 5 文件 20 项通过见 [root-related-2.log](../output/playwright/ux-composer-toolbar/root-related-2.log)，随后菜单扩展为上述 5 项。
- 权限/模型子项曾有新测试文本空格假设、旧直接引用按钮定位，以及测试库不支持的 `exact` 参数问题；原日志保留，最终按实际入口和类型契约修正。

## 3. 已完成的真实浏览器与账本核对

本批复用上批**隔离验收 fixture**，只开本批 API 端口 18902 与专用浏览器 profile。没有启动 Provider，没有重新执行上批七条消息、ZIP 工具或资格探测。以下真实操作与组件网络 fixture 测试分别记录。

### 3.1 计划模式开启／关闭

根代理通过正常 UI 在同一任务 `thread-run-20260912040845-46beb4931a1c` 实际产生两条 `POST /threads/{id}/plan`，动作依次为 `enter_plan`、`enter_deliver`，均绑定原 Run `run-20260912044054-4bd2d4524e1b`。请求不含消息正文。浏览器记录中的未发稿在前后保持相同；该轮入口使用 build 2 的 `index-CV8YfQdL.js`，不冒称后续 CSS 候选已经复验。见 [browser-plan-result.log](../output/playwright/ux-composer-toolbar/browser-plan-result.log)。

独立 SQLite 核对使用 `mode=ro`、`query_only` 和读事务，对 46 张表拍摄快照；[plan-roundtrip-check.json](../output/playwright/ux-composer-toolbar/api/plan-roundtrip-check.json) 的 12 项检查全部通过，[mode-validation.md](../output/playwright/ux-composer-toolbar/api/mode-validation.md) 说明了合法增量：

- 原 Run 在正常 Plan 准备流程中由 `running` 变为 `paused`；没有新增 Thread、Run 或 Session。报告不将这种既有准备行为误称“所有状态完全不变”。
- mode revision 2 在 `2026-09-12T06:27:13.8938072Z` 进入 plan，revision 3 在 `06:27:13.95708Z` 返回 deliver；两个操作账本精确绑定各自快照。
- Run 事件只新增 sequence 125 的一次状态改变，以及 126、127 的两次 phase 改变。phase 事件均为 `capability_grant:false`，策略与权限/profile/trust 不变。
- 其余 42 张表 hash 相同：原 7 条已提交输入、8 次业务模型请求、2 次资格请求、唯一已完成 ZIP call/Job 保持原样；没有本批输入、上传、审批、工具或模型请求。
- 两个隔离仓库的 HEAD/index/porcelain 与四个种子文件 hash 保持；9 个 live/effect 计数和外键错误均为 0。

这是模式切换的真实回合，不是一次新的模型规划、方案确认或文件执行验收；后几项不由这两条 POST 推断。

### 3.2 菜单与文件选择器

浏览器已实际打开添加菜单、观察文件选择器事件，菜单与草稿/焦点的读取结果保留。选择器没有加入文件，没有上传。文件选择器处理后 Playwright CLI 仍显示残留的 modal bookkeeping 状态，根代理通过正常 close/reopen 继续；**不把该脚本视为完整上传旅程通过，也不将 CLI 残留直接定性为应用上传失败**。见 [browser-interactions.log](../output/playwright/ux-composer-toolbar/browser-interactions.log)。

实际上传、原件下载、复制/粘贴、草稿重启与 ZIP 执行属于上批已有证据，本批复用其管道，没有重复扩大模型或工具验收。

## 4. 最终浏览器验收

实际 HTTP 静态资源与最终 dist 逐字节一致，数据库身份保持原值，见 [assets-candidate4.json](../output/playwright/ux-composer-toolbar/api/assets-candidate4.json)。最后浏览器也实际加载 `index-pdCT2I5F.js`，不是源码截图或设计稿。

实看发现并修正了四类问题：＋第二次点击受 blur/click 次序影响重新打开；权限/新对话网络面板透出底层正文；浅色发送按钮被通用样式覆盖、深色 hover 和禁用状态对比不正确；320 px 模型菜单以触发器为锚点时越出左边缘。最后分别复验原操作，不以隐藏错误或减少断言处理。

- 同一 Thread：浅/深色 × 1280/900/390/320 px × 工具栏/添加菜单/权限内网络/模型菜单，共 32 个最终截图场景。页面和弹层均未越出视口，输入区与弹层内部宽度无截断，FFFD 和 `[object Object]` 为 0，未发稿一直相同，POST 为 0。见 [最终布局日志](../output/playwright/ux-composer-toolbar/browser-layout-final.log)与[尺寸/颜色原始记录](../output/playwright/ux-composer-toolbar/browser-layout-final-results.log)。900 px 时展开的绝对定位弹层超出工具按钮组的自身宽度是预期行为，已单独检查弹层边界；不能据按钮组 scrollWidth 将其误判为正文溢出。
- 新对话：此前一次本地计划开/关的 `aria-pressed` 为 true/false，草稿保留、POST 为 0，见 [browser-new-plan.log](../output/playwright/ux-composer-toolbar/browser-new-plan.log)。最终又检查浅深色 × 1280/390/320 px 的 6 个网络弹层场景，均不透明、位于视口内、clientWidth=scrollWidth。浅色发送 hover 为白箭头/深背景，深色为深箭头/白背景，禁用态均恢复灰色且实际 disabled。没有创建对话或变更网络范围。
- 通过＋读取种子 README.md 并选择引用，选择器关闭后显示在输入内容区。新旧对话草稿不串；最后设置与 Inspector 往返后原 Thread 正文和 README.md 引用仍在，POST 为 0，见 [browser-new-final-results.log](../output/playwright/ux-composer-toolbar/browser-new-final-results.log)。读取使用原工作区证据接口，未上传新文件、未扩大项目权限。
- 代表截图已实际目视：[工具栏局部](../output/playwright/ux-composer-toolbar/final-toolbar-preview.png)、[320 px 模型面板](../output/playwright/ux-composer-toolbar/final-dark-320-model.png)、[320 px 新对话网络](../output/playwright/ux-composer-toolbar/final-new-light-320-network.png)。上一轮的权限透字、320 px 越界与 CLI 失败证据继续保留，不冒称早期候选全部通过。

## 5. Windows 预览与收尾

最终本地试用包：[TraverseBoard-UX-Composer-Toolbar.exe](../output/playwright/ux-composer-toolbar/delivery-final/TraverseBoard-UX-Composer-Toolbar.exe)，105,368,064 字节，SHA-256 `6d33cb2fb90bcd3150313451831d9ebf4cd48d500cbb4583bc06de9de1ccfeba`。根代理独立读取文件大小和 SHA，与构建审计一致，见 [root-artifact-check.json](../output/playwright/ux-composer-toolbar/delivery-final/root-artifact-check.json)。

构建 exit 0；20 项静态检查通过，1786 个源码输入构建前后无变化、116 个 dist 资源逐字节嵌入。包含最终 JS/CSS，PE 为 Windows GUI，清单声明 PerMonitorV2、asInvoker，含 15 个图标，不带 DevTools 构建标签。见 [构建结果](../output/playwright/ux-composer-toolbar/delivery-final/native-build-result.json)和[静态审计](../output/playwright/ux-composer-toolbar/delivery-final/native-artifact-validation.json)。**最终 EXE 没有启动；这是未提交的本地预览，不是签名发布，也不等于实际高 DPI/Windows 窗口验收。**

浏览器已正常关闭，见 [browser-close-final.log](../output/playwright/ux-composer-toolbar/browser-close-final.log)。2026-09-12 14:40 +08 核对路径、SHA、启动时间和端口后，只停止本批 API PID 91916；18902 已无监听，Provider 从未启动，fixture 构建残留进程为 0。停前停后 46 表 hash 全同，最终仍只有原先两次合法模式切换；9 个 live/effect 计数、外键错误为 0，两个仓库及四个种子文件不变。见 [归属核验](../output/playwright/ux-composer-toolbar/api/cleanup-ownership.json)、[收尾结果](../output/playwright/ux-composer-toolbar/api/cleanup-result.json)、[最终模式审计](../output/playwright/ux-composer-toolbar/api/cleanup-mode-check.json)。DB、profile、日志和历史包均保留；本批临时/Go/npm/browser 缓存继续使用 D 盘，最后 C 盘约 58.68 GiB 可用。

## 6. 未验和未扩展的边界

- 原生 WebView2 系统剪贴板、Explorer 物理文件粘贴、系统菜单、IME、高 DPI 和桌面多窗口外观，没有由本批 DOM/浏览器测试得到新增结论。
- 本批没有重新验证完整的上传/下载和长期草稿硬崩溃持久性；上批记录的“硬退出曾恢复较早草稿”限制仍保留，不能因工具栏调整宣称已解决。
- 没有新增任意文件夹上传、目标调度、语音、插件调用、推理档位控制或附件缓存自动回收，也没有调用真实用户 Provider 验证模型质量。
- 没有把 Fixture 的旧消息或文件说成由本批模型创造，没有改写旧失败、误发或工具记录。所有新增临时文件与缓存继续留在 D 盘。
