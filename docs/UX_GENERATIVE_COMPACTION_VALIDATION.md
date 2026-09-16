# 生成式摘要接入与验收

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

日期：2026-09-13。当前状态：**生成式摘要接入、定向验收及本地预览包已完成。** 本文件不作为整项目完成或真实模型效果声明。

## 本次范围

普通活动 Thread 历史达到既有压缩条件时，默认使用当前对话选定的模型，进行一次无工具、非流式的摘要调用。复用 Router、供应商适配器、ActiveCall、取消监听和已有预算底座；不添加外部记忆服务、独立 Run 或新数据库表。旧 Session 摘录路径仍可用，嵌入调用方可显式选择原摘录策略。

生成说明最多 1600 Unicode 字符，与 Go 从原记录选取的来源锚点共同放入既有 `handoff_memory.v1`，整个摘要仍不超过 4000 字符。空间不足时可进一步摘录生成说明或原文，并明确标记。模型不能指定来源身份、SHA 或审批权限；原消息、原工具事实及完整旧摘要保留，沿已有同 Thread/Workspace 回读路径访问。

本批 `context_compaction` 用途独立记录 started/terminal、当前来源及用量，不消耗正常回答的 transport retry、协议修复、工具轮次或 Skill 消费，也不进入公开助手文本流。上下文面板区分模型生成说明与原文摘录，沿用原补充纠正入口；活动记录区分“整理上下文”的调用结束和“摘要已保存”，不会把调用成功等同于候选已采用。

## 已处理的边界

- 输入装配包含原目标、经过验证的继承上下文、本 Session 前摘要和本次待压缩的完整旧消息；模型窗口放不下时不逐条裁掉待摘要材料。Token 预算同时考虑输入、输出和后续答复预留。
- 候选计算在 SQLite 事务外，提交重新核消息、前摘要、原目标/Run 配置、操作者队列变化、checkpoint 和活动 lease。计账导致的 checkpoint 变化只能由同一来源的原 terminal receipt 精确解释，其他变化仍拒绝。
- 普通供应商错误、格式不符、辅助预算不足可明确回退原文摘录；取消、Stop、输入变化、租约变化或未确认的收费调用不能回退后继续。
- 摘要使用来源派生的金额预留身份，避免复用正常调用或另一轮摘要的 Number=1。已知 usage 即使候选不采用仍记录；未知 usage 不冒称零费用。停止后晚到回执只能凭原 started 身份增加用量，不复活 Run、改变当前阶段或提交旧摘要。只有 total 而缺输入/输出组成时保留已知 total，费用按预留保守估计；不是供应商实际账单。未收到终态的已发送请求继续保留预留，不能由终局清理当成免费调用释放。
- 生成请求对 JSON 叶子脱敏，再用 Go 私有摘要标记避免对序列化 JSON 重复正则脱敏。摘要内换行后的目标不会被截掉；伪造 JSON、正文变化或其他用途不能获得该处理。生成正文存储与回执也保留脱敏后的结构。

## 当前验证证据

证据目录：`output/playwright/ux-context-generated`。所有临时目录及 Go 缓存使用 D 盘；没有调用真实供应商、上传真实对话、读取模型凭据或启动产品服务。

