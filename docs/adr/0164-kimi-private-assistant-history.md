# ADR 0164: Kimi K3 private assistant history

Status: proposed

Date: 2026-10-02

## Context

Kimi K3 uses Preserved Thinking History across tool calls and ordinary user
turns. The Chat Completions adapter discarded `reasoning_content`; the existing
private replay ledger required a tool round and could not retain an ordinary
answer. Root completion also projects lifecycle JSON into its public message,
which is not the original native assistant response.

Official protocol references checked for this change are the
[thinking guide](https://platform.kimi.ai/docs/guide/use-thinking-models),
[K3 usage example](https://github.com/MoonshotAI/Kimi-K3#6-model-usage),
[API schema](https://platform.kimi.ai/docs/openapi.json), and
[streaming guide](https://platform.kimi.ai/docs/guide/utilize-the-streaming-output-feature-of-kimi-api).
The [international](https://platform.kimi.ai/docs/guide/kimi-k3-quickstart) and
[mainland](https://platform.kimi.com/docs/guide/kimi-k3-quickstart) endpoints are
distinct replay origins. Guidance about large output budgets for older K2
models is not a new minimum or an authorization to enlarge the K3 probe budget.

## Decision

Enable this contract only for the resolved wire model `kimi-k3` on HTTPS
`api.moonshot.ai` or `api.moonshot.cn`, using `/v1` or the complete
`/v1/chat/completions` path. Bind the configured route, actual wire model,
canonical origin/path, runtime generation and native response identity. Provider
names, proxy endpoints, Kimi Code, older Kimi models and guessed aliases do not
enter this scope.

Numeric private envelope v4 stores only typed native metadata: a presence-aware
nullable reasoning string, the accepted assistant content shape/text, upstream
model, ordered native call identities and accepted argument hashes. Stream
reasoning fragments append exactly; absent/null fragments cannot erase earlier
text. Unknown extensions are discarded, including case variants of the private
field. UTF-8, duplicate-key, structure, per-response and aggregate-history bounds
apply. A successful ordinary answer also has a zero-call v4 marker, including
when reasoning is absent, null or empty. It is never inferred from old text.
Existing v1/v2/v3 readers and serialization contracts remain supported.

Schema 176 follows the retained schema 175. Two new immutable tables separate
successful zero-tool candidates from accepted session-message bindings:
`run_supervisor_assistant_replay` and
`run_supervisor_assistant_replay_bindings`. The primary model terminal writes a
candidate atomically with its successful receipt and accounting. Root completion
requires the latest exact candidate and matching native lifecycle action, then
binds it to the public assistant message in that same completion transaction.
Policy rejection, cancellation, stale leases, auxiliary/accounting-only calls
and unaccepted candidates cannot create historical bindings. Exact retries
verify stored private bytes and source/projection integrity; dropping or changing
replay conflicts.

History loading re-reads selected source messages under the current active
Root lease. It verifies session provenance, immutable completion/source tuples,
blob digests, ordered completed native tool rounds and accepted call arguments.
The application reuses the recorded tool-result projection and native aliases,
then restores the original ordinary assistant lifecycle JSON with its replay.
The public bounded tool-evidence record remains stored; its corresponding native
request segment uses recorded calls/results rather than that summary. Historical
rounds do not enter tool resumption or acquire execution authority. Current Go
policy, operator input and permission gates remain authoritative.

K3 qualification uses four logical requests: two synthetic tool rounds, a
strict ordinary JSON answer, and a fresh-user strict JSON follow-up carrying the
ordinary replay. Keep the overall 30-second timeout, 256-token per-request
ceiling, existing public output/chunk bounds and existing retry policy. A new
scoped binding invalidates older two-request qualification records. Do not
disable thinking or impose low reasoning effort to make qualification pass.

## Failure semantics and scope

Every scoped native assistant message requires its own matching replay and a
complete tool-result batch. Missing, cross-route, cross-origin, corrupt,
superseded, imported or summarized history fails before a new model request.
Root compaction, history omission and current-segment receipts cannot substitute
for K3 native history; existing capacities still apply and exhaustion stops
explicitly. Legacy Session chat lacks this ledger and refuses the scoped route.
Historical browser screenshot segments also refuse: their live pixel/note
delivery is fenced to the original turn and this change does not broaden that
reader to old attempts. It does not silently drop image inputs or reuse old read
authority. Operator-message images still use the existing session-source reader.
No generic context framework or Specialist workload is changed here.

Private state has no JSON/public DTO representation and formatting is opaque.
It remains in explicit private SQLite blobs and backups. It is untrusted protocol
data, never a tool argument, permission, approval, grant or public reasoning
output. Rejected replay retains already validated typed usage for failure
accounting; a missing or unvalidated usage receipt is not invented as zero.

## Recovery and alternatives

Do not fabricate empty tool rounds, weaken the v170 foreign key, backfill old
answers, copy unknown fields, or turn a summary into native reasoning. Keep the
v170 tool ledger and add the separate ordinary-answer ledger. Keep all old
readers while supported databases/backups need them. After schema 176 or v4 rows
are written, an older binary requires a matching pre-change database backup;
retain the original database and private replay evidence for recovery.

Tests cover actual local HTTP/SSE, parallel/sequential tools, retries, an ordinary
answer followed by a new user turn, qualification, atomic rollback, exact replay,
reopen, source/lease rejection, privacy and bounds. Exact-HEAD test/review evidence
is provided separately. No paid live-model call or new user grant is part of this
implementation validation.
