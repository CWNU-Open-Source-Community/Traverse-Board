# ADR 0082: Browser CDP Permission Ceilings

Status: accepted

Date: 2026-08-01

## Context

Prayu already separates a Run's execution interaction, execution environment,
and host execution permission. Browser automation needs the same separation.
Treating every CDP method as one permission would make ordinary navigation and
DOM inspection indistinguishable from request mutation, Cookie access, and
arbitrary protocol calls. It would also make a persisted UI selection look like
authority to start a browser or connect a CDP transport.

## Decision

Schema v91 adds the immutable `run_browser_cdp_permission.v1` snapshot as a
fourth, orthogonal Run-scoped permission dimension:

| Mode | Ceiling | Process-local gate |
| --- | --- | --- |
| `restricted` / 受限 CDP | exact-scope navigation, bounded DOM snapshots, and screenshots | `browser_cdp_control` |
| `full_debug` / 完整 CDP（调试） | restricted capabilities plus request capture/mutation/replay, Cookie access, and arbitrary CDP methods | `full_cdp_debug` |

The product must display `高度敏感权限` next to the full mode. Selecting that
mode requires the current Run execution permission to be `debug`, the exact
operator confirmation, ordinary CDP control, and a dedicated full-CDP process
startup gate. Full CDP capability implies every lower execution permission
gate, but CDP permission remains separate from Shell and terminal permission.

Every snapshot, including `full_debug`, permanently fixes these fields to
false:

- `transport_enabled`
- `browser_start_authorized`
- `runtime_authorized`
- `capability_grant`

SQLite constraints, immutable triggers, Go validation, HTTP projection, and
React fail-closed controls enforce the same relationship. A stored selection
cannot survive an application restart as authority: every operation must
recheck current process-local gates and, in a future adapter, the exact browser
identity, target scope, session lease, and CDP method.

Models, Agents, Tools, Skills, browser content, repository files, and documents
cannot select a CDP mode. Only an operator can use the CLI or the independently
gated control API/Desktop surface. Idempotency is bound to the exact Run,
requester, target mode, and redacted reason.

## Consequences

The Permission settings page can accurately expose two CDP policy ceilings
without claiming that the built-in browser is operational. Restricted CDP can
later be attached to Safe Web navigation without inheriting request mutation.
Full CDP can later support authorized debugging and CTF instrumentation, but
only behind a separate adapter, scope checks, audit events, budgets, and an
independent release review.

This ADR does not add a browser process starter, disposable Profile writes,
network access, a CDP socket, request interception, Cookie extraction, or any
model-callable browser Tool. The existing Disabled/Fake CDP transport remains
the only implementation in this batch.
