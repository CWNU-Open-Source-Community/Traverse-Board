# 第十五批：暂停和结束后的单文件逆向审阅

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-09，Asia/Hong_Kong。状态：本轮限定范围已实施并完成真实验收；完整 UX 清单仍未完成。

## 范围与已复核问题

用户在 N 结果后要求“继续”。本轮主线是单文件逆向提案经过普通对话续接，完成
审阅、批准和应用；小任务 Plan 只做明确的简化评估，记录在
[UX_PLAN_SIMPLIFICATION_REVIEW.md](UX_PLAN_SIMPLIFICATION_REVIEW.md)。后者没有产品改动，
不能当作三方案/六字段已取消。

工作树仍为 `<workspace>`，分支
`codex/ux-journey-convergence`，HEAD `15c399c8940aeb81d3c45bb0de96759b480d02c2`。
A–N 未提交修改与原工作树已有工作保留；无提交、推送、合并或发布。

旧 FileEditPanel 和 service/store 都要求 Running 才能新建逆向提案；旧 source 的
Session/approval 又精确属于原 Run，因此不能把按钮放开或在前端替换 RunID。
新方案复用普通 Thread 消息、已有 `workspace_change` 动作扩展及逆向/审批/Apply。
不放宽暂停写入门、不隐式恢复旧执行、不新增无消息控制接口或数据库迁移。
取舍与边界见 [ADR0158](adr/0158-reviewed-reverts-through-thread-continuation.md)。

## 已完成实现（源码确认）

V2 对暂停及历史已结束执行的已应用编辑提供“在对话中撤销此编辑”。点击只将简洁
自然要求和精确来源执行、编辑、目录、路径及预期当前 hash 加入可编辑草稿，保留已有
用户输入，关闭审阅并聚焦输入框；不会自动发送、恢复执行或写文件。原始内容及旧 hash
由服务端根据来源记录推导，不把完整工具 JSON、协议版本或动作参数放入产品正文。
运行中且属于当前目录的编辑仍走既有直达逆向；已有提案及结果未知的原 key 确认优先，
不会被草稿动作绕过。归档、只读和无控制能力连接明确不可新发，关闭审阅不更改草稿。

后端在原 `workspace_change` 下增加 `propose_revert`，严格只接受版本、动作、路径、
预期 hash 和来源 Run/Edit。服务先核对不可变来源及其原批准、同 Thread 和同物理目录，
再生成属于当前 Run/Session 的独立 pending 编辑和审批。来源批准仅证明历史，不是
当前写入授权。新提案仍需 Running/active Session，当前目录持有者和清理互斥在存储
事务中重验；暂停和终态通过用户普通发送进入既有续接，不直接开放暂停写入。

源文件原文/hash 必须完整、未脱敏；替换、创建、删除使用相应精确逆向，移动仍不支持。
新操作要求当前文件仍匹配来源应用后的 hash，删除后的逆向创建要求路径仍不存在。
已插入提案的精确原 key 重放先返回原记录，不重新读取后来变化的文件、不重置审阅状态。
Supervisor 只额外允许 `Execute` 中的 `propose_revert` 提案，保留计划、检查、权限、
预算和真正应用的门禁；不会生成自动验证通过。

只读交叉复核确认：新提案事件为“文件修改已提议”/pending；工具详情按新 Edit ID 和
当前 Session/Workspace 读取反向 Diff；同路径列表、选择和审批使用 Edit ID。模型生成
的逆向没有前端直达请求缓存，也仍可作为普通待审提案查看、批准和应用，不会继承源编辑
的已应用状态。该局部复核没有发现列表分页 SQL 中的旧 creator 过滤；真实终态走查随后发现并修复，见下文。

## 回归检查

在 `web` 目录执行：

```powershell
npm test -- src/components/file-edit-panel.test.tsx src/v2/components/conversation-revert.test.tsx src/v2/components/task-review.test.tsx --maxWorkers=2
npm run typecheck
```

