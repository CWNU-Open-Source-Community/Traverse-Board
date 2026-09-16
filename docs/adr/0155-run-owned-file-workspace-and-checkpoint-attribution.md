# ADR 0155: Run-owned file workspace and checkpoint attribution

Date: 2026-09-09
Status: Implemented locally; file/failure paths validated, successful command delivery remains open

## Problem

The Standard Code preset preserves the source workspace on Mission, Session and
Thread, while commands execute in the Run's owned Drydock. Real Web validation in
phase H showed file tools reading and proposing changes against the source. A
proposal could therefore modify a different directory from the one being tested.
The source had CRLF content while its Git worktree had LF content; the resulting
hash mismatch was evidence of two distinct targets, not a reason to relax checks.

## Decision

Resolve the file target using the Run and its durable source identity. Reuse
DrydockService's actual managed-root, Git worktree, branch, base and generation
checks. A configured Run or any Run owning a Drydock requires that exact ready
target. Missing configuration, ownership drift or an unavailable runtime fails
closed. Ordinary source-only Runs retain their current behavior.

Keep the source control identity on Mission, Session, Thread and Supervisor.
Carry the effective filesystem target through Agent Code / CodeIntel scopes,
FileEdit proposal, approval, apply, inverse, checkpoints and read projections.
Capability and attempt validation remain exact. Billing invocation IDs and
durable Supervisor call IDs have different purposes; checkpoint attribution must
resolve the triggering attempt using its actual persisted Supervisor call.

The new SQLite v153 migration changes only the FileEdit Apply insertion guard to
accept an exact Run-owned Drydock. Existing event, approval, content hash and
operation identity conditions remain. Existing migrations and completed Apply
records are immutable. Regenerate the clean-install baseline from the forward
migration plan. New source writes are rejected once the Run owns a Drydock;
historical source edits remain readable under their original identity.

File boundaries reuse existing snapshots and transaction journals. Their cursor
and observed Git binding advance through the owned Drydock lifecycle, leaving
the source cursor intact. Complete this attribution before persisting the Apply
result. The private file-boundary service propagates cursor replay failures;
failed or interrupted journals cannot authorize another write. A completed Apply
receipt replays without filesystem or cleanup operations.

Recovery dispatches by each transaction's recorded workspace. An older owned
transaction that still has its exact ordinary cursor uses that legacy cursor;
it is not reinterpreted as a new file boundary. New boundaries use the Drydock
cursor. Before attributing a persisted checkpoint, compare a fresh, non-persisted
capture's root, path, commit, branch, index and manifest, and confirm the observed
Git binding remains stable. Later external content cannot be attributed to an
older snapshot. Interrupted captures retain their incomplete status.

HTTP lists preserve source history and the exact owned target, filtering before
paging. Current change sets summarize only the effective target. History remains
readable when target resolution fails, with mutation actions disabled. V2 labels
the current target, historical directory and source-project diff separately.
Timeline records distinguish a proposal, approval and actual application;
audit-event counts are not advertised as counts of modified files.

New model HostCommand proposals are unavailable on configured/owned Runs because
that separate legacy path binds source commands. Do not relabel such execution as
Drydock work or broaden authority to make the proposal succeed. Existing Host
approval/execution history remains a separate, unconverted workflow.

## Alternatives and costs

- Replacing Mission/Session workspace IDs would rewrite control history and
  invalidate existing snapshots and approvals. Use a separate effective target.
- Inferring a worktree path from a source ID loses per-Run ownership and existing
  Git validation. Reuse the registered Drydock service.
- Adding another checkpoint table or mutation state machine would duplicate
  existing durable journals. Use their current transaction and lifecycle APIs.
- Silently adopting old source proposals would change what the user approved.
  Keep them historical and require a fresh target-specific proposal.
- Snapshot comparison and repeated Git observation add latency. They protect a
  demonstrated crash/external-edit boundary; no performance improvement is claimed.

## Verification and limits

Phase I validates actual SQLite/Git recovery, browser approval/application and
persisted Supervisor attempt attribution. The real command targets the same
Drydock but Windows PowerShell 5 fails during PSEtwLog initialization under LPAC,
before the repository check executes. Its real report remains failed/unverified.
Successful command delivery is still open; host shell smoke does not prove LPAC
support, and no permissions or trusted executable paths were broadened.

Command Jobs retain the source control workspace ID, while their canonical root
digest binds the physical Drydock. Correct the existing product E2E collector to
check both identities, not to demand a Drydock ID on the control record. Its input
fixture tests are distinct from actual operating system execution evidence.

This decision adds no model capability ranking, unrestricted Host execution,
new delivery gate UI, commit, push or merge behavior. Full native interaction,
all approval types and arbitrary command-side-effect rollback remain outside
this particular repair.
