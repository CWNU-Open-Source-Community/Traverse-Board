# 图片输入与应用预览验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-11。限定范围：本对话授权的截图输入、真实图像传入与本机应用预览。不是全产品完成度声明，也不是模型视觉能力测评。

## 结果与使用方式

对话输入框可以选择、粘贴或拖入 PNG/JPEG/WebP 图片，在发送前预览或移除。纯图片也能发送。消息历史和 Inspector 可查看原图；重开后的草稿、原图片身份及未知请求核对沿用既有恢复机制。

模型须明确支持图像。精确模型的视觉声明可在模型设置中配置；未知或不支持时保留图片并提示，不能把图片路径当成像素。兼容服务的手工声明不等于已经验证其视觉理解能力。

对话右上角的“应用预览”打开项目的本机地址。开发服务由当前任务的托管命令启动；预览复用独立的受控浏览器，需要既有的完全访问或调试权限和浏览器控制授权。地址使用 `http://127.0.0.1:端口` 或 IPv6 loopback。面板提供页面截图、页面文字及已识别控件的输入/点击，操作后重新观察。

输入动作在当前光标处追加文字，界面已说明。旧观察过期或页面改变时必须刷新；未知响应不自动重复点击。收起面板与停止预览分别控制面板和浏览器，停止开发服务使用当前任务的执行控制。

## 真实图片旅程

证据根目录：`output/playwright/ux-images-preview/`。图片汇总为 `browser/image-browser-validation.json`，12 项检查通过，4 次图片协议请求；不把历史图重传数计成额外用户发送。

- 1920×1080 中文文件名 PNG 的实际选择、认证读取、原尺寸预览和移除；640×360 第二张图片用于后来草稿保护。
- 浏览器真实 File/ClipboardEvent/DragEvent 进入产品粘贴/拖入处理，上传字节与原文件 SHA256 一致。这不是 Windows 物理剪贴板验收。
- 刷新和完整浏览器退出重开保留中文空白/换行正文和原图片 ID/SHA。不同项目不串图。
- 原发送实际成功但响应丢失后，重开仅 GET 原请求标识核对，未新增 POST；后来的文字和图片仍保留。首次实际失败暴露的图片指纹遗漏已修复，使用同一个原请求复验通过。
- 既有对话纯图、图文以及新建对话首条纯图都实际进入模型请求；四次请求的原生图像顺序、类型、大小和 SHA 与输入一致。
- Inspector 可以查看原图及其 ID/SHA。不伪造空正文用户指令。

协议侧定向回归覆盖 OpenAI Chat/Responses、Anthropic、Ollama 的原生图片格式；PNG/JPEG/WebP 解码、格式/大小限制、精确模型能力与历史上下文图片预算等见后端索引。真实浏览器主旅程使用 PNG 与本机固定 OpenAI Chat 协议服务。

## 真实应用预览旅程

在独立工作区预置 Node 应用，由产品的 `command_runtime` 实际启动。预置应用不是模型生成成果；本次验收的是进程启动、持续运行、浏览器操作与图片证据接通。

1. 产品收据与 Windows API→PowerShell 7→Node 父子链、Job、18886 端口和健康响应一致。模型普通等待及下一条输入不会误杀仍需使用的托管应用。
2. 独立 Chrome 实际导航至指定地址，捕获真实页面。带禁用按钮、隐藏字段以及输入事件更新提示的页面也能正常观察。
3. 后端 API 实际中文输入、点击、状态更新通过；重复使用旧 snapshot 的点击返回 409，计数未再次增加。
4. 模型实际调用 `browser_screenshot`。磁盘 PNG、精确工具收据与模型请求的原生图片均为 25,904 字节，SHA256 `02c73240a868543d60c08ea9780f60ba4b5d4f41d7eca34f8e72bfd09df419a9`。证据 `browser/preview-api-api10-screenshot-disk-wire-match.json`。
5. 前端 `index-CeHJEEvB.js` 完整界面初验：主界面输入“主界面最终中文验证”后得到真实 input 事件，点击后页面输出同文、更新次数 1，两次操作均 HTTP 200。元数据中的隐藏字段不出现在操作列表，禁用按钮不能点击。证据 `browser/preview-ui-bundle11.log`。
6. 1280、960、390 CSS 像素宽度下，文档和预览面板均无水平溢出或 U+FFFD 替代字符；面板字号 14px，背景不透出底层正文。滚动后关闭按钮仍可见，收起后焦点回到“应用预览”。已目视对应截图。
7. 收尾发现重开面板会依据缓存 ready 误读已经关闭的浏览器。修复为每次挂载先取得当前 session，覆盖生产 5 秒缓存新鲜期。**最终包入口 `index-C7c50Emn.js` 已再次实际输入/点击成功**，然后在收起面板、通过 API 关闭浏览器后 135ms 内重开：截图读取次数保持 1，图片 0、错误提示 0。证据 `browser/preview-ui-cache-real.log`；两版 CSS 入口同为 `index-BwPsyUIV.css`。

