# 活动与执行记录状态：呈现收敛验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-12，Asia/Hong_Kong。实施树：`<workspace>`。用户在区分 Thread 续聊与 Run 生命周期后，明确要求“按你说的，更改呈现”。本批是呈现修正，不是重新实施任务恢复或后端连续性。

## 修改及行为边界

- 主对话移除失败恢复后的重复“可继续”徽标，继续显示真实当前活动。读取中显示“正在同步状态”；不提供活动读取显示“活动状态未提供”；查询失败显示“状态读取失败”；身份不符或未知状态显示“活动状态未知”。不凭单项查询失败推断整个应用离线或对话关闭。
- Run.running 显示“未结束”，并标明“执行记录状态”；Session.active 显示“未关闭”，并标明“上下文记录状态”。说明移到状态提示，不决定 Thread 是否可续聊，也不代表 Agent 当前活动。旧 Run、Session、侧栏、审阅、记录列表、代码交接及既有共享消费者同步。
- 高级页继续显示准确来源任务的 Agent 活动，并说明下方所选记录可能属于更早执行。历史开始事件继续显示“记录时执行中”。工具、Job、审批/失败历史保留原语义。
- 最终复核发现原附件限制提示会在活动查询失败后依据缓存 running 断言“当前正在执行”。已仅修正提示：可靠当前活动、提交中、停止未完成、尚未确认分别描述。原 `working || runActive` 限制、发送条件、未知请求身份、后端续聊、权限及执行控制不变。失败提示说明此次读取不会关闭对话；没有增加恢复入口。

没有创建新状态服务、持久化框架或执行协议；不扩入 hooks、Plan、全文搜索或所有历史诊断面板改造。694 项开工既有改动保留，不提交/推送/发布本项目。

## 定向检查

证据目录：`output/playwright/ux-status-presentation`。

- `legacy/component-tests-verified.log`：7 文件34项通过。
- `review/tests.log`：2 文件12项通过。
- `root-tests-verified.log`：3 文件34项通过；其中 conversation 末轮修正后由 `conversation-final-tests.log` 的16项替代先前15项。因此本批最终覆盖12文件81项，不把重复运行累加，不称全仓 CI。
- 原控制、失败可续聊、未知请求、切任务、来源活动绑定与通用工具状态断言保留。新增1项验证缓存 running → 查询失败时附件提示不冒称当前执行、文字按钮仍启用、重试读到 idle 后引用限制恢复。
- `web-build-final.log`：TypeScript及生产构建退出0；最终 JS `index-iG4Nuk6W.js`，CSS `index-DQolp02T.css`。保留既有大 chunk 构建警告，不在本批另行拆包。
- 范围 diff-check 无空白错误。初轮旧文案断言未更新造成的失败保留在原日志，修正断言后重新通过，没有为断言恢复冗长正文。

## 实际浏览器

复用上批专用隔离 home / 两个仓库，新建本批独立 browser profile。仅启动本批自有18896 API，未启动 provider，未重复 setup；API通过当前 web/dist 提供最终资产。`api/ready-final-copy-assets.json` 核 HTTP 首页、JS/CSS 与当前文件逐字节一致。

- `browser-main.log`：真实 idle 显示“等待新消息”。用浏览器响应注入模拟一次 running，再模拟503，明确这是状态呈现夹具，不是真实 Agent 执行。头部及文件引用提示不再使用缓存肯定当前工作；未发送草稿内容不丢、文字发送按钮仍启用；移除注入后真实 GET idle，引用入口恢复。草稿随后清空，没有发送。
- `browser-inspector.log`：来源 idle 与 Run“执行记录状态：未结束”、Session“上下文记录状态：未关闭”可同时区分；另一次503只使来源活动显示读取失败，不改历史记录状态；重试恢复。旧模型开始记录显示“记录时执行中”，详情解释其时间归属。
- `browser-review-lists-final.log`：检查与交付、代码交接的记录状态和运行/会话列表标签一致；列表说明打开记录不会启动执行。初轮脚本误在“执行记录”页签找“检查与交付”字段，超时日志保留；改正导航后通过，未改产品来迁就脚本。首次登录/后续一次旧 ref 已失效的 CLI 错误也仅为驱动定位问题。
- 主对话正常/失败、Run、Session、历史详情分别覆盖浅/深色1280×900、390×844；审阅与两类列表覆盖浅色1280×900、深色390×844。26个布局快照无 document 横向溢出或 U+FFFD。目视主对话、失败提示、Run、Session、历史详情、审阅与列表代表截图，状态及按钮无新增遮挡或重叠；没有修改 CSS 或字体。不是所有旧高级面板的整体设计验收。
- 三段浏览器请求观察均无 POST/其他写请求。console仅两条与注入路径对应的503。浏览器 `uxstatuspresentation` 已关闭。

## 本地包与收尾

最终 [TraverseBoard-UX-Status-Presentation.exe]（本地保留证据，未公开：`output/playwright/ux-status-presentation/delivery-final/TraverseBoard-UX-Status-Presentation.exe`），104962560B，SHA256 `0c3bae2872d1ae7a49447b32d370855f08aa02251b523b59270f8ff1ea401f6d`。版本 `v0.1.0-ux-status-presentation`，07:37本地构建退出0；20项静态通过，1736源码输入构建前后不变、116前端资产精确内嵌。root另行运行验证并保存 `delivery-final/root-independent-artifact-validation.json`，结果及SHA一致。EXE没有启动，这是未提交本地预览，不是签名发布。

初版 `delivery-verified` 已用 `SUPERSEDED.md` 标明缺少末轮附件提示修正；其包与日志保留，不能作为本批最终试用包。

`api/final-after-browser-audit.json` / `api/final-audit.md`：16表完整行hash与行数、两个仓库HEAD/分支/索引/porcelain、8文件SHA均与上批最终现场一致；FK0、live0，两个Thread真实执行状态idle。本批业务增量0，历史模型与Git请求没有重发。按精确exe/commandline/18896归属关闭唯一API31992，`api/shutdown-final-result.json` 确认PID消失、18896/18897无监听；provider始终未启动，旧服务、用户窗口、home/profile与旧报告保留。原工作树仅同步本对话必读。

Windows 原生 WebView2、物理高DPI、真实模型能力和真实账号 GitHub 外部旅程未由本批验证。浏览器检查与静态桌面包核验不能替代上述范围。
