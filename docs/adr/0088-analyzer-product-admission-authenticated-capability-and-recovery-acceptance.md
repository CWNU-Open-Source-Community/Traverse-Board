# ADR 0088: Analyzer Product Admission, Authenticated Capability, And Recovery Acceptance

- Status: Accepted
- Date: 2026-08-04

## Context

ADRs 0085 through 0087 established format, provenance, release, scope,
resource, sandbox, immutable-handoff, low-privilege, and filesystem evidence.
Some observations are design candidates and some are test-conformance evidence;
none are production verification. A product adapter must not turn those records
into start authority merely because their digests are present.

The next boundary therefore needs three product-inert contracts: an exact
admission classification over the original evidence, an authenticated
operator-owned request that cannot be widened into arbitrary process input,
and an explicit restart/failure acceptance list. These contracts must expose
missing production controls without adding a process starter, product route,
capability issuance, persistence, or execution authorization.

## Decision

### Exact product-adapter admission matrix

P10-I1 rebuilds and validates the complete F/G/H evidence chain instead of
trusting supplied digests. Its 20 controls classify 18 candidate observations,
including 13 test-conformance-only observations, zero production-verified
controls, and 20 open start blockers. Candidate and test-conformance counts
overlap by design; neither category is production evidence.

The missing durable-intent and append-only-audit controls remain explicit.
The matrix keeps admission, product-adapter readiness, process-start readiness,
and every authority flag false. Transient evidence inputs carry `json:"-"` on
all exported fields so accidental serialization cannot create an alternate
evidence envelope.

### Authenticated operator-owned one-shot request

P10-I2 defines a domain-separated Ed25519 request and verification contract.
The signature binds the exact admission matrix, scope, launch plan, release,
analyzer, platform, executable, operator approval identity, nonzero 32-byte
nonce, and bounded validity interval. The request contains no path, command,
argv, environment, or input body.

This is an authenticated request contract, not a bearer capability and not a
start token. Verification proves the signature and exact evidence binding, but
deliberately records clock validity, durable replay protection, and atomic
consumption as absent. Raw private/public key material, signature bytes, and
nonce bytes are not retained in the resulting contract; only bounded digests
and metadata survive. Capability issuance, consumption, process start, and all
authority remain false.

### Restart and failure cleanup acceptance

P10-I3 binds ten ordered acceptance scenarios to the exact admission matrix,
signed request, capability contract, and scope:

1. intent committed before start;
2. start submitted with process identity unknown;
3. running deadline;
4. operator cancellation;
5. crash before result publication;
6. crash after result publication;
7. orphan process tree;
8. foreign staging collision;
9. replay after a terminal receipt; and
10. stale-generation worker.

Every scenario requires idempotent replay handling and remains an open product
blocker. The applicable scenarios additionally require write-ahead intent,
generation fencing, exact process identity/tree quiescence, no-replace
publication, and protection of foreign resources. No lifecycle store,
generation ledger, cleanup executor, recovery reconciler, or apply operation is
introduced by this decision.

## Security Invariants

- Candidate and test-conformance observations never imply production evidence.
- Exact source evidence is rebuilt and verified; caller-supplied digests are
  insufficient.
- The signed request carries no executable path, shell command, argv,
  environment, or analyzer input body.
- A valid signature cannot issue or consume a capability and cannot authorize a
  process.
- Clock acceptance, durable nonce replay protection, and atomic consumption
  must be supplied by a later independent durable control plane.
- Recovery acceptance is descriptive and start-blocking; it does not perform
  cleanup or mutate lifecycle state.
- Strict JSON decoding rejects missing, unknown, duplicate, malformed, or
  future fields at every contract layer.
- CLI, HTTP, Desktop, Tool, Skill, Store, Run/Event, Artifact, Sandbox, and
  product process surfaces remain unchanged.

## Verification

Focused tests cover exact classifications and counts, all-false authority,
strict nested JSON round trips, missing/unknown/duplicate/future fields,
evidence and executable tampering, nonce and interval bounds, Ed25519 forgery
and foreign-key rejection, widening attempts, transient-evidence serialization,
raw cryptographic-material retention, scenario uniqueness, foreign-resource
protection, and idempotent recovery requirements. Analyzer ordinary and race
tests, vet, warning-free staticcheck, Linux no-CGO cross-compilation, and tagged
Desktop boundaries pass.

This batch also closes the cumulative six-slice robustness gate: the uncached
repository Go suite passed in 554.5 seconds and the full race suite in 617.8
seconds; repository vet, staticcheck, govulncheck, and module verification pass.
All 48 Web test files and 178 tests, strict API checks, production build, and
zero-finding npm audit pass. Rust fmt, 7 unit plus 2 shared-vector tests,
warning-free clippy, and the locally cached 1,186-advisory RustSec scan pass.
No reachable Go vulnerability or enabled high/medium-risk regression was found.

## Consequences

P10-I1/I2/I3 make the production gap machine-readable without narrowing it by
assertion. Product execution remains disabled because no production filesystem
and network sandbox, durable replay ledger, atomic one-shot consumption,
write-ahead lifecycle state, generation fencing, recovery executor, or
append-only lifecycle audit has been accepted.

The recommended next analyzer batch is P10-J: add a durable nonce/request
ledger, generation-fenced write-ahead start-intent state machine, and
append-only lifecycle/recovery receipts using only Disabled/Fake execution.
Connecting a real process starter remains a separate later decision.
