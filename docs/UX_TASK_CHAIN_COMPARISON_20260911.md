# 完整任务链路与下一步功能建议

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-11。基于当前未提交实施树、A–S 与统一框架验收、当日打开的官方文档。CC 指 Claude Code；区分 CLI 与桌面能力。本次是只读评估，不是三款产品的同条件性能测试，也未开始实施以下建议。

**后续实施记录（同日）：** 用户随后明确授权第 1 项。普通 Thread 已接通现有持久摘要，并补充重复压缩、重启、工具证据和多后继摘要保留的验收，详见 [上下文连续性实施记录](UX_CONTEXT_CONTINUITY_VALIDATION.md)。下面的调用位置与“未开始”描述保留为评估当时的基线；它们不再表示第 1 项仍完全未实施，也不表示其他建议已完成。

**第2项后续实施（同日）：** 用户以截图选择刷新/退出/重开续接后，已实现本机草稿、引用、原提交身份持久化，以及创建/消息两阶段只读核对。真实浏览器关闭重开、丢响应和隔离API重启均有证据；原消息不重复执行，后来新稿保留。最终126项前端回归通过，详见 [重启续接实施验收](UX_RESTART_CONTINUITY_VALIDATION.md)。下表第2项的内存状态描述也成为修复前基线；原生Windows实际重开、高DPI、跨设备同步仍未据此验收，其他建议保持待选。

## 当前判断

**第3/4项后续实施（同日，最新状态覆盖下方评估基线）：** 图片粘贴/预览/删除/真实图像传入与应用预览已完成限定验收，见 [图片与预览](UX_IMAGES_PREVIEW_VALIDATION.md)。任务级审阅/Git/PR已接通并完成独立仓库与环回协议验收，见 [交付流程](UX_TASK_DELIVERY_VALIDATION.md)；其后完成 [交付与Inspector收敛](UX_DELIVERY_CONVERGENCE_VALIDATION.md)。下表第3/4项缺口描述已成为修复前基线，不能继续当作当前完全缺失。真实GitHub账号、模型自主按评论修复和原生DPI仍未据此验收；第5/6项尚未成为新的实施授权。最新剩余源码复核见工作必读v1.86。

Traverse Board 的基本编程循环已有真实证据：接入工程、实际检索/安装、文件创建、运行测试、修复失败、审阅交付和原对话继续。见 [真实工程验收](UX_REAL_AGENT_ACCEPTANCE.md)。Q 后续解决审批自动续跑、模型切换设置保持和具体失败反馈；统一框架解决对话/Inspector/设置割裂。不能重列为完全没有这些功能。

与成熟产品相比，仍缺少充分验证的长期连续使用体验，以及从需求到可合并代码的完整工作流。一个经过人工干预和模型切换的工程成功，不能证明稳定无人干预完成；UI统一也不能证明模型能力提升。不提供“完成度百分比”或未经控制的 harness 排名。

## 优先建议及验收目标

| 顺序 | 建议 | 现状与最小验收目标 |
| --- | --- | --- |
| 1 | 长任务上下文连续性 | 已有 contextmgr、项目指令、显式记忆和连续性基础。普通 Supervisor 会读取已有 summary（run_supervisor.go:712、722），但模型窗口兜底会逐条移除历史（model_context_window.go:60）；MaybeCompact 唯一生产调用位于 session/session.go:659。应先核实并接通普通 Thread 主链的摘要生命周期，再做跨窗口真实任务：保留目标、用户后续修正、待办、失败结论和证据引用。没有实测前不宣称已发生某次遗忘，也不从零另造记忆平台。 |
| 2 | 重启和未知提交的持久续接 | Thread历史和已保存失败存在服务端，普通失败后可以直接继续；前端草稿仍为 app.tsx 的 state，提交身份仍在 use-thread-turn.ts 的 QueryClient MutationCache。目标：提交响应丢失后退出/重开，恢复原任务、原请求身份和未发送内容，先核对原结果，不重复发送或执行。重开历史不等于自动执行旧请求。 |
| 3 | 图片输入与应用预览 | 普通消息附件目前发送 workspace_file 引用（use-thread-turn.ts:56），不是图片视觉输入。浏览器 Dock 仍 reserved（workbench-dock.tsx:70），虽已有 CDP/浏览器会话基础。目标：截图粘贴/删除/模型能力提示→真实模型接收图像；开发服务→预览→截图/交互检查→修复。优先复用成熟浏览器/CDP/Playwright能力，不自造浏览器引擎。 |
| 4 | 任务级审阅和 Git/PR 交付 | 已有 Diff、单文件逆向、checkpoint、Handoff、Git worktree、GitHub现有PR/CI读取与review提交。主审阅仍按selectedRun展示，普通Handoff不等于整个Thread当前代码已验证。目标：显示任务范围与当前代码版本、测试是否过期、行级反馈再修复；串起分支/worktree→review→commit/push→PR→CI→继续修复。使用现有Git与GitHub接口，所有外部写按用户意图和精确范围执行。 |
| 5 | 把已有 Plan/Deliver 和上下文控制接入普通对话 | 阶段确为Plan/Deliver；V2创建当前固定phase:deliver（app.tsx:57），已有计划流程位于高级Run/PlanDeliveryPanel。目标：普通对话可明确先规划或执行，确认计划后直接继续同任务；无需每个小修改都强制审批计划。显示正在使用的项目指令、重要上下文及删减/压缩状态，并允许用户纠正。 |
| 6 | 扩展与并行工作的真实使用闭环 | MCP Invoke、Skill安装、LSP启动/definition/references、多Agent审批/取消/汇总均有基础。优先选一个常见LSP、一个MCP、一次真实并行任务，完成配置→调用→失败反馈→汇总验收；再收敛普通入口。不把登记成功或测试fixture当作外部工具真的可用。 |

