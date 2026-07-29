# ADR 0080: Public Model Updates And Harness Activity

- Status: Accepted
- Date: 2026-07-30
- Scope: P13-A1, P13-A2, and P13-A3; no schema migration

## Context

Agent products often show prose progress beside tool and lifecycle rows. That
presentation can be mistaken for hidden chain-of-thought even though the two
have different origins and trust properties:

- a model may author a concise public status update;
- the Harness can report actions it actually requested, started, completed, or
  rejected;
- runtime duration and connection state are host metadata.

Provider-private reasoning, provider `thinking` blocks, prompts, raw model
deltas, tool arguments, and tool output must not be exposed or reconstructed.
A model-authored claim must also not be styled as a verified execution fact.

## Decision

Prayu adds a read-only `run_activity.v1` projection over existing durable Run
events. It has no migration and no mutation path.

The projection accepts at most 100 source events, orders them by Run sequence,
and maps only an explicit Go allowlist:

- public operator Session messages;
- public model Session messages created from `root_lifecycle.v1.message`;
- model-call lifecycle metadata;
- Agent/Supervisor lifecycle metadata;
- Tool names and lifecycle status, without arguments or results;
- approval, fixed-command proposal, file-edit, plan/checkpoint, and Run status
  metadata.

Unknown event types are omitted. There is deliberately no mapping for provider
thinking or `model.delta`. Raw event payloads are never copied into the
response. Every title and detail is UTF-8 checked, control-character cleaned,
secret-redacted, and rune-bounded.

Each item identifies one of three sources:

- `model`: public user-facing prose, not a verified execution fact;
- `harness`: a durable Go-owned event and therefore verifiable as a recorded
  lifecycle fact;
- `operator`: a recorded user input with its instruction-authority flag.

The response always fixes `private_reasoning_included=false`. The React
timeline refuses to render if a server ever declares it true.

The Desktop Run workspace opens on Activity. It keeps raw Events in a separate
diagnostic tab. SSE or Desktop polling supplies only a refresh signal; the
Activity endpoint rereads the durable event store and remains the canonical
projection.

The root system contract now defines `root_lifecycle.v1.message` as concise
public progress or result text. It asks for completed actions, verified
outcomes, and a next step where relevant; forbids private chain-of-thought,
hidden prompts, secrets, and raw tool output; and requires model judgments to
be distinguished from Harness-verified results.

## Consequences

- Prayu can provide a Codex-like public activity experience without claiming
  access to private chain-of-thought.
- Models that do not expose a provider-native public reasoning summary still
  produce useful public lifecycle messages through the existing root
  protocol.
- A future provider-native public summary requires a separate explicit
  contract. It must not be inferred from private reasoning blocks or returned
  to the model as trusted context.
- The projection grants no model, Tool, file, process, network, approval,
  browser, Docker, or terminal authority.
- A bounded recent window may omit older activity; full raw event diagnostics
  remain separately paginated.

## Verification

Go tests cover source separation, ordering, cross-Run and duplicate-sequence
rejection, secret redaction, Unicode bounds, omission of private thinking,
model deltas, tool arguments, and tool output, HTTP query bounds, missing Runs,
and the root public-progress prompt boundary.

React tests cover source labels, verified Harness markers, the private-
reasoning disclaimer, truncation, stream errors, empty state, and fail-closed
behavior if private reasoning is ever declared included. TypeScript generation,
strict type checking, component tests, and the Vite production build consume
the Go-generated OpenAPI contract.

## 中文结论

Prayu 展示的不是模型私有思维链，而是“模型公开进度 + Harness 可验证事件”。
模型公开文字只代表模型对用户的说明；工具、审批、文件和生命周期事实必须来自
Go 持久事件。`run_activity.v1` 不透传原始 payload、Prompt、thinking、delta、
工具参数或工具输出，并固定 `private_reasoning_included=false`。这个切片只增加
可读性和审计表达，不增加任何执行权限。
