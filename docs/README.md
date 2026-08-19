# Prayu 文档导航 / Documentation Index

[中文](../README.md) | [English](../README.en.md)

本目录把面向用户的说明、当前工程状态和历史开发账本分开。首次阅读请从“产品与使用”开始；恢复开发任务时再读取“当前工程上下文”；逐切片记录只用于追溯，不是待办清单。

This directory separates user-facing documentation, current engineering state, and historical ledgers. Start with Product and Usage. Read Current Engineering Context when resuming development. Slice ledgers are historical evidence, not a backlog to replay.

## 产品与使用 / Product and Usage

| 文档 | 用途 |
|---|---|
| [README 中文](../README.md) / [README English](../README.en.md) | 产品定位、核心能力、快速开始和历史阶段索引 |
| [产品范围 / Product Scope](PRODUCT_SCOPE.md) | 当前核心范围、可选附加能力和扩展接口 |
| [使用手册 / Usage](usage.md) | CLI、Provider、Workspace、Run、审批和操作者工作流 |
| [Workspace Checkpoints](workspace-checkpoints.md) | 检查点时间线、预览、Undo/Redo/Rewind、独立 Fork 与故障处理 |
| [可交付多代理 / Deliverable Batches](batch-delivery.md) | child Worktree、缩权工具、邮箱、交付收据、复核、合并与恢复 |
| [Windows Desktop 计划](DESKTOP_PLAN.md) | 桌面架构、发布门和仍未开放的能力 |
| [Skill 包计划](SKILL_PACKAGE_PLAN.md) | 惰性 Skill 导入、校验和未来分发边界 |

## 架构与协议 / Architecture and Protocols

| 文档 | 用途 |
|---|---|
| [架构说明 / Architecture](architecture.md) | Go 单一控制平面、Run-centric 领域和跨语言边界 |
| [HTTP API](http-api.md) | 认证 API 行为与 DTO 边界 |
| [OpenAPI](openapi.json) | 由 Go 生成并受测试保护的机器可读合同 |
| [错误模型 / Errors](errors.md) | 稳定错误类别、CLI 退出码和 HTTP 映射 |
| [ADR 0118 / Workspace Checkpoints](adr/0118-transactional-workspace-checkpoints.md) | 事务边界、内容寻址、三方恢复、崩溃收敛与权限模型 |
| [ADR 0119 / Deliverable Batch Agents](adr/0119-deliverable-batch-agents.md) | child 所有权、一次性 authority、提交 WAL、独立复核与本地合并队列 |
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
- 不在文档、示例或截图中提交 API key、控制令牌、绝对用户路径或 Provider 原始响应。
- Keep Chinese and English product entry points aligned; additional README language variants are intentionally out of scope.
