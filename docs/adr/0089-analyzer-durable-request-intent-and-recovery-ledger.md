# ADR 0089: Analyzer Durable Request, Intent, And Recovery Ledger

- Status: Accepted
- Date: 2026-08-05

## Context

ADR 0088 authenticates an operator-owned one-shot Analyzer request and defines
restart/failure acceptance, but intentionally leaves clock acceptance, durable
nonce replay protection, atomic consumption, lifecycle persistence, and
recovery mutation absent. A valid signature alone must never become process
authority.

The next boundary must make one-shot and recovery semantics durable before any
product process bridge exists. It must remain useful under crash and replay,
while proving that persistence does not silently introduce a command, process,
network, filesystem, or Artifact path.

## Decision

### Signed-request projection

Schema v93 stores an append-only `analyzer_durable_start_request.v1`
projection only after the complete P10-I request, matrix, nonce, public key,
and detached signature have been rebuilt and verified. The record binds the
exact Run, Workspace, signed request, analyzer/platform/executable, operator,
admission, scope, request/contract digests, validity interval, and adapter.

The nonce digest is globally unique. Exact ID/fingerprint replay is
idempotent; reusing a nonce for another record fails. The Run must be
nonterminal and its Mission must own the exact Workspace. Raw nonce, key,
signature, path, command, argv, environment, input body, and process-start
material are not persisted. The record remains a request, not a bearer
capability.

### Generation-fenced write-ahead intent

Each request has an append-only `analyzer_start_intent.v1` chain. Generation
one is either terminal `disabled` or Fake `prepared`. Fake may atomically move
from `prepared` to `consumed` before signed expiry, and from `consumed` to
`fake_succeeded`, `fake_failed`, `recovery_required`, or `cancelled`.
Unconsumed work may expire or be cancelled.

Every successor binds the previous intent fingerprint and latest generation.
Go validates the state machine and SQLite independently enforces ancestry,
time, event, and Run/Workspace/request bindings. A stale competing worker
cannot append another successor. Exact retries observe the already committed
generation.

### Append-only receipts and reconciliation

Every intent generation atomically appends a redacted
`analyzer_start_lifecycle_receipt.v1` and bounded Run events. Receipt ancestry
matches intent ancestry. All request, intent, and receipt rows reject update
and delete.

Restart reconciliation performs metadata closure only. A latest `consumed`
intent becomes `recovery_required`; an expired latest `prepared` intent becomes
`expired`. Reconciliation cannot start, inspect, terminate, or clean a process
and cannot publish an Artifact.

## Security Invariants

- The only adapters are `disabled` and `fake`.
- Fake success is lifecycle evidence, not production sandbox or execution
  evidence.
- Capability issuance and real process, network, filesystem, secret, override,
  persistence, recovery-apply, and Artifact authority remain false.
- No executable, argv, environment, stdin, output, path, process handle, raw
  cryptographic material, or bearer value enters the ledger.
- Strict JSON rejects malformed, duplicate, missing, unknown, future, or
  authority-widening fields. SQLite repeats key shape and zero-authority checks
  for direct writes.
- Nonce uniqueness, exact Run/Workspace ownership, nonterminal Run state,
  generation ancestry, signed expiry, and redacted audit are database-enforced.
- CLI, HTTP, Desktop, Tool, Skill, model, and product Runner surfaces remain
  unchanged.

## Verification

Tests cover the full authenticated request to durable projection, exact replay,
nonce rebinding, wrong Run/Workspace binding, Disabled closure, Fake
prepare/consume/success, expiry, cancellation, stale generations, competing
consume/cancel, restart reconciliation, receipt ancestry, event redaction,
unknown/direct-SQL authority widening, immutable rows, and v92-to-v93 upgrade.

The final review added explicit negative coverage for an invented stored
successor state and a receipt predecessor crossing Run ancestry. The focused
ordinary and race suites for `internal/analyzer` and the P10-J Store paths,
plus `go vet`, `staticcheck`, and `git diff --check`, pass. This three-slice
gate does not claim another full-repository six-slice robustness run.

## Consequences

Prayu now has durable one-shot and restart semantics without claiming process
readiness. The next Analyzer decision must select independently verifiable
production OS sandbox evidence and exact process/recovery ownership. A real
starter cannot be inferred from this ledger and remains separately gated.
