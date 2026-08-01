# ADR 0083: Restricted Loopback Browser Runtime Core

Status: accepted

Date: 2026-08-02

## Context

Schema v91 separates restricted and full-debug CDP policy ceilings, but those
snapshots deliberately grant no browser process, Profile, socket, or protocol
authority. The next implementation step needs a real, testable Safe Web core
without turning a persisted selection into an executable capability or
exposing personal browser state.

CDP request interception is not an operating-system network sandbox. Even when
every page request is paused and checked, the browser process can have internal
network features that are not represented as an ordinary page request. A
restricted transport therefore cannot be wired into the product until a
separate OS- or container-enforced network boundary has production evidence.

## Decision

P11-C5 adds a Windows-only browser process adapter behind process-local startup
gates. Authorization requires the exact schema-v85 acceptance, launch attempt,
generation lease, independent review, schema-v91 restricted permission, one
literal loopback origin, direct proxy mode, and no relaxed browser security
flags. The executable is reopened and rehashed immediately before creation.
Windows starts it without a Shell, suspended and atomically assigned to a Job
Object with kill-on-close, process-count, memory, and runtime limits. Arguments
are fixed by Go: a disposable Profile, `about:blank`, loopback-only DevTools,
no extensions/sync/crash reporting/proxy, and disabled hostname resolution.

P11-C6 materializes only the exact disposable Profile generation derived by
the existing ownership plan. A canonical owner marker, process-local lease,
Profile-local environment directories, generation ancestry, and quiescent
process-tree evidence control create, recovery, release, quarantine, and exact
cleanup. Personal Chrome/Edge profiles, model-owned paths, foreign markers,
active generations, indirect paths, and replayed cleanup are rejected.

P11-C7 opens only the `DevToolsActivePort` endpoint inside that exact Profile
and dials a literal `127.0.0.1` WebSocket without a proxy. It creates one
dispose-on-detach browser context and one owned target, denies downloads,
disables cache and service workers, and pauses every page request. Redirects
and subresources must remain inside the exact Run scope and request budget or
are failed. The closed method table permits only lifecycle setup plus:

- exact-scope navigation;
- bounded DOM metadata counts without page text or script evaluation;
- bounded PNG screenshots marked as untrusted evidence.

`Runtime.evaluate`, response/request bodies, Cookie access, request mutation,
request replay, arbitrary CDP methods, personal profiles, and full-debug CDP
remain unavailable. Returned browser material is evidence, never an
instruction source.

The concrete runtime has no CLI, HTTP, Desktop, Tool, Skill, or model route in
this batch. Production activation requires a later independent adapter and
verified OS/container network containment. Tests use fake processes and a
local scripted WebSocket; they do not launch an installed browser.

## Consequences

Prayu now has a concrete but product-inert restricted browser runtime core. Its
identity, lifecycle, cleanup, method set, and evidence boundaries can be
audited before any end-user activation. Windows is the only concrete process
platform; other platforms fail unavailable.

The built-in browser is still not an end-user feature. A UI selection or
persisted v91 snapshot cannot reconstruct runtime authority after restart, and
full-debug CDP cannot reuse the restricted adapter. Product work must preserve
this distinction and must not describe CDP interception as a network sandbox.
