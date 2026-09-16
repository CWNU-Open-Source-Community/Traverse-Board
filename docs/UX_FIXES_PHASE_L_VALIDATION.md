# Shell 修复与真实交付验证

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-09。工作树 `<workspace>`，分支 `codex/ux-journey-convergence`，基线 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。本轮遵循用户“Shell 仍未修好不能以阶段验收结束”的纠正，持续处理实际启动、文件读取、退出状态和交付链。A–L 是本地未提交改动，未推送/发布。

## 最终支持策略及实现

- Windows Local Sandbox 的 PowerShell profile 使用 PowerShell 7；宿主可用 `CYBERAGENT_POWERSHELL_PATH` 指定完整 `pwsh.exe`。显式错误配置不静默回退，模型不能通过命令环境选择运行时。既有 host profile 仍可使用 PS5。所有运行时均经过项目外路径、文件别名、PE 和指纹检查。
- Windows `windows_appcontainer_policy.v2` 为受信 PowerShell profile 增加 `lpacInstrumentation`。启动前精确校验实际 token：普通命令两项、PS7 三项能力及属性，无网络能力。结果与 owner v2 的事实及指纹绑定此选择，旧 owner v1 按原格式恢复；无新数据库表。
- 系统 PS5 的 ETW、COM 和相对路径问题均实际定位。诊断时加入 COM 可使其启动，但外部 `.ps1` 策略读取仍被拒绝。最终生产不保留这项额外 COM 权限，也不修改 ExecutionPolicy 或共享祖先 ACL。微软说明 LPAC 需要明确能力访问 COM 等系统资源：[官方机制说明](https://learn.microsoft.com/en-us/windows/win32/secauthz/implementing-an-appcontainer)。
- `NormalizeLocalSandboxCommandRuntimeSpec` 在写入 Job/审批重放指纹前构造真实规范 argv。使用 PowerShell 自带临时 PSDrive，以已验证工作区为根并保留相对 cwd；不靠读取宿主祖先目录元数据完成路径规范化。UTF16LE `EncodedCommand` 和 `OutputFormat Text` 保持多行/中文传输并遵守 Manifest 参数限制。
- 原内联 command body 在顶层执行，保留 `$?`、提前 return、显式 exit 和原生命令的退出行为。试验中发现的嵌套 ScriptBlock 假成功方案已撤销。声明和较长脚本应保存在项目 `.ps1` 中调用；4096 字节的编码后单参数边界未放宽，限制详见 [运行时配置说明](local-sandbox-windows.md)。

## 已有实测证据

原始记录均在 `output/playwright/ux-phase-l`；诊断与产品执行明确分开。

- `native/SECOND_BLOCKER_DIAGNOSIS.md`：最终生产 normalizer 直连真实 LPAC，原 I `check.ps1` exit0；两级中文/空格/单引号 cwd 与 `../` 及含 `using`/`param` 的项目脚本 exit0；中文错误保持 exit23；初始化故障 exit125 且后续脚本不执行。连同后续缓存对照共 24 次已启动诊断均回收进程树、恢复 ACL、删除 profile，无 owner 残留。
- `native/product-ps7-boundary.log`：源文件读写和工具链写入均被拒绝；TCP/UDP 10013 且宿主无连接/包；真实子进程启动后回收。诊断运行时副本及 I 历史文件哈希未改。
- `backend/instrumentation-only-final-race.log`：最终两/三能力策略、旧新 owner 恢复和相关边界 9 项 Windows race 回归通过；Linux 交叉编译通过。未调用测试的机器 ACL 修复入口。
- `bootstrap-production-helper-race.log`：7 类真实 PS7 固定脚本退出状态回归及系统 PS5 拒绝用例通过，无跳过。`bootstrap-host-final-comparison.json` 保存与原命令逐项对照，属于宿主语言语义测试。
- `application-final-race.log`：Local 请求能力与工作区编译、整批预检查、adapter 级别隔离相关回归通过（1.466s）。前端窄提示 26 项测试及类型检查/构建通过；旧大包告警仍存在。协议目录校验为 33 families / 1007 identifiers / 107 explicit entries。

## 正式产品链

独立 SQLite 备份 I，固定本机脚本化 provider 发出真实产品工具调用，不模拟文件写入、Job 或报告。模型能力和联网实验不在此证据范围。

- 新源项目 commit `faa85062a8e85436cd5c862bca73b1ce53896790`；Run `run-20260908234852-2f4700f6df4a`，Drydock `drydock-95f381b24c36f7e9d5c50c6e8ac89345`。
- 真实 Web：配置 → 选择计划/进入交付 → 审阅批准 `edit-6f227e071daf31a6b083abb7974bf701` → 实际应用到 Drydock → 执行原 `& ./check.ps1`。
- Job `command-job-8b7db7bf45ca83eb34511388` 为真实 `completed / exit0 / 840ms`，输出 `UX_L_CHECK_PASSED`，实际 pwsh.exe 与工作区哈希匹配。`ui-11-command-visual.log`、宽屏截图及 `12-command-passed-narrow.png`（已检查未被侧栏遮挡）显示正确结果；旧 I 失败记录完整保留。
- 源项目外部改动 `UX_L_SOURCE_USER_CHANGE\r\n` 和原用户笔记保持；Drydock 的 `review.txt` 为 `UX_L_EXPECTED_FIXED\n`，笔记原字节不变。`verify_persistence.py --final` 在报告成功后只读核查 SQLite、文件及实时 GET，0 failed / 0 pending；该次审计不含随后才进行的人工完成和任务 finish。
- 首次生成报告返回真实 500：Git 在临时 index 加入 PS7 启动缓存的长路径时失败。生产 `drydockGit` 现仅在 Windows 的受控调用加 `-c core.longpaths=true`，不写 Git 配置。真实临时索引、长路径枚举及 bytes/index/config 保持回归通过（race 5.198s），见 `backend/delivery-longpath-validation.md`。
- 界面“确认上次报告”保留原请求，修后成功生成 `standard-code-delivery-969fe28670d13c06d389f83490c02ccf`：passed、verified=true、current_revision=true，收据 `8db968663a777f03294a0af2f4bbfc376dfdbc559fad01b7641cd69370388179`。首次失败及重试记录分别是 `ui-08-report.log`、`ui-13-report-retry.log`。
- 报告诚实列出 review.txt 和另一条未跟踪的 PowerShell `StartupProfileData-NonInteractive` 缓存，没有隐藏缓存或删除后冒用检查。`native/cache-diagnosis/CACHE_CONTROL_FINDINGS.md` 保留官方源码与真实 A/B；不引入 .NET 标为 INTERNAL 的缓存关闭开关作为产品兼容承诺，运行缓存与用户交付内容分离仍需后续改进。
- 真实 Web 开始计划项、填写针对本夹具的六项人工说明并明确完成：item `work-20260908235058-560635f0428f` v3；人工 checkpoint `delivery-checkpoint-20260909001526-9b107fb1c1e0` 绑定 v2。这些说明由受控验收操作基于已有证据填写，不等于用户已逐字人工审阅，也不是自动检查。`ui-14` / `ui-15` / `ui-17` 保存证据。`ui-16` 因脚本使用了错误的刷新按钮名称超时；后续 UI 与持久状态确认完成动作已经成功，不将该脚本超时算作产品失败。
- 请求最终 finish 首次返回真实 412，`ui-18-finish-task.log` 留存。真实完成门已通过，错误来自 `ProjectDeliveryAction` 给 Finish 写入了只允许 Wait 使用的 Reason。现清除这项无效投影，仍在 Supervisor ledger 保留通过原因。修前真实 SQLite 落点复现，修后两项 race 回归通过（1.345s）。
- 修后用既有受控 `POST /runs/{run_id}/execute`、新操作 key 恢复同 Run 原来的未完成消息（`20-recover-same-run.json`），没有修改 SQLite 台账或另造任务。新 handoff `run-handoff-20260909002658-f48a40f9b93d` completed / turn_finish，原消息提交，pending/prepared 都为 0。**恢复入口这一步是实际 API 调用，不能冒称 Web 按钮。**
- 交互式 Thread 的既有语义是 requested Finish → effective Continue：本轮交付完成后保留可继续的 Run，数据库 Run 仍 running；此处不把它标为 completed，也不修改产品生命周期来适配测试。完成门生成的当前报告为 `standard-code-delivery-fb7d46958ea00186d26496cd56c43184`，收据 `a409af651033d4c9541b6a109a5a2f449bb4173764d0a774c085413fef0afe9c`，仍 passed/verified/current。先前界面生成的报告也保留。
- 恢复后，原 Thread 请求确认仍返回旧 handoff 的 412；已由真实 Web 复现（`ui-21-confirm-original-before.log`）。现仅在同 key/内容/消息绑定成立、消息已 committed 且原 session/attempt 提交事件可核对时返回确认，保留原失败 handoff，不将 pending/cancelled 当成功。四项相关 race 通过（2.583s）。同页原按钮修后 202/replayed=true、同 Run、successor_created=false（`ui-23-confirm-original-after.log`）；没有重复模型或工具执行。
- `verify_persistence.py --final --require-turn-complete` 全部通过：当前 passed 报告、计划项 v3 与原 v2 人工记录、原消息提交、成功恢复 handoff、0 pending/prepared/active lease、请求 Finish/应用 Continue、源文件及 I 历史保持。核验文件 `backend/persistence-recovered-turn.log`；交互式完成与可选 CLI 终态完成分开。
- F14 失败文案修正：generic 412 不再武断归因权限/模型改变，unavailable 不再一律归因模型服务；未知请求需先确认再继续。后端投影定向测试及 14 项前端测试通过。`ui-21` 的短暂忙碌状态在 `ui-22` 稳定后消失，不是持续 Run.running 误判；不为此额外改忙碌逻辑。

## 最终界面、检查与清理

- 报告请求未知时隐藏新建动作，仅保留原请求确认。旧按钮本来已有 disabled，真实脚本超时不证明它可点击；本次属于引导清晰度改进，6 项组件回归通过。
- `ui-24-confirm-settled.log` 确认 Composer 在原请求成功后仍残留初次错误。现复用已有提交记录，按同 Thread/Workspace/原 operationKey 清除对应错误，保留新草稿和其他请求错误。真实 Conversation＋Composer 交互覆盖“初次失败→确认也失败→原 key 确认成功”、新草稿和跨任务晚返回，共 3 文件 21 项通过（3.93s），typecheck 通过。失败复现及最终日志为 `composer-confirmation-retry-before.log`、`composer-confirmation-request-identity-final.log`。该回归证明同页清理；重载后的截图不替代它。
- 最终前端类型检查/构建通过（`final-ui-build-confirm-retry.log`），API 随后重启。`ui-26-final.log` 确认实际加载 `index-DrhK6kxc.js`，无旧错误或结果未知提示；当前报告仍通过，计划项已完成，对话可继续。1440×960 与 390×844 截图 `26-final-report-wide.png` / `26-final-report-narrow.png` 经目视与遮挡检查，报告结论可达。旧大包告警仍存在，未宣称性能改进。
- 最终 `verify_persistence.py --final --require-turn-complete` exit0，39 项通过，0 pending / 0 failed，详见 `persistence-final-assets.log` / `verification.json`。1 个真实 Job、2 份 sealed report、1 项完成的人工计划，源文件、笔记与 I 旧历史保持；无待处理消息或 active lease。没有跑完整 Go、所有前端测试或全原生平台矩阵。
- 最终 API `cyberagent-ux-phase-l-confirm.exe` SHA256 `d454d8e37328db70df1ef0edeac74fbb6819b8af18132d3e3a9af8903f5a0a5f`；JS SHA256 `ec5c98e122fe09e022e0ed282bf20af08d5f5eb0e7b5ea61a6e40227ec8beb6c`，CSS `index-hT11lUFT.css`。完整产物与相关源码摘要在 `final-build-identities.json`。
- 08:47:53 +08 清理完成：API77864、provider82864、Playwright uxphasel/daemon67956 均关闭，18867/18868 无监听，L 运行时无残留进程。24 次 native 诊断的进程树/profile/ACL 均已回收。证据和运行时副本保留；`cleanup-final.json` 与 `native/diagnostic-cleanup-final.json` 可核对。

## 当前剩余范围

本轮已打通受测 Windows＋PS7 的真实 Shell、交付报告与恢复确认，支持范围不包含 PS5 Local。更广的审批种类、原生/DPI/无障碍矩阵、运行缓存与用户交付分离、暂停/终态单文件逆向入口、输出正文连续审阅、真实 LSP、旧 Host/CLI handoff、跨刷新草稿/未知 key、全文搜索和性能仍未全部完成。人工六字段与强制计划步骤对小任务的成本也仍需评估。此次通过不等于 F01—F14 或全部项目验收完成。

本轮继续复用现有运行时、临时索引、提交消息和收据，修正彼此之间的契约；没有增加新的数据库台账或替换框架。真实失败说明了跨层旅程验证的必要性：原单元测试曾把不合法的 Finish.reason 当成正确输出，实际写入路径才揭示错误。后续测试应优先覆盖这种真实边界，避免累加仅复述实现的断言。
