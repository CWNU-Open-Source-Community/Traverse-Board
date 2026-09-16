# ADR 0156: Persistent conversation and working directory, independent turn outcomes

Date: 2026-09-09
Status: Implemented and locally validated within Phase M scope; uncommitted and unpublished

## Problem and scope

After a temporary model failure, the operator should be able to send a correction
or “continue” in the same conversation. Previously a successor Run could retain
some messages while resolving file operations back to the source project. This
lost the effective working context even though the old modified worktree remained
on disk. Exposing an additional failed-Run recovery workflow did not solve that
product problem.

The user explicitly selected Codex as the design reference. Its documented
[Thread/Turn model](https://learn.chatgpt.com/docs/app-server) supports a persistent
conversation with individually completed, failed or interrupted turns. This is
a semantic reference, not a claim of implementation or benchmark equivalence.

## Decision

Keep Thread as the conversation identity. A product turn has an independent,
durable outcome. Keep Run as the internal budget/configuration/execution epoch;
do not mechanically rename it or replace the existing harness. An ordinary
continuation stays in the existing Run when its context remains usable. Necessary
model, permission or budget transitions can create a successor transparently.

Close a recoverable failed turn only after persisted evidence proves that its
execution has ended and that no unresolved tool effects remain. Record the exact
accepted input and bounded, source-attributed failure/tool evidence in the Session.
Keep failed handoffs and real tool results unchanged. The next model call receives
that context without replaying the failed turn's tools. A verified `turn_failed`
response tells the client this turn has ended; a generic HTTP error does not.

Pending accepted corrections are presented before the next model call. They stay
in the queue until their own processing is committed. Stop preserves accepted
input; an epoch transition carries applicable unprocessed instructions as user
context with separate “not executed” provenance. It does not pretend cancelled
queue records were executed. Existing immutable request identities remain exact.

The composer sends ordinary messages after a pause or closed failure. Unknown
transport outcomes trigger one automatic confirmation of the original key/body.
Further attempts confirm that identity before starting a new request. The submission boundary carries the actual request identity when sending a new draft
first confirms an older request. A late response cannot clear a different draft or
a different conversation. This is not
an authorization to repeat a tool whose side effect is unknown.

## Physical directory and execution identity

A Thread's consecutive execution epochs use the same registered Drydock directory.
The physical row retains its original creator, Session, path and history.
`thread_drydock_bindings` records an immutable adjacent-epoch association;
`run_file_drydock_bindings` resolves each logical execution to its actual directory.
Read history and current execution authority are separate checks.

Publication validates the exact predecessor, source project, current physical
identity and quiescence. The latest Thread epoch holds the directory even while
paused or terminal. Old epochs cannot regain execution or cleanup authority. New
execution still requires its current permissions, backend capability and lease.
New checkpoints, commands, receipts and reports name the actual executing Run and
Session; a historical parent checkpoint retains its original attribution.

The selected plan and completed human work retain verifiable references to the
original checkpoint and handoff. They do not become new automated verification.
Previously observed mutations can initialize the new supervisor at “needs new
verification”, with exact source provenance. Old jobs, passed reports, grants and
consumed budget are never copied into new successful results.

Automatic expiration cleanup skips directories held by a Thread. Explicit cleanup
uses a narrow `drydock_cleanup_operations` reservation, serialized with publication
and execution. Metadata and the real final cleanup receipt commit together. A crash
does not release this reservation by timeout: the same request must confirm the
exact filesystem outcome. A stale dirty observation or an uncertain Git error cannot
close the reservation as preserved while another same-key removal may be in flight.
Completed callers return the canonical persisted receipt. A removed directory has no after-content snapshot, so
cleanup is not represented as a fabricated successful checkpoint mutation.

## Alternatives and costs

- Renaming Run to Turn would leave file ownership, failure and retry behavior
  unchanged. Retain the useful execution boundary and change its product contract.
- Copying a checkpoint into a fresh worktree for every epoch was implemented as a
  candidate and tested. It was rejected as the default because excluded dependencies,
  ignored caches and large files could make normal continuation fail. Real branching
  remains distinct from continuing a conversation.
- Moving the original physical row to each new Run would rewrite historical scope
  and undermine old receipts. The small immutable association preserves that scope.
- Reusing a checkpoint mutation to journal cleanup would require an invented
  after-checkpoint. The dedicated removal reservation records only facts that exist.
- A persistent directory requires physical ownership checks in existing execution,
  restore, delivery and cleanup paths. This cross-cutting maintenance cost is real;
  a UI-only recovery button would hide it rather than resolve it.

The existing snapshot/report completeness policy still applies. Preserving a large
dependency directory for continuation does not mean that excluded content was
captured or independently verified for delivery. Persistent drafts across an app
restart, all legacy Host/CLI paths, and a redesign of mandatory Plan ceremony remain
separate work. Dynamic results and limitations belong in the phase validation record.
