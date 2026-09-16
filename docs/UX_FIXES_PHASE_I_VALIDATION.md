# 第九批：文件、检查和隔离工作区一致性

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-09，文件目标统一、恢复归因及失败报告路径已完成限定验收。真实检查命令在 Windows PowerShell 5 的 LPAC 启动阶段失败，成功“修改—检查—交付”旅程仍未验收；全部 UX 收敛未完成。

产品工作树：`<workspace>`，分支
`codex/ux-journey-convergence`，基于 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。
A–I 均为本地未提交修改；原工作目录只同步工作必读。没有产品提交、推送或发布。

## 所选标准与实际修复

1. 文件读取、提案、批准、应用、逆向修改和对应检查点使用当前 Run 的精确 Drydock；命令和报告使用同一实际目录。保留 Mission/Session/Thread/Supervisor 的来源控制身份。
2. 已配置或拥有 Drydock 的 Run 在缺失、不可用、绑定漂移时拒绝新操作，不回退到来源。普通来源任务继续工作。不同 Run 不能因共享来源项目而串目录。
3. 旧来源提案保留原身份，只读审阅，不能重解释为隔离目录批准。已完成 Apply 重放不重新读取或清理文件。
4. 所有文件修改复用已有快照、事务和 Drydock 生命周期；来源恢复游标保持。先完成检查点归因，再记录 Apply 成功。中断保留不完整状态，重试不吞游标错误；后来的外部文件不能归入旧快照。
5. v153 仅更新精确 Apply INSERT 守卫；保留原审批、事件、内容哈希和迁移校验。历史查询先按 Run/来源/自身 Drydock 筛选，再分页。
6. UI 区分当前目录、历史原目录与来源项目差异。未应用提案、已批准、已应用分别显示，不能把两个审计事件称为修改了两个文件。

架构取舍与成本见 [ADR0155](adr/0155-run-owned-file-workspace-and-checkpoint-attribution.md)。未新增依赖或第二套快照表。新增模型 Host 提案对已配置/拥有 Drydock 的任务关闭；既有 Host 审批执行链没有因此迁移。

## 实际发现、失败与修正

- H 的来源 CRLF 与 Drydock LF 导致原哈希冲突；I 原实现真实测试先失败，确认目标读错。新读取与新提案改为精确 Drydock。
- 旧私有 Drydock checkpoint 服务曾将 CommandBatch 事务记入普通游标。新恢复最初仅接受 FileTool，旧兼容测试失败 9.246s。已增加精确事务工作区及 before/expected 游标匹配的旧路径，不能把来源游标送入新 Drydock 游标。
- 已持久化 after 快照但游标失败后，如果外部文件变化，旧实现会将新树归因到旧快照。新实现复用非持久 Capture 对比 root/path/commit/branch/index/manifest，并确认 Git 观察一致；差异拒绝归因。
- 终态边界重放原来吞掉游标补偿错误，before 游标失败还可能留下 After=Before 的失败边界。仅 owned FileEdit 服务启用严格重放：准备失败保持 Prepared，失败/中断边界拒绝授权新写，Completed 通过原生命周期收据补游标并传播失败。
- 首个真实 I Run 已应用文件，但 Supervisor 拒绝继续命令：检查点 AttemptID 为空，标记无法归因。真实持久调用编号与网关计费 invocation 不同；文件工具原来传错编号。修复以真实 Supervisor call 查询所属 Attempt，不放宽监督器校验、不人为重置停止记录。
- 浏览器在未写入的提案阶段显示“修改了 2 项”：原因是把 tool 和 file 审计事件数称为文件修改数。新时间线保留审计叶子，按真实 file_change 状态说明提案/批准/应用。

## 独立真实现场

`output/playwright/ux-phase-i/` 保存数据库、API、确定性本机响应服务、脚本和日志。
数据库由 H 只读备份，只复用合格的本机模型，**不是空实例导入测试**。另外建立独立 Git 项目，夹具提交 `206f0d6` 不属于产品提交。提交后模拟用户的来源修改：`review.txt` 为 `UX_I_SOURCE_USER_CHANGE\r\n`，另有来源专属笔记；Drydock 的原内容为 `UX_I_EXPECTED\n`。

