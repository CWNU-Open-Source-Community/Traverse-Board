# 第八批：编码配置续接与计划入口

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-08；本批配置续接与计划入口的限定实现和分层验收已完成。完整“修改—命令检查—交付”因实测发现的目录身份断点仍未完成，列为下一批最高优先。本文不代表全部 UX 问题完成。

产品修改位于 `<workspace>`，分支 `codex/ux-journey-convergence`，基于 `15c399c8940aeb81d3c45bb0de96759b480d02c2`，包含未提交的 A–H 修改。原工作目录只同步必读文档。没有产品提交、推送或发布。

## 修复与验收标准

1. preset 配置 Run 权限的同一事务同步既有 Thread 权限偏好，使普通 `/threads/{id}/turns` 沿用已配置的 Run；不能误取消后再报 412。以 intent 开始时 Thread ID、snapshot ID/revision 作 CAS，不能覆盖之后明确修改的偏好。旧 configured key 只读重放。
2. 可提前确定为不可用的 pending 模型在取消旧 Run 前拒绝；原 Run、消息和绑定保持，纠正模型后可继续。并不承诺所有 successor 准备失败都具备原子回滚。
3. V2 审阅复用现有 StandardCodeReadinessPanel 与 PlanDeliveryPanel，在当前可继续 Code Run 上配置环境、选择方案和进入交付。配置不自动运行模型、不静默扩大其他任务权限。历史执行只读。
4. 配置与 Plan 操作身份保留在页面 QueryClient，关闭/重开面板、切换 Run 后仍用精确原请求确认；迟到结果不覆盖其他任务。明确永久失效才提供新预检查，一般冲突或丢失响应仍确认旧 key。
5. 真实连续流程的每个阶段以 API、持久记录和实际文件为准；没有命令 Job 不能写测试通过，生成报告不能代替命令验证。

未新增表、迁移、依赖或通用配置框架；SQLite 仍 v152。架构取舍见 [ADR0154](adr/0154-standard-code-thread-continuity-and-plan-controls.md)。

## 已发现并修复的真实契约问题

- preset 的空 `blocked_by` / `remediation` / `next_steps` 原来返回 null，严格前端拒绝，使有效预检查显示未知。Go 视图改为非 nil 空数组，没有关闭校验。原始失败在 `output/playwright/ux-phase-h/preset-contract-before.log`。
- readiness 的已安装适配器身份与当前执行授权是两件事：Plan/paused 时可有 `local_windows_lpac` 身份而 `current_run_granted=false`。前端曾把这个合法组合拒绝，实际 200 仍不能打开计划。原件为 `readiness-actual.log` / `run-detail-actual.log`；已修复为身份成对、类型/枚举/长度仍严格验证，false 不再禁止合法身份。真实 JSON 固化在 `web/src/test/fixtures/readiness-paused-plan.json`，修前失败与修后通过分别留档。
- 永久 Thread 偏好锚点失效使用可选 `error.operation_key_invalidated: true`。只接受布尔 true，普通 409 不猜为永久失效。重新核对使用新 key、未确认信任和无旧 digest；运行中暂未静止仍保留原 waiting intent。
- 已配置任务进入 Deliver 后，readiness 的 Plan tuple 不再 selected。旧界面因此显示受阻并重新提供开始编码，会诱导用户重配回 Plan。V2 现在依赖真实 persisted configured 标志和 Deliver phase 显示“交付中 / 已配置”，禁用新的配置；原未知请求确认仍可继续。旧独立诊断页不受此展示限制。恢复按钮复用现有样式。

## 独立真实环境与当前证据

H 数据库由 G 只读备份，仅复用合格本机模型；另建独立 Git 项目 `source-project`，不是空实例 onboarding。API 只监听本机 18868，确定性 OpenAI 兼容夹具只监听 18867，没有外部付费模型调用。启用已有 workspace import / permission control / workspace sandbox gates；未启用宿主 Full Access 或网络/凭据访问。

当前身份：Run `run-20260908011407-4df813b9f0ca`、Thread `thread-run-20260908011407-4df813b9f0ca`、Workspace `ws-import-09764df966ca4ca3b737716b`。