最终自然语言草稿版本为 3 文件 24 项通过，6.43s；typecheck exit 0。覆盖 paused、历史
completed、原草稿及焦点、无自动发送/写入、取消不变、只读/归档、已有提案和未知 key
优先。证据是 `output/playwright/ux-phase-o/frontend-revert-natural-draft-final.log` 与
`frontend-revert-natural-draft-typecheck.log`。最初重复状态 guard 引起的 TypeScript 窄化
错误留在 `frontend-revert-typecheck.log`；修复后的检查及原完整 JSON 草稿的中间结果均
保留，不能把中间构建冒充最终界面。`frontend-build-natural-draft.log` 记录构建成功
（1.86s），输出主资产 `index-6LYNjlnn.js`；ui10 实际回读确认加载该资产。

后端命令均使用进程级 `GOTMPDIR`、`TEMP`、`TMP` 指向 `D:/Traverse-O-Temp`，详细
原文与边界见 `output/playwright/ux-phase-o/backend/backend-validation-results.md`。

| 检查 | 结果与原始日志 |
| --- | --- |
| `go test -p 1 ./internal/toolgateway ./internal/application -run '^TestAgentCodeRevertPayload' -count=1` | toolgateway 0.306s；application 0.058s 仅编译、`[no tests to run]`；`backend/revert-contract-first.log` |
| `go test -p 1 ./internal/application -run '^TestAgentCodeRevertContinues' -count=1 -v` | 真实 SQLite/Git 续接、提案、新审批与应用通过，包 75.228s；`backend/revert-continuation-second.log` |
| `go test -p 1 ./internal/application ./internal/httpapi -run '^TestFileEditRevertProposal' -count=1` | application 3.728s、HTTP 0.437s；`backend/legacy-revert-first.log` |
| 最终相关 race（命令如下） | toolgateway 1.456s、application 203.077s、HTTP 1.770s，exit 0；`backend/revert-final-race.log` |

```powershell
go test -p 1 -race ./internal/toolgateway ./internal/application ./internal/httpapi -run '^Test(AgentCode|FileEditRevertProposal|StandardCodeSupervisor)' -count=1
```

最后 race 于 2026-09-09 16:22:31 +08 确认完成；覆盖显式来源的创建/删除逆向、改 hash
和错误来源 Run 拒绝、既有能力与目录身份、Supervisor 及 HTTP 同 Run 兼容路径。
真实续接夹具还覆盖第二 SQLite 连接重放已插入提案、后来的外部修改及其他 Thread
拒绝、旧编辑/批准和 source CRLF 不变。它没有运行或伪造 Shell Job，也不是浏览器或
完整平台验收。第一次续接检查仅因测试未使用的 `encoding/json` import 编译失败，
原日志 `backend/revert-continuation-first.log` 保留，不能记为产品故障复现。

本次没有增加数据库表、迁移、HTTP DTO 字段或公开协议版本；未跑全仓或全平台测试。

## 真实基线

O 从 N 数据库做只读 backup，旧 N/M2 目录不写。新 source commit
`7742e8763b2cef711857aa3481b0f54df11ceb46`，包含五个用户文件。
Thread `thread-run-20260909080553-630557341b3a`，初始 Run
`run-20260909080553-630557341b3a`、Session `sess-20260909080553-c8aa01b00135`。
导入/创建经真实 API；UI 选择 Plan、进入 Deliver、审阅批准源 edit
`edit-ae194a9de27558dbc9dde9f2cc79ba88`，普通消息真实应用 review.txt：
`UX_O_EXPECTED` → `UX_O_EXPECTED_FIXED`，随后暂停。

`ui-08-paused-baseline.log` 和 `08-paused-disabled-baseline.png` 实际记录旧 JS
`index-8Ceg5rk6.js` 下撤销按钮 disabled；这不是仅源码推断。准备使用 N 最终 Go
执行程序，后续新动作必须在 O 最终候选中单独验收，不能引用准备阶段作为修复通过。
本机确定性 provider 只编排模型响应与工具调用，实际 Job、文件、审批、报告由产品产生；
不推导真实模型质量、所有 provider 或品牌能力排名。

