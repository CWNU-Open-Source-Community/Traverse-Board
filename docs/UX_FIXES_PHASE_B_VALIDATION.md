# 第二批 UX 修复与验证：审批、任务审阅、文件引用

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-08，Asia/Hong_Kong。实施目录 `<workspace>`，
分支 `codex/ux-journey-convergence`，基于 `15c399c8940aeb81d3c45bb0de96759b480d02c2`。
本批与第一批均为本地未提交改动，原用户工作目录未叠加产品修改。
本记录是限定环境的验证，不代表 F01—F14 或原生平台全部验收。

## 已实现

- **F06**：按精确审批身份读取后端预览；保留元数据队列，区分模拟执行、记录 Git 批准、文件审阅和网页读取；展示目录、命令/参数/对象、范围及拒绝含义。加载失败可重试，过期/不匹配/截断时不能批准；批准服务补验 Shell/ScriptProcess 指纹。真实宿主及受控命令复用已有专用面板。
- **F07**：对话标题栏直接打开任务审阅，按执行选择文件差异、检查与交付、结构化工具记录、参考资料及检查点。差异可携带文件路径、编辑 ID、前后哈希和执行 ID 放入修正草稿。项目当前差异明确包含用户/其他任务修改；历史“已应用”不再冒充当前文件内容。
- **F13**：已有任务可选最多 4 个项目文件，显示/移除引用，发送时通过既有 evidence API 验证摘要再发消息。失败保留草稿；并发新增引用不会被上一条消息的完成清除。已附加资料可在任务审阅中查看。
- **恢复反馈**：暂停后输入仍可编辑，发送需先恢复；新增直接恢复入口。检查点预览不完整时禁止确认，相同恢复重试保持操作身份。交付组件打开文件使用报告绑定的 Drydock 工作区。
- **真实走查发现并修正**：附件失败后成功重发不再留旧错误；CLI/Desktop 未启用交付服务时不再把 typed nil 当有效控制器并误报任务 ID 无效。没有报告时明确不能推断检查通过。

复用与边界见 [ADR 0148](adr/0148-approval-preview-and-task-review.md)。没有新增依赖、数据库迁移、通用状态机或回滚平台。

## 真实 API 与浏览器证据

环境：Windows、真实 Go API + 生产前端、独立 SQLite/项目目录、本机确定性 OpenAI 兼容响应服务。
测试服务仅访问 loopback，不调用真实付费模型。夹具通过真正的 Gateway 产生 Shell 和文件提案，
未拦截或伪造相关业务 HTTP 响应。全部产物位于 `output/playwright/ux-phase-b/`（Git 忽略）。

| 场景 | 实际结果 | 证据 |
| --- | --- | --- |
| 精确 Shell 审批 | 页面显示两条不同 echo 命令与根目录；一条批准模拟，一条拒绝，均返回真实 202。没有启动真实命令进程 | `approvals.log`、`02-exact-approval.png` |
| 文件引用进入输入 | context.md 的摘要通过 evidence 202，随后消息 202；脚本模型实际收到 `UX_CONTEXT_SENTINEL_20260908`，只记录布尔值，不保存原始提示 | `context-send.log`、`provider-events.log`、`01-context-selected.png` |
| 选中后文件改变 | 修改夹具 context.md 后，附件返回真实 409，只发出 evidence 请求，没有 `/turns`；草稿/引用可恢复，移除引用后发送成功，旧错误消失 | `stale-context-final.log`、`07-stale-reference.png` |
| 差异与修正 | 编辑提案在主界面可见；请求修正包含精确编辑身份、文件、哈希及执行 ID；已有草稿保留 | `file-review.log`、`03-file-diff.png` |
| 文件实际写入 | 批准编辑意图不写文件；随后 apply 写入并形成检查点，原未提交文件修改不变 | `file-review.log`、`second-edit.log`、`04-file-applied.png` |
| 冲突拒绝 | 同一文件的外部修改阻止撤销；无关外部新增文件也阻止整个快照恢复。确认禁用，没有覆盖 | `restore-conflict.log`、`restore-safe.log`、`05-restore-conflict.png` |
| 正常撤销/重做 | 在原有文件及外部新增文件都进入第二次编辑前快照后，真实 Undo/Redo 事务均 completed；只切换 review.txt 的第二次编辑，其他两文件哈希相同 | `restore-authorized-timeline.json`、`redo-transaction.json`、`undo-disk-verification.json`、`redo-disk-verification.json` |
| 权限拒绝 | 保守模式预览通过但确认返回 403，文件未变；在隔离夹具中通过真实 UI 选择逐次审批后完成恢复。没有绕过恢复权限 | `current-restore.txt`、`restore-permission.log` |
| 暂停与草稿 | 暂停时发送禁用、草稿仍可写；恢复 API 成功后草稿保留、发送恢复 | `resume-draft.log` |
| 视口与键盘 | 1440×960、390×480；发送、引用、审阅及两个弹层关闭按钮的边界可达；Escape 关闭审阅后焦点回到入口，草稿不丢 | `layout-final.log`、`08`—`13` 号截图 |
| 无报告状态 | 修正 typed nil 注册后，未配置服务的真实请求按不可用处理，界面不再误报无效任务 ID，不显示检查通过 | `final-login.log`、`11-narrow-checks.png` |

