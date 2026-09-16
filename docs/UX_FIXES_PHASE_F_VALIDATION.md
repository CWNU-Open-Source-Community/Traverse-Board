# 第六批 UX 修复与验证：单文件撤销与审阅恢复

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-08，Asia/Hong_Kong。目录 `<workspace>`，分支 `codex/ux-journey-convergence`，基于 `15c399c` 的前五批未提交改动。**本批限定范围已验收，完整 F01—F14 尚未完成。**

## 本批选择

优先把已应用文本 FileEdit 的撤销接入既有提案、差异、精确批准与应用流程。支持 replace/create/delete 的逆向，移动、脱敏或不完整原文、已经后来改写的目标文件不在当前可恢复范围。当前要求 Running Run 和 active Session；保持任务权限，保守模式沿用普通 FileEdit 的精确审批。完整终态任务的撤销与交付仍另列待办。

选择逆向提案是为了复用已有单文件写入与冲突保护。没有给整项目检查点的预览单方面过滤路径，也没有引入第二套快照表。比较与边界见 [ADR 0152](adr/0152-reviewed-inverse-file-edits.md)。

独立修复现有检查点预览迟到覆盖、未知恢复请求跨标签丢失身份、暂停/恢复回调串 Run，以及相同 Run 的检查点恢复准备和恢复执行之间的互斥。后者以已有 prepared/applying 恢复事务和执行租约联锁，范围不扩展为跨 Run 的通用工作区锁。

## 验收要求

- 从文件编辑记录生成精确逆向 diff，经用户批准和应用恢复；原编辑历史保留，用户其他文件不变。
- 原文不足、目标已变、跨 Run/Workspace、非允许状态明确拒绝；批准后再变目标仍拒绝覆盖。
- 创建并发、服务端重启及持有原 key 的未知响应重试保持提案身份，不把 approved/applied 状态重置为 proposed；拒绝后可显式新尝试。浏览器页面刷新后 key 的恢复不在当前范围。
- 人工应用实际持有现有执行租约，与模型执行互斥；旧已完成应用可读取重放结果。
- 检查点准备和 Resume/Acquire 的两种先后均受同一数据库边界约束；事务完成/失败后允许正常继续。
- 恢复选择变化、切标签、响应丢失和失败事务重放，不能误显示成功或用新身份重复写入。
- 必要 Go/前端/契约检查与真实浏览器走查，分别记录实际结果。

## 现场

夹具 `output/playwright/ux-phase-f/`：SQLite 只读备份自第五批，复用已通过资格的本机模型；另建 source-project，包含待修改文件和用户已有笔记。不是空实例接入验收。Go API 在 18868，本机确定性 provider 在 18867；显式选择 `ux-local-fixture/ux-scripted-model` 创建任务，未调用外部模型。

任务 `thread-run-20260907233518-75b4647be4dd`，Run `run-20260907233518-75b4647be4dd`，保守权限。先用 E 固定可执行文件/API 建立真实普通提案，经界面批准/应用后切到 F 构建；逆向验证时已关闭 `--enable-file-edit-proposals`，capabilities 证实 review/apply 为 true、arbitrary proposal 为 false。另外用现有 CLI 在相同 Session 建立一个新文件的正向提案，供删除正文验证；没有直接修改数据库来伪造已应用状态。

## 已完成的真实 Web 与磁盘验证

| 场景 | 实际结果 / 证据 |
| --- | --- |
| 正常逆向替换 | 从已应用记录生成普通待审提案，展示正确正反 diff；批准、应用后磁盘恢复原文，原历史保留；`verified-inverse.json` |
| 提案成功响应丢失 | 代理先取得真实后端 202，再向浏览器中断响应；切执行记录后返回，以相同 key 得到同一 Edit / replayed=true，数据库仅一份逆向提案；`inverse-unknown.log` |
| 应用成功响应丢失 | 真实后端写盘后中断响应；跨标签后原 key 确认，replayed=true、file_written=false，无重复写入；`apply-unknown.log` |
| 用户其他文件 | 用户已有 `user-notes.txt` 和正向应用后外部新增 `after-agent-notes.txt` 均逐字节保留；不是整项目回滚 |
| 再次恢复 | 对已应用逆向再提议、批准和应用，磁盘恢复正向结果；`redo.log` |
| 批准后目标变化 | 外部编辑 `review.txt` 后应用返回 409/CONFLICT，拒绝覆盖；`stale-apply.log`。目标保留外部修改 |
| 提案前目标变化 | 原源编辑的目标已变时，新 key 请求 409/CONFLICT，数据库没有新增提案；`final-persistence.json` |
| 删除并恢复新文件 | 正向创建后，逆向 delete 显示真实删除正文；批准/应用使文件消失；再对 delete 生成 create，批准/应用恢复相同摘要；`delete-preview.log`、`restore-created.log` |
| 窄窗口可达 | 390×480 下批准、应用、预览按钮经正常滚动可见，实际点击成功。包围盒分别约 (145.67,313.42,95.33,34)、(145.67,313.42,75.33,34)、(25.33,313,92.33,34)；不是仅以无横向滚动条判断 |

截图 `01-applied-source.png`、`02-inverse-diff.png`、`03-unknown-apply-after-tab.png`、`04-apply-confirmed.png`、`05-target-conflict.png`、`06-delete-diff-wide.png`、`07-delete-approve-narrow.png`、`08-delete-apply-narrow.png`、`09-restored-created-file.png`；已目视核对真实删除 diff 和窄屏按钮。模拟响应中断、测试期间 API 重启和预期 409 会产生对应浏览器控制台错误，不以这些人为注入的错误冒充产品正常运行异常。

