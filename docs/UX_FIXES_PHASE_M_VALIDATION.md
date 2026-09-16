# Phase M：持续对话、独立轮次与工作目录延续验证

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-09。**M 限定范围已完成本地实施与验收。** 工作树 `<workspace>`，分支 `codex/ux-journey-convergence`，实施基线 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。既有 A–L 本地改动保留，未提交、推送或发布。最终升级、换模型与断连/停止连续旅程见本文后半部分；不宣称完整 UX 清单或各平台全部通过。

本轮按用户明确选择的 Thread/Turn 方向实施：用户继续发送要求，后端处理失败收束、去重与必要执行上下文衔接；不把选择或恢复某个 Run 变成日常操作。架构决定与维护成本见 [ADR 0156](adr/0156-thread-continuity-and-independent-turn-outcomes.md)，总目标及剩余范围见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)。

## 证据与实例边界

- **源码确认**表示当前实现及约束可核对；不等于所有入口已经实测。
- **自动回归**说明实际执行的测试范围。SQLite/Git/文件工具集成与模拟模型、模拟命令结果分别声明；模拟 Job 不算真实 Shell 验收。
- **真实 UI/API**使用本机脚本化 provider 控制响应，产品 API、数据库、文件工具和明确标注的隔离命令实际执行；不证明模型能力或与 Codex/Claude Code 的性能排名。
- 原始证据位于忽略目录 `output/playwright/ux-phase-m`（下称 M）和 `output/playwright/ux-phase-m2`（下称 M2）。文件名中的 `final` 仅代表写入时那个候选的检查，不能自动代表后来改动也已验收。
- M 的早期模型切换候选曾尝试给每个后继复制工作目录。该方案因忽略的依赖、缓存和大文件阻断日常续接而撤回；M 实验实例封存，M2 从 L 数据库备份重新建立受控项目。早期 clone 结果不计为最终稳定目录方案通过，也不回写旧实验账本来冒充升级。

## 当前已实现的契约（源码确认）

1. **失败属于本轮。** 可恢复失败只有在持久收束成功、原输入及已完成工具证据进入原 Session、未决副作用已排除后，才返回可选 `error.turn_failed:true`。同 key 精确重放仍返回该失败结论；通用 HTTP 错误、网络断连不猜成已经结束。失败 handoff 和旧工具结果保留，下一条新要求获得原上下文而不重执行旧工具。
2. **自然发送与未知结果分开。** V2 对未知提交只自动核对一次原 key/body；仍未知时保留明确核对入口。已收束失败可以直接发送新要求。暂停且无停止过程时可正常发送，由既有服务恢复执行；停止中、停止失败等按实际状态控制。新草稿、其他 Thread 和晚返回按精确提交归属隔离。202 受理或队列保留不被写成已经完成。
3. **接受的补充要求不悄悄丢弃。** 下一次模型调用前呈现适用的 pending 输入，只有各自处理提交后才消费；停止保留已接受输入。必须换执行上下文时，适用但尚未执行的要求有单独来源说明，不将取消历史改成成功。
4. **稳定的 Thread 工作目录。** `thread_drydock_bindings` 保存相邻执行关联，物理 Drydock 仍保留原创建 Run/Session/path。新文件、检查点、命令及报告使用本次真实执行身份；新操作仅当前持有者可执行，旧 Run 历史读取不变成执行或清理授权。普通依赖目录、缓存和大文件不因换模型被克隆策略丢弃。
5. **人工计划延续，当前检查重新做。** 选中方向、模块及完成进度通过精确同 Thread 来源投影。当前完成项引用原始有效人工 checkpoint 和 handoff；没有复制一份新 checkpoint，也没有复制 Job、审批或报告。改 criteria/dependencies、缺原人工记录等不能沿用完成状态。未选择的方案仍可继续选择；新 Run 只初始化消费量为 0 的既有工具预算行，以满足原选择操作的 writer fence，不伪造工具调用。
6. **保留修改观察，不保留旧通过。** 新 Run 的第一次真实 lease/turn 读取相邻前驱的持久 Supervisor 观察，并核对原 `workspace_apply` 调用、FileTool 事务及 sealed after checkpoint。原 mutation epoch 有明确来源；当前权限、工作目录与能力重新核对，旧 verified、Job、报告和消费计数清空。正常已观察修改可直接进入重新检查，无需再做一次无意义编辑。后续世代引用原始修改证据；已有批准不转为新授权。
7. **清理是独立的删除事实。** 当前 Thread 持有的目录不会按普通到期策略自动清理。显式删除用窄 `drydock_cleanup_operations` reservation 与发布/执行互锁，不伪造目录已经不存在后的成功内容快照。中断须以原请求核对实际结果，不能靠超时自动解除 reservation。恢复与清理的最终检查见后文，未知删除结果不会被提前封成“已保留”。

