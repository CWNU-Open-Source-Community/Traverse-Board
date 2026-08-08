# ADR 0092: Safe Public Model Streaming And Desktop Convergence

- Status: Accepted
- Date: 2026-08-09
- Scope: P13-B1, P13-B2, and P13-B3; no schema migration

## Context

P13-A established a durable public activity timeline, but a user still saw no
assistant prose until a complete `root_lifecycle.v1` response passed strict
validation and was committed. Forwarding raw provider deltas would improve
latency while exposing private reasoning, malformed JSON, secrets, nested
tool data, or text that later fails the root protocol.

## Decision

Prayu parses only the top-level JSON string fields of the in-flight
`root_lifecycle.v1` object. A provisional message becomes visible only after
the exact protocol version and a valid root action are known. Unknown,
duplicate, nested, fenced, invalid UTF-8, invalid escape, policy-rejected, or
secret-bearing content is withheld. A bounded rune tail remains hidden while
the string is incomplete so a later token cannot turn already displayed text
into a credential prefix.

The resulting `model_public_stream.v1` snapshot is replaceable rather than
append-only. It binds the exact Run and active model attempt, carries a
monotonic revision, and contains only redacted public assistant text plus
bounded lifecycle counters. Raw provider JSON remains in worker memory. The
provisional snapshot is process-local, is never written to SQLite or Run
events, and disappears when the active call ends.

The authenticated read endpoint is
`GET /api/v1/runs/{run_id}/active-call`. It returns 404 before a call exists or
after it is released. The Desktop polls only while execution is pending,
replaces text by revision, ignores stale revisions, retains the last safe
snapshot during finalization, and converges on the durable Session/activity
projection. Cancellation uses the exact returned attempt identifiers and a
stable in-memory idempotency key. Reconnect does not replay provider bytes.

The interface labels this content as provisional model output and does not
call it chain-of-thought. Provider-private reasoning, hidden prompts, raw
deltas, tool arguments, tool results, and malformed root output have no public
projection.

## Consequences

- Users receive visible assistant progress before final persistence without
  widening model, Tool, file, process, network, browser, or terminal authority.
- A crash may lose provisional text; the durable validated Session message is
  the only canonical result.
- A 404 after a previously observed snapshot means finalization, not deletion
  of the already displayed safe text.
- Providers remain responsible for supporting the selected streaming
  transport, but no provider-specific raw reasoning format becomes a Prayu
  contract.

## Verification

Go tests cover incremental JSON, escapes and surrogate pairs, duplicate and
unknown fields, nested/fenced injection, secret truncation, policy rejection,
exact Run binding, process-local cleanup, and the invariant that provisional
text never enters durable events. HTTP tests cover bearer authentication,
body/query rejection, missing calls, invalid snapshots, and OpenAPI routing.

React tests cover strict response parsing, cross-Run rejection, 404 waiting and
finalizing states, transient reconnect, monotonic revision replacement,
visible provisional rendering, and exact cancellation binding. The production
Vite build and Desktop package gate pass.

P13-B3 also exercised the real configured
`deepseek/deepseek-v4-flash` route. One bounded Supervisor execution streamed
five events, repaired one initially invalid root response, used 771 tokens,
finished without tools, and committed the final assistant message to the exact
Session. Credentials and raw provider payloads were not recorded in this ADR.

## 中文结论

P13-B 展示的是经过 Go 增量解析、Policy 和脱敏后的公开助手文字，不是模型私有
思维链。增量快照仅存在于当前进程，不能恢复、不能作为指令、也不会写入 SQLite；
只有完整通过 `root_lifecycle.v1` 校验的最终消息才进入 Session。桌面端以全量快照
和单调 revision 收敛，取消绑定精确模型 Attempt，断线重连不会重放原始 Provider
字节。本批不增加任何执行权限。
