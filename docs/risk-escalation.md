# Durable risk escalation

Issue #138 adds `risk_escalation.v1` for the exceptional case where a trusted
Standard Code Run in `workspace_access` needs an action that its default
Workspace boundary cannot authorize. The feature does not switch the Run to
Full Access. It turns that one request into an exact, durable proposal and parks
the existing Supervisor tool loop until an operator decides it.

## What is proposed

`host_command_propose` accepts the risk protocol only from the Code root
Supervisor under current Agent Code authority. The immutable proposal binds:

- Run, Mission, Session, Workspace, root Agent, Supervisor turn, durable tool
  call, and the original tool invocation;
- mode, interaction, execution-profile, and permission snapshot IDs and
  revisions;
- Workspace-root fingerprint and Agent Code capability generation;
- executable path and SHA-256, every argv item, working directory, sanitized
  environment-name digest, host-network intent, timeout, output limit, process
  limit, and memory limit;
- a normalized risk scope and its SHA-256 fingerprint.

The risk scope is a closed set. Each selected kind requires its corresponding
metadata:

| Kind | Required review metadata |
| --- | --- |
| `network` | exact targets and a bounded purpose |
| `credential` | credential kinds only; never values |
| `host_path` | exact absolute paths |
| `policy_denial` | stable policy code and reason |
| `non_whitelisted_tool` | requested tool identity |
| `other_high_risk` | bounded explanation |

Inputs are normalized and sorted before fingerprinting. Secret-like values are
rejected rather than persisted in redacted form. The model cannot set reviewer
identity, confirmation, authorization kind, grant TTL, use count, generation,
grant ID, or a capability bearer.

## Operator decisions

The Desktop approval panel and control-token HTTP route expose three choices:

1. **Deny.** The Approval is durably denied and the exact Supervisor call
   resumes with an ordinary denied tool result. The model may choose an offline
   alternative or finish.
2. **Approve once.** The exact proposal receives one approval. No Session grant
   is created.
3. **Grant exact scope to the current Run.** The operator explicitly chooses a
   TTL from 1 to 900 seconds and 1 to 8 total uses. The bounded grant is tied to
   the current Run, Session, Workspace, risk-scope fingerprint, all four
   snapshot IDs/revisions, Workspace-root fingerprint, capability generation,
   and a monotonically increasing generation. Every authorized proposal writes
   one immutable consumption row and decrements the remaining-use count.

The grant ID is metadata, not a bearer, and is never delivered to a model,
Skill, MCP server, or repository content. Those sources also cannot act as a
reviewer. A grant from another Run, Workspace, Session, scope, snapshot, root,
or capability generation cannot authorize the proposal. The ordinary grant
CLI can inspect and revoke a bounded grant:

```text
cyberagent approval grant list --run <run-id> --tool host_command_propose
cyberagent approval grant show <grant-id>
cyberagent approval grant revoke <grant-id> --reason "scope no longer needed"
```

The generic CLI `grant create` command intentionally cannot mint a risk grant;
only the exact risk-review flow can create one.

## Durable wait and exact continuation

Creating the proposal atomically creates the existing Approval record, moves
the Run to `waiting_approval`, and releases the Run execution lease. Closing the
renderer or application does not remove the proposal. On restart, the same Run
and proposal are visible through the existing host-command proposal list.

After a durable denial, execution result, or invalidation exists, the control
plane resumes only the proposal's original Supervisor turn and durable tool-call
ID. Replaying the resume request after that call is terminal is a no-op. It
cannot create a replacement turn or execute the side effect again.

Approval first rechecks the current Run/Workspace binding, every snapshot and
revision, capability generation, executable identity, working directory, and
sanitized environment digest. Drift invalidates the proposal and any matching
active bounded grant before the Run receives a non-executing failure result.

## Write-ahead execution fence

Before the host process may start, the control plane commits an immutable
execution intent bound to the proposal, Approval fingerprint, optional grant
generation/consumption, and exact command. A terminal result and metadata-only
receipt are then committed together with untrusted evidence.

If an intent exists without a durable result, the outcome is permanently
`execution_uncertain`. The proposal and related grant are invalidated and no
automatic path may retry it. Application restart restores records, never the
process-local authority required by the host executor.

The host process is non-sandboxed and can use host networking. Drydock and Git
worktrees provide ownership and recovery boundaries; they are not security
sandboxes. Process authority still requires the current process-local Workspace
Access and operator-approval gates.

## Deck Log and Bell Book reconstruction

Schema v136 reuses `tool_approvals` as the only decision ledger. The Deck Log is
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
