# ADR 0111: 内置 Skill 的模式兼容与调用策略

Date: 2026-08-18

## Status

Accepted. ADR 0113 subsequently extends the same metadata model to the
schema-v111 external-package installation ledger.

## 背景 / Context

原有 `skill.v1` 只声明兼容 Profile。root 每轮接收完整已选集合，而 Specialist
是否接收指导由 `code/review/learn/script` 名称表和 Code/Cyber 分支隐式决定。
这不能清楚表达同一 Profile 下的执行表面、Plan/Deliver 阶段、接收角色，以及
操作者选择和模型建议之间的不同准入边界。

## 决策 / Decision

### 1. 兼容性是四维交集

当前内置 Manifest 以 `skill.v1` 的可选、向后兼容字段声明：

- `profiles`: 工作流意图，取 `code|review|learn|script`；
- `surfaces`: 产品执行表面，取 `code|cyber`；
- `phases`: Run 当前阶段，取 `plan|deliver`；
- `roles`: 正文接收者，取 `root|specialist`。

一次正文交付必须同时命中四个集合。三个新增模式数组必须一起出现、非空、去重，
并按上面的规范顺序编码。它们只限制上下文交付，不授予工具、网络、文件、进程或
Provider 权限。

### 2. 调用来源与正文交付分开判断

- `user_invocable=true`: 操作者来源的控制面请求可以选择该 Skill；
- `model_invocable=true`: 受控选择器可以接受模型来源的候选；它本身不会让模型
  自动选择，也不创建新的选择入口；
- `explicit_only=true`: 必须由操作者明确点名并固定，隐式用户推荐和模型来源都
  拒绝。该组合要求 `user_invocable=true` 且 `model_invocable=false`。

每个 mode-aware Skill 至少开放一种调用来源。调用许可仅决定“能否进入已选集合”，
模式兼容再独立决定“本轮是否能把正文交给这个角色”。

### 3. 当前内置矩阵

| Skill | profiles | surfaces | phases | roles | 调用策略 |
| --- | --- | --- | --- | --- | --- |
| `code` | code | code | plan, deliver | root, specialist | user + model |
| `review` | code, review | code | plan, deliver | root, specialist | user + model |
| `learn` | learn | code | plan, deliver | root, specialist | user + model |
| `script` | script | code, cyber | plan, deliver | root, specialist | user + model |
| `plan-delivery` | 全部 | code, cyber | plan, deliver | root | explicit user only |
| `doctor` | 全部 | code, cyber | plan | root | user + model |
| `debug` | 全部 | code, cyber | plan, deliver | root | user + model |
| `run-verify` | code, script | code, cyber | deliver | root | user + model |
| `focused-checks` | code, review, script | code, cyber | deliver | root | user + model |
| `simplify` | code | code | deliver | root | user + model |
| `security-review` | code, review, script | code, cyber | plan, deliver | root | user + model |

`plan-delivery` 因此不会下发给 Specialist，也不能由模型隐式激活。Cyber 不再依赖
硬编码名称排除普通 Code 指导；兼容矩阵直接给出结果。

### 4. 运行时与历史兼容

创建新 selection 时读取 Run 的当前 Surface/Phase，并校验调用来源。root 每轮根据
当前 Run mode 重组上下文；Specialist 从父 selection 的精确固定版本中按 Manifest
元数据挑选至多一个条目，不再使用当前版本名称表。

`skill_selection.v1` 仍固定完整且不可变的操作者选择。schema-v110 另建 mode-bound
root preparation/commit 账本，将当前 mode snapshot、selection 总条目数与本轮实际
交付子集分别持久化；子集可以为空，但必须由 Go 使用精确固定版本重新计算。这样
Plan-only 与 Deliver-only Skill 可以共存，而 schema-v40 历史 preparation 保持原义。

内嵌 `1.0.0`、`1.1.0` 与 `review@1.2.0` 历史版本保持原字节和指纹。缺少新字段的 legacy Manifest
采用保守策略：只能由操作者显式选择；root 保持旧 Profile 行为；Specialist 保持旧
Code 同名映射与 Cyber Script-only 映射。

schema-v69 外部安装账本在本决策落地时尚不保存新增字段，因此当时对 mode-aware
导入失败关闭。后续 ADR 0113/schema v111 已用独立迁移保存完整字段，并保持 legacy
指纹不变。

## 后果 / Consequences

- CLI `skill list/show/package validate` 可观察有效模式与调用策略。
- Surface、Phase、Profile、Role 和调用来源形成单一可测试的兼容判定。
- 本决策完成时 Registry 包含十一项内置 Skill；ADR 0113 又增加
  `run-skill-generator`。历史 selection 可按固定版本精确恢复。
- `model_invocable` 只是准入声明。当前产品仍只有操作者发起的内置 Skill 选择路径，
  后续模型选择器必须另行增加权限、预算、审计和产品入口。
