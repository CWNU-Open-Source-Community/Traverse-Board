# Historical risk escalation evidence

The `risk_escalation.v1` producer, review actions, grant-consumption writers and
command executor are retired by [ADR 0165](adr/0165-retire-legacy-command-execution.md).
New commands use [bounded Command Runtime approval](convergence/command-bounded-approval.md).

## Reading retained records

Historical Host/Risk list and detail GET routes, CLI history and Desktop projections
still show saved proposals, exact command fingerprints, Run/Supervisor bindings,
reviews, grant-use metadata, invalidations, intents, results and receipts. A grant
row is historical metadata, not an execution capability. No reader creates a
proposal, renews an approval, consumes a grant or starts a process.

The stored envelope may describe executable/argv, cwd, environment-name digests,
network targets, credential kinds, host paths, policy refusal and resource limits.
Secret values and capability bearers are never returned. Historical field names
and protocol decoders are retained so upgrades do not erase user evidence.

## Exact saved-outcome continuation

The authenticated historical resume operation accepts no command, approval or
grant parameters. It checks the exact Run, proposal and durable Supervisor call.
A saved denial, result or invalidation may settle only that original call. An
execution intent without a result remains unknown; recovery records that fact
without resending the effect. Pending or approved records without a saved outcome
cannot regain execution authority. Saved ordinary Host outcomes can continue only
their existing Thread, subject to current lifecycle and lease checks.

## Historical event reconstruction

Historical schema v136 used `tool_approvals` as the decision ledger. The Deck Log is
the ordered Run event stream; the Bell Book is reconstructed from the exact
proposal, Approval/Grant, consumption, write-ahead intent, result, receipt, and
invalidation records.

| Order | Durable fact | Principal event |
| --- | --- | --- |
| 1 | immutable proposal and pending Approval | `approval.requested`, `risk_escalation.proposed` |
| 2a | one-call operator decision | `approval.decided` |
| 2b | bounded grant creation and exact use | `approval.grant_created`, `approval.grant_consumed`, `approval.decided` |
| 3 | write-ahead intent | `risk_escalation.execution_prepared` |
| 4a | terminal metadata result and receipt | `risk_escalation.execution_completed` |
| 4b | expiry, revocation, drift, exhaustion, or uncertainty | `approval.grant_expired` / `approval.grant_revoked` / `approval.grant_invalidated`, then `risk_escalation.invalidated` |

Proposal, operation, consumption, intent, result, receipt, and invalidation rows
are immutable. Events contain bounded identities, fingerprints, counters, and
status only; credential values, environment values, raw output, operation keys,
and capability bearers are not representable.