主界面预览是截图与受约束的 DOM 控件操作，不是完整的交互式浏览器嵌入。上述结果不覆盖任意 iframe、canvas、拖拽或复杂网页控件。

## 实测发现并修复的缺口

| 缺口 | 修复与证据 |
| --- | --- |
| CLI 未提供 FullCDP 所需的共享执行能力 | API 与其他服务复用同一个 RuntimeAuthority；CLI 实际启动通过 |
| Run 读取缺图片计数字段、未知图片请求核对漏图片指纹 | 补齐原 SQL/只读观察器，原丢响应请求重开后仅 GET 核对通过 |
| 普通模型 wait 导致后台应用被终止 | 仅对精确归属的运行中 Job 保持等待，暂停/审批/撤权回收规则保持 |
| 打开预览后停在 about:blank | 复用既有受约束导航，导航成功后才发布就绪 |
| Chromium 临时节点编号重分配被误判为文档变化 | 文档使用稳定 backend 身份，动作在重读文档后重新定位并核对原元素；输入后的正常 HTML 状态更新可继续 |
| 页面已有 disabled 控件使整次观察失败 | 保留只读展示，不给该控件动作身份；新发生的元素漂移仍拒绝 |
| 合法静态 Debug/Full 权限被截图工具层错误过滤 | 兼容 generation 0 的已有授权语义，继续要求实时授权、RunFence 及精确会话绑定；动态撤权仍拒绝 |
| 重开面板显示默认地址/浏览器、透明背景文字叠加 | 回显实际观察地址与会话浏览器，使用不透明背景；补充输入追加语义、滚动关闭入口 |
| 面板重开时缓存 ready 误触发旧浏览器读取 | `refetchOnMount: always` 并等待当前会话查询完成，正常轮询不重复自动截图；生产 staleTime=5000 的回归及真实135ms重开通过 |

没有通过删除历史失败或放宽导航、元素身份、会话和撤权校验来获得通过。API7/8 仅诊断的 overlay 未进入生产代码或交付构建。

## 验证边界与记录

- [后端验证索引](../output/playwright/ux-images-preview/backend-verification-index.md) 按领域列出已有定向测试、race、红测与补跑，集合相互可能重叠，不能相加为全仓 CI。
- 最后预览前端 1 文件/4 项通过；其余图片、恢复、Inspector、模型设置定向集合见实施报告及证据目录。最后 TypeScript/生产构建通过，协议注册表和 diff 检查退出 0。Monaco 等现有大 chunk 的构建提示仍保留。
- 固定 Provider 证明原图字节经过真实产品请求链，**未测真实模型理解截图并自主改好界面的能力**。
- Chrome 真浏览器已验；Windows 原生窗口、高 DPI、系统物理剪贴板以及 Edge 实机旅程仍未验。用户此前允许先完成后台验收。
- 原 S/Q/restart 实例和用户窗口未操作。无提交、推送、合并或发布。

## 收尾与桌面包

显式关闭后旧图/动作 409；另一个会话撤权后旧图/动作同样 409，异步清理实际结束原因为 `permission_revoked`，CDP、进程树、临时资料均清理。历史到期附近的 `process_exited` 收据保留，不冒称明确 timer-expired 分支。证据 `browser/preview-api-final-boundary-summary.json`。

最后缓存回归后再次关闭、收回浏览器权限并暂停任务。最新 Job `command-job-0f975d4a768e4c5f58d690e4` 已回收，动态状态 `killed / exit125`；PowerShell 158876、Node 156836 和 18886 监听均不存在。Run paused，活动 lease、pending/prepared 输入均 0。19 条历史工具调用完成不等于其启动的 Job 仍在运行，两种状态分开记录。证据 `browser/preview-api-api13-final-summary.json`。

最终 Windows 本地预览包：[TraverseBoard-UX-Images-Preview.exe]（本地保留证据，未公开：`output/playwright/ux-images-preview/delivery-final/TraverseBoard-UX-Images-Preview.exe`）。104,329,216 字节，SHA256 `2a08c34fc8de64a19aae0eac6c35ad247af0028b586e7e69c46e066073150ffa`。

`delivery-final/native-build-result.json` 构建退出 0；`native-artifact-validation.json` 19 项静态检查通过，1,689 个输入构建前后不变、116 个前端资源完整嵌入，最终入口 `index-C7c50Emn.js`。Windows GUI 子系统、manifest/DPI 声明、图标静态检查通过，**包未启动，不代表 Windows 原生窗口或高 DPI 已验收**。旧 `final/` 和 `delivery/` 是保留的中间候选，不作为本次交付。

API 152572/18884 与固定 Provider 146812/18885 空闲保留，重用前先核 PID/路径；测试项目和原草稿保留。无提交、推送、合并或发布。
