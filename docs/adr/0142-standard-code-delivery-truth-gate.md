# ADR 0142: Standard Code delivery truth gate

- Status: Accepted
- Date: 2026-08-26

## Context

Standard Code already had Diff, Command Runtime, Artifact, Workspace
Checkpoint, Code Handoff, GitHub Review, and operation receipts. Those facts
were projected independently, so an Agent could present a successful narrative
without one machine contract proving that the commands were terminal, the
output was complete, and the tested revision was still current.

Completion also needs explicit negative outcomes. No applicable tests, user
skip, budget exhaustion, truncation, missing dependency, approval denial,
cancel, timeout, and post-test mutation must not collapse into a generic
success or failure label.

## Decision

Introduce the externally durable `standard_code_delivery.v1` family and schema
v137 immutable ledger.

The Application captures a final Checkpoint, a combined base-to-live Git Diff,
and a second Workspace observation in a bounded retry loop. It records only
when HEAD, branch, index and Checkpoint revision align. Verification facts come
from selected durable Command Runtime Jobs and their after-Checkpoints and
Artifacts. A passed verification requires terminal `completed`, exit code zero,
a reaped process tree, complete bounded output evidence, the current final
revision, and unchanged permission/backend binding.

The status set is closed to `passed`, `failed`, `partial`, `not_run`, `blocked`,
and `stale`; only `passed` is verified. Explicit declarations represent no
tests, user skip, budget, dependency, or approval outcomes. Agent text and CI
status names are never evidence inputs.

The stored receipt is immutable. Freshness is a read-time observation excluded
from its digest. Workspace, Drydock, permission, backend, capability or
Supervisor mutation drift projects the old receipt as stale without rewriting
history.

Desktop, CLI, HTTP, Code Handoff/export, GitHub Review and the final Standard
Code reply consume the same Go `Report`. Standard Code finish and lifecycle
completion require its current passed projection. The report provides
navigable relative files, Artifact references, the exact final Checkpoint and
Undo/Rewind/Fork entry points.

Public validation rejects control text, secret-like material, absolute host
paths, raw environment/output, private reasoning and mutation-grant flags.
Unsafe relative paths are omitted while hashes retain provenance.

## Consequences

- Schema v137 adds an append-only receipt table bound by foreign keys and
  insertion/immutability triggers.
- Mutating after verification requires re-running verification and recording a
  new receipt; reverting bytes does not revive a receipt after the Supervisor
  mutation epoch changed.
- Output truncation or missing Artifacts cannot be displayed as verified.
- Repositories choose their own verification commands; the protocol records
  exact Jobs rather than prescribing a universal test command.
- Recording creates a final Workspace Checkpoint but does not commit, push,
  merge, overwrite the source Workspace, or delete unattributed files.
- Checkpoint recovery is limited to captured Workspace and index content; it
  does not claim to undo external side effects.
- A Run-owned Drydock/worktree remains an ownership and recovery mechanism,
  not a process-isolation claim.
