# 第十四批：运行缓存隔离与报告内的已保存输出

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-09，Asia/Hong_Kong。状态：本轮限定范围已实施并验收。新用户消息直接重跑
原 PS7 检查成功，报告 passed/verified/current，300 行中文输出可在报告中完整审阅；
项目没有新增运行缓存，用户同名目录中的真实文件继续进入 diff/checkpoint。
这不是完整 F01—F14、全仓测试或全部平台完成声明。

## 范围与取舍

用户在 M 的持续 Thread / 独立 Turn 修复后要求“继续”。本轮解决新 Shell
缓存污染项目差异、报告内缺输出正文两个相关交付缺口。继续在
`<workspace>`、`codex/ux-journey-convergence` 实施，
基于 `15c399c8940aeb81d3c45bb0de96759b480d02c2`；A–M 未提交改动保留。
未 commit/push/merge/publish，原工作树仅同步工作必读。

真实验收又发现两个直接阻断该流程的问题：完整保存输出被误判为预览截断；新的
用户消息重跑相同命令被整个 Run 的去重记录拒绝。本轮一并修复，没有通过缩短
输出、修改命令字符串或制造文件编辑绕过。

新 Local 命令把 HOME/TEMP/AppData 放到外部私有、命令结束即回收的 scratch。
保留实际 cwd、PS7 normalizer、三个 SID 的精确集合、离线/无凭证边界和进程树回收。
计量项目及 scratch，不允许删除项目文件抵消 scratch 写入。owner v3 增加目录身份，
保留原 v1/v2 指纹恢复，但旧记录不获新删除权限。未按 `.traverse-board` 路径过滤、
未自动删除旧缓存或重算旧报告。跨命令缓存复用不在本轮承诺，可能增加构建成本。

报告增加动态 `output_sources`，只从真实持久命令活动查找对应 Job/artifact。
报告读取不加载 blob 或完整输出 Job；正文继续走既有 Thread 作用域接口、完整
blob 校验、二次脱敏与展示摘要核对。前端共用 `SavedCommandOutput`，按需展开、
原位重试、独立请求身份，明确保存上限。原 sealed receipt 与二次脱敏正文摘要
可以不同，不放宽任一验证。无新表、迁移、日志框架、下载接口。
取舍见 [ADR0157](adr/0157-runtime-scratch-and-saved-delivery-output.md)。

Report 与 Supervisor 共用完整保存判断：仅对 `inline_window` 且两路实际保存正文
通过原 Job/descriptor/blob/hash 校验的记录认可完整；真正超过保存上限、缺失或损坏
正文等仍拒绝。读取旧报告与原 key 重放不重新评价或改写封存结论。

命令重跑按既有持久 operator message/delivery 区分明确新输入与同输入恢复。
历史指纹不变，仅命令 run/start 可离开另一条明确消息的去重范围；文件编辑和 Job
管理的原范围保留。Diagnose 中新输入使用既有 turn-prepared 记录开放首次命令启动，
仍可先提出修改；没有全局切成 Execute，没有新增迁移、重置预算/失败计数或放宽 Stop。

## 已完成的定向验证

- 前端 3 文件 58 项，4.11s；含双 Job/双流懒读、错误重试、晚返回隔离、错绑不发正文、
  可选字段闭集及旧服务兼容。已有真实 client 摘要/授权负例 1 项，1.36s。最终新 schema
  后 typecheck exit0。日志 `output/playwright/ux-phase-n/frontend-report-output-final.log`、
  `frontend-output-client.log`、`frontend-report-output-final-typecheck.log`。
- 后端真实 test-child + SQLite + HTTP 新集成 0.564s。报告控制器提供固定元数据来隔离
  投影；不是完整沙箱交付旅程。证明报告 GET/POST 零正文预读、真实来源、双流脱敏、
  原 receipt 不改、缺来源及策略不适合时仅元数据、错摘要拒绝、跨 Thread 404。
- 关联 race 最终 HTTP 11.908s、store 1.699s、application 9.296s；前两项与最后一项来自
  两次实际通过命令。完整 regex、命令与原始失败记录见
  `output/playwright/ux-phase-n/backend/backend-validation-results.md`。
- OpenAPI 与 TypeScript schema 已生成；协议注册表同步 33 家族、1008 标识、107 显式
  测试/金样项。前端 `npm run build` 含 typecheck 通过，实产 JS `index-8Ceg5rk6.js`。
  大 chunk 警告保留，不宣称加载性能已解决。
- 最终 Windows sandbox race：19 个顶层实测通过、4 个 helper 跳过，14.397s。
  包含真实 Go 编译、孙进程 timeout/cancel 后回收、双目录写入额度、网络/源目录/
  凭证拒绝、v1/v2/v3 恢复、替换与普通同名文件保护。生产 normalizer + LPAC PS7
  另有四项：原脚本 exit0，复杂子目录 exit0，中文 stderr/exit23，注入不存在根时
  bootstrap exit125 且用户脚本未执行。完整证据及路径/内存限制见
  `output/playwright/ux-phase-n/native/SCRATCH_VERIFICATION.md`。
