# 上下文管理：开源复用与现有实现的取舍

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-12，Asia/Hong_Kong。基于当前 UX-Fixes 工作树及本日官方源码/文档。用户询问技术路线；本文为建议，尚未授权实施或部署任何外部服务。本轮没有传输本项目对话、模型凭据或数据库给记忆服务，没有修改产品代码。

建议：以成熟开源方案为基准，局部替换上下文策略；保留执行事实和恢复契约；将长期记忆作为可选、可替换后端。现有代码投入不是保留理由，部署语言也不是拒绝成熟实现的充分理由。采用外部系统应依据能力收益与适配成本。

## 必须区分的范围

1. 原始事实：消息、工具调用及结果、审批、真实执行状态、精确来源。需要完整保存和一致性保障。
2. 当前模型输入：预算内选哪些消息、如何摘要、何时回读旧内容。这是当前最需要替换或优化的部分。
3. 跨任务长期记忆：项目经验、偏好、历史决策的提取、更新和检索。高关注度记忆项目主要在这里提供价值。

事实提取型记忆不能自动替代工具调用与结果的配对，也不能把模型声称“已完成”变成通过检查或新的授权。

## 候选项目及价值

| 项目 | 经官方资料确认的定位 | 本项目建议用法 |
| --- | --- | --- |
| Codex 开源上下文实现 | 编程 Agent 内部压缩、历史重建及初始上下文重新注入 | 第一参考，研究其上下文生命周期；不是独立 Go 组件 |
| LangMem | 滚动摘要和记忆处理原语；有独立存储适配能力及 LangGraph 集成 | 参考摘要状态、阈值、预算和原历史/模型输入分离；Python组件是否运行时接入按成本判断 |
| Mem0 | 对话事实、偏好等长期记忆的提取与检索，提供库、自托管及托管服务 | 可作为跨任务记忆候选；不直接代替原历史账本或 compactor |
| Letta/MemGPT | 有状态 Agent 运行时与记忆管理 | 借鉴分层与按需检索；整体接入意味着更广的 runtime 迁移 |
| TencentDB-Agent-Memory | Chat Memory、Skill、Wiki、CodeGraph 的团队记忆中心 | 团队共享知识候选；优先评估显式 API 适配，保留模型实际输入可解释性 |
| MemOS | 长期记忆与混合检索；同时有完整服务和本地插件路线 | 比较其轻量本地方案，作为可选后端；不默认把完整服务依赖塞进桌面 |

来源：[Codex compact.rs](https://github.com/openai/codex/blob/main/codex-rs/core/src/compact.rs)、[LangMem 摘要指南](https://langchain-ai.github.io/langmem/guides/summarization/)、[Mem0](https://github.com/mem0ai/mem0)、[Letta 当前入口](https://github.com/letta-ai/letta)、[TencentDB-Agent-Memory](https://github.com/TencentCloud/TencentDB-Agent-Memory)、[MemOS](https://github.com/MemTensor/MemOS)。

版本边界：Letta 当前入口指向 letta-ai/letta-code，旧 V1 server 已归档，不能拿旧 Docker/Python 教程当当前主线。Tencent 推荐 Core/Hub/Proxy，默认 SQLite，不要求腾讯云数据库；代理接法会采集对话并注入上下文，需适配我们实际模型输入的来源展示。MemOS 完整服务使用 Python/REST 与 Neo4j/Qdrant，但另有面向其他 Agent 的 Node/SQLite 本地插件，不应一概称只能重部署。Mem0 当前 README 明确部分性能数字来自含专有优化的托管平台，不能等同开源 SDK 的实测结果。

以上均为官方描述与适配判断，没有在本项目实测这些后端。未按精确 star 数排序；关注度、当前维护分支、可复用代码范围、相同任务表现是不同指标。

## 本地最小替换边界

`Manager.Compact` 直接调用规则摘要函数，策略尚未插件化：[context.go](../internal/contextmgr/context.go#L168)。建议把候选摘要生成与持久提交分开，输入包括带来源的历史、已有摘要、预算，输出摘要与精确覆盖范围。原4000字符摘录作为对照/明确降级路径，不把它当成熟语义记忆的最终形态。

预算与选择可分别改进：[selector.go](../internal/contextmgr/selector.go#L38)。完整请求仍需计算工具、输出预留和模型窗口；新摘要不能靠遗漏原目标来伪装适配成功。

已有 SQLite 原文与包含已压缩记录的分页读取：[read_pages.go](../internal/store/read_pages.go#L148)。先补按 Thread/Session 限域的历史搜索及按来源精确回读，再根据真实召回瓶颈决定是否增加语义检索。本轮未确认 FTS5 编译支持或中文分词效果，不把 LIKE 查询当成熟全文检索。

现有压缩在事务内检查 attempt/pending/lease 并保存摘要和来源标记：[store 实现](../internal/store/supervisor_context_compaction.go#L35)。不能直接在该事务内替换成耗时 HTTP/LLM 调用；应先计算候选，再核对原输入版本及执行身份，最后原子提交。外部记忆内容不负责判断审批有效性或某项工具是否实际完成。

长期记忆已有作用域、有效期和版本记录，但选择主要依赖更新时间：[context_memories.go](../internal/store/context_memories.go#L61)。外部库可提供候选提取、去重/冲突识别或相关性排序；本地保留明确的来源、修改/删除和任务隔离行为。

## 把借鉴落成可检验结果

建议只做小范围对照，不同时部署六套系统：先比较现有规则摘要与参考成熟机制的替代策略，另选一个长期记忆后端作为后续候选。使用同一组有界公开/合成长任务轨迹，固定模型与预算，覆盖中段纠正、旧失败证据、跨文件重构、多次压缩和重启。

评估实际任务完成、关键要求保留、原证据回读、过时记忆误用、额外调用费用、延迟及本地部署成本。服务失败时仍保留用户历史和未完成任务。只有某个外部方案在这些方面表现出足够收益，才扩大接入；若收益明确，应直接使用成熟组件，避免为了Go语言一致性重复实现完整记忆平台。

当前推荐顺序：Codex/LangMem 的上下文生命周期和摘要机制 → 本地原历史检索 → 小规模真实任务对照 → 按跨任务或团队需求选择 Mem0/MemOS/Tencent 等后端。没有要求安装服务、迁移数据库、改模型代理地址或重写 harness。
