# 第十批：PowerShell 输出乱码与失败说明

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-09。用户指出第九批截图的stderr乱码。本批已修复所证实的解码缺口和误导文案；PowerShell在LPAC中的PSEtwLog初始化失败本身仍未解决。

产品工作树为 `<workspace>`，分支 `codex/ux-journey-convergence`，基于 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。A–J均为本地未提交修改；原工作目录只同步工作必读，未提交、推送或发布。

## 已确认原因与实现

- 截图其他中文正常，乱码出现在进程stderr。独立Local LPAC诊断再次启动相同的系统PowerShell 5、相同argv，捕获194字节无BOM UTF-16LE。先通过原UTF-8清理器后，结果与I的Job乱码逐字相等；严格UTF-16LE解码并往返编码与原字节相等。完整中文是：`Windows PowerShell 被终止，返回以下错误: “System.Management.Automation.Tracing.PSEtwLog”的类型初始值设定项引发异常。`
- 原链路为进程字节→Local捕获→sandbox管道→CommandRuntimeManager→只支持UTF-8的RedactingStream。NUL被删、非法字节被替换为U+FFFD，Job和Artifact只保留清理后文本及其摘要；原始观测长度不能恢复丢失字节。这是后端日志解码缺口，不是字体或网页字符集问题。
- `command_runtime_output.go`在脱敏前处理明确的UTF-16 BOM；无BOM只对已解析的PowerShell profile、`powershell.exe`的stderr、完整UTF-16LE `Windows PowerShell `前缀启用特例。其余保持UTF-8，不按NUL比例猜测编码，不把所有Windows输出转成GBK/ANSI。
- 解码复用已有版本 `golang.org/x/text` 的流式reader；该模块由间接改为直接依赖，版本未变，无新增库版本、数据表或迁移。底层ObservedReader仍累计原始字节，之后沿用原ANSI/控制字符清理、秘密脱敏、大小限制、UTF-8页游标与摘要。半个code unit、代理对和EOF由成熟解码器处理。
- V2预览或完整输出含U+FFFD时显示“部分输出字符无法正确解码，当前文本可能不完整。”，保留原文、状态和退出码，无自动重试。没有篡改I的Job、Artifact或失败报告。
- checkpoint事务按已记录kind显示命令操作、文件操作、撤销、回退、重做或分支创建；未知kind使用中性“检查点操作”。本次command_batch失败不再误称“工作区恢复失败”；既有状态、事件身份与排序不变。

截图的重复“本机脚本化验收……”来自固定响应夹具，方便逐步验证真实工具行为；不是正式模型的正常回答或模型能力评测。解码修复也不表示检查脚本已执行或项目测试通过。

## 实际验证

证据目录 `output/playwright/ux-phase-j/`。

| 范围 | 结果 / 证据 |
| --- | --- |
| 原始LPAC单次复现 | `ps5-raw-3421531594/`、`raw-probe.log`。raw SHA256 `87c916160d65585305af07af5c0285c7669b5caf4c9b21823e4d48dd7152cefa`，194 bytes；UTF-8非法、UTF-16LE严格解码和往返成功；原清理结果与I相同 |
| 实际LPAC→生产Manager→存储 | `production-manager-lpac.log`，race 1.625s。overlay仅注入诊断测试，复用既有内存store；executor为真实LocalBackend，生产collect处理新的OS输出。`manager-lpac-4139336866/`记录真实raw、Job、页、摘要和清理 |
| 输出正确性与保留失败 | 中文无U+FFFD；原始194字节及exe摘要相同，decoded SHA256 `63963023ff9e5e9e34803403a92b14dc9a8ae421114375ffa2b7107436dc4a3b`；`failed/exit4294901760/tree_reaped=true`保留。新文本摘要自然不同于旧乱码摘要。不是SQLite/HTTP全命令旅程或脚本成功证明 |
| Manager/流处理定向race | runner 3.821s、outputsafe 1.137s；`output-decoding-race.log`。逐字节pipe拆分、中文/emoji/代理对、UTF-8不误解、UTF-16两种BOM、EOF、秘密跨块及控制符清理、raw计数/持久摘要/页游标、真实失败终态 |
| 失败叙述 | 修前定向失败0.289s，修后runactivity整包0.286s；`checkpoint-narrative-before.log` / `checkpoint-narrative-after.log`。8类×3阶段及身份不变 |
| V2提示 | activity-detail 22项4.24s及typecheck通过；`activity-output-notice-tests.log` / `activity-output-notice-typecheck.log` |
| 真实历史页面 | I数据库只读backup到J，API仅启用只读访问，不启动provider或修改任务。最终前端 `index-ChTse8ic.js`。历史乱码保留、提示出现、失败文案正确；1440×960/390×480截图目视核验，窄窗口提示边界x62.33/宽280；`history-output-ui.log`及02/03截图 |
| 构建 | Go API与前端build通过；主JS1851.23kB/gzip486.80kB，原有chunk警告保留。`go-build.log` / `frontend-build.log`、最终diff check通过 |

原始输出与严格分析详情见 `output-encoding-diagnosis.md`，不得把人工推测的中文补进历史记录。I `final-jobs.json`前后文件摘要未变。这里的标准库/成熟编码器测试与真实LPAC测试各有范围，不叠加为全项目覆盖率；本批未重复前端全量和完整Go套件。

失败记录保留：输出测试初次对既有脱敏文案期望漏写`token=`，纠正测试后通过；这是断言错误，未改变脱敏规则。启动UI服务时带ExecutionPolicy Bypass的PowerShell包装命令被自动审批审查拒绝（仅返回blocked by policy）；随后直接启动已构建API成功，没有更改执行策略。只读API未开启execution控制路由，页面执行状态轮询返回404并显示读取失败；本轮只核验历史输出，不声称控制台零错误或只读模式完整验收。其轮询反馈另列F14待办。

## 保留边界与后续

新输出的乱码与误标已修；旧乱码没有可靠原始字节可用于修复。真实Shell仍在启动阶段退出，未执行check.ps1。未改ACL、注册表、ETW/审计、可信Shell选择或LPAC权限，未验证PS7解决该问题。

下一步仍为相同隔离边界的Shell初始化诊断与明确可用性反馈，以及Plan人工DeliveryCheckpoint的V2入口；完整成功交付、原生/审批矩阵和其他F01–F14边界按工作必读继续。

J专用API57056、Playwright会话uxphasej（daemon72472）已关闭，18867/18868无监听，未启动provider。两次独立LPAC探测均正常Close/Shutdown；详见`cleanup-final.json`及manager verification。原I服务未重启。后续不要等待旧session，重新核对服务身份再恢复夹具。
