# 第七批：交付报告真实性与原请求恢复

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-08，Asia/Hong_Kong。工作树 `<workspace>`，分支 `codex/ux-journey-convergence`，基于 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。本批产品改动与 A–F 一样尚未提交、推送或发布。架构取舍见 [ADR 0153](adr/0153-delivery-report-observation-and-replay.md)，总目标见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)。

## 范围与结果

本批限定 F07/F14 的交付报告审阅、真实性与失败恢复，涉及少量 F09/F10 入口修正。报告路径已完成下述实现与分层验证；完整 Standard Code 开始、规划、执行检查、交付的连续旅程仍未验收。浏览器实际报告为 `not_run`，不是通过测试的报告。

- 历史 receipt 与当前 observation 分开显示。刷新中、刷新失败或缺少 observation 时，不把缓存的 passed 显示为当前版本已验证；展示核对时间、过期原因、原始结论。文件行标为报告记录，打开时读取对应 Drydock 的当前文件。零文件、零命令和零冲突显示真实 `0`。
- 生成报告时固定提交的 Run/Thread/Workspace/body/key；沿用 QueryClient 保存 pending/unknown。跨执行选择、标签和面板关闭重开后仍能以原请求确认，迟到回调只清理自己的身份。此保证限于页面生命周期，不声称刷新或退出后保留 renderer key。
- 后端先查原 operation，再按封存绑定核验请求指纹并投影新的 observation。文件、epoch 或生命周期变化不再让已成功的原请求先执行新 capture；不同请求仍拒绝。原空 Job 自动选择与同一显式清单保持旧指纹的等价语义。重试不返回另一份 latest 报告。
- 修复 preset `local` 与实际 adapter `local_windows_lpac` 名称直接比较造成的错误过期。仅映射已有 Local/Docker 的精确名称，不修改 receipt，不接受任意前缀。
- Supervisor 保留 Job 自身可证明非 passed 的精确失败检查 IDs。仍在 Diagnose，不放宽 current mutation 的完成门槛；新 mutation 清空旧 IDs。投影不完整但全部 Job 成功时不把这些 IDs 当作通过依据。
- 创建报告只依赖详情返回的可选 `standard_code_preset_configured` 明确事实，缺失为未知；全局 capability 或相似执行 tuple 不代表任务已配置。历史选中执行必要时读已有 Run detail，不为历史列表每行查询。后端仍检查实际报告前置条件。
- 输出入口复用 ArtifactDetail，按精确 artifact ID 读取并核对 Run、source Job、SHA、size、stream；展示输出记录元数据，**不包含输出正文**。没有把 job ID 冒充需要 call ID 的 activity 路由。原多个同目的地 Checkpoint/Undo/Rewind/Fork 按钮改为“查看项目恢复选项”。

无新表、migration、后台 worker、依赖或交付框架；SQLite 保持 v152。只读配置投影不写入 Domain Run.Config，也不授予新权限。

## 真实服务与浏览器证据

产物目录：[output/playwright/ux-phase-g](../output/playwright/ux-phase-g)。数据库通过 SQLite 只读 backup 复用 F 的合格本机模型，另建独立 Git 项目；不是空实例接入验收。使用真实 Go API、生产前端和本机确定性 provider，所有模型请求仅到 `127.0.0.1:18867`。

API 绑定 `127.0.0.1:18868`，仅追加 `--enable-workspace-import --enable-permission-control --enable-workspace-sandbox`，独立只读/控制令牌；无 Full Access、任意 host proposal 或外部凭据。最终可执行文件为 `cyberagent-ux-phase-g-final.exe`。