- inline 保存修复的 SQLite/Git 回归 13.370s，后续 race application 54.496s、HTTP
  4.295s；这些是新消息重跑修复之前的版本，不冒称覆盖后续代码。
- **最终合并候选**：进程内 `GOTMPDIR/TEMP/TMP=D:/Traverse-N-Temp`，执行
  `go test -p 1 -race ./internal/application ./internal/httpapi -run '^Test(StandardCodeSavedInlineOutput|StandardCodeDelivery|StandardCodeSupervisor|CommandVerificationConclusion)' -count=1`，
  application **114.880s**、HTTP **4.474s**，整体 exit0。
  日志 `output/playwright/ux-phase-n/explicit-input-and-saved-output-final-race.log`。
  新消息回归使用真实 SQLite/Git/队列，覆盖相同正文不同 key、原 key 不重排、同输入
  重建后防重、自动 kernel turn 不等于新输入、修改提案与原计数保留；命令结果为
  明确构造数据，OS 证据来自下面真实产品旅程。独立只读复核没有发现新增阻断。

环境失败没有抹掉：C 盘约 584 MB 可用曾导致 Go 链接磁盘不足及旧 Host PS5 初始化
失败；同组转 D 盘后 HTTP/PS5 通过。较长 D 临时根又使既有 FileEdit Git 夹具报
`$GIT_DIR too big`，短 D 根后 application 通过。只改变本轮测试进程临时目录，
没有清理共享缓存或修改生产 Git/权限。首次源码测试夹具编译及构造错误保留于上述索引。

原生先前若干名含 final 的日志实际失败，以 `local-sandbox-budget-adjusted-final-race.log`
为最终通过。两个 race 测试进程共用 256MiB 时连旧 HOME 对照也无法启动；只将这个
进程树测试夹具校准至 1GiB，生产配额不变。较长有效 profile 路径也实际影响子进程；
scratch 名由 64 改为 32 个十六进制字符，完整身份仍保留于 journal，Mkdir 碰撞拒绝。
受测 259/260 字符 AC 路径通过，故不臆造统一 260 阈值，也不承诺任意长 portable 根。
新消息集成早期失败是 SessionID/模型递增序号/调用录制等测试夹具构造问题，原日志
保留；真实去重问题以 UI13 和 provider 记录为依据，不能混写为更多生产故障。

## 真实产品验收

`output/playwright/ux-phase-n` 从 M2 SQLite 做只读 backup；原 M2 数据与工作目录不写。
新 source commit `83388a4076e4bb456c78acac1ae2215ddfebe8d5`，原五文件包含真正的
`.traverse-board/home/user-owned.txt`；`check.ps1` 输出 300 行中文及首尾标记和 stderr。
本机确定性 provider 只编排工具调用；真实 Job、磁盘和报告由产品执行产生，不是模型
质量或三产品能力基准。N 导入/创建使用实际 API，不冒称本轮用 UI 重新验收导入。

Thread `thread-run-20260909064255-56108c067ae5`，Run `run-20260909064255-56108c067ae5`，
Session `sess-20260909064255-43f83dc074cb`。实际 UI 已选择 Plan、进入 Deliver、审阅批准
`edit-72966da6c3521248ffaddb263d412a9b`，普通消息发送应用成功；未点击恢复任务。
应用后真实 Drydock 仅有原五文件，只有 review.txt 修改；source 原五摘要保持。
这些准备步骤使用 M 最终 API；以下两条实际 Job 均由 N 的生产 scratch 实现执行。
模型服务为本机确定性夹具，PS7 为明确指定的 K 阶段可信工具目录；不声称自动安装、
任意机器默认配置或真实付费模型质量已验证。

首个 N 生产 PS7 Job `command-job-045f615cdf2b5dd6d1553b67` 已实际 completed/exit0、
956ms、tree_reaped，项目仍仅原五文件，报告 diff 仅 review.txt。报告
`standard-code-delivery-d1d127b13df5cacc7cfd1596083a6e70` 却为 partial：已定位到
`inline_window` 被误当作保存输出丢失，现已修复报告与 Supervisor 完成门相同判断。
这不是 Shell 失败，也没有通过缩短夹具输出规避。

实际 UI `ui-10-first-report.log` 证明生成报告和展开来源记录均 0 正文请求；
`ui-11-output-retry.log` 一次受控浏览器网络中断后原位重试成功，stdout 26774 B，
300 行中文及首尾标记齐全，stderr 45 B，两者均 truncated=false、无 U+FFFD。
原始观测 stdout 27076 B 与保存值差 302 B，是 302 个 CRLF 规范为 LF，不是正文丢失。
原 partial 报告及收据 `c5419731fdac9d205ba4a70af143d35589d9f25cb2145717dcb4bc14a6192013`
保留。随后仅在 N Drydock 用户文件追加受控 marker `UX_N_USER_CHANGE_AFTER_CHECK`，
实际 GET 观察变 stale、原 receipt/status 不变；`user-fixture-change.json` 与
`first-report-after-user-file-change.json` 留证。原 source 与 M2 均未写。

