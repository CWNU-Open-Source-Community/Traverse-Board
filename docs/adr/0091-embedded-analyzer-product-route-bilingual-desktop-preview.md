# ADR 0091: Embedded Analyzer Product Route And Bilingual Desktop Preview

- Status: Accepted
- Date: 2026-08-05

## Context

ADR 0090 selected an embedded Rust/WASI Analyzer but intentionally stopped
before guest execution, authorization, durable results, or a product route.
The expanded P10-L through P10-M batch must turn that candidate into one narrow,
auditable product feature without turning Rust into an Agent, accepting
caller-supplied WebAssembly, or granting filesystem, network, subprocess, or
host-process authority. The same batch must make the Windows Desktop directly
testable in Chinese and prove that a real Provider response survives the full
Run/Session persistence path.

## Decision

### P10-L1: bounded embedded execution

Go embeds the pinned Rust `wasm32-wasip1` release fixture and creates a fresh
`wazero` Interpreter runtime, WASI host, compiled module, and guest for every
invocation. The caller supplies only a bounded Analyzer request. Go supplies a
fixed synthetic argv, empty environment, deterministic random source, bounded
memory stdin/stdout, and a context deadline. It mounts no directory, exposes no
socket or custom host module, starts no native process, and closes the runtime
on completion, cancellation, or deadline. Exit code, protocol, size, SHA-256,
and deterministic result semantics must all validate before a result exists.

### P10-L2 and L3: one-shot authority and atomic evidence

Schema v94 records a secret-free, maximum-five-minute capability exact-bound
to Run, Workspace, request, candidate, and the embedded module digest. The
random bearer exists only in memory; SQLite stores its SHA-256 and consumes it
atomically once. Expired, replayed, mutated, deleted, or mismatched capability
use fails closed.

Schema v95 atomically commits the consumption, redacted execution record,
metadata-only Artifact descriptor, and Run events. The Artifact content is only
the validated metadata-summary JSON; it stores no bearer, raw request, raw
input, module bytes, input file contents, guest stderr, path, command, argv,
environment, or native-process identity. The public receipt excludes the
Artifact body. A successful idempotent replay returns the existing record and
never runs the guest twice.

### P10-M1 and M2: controlled product route and real chat

The CLI and control-token HTTP/Desktop API may invoke only the fixed embedded
Analyzer service with exact confirmation. React can submit bounded text or a
workspace-relative regular file; Go resolves and reads the file within the
Run Workspace before passing bytes to the filesystem-free guest. The public
view exposes only metadata and all-false host authority.

Desktop language is `zh-CN|en-US`, defaults to Chinese, and persists only as a
local UI preference. Technical identifiers and proper names such as Prayu,
Run, Session, Provider, Harness, Skill, API, JSON, SHA-256, Code, and Cyber may
remain English; ordinary navigation, controls, states, and help text are
bilingual.

A deterministic integration server speaks the real Anthropic-compatible SSE
protocol. The test stores a Provider credential through the OS-owned interface,
reloads the Registry, performs the two-call Harness qualification, selects the
route, creates and starts a Run, persists a user Session message, executes one
bounded Supervisor step, and reads the persisted assistant response from
SQLite. It exercises the production Provider parser and control plane rather
than the Mock Provider, while making no paid external request and exposing no
secret.

### P10-M3: safe local product entry

The portable package includes a hash-bound operator-preview launcher and
bilingual local-test guide. The launcher passes only `--operator-preview`; it
must not contain danger-full-access, maximum Debug, Full CDP, user-terminal, or
background wake-worker flags. Direct EXE launch remains conservative by
design. A smoke script uses an isolated `CYBERAGENT_HOME`, verifies that the
Desktop remains alive long enough to create and migrate its local store, and
closes only the exact process it started.

The cumulative full-repository robustness gate originally scheduled after L3
is intentionally deferred until all M3 package and documentation edits are
complete. This avoids claiming a release gate over code that had not yet been
included in the batch.

## Security Consequences

- TypeScript never chooses module bytes, host imports, capability fields, file
  authority, network policy, command, argv, or process behavior.
- Repository text, model output, Skill content, and tool results cannot issue or
  consume the Analyzer bearer and cannot self-enable the Desktop capability.
- The safe preview launcher improves testability without granting the four
  high-risk process-local gates.
- API credentials remain in Windows Credential Manager or process environment;
  SQLite, events, public diagnostics, screenshots, and package files contain no
  plaintext secret.
- A WASI guest bug remains bounded by strict imports, resource limits,
  per-invocation ownership, one-shot authorization, and metadata-only commit.

## Verification

Focused tests cover bounded success, deterministic replay, malformed results,
timeout, cancellation, close, oversized input/output, capability expiry and
binding, atomic concurrent consumption, v93-to-v94-to-v95 migration, immutable
tables, transactional Artifact/event commit, HTTP authority, Desktop capability
projection, bilingual UI behavior, and real Anthropic-compatible chat
persistence.

The post-M3 local release gate completed with these measured results:

- `go test ./... -count=1 -timeout=20m`: 610.6 seconds; the Store package used
  593.0 seconds. A first run at Go's default ten-minute package boundary timed
  out while the same exact migration test passed ten consecutive focused runs;
  the explicit local release timeout avoids misclassifying slow Windows/CGO
  contention as a deadlock.
- `go test -race ./... -count=1 -timeout=20m`: 503.5 seconds with zero races;
  `go vet`, warning-free `staticcheck`, `go mod verify`, clean `go mod tidy
  -diff`, and `govulncheck` with zero reachable or imported-package
  vulnerabilities all passed.
- Web tests passed 182 assertions across 50 files; strict TypeScript and Vite
  production build passed; `npm audit --audit-level=high` reported zero
  vulnerabilities; generated OpenAPI TypeScript was byte-for-byte stable.
- Rust passed format, 7 library plus 2 CLI tests, warning-denying clippy, the
  locked WASI release build, and RustSec audit over 1,189 advisories/42
  dependencies. The rebuilt module SHA-256 exactly matched the embedded fixture:
  `2f74c1eab73b27e905eec63ff91c81b659514bfbc2a87ec4cc1748a89c5a126c`.
- The production Anthropic-compatible SSE/Provider/Registry/Harness/Router/
  Run/Session/Supervisor persistence path passed three consecutive focused
  runs. It used a deterministic local SSE endpoint, not `MockProvider`, and did
  not spend or expose an external API key.
- A reproducible pre-commit Windows candidate was built twice with SHA-256
  `1cfcaa58361da969b45e276272f8c97b7bf0c2dff6efb1a37cc2bd4fddf711af`.
  The isolated-home operator-preview smoke created its store, remained alive,
  and closed only its exact process. Automated compatibility checks passed;
  `release_ready=false` remains solely because the manual Windows 10,
  WebView2, and display-scaling matrix plus code signing/installer are pending.

The combined audit fixed a concurrent Store migration-ledger check that had
occurred before the write lock and changed workspace-file Analyzer reads to a
handle-confined `os.Root` flow with post-read identity checks. No unresolved
high- or medium-severity issue is known on the enabled operator-preview path.
Hosted GitHub CI is the remaining independent confirmation after push.
