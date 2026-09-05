# ADR 0003: Go-owned Run Execution Modes

- Status: Accepted
- Date: 2026-07-13

## Context / 背景

CyberAgent Workbench needs a product-level switch between coding work and authorized cyber work, plus an explicit planning stage before delivery. Treating either switch as a prompt-only convention would make recovery ambiguous and could accidentally imply permissions that were never granted.

CyberAgent Workbench 需要在编程工作与经授权的网络安全工作之间切换，同时支持先规划、后交付。若只用提示词约定这些状态，重启恢复会产生歧义，也可能让模型误以为模式名称本身授予了权限。

## Decision / 决策

Each Run has one append-only `run_mode.v1` snapshot with two orthogonal axes:

- `ExecutionSurface`: `code` or `cyber`.
- `ExecutionPhase`: `plan` or `deliver`.

Permissions, approval state, network scope, tool availability, and child-Agent admission remain independent Go-owned controls. A mode never grants a capability.

每个 Run 都有一份只追加的 `run_mode.v1` 快照，包含两个正交轴：

- `ExecutionSurface`：`code` 或 `cyber`。
- `ExecutionPhase`：`plan` 或 `deliver`。

权限、审批状态、网络 Scope、工具可用性和子 Agent 准入继续由 Go 独立控制。任何模式都不授予能力。

The surface, Profile, Workspace scope, protocol version, and policy version are immutable within a Run. Moving between Code and Cyber requires a new Run. Network scope has one narrow exception: an operator may append exact public HTTPS hostnames through the separately audited, revision-bound transition while the Run is `created` or `paused` and has no active execution lease. That transition cannot remove a hostname, grant a wildcard, or alter any other mode field. The phase follows the same quiescent boundary. Operation keys are persisted only as domain-separated digests, and exact replay returns the existing revision.

工作面、Profile、Workspace Scope、协议版本和策略版本在一个 Run 内保持不可变。Code 与 Cyber 之间切换必须创建新的 Run。网络 Scope 只有一个窄化例外：操作者可在 Run 为 `created` 或 `paused` 且没有活动 execution lease 时，通过独立审计、绑定 revision 的转换追加精确公网 HTTPS 主机名；该转换不能删除目标、授予通配符或改变其他模式字段。执行阶段遵循相同静止边界。操作键只以域分隔摘要持久化，相同意图重放返回已有 revision。

Go loads and validates the mode snapshot inside the Supervisor transaction. `plan` may reason and create the already-approved structured memory records, but it cannot complete a Run. Model `finish` is repaired once through the existing bounded lifecycle protocol; operator completion and Store finalization also fail closed. The built-in Plan/Delivery Skill will provide workflow guidance on top of this state machine, not replace it.

Go 在 Supervisor 事务内加载并校验模式快照。`plan` 可以推理并创建已经开放的结构化记忆记录，但不能完成 Run。模型返回 `finish` 时只允许经过现有有界生命周期协议修复一次；操作者完成和 Store 最终提交同样失败关闭。后续 Plan/Delivery 内置 Skill 只在该状态机之上提供工作流指导，不能替代状态机。

Legacy databases and API callers that predate this ADR default to `code/deliver`. The same persisted revision is projected through CLI, TUI, HTTP/OpenAPI, and TypeScript.

旧数据库以及未携带新字段的调用方统一兼容为 `code/deliver`。CLI、TUI、HTTP/OpenAPI 与 TypeScript 投影同一份持久化 revision。

## Consequences / 影响

- A Run cannot silently drift from coding into cyber activity.
- Plan and Delivery are recoverable domain state rather than transient prompt wording.
- Mode changes are auditable and replay-safe without exposing raw operation keys or scope targets in events.
- Existing permission, Policy, budget, approval, lease, and Sandbox boundaries remain authoritative.
- Changing surface creates a separate Run and event history by design.
- Web mode mutation, model-selected phase changes, and the complete three-direction planning workflow remain separate future slices.

- Run 不能从编程任务静默漂移到网络安全任务。
- Plan 与 Delivery 是可恢复的领域状态，而不是临时提示词。
- 模式变更可审计、可安全重放，事件中不暴露原始操作键或 Scope 目标。
- 既有权限、Policy、预算、审批、租约和 Sandbox 边界继续拥有最终决定权。
- 切换工作面会按设计形成独立 Run 与事件历史。
- Web 模式写入口、模型自主切换阶段和完整三方向规划工作流属于后续独立切片。
