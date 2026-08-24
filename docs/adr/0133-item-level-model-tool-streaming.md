# ADR 0133: Provider-Neutral Item-Level Model and Tool Streaming

- Status: Accepted
- Date: 2026-08-24
- Scope: GitHub Issue #152 and schema v130

## Context

`llm.ChatChunk` historically carried text deltas but exposed tool calls only as a
complete list on the terminal chunk. OpenAI-compatible adapters could assemble
interleaved function arguments and Anthropic-compatible adapters could assemble
content blocks, but the shared Application layer could not express item starts,
argument progress, or the boundary between model preparation and Go-authorized tool
execution. The public UI therefore learned about tools only after the model response
had ended, while the durable ledger had no stable identity that reconciled a live
provider item with its local Supervisor call.

Persisting provider wire events is not an acceptable fix. Wire formats differ, token
and argument deltas can contain secrets or private reasoning, and a provider-owned
call ID is not execution authority.

## Decision

The in-memory provider-neutral protocol is `llm.item_stream.v1`. Every event has a
strictly increasing sequence, response identity, provider/model identity, and explicit
`provisional`/`durable` state. Item events additionally carry an item identity and,
after it becomes known, a call identity. The ordered lifecycle is:

```text
response_started
  output_item_started
    text_delta*
  output_item_completed

  output_item_started
    tool_call_started
    tool_argument_delta*
    tool_call_completed
  output_item_completed

tool_execution_started
tool_execution_completed
response_completed | response_failed | response_cancelled
```

`tool_call_completed` means that bounded arguments are ready for Go validation. An
`output_item_completed` boundary means that the provider finished producing that item;
it does not grant authority and does not claim that the tool ran. Only the Go
Supervisor may issue execution-start/completion facts after validation and durable
call creation. Provider adapters are rejected if they emit execution events.

Adapters declare either `item_delta` or `item_complete` granularity:

- OpenAI-compatible streams preserve the arrival order of interleaved indexed tool
  deltas while final compatibility calls retain deterministic index order.
- Anthropic-compatible streams preserve content-block order and map text/tool-use
  block starts, deltas, and stops into the same event vector. Thinking/signature blocks
  are ignored and never enter the public protocol.
- Ollama and the deterministic mock provider explicitly degrade to complete-item
  events where their wire protocol lacks argument deltas.
- Providers not yet migrated continue through the legacy `ChatChunk` adapter, which
  synthesizes the same lifecycle at complete-item granularity.

The Application-owned accumulator validates one state machine, rejects mixed legacy
and explicit modes, sequence/model/identity changes, duplicate or post-terminal
events, malformed or mismatched arguments, missing usage, and unfinished items. It
replaces upstream response/item/call IDs with deterministic attempt-owned IDs before
any public or durable consumer sees them. Raw IDs therefore remain connection-local.

## Aggregation and failure semantics

Text keeps the existing 2 KiB/250 ms durable aggregation policy. Item boundaries are
queued independently and force a `model.delta` record even when no text was emitted,
so aggregation cannot erase lifecycle transitions. Text deltas and argument deltas
remain bounded in memory; they are not fields of the durable boundary type.

Cancellation produces `response_cancelled`; malformed data, provider failure, missing
terminal usage, or EOF produces `response_failed`. In-progress items become cancelled
or failed. None of these paths synthesize `output_item_completed` or
`response_completed`. A successful terminal event requires every provider item to be
complete and usage to match the compatibility terminal chunk exactly.

## Public and durable projections

`model_public_stream.v3` exposes only a content-free item projection alongside the
existing redacted public text:

- stable response/item/call identity;
- item type and lifecycle status;
- bounded, redacted tool name;
- accumulated argument byte count;
- provisional/durable flags.

It has no field for arguments, raw payload, credentials, usage, private reasoning, or
provider wire data. The TypeScript boundary uses a closed-key validator and rejects any
attempt to add such fields. The UI may show “Preparing call” while bytes arrive, but it
states that Go validation must occur before execution.

SQLite persists only content-free item boundaries in `model.delta`. On successful
model completion, the Supervisor binds each stable stream call to the deterministic
Go-issued call ID. Schema v130 stores the response/item/call reconciliation identities
on `run_supervisor_tool_calls` and makes them immutable. The tool payload remains under
the existing normalization, size, redaction, policy, budget, and idempotency controls.

The durable execution lifecycle is
`supervisor.tool_execution_started` then
`supervisor.tool_execution_completed`; the existing
`supervisor.tool_result_recorded` event remains as a compatibility projection. Start
is idempotent, completion and the compatibility result are transactional, and a result
without a durable start is rejected. Restart retries only a still-pending local call
through its deterministic operation key, preserving existing exactly-once logical tool
effects.

Completed Session history is projected as durable completed message items using stable
Session/message-derived IDs. This includes history written before schema v130; no
history rewrite is required.

## Security properties

- Provider deltas never carry or mint tool authority.
- `StreamEvent` raw text, argument, completed-call, and usage fields are deliberately
  excluded from JSON serialization.
- Public and `model.delta` projections cannot represent raw arguments or wire payloads.
- Tool arguments are assembled only up to the provider payload limit and must match the
  one normalized complete JSON call before the Supervisor accepts them.
- Anthropic private thinking and all adapter raw response bodies remain private.
- Stable identifiers are domain-separated hashes over Application-owned attempt facts;
  upstream IDs and credentials are not persisted.

## Consequences

Consumers can render ordered text and tool cards without understanding provider wire
formats, and live provisional cards can reconcile with the durable Supervisor ledger.
The compatibility fields on `ChatChunk` and terminal `ChatResponse` remain available,
so existing providers and callers can migrate incrementally. Complete-item providers
remain less granular but declare that degradation explicitly.

The public stream is still process-local and replaceable. Restart recovery relies on
the durable boundary and Supervisor ledgers rather than replaying token or argument
deltas. A reconnect may therefore jump directly from a provisional card to its durable
history projection without fabricating missed events.

## Verification

Tests cover OpenAI interleaving, Anthropic content-block order, Ollama/mock degradation,
legacy adaptation, stable identity replacement, argument mismatch, forbidden provider
execution, cancellation without false completion, JSON non-disclosure, boundary-only
durability, v129 upgrade/immutability, execution start/result idempotency, real-compatible
OpenAI tool round-trip, old-history projection, closed public API parsing, and UI tool
preparation without argument disclosure.

## 中文结论

`llm.item_stream.v1` 将 OpenAI、Anthropic、Ollama、Mock 与旧 `ChatChunk` 统一为有序的
response/item/call 生命周期。参数增量只在有界内存中拼接；Provider 只能表达“工具调用已准备”，
不能签发 authority 或执行工具。Go 完整验证后才创建确定性本地 call ID，并依次记录执行开始、执行完成
与兼容结果事件。

公开 `model_public_stream.v3` 和持久 `model.delta` 只保存稳定 ID、状态、工具名与参数字节数，不保存
参数、raw wire、凭据或私有推理。取消、断流和错误只产生失败/取消终态，不伪造完成 item；旧历史无需
改写即可投影为 durable completed items。
