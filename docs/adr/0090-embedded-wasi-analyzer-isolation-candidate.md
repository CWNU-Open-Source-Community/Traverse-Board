# ADR 0090: Embedded WASI Analyzer Isolation Candidate

- Status: Accepted
- Date: 2026-08-05

## Context

P10-F through P10-J established native-executable identity, provenance, scope,
test-only operating-system isolation observations, product admission, signed
requests, durable write-ahead intent, and restart reconciliation. Those layers
still do not provide independently portable production isolation. Making a
native PE/ELF subprocess the default would require separate Windows and Linux
production sandbox implementations and would retain host process, path,
filesystem, environment, network, and orphan-process attack surfaces.

The Rust analyzer already accepts a bounded JSON envelope on standard input and
returns bounded JSON on standard output. It has no reason to own a native host
process. Prayu therefore needs a smaller primary isolation candidate while
preserving `TypeScript -> Go -> Rust` and keeping Go as the only control plane.

## Decision

### P10-K1: fixed isolation profile

The primary Analyzer candidate is Rust `wasm32-wasip1` embedded by Go through
`github.com/tetratelabs/wazero v1.12.0`. The profile fixes:

- the wazero interpreter rather than the compiler engine;
- WebAssembly Core v2 and a 4,096-page (256 MiB) per-memory ceiling;
- a 16 MiB module-size ceiling and `_start` as the only callable entry point;
- a fresh runtime, compiled module, and future guest instance per invocation;
- context cancellation checks, no shared compilation cache, no custom sections,
  and no debug metadata;
- no inherited argv or environment, no filesystem mount, no host clock, no host
  entropy, no socket/custom host module, no native process, and no host path;
- future synthetic argv, empty environment, deterministic random input, and
  bounded in-memory stdio only.

The profile is a strict, deterministic, default-deny record. It contains no
module bytes, path, command, runtime instance, process handle, or capability.

### P10-K2: compile-only module assessment

Go first limits the caller-owned module bytes, then asks a fresh wazero
interpreter runtime to `CompileModule`. It never registers a WASI host module,
instantiates a guest, or calls an export. A second bounded parser inspects the
validated WebAssembly import section so table, memory, global, tag, or unknown
host imports cannot hide behind a function-only API projection.

The exact accepted Rust fixture surface is nine `wasi_snapshot_preview1`
functions with fixed signatures: args/environ reads, stdin/stdout fd metadata
and reads/writes, deterministic random fill, and process exit. Filesystem paths,
preopens, clocks, polling, sockets, and custom modules are absent. Function
exports are restricted to `_start` and the Rust toolchain's `__main_void`; a
future runner may call only `_start`. Imported memory is denied, exported memory
must fit the runtime ceiling, and all inventories are sorted and bounded.

The assessment retains only module/profile digests, module size, bounded import
and export metadata, memory pages, policy outcomes, and an integrity
fingerprint. It explicitly records `compiled_only=true`, `instantiated=false`,
`guest_executed=false`, and all-false product authority.

### P10-K3: ownership and non-starting release decision

Go invocation scope owns the runtime, compiled module, and future guest
instance. No PID or process tree exists, no object is shared across Runs, and
close order is guest, compiled module, then runtime. The Go Run Supervisor owns
the deadline; the recovery reconciler owns metadata closure only. A host crash
cannot leave a guest process, a consumed v93 request is never replayed
automatically, and retry requires a new signed request.

Compile-time acceptance does not enable execution. Seven independent gates
remain open: runtime execution conformance, bounded stdio, deadline-close
observation, result-validation handoff, capability issue/consume, production
evidence acceptance, and product-route review. The release decision therefore
fixes `ready=false`, `start_blocked=true`, and all authority false.

The existing native subprocess contracts remain historical/test evidence and a
separately reviewable fallback. They are not silently rewritten to mean WASI,
and no CLI, HTTP, Desktop, Tool, Skill, model, Store, Run, Event, or Artifact
route is added in this batch. SQLite remains schema v93.

## Security Consequences

- README, model, Skill, or tool output cannot select a module, widen imports,
  instantiate a guest, or grant a runtime capability.
- Interpreter selection avoids guest JIT/native code generation and removes the
  default native child-process lifecycle from the selected path.
- WASI is not treated as automatically safe. Host imports, stdio, deadlines,
  result validation, capability consumption, and production evidence each keep
  separate fail-closed gates.
- `random_get` being present does not grant host entropy; the selected profile
  permits only a deterministic source in a future execution slice.
- Successful Fake lifecycle or successful `CompileModule` remains weaker than
  production execution evidence.

## Verification

Tests cover strict profile round trips, missing/unknown/authority-widening
fields, malformed modules, unknown function imports, non-function imports,
missing `_start`, extra exports, compile-only state, immutable ownership, failed
assessment rejection, and the seven open release gates. CI builds the actual
pinned Rust fixture as `wasm32-wasip1 --release` and checks its exact imports,
signatures, exports, and memory policy through Go.

The three-slice functional gate passes the complete Analyzer package, focused
race, vet, warning-free staticcheck, Go module verification/tidy, Rust 7+2
tests, fmt, clippy, and the real release WASI build/assessment. This is the first
half of a six-slice cycle, so it does not claim another full-repository race,
vulnerability, dependency, or release-build gate.

## Next Decision

P10-L1/L2/L3 may add test-only interpreter execution conformance for bounded
stdin/stdout, deadline/cancellation/close, and deterministic result validation.
It must still expose no product route and must finish the cumulative six-slice
robustness gate. Capability issuance, production evidence acceptance, and a
user-visible Analyzer route remain later independent reviews.