Supervisor 的此次自动衔接依赖可证实的 `call_observed workspace_apply`。只有人工直接 Apply、从未经 Supervisor 观察的旧 Run，不会被推定为已有自动 mutation epoch；目录文件仍保留，但该旧人工 Apply 与自动观察的衔接不在本次已通过范围。跨应用重启持久保存 renderer 草稿/未知请求 key，也未在本轮承诺。

## 已执行的自动回归

以下命令在实施工作树运行；前端命令工作目录是 `web`。

| 范围 | 命令及结果 | 原始证据与边界 |
| --- | --- | --- |
| V2 自然发送、未知确认、暂停发送及任务归属 | `npm test -- src/v2/components/conversation-recovery.test.tsx src/v2/components/conversation.test.tsx src/v2/components/composer.test.tsx src/v2/components/file-context.test.tsx src/v2/components/thread-run-recovery.test.tsx src/v2/components/thread-execution-control.test.tsx src/v2/app.test.tsx --maxWorkers=2`；7 文件、53 项通过，13.64s | M/`frontend-continuation-with-paused.log`。真实 Conversation＋Composer 交互，覆盖失败→确认仍失败→原 key 成功、保留新草稿、另一任务晚返回、一次自动核对上限、暂停发送不要求先调用恢复按钮。 |
| 错误契约 | `npm test -- src/api/client.test.ts -t 'terminal failed-turn marker\|terminal unqueued turn rejection\|operation-key invalidation marker'`；3 项通过、164 项未匹配跳过，2.36s | M/`frontend-error-contract.log`。仅 literal true 接受；错误类型及互相矛盾标志不被宽松解析。未称全 client 测试通过。 |
| Plan 人工历史来源 UI | `npm test -- src/components/plan-delivery-work-items.test.tsx src/components/plan-delivery-work-items-regression.test.tsx src/components/plan-delivery.test.tsx --maxWorkers=2`；3 文件、15 项通过，10.85s | M/`plan-history-ui-final.log`。读取原 Run 的原 handoff；显示当前自动检查仍须重新验证；ready 后不开放改写旧记录的表单。 |
| 最后类型同步 | `npm run typecheck`，exit0 | M/`plan-frontend-final-typecheck.log`。OpenAPI/TS 重新生成后修正测试夹具的 enum literal widening，没有放宽产品类型。 |
| Plan 持久来源及旧版本 | `go test -race ./internal/store -run '^(TestThreadPlanContinuation\|TestDeliveryCheckpointSQLite\|TestPlanDeliverySQLite\|TestSchemaV41Upgrade)' -count=1`；48.242s，通过 | M/`plan-final-integrated-race.log`。真实 SQLite 两代完成项、原 CP/handoff 归一、HTTP 只读投影、缺 CP、改标准/依赖拒绝、未完成 gate、继承未选方案后正常选择、旧 V41 升级。人工完成门通过不等于 Standard Code 自动验证通过。 |
| Supervisor 修改观察与重新检查 | `go test -race ./internal/application ./internal/domain -run '^(TestStandardCodeContinuation\|TestStandardCodeSupervisor)' -count=1`；application 81.423s / domain 1.164s，通过 | M/`supervisor-continuation-exact-call-race.log`。真实 SQLite＋Git＋注册 Gateway 两次读取、Plan、选择、提案、审阅、Apply，再两代 successor；新真实 turn 中持久命令调用的门禁可达，原 source CRLF 不变，原 mutation ledger 不改，伪造来源被拒绝。命令输出使用明确 synthetic DTO 测试旧 passed 不继承及当前状态机成功分支，**没有在此测试执行 OS 命令**。 |
| 稳定目录及失败发布恢复 | `go test -race ./internal/application -run 'TestThreadFileContinuation\|TestRunFileWorkspaceConfiguredMissingDrydockFailsClosed' -count=1`；56.345s，通过 | M/`native/stable-directory-final.log`。真实 Git/SQLite 审阅 Apply、三代同物理目录、源笔记保持、ignored node_modules/.cache/5 MiB 文件保持、发布前失败与发布后丢响应按原 key 恢复；Created readiness 不虚报执行授权。不含随后新增的恢复/清理分支。 |
| Thread / 当前物理身份 / 权限 / 执行互锁 | `go test -race ./internal/application ./internal/httpapi ./internal/store -run '^Test(ThreadTurn\|ThreadRunRecovery\|ThreadRunExecutionRecovery\|ThreadDrydock\|ThreadStandardCodeContinuationPreservesCurrentPermissionAndRequiresProvenance\|FileEditDrydock\|LocalCommandCapabilities\|SQLiteRunExecutionLease\|RunLifecycle)' -count=1`；372.869s / 23.882s / 19.386s，通过 | M/`backend/stable-runtime-and-turn-final-race.log`。失败、停止、预算/配置输入延续、stable 实际 Apply、物理创建者与当前 lease、精确 preset 来源；编译早于最后 Advance metadata fence 与新增恢复分支，不称后两者已由此覆盖。 |
| 跨连接清理 reservation | `go test -race ./internal/application -run '^TestThreadDrydockPreparedCleanupBlocksExecutionAndPublicationAcrossConnections$' -count=1`；16.313s，通过 | M/`backend/stable-cleanup-metadata-final-race.log`。包含实际 Use 在 prepared cleanup 下拒绝。其后恢复分支结果见后文最终检查。 |
| 迁移与生成基线候选 | `go test ./internal/store -run 'TestSchemaV154\|TestSchemaV153\|TestSchemaV41Upgrade\|TestCleanInstallBaseline' -count=1`；61.866s，通过 | M/`native/binding-schema-baseline-cleanup-final.log`，含 cleanup FK 修正。其后 Undo/Rewind/Fork runtime SQL 又有改动；后文48.680s检查覆盖最终迁移和基线，这条保留为候选证据。 |