本批截图已实际打开检查布局。CLI 对原生 confirm 的工具输出会先返回弹窗状态，
不能仅凭脚本退出码推断恢复成功；本记录以最终 Go 事务和磁盘内容为依据。
`restore-safe.log` 保留最初受阻的结果，不能把它算作成功撤销。
两次正常恢复中保留的 external-later.txt 已存在于对应目标快照，不能据此宣称支持忽略快照后的外部新增。

## 自动检查

| 检查 | 结果 |
| --- | --- |
| 前端全量 | 98 文件 / 566 测试通过，最终一次 22.05 秒；`frontend-final-tests.log`。保留既有 jsdom canvas 警告，无新增 canvas 依赖 |
| 生产构建及 TypeScript | 通过，`build-final.log`；主 JS 1,797.38 kB / gzip 470.38 kB，已有大块体积警告仍在 |
| 审批 Go race | application / httpapi 针对审批绑定、决策与预览通过；`go-approval-race.log` |
| 交付 Go race | delivery 及现有当前版本验证场景通过；`go-delivery-race.log` |
| 整个 HTTP 包 | 本批中途全包通过，52.884 秒；后续交付错误分类新增测试另行 race 通过。没有把中途结果说成最终整包重跑 |
| Desktop 安全 tag | `go test -tags "desktop,wv2runtime.error" -count=1 ./cmd/cyberagent-desktop ./internal/desktop ./internal/webui` 通过；`go-desktop.log` |
| 协议登记 | 同步，33 families / 1000 identifiers；`protocol-check.log` |
| API 生成一致性 | 重生成 OpenAPI 和 schema.d.ts 前后哈希一致；`api-sync.log` |

## 未完成与后续范围

- 检查点依旧是全项目恢复；快照后的无关修改也可能阻止撤销。仅修改 Preview 以忽略冲突会与持久事务和最终清单校验冲突，本批没有更改恢复算法。逐文件撤销和降低恢复对较宽任务权限的依赖需后续设计。
- 真实宿主/受控命令入口已接入，现有组件/HTTP 测试继续通过；本批未执行真实宿主命令、完整沙箱或高权限矩阵。ScriptProcess/Git/web-fetch 新预览的全部真实类型矩阵尚未完成。
- Standard Code 当前版本校验沿用既有 Go 测试；真实浏览器仅覆盖不可用/无报告状态，未建立完整 Standard Code 沙箱交付场景。
- 文件引用限已有任务的项目文件；首条消息、图片和任意系统附件、重启后草稿持久化不在本批完成范围。
- 本批结束时 F11 历史规模/任务地址尚未处理；后续实现与验证见 [第三批记录](UX_FIXES_PHASE_C_VALIDATION.md)。Web 导入已有目录和原生平台矩阵仍待处理，其余 F09/F10/F12/F14 按必读任务书保留。

本记录的活动服务状态以后续必读文档交接为准；不得假设夹具一直运行。
