# 普通 Thread 长任务上下文连续性

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-11。实施树：`<workspace>`，分支 `codex/ux-journey-convergence`，Git 基线 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。包含此前未提交的 A–统一框架改动；本报告只说明本批上下文工作。

## 问题与目标

用户明确要求复用现有压缩器，补齐普通 Thread 主链，并验收原目标、后续修正、限制、未完成事项和工具证据。此前只有源码判断；本批先通过真实 SQLite、ThreadTurnService→Handoff→Supervisor 和固定 Provider 捕获请求，复现了信息缺失。

旧主链连续 14 次普通提交均正常完成，同一个 Run 中实际执行一次 workspace_read。第 13 次和关闭重开数据库后的第 14 次请求均缺少首条目标、只读/禁止安装限制、待完成验收、第二条语言修正、原读取内容及精确工具 ID；没有持久摘要。原始记录没有被删除，缺的是模型输入。证据：`output/playwright/ux-context-continuity/thread-mainchain-baseline-red.log`。首次仅测试编译写错 NetworkMode 字段的失败单独留在 before.log，不算此问题的复现。

## 实现

- Supervisor 在请求模型前检查已提交历史；超过原 20 条边界时，用已有 contextmgr 压缩并保留最近 4 条。取消投影层直接切最近 20 条的行为。
- 如果完整请求仍超出模型窗口，先压缩到保留最近 1 条，再重建摘要、当前输入和同一组原生工具调用/结果。Root 不使用通用窗口适配器静默删历史后的请求。仍放不下则明确失败，保存原输入和失败身份。
- 摘要与 compacted 标记在同一 SQLite 事务中提交，校验当前 checkpoint、lease 及其代次。当前未完成模型阻止压缩；已被接管的旧代次记录不永远阻塞恢复。原文、来源、正文哈希、审批和工具账本不被改写。
- 摘要复用现有 handoff_memory.v1 和来源校验。保留原始用户意图、早期澄清、最近实质输入、最近公开进度及工具证据；重复的短续跑消息不占满保留位置。单条不再仅截前 220 字符，既有 1000 字符上限内保留完整内容，超长用明确的头尾节选。总 JSON 仍最多 4000 字符、12 条记录。
- 正常工具轮次完成时，在原完成事务内记录非授权的工具上下文：工具名、精确 call ID、终态、原结果 SHA256、短输出/错误内容。保留最近 6 次调用并标明省略；原始完整结果仍在工具账本。已完成不等于检查通过。现有公共消息投影不把这条内部 tool 上下文显示成重复聊天消息。
- 保留原任务目标，并检查摘要和继承上下文确实进入请求。记忆区预算随已配置的模型窗口增长，完整请求仍受工具、输出额度和安全余量的总预算约束。
- 旧 Session 调用 Supervisor 时由执行器持有压缩所有权，返回实际压缩标志/摘要 ID；外层不在释放 lease 后再次压缩，避免已提交回复被收尾错误改成失败。未委托 Supervisor 的旧 Session 压缩路径保持兼容。
- 连续模型后继不再用当前 Session 的摘要覆盖已继承摘要。单份保持旧格式，多份在既有 SummaryContent 内平铺，逐份保留原 ID、正文和 SHA256；同 ID 冲突、错误来源或摘要累计超过现有 16 KiB 限制时，明确拒绝创建后继，保留之前的内容。近期消息只读取未压缩记录，原文查询仍可包括全部历史。
- 上下文容量失败通过确切 Handoff 原因封存为 context_window_exceeded；与普通预算耗尽、模型空答复、取消区分，显示“当前模型的上下文空间不足”。原 key 确认不会再次请求模型。

本批不新增数据库表、Schema 版本或自动长期记忆，不增加摘要专用模型调用，不改变原权限边界。

## 已完成的验收

以下测试均使用独立临时数据库和文件，不连接实际付费模型，不修改用户工程或旧 S/Q 验收数据。

| 验收 | 实际结果 |
| --- | --- |
| 原目标、后续修正、只读/禁止安装限制、未完成事项 | 24 次正常 Thread 提交、两次持久压缩、关闭重开 SQLite 后，模型请求仍含全部指定事实 |
| 工具证据 | 实际 workspace_read 返回的内容、精确 call ID、原结果 SHA256 进入请求；来源绑定原 Session 行，未提升为系统或授权指令 |
| 原历史保持 | 首次压缩前和后续快照的正文、来源、哈希、时间一致；只允许 compacted 标志变化；工具账本和权限保持 |
| 窗口压力 | 少于 20 条历史、8192-token 窗口下，必须压缩后成功，context_history_omitted 为 0 |
| 当前输入过大 | 请求模型前明确失败、0 次 Provider 调用，原输入与失败身份持久保存，原 key 确认和重开读取一致 |
| 同轮工具循环 | 实际 workspace_read 返回后收窄测试窗口，下一请求触发摘要；原始调用参数和结果完整配对，工具只执行一次，当前输入/attempt/lease 不变 |
| 原子性与恢复 | 注入压缩标记写失败后摘要一并回滚；重试、重开、旧 checkpoint/lease、接管、活跃模型及 pending 工具边界通过 |
| 连续切换模型 | 真实 SQLite 的 A→B→C→D 后继保留三个来源摘要的原 ID/正文/哈希；来源使用合法 contextmgr 压缩生成，普通选择/重置模型及 Thread 提交形成后继；不是模型自行生成摘要的能力评测 |
| 继承摘要容量 | 超过 16 KiB、摘要身份冲突或来源 Session/Workspace 不匹配时保持原内容并拒绝；更大已配置模型窗口能够容纳超出旧固定记忆区的继承内容 |

