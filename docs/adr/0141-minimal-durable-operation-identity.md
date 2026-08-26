# ADR 0141: Minimal durable-operation identity and replay pilot

- Status: Accepted and implemented
- Parent decision: [ADR 0135](0135-pre-1-0-product-convergence.md)
- Issue: [#165](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/165)

## Context

Many domain ledgers independently retain an operation-key digest and a canonical
request fingerprint. Their first replay question is common: did a retry use the same
key for the same request? Their transaction, authority, write-ahead intent, result,
recovery, and cleanup semantics are not common.

`runmutation.Operation` cannot become the shared kernel: it deliberately binds
structured WorkItem/Note mutations to a Run, Session, target, requester, and optional
supervisor lease. `operationreceipt.Receipt` also cannot become write authority: it is
a content-free presentation projection and does not prove a transaction or backend
cleanup.

ADR 0135 therefore approved only a two-flow pilot of a storage-free identity value
object and replay validator.

## Decision

Add `internal/durableoperation` with exactly three responsibilities:

1. validate a normalized, versioned domain separator and lowercase SHA-256 operation
   key/request digests;
2. create fingerprints from an ordered sequence of UTF-8 fields using an unsigned
   64-bit big-endian byte length before every field; and
3. decide whether two valid identities under the same domain and key are a replay or
   conflict.

The immutable value object contains only:

- the versioned domain separator;
- the operation-key digest; and
- the request fingerprint.

Its fields are private and can only be obtained through validating construction and
read-only accessors. It carries no raw operation key, payload, result, timestamp,
scope, approval, authority snapshot, lease, revision, receipt, recovery, or cleanup
state.

### Fingerprint encoding

For a domain `D` and ordered fields `F1..Fn`, the encoded hash input is:

```text
u64be(len(D))  || UTF8(D)
u64be(len(F1)) || UTF8(F1)
...
u64be(len(Fn)) || UTF8(Fn)
```

This preserves the existing `runmutation.Fingerprint` bytes used by both pilots. It
also makes field boundaries explicit: `("ab", "c")`, `("a", "bc")`, an omitted
field, and an empty field are distinct. Domain services still own field selection,
normalization, redaction, bounds, and semantic order.

Invalid domains or UTF-8 cannot produce a digest. The compatibility wrappers return
an empty digest on an impossible hard-coded-domain error, and existing domain
validation rejects that value before persistence.

### Replay decision

| Stored vs requested identity | Shared result | Domain action |
| --- | --- | --- |
| same domain, key, and fingerprint | `replay` | load and revalidate the exact durable domain result |
| same domain and key, different fingerprint | `conflict` | return the domain's existing conflict category and text |
| malformed/missing digest | fail closed | reject before any mutation or effect |
| different domain or key | fail closed as non-comparable | perform no replay decision; lookup/creation remains domain-owned |

The shared package does not return `apperror` values. Each domain preserves its
existing public error contract.

## Pilot flows

| Flow | Shared portion | Domain-owned portion retained |
| --- | --- | --- |
| Controlled Run creation | `run_creation_operation.v1` / `run_creation_request.v1` encoding and replay decision | Workspace lookup; Mission/Run/Session/mode construction; initial events; one SQLite transaction; initial-state revalidation |
| Scheduled Job control | `scheduled_job_operation.v1`, create/transition request encoding, and replay decision | Job ID output semantics; owner Run/root Agent; mode/permission authorization; expected revision; schedule; lease/round/recovery state; events |

Both call paths continue to compare their additional persisted domain bindings after
the shared identity decision. Scheduled Job create still treats its generated Job ID
as an output, while transitions still require the exact Job ID and expected revision.

## Compatibility evidence

- No SQLite migration, table, index, trigger, CHECK, event, API, CLI, or receipt was
  added or changed.
- Golden vectors pin the released Run creation digests and Scheduled Job digests,
  including Unicode, empty fields, field-boundary ambiguity, and field order.
- A combined fixture persists Run creation plus Scheduled Job create/pause operations,
  restores the real schema v122 boundary, upgrades through the complete historical
  plan, restarts, and replays every operation without adding an operation row or Run
  event.
- The same fixture proves changed requests still return the exact existing conflict
  category and message.
- Existing two-connection concurrency tests continue to prove one committed identity
  and one replay for each pilot.
- `operationreceipt` is unchanged and remains content-free presentation only.

## Authority and failure boundary

`internal/durableoperation` cannot:

- open or commit a transaction;
- read or write SQLite;
- grant approval, permission, capability, or execution authority;
- acquire, renew, release, or reconstruct a lease;
- choose write-ahead intent placement or start an external effect;
- create or validate a public receipt;
- reconcile a restart; or
- select or delete a cleanup target.

Missing or malformed identity input returns an error. It never becomes first-use,
replay, success, or authority.

## Rollback

The two pilots can be reverted independently to their previous length-delimited hash
helpers and field comparisons. Because the persisted bytes, schema, and public errors
did not change, rollback requires no database conversion, reader retirement, or
receipt migration. The golden vectors remain the compatibility oracle.

## Non-goals and expansion gate

- no generic repository, transaction callback, operation ledger, or state machine;
- no migration of approval, Docker, Git, FileEdit, batch delivery, or Command Runtime;
- no unification of intent, authority, lease, result, receipt, recovery, or cleanup
  tables;
- no expansion of `runmutation.Operation`; and
- no transaction authority for `operationreceipt`.

Another adopter requires a separate review proving that its identity comparison is
the same while its domain state machine remains intact. This pilot does not authorize
automatic repository-wide replacement.