## 真实连续旅程

所有日志、截图、数据库核验和程序身份在 `output/playwright/ux-phase-o`。模型响应来自
本机确定性夹具；工具、文件、审批、Git/SQLite、Windows LPAC 命令走实际产品路径。

| 步骤 | 实际结果与证据 |
| --- | --- |
| 暂停后发起 | ui10 点击“在对话中撤销此编辑”，保留原补充草稿与焦点，0 POST；宽 1440×960 / 窄 390×480 截图已查看。ui13 普通发送生成新 pending inverse，ui14 显示精确反向 Diff。 |
| 新批准与应用 | ui15 只批准编辑意图，`file_written:false`；ui16 普通发送后真正应用，内容从 `UX_O_EXPECTED_FIXED` 恢复为 `UX_O_EXPECTED`。 |
| 再次恢复编辑 | ui17b 引用已应用 inverse，ui18 新提案、ui20 新批准、ui21 独立应用，内容再次为 FIXED。不是回写原 Edit 状态。 |
| 原检查与报告 | ui22 实际 `& ./check.ps1`，Job `command-job-bb2955ddb9de7c83afdb446b`，exit 0、913ms、tree_reaped，stdout `UX_O_CHECK_PASSED 中文检查通过`、stderr 空；ui23 报告 passed/verified，ui26b 已核对当前版本。 |
| 人工记录与本轮完成 | ui24 开始计划项，ui25 六段真实且明确受测边界的人工说明，ui26 完成成功；ui26b 显示完成 1/1。ui27 普通 Finish 提交，产生第二份通过报告，结束当前 Turn。 |
| 真正终态 | 普通 Finish 与 ui28 的真实 503 都只结束 Turn、保留 Run，这是 M 的预期。另通过已有 CLI `run finish` 完成终态准备：29-cli-explicit-finish.log exit 0，Run 真正 completed，无直接改数据库。 |
| 原对话自然后继 | ui30 从历史 source edit 生成草稿，保留此前失败输入；ui31 普通发送自动产生新 Run/Session、仍用同物理目录，生成新的 pending inverse。没有点击恢复入口、重新导入目录或重绑旧审批。 |
| 续接后的审阅应用 | 修复下述队列遗漏后，ui32e 从重启实例直接读取原 pending。ui33 新批准，ui34d 在 390×480 收起侧栏后普通发送并成功应用。未重新生成待审提案来掩盖列表问题。 |
| 后续用户修改保护 | 第36步只向 O 的当前 review.txt 追加用户 marker。ui37 引用最新 applied inverse，ui38 实际工具返回 CONFLICT，0 新提案/审批/应用；文件保持追加后的内容。ui41 显示明确核对来源与当前文件的原因。 |

两份原报告为 `standard-code-delivery-d87bfbdb8233eefaedd5d76aba6501ce` 与
`standard-code-delivery-5befadd5d0663ee884885d6ceb96f1ff`，均只引用上述真实 Job。
它们保存撤销前的验证收据；随后撤销及外部修改不重新封装旧收据。
后继 Run 没有新 Job 或自动通过报告。ui43 读取完成后明确提示不能据此判断检查通过；
原人工计划记录标明历史沿用，不冒充当前自动检查。ui35 的瞬时截图仍在加载，不用它
代替 ui43 的最终读取结果。

三条新的逆向编辑分别为：

| 新编辑 | 来源 | 所属执行 |
| --- | --- | --- |
| edit-revert-2a793902e6c6a54b3df4af087326f2004c20df56c0f51eb8 | 原 edit-ae194a9de27558dbc9dde9f2cc79ba88 | 初始 Run/Session |
| edit-revert-33aada6f72e4cf805f8f506f81b7abae65e1dee163ee9716 | 上一条 inverse | 初始 Run/Session |
| edit-revert-eb9c8db5c4d2b24e32ad89c7181de97f38cd8a4c4a72e304 | 原 edit-ae194a9de27558dbc9dde9f2cc79ba88 | 后继 run-20260909083733-0058bc2c73e9 / sess-20260909083733-1f7060be70e9 |

