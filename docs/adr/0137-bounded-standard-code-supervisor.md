# ADR 0137: Bounded Standard Code Supervisor completion protocol

- Status: Accepted
- Date: 2026-08-25
- Scope: GitHub Issue #137; SQLite schema v135

## Context

Workspace tools already read and apply reviewed edits, Command Runtime already
owns real foreground and background processes, Workspace Checkpoints already
record mutation boundaries, and the root Supervisor already has a durable
multi-tool loop. None of those individual components proved that a model had
inspected the repository, applied a real change, observed a failed verification,
fixed it, and verified the current result before claiming completion.

A prompt-only sequence would not survive cancellation or restart and could not
fence repeated side effects, permission drift, output budgets, or background Job
ownership.

## Decision

Add `standard_code_supervisor.v1` as a bounded state coordinator inside the
existing `RunSupervisor`. It activates only for the root of an exact configured
Standard Code preset. It does not schedule another Agent, introduce another
model loop, or widen the existing tool registry.

The coordinator requires two consecutive read-only rounds before Plan, the
existing explicit operator Plan-to-Deliver selection before writes, a completed
Checkpoint transaction for each mutation epoch, and structural Command Runtime
success for that same epoch before `finish`. Failure enters Diagnose; fixes and
commands remain bounded. Persistent failure or drift produces a durable stop and
cannot be converted to success by model output.

The fixed local limits cover per-turn tool rounds, commands, Jobs, fixes, output,
no progress, repeated failure, ledger size, and snapshot size. Existing Run
token, elapsed-time, and total tool-call budgets remain authoritative and their
immutable limits are copied into the completion projection.

## Authority and recovery

Every state row binds the configured preset, Run/Mission/Workspace, root Agent,
mode, execution-profile, interaction, permission, and browser-CDP revisions,
current Supervisor turn/attempt, active lease, and exact Run event. Call decisions
additionally bind the existing Supervisor tool call. The new ledger stores structural facts and digests; raw bounded
arguments/results remain in the existing call and Artifact ledgers.

After an applied edit, the coordinator reads the exact after-Checkpoint and
derives the root identity and only capability generation accepted by the next turn.
The explicit Plan-to-Deliver revision recomputes its new generation from that same
bound root, so the legitimate phase transition does not authorize arbitrary drift.

Deterministic intent fingerprints suppress a different call that repeats an
already handled side effect. Recovery of the same call continues through the
existing tool execution receipt and each underlying idempotent Workspace or
Command Runtime operation. Background Jobs retain Run ownership, permission
revision, mutation epoch, and monotonic output cursor across turns. A stale-epoch
Job can be observed or terminated but cannot verify a newer edit. Cleanup of
an owned Job remains possible after completion-budget stop, but permission drift
never restores control.

## Fail-closed boundaries

Plan continues to expose read and Plan proposal tools only. Review, Learn,
Cyber, child, and Specialist contexts do not inherit Standard Code process or
write authority. Repository content, stdout/stderr, MCP output, and tool text are
untrusted evidence and cannot change state-machine policy. The protocol never
installs dependencies, enables network or credentials, or falls back to an
unsandboxed host process.

The Drydock/worktree remains an ownership and recovery boundary, not a security
sandbox. The selected Local OS or fixed Docker adapter remains the process and
network isolation boundary.

## Consequences

Completion now has an independently auditable Go proof instead of a model
claim. The additional snapshot and transition ledger increase migration and
test surface, and a stale or ambiguous state stops rather than attempting to
infer progress. Language-specific verification remains a repository-evidence
choice inside the existing constrained Command Runtime.

## Verification

Tests cover two consecutive read rounds, reviewed Checkpoint-bound mutation,
failure/diagnosis/fix/current-epoch success, persistent failure budget stop,
false-finish rejection, restart duplicate suppression, permission and cursor
drift, background start/read/wait/cancel/kill ownership, stopped cleanup, exact
Standard Code preset/lease persistence, v134-to-v135 migration, and the existing
Plan/Review/Learn/Cyber/Specialist capability matrices.
