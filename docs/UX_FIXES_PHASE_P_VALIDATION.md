# P：小任务 Plan 简化

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

目标是让明确的小任务只提出有实际意义的方案，并可不填写人工验收表直接完成已选事项。需要人工验收的任务由操作者在采用方案时明确选择。方案数量、采用、完成、失败确认和跨执行承接须一致，不能只隐藏字段。

限定实际旅程、来源详情窄屏补修和最终持久化审计已通过。代码基线为15c399c8940aeb81d3c45bb0de96759b480d02c2，实施位于独立UX-Fixes工作树；所有本轮修改仍未提交。

## 合同与实现

- `plan_delivery.v1` 允许1–3个方向，旧三方向序列化及指纹不变。新v156放宽提案表约束；历史迁移不改。
- 不可变Selection保存操作者选择的 `manual_acceptance=required|on_demand`。旧记录/旧请求省略字段仍required；新Web显式默认on_demand，CLI可明确选择。模型提案不能自行设置策略。
- 采用并交付使用原select与enter两API及独立原key。第一步成功后只核对剩余步骤；不自动运行模型或工具。
- 按需模式免必填人工检查点，仍要求事项状态、原模块要求/依赖、Deliver及实际权限。真实检查、当前版本报告与完成门禁不变。
- M承接required原CP或on_demand原完成事件；校验同Thread相邻执行及精确模块，不复制人工记录、Job或自动通过。

## 已发现并补修的缺口

独立回归 `on-demand-projection-before.log` 复现4个失败：正常Update修改criteria/删除依赖后仍能完成；直接SQL同样能绕过。共享原CP模块投影检查和SQL待写NEW值检查已补，`on-demand-projection-after.log` 四例转绿；正常按需完成及旧CP约束同时通过（1.883s）。未把人工表单当作正确性校验的唯一入口。

## 中间实际现场与暂停

初始P从封存O只读backup升级v156草稿：5794条旧行、9份旧required选择保持，无外键违规。P Thread `thread-run-20260909130702-4ad6a95666ab` 已配置Local、通过实际工具提出单方案；尚未采用、修改、运行检查或完成。此前先启动API后build，ui04仍见旧Plan控件，不能算新UI通过。其失败和原数据保留。

用户随后询问Windows密钥授权弹窗。未点击授权或尝试密码，暂停后核对旧P服务/browser均已退出、相关日志无匹配归属；原因仍未知。用户明确“那继续吧”后恢复。

补修导致未发布v156草稿内容改变，最终验收使用P2独立实例从O正常升级，不改P原迁移checksum。

## 最终实际产品旅程

证据目录为 `output/playwright/ux-phase-p2`。本机确定性响应服务只编排模型回复，真实工具、Go API、文件审批、磁盘写入、Windows LPAC/PS7及交付报告均由产品执行；没有伪造命令结果，也不代表真实模型能力评测。

