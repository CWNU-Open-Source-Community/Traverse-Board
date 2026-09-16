# Traverse Board 重大能力差距复核

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-12，Asia/Hong_Kong。范围：当前 `<workspace>` 工作树，分支 `codex/ux-journey-convergence`，基线提交 `15c399c8940aeb81d3c45bb0de96759b480d02c2`，包含累计未提交实现，不代表 GitHub 默认分支或正式发布版。

本轮为源码复核、官方文档与论文比较。没有运行模型评测、修改产品代码、启动服务或执行 Git 发布。以下建议尚未获得本批实施指令；不能把建议写成已实现，也不能把未实测写成已确认故障。

## 判断

已有普通对话执行循环、规划与纠正、压缩接入、文件搜索与编辑、后台命令、LSP 工具、图像输入、浏览器预览、审阅及 Git/PR。这些不应重新列成整块缺失。

仍有重要能力未形成普通用户可持续使用的完整流程：长历史的按需回读、具备工具的子 Agent 协作、扩展的配置到消费、环境与验证反馈的连续使用，以及跨任务经验沉淀。真实任务可靠性评测不足应单独列为证据缺口。

## 1. 长任务历史检索和分层记忆：优先

当前压缩是有界规则摘录，最多 4000 字符、12 条记录、单条 1000 字符。普通 Thread 已接入，原文和工具来源保留，不是没有压缩。但当前模型工具目录未见专用的历史搜索、按消息/工具来源回读接口；UI 上下文面板不能替代模型自己的检索能力。多次后继累计摘要超过 16 KiB 时拒绝创建后继，保留旧内容，存在明确容量终点。

