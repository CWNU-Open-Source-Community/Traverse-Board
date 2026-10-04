# ADR 0165: Retire legacy command execution and retain historical evidence

Status: accepted for PR239 implementation; native Windows verification remains a CI requirement.

## Decision

New commands use `command-runtime.v2`, the existing Job/approval ledgers and the
common operation authorizer. Ask, Auto and Full are the writable permission
preferences. Full requires a live process activation; an old permission row,
startup flag, stored approval or receipt cannot recreate execution authority.

Remove Controlled, Once, Host proposal and Risk escalation creation, review,
intent/result writers, application executors, native executor wrappers, tool
advertisements and their exclusive tests. Remove the proposal review endpoints,
`once-command run`, `run command-proposal review`, and the two proposal startup
flags and capability fields. Fixed operator commands use Command Runtime.
`command-plan`, historical `command-proposal list/show`, and
`once-command proposals` remain non-executing reads.

Remove uncalled request constructors and their producer-only validation helpers
as well. Historical tests construct saved records directly, retaining record
validation and fingerprint checks. The retired Once spec/request/approval hash
helpers are unnecessary for reading the stored fingerprint fields.

## Retained data and continuation

Keep schema migrations, immutable proposal/approval/grant/intent/result tables,
decoders, saved receipts and evidence readers. Authenticated GET history is
available without a control token or an execution feature flag. Historical
review buttons and execution-producing POST routes are removed.

The remaining Host `POST .../resume` has no request body or query parameters. It
requires control authentication, Run execution control and exact Run/proposal
ownership. A saved ordinary Host denial or terminal result may continue its
existing Thread; an intent without a result remains unknown and cannot continue
through that path. Risk recovery consumes only the saved denial, result,
invalidation or unknown-intent fact for the exact durable Supervisor call. It
never creates a command, refreshes a grant, consumes another use or resends an
external effect. Pending/approved records without a saved outcome cannot run.

Frozen schema177 fixtures retain original foreign keys and immutable triggers,
then close/reopen through current migrations. Tests compare historical rows
before and after upgrade/read/continuation, verify receipts and bounded saved
output, reject mismatched turns/calls and live leases, and check foreign keys.
Application tests use a read-only history store to prove saved-outcome replay
cannot reach a writer or executor.

## Shared code and adjacent lifecycle fixes

Keep native pinning, pipes, process ownership and cleanup primitives used by
Command Runtime and the repository/batch runner. These are shared mechanisms,
not a second proposal execution system. Background Jobs keep their original
process owner across model turns and Run lease handoff. The process-local owner
check uses the same operation authorization and exact completed-start/Job proof,
while launch and all stdin delivery still require the current attempt and lease.
It also rechecks Run state, workspace, permission epoch/fence, policy and grants;
it cannot adopt a Job in a cold manager or refresh an old approval.
Keep historical protocol definitions
needed to validate saved records; removing their old producers does not authorize
rewriting user data.

Terminal Agent input uses current Full activation plus the existing Debug
interaction and explicit terminal lease. It rechecks the original generation,
epoch and fence at effect boundaries. Cancellation does not revoke an otherwise
valid lease or replay prepared input. Scheduled repair retains explicit repair
confirmation and normal tool checks, requires current Full, and still installs
no implicit production repair executor. Context compaction runs when history is
omitted before mandatory-context overflow is finalized; token limits are unchanged.

Optional ownership diagnostics report the failing authority/owner-renew phase
using fixed categories and timing only. They do not increase timeouts, change
kill behavior, expose process input, or claim that native Windows pagination is
fixed. Native evidence must come from the approved temporary CI runner.

## Rollback

Keep historical readers and receipts on rollback. There is no schema downgrade
or data rewrite. Restoring an old execution writer requires a separately reviewed
product decision; a historical approval or uncertain intent is never permission
to re-execute. Protocol registry retirement entries identify removed fingerprint
names while preserving supported schema readers.
