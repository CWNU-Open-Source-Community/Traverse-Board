# 第十一批：人工验收入口、只读观察与 Shell 初始化诊断

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-09。本批限定的人工验收入口、只读反馈和初始化错误说明已实现并验证；生产Shell启动成功仍未完成。用户明确要求继续剩余修复。产品工作树为 `<workspace>`，分支 `codex/ux-journey-convergence`，基于 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。A–K全部是本地未提交改动，未推送或发布；原工作目录仅同步工作必读。

## 本批范围

1. 复用既有 Plan/Delivery 工作项、检查点、交接 Note 和权限边界，补上 V2 的开始、记录人工说明、明确完成入口。
2. 按实际接口可用性读取执行状态，区分只读观察能力与控制权限，解决J只读实例反复请求不存在接口的问题。
3. 继续同LPAC边界诊断PowerShell启动；新增准确提示，保留原失败记录。成功 Shell/完整编码交付仍未验收。

## 实现与语义

- 新的三个HTTP操作沿用 `plan_delivery_control.v1` 和原控制令牌：`/runs/{run_id}/plan/work-items/{work_item_id}/{start|checkpoint|complete}`。开始/完成按精确版本更新，暂停、Deliver、无活跃租约、选中且已接入验收的项、真实依赖与现有完成门仍执行。模型、工具和额外权限均不由这些操作启动。
- 开始/完成复用同事务 `WorkItemChangedEvent` 存成功收据，检查点复用原operation/checkpoint/Note台账；同Run跨三种动作的键不能交叉复用，无新表/迁移。实际应用版本与当前工作项版本分别返回，已成功原请求在后续状态变化后仍可重放。
- 六类文本沿用已有人工验收契约：每项需要针对性验证、Diff审阅、安全审阅及交接；最后一项另有功能和异常恢复说明。这是人工陈述，后端不自动关联或验证所写Job/报告，不可称测试通过。界面计数明确为“当前有效人工记录”；保存与完成是两个动作，不自动完成任务，也不改变 Standard Code 的 verified/失败结果。
- 草稿与冻结请求使用已有Query缓存按Run/工作项区分，关闭面板后可恢复。网络/响应未知保留原键、正文和版本；确认时即使遇鉴权或状态拒绝也保留未知身份。首次确定拒绝可返回编辑。跨任务晚返回只更新原任务。当前版本已存记录不再开放重复填写，版本变化才重新接受新记录；旧版豁免计划保持只读。
- 后端新增可选 `thread_execution_read_enabled`，与现有GET执行接口的真实注册条件一致。Web/Desktop连接保留该只读事实；有读令牌可以观察开放接口，有控制令牌也不会凭空轮询缺失接口。旧服务不提供该字段时不轮询。真实404停止自动轮询，保留手动刷新；无能力时明确显示工作记录仍可查看，不猜idle/failed。
- 已知PowerShell启动输出与退出码的窄匹配增加“输出提示 PowerShell 初始化失败，当前检查未完成”说明。完整原输出、U+FFFD提示、退出码、摘要、状态仍保留；匹配结果不作为未发生副作用或授权依据，无自动重试/额外启动探针。

## Shell新证据

`output/playwright/ux-phase-k/powershell-lpac-root-cause.md`保存原始命令、官方来源及范围。真实同LPAC helper成功加载系统SMA，内层异常明确为 `PSEtwLog → PSEtwLogProvider → EventProvider.EtwRegister` 的Win32Exception/5拒绝访问。没有把无法读取的provider SDDL猜成具体ACL。

仅诊断overlay增加Windows内置 `lpacInstrumentation` 后，静态初始化成功；PS5完整启动仍在15秒超时。原相同LPAC中的PS7副本也被ETW拒绝，单cap overlay中的PS7才实际输出marker/exit0。严格负例记录源目录读写及工具目录写入被0x80070005拒绝、TCP/UDP为10013且本机监听未收到连接/包、真实子进程随父回收。所有probe结束、权限清理和源码/原runtime/历史摘要保持记录在 `diagnostic-cleanup.json`。

这解释了首个阻断并给出兼容候选，不能推出该cap仅有ETW权限或生产策略已安全兼容。生产仍原两SID、可信Shell选择不变；未改机器ACL、审计或执行策略，也未把Codex缓存PS7纳入产品。245MiB隔离工具副本保留在output用于复核，不是产品依赖。PS5仍未修好，PS7候选仍需受支持的安装/资格与策略范围决定。

## 当前验证与失败保留

证据统一在 `output/playwright/ux-phase-k/`。

