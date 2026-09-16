# Traverse Board：真实 Agent 工程验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-10 18:00，Asia/Hong_Kong。**本次限定的真实工程旅程已完成，整体 UX 收敛仍未完成。** 产品最终检查 11/11、外层独立黑盒检查 13/13 通过；交付后原对话真实只读续聊完成。旧失败、人工干预和历史收据缺口继续保留，不沿用“企业级”“98%/99%”估计。

## 范围与实际版本

用户已批准独立目录 [Traverse-Board-Agent-Acceptance](<isolated-acceptance-workspace>)。目标是在产品中用真实模型创建一个 Node.js JSONL 日志汇总 CLI，使用成熟的 Zod 库，完成检索、受审联网安装、文件提案与应用、失败测试、修复、交付和原对话继续。外层助手及子 Agent 没有代写或复制目标工程，也没有预装项目依赖；独立黑盒输入及结果保存在验收证据目录。

产品修改位于 `<workspace>`，分支 `codex/ux-journey-convergence`，HEAD `15c399c8940aeb81d3c45bb0de96759b480d02c2`，包含未提交的 A–P 和本次修复。原工作树仅同步工作必读，未推送、合并或发布。隔离产品数据库是 `output/playwright/ux-real-journey/home/cyberagent.db`，没有手工改写数据库或触碰用户真实产品数据库。

最终 API 为纯生产 `cyberagent-real-empty-recovery.exe`，SHA-256 `9d7d7efff2419f08998786d8bb5c34db304e00f9917ff70527a14b131f81e602`；Web 主资源 `index-BAaexJ-u.js`。最终核对时 PID 143168、127.0.0.1:18868 保持可查看，没有活跃模型或工具执行。Node 24.14.0、PowerShell 7.6.5 来自只读 Windows 文件版本资源核对，见 `runtime-file-version-evidence.json`。

工程采用普通 Code、Local、逐次审批，直接写入指定源目录，没有用隐藏 Drydock 或 Standard Code 代替。工程 Thread 为 `thread-run-20260910040145-8ee426d316ec`，保留三个 Run 的前后继关系：

| Run | 作用与实际路由 |
|---|---|
| `run-20260910040145-8ee426d316ec` | 初始失败及网页读取历史 |
| `run-20260910064350-a4f973a7ede3` | `official-deepseek/deepseek-v4-pro`，Responses；实际安装、主要代码、测试和两处修复；模型切换后正常 cancelled |
| `run-20260910092949-39b72da84282` | `deepseek/deepseek-v4-flash`，Anthropic；独立 README 审批应用、最终检查、交付及只读续聊 |

原生搜索在单独的真实对照 Thread `thread-run-20260910042852-35c694ee6aec` 验证，工程对话也保留实际网页读取记录。不能将两条对话包装成一次无人干预的单模型任务。

下列短文件名均相对于 [验收证据目录]（本地保留证据，未公开：`output/playwright/ux-real-journey`）。

## 验收结果

