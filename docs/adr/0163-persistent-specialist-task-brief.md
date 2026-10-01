# ADR 0163: Persistent Specialist task constraints and pinned attempt briefs

Status: proposed

Date: 2026-10-01

## Context

A consumed parent instruction disappears from the no-tool Specialist's actual
request after the recent-history window. Child-owned WorkItem descriptions and
acceptance criteria can also be truncated or omitted. Durable source records do
not themselves prove delivery. Mutable WorkItems and an empty prepared inbox
currently allow the same attempt to observe different task state after recovery.

## Decision

Reuse immutable parent messages and current child-owned WorkItems as task truth.
Schema 175 stores one bounded `specialist_task_brief.v1` snapshot per attempt,
including complete active instructions and unfinished WorkItems, ordered source
IDs, protocol/sequence or item versions, and SHA-256 bindings. Its effective
fingerprint excludes attempt and lease identity. Preparation is the cutoff:
same-attempt reads recover this snapshot, including an empty snapshot; later
source changes enter a fresh attempt. Current direct-parent, child, Run and active
lease checks still control preparation and dispatch.

Keep `specialist_instruction.v1` as the existing additive writer. A separate
strict `specialist_instruction.v2` adds explicit append, replace and withdraw
operations. Replace/withdraw bind an earlier active message ID and its exact
stored payload hash in the same child/root/Run scope. Prose never implies a
withdrawal. Consumed instructions remain effective; explicit retirement records
remain retained so replay cannot resurrect old constraints.

Complete task constraints and WorkItems are mandatory before optional Notes.
Existing history bounds and model-window fitting stay bounded. Old child-context
user envelopes are excluded from historical replay so retired constraints are
not delivered again. Verify the complete current input after final fitting and
in repair/transport retries. A content fingerprint in `model.started` binds the
current attempt snapshot; it carries no new authority. Pending inbox deliveries
still consume exactly once only with the existing continue/finish transaction.
Repeated active constraints do not create new consumption of their old message.

Bounds are explicit: 256 instruction source operations, 32 active instructions,
20 active child WorkItems and 128 KiB snapshot JSON. Existing 4,096-token context,
28 KiB selected-source and 32 KiB final-input limits remain dispatch fences. Count,
size, identity or fingerprint failure stops the attempt before model dispatch;
the original records are retained. No model summary, paid call, tool loop,
credential, network, approval or execution grant is added.

## Compatibility, recovery and rollback

Migration 175 is forward-only. Historical migration checksums and the v1
delegation-application writer/binding remain unchanged; only the current delivery
guards gain v2 support. New snapshots and eligible source payloads are immutable;
source deletion is blocked while their Run exists. Whole-Run cascade semantics
are retained. A new attempt rebuilds task truth under its own lease and receives
its own snapshot and dispatch receipts. Old delivery receipts or leases never
authorize it. A lost pre-upgrade running attempt is terminalized by existing
lease recovery and rebuilt; migration does not invent delivery evidence.

Older binaries reject schema 175. Roll back writers only with the new readers and
guards retained, or restore a pre-upgrade backup; never remove migration history
or reinterpret v2 records as v1. Original Session history and task records remain
available for audit; no automatic summary alters them.

## Validation

Verify actual outbound requests independently of model replies: history count
and byte pressure, full WorkItem text, Notes competition, final-window fit and
refusal, explicit correction/retirement, two-child isolation, reopen and pinned
empty recovery, crash/fresh leases, idempotent consumption, immutable sources,
transaction rollback, v174 upgrade and v1 compatibility. Independent parent
review and exact-HEAD CI remain required before any merge.