- 浏览器实际信任配置已完成，主动丢弃成功 POST 响应后，关闭/重开面板，用原 trusted key 确认返回 202/configured/replayed=true；未再次配置。390×480 确认按钮边界 x31/y292.1875/w144/h27。`configure-confirmed-retry.log`、`04-unknown-preset-after-reopen-narrow.png`。
- 最终生产前端普通发送实际返回 202、`successor_created=false`、原 Run ID、model_called/tool_called=true；两次 `workspace_read` 后 `plan_delivery_propose` 生成三方向，root wait 使 Run paused。`first-turn.log`、`provider-events.log`。没有 G 的 cancelled/412，尚不能由此宣称完整交付链成功。
- 修正 readiness 后，实际选择方向 1，主动丢弃成功响应，切换文件/检查标签后以原 `web-plan-109dd3a0-e701-41ad-a32d-94a1617f62eb` 确认，202/replayed=true/同 selection；随后进入 Deliver，mode revision 2→3，无模型或工具调用。`plan-original-request.log`、`plan-retry-request.log`、`plan-retry-response.log`、`plan-deliver-response.log`；窄屏 07 系列截图已目视。脚本回调曾报 route already handled，不能把整个脚本写成通过；已从独立网络原件确认实际动作与身份，不重复制造选择操作。
- 最终 bundle 再验证 Deliver 按钮禁用，390×480 包围盒 x31/y350.203125/w329/h72.59375，在滚动区可见可达；1440×960 / 390×480 最终 10 系列截图已目视。`deliver-final-ui-verified.log`。一次脚本沿用了交接中的简称而非实际完整标签，超时后按新 snapshot 修正，不是产品故障。
- 下一普通 turn 提案先使用 Drydock 文件 SHA，工具拒绝 `workspace patch source changed after it was read`。经真实 `workspace_read review.txt` 返回 source SHA 后，提案成功，但 WorkspaceID 仍为 source。这不是要放宽摘要校验的理由，而是暴露文件与命令所在目录不同，详见下节。没有批准或应用该提案。
- 最终持久核验：同一 Run paused/Deliver，1 个 proposed 编辑、0 个命令 Job、9 次本机脚本化模型调用；source review 保持 CRLF、Drydock review 保持 LF，两端 Git status 均干净、用户笔记保留。`verified-persistence.json` / `persistence-final.log`。没有生成 H 的 passed 报告或执行 finish，不声称真实命令验证成功。

## 检查记录

- Thread 模型资格新回归：三个真实 SQLite 场景修前均因旧 Run cancelled 失败；修后相关 application race 3.544s 通过。精确命令在 `pending-model-commands.txt`，前后日志分开保留。
- preset/Thread CAS、replay、delivery 相关 Go race 和真实 HTTP 数组/error-marker 检查通过；精确范围、耗时、fixture 时间失败和修正见 `backend-validation-results.md`。该文件明确是工具输出转录，不冒称原始日志；未跑全 Go。
- 前端 marker 合并后全量 101 文件/627 项 19.76s、typecheck 通过。readiness 尾部修复定向 4 文件/188 项 4.06s（`frontend-readiness-targeted-final.log`）；其中前一次 187 通过/1 失败为旧测试仍要求合法 !granted+identity 拒绝，原日志保留。Deliver 防重配定向 2 文件/19 项 4.19s（`frontend-deliver-state-targeted.log`）与 typecheck 再通过。未为了尾部窄修重复全量；不同阶段数字不相加作覆盖率。
- Go 主程序、最终前端生产构建通过；最终 JS `index-BRXaf8mT.js` 1847.80kB/gzip486.01kB，既有大 chunk 警告保留，不是首屏加载实测。最终 build 日志 `frontend-build-deliver-final.log`。协议登记 33 家族/1004 标识/107 显式项、diff check 通过（有既有 LF/CRLF 提示，没有 whitespace error）。
- TS schema SHA256 `32432170CDE50CCF7ECE4B70897FC41252CF6D708515644F183ECBBAFD4D9968`；本机 API binary SHA256 `EF10C5D2C0FD6F74425318E7A15AAC9F4ABE0196C3815B6DD41F63B2C29FBDD9`。没有全 Go / 原生安装包矩阵的通过声明。

## 下一优先：文件修改与命令验证须指向同一目录

2026-09-09 I 复核补注：下文保留 H 当时现场。I 已修复文件目标断点，但纠正了
collector 的 Job 身份假设：Job.WorkspaceID 是 source 控制身份，实际目录须核对
WorkspaceRootSHA256，不能要求 Job 也改成 Drydock WorkspaceID。参见
[第九批验证](UX_FIXES_PHASE_I_VALIDATION.md)。I 的真实命令已指向正确 Drydock，
仍在 Windows PS5/LPAC 启动初始化时失败，成功交付并未完成。