| 环节 | 真实结果 | 证据 |
|---|---|---|
| 原生搜索及引用 | Pro 实际搜索返回 Zod/Node 公开来源，后续普通消息使用保存来源；原搜索失败保留。来源为供应商提供的搜索依据，引用标记 locally_verified=false，不等同本地逐页核验 | `evidence-search-after-tail-success.json`、`pro-search-links-after-ui.yml` / `.png` |
| 工具参数与连续执行 | 输出额度修正后实际 wire 为 4096 tokens，四次 4308–5822B 的完整文件参数到达工具层，均为同一测试路径的尝试；同一输入跨 4+4+1 次工具调用，无重复用户消息 | `output-budget-actual-validation.md` |
| 文件落盘 | Agent 经提案、审阅、实际 Apply 创建 package、源码、测试、README 和两个例子；提案批准本身不计作已写入 | `apply-approved-package-1.json.json`、`tests-real-applied-detail-1.json`、`implementation-source-detail-1.json` |
| 实际联网安装 | 受审 npm 命令 exit 0，真实 registry 下载并安装 Zod 3.23.8；下载包 SHA-512、lockfile 与安装包对应，package.json 保持 | `dependency-install-result-1.json`、`dependency-install-actual-validation.md` |
| 先失败再实现 | 初始实现不存在，真实测试 exit 1、9 项全失败；实际实现后原 9 项全通过 | `red-test-existing-host-result-1.json`、`implementation-full-test-run-1.json` |
| 实际例子 | 中文输入 exit 0、total=3、各等级 1、duration_ms=35，输出文件与 stdout 字节相同；无效输入 exit 1、stdout 空、stderr 指出第 2 行 | `example-chinese-run-1.json`、`example-invalid-run-1.json`、`backend/example-chinese-output-readonly-compare.json` |
| 独立发现缺陷 | 首次外层黑盒检查 11/13；发现输出写失败仍打印成功 JSON、累计数值溢出后成功输出 null 两个缺陷 | `engineering-oracle-result-1.json`，保留原失败 |
| Agent 修复与回归 | 两处源码修复实际应用；原测试前 158 行 SHA 不变，新增两项回归；准确 README 由新 Session 独立提案、审阅并应用 | `oracle-tests-applied-5.json`、`regression-original-tests-readonly-check.json`、`ui-flash-readme-schema-correction-11.log` |
| 最终产品检查 | 精确受审 Host 单次执行，exit 0，11 tests / 11 pass / 0 fail / 0 skip / 0 cancel；stdout 792B 完整、stderr 空、进程树回收 | `flash-final-eleven-tests-result-11.json`、`backend/final-host-eleven-binding-check.json` |
| 独立复验 | 13/13 通过；两个原失败场景均非零退出、stdout 空；执行前后源码 SHA 一致 | `engineering-oracle-result-2.json` |
| 主界面审阅与交付 | 正常 Finish；审阅改动→检查与交付→代码交接→查看已保存输出可读取最终 11 项统计；Handoff 和 JSON/Markdown 导出 HTTP 200 | `final-finish-lifecycle-12.json`、`final-code-handoff-12.md`、`ui-final-handoff-saved-output-12.yml` / `.png` |
| 原对话继续 | 普通发送进入同 Run 的 Turn 6，实际完成三个 workspace_read，随后结束本轮并恢复输入；没有新增文件编辑、安装、Host 提案或执行 | `ui-final-read-request-13.log`、`final-read-durable-tools-13.json`、`ui-final-read-result-13.yml` / `.png` |
| 续聊前后审计 | 8 个受核文件 SHA 全部保持；所列历史表旧行无丢失或改变；Thread 三个 Run 绑定不变；新增工具恰好三个只读调用 | `backend/finish-baseline.json`、`backend/after-read.json` |

产品内最终测试为 `& 'D:/Node/node.exe' --test`，工作目录是验收项目，超时 120000ms。精确提案 `host-command-proposal-a280f4320625a3d01b9ac50b`、结果 `host-command-result-20ffeb727a9fe1b891e677d2`、收据 `host-exec-1174a53a056b47d7990baf8f` 相互绑定，执行于 09:40:36–09:40:37 UTC。累计六次真实 Host 执行的审批、意图、结果和收据均匹配；其中初次红测和无效输入例子按真实非零结果保留，不能写成“六次全成功”。

独立黑盒复验于 09:41:09 UTC 执行，覆盖中文、CRLF、空行、空文件、行号、字段类型与取值、输出文件一致性、写入失败和累计溢出。它是外层验收证据，**不是产品 Agent 自己执行了 13 项测试**。首轮红测 stdout 18305B 完整捕获，详情展示的 16 KiB 上限是另一层限制，不能混称输出被完整展示。

最终关键文件：

| 文件 | SHA-256 |
|---|---|
| [源码](<isolated-acceptance-workspace>/src/log-summary.mjs) | `571a96c5a35b9d7f7774eb1f5db5e1a9c27c694764d03f62a5a71c133653269e` |
| [测试](<isolated-acceptance-workspace>/test/log-summary.test.mjs) | `eddb207e87000617b7e8c861da7cbc4bc220bdd67bdce731de42c86a8d4e811d` |
| [使用说明](<isolated-acceptance-workspace>/README.md) | `2cd1461a26db6b72906fb2df16743ef45b20c95c08a71e424bee4756e734a1ae` |

