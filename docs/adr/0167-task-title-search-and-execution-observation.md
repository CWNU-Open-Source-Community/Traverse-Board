# ADR 0167: Server-side task title search and execution observation

- Status: Accepted for issue #272
- Date: 2026-10-07

## Context

The sidebar searched only the task titles already fetched by the browser. Older
matches were invisible until every preceding page was loaded. Its status dot
represented composer availability, which cannot establish whether an Agent is
executing, waiting for approval or idle between turns.

## Decision

Extend the existing read-only `GET /api/v1/threads` contract with optional `q`.
SQLite filters titles before applying the existing creation keyset window. The
query is a literal substring, not a wildcard expression or message-body search:
ASCII letters match case-insensitively, other Unicode characters match exactly,
and `%`, `_` and backslash retain their literal meanings. Trim surrounding space,
fold ASCII case and remove an empty query before binding the cursor scope. The
query is bounded to 256 Unicode characters. Omitted or blank `q` retains the
existing unfiltered behavior and lifecycle filters.

Order remains `created_at DESC, id DESC`; changing the query or lifecycle filter
requires a new first page. Equal timestamps use the immutable ID tie-breaker.
Inserting a newer task cannot shift an older cursor window. This is a live
keyset listing, not a frozen snapshot: a concurrent rename, archive, deletion or
execution transition can change membership or status between requests. No
unbounded history fetch, offset pagination or new persistent search index is
introduced. Existing page limits and the explicit truncation signal remain.

Add optional `execution_state` to the shared `ThreadView`; every list response
populates it. Existing consumers and non-list views remain compatible. The
closed vocabulary is `idle`, `running`, `stopping`, `stop_failed`,
`waiting_approval`, `paused`, `completed`, `failed`, `cancelled` and `unknown`.
`composer_state` continues to describe input availability independently.

Go combines a bounded batch of durable Run facts with one observation of the
process-local Thread execution registry. Live execution ownership establishes
running/stopping status. Absence from that registry alone does not prove idle:
another entry point or process can own a lease, and an interrupted handoff or
submission can still be unresolved. These uncertain records, missing observers,
inconsistent bindings and read failures yield `unknown`. Durable Run states
provide approval, pause and terminal observations when the evidence agrees.
Pending approvals are read from their ledger as well: a waiting tool can retain
`RunRunning` and a `turn_started` checkpoint after relinquishing its live owner.
That expected approval checkpoint is not an unfinished execution by itself.
Once a Run is terminal, retained supervisor checkpoints and historical pending
approvals cannot override its failed, cancelled or completed outcome. An active
lease or an unsealed handoff/submission still prevents a settled observation.
No lease, owner token, fencing identity, raw arguments, private path or new
execution authority enters the DTO. No migration or new state machine is needed.

## Presentation and recovery

The React query cache partitions pages by normalized search term under the
existing task-list invalidation prefix. A bounded debounce respects IME
composition; clearing the query starts or restores its own unfiltered list.
Late responses belong to their original query and cannot append to a different
result set. Search and pagination never navigate, select a task or replace a
composer draft. The sidebar describes the active-title scope; archived tasks
retain their existing settings entry.

Display execution status as text associated with each task, in addition to color.
Missing or unrecognized fields and failed list refreshes display unknown rather
than stale idle. A list refresh retrieves statuses together; rows never poll their
own execution endpoints. Loading, no matches, failure/retry, more pages and
truncation remain distinct states.

While the conversation sidebar is visible, a single cached page polls every 15
seconds. Loading additional pages pauses periodic reads because an infinite-query
refresh rereads every cached page sequentially. Historical rows show the last
observation until a manual refresh or an existing invalidation, focus or reconnect
refresh updates them. Switching to a single-page query resumes polling without
discarding historical pages, changing the selected task or replacing the draft.

## Alternatives, validation and rollback

Client-only filtering cannot discover unloaded matches. Polling each row adds
requests proportional to history size and yields unrelated snapshots. Deriving
execution from composer readiness or persistent `RunRunning` would claim facts
those fields do not establish. These alternatives are rejected.

Verification covers matches beyond the first hundred tasks, same-time keyset
pages, literal wildcard characters, query-bound cursors, legacy no-query calls,
live execution and uncertain ownership, read failures, rapid query changes,
clear/pagination and preservation of the selected task and draft. Existing
OpenAPI generation, affected Go/React tests and applicable CI remain the gates;
this change adds no repeated repository-wide gate.

Rollback removes the additive search/projection and sidebar presentation. Task,
Run, message and execution ledgers remain unchanged and retain their existing
ownership and authorization rules.
