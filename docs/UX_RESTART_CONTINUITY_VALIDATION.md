# 刷新、退出和重启后的续接

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-11，Asia/Hong_Kong。实施树：`<workspace>`，分支 `codex/ux-journey-convergence`。本次承接用户截图选择的第2项；前一批上下文摘要接入的结论保持。

## 结果与使用方式

新版本在输入时保存本机草稿和文件引用，刷新或重新打开后回到原对话。发送前保存原请求标识、原内容和引用快照；上次没有收到完整响应时，重开先用只读接口查询原请求。服务端已接收的消息不会再次发送，后来编辑的草稿和新引用保留。暂时查不到记录或查询失败时保留原请求，用户可继续核对，或明确选择提交原消息；页面打开不会自动补发未知请求。

第一条消息包含“创建对话”和“提交消息”两个步骤。创建响应丢失后找回已经创建的原对话，先保存首条消息身份，再完成本地交接；不会再建一份任务。已明确拒绝的提交不与未知状态混淆，用户修正后使用新标识。确认旧请求时按原始编辑文本和引用身份清理，不能仅凭 trim 后相同就清掉后来增加的空白或换行。

## 实现范围

- 复用现有 localStorage、QueryClient 和 Thread 幂等链路，没有另建后台同步服务、任务恢复系统或数据库表。
- 恢复数据按浏览器来源、API 地址、数据存储身份隔离，内部再按项目、对话分开保存。数据存储身份使用现有设置表保存随机种子，并绑定数据库物理位置；同库重启稳定，复制到其他路径或替换数据库隔离。Health 只返回已经初始化的非授权标识。
- 新增两个只读 GET：`/threads/creation-request` 与 `/threads/{threadID}/turn-request`，使用原 `Idempotency-Key`。查询不预留、入队、补执行或封存写结果。SQLite 使用 `BEGIN DEFERRED` 读取一致快照，避免现有驱动的 immediate 事务设置把只读查询变成写锁请求。
- Windows WebView 缓存目录固定为应用 Home 下的 `webview2`，避免改预览 exe 文件名就换一份缓存，不同 Home 使用不同目录。没有移动、删除用户旧的 WebView 缓存。
- 新增恢复记录不保存登录令牌、审批结果或可复用执行授权。尚未确认的创建请求保留其原始网络模式等请求字段，仅供同一原请求核对；它们不是重开后的默认权限。
- 无数据存储身份的旧连接明确提示只保留当前窗口。旧连接保留用户主动触发的原 key 重试行为；现代连接查询失败不会降级为自动 POST。
- 格式损坏、未知版本、存储读写失败均保留原记录并显示警告。业务内容类型错误通过同一存储底座登记拒绝标记，阻止初始空值和编辑覆盖该记录，发送前检查阻止遗漏未知状态；其他键仍可保存。没有实现损坏记录的可视化导出/清理管理器。

## 真实浏览器与 API 旅程

独立环境位于 `output/playwright/ux-restart-recovery/browser`，API `127.0.0.1:18880`，固定响应 Provider `127.0.0.1:18881`。通过正常 API 注册和 qualification 本机 Provider，导入只有 README.md、user-note.txt 的新项目。使用真实生产前端、Chrome 独立持久 profile、实际 SQLite 和产品 `workspace_read`；外层助手没有代替产品完成读取。未使用真实模型能力测评。

1. 含前后双空格、换行的中文未发送草稿及 README 引用，刷新并重新连接后原样恢复；完整关闭浏览器进程并使用同 profile 重开也原样恢复，恢复监听无 POST。
2. 原对话发送后，浏览器路由实际转发一次请求，服务端返回202且完成真实 `workspace_read`，测试故意丢弃响应。当前发送流程原有的一次传输重试也被丢弃，没有再次转发。随后在 UI 输入新草稿、增加 user-note 引用，再完整关闭、重开。
3. 重开用原 key GET 核对为 completed，无新增 POST；只有一个原用户气泡。旧 README 引用清理，新稿和新增引用的原身份保留，本地未知请求记录清理。
4. 新对话创建实际返回202后丢失响应；第一条消息未发送。完整关闭、重开后 GET 找到同一个 Thread，点击打开仍仅 GET 核对首条消息。服务端未收到首条消息时保留原始正文与 key；明确点击“继续提交原消息”后仅一次 POST，原始 key/正文保持，产品实际读取文件，原始草稿清空。
5. 两个任务执行结束、lease released、checkpoint idle 后，只重启本批精确 PID/路径核对过的 API。再次重开浏览器后仍保留原对话的新稿、引用。数据存储身份不变；371张表、323行记录的完整行哈希前后相同，外键检查正常，两个工程文件哈希不变。

Provider 最终计数为 qualification 2次、workspace_read 请求2次、观察到实际读取结果2次；对应两个 Thread 各一条 committed 用户消息、各一次真实读取，没有额外执行。两个实际 tool call ID 和结果 SHA 保存在 Provider 日志、API 收据与数据库核对文件。测试脚本只记录公开验收数据与专用令牌，不使用用户模型密钥。