| 步骤 | 实际结果与证据 |
| --- | --- |
| 原项目与配置 | source commit `2712c6d61ee8bf1c141e1452fca13ca608fac880`，五个源文件摘要先存fixture-notes；真实API导入/建Thread，Web确认Local/Plan，显式使用已配置PS7，无网络、无凭证。ui01–03。 |
| 一个实际方向 | workspace_read读取README与原check，plan_delivery_propose提出1方向/1模块；提案 `plan-proposal-20260909153008-9782285ac0d8`。不是在数据库插入预选计划。 |
| 采用并交付中断 | ui05实际select202/on_demand，实际Deliver202后仅丢弃浏览器收到的响应。界面保留已采用和结果未知，关闭重开后ui06只重发原Deliver key，202/replayed，同selection；没有第二次select或模型/工具调用。 |
| 审阅与修改 | ui07–10精确提议review.txt、预览diff、批准、应用；Edit `edit-935791021c9dc1e534902d0127afd06a`，只将UX_P_EXPECTED改为UX_P_EXPECTED_FIXED。 |
| 检查前报告 | ui11生成真实报告 `standard-code-delivery-866957de120b5ec99952122a8a58bb6c`：not_run、verified=false、verification_missing、0 Job。该报告保持原义。 |
| 实际原检查 | ui12普通发送执行原 `& ./check.ps1`，真实Job `command-job-db11818ca84505b681713ccc` exit0/tree_reaped，约1191ms，中文输出正常。检查不是模型声明或人工文字。 |
| 当前报告 | ui14生成 `standard-code-delivery-d987726da8ea455324158d4316bbfb04`，passed/verified；UI等待当前版本核对完成。receipt `365a190c6f16881a3ca6954cd263cef34c32cf4394aa974713b5c60ac61c2552`，diff仅review.txt。 |
| 无必填人工表完成 | ui15真实Start202/in_progress v2，Complete202/completed v3；dialog textarea数为0，没有新增人工CP。390×480完成按钮实际位于可见窗口内。 |
| 普通Finish与真实终态 | ui16普通发送、模型Finish通过真实交付门禁，202；按M语义仅结束Turn。随后17-cli-explicit-finish使用已有CLI显式结束原Run，completed。两者不混为一谈。 |
| 原对话继续 | ui18b原对话普通发送202，自动创建后继Run/Session，实际workspace_read读取同一Drydock的FIXED文件。ui19/21显示已完成事项和精确原完成来源，人工说明仍按需、0表单；后继未运行检查、没有交付报告，未冒用旧自动通过。 |
| 终态原key确认 | 20-terminal-selection-replay：原Run已completed且后继已存在时，用ui05原select key/body，202/replayed=true、同Selection。371张表全列摘要和14次provider调用数前后完全相同。 |

原Thread `thread-run-20260909152739-36f93087f619`；原Run `run-20260909152739-36f93087f619`，Session `sess-20260909152739-3eedb2fbbef9`；后继Run `run-20260909154440-91c2ddd88490`，Session `sess-20260909154440-982f2bb313d9`。稳定物理目录身份 `drydock-9d20e7b64b3027431242bc1fad86ca2c`。原完成事项 `work-20260909153159-a642156ac3b3`；继承的是 `plan-delivery-op-8973ec9bfb43a1ecbda67681b4ff7e8d41decdcf332d528ee98baf455c9ddfa7` 这一真实完成事件。

首次ui18在页面reload后未重填此连接的临时令牌，等待composer超时，尚未发送请求；原失败日志保留，ui18b完成连接后真实发送。报告生成前/后继尚无报告的404及主动丢弃Deliver响应的网络错误均保留，不能把它们抹成“控制台零错误”。最终续接尚无报告时的通用配置提示仍偏含混，列为后续反馈改进，不能将该提示当成检查失败。

## 针对变更的验证

- 领域/工具计划组：domain0.318s、toolgateway0.297s；1/2/3与0/4、实际方向越界保严格合同。
- 选择重放与HTTP/CLI：application6.661s、httpapi3.685s、app1.111s；两种策略、省略与非法/null、Deliver/terminal后原key、并发另store错过快查后事务内重放。Guidance最终按已选策略投影，不改旧Note/快照/hash；最后Guidance/失败后当前检查/CLI组合race application1.097s、app1.829s。精确命令见P/backend/backend-validation-results.md及guidance-current-verification-cli-final-race.log。
- 根部模块一致性四个实际红例修复后，on-demand-projection-after.log通过1.883s；独立最终store race共15个顶层测试52.911s，涵盖required守卫、按需内容与依赖、两类两代承接、v41/v44/v155旧记录/原key、clean-install与代表性旧升级一致性。精确命令和原输出见P/independent-final-store-race.log；各批重叠，不累计成独立用例数。
- 前端分批5文件201项（7.04s）、最终合同2文件176项（1.45s）、布局后3文件26项（7.56s）通过，最终typecheck/build通过。具体版本、命令和中间测试参数错误在P/frontend-final-summary.md；不宣称运行全前端或全Go。
- 最终OpenAPI DTO金样0.075s；以同版本openapi-typescript生成的类型与当前schema逐字节一致；协议33家族/1008标识/107显式项同步，diffcheck退出0。P/final-contract-checks.log保存精确命令；既有CRLF转换及Vite大chunk提示不隐去。