首个 Run `run-20260908155731-f88f9ab91661`，来源
`ws-import-5fee966a88a76fcb3b7c9ecb`，Drydock Workspace
`drydock-ws-698cedbd47ff21ffb95a5d901007097b`。通过真实 API 建立 Thread，后续配置、Plan 选择和轮次均走 Web UI。

- UI 已完成信任、配置、两个真实 workspace_read、Plan 提案、选择方向 1、进入 Deliver。
- 真实读取和 proposal `edit-b983f59c9fca594d21864142dc54d8ea` 指向 Drydock。UI 批准返回 `file_written=false`，实际两个目录均未写。
- 验收脚本第一次 workspace_apply 漏传 expected_action/原文和目标哈希，被协议修复拒绝，未执行工具。补齐真实提案参数后，产品返回 `file_written=true`，Drydock 内容变为 `UX_I_EXPECTED_FIXED\n`，来源模拟修改保持。
- 后继命令因上述 Attempt 归因缺口被拒绝；没有 Job，不能写“命令通过”。此停止记录保留，修复后另建以下同来源 Run 复验。

### 最终构建下的同来源新 Run

`run-20260908162356-b3bdc32e1bfd` / `thread-run-20260908162356-b3bdc32e1bfd`，
目标 `drydock-ws-45eb74fad5f1af1bb6ebde01d200d45f`，身份见 `identities-final.json`。
API 使用 `cyberagent-ux-phase-i-final.exe`，页面重载后使用最终 `index-_eLZDFga.js`。

1. 真实 UI 完成配置、模型读取和 Plan 提案、选择方向、进入 Deliver、读取与精确文件提案。提案 `edit-7a60710c63e5a8f1d98f8dda25508e49` 的审阅批准返回 HTTP202 / `file_written=false`；目标此时未写入。390×480 批准按钮实际边界为 x145.67/y313.63/95.33×34，截图 `final-05-review-narrow.png`。提案时间线显示等待审阅，不再称已修改两个文件。
2. 后继模型真正调用 workspace_apply，Drydock 的 `review.txt` 成为 `UX_I_EXPECTED_FIXED\n`。检查点归入真实 Attempt `attempt-20260908162837-ead7fbc9f343`，Supervisor `mutation_epoch=1` 且此时没有停止原因，见 `final-applied-supervisor.json`。同来源两 Run 各有独立目标；来源用户修改与笔记保留，见 `two-runs-isolation.json` 及最终持久核验。
3. 真实 `command_runtime` 建立 Job `command-job-23c6a59bdd9ba6f4f959090e`。先前夹具漏设 closed stdin 的 `close_initial_stdin=true`，被协议修复拒绝；纠正后实际 Job 使用 `local_windows_lpac`、禁网络、无凭证，在创建时加入 Job Object，最终进程树已清理。
4. Windows PowerShell 5 在 `System.Management.Automation.Tracing.PSEtwLog` 初始化时退出 `4294901760`（`0xffff0000`），约235ms，0 stdout；**检查脚本未执行**，不能称项目断言失败或通过。stderr 保留实际脱敏输出及乱码，未据此猜测具体 ACL/Win32 错误。`final-command-closed-stdin.log`、`final-jobs.json` 和 `powershell-lpac-diagnosis.md` 保存边界。
5. UI 生成真实报告 `standard-code-delivery-1ec421d612ec0a586b96f6199fb22970`，HTTP200、`failed/verified=false`，该失败检查 `current_revision=true`，受影响文件1个。报告收据 `424a84f02f4458dafb29bd6dceca5b6cf9c50d04cda98cfb0763c05134c8fc9a`；最终检查点 `wcp-50b42634b12fa3a1a311f137b6fcfaeb`。当前版本标记不等于检查通过。截图 `final-09-failed-report-wide.png`、`final-10-failed-report-narrow.png`、`final-11-failed-job-narrow.png` 已目视核验；390×480 刷新及输出记录入口可达。`final-failed-report.log` 是真实 POST 响应，`final-failed-report-ui-final.log` 是最终 UI 断言。