## 产品修复与检查边界

1. **文件参数被截断：** 原工具请求继承过短的普通文本默认输出值；无显式限制时改用已有保守最大输出预算 4096 tokens，保留用户预算和输入预留。这不是供应商的真实最大输出能力。
2. **四轮工具边界被当成结束：** 仅在后端确认的合法工具分段边界承接同一输入，保持停止、预算、审批和消息唯一性。
3. **普通新目录无法创建：** 提案仍只读；批准应用后才建立缺失父目录，仍禁止覆盖并发目标。失败可能留下新空目录，不按路径自动删除父目录。
4. **合法空文件阻断后续写入：** npm 缓存生成零字节文件暴露快照把空内容误当未保存；修正零字节语义，没有借改忽略规则绕过检查。
5. **拒绝或边界前失败遗留 pending：** 只封存能证明未进入写入边界的失败；新消息先交模型，明确重试同提案时仍核当前权限、审批、租约和哈希。存在实际写入事务的不确定结果仍保持阻断，不能自动重放旧写入。
6. **审批及结果显示缺口：** 补创建文件的审批映射、普通 Code 的真实 Host 收据和保存输出入口；消除空技术存档冒充“交付物已记录”的占位，修正裸 URL 后中文标点边界。

验证记录分别见 `backend/tool-boundary-verification.md`、`backend/create-directories-validation.md`、`backend/capture-empty-validation.md`、`backend/host-proposal-rejection-verification.md`、`backend/file-apply-boundary-verification.md`、`backend/package-approval-preview-validation.md`、`backend/narrative-artifact-validation.md`、`backend/host-handoff-validation.md`。各自记录适用的定向、集成、race、构建和真实演练，不将不同版本的检查合成一次全仓 CI 通过。Windows junction 已测，依赖系统权限的 symlink 路径有 skip；完整平台矩阵未覆盖。

## 失败归因与人工干预

**验收驾驶方也犯了错。** 七条实际应用提示误用了 `expected_sha256`。真实 Apply 合同要求 version、edit_id、expected_action、expected_original_sha256、expected_proposed_sha256。部分模型自行遵守正确 schema 成功，部分先失败再格式修复；不能把 HTTP 202、消息 committed、model.failed、整轮失败和实际 Apply 成功混为一谈。Flash 第 3 轮是 payload invalid，第 4 轮纠正为真实五字段后才实际应用。被拒的全部原生参数未保存，不能声称已逐字证明模型照抄了错误字段。详见 [驾驶提示归因复核]（本地保留证据，未公开：`output/playwright/ux-real-journey/backend/driver-apply-schema-attribution.md`）。

同时，实际记录确有不完整函数参数、无效 patch、公开空白 message，以及格式修复后虚构“已应用”和不存在的提案 ID。Apply、Host 执行和测试结论始终以实际工具、磁盘哈希和收据为准。旧失败包括 `oracle-fix-model-failure-1.json`、`oracle-after-false-apply-2.json`、`examples-proposals-after-claim-1.json`。格式修复撤下工具的源码证据与首响应错误分别保留；不能概括为正常执行阶段始终没有工具。

