# Gemini signature replay

The OpenAI Chat adapter preserves Gemini 3 tool-round thought signatures for
the official Google AI Studio HTTPS endpoint at
`generativelanguage.googleapis.com/v1beta/openai/chat/completions`, including
configuration using the documented `/v1beta/openai` base URL. This scope uses
the actual wire model after custom model mapping: `gemini-3-…` or
`gemini-3.…`. A local route alias can select that model. A provider display name,
a proxy hostname, Vertex endpoint or a `google/` prefix does not establish the
AI Studio protocol.

The configured route model, sent wire model and returned upstream model are
recorded separately. The existing Chat acceptance rule for a valid upstream
model name remains unchanged; the adapter does not invent alias equivalence.
Private replay binds the route, exact wire model, provider, endpoint, runtime
binding, native response identity, ordered tool IDs and the accepted arguments.
Changing that provenance rejects the continuation before sending HTTP.

Only `extra_content.google.thought_signature` is supported, separately at the
assistant-message level and each tool-call position. Each signature is a
complete opaque string. Repeated identical stream metadata is idempotent;
conflicting complete values fail closed. Argument fragments retain the existing
Chat assembler. Late metadata, including an empty-content terminal delta, is
captured without changing finish, usage, model, SSE or `[DONE]` requirements.
Unknown extension siblings are ignored and never reflected into requests.

For Gemini 3 the first tool call of each current-turn step must carry its own
signature. Subsequent parallel calls may be unsigned; the first signature is
never copied to them. Every prior step retains its own signature when the next
request contains only tool results. The original assistant batch precedes all
its paired results. Durable application IDs are mapped back to original native
call IDs, while accepted tool arguments remain the sole argument authority.

Application limits are 256 KiB per signature, 1 MiB aggregate signatures per
response and across outbound replay history, the existing tool-call count limit,
and 1 MiB per persisted envelope. These are local safety bounds, not advertised
Google limits. Oversized or malformed recognized metadata is rejected before
accumulation; opaque strings are never trimmed, redacted, concatenated or
base64-reencoded. Public output, events, ordinary JSON and diagnostics do not
include these private fields.

Persistence uses numeric private replay envelope v3 under the existing tool-round
transaction and ownership fences. Readers retain Responses v1 and Anthropic v2
without rewriting rows or reinterpreting versions. Unknown versions and
version/transport mismatches are rejected. A rollback to a binary that cannot
read v3 requires a matching pre-v3 database backup; retain the current database
and its private evidence for recovery. The retained compatibility fixtures are
listed in the `control-plane-ledgers` protocol registry entry.

The Registry freezes a private sequential probe plan for scoped custom and
environment configurations. Qualification uses three logical model calls and
two synthetic echo rounds within the existing 30-second deadline, with 256
tokens per call and the existing stream bounds. Instructions for both rounds
and the final strict JSON acknowledgement are present from the first request;
continuations add assistant replay and tool results. Scoped old one-round
records have a different binding and cannot qualify this protocol. Other
providers retain their two-call probe and binding. Qualification does not
execute a product tool or grant specialist/fan-out tool authority.

Supported evidence is deterministic local HTTP/streaming, qualification,
private-envelope and SQLite failure/retry/reopen testing. No paid-provider
acceptance is claimed. Gemini 2.5, Vertex, proxies, omitted stream tool indices,
fragmented signature strings and unknown provider-private fields have no new
support. Optional signatures from final text-only responses are validated when
recognized but are not persisted across later ordinary user turns by the
tool-round Store; this change does not claim complete cross-turn text history.

Protocols checked on 2026-10-01:

- [Google thought-signature guide and OpenAI examples](https://ai.google.dev/gemini-api/docs/generate-content/thought-signatures)
- [Google OpenAI compatibility](https://ai.google.dev/gemini-api/docs/openai)
- [Google ADK Chat response metadata](https://github.com/google/adk-java/blob/main/core/src/main/java/com/google/adk/models/chat/ChatCompletionsResponse.java)
- [Google ADK Chat request metadata](https://github.com/google/adk-java/blob/main/core/src/main/java/com/google/adk/models/chat/ChatCompletionsRequest.java)

The terminal message-level metadata path is also evidenced by the official ADK
implementation. AI Studio does not document signature-string concatenation or
conflicting retransmissions; rejecting conflicts is an application policy.
