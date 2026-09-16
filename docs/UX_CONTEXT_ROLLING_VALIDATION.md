# 上下文有界滚动继承验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-12。本批接续“继续补齐”，完成普通 Thread 多后继摘要的容量修复；生成式摘要仍未实现。工作树 `<workspace>`，分支 `codex/ux-journey-convergence`，HEAD `15c399c8940aeb81d3c45bb0de96759b480d02c2`。保留此前未提交改动，没有提交、推送或发布。

## 用户行为与实现

此前换模型等后继执行把所有旧摘要全文拼接到 `thread_summary_bundle`，累计超过 16KiB 就拒绝创建后继。本批继续保留 16KiB 的快照摘要上限，改为有界滚动摘录：完整新窗口最多 4000 Unicode 字符，只引用上一份已保存的继承摘要和当前 Session 摘要，不累计整个前驱列表。

原目标、早期修正、最新要求、进展和工具观察复用现有摘录锚点策略。原始来源身份、哈希和历史授权标记不改；窗口整体标记为非授权、有损。它不承诺任意历史细节都留在当前窗口。完整旧摘要仍在不可变摘要行或旧 Run 的原始配置中。

用已有 `history_search/history_read` 搜索或回读摘要和继承快照，不增加常驻按钮或模型工具。来源绑定当前 Thread 的合法 Run/Session 链及 Workspace；回读默认4KiB、最大8KiB，后续UTF-8字节页必须带全文SHA。快照的语义指纹与选定原文的字节SHA分开返回，不能互换。普通后继在替换全文前，先实际验证旧摘要原件能按该来源读回；读取失败时不丢弃旧摘要来强行继续。

旧单份摘要保持原有形态；旧平铺 bundle、无行ID的 opaque摘要和缺少SHA的旧v0行继续可读。v0缺失哈希仅在本次投影中计算，不回填原行。已有旧摘要协议族的未知版本拒绝；普通JSON文档自己的version字段仍按不可信原文保留。

上下文查看入口显示滚动摘要的可读摘录和有损说明，完整保存文本放在详情中；没有改发送、审批、模式或权限流程。

## 主链实测

证据目录：`output/playwright/ux-context-rolling/integration`。

最终 `rolling-race-final.log`：1个顶层测试通过，Go报告24.552s。采用隔离SQLite与固定协议供应商，普通Thread入口和真实gateway执行；不是直接调用摘要helper替代主链。

- 同一Thread产生6个Run、75次普通输入、83次模型协议请求；每段历史自然达到压缩条件，正常选择/重置模型并创建后继。
- 实际摘要按旧flat bundle编码，在16938和21228字节时仍各成功创建后继；最终旧累计值25518字节，新滚动窗口约4000字节、最多两个来源，原上限未调大。
- 越界后关闭重开SQLite，再继续同一Thread。最早摘要3997B、上一继承摘要4000B、完整原快照15688B分别分2、2、3页回读，与原SHA逐字节一致；来源从真实搜索结果或窗口来源取得。
- 另外回读早期原目标、较晚中文修正、原工具参数与结果。全程 `history_search=2`、`history_read=12`、原 `workspace_read=1`；没有重执行原工具或改动原文件。
- 同Workspace另一Thread的摘要与continuity读取均为NOT_FOUND。隔离检查另有1次输入/2次模型协议请求，与主场景计数分开。

## 相关回归

集合有交叉，不累加成总测试数；未跑全仓CI。

| 范围 | 最终证据与结果 |
| --- | --- |
| contextmgr全部 | `compaction/race-final-2.log`：37个顶层race通过，2.265s；含20次滚动、旧格式、多字节预算、来源冲突 |
| store新旧历史与结果校验 | `store/final-race-1.jsonl`：16个顶层race通过，11.820s；旧游标、原始字节、跨任务、终止前驱、事务内结果重建 |
| application既有连续性与窗口回归 | `application-regression-2.log`：race通过，19.662s；原Thread续接、摘要、窗口压力及历史回读相关集合 |
| gateway/LLM针对历史工具 | `gateway-llm.log`：两包定向通过 |
| 上下文查看组件 | `frontend-context.log`：1文件10项通过，14.56s |
| 类型与生产前端 | `web-build.log/.exit`：exit0；仍有chunk大于500kB提示 |
| 协议登记 | `protocol-registry-3.log`与`protocol-tests.log`：生成及验证通过 |
| 六个相关Go包静态检查 | `vet.log/.exit`：exit0 |

本批发现并修正：新增历史搜索不能被外部fork来源阻断；只跳过其NOT_FOUND，仍计入扫描水位，合法本地来源损坏继续明确报错。已有flat-bundle集成测试原先要求每份摘要全文内嵌，按新契约改为检查有界窗口并回读每一份原摘要SHA，保留原历史与权限断言。

编译过渡期缺helper、store误引用私有校验函数、旧测试的flat-bundle断言、协议负例登记遗漏/排序等早期失败日志均保留，后续通过不覆盖这些记录。helper、store和最终接入均另经独立限定范围源码复核，未发现交付阻断问题。

## 本地试用包

[TraverseBoard-UX-Context-Rolling.exe]（本地保留证据，未公开：`output/playwright/ux-context-rolling/delivery-final/TraverseBoard-UX-Context-Rolling.exe`）

- 版本 `v0.1.0-ux-context-rolling`，105527808B。
- SHA256 `fa730993d104337c4d8e028b104aafa4747864ebf36dce55044ad76dd11f4919`。
- JS `index-Lm7-Gmpd.js`，CSS `index-BEMq2-co.css`。1797源码/嵌入输入稳定，116前端资产精确嵌入。
- root独立核包哈希/大小、7个owner冻结条目及1797个构建输入一致，见 `root-artifact-verification.json`。EXE未启动，为未提交本地预览，不是签名发布或原生窗口验收。

## 明确保留的边界

生成式摘要只完成[源码接入设计](UX_GENERATIVE_COMPACTION_DESIGN.md)。下一步需给摘要调用独立用途，复用当前供应商、取消和预算，同时核对计费导致的合法checkpoint变化；不能直接占用正常答复重试次数或整体放松候选提交检查。本批没有接入生成模型、长期记忆服务或上传真实对话。

固定协议主链只验证来源与续接机制，不证明真实模型会正确选择检索词、回读全部必要细节或完成长任务。当前仍为规则摘录；1024个Run的召回链范围、模型总输入预算和每轮工具回合限制保持，不能宣称无限上下文。外部fork来源不因引用而变成本Thread可读来源。

本批无服务或应用窗口启动，所有新Go构建和临时缓存放D盘。原生剪贴板/IME/高DPI、草稿硬退出和附件缓存GC等旧未验事项保持，不由本次后台验证推定解决。
