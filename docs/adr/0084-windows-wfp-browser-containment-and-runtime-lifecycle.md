# ADR 0084: Windows WFP Browser Containment And Runtime Lifecycle

Status: conditionally accepted

Date: 2026-08-02

## Context

The restricted loopback CDP core cannot prove that every browser-internal
connection passes through Fetch interception. Product activation therefore
needs an independent operating-system network boundary and durable evidence
that process, network filters, and disposable Profile cleanup agree after
success, cancellation, failure, or restart.

## Decision

P11-C8A implements an operator-only Windows production probe using a dynamic
WFP session. One higher-weight permit names the accepted executable identity,
literal loopback address, TCP protocol, and exact port. Lower-weight terminating
filters deny all other IPv4 and IPv6 connections for that executable path.
Filters are installed atomically before a suspended, creation-time Job-owned
browser process resumes. The probe has no CDP, proxy, public target, caller URL,
personal Profile, relaxed browser-security flag, or product start route.

The probe first proves that five local canaries are reachable without the
filters, then proves that only the exact target remains reachable with the
filters. It also verifies process-tree termination, dynamic Filter ID removal,
and exact temporary-Profile cleanup. Existing instances of the same executable
are rejected because WFP application-ID filters apply by executable identity,
not by one process ID. This path remains unsuitable for a long-lived product
adapter until elevated production evidence and the same-executable race are
independently accepted.

The native adapter is hand-bound to the Windows SDK ABI. Regression tests fix
`FWPM_ACTION0` at 20 bytes with its union at offset 4 and `FWPM_FILTER0` at 200
bytes with the action/context/id offsets used by `FwpmFilterAdd0`. IPv4 values
are converted to WFP host order. A dynamic engine close must be followed by a
fresh-engine lookup proving every recorded Filter ID is absent.

P11-C8B advances SQLite to schema v92. Append-only lifecycle checkpoints move
strictly through `running`, `cdp_closed`, `process_quiescent`,
`network_released`, `profile_released`, and `completed` or `failed`. Cleanup
uses a bounded context independent of caller cancellation. A CDP close or
persistence failure does not skip process-tree termination, WFP teardown, or
eligible Profile cleanup. Profile release is forbidden until process quiescence
and network cleanup are both verified. Redacted receipts and events contain no
page content, screenshot, raw output, personal Profile, or Full CDP data.

Lifecycle finalization has a process-local exclusive claim. Concurrent callers
cannot both mutate cleanup state or publish duplicate receipts: exactly one
caller performs bounded cleanup and every later caller fails closed as already
finalized. Probe success also requires an explicit post-delete check of the
disposable Profile rather than relying on an unchecked deferred removal.

P11-C8C remains disabled. Code completion is not production acceptance. The
current non-elevated local probe fails closed with `wfp_elevation_required`
before any browser launch, so no CLI, HTTP, Desktop, Tool, Skill, or model route
may start the restricted browser.

## Consequences

Prayu now has a concrete OS-enforced containment mechanism and recoverable
lifecycle accounting, but not an operational built-in browser. An administrator
must run the bounded probe, an independent reviewer must accept the evidence,
and the same-executable process-wide effect must be resolved before C8C can be
considered. Full Debug CDP and personal profiles remain separate unavailable
capabilities.
