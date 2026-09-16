# 小任务 Plan 与人工验收简化复核

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-09。依据工作必读 v1.33、UX-Fixes 当前 A–N 实现；本记录为 O 阶段只读源码复核和下一轮建议，未修改产品、启动服务或新增实测结论。

**结论：强制三个方向、单模块强制六段人工说明，确有超出简单任务需要的流程负担。** 三方向数量与六段文字本身不能证明工作更安全或验证更充分。已有执行隔离、精确改动审批、真实检查和结果新鲜度仍有独立价值；简化应去掉不必要的手续，保留这些真实约束。

## 当前要求与证据

以下行号以本次复核源码为准。

| 位置 | 实际要求及影响 |
| --- | --- |
| [domain/plan_delivery.go](../internal/domain/plan_delivery.go):22、140 | `PlanDeliveryDirectionCount=3`，规格必须恰好三个方向。每个方向还需标题、摘要、至少一个权衡点与模块；模块需目标、验收标准和有序依赖。即使只改一个明确的拼写错误，也不能提交一个方向。 |
| [toolgateway/plan_delivery.go](../internal/toolgateway/plan_delivery.go):86；[run_supervisor.go](../internal/application/run_supervisor.go):2070、2183 | 工具 schema 的 `minItems/maxItems` 都是 3；系统指引要求三个不同方向，并等待用户选择 1、2、3。只改提示词会被实际校验拒绝。 |
| [store/migrations.go](../internal/store/migrations.go):5394；[httpapi/openapi.go](../internal/httpapi/openapi.go):1484 | 数据库有 `CHECK(direction_count = 3)`，API 描述同样承诺三方向。真正放宽需要迁移、域校验、工具 schema、投影说明和生成契约一致更新，不能只改表单。 |
| [delivery_checkpoint.go](../internal/domain/delivery_checkpoint.go):90；[application/delivery_checkpoint.go](../internal/application/delivery_checkpoint.go):242 | 针对本项验证、改动审阅、安全审阅、交接说明必填；最后模块再必填完整功能验证、异常与恢复检查。单模块天然是最后模块，所以要填六项；中间模块是四项，且不允许提交最后两项。 |
| [domain/delivery_checkpoint.go](../internal/domain/delivery_checkpoint.go):141；[store/migrations.go](../internal/store/migrations.go):5972 | 人工文字仅检查非空、长度、字符规范及绑定一致性；数据库也有非空约束。记录不是自动测试或安全审计证据，六个字段均填“通过”不意味着六类检查真的发生。 |
| [plan-delivery-work-items.tsx](../web/src/components/plan-delivery-work-items.tsx):92、118、137、159、165 | 当前界面要求任务处于暂停的 Deliver 且无执行租约：开始此项 → 填四/六项 → 记录人工验收与交接 → 完成此项。记录成功并不直接完成事项。 |
| [run-workspace.tsx](../web/src/components/run-workspace.tsx):848、875、902、913 | 方向选择与进入交付分成两次操作，运行中还需先暂停；进入交付不会自动调用模型、运行检查或授权工具。 |

这些要求针对进入该 Plan/Delivery 流程的任务，不能概括为所有普通聊天都会要求三方案。M 已允许同 Thread 承接计划及有精确来源的人工完成记录，也不能表述成每次“继续”都要重填。

## Finish 真正依赖什么

- [standard_code_supervisor.go](../internal/domain/standard_code_supervisor.go):376 要求真实 mutation、当前 mutation 已验证且有验证 Job；[application/standard_code_supervisor.go](../internal/application/standard_code_supervisor.go):1157 还检查当前 `passed/verified` 交付报告。手写六段说明不能替代这些条件。
- [store/supervisor.go](../internal/store/supervisor.go):1627 禁止存在 pending/in-progress/blocked 事项时 Finish；[delivery_checkpoints.go](../internal/store/delivery_checkpoints.go):587 对已纳入人工验收的选择，要求所有事项完成且有绑定当前事项版本、Deliver 修订的有效记录，或 M 的精确历史完成来源。
- Plan 阶段不能完成。完成计划项也不会完成整个任务或把报告改成 passed，见 [plan_delivery_work_item_control.go](../internal/httpapi/plan_delivery_work_item_control.go):13 及 [openapi.go](../internal/httpapi/openapi.go):1532。

因此要保留的核心是：用户目标与验收标准、明确选择的工作范围、执行/写入权限、真实检查与当前版本绑定、失败和未知结果的可恢复身份。固定三个选项、把一份人工判断拆成六段文字，是可调整的产品策略，不能仅因已存在数据库约束就继续保留。

## 可执行的最小路线

1. **先用已有 API 减少重复操作，但不要把它称为根本完成。** 一个明确按钮可表达“采用此方案并进入交付”，顺序调用现有 `/plan/direction`、`/plan/deliver`；“记录并完成此项”可顺序调用现有 `/checkpoint`、`/complete`。两步分别固定原 key、请求和提交身份；第一步已成功而第二步失败时，显示已完成的步骤并只恢复剩余步骤。未知结果先确认，不能换 key 重做。复用已有 QueryClient 页面生命周期恢复方式，不新增通用流程引擎。
2. **下一轮应真正支持一至三个方向。** 明确目标的小任务默认一个可执行方案；有实质取舍时才提供其他方向，仍由用户确认范围。保留模块数、验收标准和依赖上限，选择序号必须落在该提案的真实数组内。不要为凑数生成两个虚弱方案，也不要把“方案已选择”扩大为文件或 Shell 已批准。单方案的权衡可表达真实限制，不要求伪造比较。
3. **人工验收改为按需，而非所有末项必填六段。** 普通小任务可依据真实检查、差异和交接摘要完成；明确要求人工验收的流程保留操作者确认及必要说明。适用规则必须作为可核对的持久事实，并相应调整 enrollment/完成校验；不能靠前端隐藏字段、模型自报“小任务”或填假记录绕过。实际 Job、报告、差异可提供引用或可编辑草稿，不能自动生成“安全审阅通过”“异常恢复通过”等未经执行的断言。

第 1 项只减少点击；第 2、3 项才减少强制工作量。不建议为了避免迁移长期停在第 1 项，也不建议本轮在单文件逆向修复之外直接展开新的计划平台。

## 兼容成本与验收

三方向放宽涉及 SQLite 既有 CHECK 与关联外键/触发器，应通过新迁移处理，不改已发布迁移 checksum。人工要求调整还涉及现有证据列约束、gate 语义、域验证、API 及读投影；旧六字段记录、原指纹和幂等重放保持原义。M 的两代计划承接、人工完成来源与新一轮自动验证不得回退。是否需要协议版本变化应按最终契约确定，不能用同名字段悄悄改变旧记录含义。

下一轮最低验收：

- 新小任务可以只有一个方向、一个模块；无凑数方案，用户一次明确确认后进入同一任务执行。
- 简单流程不再被六段重复说明阻塞；需要人工验收的流程仍明确展示待确认内容。没有真实检查时不能称自动验证通过，陈旧报告不能完成当前检查门禁。
- 旧三方向/六字段记录可读、原 key 可重放；M 计划与工作目录衔接不丢进度、不复用过期自动检查。
- 合并操作的任一步响应丢失、切换任务和权限变化都不串身份、不重复提交；界面准确说明部分成功和剩余步骤。
- 以真实小改动完成“方案 → 审阅/执行 → 检查 → 完成”验收，记录实际输入字段数和确认次数；不以按钮减少或单元测试数量代替旅程完成。
