# Anthropic thinking and direct-tool replay

Traverse Board preserves native Anthropic Messages thinking during supported
direct-tool exchanges in both Chat and SSE streaming. This support does not
enable thinking automatically or grant additional tool permissions.

## Configure and qualify

For an explicit mode, edit a custom Provider with the `anthropic_messages`
transport in Provider settings. Its **Advanced JSON** is stored as
`advanced_config`; configure `request_body.thinking` there. For a model that
supports adaptive thinking, the editor value can include:

```json
{
  "request_body": {
    "thinking": {"type": "adaptive"}
  }
}
```

The built-in `anthropic` environment Provider follows upstream thinking defaults.
Use the exact model's [current thinking configuration](https://platform.claude.com/docs/en/build-with-claude/thinking):
adaptive, legacy manual `enabled` budgets, and `disabled` are not interchangeable
across models. An empty `thinking` string with a valid signature is supported.

Save the definition, then explicitly qualify the exact Provider/model using the
[Provider controls or CLI](usage.md#model-and-provider-commands). Qualification
retains two model calls, one synthetic tool, a 30-second deadline and 256 tokens
per call. A thinking mode requiring a larger allowance cannot pass this probe;
the probe does not suppress thinking or increase its budget. Explicit online
qualification can incur Provider charges. Local fixture results do not qualify
your endpoint or model.

## Continuation, privacy and reopen

The original ordered thinking, signature and `redacted_thinking` data are retained
as private protocol state and returned unchanged with the paired native tool
results, following [Anthropic's tool workflow](https://platform.claude.com/docs/en/build-with-claude/thinking-tool-workflows).
They are excluded from public chat text, history/event projections and ordinary
JSON/diagnostic formatting. Private replay remains sensitive data in the local
database and its backups.

Replay is bound to the original Provider, model, transport, endpoint/runtime
configuration, thinking setting, native response identity and accepted tool batch.
Retries and database reopen restore it under the original Run/turn/attempt/round
fence; each response retains its own identity. Changed bindings or incomplete,
malformed or truncated tool responses fail closed. Keep the same configuration
through the tool turn; use a fresh Run for a different configuration. Reopen does
not turn private state into tool authority or redispatch completed tools.

## Supported boundary

- Direct local-tool calls accept an absent/null caller or `caller.type: "direct"`.
  Supported caller metadata, absent/null/empty `toolset_name`, and null/empty
  citations survive private replay.
- Ordinary public text accepts nullable/array citations and SSE citation deltas.
  Private tool replay with nonempty citations or citation deltas is unsupported
  and rejected, rather than losing citation state.
- Server-tool callers, unknown caller variants and nonempty toolset families are
  unsupported. Their separate authority contracts are not treated as local tools.
- Stored Anthropic replay uses version 2; the existing Responses version 1 reader
  and format remain supported. Unknown versions are rejected; no old rows are
  rewritten or readers retired by this feature.

Evidence for this change is deterministic local HTTP/SSE, retry, two-tool-round,
Harness continuation and SQLite reopen tests. It makes **no live paid-provider
acceptance claim**, including for other Anthropic-compatible services.