物理目录始终为 Drydock `drydock-6d4bb3f08b34f699660ee8edf54ce6ce` 的原 path，
creator/session/path 不变；当前持有者为后继。最后内容为 `UX_O_EXPECTED\n` 加
`UX_O_USER_CHANGE_AFTER_REVERT\r\n`，SHA256
`6952e4f38707b0a2ef0af880e0277f157437d4b2d1773b9ef6c7f22884e5fdde`。
源项目五文件、工作目录其他四文件均保持。该用户标记保留，没有复原后伪称拒绝有效。

## 动态验收发现并修复的遗漏

1. **后继的文件列表为空。** ui31 已实际生成新 Session 的 pending inverse，
   `/runs/<successor>/file-edits` 却返回 200 + `items:[]`，ui32b 显示没有提案。
   `ListRunFileEditPreviewsPage` 仍按 `drydock_workspaces` 的 creator Run/Session 过滤，
   没有使用已有续接绑定。现与单项详情的归属判断一致，按已有 schema 是否支持选择
   `run_file_drydock_bindings`；保持 Run/Session/Mission/source/Workspace 条件，先过滤
   再分页，保留旧 schema fallback。没有新增 view/table/migration，也没有补造 UI 行。
2. **冲突提示引导盲重试。** ui39 原始展示为“执行上下文已变化，请重试”。仅对规范化
   `workspace_change.propose_revert` 的 CONFLICT 改为先核对来源编辑和当前文件、保留
   后续修改。没有推断所有冲突都必然来自磁盘变化，没有暴露原始错误或更改错误码。
   ui41 在 release 程序中重新读取原失败事件已验证；没有重发请求或改历史数据。

补充回归：`continued-file-queue-before-second.log` 在旧代码真实复现 200 空列表；
修后 store/HTTP 定向 race 分别 12.223s / 45.591s，覆盖后继 pending 列表与详情一致、
分页、旧 Run 和同源不同 Thread 隔离、无 resolver 时只读。
`revert-conflict-presentation.log` application 8.351s 通过；HTTP 0.054s 仅编译、
`[no tests to run]`，不冒称执行了 HTTP 呈现测试。协议检查仍为 33 家族、1008 标识、
107 显式测试/金样；diff check 通过，仅保留已有 LF/CRLF 提示。

## 原请求重放与最终持久核验

ui31 的原 key/body 各做一次确认：`40-original-terminal-key-replay.json`，以及实际
release 重启和用户改动之后的 `42-original-key-after-restart-and-user-change.json`。
两次均 202、`replayed:true`、同 committed steering、同后继 Run/Session；Edit、审批、
Apply、Job、报告、provider 日志及 Thread 记录摘要前后不变。未换 key、未重复模型调用。

`final-audit.json` 实际以 `--final` 运行，**106/106 通过**，不是把 preview 标记翻转。
初始基线、未完成预检查、明确最终预期和冷启动前预期均保留。最终检查包括：

- N 原有 6 Job / 10 报告 / 13 Edit / 13 审批 / 44 artifact，M2 原有 4 / 8 / 12 / 12 / 35，
  以及相关审批/应用表，按原行主键和全部列摘要保持；18 个旧夹具物理目录逐文件保持。
- O 原编辑、原批准与应用记录不改；正好三个独立新逆向、各自新批准和一次真实应用。
- 同 Thread 两代执行，初始 completed、后继 paused；同物理目录、当前持有者正确。
- 正好一个新真实 Job、两份原执行报告；原 source 五文件和最终五文件的预期摘要匹配。
- 外部修改后 failed/CONFLICT 的工具记录没有新 edit；原 key 重放未创建重复副作用。
- 无 pending 输入、未决交付、活动租约、未决文件事务或外键违规。