单方案宽度、人工策略选项排版、主Plan多余诊断行已经按实际界面补修。root目视1440×960的15-single-plan-final-wide.png和390×480的15-complete-without-manual-fields-narrow.png；后继19截图证明承接语义。21截图又发现来源详情长ID撑出横向滚动，现已仅对该详情补min-width/换行/取消默认缩进；最后typecheck/build均exit0（Vite阶段1.91s），未添加镜像样式测试。API重启后ui22/23实际加载最终index-DZi43RUe.js，来源宽/scrollWidth均350px，dialog均389px，末事件ID换行后高度62px，390×480可见。root目视22/23两张最终截图，原溢出截图保留。

## 最终构建与现场关闭

最终后端 `cyberagent-ux-phase-p-release.exe` SHA256 `7c62e729b2fe58e464f47aff386df8c6bc2e613f7998269013650dc0333a0555`，schema v156；Guidance及SQL一致性补修已包含。最终JS `index-DZi43RUe.js`、CSS `index-D_QfJWOv.css`，实际服务ui_digest `13602170b1aec74e03877a00349a3b8bb6671685fd13f0f44dee7885bc9907e7`。D401A7Nu是前一轮布局验收资产，BQVZFEZ8是采用/丢响应时资产；仅最后CSS变化另做窄屏复验，不能把全部历史截图标为最终资产。

23:51:30 +08，按精确进程身份关闭P2 API112392/provider109496，Playwright uxphasep2也已关闭，18867/18868无监听。24-browser-closed.log、25-services-closed.json保存结果。没有删除文件、旧失败夹具或此前被拒删除的N目录；没有提交、推送、合并、发布，也未触碰密钥授权。

预审发现旧blob全列比较有2行不同：仅共享的用户笔记和项目自带home文件的reference_count增加，原内容/摘要/大小/created_at未变。这两个内容被新checkpoint正常去重复用，旧v117 trigger维护计数。严格5794全列不变的原预审仍记录失败；每项8个新增引用已逐一核对，全部是本P2 Run、对应路径、sealed检查点，无旧引用删除，旧/新reference_count均与实际引用总数一致。不能把计数正常变化冒称全列不变。

## 保留的边界

旧3方向、required六字段、历史豁免与原key仍按原含义读取；新Web默认按需不能重解释旧记录。按需仅去掉必填人工说明，仍要按实际结果操作计划项，采用动作也不自动执行整个任务。跨Run仅继承完成进度，自动检查/当前报告需按新执行实际结果核验。

组合请求和人工草稿沿用当前QueryClient生命周期，未实现跨浏览器刷新/renderer/进程重启持久化。其他审批类型、原生DPI/键盘/平台矩阵、真实LSP、其余Host/CLI、全文搜索及性能仍待推进；完整F01–F14尚未完成。本机密钥弹窗归因仍未知，用户授权恢复不等于已定位弹窗原因；没有操作该授权窗口或修改密钥权限。


## 最终持久化审计

2026-09-10 00:00:52–53 +08，已关闭服务后的 `final-audit.json` / `final-audit.log` 中48/48项必要核验通过。原5794行比较中，除上述两指定共享blob计数由可枚举新引用增加，5792行全列保持；全部49份旧blob的内容/摘要/大小/created_at保持，9份旧required选择保持。`blob-reference-delta.json`记录两项各8个新增引用的精确checkpoint/run/path与计数对应，`audit-preview-corrected.json`保留44/45的原严格观察。没有改旧数据库或重写其历史失败为通过。审计器首次误用lease表的id列而失败也保留在audit-preview.log，随后按真实lease_id修正。

实际新状态为原Run completed、后继paused；原Run1个真实Job、3份report按顺序not_run/passed/passed，第三份来自ui16普通Finish。原选择是on_demand单模块，原WorkItem completed v3、人工CP数0；后继completed v1精确引用原事件，自动Job/report均0，监督快照也未继承verified状态。源项目五文件摘要与原commit保留，实际Drydock只有review.txt内容按请求改变，真实用户笔记和项目自带home文件保持。没有pending输入、active lease、未决文件事务或外键违规。

这份最终审计只覆盖P2的明确旅程及其继承的历史内容完整性；计数增加是已有内容去重语义的正常结果，不需要为通过预设“全列不变”而修改产品。最终工作必读已更新并同步原工作目录入口；所有产品更改留在独立工作树，未提交。