**纠正命令身份判断：** Job 的 `workspace_id` 保留来源控制身份，真实执行目录另由
`workspace_root_sha256` 绑定。实际 Go 规范路径函数算出的 Drydock SHA
`986de443be53823e87924ed81e068ee741ac645be8c17e142231c4942325c145`
与 Job 相等；source SHA 为 `784f6bdb7f5d1468891448472c9bcd741a4ce9c05c23023186971e87ad841d8d`。
源码进一步核对 Windows cwd 映射到 Binding.DrydockRoot。不能要求所有对象的 WorkspaceID
相同。H 引用的 producte2e collector 原来也混淆这一点：本批只修正验收条件为来源控制身份
与 exact Drydock root SHA，并保留命令/输出/sandbox 条件。其输入夹具回归不是新的 OS 命令成功证明。

现有 PS5/PS7 host smoke 不能替代 LPAC 兼容验收；真实 LPAC 测试覆盖 Go/probe，未覆盖
当前 PowerShell 启动。仅发现 Codex 私有缓存 PS7，产品可信解析器按 ProgramFiles PS7 → 系统 PS5
选取，不读取任意 PATH。没有更改 ACL、硬编码缓存路径、启用 Full Access 或人为改写失败 Run。

最终只读核验 `verify_persistence.py` 的21项事实断言通过；`verified-persistence.json` / `.log`
保存真实数据库、字节及实时 GET（2026-09-08T16:50:08Z）的证据。Run 为 paused，Supervisor
为 diagnose，mutation_epoch=1 / verified=0，失败 Job 引用保留。两个 I Run 均没有来源
checkpoint 记录；最后普通游标是既有报告 Capture 的 Drydock 检查点。因此浏览器现场不能独立
证明“已经存在的来源游标保持”，这部分由真实 SQLite/Git 定向回归覆盖，不能混称同一证据。
核验脚本首轮使用错误的 Apply 状态名与 GET 包络，修正后的最终断言通过，初始失败日志保留。

另发现尚未修的失败文案：事件 seq245 是 `workspace.checkpoint_transaction_failed`，kind
为 `command_batch`，before/after 快照都存在，且该 Run 没有 Undo/Rewind/Fork。现有
`internal/runactivity/activity.go:229` 却统一命名“工作区恢复失败”。后续应按事务类型说明
命令或文件操作失败，不能把本次事实写成实际执行过恢复，或断言快照捕获失败。

本机响应服务只编排工具调用，不能作为真实模型质量、品牌排名或真实 Job 的替代。API 仅本机监听，启用已有 workspace sandbox gates，未启用宿主 Full Access、工具网络或凭证注入。

## 已完成检查

以下是当时明确范围的结果，不按测试数量相加计算覆盖率。后续生产变更涉及的范围须重新定向验证。

