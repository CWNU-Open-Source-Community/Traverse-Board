# ADR 0140: Durable exact risk escalation

- Status: accepted
- Date: 2026-08-26
- Scope: GitHub Issue #138; SQLite schema v136

## Context

Standard Code intentionally defaults to `workspace_access`: trusted Drydock,
guarded Workspace files, disabled network and credentials, and no arbitrary host
authority. Real development can still require a dependency download, private
registry credential kind, exact host path, non-whitelisted tool, or an operation
that a current policy refuses. Requiring a pre-emptive switch to Full Access
widens authority for unrelated work, while an automatic escalation lets model
output cross the capability boundary.

The existing system already has immutable host-command proposals, the Approval
and Session Grant ledgers, Supervisor tool rounds, and fenced Run execution
leases. A second approval ledger or a resumable process handle would split audit
truth and weaken recovery.

## Decision

Add `risk_escalation.v1` as a Workspace Access variant of the existing
`host_command_propose` tool.

1. The proposal is immutable and binds the exact command, categorized risk
   metadata, resource budget, Run/Workspace/Supervisor call, current mode and
   execution snapshots, Workspace-root fingerprint, and capability generation.
2. The model proposes only. Operator controls choose deny, exact once, or a
   bounded current-Run grant. A bounded grant has exact scope, TTL no greater
   than 15 minutes, at most eight uses, generation, immutable per-proposal
   consumption, and explicit revocation/invalidation events. It has no bearer.
3. `tool_approvals` remains the sole decision ledger. Schema v136 adds proposal,
   operation, consumption, execution-intent/result, and invalidation facts, but
   no parallel decision table.
4. The Supervisor persists its pending call, moves the Run to durable
   `waiting_approval`, and releases the execution lease. A decision resumes only
   the same turn and call.
5. Host execution requires fresh process-local Workspace Access and operator
   approval gates plus exact in-process authorization. Persisted state cannot
   restore process authority after restart.
6. An execution intent is committed before process start. Intent without a
   durable result is permanently uncertain and cannot be retried.
7. Permission, mode, profile, Workspace, executable, root, or capability drift
   invalidates the proposal and matching active grant before execution.
8. Denial and invalidation return ordinary bounded tool results, allowing the
   same Supervisor loop to select an offline alternative.

## Consequences

- Operators inspect and authorize only a precise, bounded exception; Standard
  Code never silently becomes Full Access.
- Renderer close and application restart retain a visible wait without retaining
  a process or lease.
- A model, Skill, MCP server, repository document, or another Run cannot consume
  the grant or choose its bounds.
- Strict write-ahead uncertainty may require a human to inspect an external
  system before issuing a new, separately reviewed action. This is preferable to
  replaying a side effect.
- The host process remains non-sandboxed. Drydock and worktrees continue to be
  ownership/recovery mechanisms, not security sandboxes.

## Rejected alternatives

- **Switch the Run to Full Access:** authority is too broad and persists beyond
  the exceptional call.
- **One “allow all commands” button:** it cannot express or audit exact network,
  credential, path, policy, and resource boundaries.
- **Persist a bearer or process handle:** it would make restart restore authority
  and expose a target for models or extensions.
- **Create a second approval ledger:** it would make proposal/decision ordering
  ambiguous and duplicate existing Approval semantics.
- **Retry after an ambiguous host start:** it can duplicate external side effects.

Operational details and the event/receipt reconstruction order are documented in
[Durable risk escalation](../risk-escalation.md).
