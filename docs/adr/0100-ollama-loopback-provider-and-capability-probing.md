# ADR 0100: Ollama 本地 Provider 与能力探测

Date: 2026-08-15

## Status

Accepted for the exact, explicitly-configured, loopback-only Ollama profile
described below. LAN scanning, implicit enablement, and automated install or
model pull remain unavailable.

## 背景 / Context

离线、本地优先场景需要一个不依赖云凭证的模型 Provider。Ollama 在
`http://127.0.0.1:11434` 提供本机 HTTP API（`/api/tags`、`/api/chat`、`/api/show`），
但不同模型的 tools、vision、JSON 与 context window 能力各不相同，且这些能力随
Ollama 版本和模型族变化。若因为“本地模型存在”就假设其具备完整 Agent 能力，会
把工具 schema 发给不支持工具的模型，或在能力未知时伪造调用。

## 决策 / Decision

### 1. 显式 loopback 端点；默认关闭

`ollama` 是唯一无凭证 Provider。Registry 只在 `CYBERAGENT_OLLAMA_BASE_URL` 与
`CYBERAGENT_OLLAMA_MODEL` 同时显式配置时启用它；缺省永不探测、永不扫描局域网。
Base URL 只接受 `http` scheme、loopback host（`127.0.0.0/8`、`::1`、`localhost`）、
无 userinfo/query/fragment/路径前缀；HTTPS、非 loopback、redirect、代理 transport
一律拒绝。构造出的 HTTP client 强制 `Proxy=nil`，即使进程环境定义了 `HTTP_PROXY`
也不会把“本地”请求路由到代理。模型名按有界 UTF-8 规范化（无空白/控制字符、
无 `..`、无 `//`、无前缀 `:` 或 `/`）。

### 2. 能力探测与失败关闭

`/api/tags` 的 `capabilities` 字段存在即表示该模型已探测；缺失表示能力未知。
`/api/show` 返回 `capabilities`（closed set：`completion|tools|vision`）与
`model_info` 中的 `<family>.context_length`（只接受数值，取最大值并限界）。
`SupportsTools`/`SupportsVision`/`SupportsJSONMode` 只读取进程内探测缓存；未知一律
false。JSON 视为 wire 级 `format=json` 特性（对任意已探测模型可用），而严格 JSON
输出有效性仍由独立 Harness qualification 验证。探测是 best-effort：路由选择、
qualification 与 diagnostic 前都会尝试，失败不阻塞操作，能力继续按不支持处理。
context window 探测成功后通过 `Router.SetContextWindow`（source=`ollama_probe`）
进入真实预算规划。

### 3. no-tool 安全路径

`DescribeModelHarness` 按探测结果输出 `ollama_chat` transport：tools 未确认时
`ToolStrategy=none`、JSON 未确认时 `JSONStrategy=none`，且 root 资格保持
`qualification_required`。Provider 在 `prepareRequest` 层再设一道防线：向未确认
tools 能力的模型发送 Tool schema 会直接失败，绝不透传 schema，也绝不伪造
tool call。这样 no-tool 模型只能走无工具安全路径（如 fanout/plain chat），不会
进入错误的 Tool loop。

### 4. 聊天、流式与 usage 估算

`/api/chat` 支持 `stream=false` 与 NDJSON `stream=true`。流式状态机逐行消费事件：
message content 增量、完整 `tool_calls`（合成确定性 ID）、终态 `done`/`done_reason`
（closed set：空、`stop`、`length`）与 `prompt_eval_count`/`eval_count`；done 之后
的非空事件、重复 tool_calls、超限文本、截断流与 EOF 缺失终态均按协议错误拒绝。
Ollama 的 tool call 没有 ID，adapter 按顺序合成 `ollama_call_<n>`；arguments 若被
上游序列化成 JSON 字符串则解包为对象。usage 优先采用 daemon 计数，缺失时按
字符/4 向上取整保守估算（input 按序列化消息字节、output 按响应文本），保证预算
记账始终有界。

### 5. 稳定错误映射与可解释诊断

错误只通过稳定分类暴露，原始 wire 文本绝不外泄：404 且正文含 `not found` →
`model_not_found`；正文含 `out of memory` → `capacity`；`rate limit` → `rate_limit`；
连接类错误 → `network`。loopback 连接被拒（服务未启动）映射为 retryable `network`，
消息固定为 “Ollama service is unreachable on the configured loopback endpoint”，
不携带 host/路径/payload。不自动安装 Ollama、不 pull 模型、不修改系统服务。

### 6. 入口接线

Registry 新增 `ProviderKindOllama`（kind `ollama`），OpenAPI 的 provider 路径枚举与
`transport_protocol` 枚举加入 `ollama`/`ollama_chat`；credential 枚举保持四位——
Ollama 无凭证，不属于系统凭证允许列表。CLI、HTTP/OpenAPI、Desktop 与 Web 复用现有
通用模型控制面（provider list/test/qualify、model set、模型可用性视图），Web 客户端的
kind/transport 校验同步接受新枚举。`configs/models.yaml` 仅保留文档级示例。

## 后果 / Consequences

- 显式配置的 loopback Ollama 可以完成模型列表、聊天、流式、取消、usage 估算与
  能力探测；工具/视觉/JSON/上下文能力不明时一律按不支持处理。
- 本地模型仍走完整 Policy、redaction、budget 与 Tool Gateway；“本地”只是部署位置，
  不是信任授权。
- 非 loopback 端点、代理绕过、重定向、隐式启用、LAN 扫描、自动安装与自动 pull
  仍不属于本决策；真实 Ollama smoke 是可选人工步骤并记录在 usage 文档。

