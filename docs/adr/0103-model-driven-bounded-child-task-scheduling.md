# ADR 0103: 模型驱动、有界 child 任务调度

Date: 2026-08-15

## Status

Accepted for the model-proposed, Go-adjudicated child task contract described
below. Fan-out plan execution stays on the existing operator-driven flow.

## 背景 / Context

已有核心委派（最多两个 child，模型经 specialist_delegation_propose 提出、操作者
审批链准入）与批量只读 fan-out（1/2/4/6 档位，operator CLI 建计划执行）。缺少的是
模型在一个明确协议里提出 child 任务、由 Go 选择执行面并原子复核预算的完整产品路径。

## 决策 / Decision

### 1. child_task_proposal.v1 协议

每个任务携带 title/goal/skills/input_refs/dependency_ordinals/surface_hint/
turn_limit/token_limit/timeout_millis/expected_artifacts；严格解码、未知字段拒绝、
去重目标（title+goal 唯一）、依赖自环与环写入前拒绝。空 surface_hint 归一为 auto。

### 2. Go 裁决执行面

含写/审批技能（非 model.chat/list_workspace/read_file）的任务强制 core 面（≤2）；
纯只读任务按数量解析 fan-out 档位 1/2/4/6；混合 hint 与只读面上声明依赖均拒绝。
模型只能 hint，最终面由 Go 决定；操作者审阅可把 fan-out 档位压到显式上限
（1/2/4/6），模型无法自行提高。readonly 任务的权限天花板由现有 fan-out
capability 指纹（仅 list/read，无 Shell/写/网络/再委派）保证。

### 3. 提案→审阅→准入

schema v102 持久化 proposal/assignments/两类幂等操作表；同操作键回放、同语义
fingerprint 跨键去重。写入前在事务内复核：run 运行中、root 存在、core 面 child 数
≤2、总 turn/token 不超过 effectiveRootBudget（沿用 v21 预留机制）。审阅 approve/
deny 幂等；core 面准入走既有 AdmitSpecialist，并在准入后把依赖序绑定到 schema
v101 的 agent_dependency_edges（唯一唤醒收据保证重放不重复绑定）；readonly 面记录
绑定意图，不可变 fan-out 计划创建与执行仍由操作者经既有 fan-out 流程完成。

### 4. 事件与 Activity

新增 agent.child_task_proposed/reviewed/admitted 事件；Run Activity 以折叠项展示
surface/档位/任务数，绝不展示任务 goal、输入引用或期望产物。新工具
child_task_propose 属于 agent_proposal 类、自动审批、必须由 fenced root Supervisor
调用，永远只记录提案（admission_authorized=false），不创建 Agent。

## 后果 / Consequences

- 模型不能无限嵌套、动态提高档位或给只读 child 加写/网络能力；并发档位只提高
  吞吐，不提高权限。
- Desktop/Web 通过 Run Activity 看到提案状态与预算摘要；child 树与取消复用既有
  agent 图投影与逐 child 取消控制。
- fan-out 计划创建/执行的 operator 流程与预算预留不变；跨机器/云端调度仍不在范围内。

