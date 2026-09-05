# ADR 0106: Full CDP Debug Channel

Status: accepted

Date: 2026-08-16

## Context

Safe Web and Full CDP are two independent browser permission chains. Safe Web
is the restricted, network-contained operator entry (ADR 0105); Full CDP is the
highly-sensitive debug channel that can observe, capture, mutate, and replay
requests and access cookies. Full CDP must never inherit Safe Web's evidence or
authorization, must never disable browser security, and must be bound to a
short-lived, per-call capability.

## Decision

`FullCDPAuthorization` (`browser_full_cdp_authorization.v2`) is independent from
the Safe Web start authorization. It is issued only when the Run has an exact
live Full Access or Debug execution snapshot, the independently confirmed Full
CDP sub-permission is enabled, the process installed the dedicated Full CDP
start/Profile/transport gates, and the caller supplies an exact per-call confirmation.
Full CDP defaults on when a Thread enters Full Access or Debug, may be disabled
independently in either mode, and is forced off below those execution ceilings.
This sub-permission applies only to a Traverse-managed isolated built-in browser;
it cannot attach to the Wails WebView or the user's system Chrome, and changing
the switch never starts a browser.
It binds the Run, Workspace, accepted
executable identity, browser and execution permission revisions, process-local
activation generation, revocation fence, and session scope, and expires after a
five-minute TTL. It authorizes request capture, mutation, replay, cookie access,
and arbitrary methods, but never webpage-instruction elevation
(`InstructionAuthorized` is always false) and never origin-policy or certificate
relaxation.

The `FullCDPSession` reuses the restricted CDP WebSocket client but admits an
additional highly-sensitive method set (`Network.getCookies`,
`Network.getAllCookies`, `Storage.getCookies`, `Fetch.fulfillRequest`,
`Runtime.*`, `Log.enable`) only when the client carries the Full CDP flag. Every
result is metadata-only: raw request bodies, headers, cookie values, and page
content are never returned.

## Consequences

Full CDP and Safe Web are provably non-crossing: Safe Web evidence or the
restricted browser permission cannot authorize Full CDP, and an execution mode
below Full Access cannot enable it. The Full CDP capability is
TTL-bounded, per-call confirmed, and invalid after restart. The metadata-only
result contract removes secret/cookie/header/body leakage by construction. The
dedicated, one-shot browser process uses the same standard-user Windows Job
substrate as Safe Web, but its start authorization, disposable Profile lease,
transport capability, and cleanup receipt are independent. It does not inherit
Safe Web WFP evidence or lifecycle authority. Literal-loopback navigation and
hostname-resolution default-deny remain mandatory.

Windows Wails Desktop owns the production Run-scoped Open/Status/Close caller.
The HTTP boundary accepts only a target, browser product/channel, permission
revision CAS, idempotency key, and explicit open confirmation. Executable paths,
PIDs, Profile paths, argv/environment, DevTools endpoints, WebSocket URLs,
fences, tokens, and authorization fingerprints never cross that boundary.
Close is synchronous and converges CDP close, Job/tree reaping, exact Profile
release/deletion, and a metadata-only audit event. TTL expiry, process exit,
permission/fence revocation, terminal Run state, and Desktop shutdown invoke the
same idempotent close path. Standalone CLI and Supervisor callers remain absent.
