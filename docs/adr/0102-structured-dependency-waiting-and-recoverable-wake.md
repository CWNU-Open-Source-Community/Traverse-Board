# ADR 0102: 结构化 Agent 依赖等待与可恢复唤醒

Date: 2026-08-15

## Status

Accepted for the durable, Run-scoped dependency contract described below.
Model-driven child scheduling (Issue #51) consumes this contract; arbitrary
child creation and distributed scheduling remain out of scope.

## 背景 / Context

“任务 A 等待任务 B”此前只有两段不完整的实现：进程内同步 waitgraph 只能
检测单次调用栈里的工具/Agent 回调环；子 Agent 的完成/失败只通过 inbox 通知
消息到达父节点，没有持久化的等待边、没有 deadline、没有唯一的唤醒收据。
因此进程在“写 edge、目标完成、提交唤醒”之间崩溃时，等待方可能永远挂起或
被重复唤醒；父等子、子反等父、Tool/RAG 反向依赖和无限 Running 也没有稳定的
死锁/活锁诊断。

## 决策 / Decision

### 1. 版本化持久等待边

`agent_dependency.v1` 边由 source（kind+id）、target（kind+id）、reason、
deadline、generation 与 failure_policy（fail|notify）组成，状态是 5 值闭集：
`wait|satisfied|failed|cancelled|expired`（复用 AgentDependencyState，补齐 wait
与 expired）。schema v101 用 `agent_dependency_edges`（对 open 边按
run+source+target+generation 唯一）、`agent_dependency_wakes`（每条边至多一张
收据，UNIQUE(edge_id)）和 `agent_dependency_edge_operations`（操作键幂等）
持久化。v1 只接受 run 内真实 agent 节点作为端点，跨 Mission 边写入前拒绝。

### 2. 写入前图校验

`internal/waitgraph` 新增持久图校验：自环、两节点环、多节点环（DFS 可达性）、
下层运行时（tool/retriever/store/runner）→ Agent 反向等待、以及最长链深度
（64）都在写入事务内、基于已持久化的 open 边完成；环错误映射为稳定
`CONFLICT`、反向等待为 `POLICY_DENIED`、容量为 `RESOURCE_EXHAUSTED`。

### 3. 恰好一次唤醒与幂等恢复

结算目标（satisfied/failed 按 policy）、取消源（父取消扇出）、deadline 过期与
崩溃恢复共用同一 settle 事务：先插入唯一唤醒收据（回放返回已有收据、绝不
二次唤醒），再 CAS 更新边状态，随后向 agent 源投递依赖通知消息；等待中的源
按既有 wake 语义转 ready，fail 策略下转 failed（带 finished_at）。
`ReconcileDependencyEdges` 以目标节点终态、run 终态与 deadline 决定每个 open
边的结局；Run 进入终态时由 RunService 钩子自动扇出取消。

### 4. 稳定 deadlock/livelock 诊断

无进展 deadline 过期前发出 `dependency.deadlock_detected`（含边数）；同一源
重复 wake→rewait 超过 64 次判定轮询活锁，发出 `dependency.livelock_detected`
并以稳定 `LIVELOCK` 错误拒绝新等待。新增 apperror 码 `DEADLOCK`/`LIVELOCK`。
`DetectDependencyStalls` 提供只读诊断投影，不修改账本。

### 5. Activity 折叠展示

Run Activity 新增 `dependency` kind：等待已记录/已满足/已失败/已取消/已超时/
死锁/活锁七种事件映射为折叠项，detail 只含 source→target 身份与 reason，
绝不透传 raw child output。Web 时间线新增状态标签与图标。

## 后果 / Consequences

- 模型只能提出依赖意图；Go 在写入前验证图、预算、Scope 与 owner，外部
  Tool/RAG 结果不能反向调用等待中的同一 Agent。
- 崩溃恢复与并发结算都锚定在唯一唤醒收据上，不丢事件、不双重消费。
- #51（模型驱动 child 调度）可以直接消费本合同的 open 边、唤醒收据与
  诊断投影；本决策不引入分布式调度、goroutine 锁代替业务依赖图或任意
  child 创建。

