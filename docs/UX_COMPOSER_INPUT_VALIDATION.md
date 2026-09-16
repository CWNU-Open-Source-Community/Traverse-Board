# 输入、图片与原始附件验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-12。实施目录：`<workspace>`。这是本地未提交候选的有限验收记录，不是全项目完成或发布声明。

## 用户目标与实现

- 输入框恢复系统编辑上下文菜单；桌面文件粘贴复用 Win32 CF_HDROP，仅在用户明确粘贴时读取。选择、拖入与粘贴使用同一附件链路。
- 图片使用 112×112 方形缩略图，附件区使输入框增高；原始像素不因缩略图裁切而改变。查看器保持原比例，支持适应窗口、100%、缩放、下载、复制与 Escape 回焦。
- 普通文件显示文件名、大小、下载与移除。遵循用户纠正，不把 ZIP/PDF/Office 的“已解析／未解析”做成普通交互步骤，也不新增万能解析器。
- 原文件保存在已有不可变 blob 存储中，按实际发送消息绑定到 Thread；模型可通过已有授权命令工具按需读取或解压。文本有有界 UTF-8 摘录，二进制不预解析。
- 草稿、附件与未知请求身份复用已有持久化和精确版本核对；刷新只核对原请求，不自动重新上传或发送。后来修改的完整草稿受到保护。

当前边界：普通文件每条最多 4 个、单个不超过 5 MiB，允许空文件；图片独立最多 4 张，支持 PNG/JPEG/WebP。不是无限文件数、目录上传或任意文档格式解析支持。

## 原文件进入工具的方式

原 SQLite blob → 当前准备输入及同 Thread 已提交历史的精确集合 → 独立应用缓存 → 既有 `command_runtime`。通过固定环境变量 `TRAVERSE_ATTACHMENTS_DIR` 定位原件；文件身份、字节、manifest 与原 Job 指纹绑定，调用前重核。

Local 后端复用现有只读输入与 AppContainer 边界，附件目录不进入 PATH；FullAccess 保持原授权，不宣称宿主机只读隔离。不向用户仓库复制文件，不因附件自动扩大权限。未提供支持的命令工具或 Docker 不显示假的可用路径。历史集合取近期最多 64 个、当前输入优先，遗漏数量明确；达到历史上限不会阻断后续纯文字对话。

缓存按 Run 和 manifest 复用，不同集合仍会累积；尚未实现并发安全的自动回收。不能把一次 Job 列表观察当作安全删除依据，也不能声称整个应用数据目录空间已经有界。

## 实际浏览器验证

独立 API 18900、协议 Provider 18901、`uxcomposerinput` 浏览器目录；项目、附件与令牌均为本批夹具。固定 Provider 检验协议及工具链，不代表任意外部模型的理解质量。

|链路|实际结果与证据|
|---|---|
|单独文件创建对话|117 B 中文 TXT 无正文可发送，历史保留附件、请求收到原文本。`browser-input-send.log`。|
|混合输入|原图、JSON、合成 DOM paste 文件与 drop 文件可一起加入；正文保留，移除后输入框收缩。`browser-mixed-draft.log`、`browser-scope-layout.log`。|
|图片|方形缩略图、原比例、100%、下载原件、Escape 回焦通过；下载 SHA 与 10031 B 原 PNG 一致。`browser-viewer-rest.log`。|
|复制图片|最终使用验证过的 Blob，浏览器拦截到相同字节与 SHA，控制台无复制相关 CSP 错误。`browser-final-copy.log`。没有写用户系统剪贴板。|
|上传回复丢失|真实服务端保存后截断回复；刷新、重连 POST 为 0，以原 key GET 找回 0 B 文件，其他草稿与附件保留。`browser-upload-recovery-result.log`。|
|发送回复丢失|实际历史为 1 图 3 文件；等待期间新增文字与文件，重开保留整份新草稿，无自动 POST。`browser-turn-recovery-observed-result.log`。|
|草稿隔离|另一项目的新草稿不带入原附件；回到原 Thread 恢复原内容。`browser-scope-layout.log`。|
|布局|实际设置浅色／深色，1280／390 px 的输入框与查看器无横向溢出和替代字符。深色证据使用 `*-dark-verified-*`；仅 emulateMedia 的早期同名图仍是浅色。|
|权限确认|确认、取消、Enter 确认均不发送草稿，正文与 ZIP 保留，历史数量不变；原生发送按钮仍单独起效。`browser-permission-no-submit-result.log`。|
|正常发送结清|第7条普通附件输入接收后正文与附件清空；刷新重连 POST0，仍空。`browser-normal-settlement-final-2.log` 的这些子步骤通过，末尾旧浏览器下载退出使整条脚本 exit1，未称整条通过。|
|原 ZIP 下载|全新直接 Chrome（显式 sandbox、相同 CSP）及全新同 CLI 会话都下载143 B，SHA匹配且页面仍存活。`download-diagnostic/validation.md`；原 profile 退出原因未定位。|
|最终长命令布局|最后资产在浅/深1280/390展开长命令时，内部列表 clientWidth=scrollWidth（768/366），无 Object/FFFD；Host准确显示“宿主网络未隔离”，无业务 POST。`browser-label-final-result.log`。|

