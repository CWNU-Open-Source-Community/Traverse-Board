# ADR 0086: Analyzer Signed Provenance, Scope Approval, And Test Sandbox Conformance

- Status: Accepted
- Date: 2026-08-04

## Context

ADR 0085 established caller-byte executable-format evidence, a digest-only
release candidate, and a resource/sandbox launch-plan design candidate. Those
objects intentionally did not verify cryptographic provenance, record an exact
operator acknowledgement of scope and limits, or demonstrate any operating
system enforcement. These gates must advance without creating a product
process starter or allowing test observations to become execution authority.

## Decision

### Canonical detached provenance verification

`analyzer_provenance_statement.v1` is a strict canonical JSON statement over
release metadata and digests. It binds the analyzer, release channel and
version, target GOOS/GOARCH, executable and format evidence, signer identity,
source repository, source revision, and build recipe. The caller supplies the
exact statement, Ed25519 public key, and detached signature. Verification uses
the fixed `cyberagent-workbench/analyzer-provenance/v1\x00` signing domain and
rebuilds the existing release candidate before accepting the signature.

`analyzer_provenance_verification.v1` stores only bounded metadata and digests;
it never returns the statement, key, or signature bytes. It proves canonical
statement binding and one Ed25519 signature only. Platform signature,
immutable-handle identity, release approval, and every process, product,
network, filesystem, persistence, and Artifact authority remain false.

### Exact scope and limits acknowledgement

`analyzer_scope_limits_approval.v1` binds one operator to the exact request,
executable, release candidate, provenance verification, launch plan, design
review, resource plan, and sandbox plan. The operator identity must equal the
F3 design reviewer and the confirmation must exactly equal
`APPROVE-ANALYZER-EXACT-SCOPE-AND-LIMITS`.

The receipt means only that the exact scope and limits were acknowledged. It
is not authenticated, is not a durable or capability grant, and cannot
authorize execution, process start, product invocation, network, host
filesystem access, result persistence, Artifact commit, or an override.

### Test-only operating-system conformance

Operating-system process execution exists only in platform `_test.go` files.
On Windows, the test creates and queries a Job Object with kill-on-close,
single-process, process-memory, and process-CPU limits. It starts helpers with
an explicit minimal environment, observes the second Job assignment fail with
`ERROR_NOT_ENOUGH_QUOTA`, closes the Job, and confirms the first process is
reaped.

On Linux CI, the helper sets and queries `RLIMIT_DATA` and `RLIMIT_CPU`, sets
and queries `PR_SET_NO_NEW_PRIVS`, installs an architecture-checked seccomp
filter that denies `socket` and `connect` with `EPERM`, and verifies the process
group is gone after completion. The Linux observation does not claim a process
count limit; the Windows observation does not claim no-new-privileges or
network denial.

Neither platform claims read-only filesystem enforcement, a dedicated
identity, immutable-handle handoff, complete sandbox enforcement, product
readiness, or product authority.

## Security Invariants

- All new wire decoders are strict, bounded, canonical, and reject unknown,
  duplicate, future, missing, or mutated fields.
- Raw provenance statements, public keys, detached signatures, paths,
  commands, argv, environment values, and analyzer output do not enter the
  verification or approval result envelopes.
- Operator acknowledgement cannot become authentication, a grant, an
  override, or execution authorization.
- Production analyzer files contain no process starter. No CLI, HTTP, Desktop,
  Tool, Skill, Store, Run/Event, persistence, or Artifact route consumes these
  contracts.
- Platform conformance observations record both what was observed and what
  remains unverified; incomplete evidence always fails closed.

## Verification

Focused tests cover deterministic and round-trip encoding, canonical-byte and
domain separation, key/signature/statement drift, strict schema widening,
exact operator and confirmation binding, plan and provenance drift, and every
false-authority invariant. Windows Job Object conformance passes locally; the
Linux test is cross-compiled locally and is wired into the Ubuntu CI analyzer
gate for native execution.

The six-slice gate passed the uncached repository-wide Go suite in about 513.5
seconds, the full race suite in 547 seconds, repository-wide vet/staticcheck,
zero-reachable-finding govulncheck, Go module verification/tidy, five additional
focused Analyzer race repetitions, 178 React tests across 48 files, strict
TypeScript/Vite, zero-vulnerability npm audit, Rust fmt/test/clippy/RustSec,
secure Desktop checks, and a reproducible Windows double build. Newly reported
`brace-expansion` and `postcss` advisories were resolved with exact 5.0.9 and
8.5.25 overrides before rerunning the Web gate. The final GUI SHA-256 is
`d9bf7dc005d513046777cf7ad6a8fcf49a64190de1bef76ca822cbaf53ca9e48`;
release readiness remains false.

## Consequences

The release chain now has cryptographic caller-byte provenance, an exact
human scope/limits acknowledgement, and OS-specific test evidence without
gaining an execution path. The next independent gates should prove
caller-owned immutable-handle handoff, dedicated low-privilege identity, and
read-only filesystem/staging enforcement. A product adapter may be considered
only after those controls receive their own threat model and acceptance gate.