1. **真实配置。** Windows Local readiness 为 ready，实际 LPAC qualification 成功。新 Thread 在 created 状态经 preset preflight 返回 `workspace_untrusted`；检查独立源码后用新 key 确认准确 trust digest，返回 configured、network disabled、credentials none。`local-readiness.json`、`preset-preflight-second.json`、`preset-configured-second.json`。
2. **真实未验证报告。** 通过现有 lifecycle start、Session message 和 Run execute 保持已配置 Run。脚本 provider 的 finish 内容不符合 Plan/监督约束，执行报失败，Run 仍 running，但真实 Supervisor snapshot 已存在。后端报告保存为 `not_run`、0 条 verification Job；不是伪造 passed。`session-message-second.json`、`run-execute-third.json`、`report-initial.json`。总计 4 次本机确定性模型调用，包括协议修复尝试；0 个实际 Command Runtime Jobs。
3. **丢失成功响应。** 在 V2 点击生成，拦截器先让真实 POST 完成并保存返回内容，再 abort 响应。关闭审阅后重开，原请求确认入口仍在。`02-report-unknown.js`、`browser-report-unknown.log`、`02-unknown-after-reopen.png`。
4. **文件变化后确认原请求。** 仅修改已核对归属的 G/home/drydocks 下 `review.txt`。390×480 点击原请求确认，200/replayed 返回相同 report/receipt/checkpoint，新的 observation 为 stale，原 receipt_status 仍 not_run；页面显示记录后项目已变化。按钮边界 `(13,349.1875,112,27)`。`change_after_report.py`、`03-confirm-report.js`、`browser-report-replay.log`、`03-confirm-narrow.png`。最后的中性文案修正见 `05-final-stale-wide.png`。
5. **持久无重复与保护外部修改。** 同 key 改 uncovered intent 返回 409。前后均为 2 份报告、3 个检查点；存储仍 not_run，动态观察为 stale。原 source `review.txt`、用户笔记和 Drydock 外部新内容均保留。`verify_persistence.py`、`verified-persistence.json`、`intent-conflict.json`。
6. **读失败及恢复入口。** 对报告 GET 注入可控 503，已有真实 receipt 保留，标题变为“本次核对失败，当前版本未确认”；恢复网络后重新显示 stale。在 390×480 点“查看项目恢复选项”，实际进入有“暂停任务以预览撤销”的恢复页；按钮边界 `(13,303.1875,106,30)`。这是入口验收，没有执行恢复写入。`04-read-failure-and-recovery.js`、`browser-read-recovery.log`、相关截图。
7. **最终构建与未配置状态。** 刷新页面重新连接后，确认最终资源 `/assets/index-B5eM4zHx.js`、三个真实零计数；切到未配置的旧普通任务，报告创建按钮不显示，原因与“尚无报告”可见。`05-final-ui.js`、`browser-final-ui-verified.log`、`06-final-empty.js`、`06-unconfigured-empty-narrow.png`。1440×960 和 390×480 截图均目视检查。

浏览器没有实际 passed Job 或输出 artifact，故“历史 passed + 读失败”及精确输出身份在组件回归覆盖，真实服务 Job 投影在下一节的 SQLite/Git 集成覆盖，不混称真实 OS 命令验收。可控断连/503 会产生预期网络 console 错误，不冒称 console 零错误。

## 检查记录

