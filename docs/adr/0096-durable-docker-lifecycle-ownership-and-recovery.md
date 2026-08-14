# ADR 0096: Durable Docker Lifecycle Ownership And Recovery

Date: 2026-08-14

## Status

Accepted as an internal Sandbox control-plane boundary. It is not a product
execution gate.

## Context

ADR 0095 proved the fixed-endpoint Docker mechanics for start, wait,
termination, and exact cleanup, but its one-shot transport could lose process
state when the worker crashed. A request fingerprint was present in memory,
yet no committed record proved which Run intended to create the container, who
currently owned the lifecycle, or whether an ambiguous daemon write had
completed. The schema-v56 rehearsal ledger cannot fill this role because it
permanently asserts that the retained container and its process were never
started.

Persisting a mutable status alone is insufficient. A worker may stop after a
Docker request reaches the daemon and before the following SQLite commit. A
replacement worker must distinguish that uncertainty from an operation that
was never attempted, and an expired worker must be fenced from later daemon
writes as well as database commits.

## Decision

1. Schema v97 adds a separate Docker lifecycle aggregate. It commits an
   immutable launch intent and generation-one lease before the first create
   request, records each external action intent before that action, appends
   lifecycle observations, and permits one immutable cleanup receipt. It does
   not reinterpret or widen schema v56 rehearsal records.
2. The aggregate binds the Run, Workspace, v54 plan, lifecycle Attempt,
   immutable container resource generation, exact request/spec/authority
   fingerprints, fixed endpoint class and fingerprint, deterministic name,
   and a versioned ownership-label fingerprint. It supports only the fixed
   local Unix socket and fixed Docker Desktop Linux-engine NPipe endpoints.
3. The current lease contains an opaque owner, lease id, monotonically
   increasing lease generation, and bounded expiry. Every lifecycle database
   mutation and every Docker POST or DELETE performs a final full-identity
   fence check. Resource generation does not change during lease takeover;
   this lets a newer Supervisor reconcile the exact container created by an
   older generation without accepting that older owner.
4. Lifecycle containers add exact labels for the lifecycle Attempt, immutable
   resource generation, and launch-intent fingerprint to the existing exact
   Prayu label set. Recovery starts from bounded durable intents, inspects only
   the deterministic name, and requires the complete label set plus the exact
   image, command, mount, user, security, resource, network, and name
   configuration before mutation. Missing, partial, extra, legacy, foreign, or
   inconsistent identity is fail-closed and untouched.
5. The Docker transport exposes checkpointable observe, create, start, wait,
   terminate, and cleanup primitives while retaining its fixed HTTP allowlist.
   Create, start, TERM, KILL, and delete each fence immediately before their
   daemon request. Ambiguous responses are resolved by exact inspection rather
   than by blindly repeating a non-idempotent operation.
6. Durable transitions use `created`, `started`, `exited`, `cleaning`,
   `cleaned`, and `failed`. Exact replays converge without duplicate audit
   facts. A failure does not imply that a possibly-running resource is gone;
   cleanup remains recoverable until the unique receipt exists.
7. Startup reconciliation is database-led and requires the caller to
   revalidate current Run, Workspace, permission-snapshot, process-capability,
   and container identity authority. Persistence is evidence of prior intent,
   not fresh authorization. No daemon-wide adoption or orphan enumeration is
   introduced.

## Security And Capability Boundary

The coordinator and fixed-endpoint constructors remain Go-internal and are not
wired to Agent, model, CLI, HTTP, Desktop, or generic Runner entry points.
Network remains disabled; output/log capture, secret materialization, Artifact
commit, and production admission remain outside this slice. Public projections
and events omit raw container ids, names, host paths, operation keys, lease ids,
owners, and request bodies.

SQLite fencing cannot retract a Docker request already accepted by the daemon.
The design therefore also bounds daemon calls by the lease lifetime and makes
recovery inspect exact state before the next effect. Product execution remains
disabled until the later isolation, IO, and admission gates are independently
implemented and reviewed.

## Consequences

Prayu can resume its own exact lifecycle after pre-create, post-create, or
post-start worker failure and can reject stale database and Docker writes.
Unknown containers remain outside its authority. The additional action ledger,
lease renewal, and strict identity checks add implementation complexity, but
make crash ambiguity explicit and testable rather than silently leaking or
restarting a process.
