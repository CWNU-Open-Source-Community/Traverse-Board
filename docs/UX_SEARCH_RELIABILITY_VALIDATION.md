# 真实联网搜索失败的诊断与修复

> 历史批次记录：状态仅对应文内日期。当前验收边界见 [公开审阅说明](UX_PUBLIC_REVIEW_STATUS.md)；本地日志、截图与安装包未纳入公开分支。

2026-09-13。现场诊断、原因分类和界面反馈修复及本地预览构建已完成；**实际 DeepSeek Flash 原生联网搜索仍失败**。本报告不以定向测试通过替代真实搜索验收，旧长任务验收失败保持原结论。

## 已确认的现场事实

用户运行的是上一批最终 `delivery-v2/TraverseBoard-UX-Context-Reliability.exe`，SHA `c77125ee53af3d83f055af9708e8e8d8fd1ed0f74bd9749114d152325a89e234`，不是因仍在运行旧包。只读核对真实数据库匹配截图的 Agent、两条中英文查询及 `response_invalid`；路由为 `official-deepseek/deepseek-v4-flash`，官方 `/responses` 端点、provider_native 策略。没有写入或重试用户对话，没有停止用户应用。

两条工具失败记录不必对应两次实际HTTP请求：适配器对同一配置的失败有短时负缓存。本次不凭工具卡片数量推算实际请求次数。

后续模型实际选择 `wait`，随后Run由running变为paused；搜索工具的UNAVAILABLE本身不会直接暂停Run。用户之后操作恢复仅解除生命周期暂停，不会重试搜索。完整权限表示允许范围，不是搜索服务健康状态。

## 独立真实诊断

复用实际只读Run模型路由、Provider定义、正规凭据解析与生产搜索适配器，仅发送用户已给出的公开查询。新证据在 `output/playwright/ux-search-reliability`；请求有界，不存凭据/请求头/私有推理或完整响应正文，不改原Thread。

第一探针：**1 POST，4.274秒，HTTP200 application/json、整体completed；只有一条completed message，0实际web_search_call、0搜索来源引用、无有效results JSON**。未截断、没有incomplete，usage158输入/539输出、reasoning0。适配器因此返回response_invalid。这说明该次请求没有实际执行搜索，不能解释成用户权限不足、网络断开或没有最新进展。详见 `probe-1-result.json`。