详细过程和截图位于 `output/playwright/ux-composer-input/browser-validation.md`。

## 真实发现与修复

1. 查看器原 `fetch(blob:)` 被当前 CSP 拒绝，导致复制失败。改为直接使用已验证的原 Blob；没有放宽 CSP 或重复下载。
2. 权限弹窗的 portal 表单提交沿 React 组件树冒泡，导致点击“启用完全访问”意外发送外层草稿。子表单阻止冒泡，Composer 只处理自己表单的提交；文件搜索弹窗同类路径也有回归。意外的第 3 条夹具输入保留在账本中，未删除或伪称主动发送。
3. 普通发送清空草稿时，旧的 partial update 漏掉新增普通附件字段，出现正文清空而 ZIP 卡片残留。现改为完整替换精确已发送版本，相关 3 文件 25 项通过；精确版本保护新草稿的规则保持。
4. 新命令运行器的 Host PowerShell 默认输出中文曾损坏，已复用既有 UTF-8 初始化。实际中文、emoji、stdout/stderr、非零退出、early return 与旧 Job 不重启有回归。旧历史乱码没有被改写。
5. 活动摘要把 React 状态标签放进字符串 `join`，导致 `[object Object]`。现保留标签直接渲染，等待阶段省略默认的 0ms；真实失败、退出码和终局耗时保留。真实 DTO 到投影再到组件的回归先复现失败，修后相关 45 项与类型检查通过。
6. 真实长命令撑开内部 grid 到3369px，但文档整体宽度仍1280px，原先只测外层宽度漏检。现约束 grid 与子项最小宽度；摘要省略、完整命令换行、标准输出可滚动。实际宽窄收起/展开及最终浅深四组复验通过。
7. FullAccess 的 Host 命令曾错误显示“无网络”。现按后端明确的执行环境区分：Host“宿主网络未隔离”、Workspace Sandbox禁网“无网络”、历史未知边界“网络隔离未确认”。34项回归与类型检查通过，未修改权限或执行行为。

测试脚本自身的失败也保留：初次选择器拼错导致超时；混合消息恢复脚本一度错误要求删除用户已修改的新版本内的旧附件；ZIP 夹具挑选“最后 user 段”误把历史图片描述当当前输入。它们不能作为产品通过记录，也没有据此删账本或放宽权限。

配置诊断更正：隔离夹具最初是 preview/noop，FullAccess 本身不会凭空提供命令工具。暂停后通过正常 API 选择 Local、再确认 controlled/trusted，得到合法配置回执，配置本身未授予进程或执行权限。早期把灰按钮推断为“先信任／先选 Local”的循环不成立：按钮的 selectable 只看 Run 可配置状态和控制能力，未信任影响的是 runtime_available。当时 Agent idle，但 Run 尚未暂停。没有据此修改产品或放宽守卫。

第 5 条 `FINAL_INPUT` 在浏览器测试工具异常关闭前已经送达，命令调用已准备但随后 cancelled，Job 为 0。根因是 root 的 run-code 异步请求监听误用该作用域不存在的 `URL`，属于验收脚本错误；第6条出现同类退出，但服务器已完成，没有重发。恢复同一浏览器目录并重连只产生 GET，没有重新发送。进程硬退出后该目录保留的是较早草稿，最近写入不在恢复快照中；不能把正常刷新测试扩大为任意硬崩溃持久性保证。独立的正常 close→同 profile reopen 探针保留了正文与原附件，POST0。原消息与命令 checkpoint 保存在服务端，后续通过既有执行队列继续原调用。