后端详细命令索引为 M/`backend/backend-validation-results.md`。它明确区分早期 clone 候选、稳定目录候选和后续修复。部分中间日志是测试夹具的 key 长度、真实 adapter authority、normalized call ID、交互式 wait→continue 断言或并行生成基线问题；没有通过删门禁解决，也不把这些夹具错误写成产品现场失败。

M/`pending-instructions-race.log` 还记录两项真实 pending 输入模型上下文回归通过；M2/`cleanup-service-final-race.log`、`frontend-final-build.log` 与 `protocol-check.log` 是对应候选实际输出。前端构建仍有大包告警，协议目录候选检查为 33 families / 1006 identifiers / 107 explicit entries；最终生产变更后的检查和产物身份见后文，不据文件名推定全树冻结。

## M 旧实例：自然继续、丢响应与停止后继续（真实 UI/API）

实例 Thread `thread-run-20260909030431-362393ab7647`，Run `run-20260909030431-362393ab7647`；源项目 commit `749408bde390c48e9390328175c8529ca94239b9`。日志 `fixture-notes.json`、`identities.json`、`provider-events.log` 和 `stage-m-*.json` 保存夹具及请求事实。

- `ui-10-fail.log`：发送按钮当时可用，真实 provider 故障返回 503/UNAVAILABLE，带持久收束后的 `turn_failed:true`。`ui-11-continue.log` 中用户直接发送“继续，服务已恢复。只读取已修改的当前文件，不重新应用编辑”，新 key 返回 202、同 Run/Session、消息 committed、无 successor；草稿清空且 unknown 提示为 0。不能把更早 `ui-07`/重载前资产行为混作新控件验收。
- `ui-12-disconnect.log`：浏览器在真实 API 已返回 202 后主动丢掉一次响应。客户端自动核对使用**相同 key 和相同 body**，返回相同消息 ID、`replayed:true`；没有要求用户另建 Run。返回的 `model_called/tool_called` 是原提交投影，不能误读为核对重新执行了一次模型或工具。
- `ui-13-stop.log`：真实停止入口返回 202/stopping；原请求随后 499/CANCELLED、`turn_failed:true`。`ui-14-after-stop.log`：在同一输入框直接发送停止后的继续要求，202、同 Run/Session、committed，草稿为空、unknown 为 0。停止不撤销此前已经应用的文件修改。
- `interim-continuity-assertions.json` 记录旧失败 key 仍失败、确认阶段新增模型调用为 0、当前文件仍为 `UX_M_EXPECTED_FIXED`、源与当前用户笔记保持、3 次继续请求带有失败上下文。这些是该实例的阶段事实；不代替 M2 最终升级后的身份与文件核验。

