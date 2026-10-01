# ADR 0161: Provider failure boundaries and source-bound Specialist costs

Status: accepted

Date: 2026-10-01

## Context

Anthropic streaming errors exposed arbitrary upstream `error.message` text to
application failures and persisted model receipts. Pattern redaction cannot
remove ordinary private prompt text or opaque credentials. The actual HTTP
adapter also classified its client's timeout as caller cancellation before
headers, and as a protocol error while reading successful SSE. OpenAI Responses
had the latter problem. A failed read may deliver complete buffered frames and
an incomplete final line together, so prioritizing every Scanner error would
discard valid terminal or permanent-error frames.

Correct transport classification reaches existing finite retry paths. The
Specialist path previously released unknown failed-call reservations, used
model numbers that restarted for each Agent attempt as monetary keys, and could
associate a receipt from a different child or source. Retaining only the first
reservation would still allow another child or turn to reuse it before the cap
check. Its successful settlement also used cumulative Agent usage, which would
double-charge earlier protocol-repair calls after reconciling failed receipts.

## Decision

- Anthropic SSE errors use a fixed local diagnostic. Known wire error types
  select existing outcomes; arbitrary upstream messages, types and codes are
  not retained as messages or causes. Existing overload/api retry classification
  is preserved.
- At HTTP failure boundaries, consult the request's live caller context first.
  Actual cancellation and caller/Run deadlines retain only standard `ctx.Err()`
  as their cause. Client timeouts with a healthy caller are retryable network
  failures and do not carry a misleading caller-deadline cause. The context-free
  normalizer's existing contract, 60-second client ceiling and retry limits stay
  unchanged.
- Each line reader records whether ScanLines consumed a newline. Complete
  frames are processed before a buffered read error. A non-EOF error prevents
  decoding an unterminated tail or flushing an incomplete SSE event. Malformed
  complete frames, size limits, missing terminals and unexpected EOF retain
  their strict protocol failures; clean EOF terminals without a newline remain
  compatible.
- New Specialist calls carry `SpecialistAttemptID`. The monetary key is a
  positive int64 derived from SHA-256 of the domain `specialist_model_cost.v1`,
  Agent attempt ID and persistent model number, separated by NULs. The low 61
  bits plus bit 61 isolate it from legacy small integers. Existing scope already
  separates Specialist, root and fan-out rows; model and transport counters do
  not change.
- Persist the same derived key in existing Specialist start and terminal event
  payloads. Reconciliation authenticates exact run, source, subject, sequence,
  Agent attempt, model number, provider and model. Missing or duplicate evidence
  retains exposure; conflicting identities fail closed. Replay cannot strip or
  change the start binding.
- Sent Specialist failures settle known usage or retain the full unknown-cost
  estimate. Receipt persistence and settlement share the existing bounded event
  context, and failures stop further retries. Successful calls settle only
  their own response usage; cumulative usage still controls token budgets.
- Only Router's prepared-request rejection before provider dispatch creates a
  source-bound `dispatch:not_sent` receipt and releases its reservation. The
  dedicated Store method requires permanent failure, zero usage, no stream
  evidence and no retry. Zero usage alone never proves a request was unsent.
- Single release, terminal Run bulk cleanup and restart reconciliation use the
  same Specialist evidence. Legacy open reservations lack child/turn identity
  and remain conservatively retained; readers do not guess from a later receipt.

## Compatibility and recovery

No database migration, historical checksum change, new permission or tool grant
is needed. Existing legacy Specialist start/terminal records remain readable;
their absent monetary key maps to zero only for replay validation. Existing
legacy monetary rows retain ambiguous exposure until exact evidence is available.
New keys are additive and registered as an internal durable protocol. Removing
the new writer must retain the key-aware reader and conservative cleanup for
already persisted rows; an old binary must not resume monetary Specialist work
against these records because its cleanup does not enforce these guarantees.

No stream failure publishes a successful response or authorizes partial tools.
Approval, idempotency, lease ownership, cancellation and terminal-write fences
remain in their existing transactions. Unknown-cost settlement is a local
conservative estimate, not an upstream billing receipt.

## Validation

Real local HTTP adapters cover header/body timeouts, partial frames, complete
buffered frames, clean EOF, strict protocol failures and caller cancellation or
deadline. Actual qualified Router/Supervisor and SQLite reopen tests cover
privacy, bounded recovery, partial-tool non-dispatch, unknown costs and cap
refusal. Specialist HTTP tests cover unknown-cost retry, a one-reservation cap,
two children and two turns, protocol-repair usage and prepared-request rejection
with zero upstream calls. Store tests cover terminal-before-settlement reopen,
cancel cleanup, foreign receipts, replay drift, not-sent constraints and legacy
retention. These controls do not constitute an external-model autonomous
artifact acceptance or native crash-recovery test.
