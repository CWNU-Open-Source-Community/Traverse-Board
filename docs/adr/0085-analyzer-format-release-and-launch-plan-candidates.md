# ADR 0085: Analyzer Format, Release, And Launch-Plan Candidates

- Status: Accepted
- Date: 2026-08-03

## Context

The analyzer bridge already has inert invocation, identity, preflight, outcome,
result, Artifact-candidate, and test-only staging contracts. Product execution
must remain closed until executable format, release policy, and resource/sandbox
requirements can be reviewed without introducing a path lookup, process starter,
network fetch, persistence route, or mutable authorization object.

## Decision

### Caller-byte executable format evidence

`analyzer_executable_format.v1` inspects only the complete byte slice supplied
by the Go caller. It binds the invocation candidate, executable identity,
preflight, image byte count, and SHA-256. PE validation covers DOS/PE/COFF,
optional-header and section-table bounds, executable-image characteristics,
DLL rejection, and supported machines. ELF validation covers class, byte order,
version, executable type, machine, header/program-header bounds, and at least one
bounded nonempty `PT_LOAD`. Format and machine must match target GOOS/GOARCH.

This evidence proves only bounded format and architecture agreement. It does
not prove executable semantics, provenance, immutable-handle identity, or
authority to start a process.

### Digest-only release policy candidate

`analyzer_release_manifest.v1` carries only release metadata and digests for
the executable, format evidence, provenance statement, signer identity, and
signature envelope. `analyzer_release_allowlist.v1` is an explicit,
operator-managed Go input with at most 64 deterministic, unique entries; it is
not loaded from network or environment. `analyzer_release_candidate.v1`
requires one exact allowlist match.

The match is a policy fact, not cryptographic verification. Provenance,
cryptographic signature, platform signature, immutable handle, and release
approval remain false, as do process, product, network, and host-filesystem
authority.

### Resource and sandbox design candidate

`analyzer_launch_plan.v1` deterministically binds request and release evidence
to bounded wall/CPU time, 256 MiB memory, one process, and a shared bounded
output budget. It records mandatory candidate controls for a Windows
restricted-token Job or Linux namespace/seccomp backend, dedicated
non-administrator identity, read-only input, private staging, deny-all network,
minimal environment, process-tree reaping, immutable-handle handoff, and
no-replace result publication.

Hard limits and enforcement are required but unverified. The plan carries no
path, command, argv, environment values, input body, or process starter and
fixes `start_blocked=true`. `analyzer_launch_plan_review.v1` exact-binds a
reviewer and confirmation to one plan and release candidate, but its decision
is only `accepted_as_design_candidate`; it cannot authorize execution or act as
an operator override.

## Security Invariants

- All wire decoders are strict, bounded, and reject missing false fields,
  unknown fields, duplicates, mutation, and future protocol values.
- Caller-owned executable bytes are the only image input; no filesystem path,
  environment variable, command, process, database, or network source exists.
- Digest pinning is not signature verification, and design review is not
  execution approval.
- Process start, product invocation, persistence, Artifact commit, network, and
  host-filesystem authority remain false throughout all three protocols.
- No migration, Store, Run/Event, CLI, HTTP, Desktop, Tool, Skill, or product
  adapter consumes these candidates.

## Verification

Focused tests cover valid PE/ELF targets, architecture and format mismatch,
truncation and malformed bounds, deterministic allowlist ordering, duplicate
and nonmatching entries, digest mutation, strict wire decoding, unsupported
platforms, resource calculations, exact review confirmation, and every false
authority invariant. Final analyzer ordinary tests, vet, warning-free
staticcheck, and focused race pass. The repository-wide ordinary Go suite and
Rust workspace 7+2 tests also passed during the batch gate.

## Consequences

The product gains reviewable preflight evidence without gaining an execution
path. The next independent gates are caller-byte cryptographic provenance
verification, an exact scope/limits approval receipt, and test-only OS
resource/sandbox enforcement conformance. None may add a product process
starter until its own security and robustness review passes.