| 检查 | 实际结果 / 证据 |
| --- | --- |
| 前端全量 `npm run test` | 101 文件 / 619 项通过，22.23s；`frontend-tests-final.log`。jsdom 已有 canvas 未实现提示，非失败 |
| 类型检查 | `npm run typecheck` exit 0；`frontend-typecheck-final.log` |
| 最终零计数/文案调整后定向测试 | `npm run test -- src/components/standard-code-delivery-panel.test.tsx src/v2/components/task-review.test.tsx`：2 文件 / 9 项通过，2.99s；`frontend-copy-targeted.log`；未因文案重复全量 |
| 最终生产构建 | `npm run build` exit 0（含 tsc）；`frontend-build-final.log`。JS 1828.81 kB / gzip 480.26 kB；既有 chunk 警告仍在，不是加载性能结论 |
| Supervisor race | `go test -race ./internal/application -run '^TestStandardCodeSupervisor' -count=1`：10.359s；`supervisor-failed-verification-after.log`。旧代码定向失败原件为 `supervisor-failed-verification-before.log` |
| Delivery application race | `go test -race ./internal/application -run '^TestStandardCodeDelivery|^TestCommandVerificationConclusion|^TestProjectStandardCodeUncovered' -count=1`：29.549s；准确命令与工具结果转录见 `backend-validation-results.md` |
| HTTP / Store 定向 race | 同一多包命令中 HTTP 5.169s、store 10.770s 通过；该次整体曾因 application 的测试操作员字符串非法而 exit 1，修正后仅 application 重跑通过。不能称多包原命令全绿 |
| 真实 SQLite/Git 集成边界 | exact original vs latest、异意图、磁盘/epoch、终态+第二连接、receipt/cursor/event/checkpoint不增、failed observeCommand→IDs→empty Record、adapter映射。Job outcome 为明示测试数据，无真实进程；`standard_code_delivery_replay_test.go` |
| Go 生产构建 | `go build -o output/playwright/ux-phase-g/cyberagent-ux-phase-g-final.exe ./cmd/cyberagent` exit 0，最终 binary 实际服务验收 |
| OpenAPI / TS schema | 最终 Go API 重新导出并生成；TS SHA256 `CAD3428325B421039A220978EC3ABBAB838DBE79145F212A5F739EB321E5EC97`；`schema-generation-final.log`、`artifact-hashes.json` |
| Protocol registry | `go run ./cmd/protocolregistry -check -baseline 15c399c8940aeb81d3c45bb0de96759b480d02c2` exit 0，33 家族 / 1004 标识 / 107 显式项；`protocol-registry.log` |
| Diff 空白检查 | `git diff --check` exit 0，仅已有 LF/CRLF 警告；`diff-check.log` |

没有跑全量 Go，也没有本批原生 Wails 窗口验收。以前完整 Go 检查的失败历史仍有效，不用定向通过替代。

## 失败、边界及下次重点

**新确认的产品缺口：** 第一份 created Thread 配置 Standard Code 后直接 `/threads/{id}/turns`，pending configuration 逻辑把该 configured Run 标为 cancelled，随后请求 412，未开始模型调用。`turn-inspect.json`、`inspect-fixture.log` 的 `thread_epoch_transition`、`thread-create.json` 与 `preset-configured.json` 可核对。第二份通过现有 Run 控制路径产生报告不修复此缺口。后续优先核对 Thread 的预期配置与 preset 原子配置如何一致，再演练真实 Plan→Deliver→命令验证；不得删校验或只在 UI 隐藏问题。

测试脚本初期也有明确的夹具错误：将创建 202 当失败、15-byte lifecycle key 被拒绝、report body 多加不接受的 version、错误 URL 片段、同 URL 未真正刷新/刷新后未重新连接、SQLite 列名猜错。修正脚本后相应实际步骤均通过；原失败日志和修正后的产物分开保留，不能将工具返回 exit 0 内的 `### Error` 写成成功。后端构造 Job、非法操作员等测试失败完整保留在其转录文件。

仍未完成：完整 V2 Standard Code onboarding/配置续接、真实验证命令到输出正文和最终交付连续旅程、非 Shell 审批矩阵、原生目录选择/DPI/键盘与无障碍矩阵、暂停/终态单文件恢复、历史全文搜索与加载成本。报告查看不是持续文件监控，已核对的某时刻也不能保证之后文件未变化。

## 清理

Chrome `uxphaseg` pid 60328、最终 API pid 79140、provider pid 83684 均关闭，18867/18868 无监听；`cleanup-final.json`。最终 binary SHA256 为 `3483D50592405260F7012B7D7AF0CA13C0D77A2DDC3D6540B32457F1784E5B0F`。所有隔离证据保留，无提交/推送。原 `CTF CyberAgent Workbench` 中用户既有代码不变，仅同步工作必读文档。