inline 修复后的第二次普通发送（`ui-13-verify-final.log`）被
`duplicate_side_effect_intent` 拒绝，0ms、无新 Job。新消息边界修复后，仍以同一原命令
`& ./check.ps1`、同一 purpose 和输出额度发送；没有点击恢复、改脚本或伪造编辑。
`ui-14-verify-message-boundary.log` 返回 202/committed，产生第二条实际 Job
`command-job-953a4b8cf2b2c3d5bfb329be`，exit0、998ms、tree_reaped。
UI 保留此前 denied 记录。

`ui-15-final-report.log` 通过实际界面生成新报告
`standard-code-delivery-ce0934452941c84c2fc331b790b13620`：passed、verified=true、
current revision，receipt `21ade58acc0234fa6bb5e3b6969707bc0c5ca0ee955994ce7773bd8c6dbff6b2`。
只引用第二条 Job，output_truncated=false；diff 正好是 review.txt 与
`.traverse-board/home/user-owned.txt`，最终 checkpoint 含全部五个实际用户文件。
原 marker 保留，旧报告保持原 partial，未通过路径名称过滤或重封收据获得通过。

生成报告及展开两条来源仍各为 **0 正文 GET**。`ui-16-final-output.log` 对第二条 Job
真实活动 `toolu_e1d60b6e701ca4ee95375b60` 分别按需 GET，均 200；stdout 的 300 行、
开始/结束标记及 stderr 中文完整，无 U+FFFD，均 truncated=false，没有混用旧 Job 内容。
实际 1440×960/390×480 截图和几何记录确认输出可滚到末尾、收起按钮可达；
`16-final-output-tail-narrow.png` 是真实窄窗口末尾。初次返回截图侧栏仍打开，不能据此
声称输入可达；随后 `ui-18-composer-narrow.log` 实际收起侧栏，未发送的草稿保留，
发送按钮 x341.33/y409.17、28×28，位于窗口内并可用。检查草稿随后清空，未触发新消息。
旧 console 404 和一次人为中断记录保留，不宣称整个历史零错误。

`17-original-key-replay.json` 使用 UI14 捕获的原 key/body 再次调用真实 API：202、
replayed=true、同一 committed steering；前后两条 Job 全列摘要、报告摘要、provider
事件文件摘要和计数完全相同。这是原请求确认，不是再次执行命令。

`final-state.json` 保存实际 GET 的当前 passed 报告；`final-audit.json` 全部必要核验
通过，只读 SQLite/事务回滚、Git 禁可选锁。它逐列比较 M2 原 4 条 Job、8 条封存报告
与 N 复制记录，校验首条 N Job/partial 报告和真实 denied 行保持，原 source 五文件
及摘要保持、当前 Drydock 五文件及 marker、两条真实 PS7 Job 的 cwd/权限/无网络/
关闭 stdin/回收/双流 blob，以及最终 diff/checkpoint/generation、无待投递/执行租约/
文件变更事务和外键违规。旧数据库整文件摘要只证明审计期间不变，不虚构 N 开始前
整文件摘要。

## 最终构建、清理与剩余边界

最终真实 UI 使用 `cyberagent-ux-phase-n-continuation.exe`，SHA256
`7b1c31b15a212dfcf17c26e941a67d0b1ac28eac9583c2b057a8f14d592361d8`，07:28:21Z 构建，
晚于生产源码冻结。JS `index-8Ceg5rk6.js`、CSS `index-hT11lUFT.css`；完整来源摘要在
`final-build-identities.json`。原生 6 文件摘要与最终原生回归一致。数据库沿用 M 的
迁移 v155，N 不新增迁移。Go build、前端 build/typecheck、协议同步和 diffcheck 通过；
未运行全 Go/全前端/全平台矩阵。

`cleanup-services.json` 记录精确检查进程路径及命令行后关闭 N API57132、provider82224、
浏览器 uxphasen/74460；18867/18868 无监听，实际 runtime owner 根只剩隐藏 owner.lock，
无残留 scratch。原生 8 个独立夹具的进程树/profile/ACL 也正常回收，纠正后的观察记录
为 `native/diagnostic-cleanup-corrected.json`；最早观察器漏 `-Force` 的错误原文件保留。

自动审批拒绝删除两个已确认空的测试根（`.n` 与 `N/native/go-tmp`），仅返回
`blocked by policy`；已停止尝试并保留。这不等于运行 scratch 回收失败，亦未清理
用户文件、旧缓存或任何历史证据。

N 夹具按请求暂停等待人工审阅，Plan 人工项目仍待处理；本轮不宣称人工完成或任务
Finish 已演练，M 的独立旅程保存该证据。仍待推进：暂停/终态单文件逆向 UI、强制
三方案/六字段的小任务负担、其他审批、原生 Wails/DPI/键盘、真实 LSP、其余 Host/CLI、
跨应用重启草稿/未知 key、全文搜索与性能。每命令独立 scratch 不保跨命令工具缓存，
可能增加重复构建成本；当前未测成性能收益。