当前DeepSeek分支使用 `tool_choice=auto`，虽有文字指示必须搜索，却允许接口直接生成回答。官方 [Responses API](https://api-docs.deepseek.com/api/create-response/) 声明支持以 `{"type":"web_search"}` 指定搜索工具。因此做了独立对照，其他实际配置、2048输出限制及45秒请求期限保持，每个探针最多1 POST：

| 探针 | 请求差异 | 实际结果 | 结论 |
| --- | --- | --- | --- |
| 1 | 原生产请求 auto | HTTP200/completed，2322字符普通回答，0实际搜索动作、0来源；4.274秒 | 未实际搜索，原适配器报 response_invalid |
| 2 | 仅指定 named web_search | 11.134秒后传输失败，没有HTTP响应及usage | 具体连接失败原因未保留，不能据此判断工具支持；供应商是否收到及计费未知 |
| 3 | 与探针2完全相同的请求JSON | HTTP200/completed，265字符普通回答中的 DSML 工具标记，0实际搜索动作、0来源；1.051秒 | 文字工具标记没有被执行，强制指定也没有修复搜索 |

探针3编译时已包含本批错误分类，因此返回 `search_not_performed`；请求JSON与探针2相同，但本地错误分类不同。两个收到响应的探针分别697、213 tokens；未收到响应的探针用量未知，不推算总费用。详见 `probe-1-result.json`、`probe-2-validation.md`、`probe-3-validation.md`。

**没有采用 named 的生产改动**，因为它没有通过真实比较。没有把 DSML 普通文字转成已执行搜索，没有接受无实际搜索动作的链接，没有改模型、提额度或添加外部后端。官方 [Responses 指南](https://api-docs.deepseek.com/guides/responses_api/) 的服务端多轮执行说明，不等于本次配置实际执行成功，也不能用客户端一次POST冒称仅一次服务端工具调用。

三次诊断均已退出，未继续第四次。用户进程PID、路径和启动时间保持；原Run最后事件序号仍为53、原搜索工具记录仍为2条。没有发送原对话新消息、重试原工具或写数据库。证据 `after-three-probes-readonly.json`、`process-after-probe-3.json`。

## 呈现修复

旧工具详情仅用通用UNAVAILABLE生成“所需的联网能力当前不可用”，丢失搜索服务的稳定细类。现在区分 `search_not_performed`（未返回实际完成的搜索）、`response_incomplete`（响应未完成）与原有 `response_invalid` 等原因，并仅对服务构造的已知原因输出固定中文解释，原始错误正文不进入公共DTO。旧保存记录仍按原 `response_invalid` 解释为“搜索服务返回了无法使用的响应”，不凭新探针改写历史成更细的原因。任意未知正文只显示通用搜索失败，真实权限拒绝不被覆盖。原30秒失败缓存与权限、来源校验保持。

前端同时收敛“恢复任务”的含义及失败的0来源标记：提示“本轮已暂停，可发送新消息继续”，按钮改为“解除暂停”，说明不会重试失败工具；失败且0来源显示“未取得搜索结果”，不再显示0条待验证。保留合法wait、原API与权限边界，不自动重试失败工具。

## 验证与交付

- Provider适配器/服务：25项顶层测试、32项子例，race 1.805秒通过；`go vet ./internal/webevidence` 退出0。
- 中文活动投影：3项顶层测试、7项子例，race 1.188秒通过；`go vet ./internal/application` 退出0。
- 前端：3个测试文件共62项通过，typecheck及Vite生产构建退出0；包含暂停解除与原请求标识重试不提交新消息、普通发送继续、失败搜索和成功空结果的区分。
- 12个改动文件已分别冻结，独立源码复核未发现必修问题；定向 `git diff --check` 退出0。以上验证不代表供应商搜索成功。
- 前端资产：`index-DU5IK5K2.js`、`index-CypxNa1Z.css`，共116项，已逐文件验证嵌入。
- 新本地反馈预览 [TraverseBoard-UX-Search-Feedback.exe]（本地保留证据，未公开：`output/playwright/ux-search-reliability/delivery/TraverseBoard-UX-Search-Feedback.exe`），版本 `v0.1.0-ux-search-feedback`，105794048字节；SHA256 `bde907d80a267a225b7bb3d1e245cb2b854c87c41f33c978931921eda224b916`，root已独立复核SHA。构建退出0、20/20项静态核对通过，1810项构建源输入及12项本批改动构建前后稳定。详见 `delivery/delivery-result.json`、`native-artifact-validation.json`。
- 新包未启动，不覆盖或重启用户旧包。构包前后PID63676仍为旧路径和原SHA，原生窗口交互尚未实测。此包只修错误分类及反馈，不作为搜索已恢复的交付。

冻结及详细日志：`provider-search-failure-freeze.json`、`typed-failure-freeze.json`、`ui/ui-source-freeze.json`，均位于 `output/playwright/ux-search-reliability`。源工作树开工880项dirty全部保留，不产品commit/push/release。

## 当前边界

**已修的是错误分类和误导呈现，未修复的是本次配置的原生搜索能力。** 现有证据能确认本次实际请求没有完成搜索，不能进一步断言供应商全部模型或所有接口永久不支持搜索。要恢复功能，后续仍须得到可用的原生搜索响应，或明确接入可用搜索后端，并在普通Thread链路取得真实来源；仅解除暂停、换查询语言或增大权限都不能证明故障已解除。

旧Node/Docker/模型编程及长上下文失败均未由本批解决，不新增独立搜索平台或擅自切换外部供应商，不重跑已结束smoke或开启无限探针。原始失败及以上未知项保留。
