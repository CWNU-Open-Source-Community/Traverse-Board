# ADR 0103: Full-Access Launch Token For Browser De-Elevation

Status: accepted

Date: 2026-08-16

## Context

The Windows production probe runs elevated so it can install WFP filters, then
must launch the browser as the same user's non-elevated, medium-integrity
primary token. On Microsoft-account (MSA) administrator tokens the elevated
token lacks `SeAssignPrimaryTokenPrivilege`, so `CreateProcessAsUser` can never
de-elevate. The `CreateProcessWithTokenW` fallback — which requires only
`SeImpersonatePrivilege` — is therefore the sole route, but it was rejecting
the launch token with `ERROR_ACCESS_DENIED` at the `process_create_with_token`
stage.

## Decision

Duplicate the trusted interactive-shell (`explorer.exe`) token with
`TOKEN_ALL_ACCESS` instead of the documented minimum
`TOKEN_QUERY|TOKEN_DUPLICATE|TOKEN_ASSIGN_PRIMARY`. `CreateProcessWithTokenW`
needs more than that minimum on a restricted-access duplicate and otherwise
fails closed with `access_denied`. The token is still validated before any use:
same user, same session, non-elevated, medium integrity, primary type, and a
trusted `explorer.exe` system path.

## Consequences

An administrator production probe now passes on an MSA machine (verified
against an accepted Chrome install): all eleven acceptance flags are true, the
child is a same-user / same-session / non-elevated / medium-integrity primary
token bound to the restricted Job before resume, and no WFP rules, browser
processes, or temporary Profile remain. The token is duplicated from a
rigorously validated source and used only to launch the browser, so full access
does not widen any product authority. Issue #1 is unblocked.
