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

`FullCDPAuthorization` (`browser_full_cdp_authorization.v1`) is independent from
the Safe Web start authorization. It is issued only when the Run uses the
maximum-access debug permission (with operator confirmation), the process
enabled the full-debug gate and the restricted-CDP runtime, and the caller
supplies an exact per-call confirmation. It binds the Run, Workspace, accepted
executable identity, permission revision, and session scope, and expires after a
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
restricted permission cannot authorize Full CDP, and the maximum-access debug
permission cannot enter the restricted runtime. The debug capability is
TTL-bounded, per-call confirmed, and invalid after restart. The metadata-only
result contract removes secret/cookie/header/body leakage by construction. The
dedicated, one-shot browser process and Profile remain shared with the Safe Web
launch machinery, while the Full CDP authorization stays a separate, bounded
gate.
