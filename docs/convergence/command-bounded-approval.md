# Bounded command approval

`command_runtime` supports an optional `review_scope` on one `run` or `start` command. It describes purpose and risk using the existing risk-scope format. The host pins the exact executable, arguments, environment, working directory and native adapter in the durable pending call. A model or plugin cannot supply a grant, permission snapshot, lease, runtime epoch or authorization fence through this metadata.

The existing ApprovalControl decision endpoint accepts `approve_for_run` with explicit `grant_ttl_seconds` (1–900) and `grant_max_uses` (1–8). This is a decision action inside Ask, Auto and Full. It is not another permission mode. Even in Full, a command that declares a review scope waits for explicit confirmation.

Commands with the same purpose/risk scope may share the existing Run grant. Each exact new command still requires a separate operator decision. Different arguments do not by themselves create a different scope. Different scopes, Runs or host authority bindings cannot share consumption. An active grant retains its original limits and expiry; the approval preview exposes those values. Changing its limits conflicts instead of silently renewing it. After exhaustion or expiry, an explicitly reviewed new command may create a new bounded grant.

The existing `tool_approvals`, `approval_operations`, `approval_session_grants`, `approval_grant_operations` and `approval_grant_consumptions` ledgers record the decisions. An exact replay reads the original consumption and never decrements again. The generic automatic session-grant method cannot authorize a command. No additional Run/Session database or SQL migration is introduced.

The final permitted consumption can execute after the grant becomes exhausted. An explicit revoke operation in the existing ledger vetoes it, including when revocation happens after that final consumption. Checking only the grant's `revoked` status would confuse these two cases.

The common operation authorizer checks the grant before dispatch and at the native launch boundary. The process manager retains the host's in-memory authority callback and rechecks it on its existing five-second ownership heartbeat, with a two-second check deadline. A failed recheck interrupts and reaps the owned process. This is polling, not instantaneous revocation; it cannot undo effects already produced. Cancellation retains the existing process-tree termination path. No callback is reconstructed from a stored receipt after restart.

Declarations do not provide filesystem or network isolation. A host working directory and a claimed risk scope cannot constrain a native program's actual effects. The installed native adapter and current host policy determine the available boundary; the common authorizer still applies.

## Compatibility and retirement stages

This increment adds the replacement bounded command path. It retains the legacy `host_command_propose` and `risk_escalation` creation/review/execution paths until their old-record bridge has passed positive and negative tests. The pending retirement list is:

- `HostCommandProposalToolExecutor.ProposeHostCommand` and old tool advertisement/wiring in the app and supervisor.
- `CreateHostCommandProposal`, `CreateRiskEscalationProposal` and obsolete public intent writers.
- ReviewService's duplicated bounded-grant creation/consumption and direct `HostExecutor.Execute` calls.
- The old native executor's duplicated five-mode decision logic, after an exact historical adapter uses the common authorizer.

Historical proposals, approvals, grants, consumptions, results and receipts remain readable. Completed decisions can be replayed without refreshing authority. A recorded execution start with no durable result remains unknown and is never automatically resent. An old approved command without an execution intent requires explicit confirmation and a bridge that retains the recorded constraints; it must not silently convert old permission data. These bridge and retirement steps are separate from this increment and are not claimed complete here.
