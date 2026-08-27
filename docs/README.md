# Traverse Board · 针路簿文档导航 / Documentation Index

[中文](../README.md) | [English](../README.en.md)

本目录把面向用户的说明、当前工程状态和历史开发账本分开。首次阅读请从“产品与使用”开始；恢复开发任务时再读取“当前工程上下文”；逐切片记录只用于追溯，不是待办清单。

This directory separates user-facing documentation, current engineering state, and historical ledgers. Start with Product and Usage. Read Current Engineering Context when resuming development. Slice ledgers are historical evidence, not a backlog to replay.

## 产品与使用 / Product and Usage

| 文档 | 用途 |
|---|---|
| [README 中文](../README.md) / [README English](../README.en.md) | 产品定位、核心能力、快速开始和历史阶段索引 |
| [产品范围 / Product Scope](PRODUCT_SCOPE.md) | 当前核心范围、可选附加能力和扩展接口 |
| [Pre-1.0 产品收敛](convergence/README.md) | Surface tier、协议冻结等级、Durable Operation 边界、规范词汇与有界后续 Issue |
| [SQLite 全新安装基线](schema-baseline.md) | 严格空库准入、事务化 latest-schema 建库、备份/旧二进制回滚与失败恢复 |
| [使用手册 / Usage](usage.md) | CLI、Provider、Workspace、Run、审批和操作者工作流 |
| [Web Evidence / Web 证据](web-evidence.md) | SearXNG 配置、Run 网络 allowlist、Search/Fetch/Citation、故障与条款边界 |
| [Workspace Checkpoints](workspace-checkpoints.md) | 检查点时间线、预览、Undo/Redo/Rewind、独立 Fork 与故障处理 |
| [Drydock 工作目录](drydock.md) | Run-owned worktree、Workspace Trust、精确检查点、审阅交付与保守清理 |
| [Standard Code 原子预设](standard-code-preset.md) | 一键 Code/Plan 组合、Local/显式 Docker、Workspace Trust、暂停配置与故障恢复 |
| [Standard Code packaged E2E](standard-code-packaged-e2e.md) | 四个固定 Git 仓库、40 项攻击矩阵、portable ZIP bootstrap 与完整发布门边界 |
| [Standard Code packaged product E2E](standard-code-product-e2e.md) | 从零参数发布 EXE 验证四语言闭环、交付投影、Thread 连续性与 Windows UX 证据 |
| [Standard Code Beta release gate](standard-code-release-gate.md) | 将同一候选的 packaged bootstrap、产品与安全证据聚合为唯一 fail-closed 发布结论 |
| [高风险升级 / Durable Risk Escalation](risk-escalation.md) | Standard Code 精确风险提案、一次/有界 Run 授权、持久等待恢复与不确定性处理 |
| [Windows Local Sandbox](local-sandbox-windows.md) | AppContainer/WFP/Job/ACL 隔离、readiness 证据、恢复与操作说明 |
| [高级 Git 工作流 / Advanced Git](git-advanced.md) | 稳定 hunk、stash、rebase/cherry-pick/bisect、受管 worktree、审批与恢复 |
| [GitHub Review Provider](github-review.md) | GitHub App Device Flow、PR/CI 证据、本地映射、审批回写与恢复 |
| [可交付多代理 / Deliverable Batches](batch-delivery.md) | child Worktree、缩权工具、邮箱、交付收据、复核、合并与恢复 |
| [MCP Client、Plugin 与 Hooks / Extensions](extensions.md) | 两阶段 MCP、签名 Plugin、凭证引用、受限 Hook、Desktop/HTTP 控制与残余风险 |
| [真实浏览器 UI 证据 / UI Evidence](ui-evidence.md) | 源码/配方绑定、真实 Edge 矩阵、产物、失败语义与操作手册 |
| [Windows Desktop 计划](DESKTOP_PLAN.md) | 桌面架构、发布门和仍未开放的能力 |
| [Skill 包计划](SKILL_PACKAGE_PLAN.md) | 惰性 Skill 导入、校验和未来分发边界 |

## 架构与协议 / Architecture and Protocols