主链三项 race 最终集合：`thread-mainchain-final-race-2.jsonl`，6.899s。工具循环 race：`retention/native-pressure-race-final.log`。contextmgr 全包 race：`retention/race-final.log`，包含 6 个新顶层用例。存储 race 最终 5 个顶层：`store/targeted-race-final.log`，2.624s。重复运行及包含关系不累加为验收数量；最终整合命令、唯一计数和原生包在下方收尾记录。

## 验证过程中处理的失败

首次接通后，原任务要求已保留，但工具内容因“长封装→输出摘录→摘要摘录”多层截取仍缺失。已压薄 Go 生成的事实表示，并针对 JSON 文本 content 保留实际观察；最终同时检查原内容、ID 和哈希，未将“保留引用”冒充“保留内容”。

旧测试对两条 Session 消息的假设需要区分用户/助手和新增非授权工具上下文；更改后的断言同时验证精确来源和重放不重复。旧 Session 第五轮轮后压缩断言更新为 Supervisor 第十二轮模型前压缩，并验证返回真实压缩标志和模型收到摘要。旧未允许工具的格式修复预期及完成冲突仍标 started 的预期，与先前已实现的准确失败语义不符；按直接拒绝、零工具执行和保留失败 checkpoint 校正。货币结算夹具固定小输出窗口，避免生产工具输出额度掩盖其结算断言；未放宽实际货币预算。

额外尝试的旧 v82 降级夹具在调用摘要存储前即因当前 v157 trigger 引用已删除 threads 表而失败，留存于 store 的日志与说明，未修成绿、未声称全库迁移通过。一次全 application 集合进入不相关 Drydock 工程测试后主动停止，结果不算通过，改为上下文/执行/审批/恢复相关集合。

## 证据边界

这是有界的原文提取摘要，不是语义理解模型。不能保证任意长消息中任意位置的所有细节永久无损；摘要记录和长正文会显式省略，完整来源仍保存在历史中。最近公开进度也不等于程序已经理解所有未完成事项。

本批固定 Provider 请求捕获证明内容进入真实产品执行链路，不证明任意真实模型一定遵守所有要求或永远不会遗忘。未回填旧版本从未写入 Session 的成功工具上下文；旧失败网页的临时完整证据补充也没有泛化为永久摘要。未运行新的真实模型任务能力评测或 Windows 高 DPI 验收。

## 最终收尾

最终整合回归（Go 临时目录为 `D:/Traverse-P-Temp`）：

```powershell
go test -race ./internal/application -run '^(TestRunSupervisor|TestSupervisor|TestThreadTurn|TestThreadContext|TestThreadModelSwitch|TestSessionRunChat|TestApprovalContinuation|TestRootProtocol|TestThreadContinuity|TestThreadTerminalComposer|TestThreadContinuation|TestConcurrentThreadContinuation|TestThreadSummary|TestContinuityCheckpoint)' -count=1 -json
go test -race ./internal/httpapi -run '^(TestThreadTurnHTTP|TestThreadTurnFailureHTTP|TestThreadFailureReference|TestThreadHTTPContract|TestThreadHTTPCreationAtomically)' -count=1 -json
go test -race ./internal/session ./internal/runactivity ./internal/threadtranscript -count=1 -json
go vet ./internal/application ./internal/contextmgr ./internal/store ./internal/session ./internal/runactivity ./internal/threadtranscript
```

三个 JSON 集合分别通过 155、7、38 个顶层测试，无失败或跳过；这是互不重复的 200 个顶层用例，不计子用例，也不与前面的重复定向运行相加。application 用时 87.066s，HTTP 4.818s，其余三包各约 1.3–1.6s。日志为 `application-final-race.jsonl`、`http-final-race.jsonl`、`session-projection-final-race.jsonl`；机器统计 `final-tests-result.json`。vet 退出 0，日志 `vet-final.log`。contextmgr 全包及存储定向 race 另见上面的独立证据；不称全仓 CI 通过。

最后只读复核发现跨内部工具段没有聚合新增的 ContextCompacted 标记。真实回归先复现“首段已经生成摘要、最终段却返回 false”，随后以四行聚合保留真实标志和最近摘要 ID；实际模型上下文一直保持。`go test -race ./internal/application -run '^TestThreadToolBoundary' -count=1 -v` 最终 6 个顶层全部通过，9.867s，记录在 `retention/boundary-compaction-race-final.log`；与上面200项不重复，子用例不再相加。其首次集合中的旧六工具 case 仍假设历史只有三条，已改为逐项验证新增两段工具证据及原用户/助手消息，未删除证据或放宽来源检查。该四行变更后补跑 application vet，退出 0（`vet-final-boundary.log`）。

本地预览包：[TraverseBoard-UX-Context-Continuity.exe]（本地保留证据，未公开：`output/playwright/ux-context-continuity/TraverseBoard-UX-Context-Continuity.exe`），103905280 bytes，SHA256 `4c8f1fcebe6d3e967a3de254c801980e6a33bbf15d525141f2c3aa595b400f0e`，版本 `v0.1.0-ux-context-continuity-preview`。

`native-build-result.json` 记录构建退出 0；`native-artifact-validation.json` 核验全部 18 项通过。1654 个生产源码/资源输入在构建前后哈希一致，116 个前端资源完整嵌入，入口沿用已验收 `index-b8IzWfGl.js`。Windows GUI 子系统、asInvoker/DPI manifest 和 15 个图标通过静态检查。没有启动程序，不将此称为原生高 DPI 渲染实测；未覆盖旧包，没有提交、推送、合并或正式发布。