- 只读轮询修前1.8秒内请求4次；修后5个相关前端文件189项、HTTP race4.448s通过，见 `execution-read-*`。
- 最初后端定向race application4.027s/store2.802s/HTTP2.551s，OpenAPI真实路由1.184s和Desktop构造0.489s通过。`plan-work-backend-validation.md`为索引，原日志保留。后续真实UI暴露多条验收标准的排序问题，不能以上述测试代替该问题修后验证。
- 前端全量103文件654项19.85s通过；后续相关4文件21项6.83s通过。Shell提示26项3.71s/typecheck通过；OpenAPI/TS生成、Go和前端build通过，protocol登记33家族1005标识107显式项同步。最终OpenAPI检查1.009s、diff check通过，末轮构建与实载身份见下方记录。
- I数据库只读backup到K，原I历史未改。只读API页面观察192212ms内没有执行接口请求，旧失败和记录可读，`readonly-ui-proof.log`、`02-readonly.png`。该截图时尚未装最后的Shell提示，最终页面另核验。
- 独立人工验收Run通过真实application/Supervisor脚本Plan工具/选择/Deliver创建，身份在 `manual-plan-identities.json`；只有准备计划使用固定响应，未模拟Shell成功。开始由真实UI POST202使待办pending v1→in_progress v2，关闭重开恢复中文草稿，见 `start-and-draft-ui.log`。1440×960/390×480表单和提交控件目视与边界检查见05截图。
- 首次真实checkpoint被412拒绝：“selected WorkItem no longer matches its immutable Delivery module”。准备器有三条验收标准，工作项保存排序而Plan保留原序；现已在application/store复用创建项时的NormalizeWorkItemDetails校验预期标准与依赖，并用于原请求handoff重建。保留内容、身份、原Note及fingerprint算法，不迁移或重写数据库。旧代码真实SQLite回归0.322s失败，新码0.329s通过；最终race application4.111s/store1.627s/HTTP2.474s通过，包含多依赖逆序、篡改/缺失标准、terminal replay。`checkpoint-criteria-order-*`和`plan-work-backend-validation.md`保存证据。

初次客户端回归误用跨origin URL，后改为实际相对URL；类型生成前新别名不存在及异步mutation返回类型编译错误均已修正。测试中的文本匹配/等待夹具错误单独记录，不能冒称产品漏洞。真实页面也暴露报告404可能代表整个接口未启用，不能据此断言无报告：现无缓存404明确说明两种可能，不解析错误文本或隐藏只读可读历史。新增两类404与只读failed历史回归，Panel/Task Review共13项3.85s通过；旧失败、修后输出见`report-unavailable-{before,after}.log`。最终关闭报告接口时，K副本仍有原I报告，真实UI显示准确歧义提示，见`report-unavailable-ui.log`和12截图。测试中有刻意断连、412、404和API重启期间的网络错误，不宣称零错误。

## 最终真实旅程与清理

- K新人工验收Run `run-20260908202057-73f7129260db`，工作项 `work-20260908202057-25b29998704c`。保留首次被拒请求的key `web-plan-item-18da2825-936b-40e7-8973-b1b2322114e4`，修后真实POST202保存后刻意中断浏览器响应；关闭重开再确认仍用同key/body，返回replayed=true和同检查点 `delivery-checkpoint-20260908203539-bc083dc168f0`，没有重复记录，项仍in_progress v2。见`replay-after-fix-ui.log`。
- 完整刷新页面并重新连接后，已存中文交接原文可读，当前版本不能重复记录；明确点击“完成此项”才POST202变为completed v3。Run仍paused，未生成Command Job/自动交付报告。见`read-and-complete-ui.log`、`manual-plan-identities.json`。这验证人工操作流程，不是Shell或模型能力评测。
- **仅K数据库副本中的**原I Run开始其待办并保存真实失败说明，项 `work-20260908162651-ea18e0d5473d`保留in_progress v2；检查点 `delivery-checkpoint-20260908204039-5fbed9398592`。没有点击完成。原失败报告实时读取仍failed，Job exit4294901760和收据 `424a84f02f4458dafb29bd6dceca5b6cf9c50d04cda98cfb0763c05134c8fc9a`保持。原I数据库没有被写入。见`record-failed-check-ui.log`、`report-stable-ui.log`、10截图。
- `verify_persistence.py`以mode=ro/query_only核对14项，全通过：唯一人工记录、原版本、中文Note证据、独立项明确完成、没有额外Job/Report、K中的原I失败Job/Report与I原库逐字段相等、I副本项未完成、两Run仍paused、source用户修改及笔记字节保留。见`verified-persistence.json` / `verify-persistence.log`。
- 视觉复核发现handoff正文落在历史网格首窄列，已修为跨整行；最终宽1019px、窄345px。旧08窄截图被打开的侧栏遮挡，不能算可视验收；最终11截图先关闭侧栏并验证elementFromPoint命中提示正文，提示宽289px，无遮挡。1440×960/390×480的表单、正文和提示均目视复核；05为表单，11为最终正文/提示。
- API缓存启动时前端资产，重建UI后必须重启API。最后浏览器实载 `index-CiPUz-SA.js`、CSS `index-hT11lUFT.css`；JS1868.72kB/gzip492.15kB，原有chunk警告保留。API `cyberagent-ux-phase-k-final.exe` SHA256 `BF442DD805C873B96E2D61C1A0B5F7CA5022BD54B2E7FB27F52B908B85AC353E`，其他摘要在`final-build-identities.json`。最后build包含typecheck；无完整Go重跑，前端全量在K中段执行后只对新变更做关联回归，不叠加覆盖数。
- 最终只读页面实载同一构建，8.6秒无execution请求且报告接口不可用文案正确；前一长观察192秒同样无请求。`final-visual-ui.log`、`report-unavailable-ui.log`保存实际加载身份。
- 04:49:38 +08 已关闭本批API68680/65960/59624/41540/75184/70076及两个专用Playwright会话，18867/18868无监听、K路径无剩余进程。无独立provider服务启动；Plan准备器使用进程内固定响应。Native探针清理见独立记录，诊断副本保留。`cleanup-final.json`为最终清理证据。

完整成功编码交付、原生平台矩阵、暂停/终态单文件恢复、全部审批类型、刷新/重启草稿持久化、性能与其他F01–F14边界仍以必读为准。本次只是接通既有人工门；其字段数量和小任务是否需要完整人工审阅仍值得后续按实际使用成本评估，不据接口完成就宣称现有流程没有过度工程化。