- `compaction/validation.md`：contextmgr 全包 43 个顶层测试 race 通过；LLM 定向 11 个顶层测试 race 通过。覆盖原锚点/生成说明预算、连续压缩、滚动继承、严格格式和嵌套 JSON 脱敏。
- `integration/generated-race-final.log` / `generated-race-final-result.json`：**4 个顶层、9 个叶子场景，最终 race 25.143 秒、exit0**。默认 Thread 主链 24 输入、2 次生成、26 次正常模型请求（含一次重试与一个工具后续请求）；总 known tokens 348，实际 workspace_read 一次，原记录 SHA、前生成摘要传递、用途隔离和公开文本边界通过。两种回退、context cancel、实际 ActiveCall Stop、Run Cancel、取消后迟到成功、调用期间新增要求、未记录终态后 SQLite 重开均通过。迟到成功场景保存 known tokens 168、摘要0；原始22消息逐 ID/SHA/marks 不变。未知重开保留原 pending turn、只发生原1次辅助调用，无再次模型请求或摘要保存。
- 重开测试用窄故障注入阻止终态与后续失败结清写入，再关闭并重新打开真实 SQLite；为模拟这个中断点，第12输入走产品已有 Submit→Handoff 内部链，第1—11输入及其他正常/失败用例走完整 Thread facade。该用例不是实际断电/进程硬崩溃测试。
- `store/validation.md` / `store/final-race-2.jsonl`：27 个 store 顶层 + 1 个 llm 顶层定向 race 通过，分别 11.848 秒/1.352 秒，exit0；包含来源/队列/租约漂移、原子发布回滚、同源未终态不重发、用途/Skill/公开流/工具隔离，以及7类实际 Reserve→终态/取消→reconcile 组合。集合和子例不重复累计。
- `previous-regressions-2.log`：原回读/滚动/工具压力与连续性定向 race 通过，34.346 秒。四处既有固定协议测试显式使用摘录策略，保持其精确原文省略/调用序列的原始验收意图；新生成式默认路径由独立集成覆盖。前一次测试因额外辅助调用消耗旧固定应答队列失败，失败日志保留。
- `ui-tests-2.log`：上下文组件 12 项通过；类型检查通过。生成正文补保留换行后，最终 `web-build-final.log` 的类型检查及生产构建 exit0：JS `index-CgVlbu9A.js`，CSS `index-CypxNa1Z.css`；既有大 chunk 警告保留。首轮 UI 测试工作目录错误导致未发现 setup，记录在 `ui-tests-1.log`，不称通过。没有声称本次对新增文字做了原生/高 DPI 目视验收。
- `activity-1.log`：活动投影包通过，辅助文本和内部失败诊断不进入助手答复。

- `monetary-and-budget-final.log`：5 个预算/金额用例 race 通过，2.122 秒，摘要来源身份与未知估算、输入窗口/Token 预留、旧金额结算通过。旧0.01 USD测试夹具实际需10,696微美元预留，摘要开/关均在首次模型调用前被拒，见独立 `compaction/monetary-probe.md`。本次只将该结算测试上限改0.02，实际只调用一次且仍断言结算8微美元，未放大生产预算。
- `go-vet-final.log`：application/contextmgr/llm/store/runactivity 五包 vet exit0。
- `root-owner-checks-final.json`：root独立核34个 owner 冻结条目的大小和SHA全部一致。首次核对脚本误将数组格式清单当作对象读取，旧结果保留，未发现源文件漂移。

## 本地试用包

[TraverseBoard-UX-Context-Generated.exe]（本地保留证据，未公开：`output/playwright/ux-context-generated/delivery-final/TraverseBoard-UX-Context-Generated.exe`），版本 `v0.1.0-ux-context-generated`，105696256 字节，SHA256 `7d18a3798dcab7a4074e311ea3e751dfe8f3fe0fff345785528350c2235bbc60`。

`delivery-final/native-build-result.json`：构建 exit0；`native-artifact-validation.json`：20项静态检查通过、1802源码/嵌入输入构建前后不变、116项dist资产精确嵌入。入口 JS/CSS 与最终web构建一致。root独立从磁盘核包大小与SHA，见 `root-package-check.json`；原大量dirty保留，结束834项。没有启动EXE或产品服务，不将PE的PerMonitorV2配置、静态资源核验称为原生目视/重启验收。当前运行实例如果使用旧包，不会自动获得这些修改。

## 尚未由本次证明的内容

固定协议供应商测试验证执行链和事实保存，不能证明真实模型概括一定准确、任务成功率提升或无限/无损记忆。真实任务同条件对照仍需另做。前批原生窗口、高 DPI、物理剪贴板、草稿硬退出持久性及附件缓存回收等边界不因本次改变。

源码复核另发现旧普通 Thread Handoff 未安装金额服务，普通模型 reservation identity 使用会在不同 Turn 重复的 Number。本次摘要自行复用同 store 的金额服务，并用独立来源身份避免碰撞；没有将整个普通任务的金额预算称为已全面解决。该旧问题应独立跟进，不能用摘要用量正确掩盖。

未 commit、push 或 release；大量已有工作区修改保留。最终产物是本地未提交预览，不是签名发行版。
