# ADR 0120: Source-bound Real-browser UI Evidence / 源码绑定的真实浏览器 UI 证据

Date: 2026-08-20

## Status / 状态

Accepted for schema v119, `ui-evidence.v1`, and the gated Windows Desktop execution surface.

已接受，用于 schema v119、`ui-evidence.v1` 与显式门禁的 Windows Desktop 执行面。

## Context / 背景

Source review, snapshots rendered outside the application, and successful frontend builds do not prove routing, runtime CSS/media behavior, event handlers, focus, accessibility, console health, or request outcomes in a real engine. Conversely, a screenshot without source/runtime provenance can be stale, captured from a personal browser session, or produced by an unrelated process already using the expected port.

The existing Safe Web runtime already defines executable discovery, publisher review, disposable Profiles, Windows WFP containment, creation-time Job Objects, exact target scope, and a closed restricted-CDP transport. The Run-owned command runtime provides cancellable application ownership under `full_access` or its strict superset `debug`; neither is a general network sandbox. UI verification must compose these boundaries without turning persisted evidence, a Skill, React, or page content into authority.

源码审阅、脱离应用的 mock/snapshot 与 build success 无法证明真实引擎中的 route、CSS media、事件处理、focus、accessibility、console 和 request 结果；无来源/runtime 的截图也可能陈旧、来自个人 Profile，或实际上由已占用端口的外来服务生成。新能力必须复用既有 Safe Web 与 Run-owned command 边界，同时保持证据、Skill、React 和页面内容均不授权。

## Decision / 决策

### 1. Immutable protocol and strict state machine

Schema v119 stores immutable `ui-evidence.v1` manifests, append-only step receipts, content-addressed artifacts, and a bounded Attempt state machine. The only transitions are `not_run -> running -> passed|failed|cancelled|timed_out|interrupted`. `not_run` is explicitly unknown and `Status.Passed()` is true only for `passed`. SQLite triggers reject terminal mutation, deletion, late steps/artifacts, authority-widening manifest JSON, quota bypass, and invalid source/Run joins.

Operation-key digests and request fingerprints make identical retries converge and changed intent conflict. Startup reconciliation converts only persisted `running` attempts to `interrupted/cleanup`; it never restarts a PID, browser, command, Profile, lease, or capability.

### 2. Exact source and recipe binding

The manifest seals the repository kind, commit/branch, dirty flag/digest, canonical root fingerprint, exact Git index digest, deterministic worktree manifest digest, optional build and mandatory start recipes, readiness contract, fixed browser identity/version/executable hash, restricted driver version, literal URL/route, viewport/DPR, locale/theme/reduced motion, deterministic fixture/seed/page state/data digest, ordered steps, masks, failure policy, Run identities, and creation time.

Command recipes retain canonical argv and Workspace-relative cwd while replacing host executable paths and environment values with SHA-256. They require `network=disabled`, `credentials=none`, closed initial stdin, bounded output/time, and reject known network-client or dependency-install intent. Raw typed fixture input is request-local and digest-sealed; it is not persisted.

Application captures a fresh checkpoint before build, after readiness, after browser assertions, and once more after owned application/browser cleanup before terminal success. Each resulting source binding is compared with the immutable manifest, closing the interval in which the still-running application could otherwise mutate source after the last assertion. Drift fails at a stable stage. Ignored/generated output follows the explicit workspace-checkpoint exclusion model; tracked or relevant untracked changes cannot silently inherit an old evidence identity.

### 3. Attempt-owned application and browser lifecycle

Before application start, Go probes the exact literal readiness endpoint. An existing listener yields `launch/preexisting_service`; it is neither adopted nor stopped, and cleanup does not claim its port. The application starts only through the current Code/Local/Deliver/root Run execution lease and `full_access` or inherited `debug` command-runtime capability.

The browser is freshly discovered from the fixed registry, version/hash/publisher revalidated, and launched headless with a disposable Profile through the reviewed Safe Web/WFP/Job lifecycle. There is no connection to an existing DevTools endpoint or personal Profile. The service, not the HTTP request, owns the bounded execution context. Start/read/wait authority continues to require the current Run lease. A distinct internal cleanup-only capability carries the exact durable Job, operation, Run, Workspace, root, and original lease identity and can only kill/reap that process-local owned Job after expiry, cancellation, or revocation; it cannot start, adopt, read, write, or target another Job. Cancel and Desktop shutdown wait for browser/application process trees, network containment, Profile removal, and owned-port release before terminal completion. After exact-owner quarantine, transient Windows sharing violations receive a five-second bounded deletion retry; exhaustion remains a cleanup failure and cannot be converted into a pass.

Read-only construction is separate from execution construction. Disabling UI-evidence control on a later launch still permits historical manifest/receipt/hash-verified artifact reads and startup reconciliation, but cannot start or cancel a process.

### 4. Closed real-browser action and capture surface

The UI-evidence CDP extension is a second explicit authorization bit over the base restricted session. Its closed method set supports device metrics, locale/media emulation, DOM query/focus/box/outerHTML, mouse/text input, accessibility, log/runtime event subscription, performance metrics, and screenshots. It deliberately excludes arbitrary JavaScript evaluation, cookie/storage access, request/response bodies, authentication material, request mutation/replay, and fulfillment.