## M2 升级前基线：真实改动、PS7 检查与人工完成

M2 独立项目 source commit `d5bd126f64c9ecc0159fac952720eb49a743b786`，Thread `thread-run-20260909035036-3d173e2cc2d3`，Run `run-20260909035036-3d173e2cc2d3`，物理 Drydock `drydock-56127182b1621e29a8c745de1f83232d`。这段先建立真实已完成修改/检查/人工记录，供最终版本升级和换模型延续核对；**不是换模型后的验收结果**。

- `ui-07-propose.log`、`ui-08-approve.log`：Web 提出并审阅批准 `edit-bade81e496069501450ff9974801d0ca`，精确改 `review.txt` 的 `UX_M_EXPECTED` 为 `UX_M_EXPECTED_FIXED`。批准响应明确 `file_written:false`，不把批准当成应用。
- `ui-09-apply.log`：通过对话要求应用已批准提案，真实 workspace_apply 后消息 committed。`ui-10-command-before.log`：对话要求运行现有 `& ./check.ps1`，真实 Local Windows LPAC Job `command-job-ad4e10e218febc004f05fb4d` completed / exit0 / tree_reaped，使用受控 PS7 `pwsh.exe`。Job 的 WorkspaceID 保留 source 控制身份，工作目录 SHA256 为 `a35b38d4d7fc86b1fc6472e1e72366705d37a922680353d13e57ebbfec21c857`；不将控制身份误认为命令写在 source。
- `ui-11-report-before.log`：Web 生成 `standard-code-delivery-a4f8a3d766f8b4c73e6612530bcde92f`，passed、verified=true，实际 Job verification 的 current_revision=true；收据 `e0b65641e7fc2805b7be94cc075020e99c735fef1919a6c41064d16369deebfc`。`before-upgrade-report.json` 的只读 GET 仍呈现相同报告及当前版本结论。sealed receipt 与后续动态 observation 分开，不承诺换配置后它仍可作为新执行的通过证据。
- 报告诚实列出两个文件：`review.txt` 和 PowerShell `StartupProfileData-NonInteractive` 缓存。没有删除缓存再冒用原检查，也未声称只修改一个文件；运行缓存与用户交付范围的分离仍是未完成项。
- `ui-12-start-plan.log`、`ui-13-record-review.log`：开始计划项并填写六项基于本次证据的人工说明，得到 `delivery-checkpoint-20260909042459-8d3846601c48`，绑定 WorkItem v2，原 handoff `note-20260909042459-75b4ec726d83`，gate_ready=true。它是受控验收操作填写的人工说明，不等于用户逐字确认，也不冒充自动检查。
- `ui-14-complete-plan.log` 的脚本在完成动作之后等待错误的“刷新报告”按钮名称而超时；该日志自身不能证明完成返回。后续 `ui-14-complete-readback.yaml` 与 `before-upgrade-state.json` 独立确认 `work-20260909041709-fb33999f7313` 为 completed/v3，计划完成与原 v2 人工记录分开。
- `capture_state.py` 保存的 `before-upgrade-state.json` 显示源四个文件哈希仍与初值相同（source_unchanged=true），当前 `review.txt` 已变更，当前/源用户笔记、check.ps1、README 的字节哈希相同，执行 lease 已 released。最终升级后应与这份基线比较，不能只对新状态自洽检查。

