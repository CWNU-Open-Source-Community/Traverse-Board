# ADR 0073: Browser Publisher, Launch Lease, And Review Gates

- Status: Accepted
- Date: 2026-07-25
- Scope: schema v85 non-starting browser acceptance, lifecycle ownership, and independent review

## Context

P11-A and P11-B established browser profiles, exact target scope, fixed-location executable
discovery, disposable-profile lifecycle plans, and sealed Disabled/Fake CDP contracts. Those
contracts intentionally could not decide whether an installed executable came from an accepted
publisher, could not own a future launch across restart, and could not prove that an independent
operator reviewed the exact launch candidate.

Connecting a process starter directly to path-based discovery would leave TOCTOU, replay,
cross-Run, stale-profile, orphan-process, and self-review gaps. Prayu therefore needs a durable
non-starting gate before any real adapter is considered.

## Decision

1. `browser_executable_acceptance.v1` opens the exact candidate read-only and hashes/parses it
   through that handle. Windows Authenticode verification is cache-only, suppresses UI, and is
   bound to the same handle. File identity, size, bytes, SHA-256, and PE architecture are
   revalidated before returning.
2. `browser_publisher_policy.v1` accepts only exact known publishers for Chrome and Edge.
   Chromium remains refused because Prayu has no fixed publisher policy for arbitrary Chromium
   distributions. Signature success is only `accepted_for_review`; it is not launch trust.
3. Authenticode revocation and timestamp freshness are not claimed because this gate performs no
   network access. `launch_trust_complete`, process, network, profile-write, termination, cleanup,
   and Artifact authority all remain false.
4. Schema v85 appends immutable `browser_launch_attempt.v1`,
   `browser_launch_lease.v1`, preparation-operation, `browser_launch_review.v1`, and
   review-operation records. Preparation persists the attempt and generation lease before any
   future adapter call and emits bounded append-only events.
5. Each attempt binds the exact Session, Run, Workspace, accepted executable, disposable-profile
   owner and generation, target scope, budgets, backend, and process-tree contract. Lease
   generation fences stale workers. Cancellation, restart observation, process-tree ownership,
   termination, and cleanup are currently executable only through package-sealed Disabled/Fake
   lifecycle adapters.
6. Reconciliation may produce a bounded candidate state but cannot start, terminate, or clean a
   process. A future production adapter must independently revalidate every bound identity and
   prove Windows Job Object or equivalent whole-tree containment.
7. Review must occur while the exact lease is active. The reviewer digest must differ from the
   lease-owner digest and the review recomputes the attempt fingerprint. An accepted review means
   only “eligible for a future adapter”; it does not grant start or runtime authority.
8. Raw operation keys, lease-owner identities, reviewer identities, paths outside the existing
   bounded executable record, profile markers, target contents, and credentials are not persisted
   or emitted. Domain-separated digests provide idempotency and independence checks.

## Security And Authority

The v85 tables and events are an audit and recovery boundary, not a capability grant. Direct
database facts cannot widen authority because every persisted protocol fixes all execution
authority fields to false, and no product start adapter consumes these records. TypeScript,
models, Skills, documents, HTTP, and tools cannot create a browser process through this package.

The Windows publisher gate deliberately fails closed for unsupported products, publisher drift,
path replacement, signature failure, PE drift, non-regular files, links, oversized input, and
identity changes. Cache-only Authenticode avoids an unexpected network side effect but cannot
claim current revocation or timestamp validation; a future release policy must decide how those
checks are obtained and audited.

## Verification

The six-slice gate passes:

- serial full ordinary Go tests in about 545 seconds and serial full race tests in about
  660 seconds;
- full `go vet`, zero-warning `staticcheck`, module verification/tidy, ordinary and Desktop
  `govulncheck` with zero reachable or imported vulnerabilities;
- secure Desktop-tag race/test/vet/staticcheck and a reproducible unsigned Windows build with
  SHA-256 `a7e482adfff18068c4d3fb588d8c4e25a79ac0aefff053df7a7ab57902b5b85b`;
- 42 React test files / 148 tests, strict TypeScript, deterministic API checks, Vite build, and
  zero-vulnerability npm audit;
- Rust fmt, 7 unit plus 2 shared-vector tests, Clippy with warnings denied, `cargo audit`, and
  Go/Rust real-fixture conformance.

An explicit read-only local smoke inspected the fixed Chrome and Edge installations. Chrome was
accepted for review with publisher `Google LLC`; Edge was accepted for review with publisher
`Microsoft Corporation`. It inspected two candidates and started no browser process. No test used
browser network, profile writes, CDP, credentials, Shell, Docker, Provider calls, or product
process authority.

## Consequences

- P11-C1/C2/C3 complete the prerequisite publisher, same-handle, durable ownership, and
  independent-review gates.
- The built-in browser remains unavailable to users. There is still no real start adapter,
  process tree, disposable-profile materialization, CDP network connection, navigation, DOM,
  screenshot, request capture/mutation, or browser UI.
- P11-C4 may add only the Safe Web production start adapter after exact revalidation and
  whole-process-tree containment. P11-C5 should separately materialize and recover disposable
  profiles. P11-C6 should separately add exact-scope CDP navigation/DOM/screenshot without request
  mutation. CTF instrumentation remains later work.

## 中文摘要

schema v85 完成的是“不启动的浏览器发布者接纳、代际租约和独立人工复核门”，不是内置浏览器
已经可用。Windows 只对同一只读文件句柄做缓存限定 Authenticode 和精确发布者复核；Chrome/Edge
可进入待复核状态，任意 Chromium 继续拒绝。attempt、lease、幂等操作和 review 以不可变事实绑定
精确 Session/Run/Workspace/可执行文件/Profile 代际/Scope/预算/后端/进程树合同。即使 reviewer
接受，启动、网络、Profile 写入、终止、清理和 Artifact 权限仍全部为 false。