Every navigation, subresource, and redirect stays within the exact literal loopback origin. The driver provides only navigate, click, bounded digest-sealed type, present/absent selector assertions, and capture. A first navigate step is mandatory. Screenshot masks are selector-based and fail when absent rather than silently leaking an expected region.

### 5. Evidence, redaction, and failure policy

V1 execution requires PNG screenshots together with DOM, accessibility, console/page-error, network/HTTP, and performance JSON so pixels cannot stand in for behavioral and runtime-health evidence. Video is reserved and rejected in V1. The logical viewport/DPR pair must fit a 7680×4320 pixel surface. PNG dimensions are bounded from `DecodeConfig` before full decode/allocation and must match `viewport × DPR` within one pixel of browser rounding; domain validation, SQLite insertion triggers, and the React receipt parser enforce the same relation. Artifact metadata binds SHA-256, MIME, length, viewport, image dimensions, source step, source commit, Run/Attempt, capture time, redaction, `retention_policy=run_history`, and `untrusted=true`. Local evidence is retained with Run history without silent expiry under per-artifact, per-Attempt, and global hard quotas of 32 MiB, 128 MiB, and 2 GiB; the CI upload copy has a five-day retention.

Text captures pass the output-safety and secret-redaction boundary. Network capture re-authorizes every diagnostic URL against the exact TargetScope, omits header, cookie, body, userinfo, query, and fragment, and persists only `[blocked-url]` for data/file/blob or other out-of-scope schemes. Screenshots cannot be content-redacted automatically, so deterministic synthetic/cleared fixtures and explicit masks are mandatory review responsibilities. Browser content and downloaded evidence remain untrusted and grant no process, network, credential, personal-Profile, request-mutation, or verification-pass authority.

The server enforces all four failure rules in V1: console errors, page exceptions, failed requests, and HTTP status failures. Stable failure stages separate build, launch, readiness, navigation, selector, assertion, console, network, capture, and cleanup. Incomplete cleanup overrides an otherwise successful outcome.

### 6. Equivalent surfaces and CI proof

Authenticated OpenAPI provides list/start/get/artifact/cancel; read and control bearers remain distinct, mutation bodies are strict/bounded, and cancellation requires `confirm=true`. Desktop uses the same Handler through its fixed bridge and adds explicit manifest review; its capability flag monotonically depends on Run execution, danger-full-access, and restricted CDP control. CLI deliberately exposes list/show/hash-verified exclusive export only, so it shares evidence semantics without copying runtime authority.

The React parser rejects unknown status/authority widening and verifies downloaded MIME/length/hash before creating a Blob. `not_run` uses a neutral badge. The built-in `run-verify@1.1.0` requires exact evidence fields, stable failure stages, `focused-checks` mapping, and a detailed PR verification receipt.

Windows CI launches the installed stable Edge from a clean fixed commit in a creation-time Job Object and disposable Profile against a deterministic loopback fixture. It covers desktop/light/en/full-motion and mobile/dark/zh/reduced-motion cells, real click/type/assertions, DOM/accessibility/performance/diagnostics/screenshots, exact request scope, dimensions and hashes. A second page that differs only by a missing event handler must fail the post-click real-DOM assertion. The workflow uploads screenshots and a source/browser-bound receipt.

## Alternatives considered / 备选方案

- Treat build or unit tests as UI proof: rejected because they do not execute browser behavior.
- Capture an externally supplied URL or attach to an existing browser/service: rejected because ownership, source, credentials, Profile, scope, and cleanup cannot be proven.
- Use unrestricted CDP/Playwright evaluation: rejected because arbitrary script, cookies, bodies, and request mutation materially widen authority.
- Store only screenshots: rejected because console, request, DOM/accessibility, environment, and provenance are needed to interpret the pixels.
- Make evidence automatically approve a baseline or PR: rejected because evidence is untrusted observation, not authorization or review judgment.
- Hide historical evidence when execution is disabled: rejected because read authority and process authority are intentionally separate.

## Consequences / 后果

- Real browser regressions can be reproduced and traced to a precise source/runtime tuple.
- The workflow is intentionally stricter and heavier than component tests; matrix breadth belongs in focused/release checks, not every edit loop.
- Windows is the current product execution platform because its reviewed WFP/Job/Profile path is complete. Other platforms retain read-only history until an equivalent containment adapter is reviewed.
- Fixture loading remains application-owned. The service binds and verifies the declared fixture and interactions but does not inject arbitrary storage or JavaScript.
- GIF/video and automatic baseline management remain out of V1.

## Verification / 验证

Tests cover protocol fingerprints, `not_run` semantics, source drift through post-cleanup completion, exact Job cleanup after lease release, secret/type digest rejection, closed CDP methods, pre-decode PNG bounds, real Edge matrix/regression detection, disposable-profile Job cleanup, exact-origin network diagnostics, immutable SQLite state/quotas/migration/reconciliation, no-adoption behavior, asynchronous cancel/close, read-only history, strict bearer/JSON/OpenAPI routes, hash-verified artifacts, CLI exclusive export, Desktop capability dependencies and bridge projection, React parsing/download/chronology/status/review gates, generated API types, and Skill archive compatibility.
