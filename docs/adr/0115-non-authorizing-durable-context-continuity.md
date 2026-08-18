# ADR 0115: Non-Authorizing Durable Context Continuity

- Status: Accepted
- Date: 2026-08-19
- Scope: Project instructions, explicit long-term memory, Session continuity; schema v114
- Related: ADR 0052, ADR 0106, issue #106

## Context

Prayu already had bounded compaction, provenance-bearing Session messages, immutable Run
configuration, and narrowing-only `.prayu/config.yaml`. It did not have directory-scoped workflow
instructions, an explicit user/project memory lifecycle, or a browsable checkpoint and branch
model. Treating repository documents or durable history as trusted instructions would allow prompt
injection to outlive a single turn. Treating a checkpoint as runtime state could also resurrect an
expired approval, credential, process, or terminal lease.

The design must preserve useful continuity while retaining the existing rule that authority comes
only from current Go-owned policy and explicit operator controls.

## Decision

### Project instructions are immutable, untrusted Run input

Go discovers a closed set of files inside the exact Workspace boundary. Root-to-target ordering,
nearest-directory precedence, canonical paths, stable content hashes, source scope, trust, and a
why-effective explanation are part of `project_instruction_snapshot.v1`. Every source carries a
closed `InstructionAuthority` value in which only workflow, formatting, and validation guidance are
true. Tool, network, Secret, Debug, plugin, hook, and policy-override fields must remain false.

Discovery is bounded by count, bytes, and depth and rejects escapes, symlink/reparse indirection,
special files, invalid text, malformed ignore rules, and concurrent byte changes. Run creation pins
the snapshot and fingerprint. A live rescan can explain drift but cannot mutate the active snapshot.
An explicit refresh binds both the prior pinned fingerprint and the live fingerprint the operator
reviewed before it appends an immutable revision.

### Long-term memory is an explicit data lifecycle

`context_memory.v1` has only `user` and `project` scopes and only operator-explicit provenance.
Creation and every update reject model/tool/system actors. Records use optimistic versions and
support edit, disable/enable, bounded retention, export, and physical deletion. Secret-like content
requires explicit redaction; sensitive source/reference classes remain unrepresentable. There is no
automatic extraction path from messages, files, tools, summaries, or model output.

Deletion removes the row rather than recording a body-retaining tombstone. Continuity snapshots
refer to memory only by identity, scope, version, and digest. A later tree can therefore explain a
deleted, disabled, expired, or changed reference without recovering the prior content.

### Continuity snapshots contain context, never runtime authority

`continuity_snapshot.v1` captures bounded redacted summaries, recent message content plus existing
provenance, active memory references, pinned project fingerprints, and exact Git branch/HEAD. Its
`ContinuityAuthority` structure must be entirely false. A stored root/checkpoint/fork/resume node is
immutable and audit-bound to Run, Session, Workspace, source or parent, and snapshot fingerprint.

Fork and Resume atomically create a new Mission, Run, Session, and branch marker. They carry forward
the exact bounded snapshot plus the source Run's pinned budget/mode/configuration, but initialize
fresh non-authorizing execution state. The explicit exclusion set is approvals, capability grants,
credentials, Debug sessions, execution leases, network authorization, processes, terminal leases,
and execution profiles.

The Session tree combines durable continuity nodes with read-only projections for compaction,
decision Notes, Artifacts, and Delivery checkpoints. Derived projections are not valid branch
authority. Memory and Git drift are warnings, not automatic mutation.

## Consequences

- Nested repositories get deterministic, explainable instructions without turning repository text
  into policy.
- Active Runs cannot drift with disk edits. Refresh is visible, fingerprint-bound, and append-only.
- Users can inspect, correct, disable, export, expire, and permanently delete long-term memory.
- Fork and Resume preserve enough reasoning context for alternate work while always requiring fresh
  authority for any effectful action.
- A v114 restart restores data and auditability only. It cannot restore process-local tokens,
  credentials, processes, browser/debug sessions, or leases.
- The continuity tree does not restore Workspace file bytes. A separate reversible-edit/checkpoint
  design may attach file-state restoration later without changing this authority boundary.

## Security And Privacy

The threat model treats every repository file, memory body, prior model response, summary, and tool
result as potentially hostile data. Go type validation, SQLite checks/triggers, strict HTTP JSON,
bounded prompt sections, redaction, and current-policy re-evaluation are independent layers. The
full bilingual threat table and deletion semantics are documented in
[Project Instructions, Long-Term Memory, And Session Continuity](../context-continuity.md).

## Verification

Tests cover root-to-target ordering, platform path canonicalization, ignore behavior, secret
redaction, malicious authority claims, traversal, symlink and size failure, stable fingerprints,
snapshot diff/confirmation, explicit-only memory actors, sensitive sources, retention, optimistic
updates, physical delete/export, continuity fingerprinting, forged provenance and authority
rejection, checkpoint/Fork persistence, fresh Run/Session creation, memory drift warnings, database
reopen, v113-to-v114 migration, strict authenticated HTTP routes, OpenAPI route coverage, CLI output,
and frontend type/test/build gates.