| 文档 | 用途 |
|---|---|
| [架构说明 / Architecture](architecture.md) | Go 单一控制平面、Run-centric 领域和跨语言边界 |
| [品牌迁移 / Branding Migration](branding/README.md) | Traverse Board · 针路簿品牌、展示词汇与兼容迁移矩阵 |
| [LSP 语义代码智能 / Code Intelligence](code-intelligence.md) | 审查配置、十项只读工具、进程生命周期、证据失效与真实 Server 验证 |
| [HTTP API](http-api.md) | 认证 API 行为与 DTO 边界 |
| [OpenAPI](openapi.json) | 由 Go 生成并受测试保护的机器可读合同 |
| [错误模型 / Errors](errors.md) | 稳定错误类别、CLI 退出码和 HTTP 映射 |
| [ADR 0118 / Workspace Checkpoints](adr/0118-transactional-workspace-checkpoints.md) | 事务边界、内容寻址、三方恢复、崩溃收敛与权限模型 |
| [ADR 0119 / Deliverable Batch Agents](adr/0119-deliverable-batch-agents.md) | child 所有权、一次性 authority、提交 WAL、独立复核与本地合并队列 |
| [ADR 0120 / UI Evidence](adr/0120-source-bound-real-browser-ui-evidence.md) | 真实浏览器所有权、受限 CDP、来源绑定、脱敏与 CI 决策 |
| [ADR 0121 / Scheduled Monitoring](adr/0121-durable-scheduled-monitoring-and-structured-diagnostics.md) | 持久任务、租约 fencing、预算、权限快照与结构化诊断边界 |
| [ADR 0122 / Advanced Git](adr/0122-go-owned-advanced-git-lifecycle.md) | Go-owned 高级 Git 合同、状态机、Checkpoint、worktree 所有权与默认拒绝边界 |
| [ADR 0123 / GitHub Review](adr/0123-go-owned-github-review-provider.md) | GitHub 凭据、证据图、远端写审批、幂等恢复与默认拒绝边界 |
| [ADR 0124 / Traverse Board Branding](adr/0124-traverse-board-branding-migration.md) | 已接受品牌、仓库 slug、兼容身份与实施边界 |
| [ADR 0125 / Windows Executable Name](adr/0125-traverse-board-windows-executable-name.md) | `v0.1.0-rc.2` 对外 EXE 文件名与发布边界 |
| [ADR 0126 / Legacy v97 Compatibility](adr/0126-legacy-v97-docker-trigger-compatibility.md) | 精确旧校验和、v125 trigger 修复、数据保留与失败关闭边界 |
| [ADR 0127 / Workspace Access](adr/0127-workspace-access-permission-contract.md) | Standard Code 工作区权限上限、沙箱 readiness、revision fencing 与失败关闭边界 |
| [ADR 0128 / Capability Readiness](adr/0128-go-owned-run-capability-readiness.md) | Go-owned 选择/运行时可用性、稳定阻塞码、修复动作、隐私与版本边界 |
| [ADR 0129 / Drydock Workspaces](adr/0129-run-owned-drydock-workspaces.md) | Run-owned worktree、Workspace Trust、崩溃恢复、review-only 交付与精确清理边界 |
| [ADR 0130 / Windows Local Sandbox](adr/0130-windows-local-sandbox-backend.md) | Windows AppContainer/WFP/Job/ACL 边界、证据、恢复与产品 gate |
| [ADR 0131 / Standard Code Docker](adr/0131-standard-code-docker-network-none-backend.md) | 固定 Docker `network=none` 后端、Drydock 投影、readiness 与恢复边界 |
| [ADR 0132 / Thread Identity](adr/0132-thread-identity-run-succession.md) | 稳定 Thread 身份、Run succession、生命周期与无 authority 继承边界 |
| [ADR 0133 / Item-level Model Streaming](adr/0133-item-level-model-tool-streaming.md) | Provider-neutral response/item/call 生命周期、Go 工具执行权威、脱敏公开/持久投影与恢复语义 |
| [ADR 0134 / Unified Thread Transcript](adr/0134-unified-thread-transcript.md) | 多 Run 主叙事、游标、live/durable 替换、虚拟化与 Composer 布局合同 |
| [ADR 0135 / Pre-1.0 Product Convergence](adr/0135-pre-1-0-product-convergence.md) | Surface 支持等级、Schema/Protocol 冻结等级、最小 Durable Operation 抽象和兼容词汇 |
| [ADR 0136 / Standard Code Preset](adr/0136-atomic-standard-code-preset.md) | 原子预设、幂等恢复、暂停后配置、显式后端选择与非授权响应边界 |
| [ADR 0137 / Web Evidence](adr/0137-go-owned-web-evidence.md) | Run 级网络授权、Search/Fetch/Citation 状态机、SSRF/解析器/法律边界与安全投影 |
| [ADR 0138 / Standard Code packaged E2E](adr/0138-standard-code-packaged-e2e-foundation.md) | 摘要绑定固件、攻击矩阵、确切 ZIP bootstrap 与不把未执行项伪装成通过的发布语义 |
| [ADR 0139 / Clean-install Schema Baseline](adr/0139-clean-install-schema-baseline.md) | 严格空库证明、生成式 schema 等价、原 migration ledger、原子失败与旧二进制回滚边界 |
| [ADR 0140 / Durable Risk Escalation](adr/0140-durable-risk-escalation.md) | 精确风险 scope、有界 grant、Supervisor 等待/恢复、write-ahead fence 与漂移失效 |
| [ADR 0141 / Durable-operation Identity Pilot](adr/0141-minimal-durable-operation-identity.md) | 无存储共享身份值对象、长度分隔摘要、Run creation / Scheduled Job 回放试点与领域 authority 边界 |
| [ADR 0144 / Standard Code Release Gate](adr/0144-standard-code-release-gate-aggregation.md) | 同候选 packaged/product/security 证据聚合、Draft Release 传递、确定性复核与 Beta-only 发布边界 |
| [ADR 索引](adr/) | 权限、持久化、执行、浏览器、Desktop 等架构决策 |

