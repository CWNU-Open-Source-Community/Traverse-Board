# ADR 0112: 通用诊断、运行验证与审阅 Skill

Date: 2026-08-18

## Status

Accepted for the embedded Registry and schema-v110 root context ledger.

## 背景 / Context

基础 Profile Skill 能说明如何编码、学习、审阅和写脚本，但缺少跨模式复用的诊断、
真实运行证据、最小可信检查、简化和安全审阅。尤其 UI 修改容易只验证源码或构建，
没有把真实页面截图绑定到启动来源，因而旧样式残留仍可能被误报为已修复。

## 决策 / Decision

新增六项 Skill 并增强现有 `review`，形成七个优先通用能力：

- `doctor`: 双 Surface、全 Profile、root、Plan-only；只报告 Provider、Harness、
  Workspace、Sandbox、网络 Scope、工具与 Skill 兼容性，不自动修复。
- `debug`: 双 Surface、全 Profile、root、Plan/Deliver；root 建有界时间线并区分
  model/tool/permission/application/infrastructure，只有 Deliver 可实施修复。
- `run-verify`: Code/Script Profile、双 Surface、root、Deliver-only；固定启动来源并
  验证真实 UI/CLI/工具路径，Cyber 只允许既有 Policy 下的本地 admitted sandbox。
- `review@1.3.0`（当前 `1.4.0`）: Code/Review Profile；增加 merge-base、完整 diff、调用链、生命周期、
  并发、恢复、测试强度与 confirmed/inferred/unverified 证据分级。
- `focused-checks`: Code/Review/Script Profile、双 Surface、root、Deliver-only；按改动
  映射最小可信测试、构建、快照、文档、安全与恢复检查，并定义扩大回归的触发条件。
- `simplify`: Code Profile/Surface、root、Deliver-only；功能验证后才做简化，删除前
  必须检查调用点、生成代码、反射、注册、build tag、平台、配置、迁移和外部入口。
- `security-review`: Code/Review/Script Profile、双 Surface、root、两阶段；默认只读，
  覆盖注入、鉴权、Secret、工具授权、Sandbox、网络和持久化风险。

这些通用 Skill 采用 root-only 交付，以便与一个 root/Specialist Profile Skill 组合，
不突破每个 Specialist Attempt 最多一个父选择指南的既有上限。root 可以通过原有受控
委派协议派发窄证据任务，但 Skill 不创建委派、工具或修复权限。

`run-verify` 的首个内建扩展为 `ui-evidence`：每份截图/GIF 必须记录固定 commit 或
dirty-worktree fingerprint、启动配方、viewport/scale、route、page state、theme、
locale、fixture、时间、console 结果与相关请求失败。源码修改、构建成功或 detached
mockup 均不能冒充真实页面证据。

schema v110 新增独立的 mode-bound root preparation/commit 表。完整 selection 仍由
schema v39 固定；Go 每轮从内嵌 Registry 精确重建兼容子集，持久化 mode snapshot、
selection 总数、交付数与摘要。空子集是合法且可审计的事实，从而支持 Plan-only
`doctor` 与 Deliver-only 能力，同时不改变 schema-v40 历史记录。

## 后果 / Consequences

- 本决策把 Registry 从五项增至十一项；ADR 0113 随后加入第十二项
  `run-skill-generator`。`review` 当时升为 `1.3.0`，原 `1.2.0` 被内嵌归档；
  `code-intel-lsp.v1` 接入后又升为 `1.4.0`，`review@1.3.0` 与
  `focused-checks@1.0.0` 继续作为只读历史版本保留。
- 六个新增名称成为内置保留名；历史同名外部 selection 仍可按原 operation key 精确
  回放，但新的外部 selection 会冲突拒绝，避免内置与外部来源混淆。
- 所有正文仍是 guidance-only；工具声明不授权，真实启动、截图、修复或删除仍需
  Go Policy、Scope、Approval、Tool Gateway 与对应生命周期操作。
- 本 ADR 本身不扩大外部包权限；后续 ADR 0113/schema v111 仅保存模式元数据，仍不
  因安装而授予选择、正文交付或工具能力。