证据：`browser/draft-before.log`、`draft-reload-2.log`、`draft-reopened.log`、`lost-turn-send.log`、`lost-turn-before-close.log`、`lost-turn-reconciled.log/.png`、`lost-creation-send.log`、`lost-creation-reopened.log`、`lost-creation-explicit-continue.log`、`after-api-restart-verified.log`、`before-restart.json`、`after-restart.json`、`provider-events.jsonl`。CLI完整退出/重开记录在本对话工具记录中，新浏览器 PID 随每次重开变化。

## 回归与证据边界

前端最终集合11文件126项通过，零失败/跳过，覆盖草稿/引用、多作用域、创建两阶段、只读核对、原 key 重试、终局拒绝、新稿空白差异、存储失败和旧入口兼容。较前一轮121项增加3项业务损坏底座测试和2项真实 Workbench 挂载/编辑/发送回归，未重复累计。证据 `frontend-final-2.json/.log`；最终整站 TypeScript 检查、生产构建和本批改动 diff 检查退出0。

后端观察器7个唯一 Go 顶层 race 用例通过，包含 `query_only` 只读约束和仅只读令牌可查询；客户端观察器13项已包含在上述前端集合。数据存储身份相关6项 race、Windows选项3项通过。OpenAPI与生成TypeScript已同步并由独立复核确认两条GET、参数和返回字段一致。详见 `output/playwright/ux-restart-recovery/backend-observer-summary.md`、`output/playwright/ux-draft-recovery/backend/validation.md`、`output/playwright/ux-restart-continuity/backend/validation.md`。

保留中间失败证据：旧兼容测试因新增提示重复占用 alert 而失败，提示已改为 status；空白回归初稿在页面加载完成前同步找输入框，已改为等待可见输入；浏览器第一次刷新后先等对话输入框，但认证令牌原本只在内存，未重新登录，故超时，后续正常重新连接验证；数据库验收脚本首次默认 GBK 读取 UTF-8 报错，已显式 UTF-8 后完成。上述失败不改写为成功，不影响已记录的实际收据。

这不是全仓 CI、原生高 DPI 或任意真实模型能力验收。真实浏览器覆盖完成、未收到和创建响应丢失；仍处理中/等待审批/失败/拒绝的细分状态主要由后端与组件回归覆盖，不能称所有状态都逐一进行了真实 GUI 演练。原生 exe 的缓存目录和资源通过静态检查，尚未实际启动新 exe 做 Windows 重开测试。

本机缓存不跨浏览器 profile/来源/端口同步，清除站点数据仍会失去本地草稿。旧版仅存在内存的草稿无法追溯恢复；旧的按 exe 命名的 WebView 缓存未迁移，首次切换固定 Home 缓存时旧外观偏好可能需要重设。同一任务多窗口同时编辑的冲突解决、损坏记录修复 UI 不在本批完成范围。正文会存于此设备站点缓存，不是云端草稿服务。

## 最终收尾

最终本地试用包：[TraverseBoard-UX-Restart-Continuity.exe]（本地保留证据，未公开：`output/playwright/ux-restart-recovery/final/TraverseBoard-UX-Restart-Continuity.exe`），103966208字节，SHA256 `2828376c8b710a1760cef48642d82c1f4859c4adf7243de81a2ef627643ba361`。构建退出0、19项静态检查通过；构建期间1667个源码/资源输入不变，116个前端资产逐字节嵌入。GUI子系统、DPI manifest和15个图标为静态核验，exe未启动。

最终前端入口 `index-BvSMOUCX.js`，SHA256 `8c5e6b2331c970a8b7a9a8c623c73725b5b3afca668438b633c18dab52c4e97b`。完整丢响应旅程使用前一候选；最后仅增加损坏记录保护后重新构建，浏览器实际加载最终入口并再验草稿/引用、纯GET恢复、无横向溢出及本页无替换字符。最后一次只读数据库核对仍371表323行完全不变，Provider计数不变。不能把前一候选全部旅程说成最终二进制重新逐项演练。

最终构建后只增加了 `recovery-flow.test.tsx` 中两项回归；构建快照中生产源码与资源保持，见 `final/post-build-source-check.json`。原快照的泛用“Tests omitted”说明仅准确对应Go测试排除，前端测试实际包含在快照中，补充记录纠正其范围。先前根目录包仅作候选保留；最终交付为上方 `final` 子目录包。

本批最终隔离API PID149700/18880、Provider PID157268/18881，均无活跃任务；浏览器 `uxrestart` 留在第一条验收对话，保留新草稿和user-note引用。后续使用前重新核PID、路径和状态。启动/重启记录在 `browser/processes-*.json`；旧服务与用户原生窗口未操作。

工作必读已同步为1.70。所有修改仍未提交、推送、合并或发布；原工作树仅同步工作必读。没有操作用户原生窗口、旧 S/Q 服务、用户原数据库或其他工程。其余图片输入、预览、Git/PR等建议不因本批完成而自动实施。