## 当前工程上下文 / Current Engineering Context

以下文档较长，面向维护者和恢复中的 Agent。按顺序读取，避免上下文压缩后重复已完成工作。

1. [PROJECT_MEMORY.md](PROJECT_MEMORY.md)：当前恢复检查点、禁止重复项和最近决策。
2. [PROJECT_STATUS.md](PROJECT_STATUS.md)：当前能力、验收证据、已知限制和发布阻塞项。
3. [TASK_BOOK.md](TASK_BOOK.md)：任务结构、切片规则和尚未完成的活跃计划。
4. [PROGRESS_BOOK.md](PROGRESS_BOOK.md)：按时间追加的完整历史记录；只在需要追溯某一切片时读取。

These files are intentionally detailed. Read them in the order above when resuming work so completed slices are not repeated after context compaction.

## 历史与指标 / History and Metrics

根 README 的[“历史开发记录”](../README.md#历史开发记录)提供可读的阶段索引和旧双指标快照。完整证据仍在 `PROGRESS_BOOK.md` 与 `PROJECT_STATUS.md` 中。

历史百分比是基于当时任务书的工程估算，不是性能 Benchmark、语义版本承诺或正式发布证明。CTF/Cyber 自动化旧百分比已经退役；该方向现在属于可选附加范围，见[产品范围](PRODUCT_SCOPE.md)。

The historical percentages are roadmap estimates, not performance benchmarks, semantic-version promises, or release evidence. The old CTF/Cyber automation metric is retired because that work is now optional add-on scope.

## 文档维护规则 / Documentation Rules

- 产品能力声明以当前代码、测试、`PROJECT_STATUS.md` 和相关 ADR 为准。
- `PROGRESS_BOOK.md` 与 ADR 只追加或显式标记 superseded，不重写历史来匹配当前叙事。
- 新增用户能力时同步更新 README、Usage 和 Product Scope；新增权限或所有权边界时先更新 ADR。
- 新增产品 Surface、持久协议或用户主概念时同步更新 `docs/convergence/`；inventory 只描述边界，不能授予 authority。
- 不在文档、示例或截图中提交 API key、控制令牌、绝对用户路径或 Provider 原始响应。
- Keep Chinese and English product entry points aligned; additional README language variants are intentionally out of scope.