第 24 轮实际公开文本 65B、去空白后为空，没有函数边界事件；第 30 轮仍有 68B 空白。原字符未保存，不猜编码组成，也没有证据证明聚合器吞掉有效调用。一次仅日志诊断保留公开结构、长度和参数哈希，不记录凭据或私有推理；该次 read/change 的最终响应、流累计及返回调用一致，**没有复现空白**，因此没有证明旧问题已定位或修复。诊断进程已停止，最终回到纯生产。记录见 `backend/turn-24-empty-response-analysis.md`、`backend/responses-structure-real-observation-1.md`。[DeepSeek JSON Output 官方说明](https://api-docs.deepseek.com/guides/json_mode/) 提及偶发空 content，但这只是背景，不能替代本次因果证据。

本次通过正常模型选择改用已有独立合格的 Anthropic Flash。Code/Deliver 和逐次审批配置保持，但实际 runtime/interaction 重置为 preview/noop、preview/untrusted，仍需正常设置重新配置 Local/controlled/trusted；不能按局部源码误称全部配置自动继承。旧 Pro 已批准未应用的 README `edit-3351cb977343e1ce6258a34e517fbc54` 保留历史，没有跨 Session 套用批准；新 Session 使用独立提案 `edit-6ea1dc57b5bd3f7f2c4cb9abe5cbe6f3`。切换通过不等于修复了 Pro Responses，也不是公平的供应商排名。

最终模型交付文字仍有两处不准确：旧输出写失败实际上已 exit 1，只是先打印成功 JSON，不能称“静默成功”；某次格式修复没有工具，不等于当前 Run 的正常工具集合没有 workspace_apply。本文作事实更正，未改写原模型历史。该旅程含明确审批、人工纠正和路由切换，**不属于无人干预的可靠完成证明**。

## 完成语义、终局审计与剩余事项

正常 Finish 后，交互式 Thread 的本轮结束，Run 可保持 running、checkpoint idle，以便普通发送继续。产品源码 `internal/store/supervisor.go` 对已准备的交互输入有该行为；本次实际第 5、6 轮均 requested_finish→continue，最后 Handoff stop_reason=turn_finish、无活跃执行。因此无需把整个 Run 强制改 completed，也没有使用“失败任务恢复入口”来做最终只读续聊。

09:47:34 UTC 基线与 09:56:14 UTC 后置快照证明：同 Thread、同三个 Run；24 个明确列出的历史表中 3056 条旧记录的完整行哈希保持，新增 61 条本轮记录，包括一条用户输入和一条回复、三个真实读取及对应事件/交接；8 个文件哈希保持。此审计不覆盖整个数据库所有表，也不覆盖 node_modules 和缓存的全部文件。当前 Flash 已静止、FK 检查无异常，所有 Run 的可执行 pending、Host intent、工作区事务、活跃终端和未过期活跃 lease 均为零。

**旧 Pro 严格 settled_observed 仍为 false。** 06:45:46 的 `run-handoff-20260910064546-f29971bd5df5` 在网页批准后确实执行过 fetch，但缺终局 Handoff result；同输入已 committed，后续工具已结清。09:29:49 模型配置切换使该 Run cancelled，仍保留 waiting/attempt_id 空的旧 checkpoint。它们是历史收据和终态投影缺口，不代表当前有进程在跑；没有删库、补造结果或改审计标准使其变绿。说明见 [历史缺口]（本地保留证据，未公开：`output/playwright/ux-real-journey/backend/finish-baseline-historical-gap.md`）。

后续按实际风险继续收敛：

- **失败后的可靠反馈和续接：** 复核错误工具参数、空白根响应、无工具格式修复后的虚报叙述；使用正确契约输入复现，不继续靠盲重发。
- **审批及配置连续性：** 补实际审批自动唤醒、嵌套网页审批终局收据、取消后 checkpoint 投影和切换模型时设置继承的验证与修复，保持现有授权边界。
- **交付信息完整性：** 普通 Code Handoff 的历史 Host 收据可读，但不是 Standard Code 的 passed/current_revision 证明；当前后继导出仅显示其自身修改和命令，不等于整个 Thread 的自动当前版本认证。最终 UI 仍有较多内部 ID 与术语，可继续简化。
- **完整 UX 和平台覆盖：** F01–F14 尚未全量收敛；原生入口、DPI/键盘、真实 LSP、其他 Host/CLI、跨应用重启草稿、未知请求、全文搜索及性能等按受测范围推进。
- **恢复边界：** 非 Git 工程含生成目录和缓存，快照修复不等于完整工作区可恢复，当前整体 recovery_level 仍 unavailable；本轮没有归因用户之前的 Windows 密钥弹窗。

本次工程产物、检查、交付和普通续聊已取得真实证据；这只关闭该限定旅程，不关闭上述产品缺口。