最终审计 SHA256：`da5dc00a9ab2eeb390cae4a4b571085f1e60888631057bac65be818aa604de16`。
执行时间 UTC 09:00:05.525581–09:00:05.689110。它证明持久化和文件事实；UI 与 OS 结论
来自上面的独立实际日志。旧报告仅证明其记录版本，不能证明后来修改后的文件已验证。

## 中间失败与证据边界

| 记录 | 解释 |
| --- | --- |
| ui11 | 准备脚本误查 approvals 而非 tool_approvals，Shell 未拦 native 失败，旧 provider stage 继续；202 但 tool_called:false、0 新 Edit。修正后 ui13 才是有效逆向链。 |
| ui17 / ui17b | combobox 自动化误用“执行范围”，修正为实际 aria“选择审阅的执行记录”；不是产品不可选择历史。 |
| ui26 / ui26b | 完成事项已成功，后置脚本误找“刷新报告”超时；只读快照证实完成 1/1 和当前通过，没有再次完成。实际名称为“刷新交付报告”。 |
| 队列测试第一版 | 缺少 checkpoint service 导致夹具应用失败，未当作队列 bug 复现；第二次旧代码测试才真实复现空列表。 |
| ui32c / ui32e | 对相同地址 goto 没有刷新页面，脚本误等令牌框；改用真正 reload 后连接并读取原提案。 |
| ui34 / ui34b / ui34d | 窄窗口的侧栏遮罩及遮罩中心定位妨碍自动点击，前两次没有发送；用实际“隐藏侧栏”按钮后 ui34d 才发送并应用。没有 force click 或修改响应。 |
| ui28 | 主动编排的真实 503，失败 Turn 保留原输入与工作。它不代表 Run 已进入终态，也不是 O 的未修 Shell 故障。 |

报告生成前的 404 和故意的 503 保存在控制台日志，不能声称整个过程零错误。
初始中文路径读取失败的审计尝试也保留，后续只读 I/O 使用 Windows 扩展路径，
没有跳过旧长文件或删除缓存。没有通过改命令、移除用户文件或伪造 Job 获得通过。

## 构建身份、清理与未覆盖范围

最终 `cyberagent-ux-phase-o-release.exe` SHA256
`54ac1a1d43a239374a26b82ac4416146bca39ae1a48efda56924a37ac840b321`，
实际 JS `index-6LYNjlnn.js`、CSS `index-hT11lUFT.css`，schema 仍为 v155。
ui32e/33/34d 使用包含队列修复的前一版程序；最终 release 只额外修正冲突说明，
ui41 与冷启动原 key 确认在最终版执行。构建日志和全部中间 exe 身份均保留，
N 原生修复文件摘要未变，不把它描述成重新验收了所有原生环境。

真实命令明确使用已配置的 PS7 路径；没有自动下载安装或保证任意宿主 PS5 可用。
主代理查看了 ui10 草稿/动作、ui32 逆向审阅、ui34d 发送及 ui41 原因截图。
宽 1440×960、窄 390×480 的选定按钮可达，窄发送要求收起侧栏；不等于全平台/DPI/键盘
与所有布局矩阵完成。替换的完整 Web 链已实测；逆向创建/删除等由针对性后端回归覆盖，
删除仍须已有独立确认，不扩大为任意移动、多文件或快照回滚。

17:02:08 +08 已按身份关闭 API61256、provider79524、browser73248，18867/18868 无监听，
runtime owner 仅剩 owner.lock。未删除任何用户文件、旧记录或此前被拒的空测试目录。
独立工作树 A–O 改动仍未提交，原工作树只同步工作必读。

下一步优先实施小任务 Plan 简化：允许有实际需要的一至三个方向，人工说明按需，
保留真实验证、权限与原记录的含义。该方案目前只有评估文档，**尚未实施**。其余审批、
原生/DPI/键盘、真实 LSP、Host/CLI、跨重启草稿/未知 key、全文搜索与性能仍按风险推进。
没有因为这一条旅程完成就宣称完整 F01—F14 收敛或所有测试通过。