这是实测和源码共同确认的未修问题，不是新的框架偏好：

- `internal/application/supervisor_tools.go:654` 从 Mission 的 source Workspace 取 root；`agent_code_tools.go:277` 再要求注册 root 精确匹配；read/change 使用该 root，FileEditApply 也按 source 绑定读写。真实 `review.txt` read 返回 `1f85c4…`，Drydock 同名文件为 `5a2f57…`。后者不是前者的合法并发基准。
- Command Runtime 和报告使用该 Run 的 Drydock。既有 `internal/producte2e/producer.go:266` / `:376` 已要求 FileEdit.WorkspaceID 等于 Drydock.WorkspaceID，命令 Job 也有同一要求；collector 规则存在不等于当前 UI/工具路径已跑通。
- Drydock 已注册独立 Workspace 行，可以复用；Mission/Session 和 Supervisor v135 控制身份继续指 source。不能全局替换 source root，因为同一项目可以有多个 Run/Drydock；不能只换路径却继续把编辑和审批记成 source 身份。
- `internal/store/migration_v115.go:103` 的 Apply INSERT trigger 仍要求 Mission Workspace 等于操作 Workspace；v127 只为 checkpoint 扩展了精确 Run/Mission/Session/Drydock 分支。安全修复需要窄迁移及一致的应用层身份处理，不能临时关约束或复制 source→Drydock 让演示通过。本批 H 没有修改这部分，schema 仍 v152。

下一批验收标准：

1. 已配置任务按 Run 精确解析并核验 Drydock root/generation；普通未配置任务继续原行为。Drydock 缺失、漂移或属于他任务时拒绝，不回落 source。
2. read/change/proposal/approval/review/apply/inverse/checkpoint/command/report 的实际目录与持久目标身份一致，复用现有 Workspace/FileEdit/Drydock。旧 source 提案不能被新代码重新解释为另一目录。
3. 真实 source 与 Drydock 内容刻意不同（含 CRLF/LF），工具读取 Drydock SHA，审阅批准只修改其目标，source 及并发用户修改完整保留，实际命令验证同一内容。一个 source 的两个 Run 不可串目录。
4. 新迁移从已有 v152 保留历史记录和旧迁移 checksum；拒绝错误 Run/Session/Workspace/审批，保留普通任务回归。只为真实风险补必要测试，不镜像实现或用模拟 Job 冒称 OS 验证。

另一个尚未接通的结束入口：选择的计划项有独立人工 DeliveryCheckpoint，当前 V2 只展示历史，写入仍为既有 CLI。`todo start <id> --version ...` → 暂停 Run 时 `run delivery checkpoint <id> --operation-key ... --focused ... --diff-audit ... --security-audit ... --handoff ...`（最后切片还要 functional/robustness）→ `todo complete <id> --version ...`。模型 file snapshot、验证 Job、交付报告均不能替代这个人工 gate；本轮未通过 CLI 代填它，不能称完整 UI 结束交付。

## 失败与验收边界

Playwright 曾因把带副标题的开始编码按钮作 exact 匹配而超时；窄视口关闭 modal 后侧栏仍开，背景元素不可点击，随后点 backdrop 中心又命中侧栏；使用真实“隐藏侧栏”按钮后原请求恢复成功，没有 force click。失败脚本和日志保留，CLI exit0不等于脚本断言通过。一次组合进程操作被工具策略拒绝；拆为检查精确 PID/路径、单独停止已确认测试进程、单独隐藏启动后成功，未绕过产品权限。

尚未完成原生窗口/目录对话框、跨平台/DPI/完整无障碍、全部审批类型、暂停/终态单文件恢复、输出正文连续审阅、全文搜索、首屏性能和真实外部模型能力验证。页面刷新/renderer退出后的请求意图持久化仍不在本批保证内。

H 专用 Chrome `uxphaseh` 已关闭，最终 API PID 82296、provider PID 77868 已停止；18867/18868 无监听，详见 `cleanup-final.json`。其他浏览器/服务保留。后续不等待本轮已结束的 exec session；重新启动须重新核对进程身份，API 启动时缓存前端，重建后需要重启。最终数据保留一个未批准 source 提案，不能在下一批直接把它当 Drydock 提案应用。