| 范围 | 结果与证据 |
| --- | --- |
| 前端全量、scope | 101 文件/632 项，26.08s；`frontend-tests-final.log`。scope 4 文件/43 项5.03s，typecheck通过 |
| 时间线尾部修复 | 2文件/30项3.60s；`frontend-edit-narrative-final.log`、typecheck通过 |
| 前端生产构建 | 首候选 `index-DyZuR_29.js`，尾部 `index-_eLZDFga.js` 1850.93kB/gzip486.74kB；既有chunk警告保留 |
| HTTP/application 文件投影 race | 33.423s /104.397s，`http-file-projection-race.log` |
| FileEdit 实际读写/审批/逆向/结果保存失败重试 race | 95.972s，`file-edit-target-race.log` |
| 新旧恢复与外部修改归因、实际完整 FileEdit 链 | 5项136.945s，`drydock-file-boundary-attribution-validation.log` |
| 严格 before/after cursor 同 key 故障重试 race | 89.548s，`file-edit-cursor-retry.log`；持续失败不记录Apply成功，释放失败后不重复文件写入 |
| v153 升级、旧记录、错误身份、分页及相关历史迁移/基线 race | 49.911s，`store-v153-migration-race.log`。缺失目标指workspace ID缺失，不冒称物理目录消失验证 |
| Agent Code/CodeIntel/Host/CLI 定向 race | application76.693s/toolgateway1.468s/app1.900s；精确命令为 `backend-validation-results.md` 的工具输出转录 |
| 最终持久 Supervisor call → Apply → Attempt 归因 race | application54.652s/toolgateway1.476s；`backend-validation-results.md` 为工具输出转录，不冒称原始日志 |
| producte2e 控制身份/物理目录及既有约束 | 12子例修前失败0.523s，修后通过0.481s；`collector-contract-before.log` / `collector-contract-after.log` 为原始输出。只验证collector，不是完整产品E2E或LPAC成功 |
| Desktop 安全 tag 构造 | 0.374s，`desktop-file-workspace-wiring.log`；不是原生窗口实测 |
| OpenAPI | 定向1.217s，`openapi-tests.log`；生成TS前后SHA一致 `32432170CDE50CCF7ECE4B70897FC41252CF6D708515644F183ECBBAFD4D9968` |
| 协议登记 | 33家族/1005标识/107显式测试，`protocol-registry-final.log`；新增cursor指纹已登记 |

最终 API 二进制 SHA256 `E83E9FBC4D68125ACAF985CD442E7E17213427F292B948FCDEE8BB58A887FB64`，
前端 JS SHA256 `2E3D818A2F76FCF6B29EED96740B1DCBC4AEB666CA90E28DAEEB1A97663BB770`，
详见 `final-build-identities.json`。collector 最后修复只影响独立验收程序，未改变 API 运行路径，
不把较早 API 二进制说成含有较晚 collector 改动。最终 `git diff --check` 通过。

首个连接脚本因按钮副标题而 exact 名称超时，另有一次 CLI 选择器转义错误和旧 ref 错误，均不作通过记录。最后报告截图脚本两次大小写断言失败：CSS 将结论渲染为大写，改为不区分大小写后通过，前两份日志保留。无交付报告时的404与 API 重启时断连保留，不伪称控制台零错误。未运行完整 Go 套件；第一批全量 Go 退出1的历史记录不改写。

## 未完成与交接

下一优先是 **Windows 可信 Shell 在相同 LPAC/审计边界的可用性诊断与反馈**。需要保留启动失败和脚本退出失败的区别，验证具体 Shell 后再承诺支持；更换版本或新增可用性探测本身不是通过证据。该失败仍阻断本机成功命令交付链，不能将本批限定文件/失败路径验收写成整条旅程通过。

完整 F01–F14 未完成。独立 Plan DeliveryCheckpoint 写入仍 CLI-only，本轮没有代填该 gate 或执行 finish。原生窗口矩阵、所有审批类型、暂停/终态逆向、任意命令副作用恢复、全文搜索与性能仍有边界。CodeIntel 为源码/定向回归，无真实 LSP 会话；旧独立 Host 审批执行与 CLI handoff 的来源变更汇总没有全部转换。真实最终应用轮次耗时25.957s，包含模型与多轮 Git/快照观察，尚未拆分或优化，不能把正确性修复描述成性能提升。

本批服务已关闭：Playwright 专用会话 `uxphasei`（daemon PID60112），API64828、启动shell66752、
provider57424；18867/18868 无监听，其他服务未动。`cleanup-final.json` 核对时间为
2026-09-09 00:51:48 +08。旧会话不再等待；续验先核对端口与精确进程，再用本批 serve.ps1
和夹具恢复。保留两个 I Run 的真实失败历史，不重置来制造成功。