**原 ZIP 实际执行通过：** 通过既有 Run `/execute` 对原第 5 条消息继续一次，未新增 Thread 消息；原 attempt/call 保持。唯一 Job `command-job-9e5f61f022d34955718bafda` 完成、退出 0、进程树已收回，stderr 为 0。stdout 为实际 `Get-FileHash` 和解压后 `Get-Content` 的 JSON：143 B ZIP 的 SHA256 为 `b4a8e2e7e3b038fda7165fdb53bf93d147c4a01e33af808a4dd7dd80abb1081a`，note.txt 为 `COMPOSER_ZIP_BYTES_NOT_PARSED`。当时5条输入；最终7条已提交输入／8次业务模型请求／1工具／1 Job，另2次资格探测。后两条仅普通附件接收，未重复命令。7个文件blob、2图、所有精确消息绑定与4个项目种子文件均独立核对；最终9项检查通过，活跃计数0、外键错误0。

该次使用已明确选择的 FullAccess／Host adapter，实际运行 PowerShell 7；Local AppContainer 只读执行另有独立真实测试，不能把两者混成同一次浏览器沙箱验收。固定 Provider 错读了 `artifacts.stdout`，实际工具结果在 `pages[].frames[].text`，因此它的回复误报失败；原回复保留。真实 Job、stdout 和送入模型的结果可独立核对，修夹具后不为取得好看的回复重复执行 ZIP。

## 定向测试与边界

证据目录统一为 `output/playwright/ux-composer-input`，测试集合有交叉，不相加成全仓通过数量。

- 前端联合 8 文件 66 项、后续权限相关 4 文件 29 项、草稿结清 3 文件 25 项和活动显示 2 文件 45 项通过；对应 `integration-tests-final.log`、`permission-submit/related-final-1.log`、`attachment-settlement/related-final-1.log`、`activity-status/related-final-1.log`。集合有交叉，不累计。最后活动网络标签34项和类型检查通过；最终 `web-build-final-6.log` exit0，JS `index-COlXfFM8.js`、CSS `index-B3VeG2sz.css`。仍有单块超过500kB的构建提示，不伪称零警告。
- 原附件应用／存储 9 个顶层 race 回归覆盖精确集合、已提交绑定、跨任务排除、历史上限、缓存字节与实际 ZIP 命令链；`files/raw-input-race-1.log`。
- Local 实际 PowerShell 7 在只读输入中读取 ZIP、解压到私有 TEMP 并读中文；写、删原件或移除绑定后读同路径被拒，Drydock 不变。`sandbox-inputs/validation.md`。
- 默认 Host 附件 4 项 race、UTF-8 6 项顶层 race／7 个真实 PowerShell 场景通过；`sandbox-inputs/attachment-default-utf8-final.log`、`sandbox-inputs/command-runtime-utf8-validation.md`。
- 旧系统 PowerShell 5 的广泛 smoke 在本机出现初始化失败，退出 4294901760；对应失败日志保留，不声称所有本机 shell 或全仓 CI 通过。
- 文件 HTTP 契约、OpenAPI 生成与协议登记已验证。

原生 WebView 右键菜单外观、Explorer 实际文件复制粘贴、Windows 高 DPI、IME 和物理系统剪贴板仍未实测。DOM 事件与浏览器复制拦截不等于原生系统验收。真实外部模型自主处理任意压缩包、PDF 或 Office 不在本次固定协议验收结论内。

## 最终本地试用包

[TraverseBoard-UX-Composer-Input-V2.exe]（本地保留证据，未公开：`output/playwright/ux-composer-input/delivery-v2/TraverseBoard-UX-Composer-Input-V2.exe`），105361920 B，SHA256 `726fd33e9a8103a72c2d38cd0394eca0e9a017b15d5111f78bdf7d4bf5a06dd9`，root已独立核对。20项静态检查通过，1784个源码输入在构建期间不变、116个最终前端资源精确嵌入；amd64 GUI、PerMonitorV2/asInvoker、15图标、无DevTools构建标签。EXE没有启动，因此这些不等于原生窗口验收。较早 `delivery-final` 候选缺最后排版/标签修正，保留证据，以 V2 为准。

正常发送清空、实际原 ZIP 工具链、独立账本核对、最终浏览器浅深宽窄检查均已完成。原会话下载退出在干净 CLI 与直接 Chrome 未复现，原因仍未确定；对应失败记录保留。硬终止恢复最近草稿的保证、原生物理剪贴板与安全缓存自动回收仍是明确限制，不以本批完成掩盖。

所有缓存和临时目录仍使用 D 盘；C 盘此前仅通过官方 Go 缓存清理释放约58.3 GiB，没有删除源码、数据库或草稿。最后观察 C 可用63142596608 B（58.81 GiB），D可用128860278784 B。

本任务不提交、推送或发布已有大量未提交改动。root浏览器已正常关闭（`browser-close-final.log`）；自有 API/Provider 的最终逐表守恒与精确停服结果见 `acceptance-host/final-business-audit.md` 和 `cleanup-final-result.json`，保留数据与证据。