这段旧构建的 `ui-08`/`ui-10` 操作仍显式点击过“恢复任务”。因此它证明原有审批、应用、真实命令和人工完成事实，**不证明最终版本的所有暂停路径都已做到自然发送**；新的暂停发送已有组件回归，最终现场复验留给下一节。

## M2 最终升级与持续对话（真实 UI/API）

M2 通过正常 API 启动从 v153 升级到最终 v154/v155；未改旧迁移账本。第二模型通过实际配置 API 和本地协议 qualification 后，由 Web 模型选择器选择 `ux-scripted-model-next`。本段继续使用升级前相同 Thread、source 和物理 Drydock。

1. `ui-17-fail.log`：先完成真实文件读取，再由 provider 返回 503；服务持久收束本轮，`turn_failed:true`，原输入与工具证据保留。`ui-18-continue.log`：直接发送继续要求，202/committed、同 Run/Session，读取已修改的 `UX_M_EXPECTED_FIXED`，不重做 Apply，没有未知请求残留。
2. `ui-20-select-and-continue.log`：在主界面换模型后普通发送，建立后继 `run-20260909044058-a6c056d976c1` / `sess-20260909044058-319cd601ffdc`。物理 `drydock-56127182b1621e29a8c745de1f83232d` 的原 creator、Session、path、workspace 均不改。源四文件与当前用户笔记字节不变，实际下一模型读取相同已改目录，并收到旧需求/失败上下文。
3. `ui-21-history-plan.log`、`ui-22-new-check.log`：Plan 所选方向及原人工完成可读，沿用项 `work-20260909044100-ff912045921a` 引用原 `work-20260909041709-fb33999f7313` / checkpoint / handoff。没有新造人工验收，也没有把旧自动检查复制成新通过。
4. `ui-22`、`ui-23`：无额外编辑，普通发送运行原 `& ./check.ps1`。实际 Windows Local Sandbox + PS7 Job `command-job-bb9005f3a0f6d867f311ce3e`，exit0、896ms、tree_reaped；当前报告只使用这个新 Job。Web 报告 `standard-code-delivery-f7a99d46dfe1fc2df54f348b0e7d385e` passed/verified/current，真实保存 review.txt 及两个 PS StartupProfileData 缓存变化。
5. `ui-24-finish.log`：普通发送完成要求，202/committed，既有完成门通过。Interactive Thread 仍将 requested Finish 投影为 effective Continue；Run 保持可继续状态，并非要求用户重开任务。门禁报告 `standard-code-delivery-fdde0f84e4625ccde8d89e101c0fd8b8`，最终只读复核仍 passed/verified/current，收据 `8d9d8baf3d5f5ad166fc53fafa7ef5721a195a333afc612b456c5a3a17b16348`。

模型回复中的大段原始工具 JSON 和“本机脚本化验收”文字来自明确的测试 provider，不能当作正式模型输出质量结论。真实工具/命令结论来自 API、SQLite、磁盘及报告，不从这段固定回复推断成功。

## 现场发现并修复的两个确认问题

`ui-25-disconnect.log` 暴露实际后继 Run 普通消息的重放错误：首次响应 `successor_created:false` 且没有 predecessor，确认响应却附带 predecessor，严格前端因此拒绝。`ui-26`/`ui-27` 只是继续确认这个旧 key，**没有实际停止或提交新消息**。后端现按创建请求的持久 digest/fingerprint 投影创建归属；新写入沿用既有 successor event，旧 event 保留首条消息兼容规则，无新 schema，也未放宽客户端校验。

原页不刷新，只重启 API 后 `ui-28` 确认原 key `v2-thread-turn-d55b162a-0430-41e8-b83a-cb620bc1ffcd` 成功。`confirmed-original-readback.json` 仍为原 steering `steer-20260909044924-c0d4756ecf93`，`confirm-provider-count.json` 为 28→28，新增模型调用 0。但该脚本发现 Composer 还残留旧错误：用户编辑新草稿后点击发送，实际先确认旧请求，错误却按新草稿指纹归属。