模型/协议兼容的实际任务验收应贯穿前四项。已有provider资格探测与能力状态，不再加重复“接入平台”；需要证明被支持的路径确实能携带长工具参数、处理空响应/限流/停止，并延续同一个任务。不同模型能力不应被统一UI掩盖。

上下文调用关系补核：普通 ThreadTurnService.executeSubmission 在 `internal/application/thread_turn.go:238` 调用 execution.Execute；`run_execution_handoff.go:265、318` 进入 Supervisor lease/step，未经过 session.Manager 外层。session.Manager 自身在 runChat 返回后（session.go:322）和 fallback chat 后（:639）会调用 compactAfterTurn，因此旧入口确有自动压缩；不能据此假设普通Thread入口也已触发。本项为源码确认的接入缺口，未做超长会话遗忘实测。

## 竞品参考及适用边界

- Codex 有持久 Thread resume/fork 接口；这说明恢复会话和开始新一轮是独立动作，不表示重开后无条件重放命令。参考 [App Server](https://learn.chatgpt.com/docs/app-server)。Claude Code 有保存会话、选择恢复和分支；不同界面有各自历史边界。参考 [Sessions](https://code.claude.com/docs/en/sessions)。
- Codex 工作树支持任务隔离与本地交接，审阅页提供工作区/分支/最近工作轮等范围以及行级反馈和Git操作。参考 [Worktrees](https://learn.chatgpt.com/docs/environments/git-worktrees)、[Code review](https://learn.chatgpt.com/docs/code-review)。我们有高级Git基础，差距是日常任务入口和交付串联，不能称Git完全缺失。
- Claude Code 桌面提供图片/文件上下文、应用浏览器预览、diff反馈及PR/CI工作流；CLI和桌面具体入口不同。参考 [Desktop](https://code.claude.com/docs/en/desktop)。Codex明确支持截图等图片输入，参考 [Image inputs](https://learn.chatgpt.com/docs/image-inputs)。这是目前截图驱动开发的直接参考。
- 两者都有指令/记忆和上下文管理；也有边界。Codex官方建议必须遵循的团队规则保留在AGENTS.md/版本化文档；Claude Code的摘要会省略信息，checkpoint不覆盖全部Shell、外部系统和并发修改。参考 [Codex memories](https://learn.chatgpt.com/docs/customization/memories)、[Claude memory](https://code.claude.com/docs/en/memory)、[Checkpointing](https://code.claude.com/docs/en/checkpointing)。不把竞品当作无限记忆或万能撤销。

## 可以后置的扩展

当前定时UI创建参数为 `max_model_calls:0`、`execution_mode:read_only`（scheduled-tasks-workspace.tsx:101–106），使用进程内调度。它有价值，但不等于定时自动编程或关机后云端工作。完整后台自动化、远程/云端接力、插件市场、复杂Agent团队、语音和完整双语可依实际需求后置；已有机制应先贯通，不继续扩建平行平台。

Windows实际DPI/多屏、完整键盘/屏幕阅读器与性能属于交付质量和验收缺口，不能一概叫“缺功能”。旧高级Run/Session内部表达继续收敛，但优先级低于可能造成任务上下文或请求身份丢失的风险。

## 比较方法

若要判断实际任务能力，选固定仓库提交和独立测试，覆盖小缺陷、跨文件功能、既有工程重构、截图驱动UI、审批/失败续接、超窗或重启后的长任务。记录任务正确率、用户纠正次数、意外停止/重复动作、时延、成本、额外改动和当前版本的检查证据。以相同模型比较harness时明确三者是否都支持该模型；以各自原生模型比较时标为整套产品体验，不能把模型差异归罪或归功于框架。

上述为建议顺序，并未新增实施承诺。后续以用户最新选择确定一批可交付范围，再更新工作必读；本次不修改产品代码、配置、数据或原生程序。
