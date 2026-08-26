# ADR 0143: Packaged Standard Code security matrix

- Status: Accepted
- Date: 2026-08-26

## Context

The frozen `standard_code_attack_matrix.v1` contains 40 required attack and
recovery cases and 75 Local/Docker executions. The existing packaged bootstrap
proved ZIP identity, deterministic fixtures, startup, restart, and owned
cleanup, but deliberately reported zero evidenced attack cases. Unit tests and
direct sandbox calls cannot close that gap: release evidence must enter through
the exact `TraverseBoard.exe` candidate and the public Go-owned Standard Code
tool path.

The matrix also crosses process failure. Renderer detach, normal Desktop exit,
forced process termination, owner-lease expiry, restart, and concurrent
Drydock edits must be observable after reopening the same durable state. A
harness assertion that merely sees a Checkpoint, or a synthetic pass for an
unavailable backend, is insufficient.

## Decision

Add an independent packaged-only executor and the immutable
`standard_code_packaged_security_evidence.v1` report.

The top-level entry accepts exactly a new harness-owned
`standard-code-attack-*` root and a portable candidate ZIP. It first proves
that the running executable is byte-identical to the ZIP entry and that strict
release metadata binds a clean, reproducible source commit. It then
materializes the embedded fixture/matrix definition and invokes all 75 pairs
through Tool Gateway, Application Command Runtime, and the currently
advertised Local or Docker Standard Code adapter.

The executor owns backend selection. Model or fixture content cannot select an
adapter, command, environment, mount, endpoint, network mode, credential mode,
permission, or host path. Every command uses the fixed Go probe with disabled
network, no credentials, bounded output, a Run execution lease, the current
mode/permission revisions, and the exact adapter generation. The gateway now
passes the complete Mission/mode/permission authority snapshot into Command
Runtime, and Application rejects any stale tuple before process creation.

Recovery cases launch the same packaged executable in a second fixed internal
mode. That mode accepts only one owner-marked harness root, one allowlisted
case, `local|docker`, and `prepare|recover`; it accepts no command or authority
input. The prepare process creates a real Standard Code Run/Drydock, starts one
background Command Runtime Job, and persists a bounded state marker. The
parent then injects the selected detach, shutdown, forced termination, lease
expiry, restart-equivalent, or concurrent-edit fault. Recovery reopens the
same SQLite profile twice and verifies terminal Job state, complete tree
reaping, backend/network/credential binding, immutable events, Checkpoint,
Drydock observation, and preservation of tracked CRLF plus untracked binary
edits.

The evidence producer derives case order, summary, verdict, per-record hash
chain, final chain hash, and report self-hash. A caller cannot supply those
values. Validation reloads the embedded matrix and rejects missing, reordered,
unexecuted, skipped, unavailable, synthetic, mismatched, incomplete, or
secret/path-bearing passes. The output is create-exclusive and contains only
stable identity plus hashes, never raw output, credentials, environment, or
private absolute paths.

## Failure and cleanup semantics

- Backend unavailability is recorded as failed and unexecuted with an explicit
  `approval_required` fallback fact. It never selects Full Access and cannot
  produce a passing #181 report.
- Permission, profile/mode, Run/root, lease, backend, capability, or operation
  identity drift fails before process creation. A stale invocation cannot
  create a second durable Job.
- Forced recovery waits for the exact durable command-owner lease to expire;
  a new process never adopts authority from a PID or container identifier.
- Startup reconciliation may mark only the matching durable Job interrupted
  and may clean only resources whose existing Standard Code ownership contract
  proves exact identity.
- Cleanup rewinds and removes only harness-created Drydocks and directories.
  Every started Job must be terminal and tree-reaped; orphan or foreign-process
  cleanup makes the report invalid.
- Windows harness subprocesses use `CREATE_NO_WINDOW` and packaged-mode
  failures are written to the captured stderr stream rather than native
  Desktop startup dialogs, so conformance recovery cannot interrupt the
  operator's visible desktop.
- Re-running requires a new root. Existing evidence is never overwritten or
  silently retried into success.

## Governance and non-goals

This is a conformance-test entry, not a product Surface or runtime protocol.
It adds no renderer, model, network, credential, Debug, Full Access, arbitrary
Shell, or release authority. The report is an input that the #140 release owner
may aggregate; it cannot declare #140 or Beta passed.

The change does not modify the central release workflow, the existing packaged
bootstrap script, aggregate report format, final gate state, four-language
repair loop, delivery contract, code signing, or Store distribution. Those
remain separately owned.

## Consequences

- A real portable candidate run is more expensive than a unit suite: it
  executes both sandbox backends, persists evidence, and exercises process
  restart and Docker reconciliation.
- The fixed probe and recovery worker remain embedded in the executable, but
  their mutually exclusive arguments and owner-root checks prevent them from
  becoming general execution paths.
- Release review can independently recompute candidate, fixture, matrix,
  backend, record-chain, cleanup, and report hashes without receiving local
  paths or secret material.