前端现由提交边界携带实际 Thread/Workspace/key 的内存错误身份，保留原 cause 与 typed failed/stopped 语义；确认仅清除对应错误。`ui-30-new-draft-confirmation.log` 在新构建真实重演：第一次及自动确认丢响应→编写新草稿→发送时原 key 确认再失败→按钮原 key 确认成功。五次 HTTP 都是同 key/body/steering；新草稿完整保留、unknown 卡及 Composer 错误消失，没有新消息或工具重复执行。

`ui-31-auto-confirm-final.log`：另一次只丢首个实际响应，系统自动一次确认，相同 key/body/steering；客户端真正完成解析后草稿为空、unknown/error 为 0，不能仅凭 HTTP202 判定通过。`ui-32-stop-final.log`：实际停止202/stopping，原轮499/CANCELLED/turn_failed；界面中文说明本轮已停止。`ui-33-after-stop-final.log`：直接发送新要求，202/committed、同当前 Run/Session，读取当前正确文件，草稿清空、无未知残留。最终原 stopped key 仍499、旧 failed key 仍503，确认均不调用模型。

## 最后自动回归、恢复和删除边界

| 检查 | 实际结果及原始证据 |
| --- | --- |
| 后继创建与普通消息 HTTP 重放 | `go test -race ./internal/application ./internal/httpapi '-run=^Test(ThreadTurn\|ThreadMessageCreationAttributionSurvivesAnotherConnectionEnqueueingFirst\|ThreadHTTPContractContinuesTerminalRun)' -count=1`；14.449s / 4.125s，通过。M/backend/`thread-successor-replay-final-race.log`，新/旧 event、独立连接、A创建但B先入队、同key改intent拒绝。修前真实HTTP红日志与首次token夹具错误分别保留。 |
| 新草稿确认错误归属 | Conversation recovery / Conversation / Composer 三文件33项通过11.76s，typecheck exit0；M2/`composer-confirmation-final.log`、`composer-confirmation-typecheck.log`。修前同页回归失败日志保留；另一次旧英文断言失败不冒称产品协议失败。最后仅调整审阅/Plan两条准确文案，已有Plan单文件5项通过11.13s，`plan-final-copy-check.log`。 |
| 历史检查点恢复 | M/native/`restore-continuation-final.log`，37.121s，通过；真实恢复后、receipt前注入中断，重开 store、两个启动 reconciler 保留未决，原 key 确认。当前执行 before/after 归属正确，历史 target/cursor不重写；source保持，历史checkpoint仍可实际fork。此组也包含删除后重开确认。 |
| 旧恢复兼容 | M/native/`restore-existing-final.log`，四项67.719s通过，真实 lifecycle、committed descendant rewind、独立fork、interrupted checkpoint；workspacecheckpoint library定向6.480s通过。M/backend/`stable-restore-and-fork-admission-final-race.log` 35.312s、`stable-source-fork-regressions-race.log` 6.191s、`drydock-cli-restore-wiring-race.log` 23.389s分别通过。CLI沿用原权限开关，无隐式授权。 |
| 最终schema与生成基线 | M/native/`binding-schema-baseline-final.log` 48.680s通过，包含v153/v154/v155及旧V41升级/clean baseline；实际M2正常启动账本为155。此前候选日志保留但不混作最后迁移版本。 |
| 清理真实删除结果及并发 | M2/`cleanup-dirty-outcome-final-race.log`，`go test -race ./internal/application -run '^TestDrydockCleanup(DirtyObservation\|ConfirmsAbsence\|UnconfirmedFailure\|ConcurrentSameKey)' -count=1 -v` 四项59.923s通过。真实删除后附带error仍准确确认缺失；未知结果保prepared；两个连接同key读取canonical receipt；另一caller的陈旧脏观察不能抢先封Preserved。最后case明确Lstat目录确已不存在。 |
| 原清理兼容 | 删除后重开、non-force/branch保护已通过；原exact-absent摘要恢复后单项race12.362s通过，M2/`cleanup-existing-absent-final-race.log`。原组合命令5PASS/1文案FAIL不能宣称全通过。旧日志误被后来同名写入覆盖，已从原执行session保留stdout恢复，见下方证据说明；最新四项通过只引用独立新名。 |
| 接口、协议和构建 | OpenAPI匹配/路由/确定性检查1.248s通过，`openapi-shipping-check.log`；协议33家族/1006标识/107显式项同步，`protocol-shipping-check.log`。最终Go build成功；最终前端build含tsc成功，Vite1.75s，`frontend-final-copy-build.log`；大包警告仍存在。Git diff --check通过。 |