证据：[摘要容量](../internal/contextmgr/handoff.go#L20)、[保留记录选择](../internal/contextmgr/handoff.go#L277)、[后继容量检查](../internal/application/thread_summary_continuity.go#L66)、[现有验收边界](UX_CONTEXT_CONTINUITY_VALIDATION.md#L51)。

建议复用现有消息、工具账本与来源校验：短摘要导航，模型按需检索原文，读取结果带来源和截断说明；旧摘要归档后索引保留。不要简单提高字符上限或一次性把全部旧记录重塞进窗口。

验收：早期目标、中段纠正、旧工具失败细节均可在多次压缩、切换模型与重启后找回；来源精确；已废弃要求不冒充当前要求；检索不带来权限提升。当前没有证据可量化真实模型因此遗忘的频率。

## 2. 修改、诊断、测试和浏览器验证的连续反馈：优先

`list/read/glob/grep` 已有分页、行范围及哈希信息，并接入 Supervisor。编辑会核原哈希和匹配次数，实际 Apply 绑定已审批内容。缺口是新版本诊断反馈仍需模型另调工具；LSP 只开放已配置且健康的服务器，桌面接入仍需显式配置。

证据：[代码工具](../internal/toolgateway/agent_code.go#L244)、[主链接入](../internal/application/supervisor_tools.go#L180)、[Apply 返回](../internal/application/agent_code_tools.go#L484)、[LSP 可用性](../internal/application/supervisor_tools.go#L686)、[桌面配置](../internal/desktop/control_plane.go#L318)。

后台命令已有启动、读取、等待、stdin 和取消；当前单 Job 有 30 分钟上限，进程在重启后无法继续拥有时标记 interrupted。持久 Debug shell 另需 Debug 模式及临时授权。预览允许模型读 DOM、点击、输入和截图，并把图像交给支持视觉的模型；但只面向用户先打开的单一本机 origin，且需有效 FullAccess/Debug。不能因此宣称没有长命令或浏览器工具，也不能将它等同通用桌面操作。

证据：[命令动作](../internal/toolgateway/command_runtime.go#L26)、[进程限制](../internal/runner/command_runtime_job.go#L1082)、[Debug 终端](../internal/toolgateway/debug_terminal.go#L146)、[浏览器边界](../internal/toolgateway/browser_actions.go#L41)、[真实图像接入](../internal/application/supervisor_browser_images.go#L175)。

建议把诊断绑定实际文件版本，把新增错误与旧错误区分；用户能看清并启用已有语言服务器；在已授权任务范围内，贯通启动开发服务、发现地址、打开预览、操作和再次核验。复用现有 runner、CDP 和 LSP；不要求每次修改跑全套测试，也不要求自动回滚所有暂时的编译错误。

验收：一次真实修改引入类型错误后模型收到准确诊断并修复；一次页面修改经真实浏览器发现问题后再修；失败命令、超时和服务退出反馈准确，用户无需跨多个高级页面接续。

## 3. 工具型子 Agent：底座与能力必须分开

已确认 Specialist 有调度和并行底座，但其模型请求明确为 no-tool，禁止 Shell/网络，收到工具调用会拒绝。因此不能将这条路径宣传为多个可自主读写、测试代码的编程 Agent。

证据：[Specialist 请求](../internal/application/specialist_runner.go#L911)、[拒绝工具调用](../internal/application/specialist_runner.go#L966)。

普通 Root 有委派/子任务提案，core child_task 准入仍调用 AdmitSpecialist，沿用上述限制；Inspector 子任务面板已有真实批准/准入按钮，不能说全部只读。[child_task 准入](../internal/store/child_task.go#L357)、[操作界面](../web/src/components/run-projections.tsx#L324)。

另有 BatchDelivery 编程工具底座，支持独立 worktree、owner/lease、读取、修改、应用和提交。因此不能笼统说所有子任务都无工具。但本轮追踪中 BatchProposeChange/BatchApplyChange/BatchGitCommit 在非测试生产代码只有定义，没有实际调用者；服务本身没有 LLM 运行器，Root/Specialist 也未接这套工具路由。[Batch 能力](../internal/domain/batch_delivery.go#L372)、[工具方法](../internal/application/batch_delivery_tools.go#L266)、[服务装配](../internal/application/batch_delivery.go#L88)。准确缺口是普通 Thread 自动启动、工具执行、汇总的 coding-worker 主链。

建议优先做有界只读调查和审阅，再按需要支持隔离写入；沿用现有预算、执行身份和取消机制，不再造一套多 Agent 框架。

验收：用户在同一对话让两个子 Agent 分别调查和审阅；每个有独立工具、上下文、状态，主 Agent 能等待、纠正、取消并引用结果。写任务需明确文件所有权或独立 worktree，避免并发相互覆盖。

## 4. 扩展从设置到实际使用：实质产品缺口

Skill 有安装和执行上下文底座，但安装不代表当前任务已选择/加载；选择外部 Skill 有 CLI 路径。MCP 设置主要是状态、重新发现和禁用，创建、审批、启用仍依赖 CLI 流程；模型使用限定 Code/Deliver/root 和有效 FullAccess，规划中的只读 MCP 也受这个粗粒度边界限制。Hooks 已有声明规则和持久记录，支持 deny/annotate/narrow/record，不执行用户自定义脚本。

证据：[Skill 设置](../web/src/v2/components/advanced-settings.tsx#L34)、[Skill 选择](../internal/app/skill_command.go#L255)、[MCP 设置](../web/src/components/shared-settings-panels.tsx#L309)、[模型 MCP 边界](../internal/application/supervisor_tools.go#L506)、[Hook 动作](../internal/hooks/hooks.go#L63)。

建议先完成安装/配置、检查、启用、任务选择、真实调用、错误定位与停用这一条流程。规划期只读能力应按实际工具权限审视，而非默认要求扩大为完全访问。脚本 Hook 属于需要设计的能力扩展，不能把现有声明式 Hook 改名后当作已经支持。

验收：新用户不打开 CLI 就能接入一个确实可用的 MCP 和 Skill；模型实际调用结果可追踪；配置失败能定位；同一只读需求不因无关执行权限被挡住。

## 5. 跨任务项目经验：增强项

user/project memory 已有显式创建、版本化、禁用和删除。当前不会自动从任务总结或工具结果提炼；模型 `note_create` 限当前 Run。可补候选经验、来源、修正/遗忘和新任务使用验证；优先普通文件或已有记忆存储，不预设需要向量数据库。

证据：[已有项目记忆边界](context-continuity.md#L63)、[Run 内笔记](../internal/toolgateway/structured_memory.go#L140)。验收例：修正一次项目特有构建命令后，新任务能正确使用，用户可以改掉错误经验。记忆属于上下文，不成为永久执行授权。

## 6. 已有的 Git/PR、恢复与回退

不能重新列为没有 Git/worktree/PR 或没有恢复检查点。普通任务已有选定文件提交、推送、独立 worktree 创建与导入；新 worktree 从已提交版本建立，当前任务仍留原目录，不等于每个子 Agent 自动有独立环境。[worktree 实现](../internal/application/thread_git_worktree.go#L21)、[普通 Git 页面](../web/src/v2/components/task-git.tsx#L215)。

PR 的 CI、日志、评论能带精确证据引用回草稿，由用户发送；模型有 Run 绑定的 GitHub 证据工具。这是有效的人机协作流程，不是没有反馈链路。自动监控、自主修复并再次推送不属于已证明完成的能力。[反馈入口](../web/src/v2/components/task-pull-request.tsx#L135)、[回原草稿](../web/src/v2/components/conversation.tsx#L393)、[模型读取](../internal/application/agent_code_tools.go#L132)。

工作区已有 Undo/Redo/Rewind 的预览与确认路径：[恢复面板](../web/src/components/workspace-checkpoint-panel.tsx#L293)。其受任务状态、冲突和权限约束，不能宣称可以还原任意外部副作用。既有验收含真实本地 Git、bare remote、worktree 和本地 GitHub 协议夹具；真实 GitHub 账号、外部 CI 及模型按审阅意见修改后再次推送的完整实测仍是边界。[交付验收](UX_TASK_DELIVERY_VALIDATION.md#L70)。

## 7. 真实任务评测：必须单独补的证据

已有真实模型完成 Node.js 工程的记录，但含人工纠正与路由切换。后来的摘要、Plan 等多项验收主要使用固定协议响应，证明产品链路能够工作。它们不能支持无人干预完成率、与 Codex/Claude Code 的能力差值、企业级或高完成度百分比。

证据：[真实工程干预边界](UX_REAL_AGENT_ACCEPTANCE.md#L76)、[Plan 固定响应](UX_PLAN_CONTEXT_VALIDATION.md#L13)。该真实工程报告中的历史修复待办已有后续批次完成，不能整份重新当成当前缺陷表。

建议先选择 5–10 个代表性真实任务：已有仓库修 Bug、跨文件小重构、带浏览器验收的功能、长任务纠正、PR 反馈修复。固定任务、起始代码、预算和人工干预规则，记录完成情况、误报、成本和中断原因。跨产品比较报告各自实际模型；要归因 harness，则尽可能用相同模型比较 Traverse 与简洁基线。小样本用于找瓶颈，不外推普遍成功率。复用已有黑盒/真实旅程能力，不新建庞大评测平台。

## 官方资料与论文带来的判断

- Codex 的子 Agent 支持并行委派、跟进和回收；其 worktree 用于隔离并行任务。应对齐用户能完成的流程。[子 Agent](https://learn.chatgpt.com/docs/agent-configuration/subagents)、[worktree](https://learn.chatgpt.com/docs/environments/git-worktrees)。
- Claude Code 把收集上下文、行动和验证串成循环；代码智能需相应插件，其项目指令与自动记忆是不同机制。[执行循环](https://code.claude.com/docs/en/how-claude-code-works)、[记忆](https://code.claude.com/docs/en/memory)。
- Codex 桌面浏览器支持打开、操作和验证页面；集成终端可提供当前输出。这里是桌面能力，不泛化为每个 CLI/IDE 都有同样浏览器。[浏览器](https://learn.chatgpt.com/docs/browser)、[终端](https://learn.chatgpt.com/docs/integrated-terminal)。
- SWE-agent 原论文在固定模型下研究动作、反馈和上下文设计。对本项目的启示是减少无效步骤、提高反馈质量，并以实际任务结果验证收益。论文的 2024 年结果不能直接用作今天产品的排名。[论文](https://arxiv.org/html/2405.15793v3)。
- 同团队后来的 mini-SWE-agent 采用简洁的 shell 执行与线性历史。这说明应实测复杂机制的价值；该项目的基准宣传不能替代我们同条件比较。[官方项目](https://github.com/SWE-agent/mini-swe-agent)。
- 成熟产品也有限制：Claude Code 的 rewind 不覆盖所有 Shell 或外部改动，不应据此为本项目承诺万能回滚。[checkpointing](https://code.claude.com/docs/en/checkpointing)。

## 建议顺序

先建立小规模真实任务基线，再优先补历史检索和编辑/验证反馈；随后做工具型子 Agent 与扩展配置闭环，项目经验沉淀按使用需求推进。草稿硬退出、应用附件缓存治理和原生 Windows 验收仍是既有可靠性事项，Go 缓存迁到 D 盘不等于它们已解决。

当前源码不足以支持更换 Go、重写 harness 或全面重建工具系统的决定。先复用已有实现和成熟工具，减少为同一普通操作拆出的配置/提案/模式切换；保留真正对应权限、冲突、重复执行风险的检查。新增流程必须能解释它改善了哪个真实任务。