最终磁盘/数据库核验：7 项编辑，其中 6 项已应用、1 项因外部改动仍已批准但未应用；用户第三方修改及两份用户笔记保留。确定性本机 provider 仅 1 次任务调用，所有审阅与撤销未重新调用模型。本记录不评价真实模型解决问题的质量。

必要后端测试与早期失败的真实转录见 `output/playwright/ux-phase-f/backend-validation-results.md`；同 Run 恢复互斥的旧码失败原件、修后工具结果转录及准确命令见同目录 `checkpoint-interlock-*20260908-073323.*`。三操作 round trip、原文脱敏/完整性、同 key 并发/重启/拒绝后新尝试、事务内新鲜 Run、人工 lease 等使用真实 SQLite 和磁盘测试，不能统称全部通过浏览器。

最终行数修复后的浏览器复验 `final-counts.log`：只有元数据时不显示假零行，读取删除详情后当前行正确为 -1，完整可得时总计 +6/-5。最终宽/窄截图为 `10-final-delete-counts-wide.png`、`11-final-review-narrow.png`，已目视核对。首次该只读验收脚本误用两个 header 的严格选择器，保留 `final-counts-selector-failure.log`；改为精确汇总 header 后通过，未为该脚本修改产品行为。

## 必要检查与实际结果

以下均在本实施工作树执行；包秒数是各包耗时，不能简单相加当成总墙钟时间。

| 检查 | 结果 |
| --- | --- |
| `npx vitest run`（web） | 101 文件 / 613 项，25.32s；4 个既有 jsdom canvas 提示；`frontend-tests-final.log` |
| 尾部行数修复定向 `file-edit-panel` / `task-review` | 2 文件 / 14 项，2.32s；`frontend-counts-targeted.log`。此前全量之后仅对应计数/展示变化，未再重复全量 |
| `npm run typecheck`、`npm run build`（web） | 最终通过；`typecheck-final.log`、`frontend-build-final.log` |
| `go test -race ./internal/application ./internal/store ./internal/httpapi -run '^TestFileEdit\|^TestOpenAPI' -count=1` | application 7.487s，httpapi 4.720s；store 无匹配测试，仅编译，不当作测试覆盖 |
| `go test -race ./internal/fileedit -count=1` | fileedit 全包 1.479s |
| `go test -race ./internal/application -run '^TestFileEditRevertProposalRechecksRunInsideAtomicInsert\|^TestFileEditApplyManualLease' -count=1` | 2.063s，后加事务内状态与跨 store 人工 lease 用例 |
| `go test -race ./internal/store ./internal/application -run '^(TestWorkspaceCheckpoint\|TestWorkspaceRestore\|TestSchemaV117\|TestSQLiteRunExecutionLease\|TestRunLifecycle\|TestFileEditApply)' -count=1` | store 25.584s、application 18.785s；含真正双连接 Restore vs HTTP Resume / CLI Resume / Acquire 并发 |
| `go test -race ./internal/httpapi -run '^TestFileEditDeleteHTTP\|^TestFileEditRevertProposalHTTP\|^TestOpenAPI' -count=1` | 删除正文投影最终 6.183s；涵盖普通/逆向/空文本/脱敏/摘要不匹配 |
| `go test -race -tags 'desktop,wv2runtime.error' ./internal/desktop ./internal/app -run '^TestControlPlaneCompletesARealAnthropicCompatibleDesktopThreadTurn\|^TestAPIOpenAPI' -count=1` | Desktop 1.572s、app 1.422s；控制面，非原生 UI |
| OpenAPI / TS schema | 最终 Go API 重新导出，`npm run generate:api` 后 hash 相同：`58C477E048431BBA48B7A832FACD4D5DDBC2707AD351C772DFCB29901395EB9E`；`schema-hashes.json` |
| `go run ./cmd/protocolregistry -check -baseline 15c399c8940aeb81d3c45bb0de96759b480d02c2` | 33 家族 / 1004 标识 / 107 显式项；新增内部指纹域归入已有家族，无 wire 版本升级 |
| `git diff --check` | 通过；换行规范化提示保留 `diff-check-warnings.log` |

最终主 JS `index-C-HgHPJY.js` 1823.66 kB / gzip 478.59 kB，既有大 chunk 警告仍在；未做首屏加载基准，不据此得出框架优劣结论。完整 Go 测试集本批未运行，第一批全套失败历史继续保留。没有新数据库迁移、回滚存储、依赖或额外运行时；schema 仍 v152。

## 清理与范围限制

Chrome `uxphasef` pid 84568、最终 API 59068、模型服务 86888 已结束，18867/18868 无监听；见 `cleanup-final.json`。前面用于 seed 的 E 可执行文件与中间 F 服务均已停止；夹具/日志留在 ignored output 中，不清理用户原有目录。模型服务 1 次调用是本机确定性响应，不是外部付费模型验证。

以下仍待推进，不属于本批通过结论：暂停/已结束任务的新逆向提案和写入，move / binary / 脱敏与不完整原文恢复，任意 shell 副作用撤销，跨 Run 共用工作区互斥，renderer 在页面刷新/应用退出后的 key 持久化，完整 Standard Code 交付与审批矩阵，原生 Wails 窗口/系统目录选择/DPI 和全量无障碍矩阵。旧检查点 unknown/迟到选择与失败反馈由定向组件测试覆盖，本批没有在真实浏览器重演全部检查点故障注入。不得将受控 Web 验证当作原生或真实模型能力验收。