删除 reservation 内，目录脏、绑定变化、Plan/Execute错误都可能与另一个同key删除交错。现在只有精确缺失或可证实的终结结果才关闭reservation；不确定则保留原key待核对，不凭超时释放，也不捏造after内容快照。实际失败证据 `cleanup-outcome-before.log`、`cleanup-dirty-concurrent-before-actual.log` 保留。后者修前真实Git删除已发生、目录不存在，却得到Preserved；更早两个fixture时序被Git预检拦住，不能写成真实删除。

证据保存纠正：后来一次测试误覆盖同名 `cleanup-outcome-final-race.log`。旧组合命令的完整16行stdout随后从原执行session15336取回，保存为 `cleanup-outcome-initial-race-reconstructed.log`，来源及原命令见 `cleanup-outcome-initial-race-provenance.json`：5PASS/1文案FAIL、75.646s、exit1。它不是原日志文件字节备份，也没有重跑冒充原记录。旧路径现为纠错索引；最终四项通过的独立原始输出为 `cleanup-dirty-outcome-final-race.log`。

## 最终可见状态、产物与清理

`ui-39-shipping-review.log` 实际加载 `index-BzUfg9LT.js`，报告通过、人工计划来源可读。1440×960、390×844及390×480报告结论可见；390×480收起侧栏后发送按钮可达。`ui-34` 的首次窄屏断言遗漏了仍打开的侧栏；`ui-35` 证实遮挡者是侧栏backdrop，按钮本身在窗口内。`ui-36` 点击backdrop中心时被侧栏项目遮挡，是脚本定位失败；改用真实顶部“隐藏侧栏”完成，未凭此改动产品布局。最终截图 `20-final-report-wide.png`、`20-final-report-390-480.png`、`21-final-composer-390-480.png` 已目视核对。最后两处文案明确“发送消息即可继续”及“当前检查以本次验证结果为准”。

`final-continuation-audit.json` 与 `shipping-state.json` 最终核对：同Thread/同物理目录；source与当前四文件摘要保持；旧Job/人工项/原报告不可变字段未改；当前新报告只引用新Job；原失败/停止key不重跑；没有pending输入/activelease；SQLite foreign_key_check为空、schema155。ignored依赖、cache及5MiB文件连续性是前面的真实Git/SQLite专项证据，M2没有额外塞入这些文件来冒称普通大型项目交付通过。

`final-build-identities.json` 保存最终API/JS/CSS、schema153—155及本地修改源文件SHA256：API `cyberagent-ux-phase-m2-release.exe` = `48f38c972cf1c4f44fb392c9e37938cfd447d787a25592258e05a115af129498`；JS = `dd0235d44393a4e41446426af5a07c2a05ba04bdcf3e6763566707f52c1f9e5e`。没有发布安装包。2026-09-09 13:15:04 +08，API58280、provider82948及专用browser/daemon80728均已关闭；18867/18868无监听、无本轮进程残留，证据文件保留，见 `cleanup-final.json`。

尚未完成：运行缓存与用户交付范围分离、输出正文连续审阅、暂停/终态逆向操作的完整UI范围、其他审批、原生/DPI/键盘矩阵、真实LSP、其余旧Host/CLI handoff、跨应用重启草稿/未知key持久化、全文搜索/性能，以及强制三方案和人工六字段对小任务的负担。未运行全库Go、全部前端或全原生矩阵，也没有付费真实模型对比。本轮证明受测路径的持续对话语义，不宣称与Codex全面等价或F01—F14全部收敛。
