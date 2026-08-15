# ADR 0105: Safe Web Readiness Receipt

Status: accepted

Date: 2026-08-16

## Context

The restricted Safe Web operator entry must stay disabled unless the Windows WFP
production evidence for the exact accepted browser is still valid. The evidence,
its review, and the launch plan are already immutable and fingerprint-bound, but
nothing yet collapses "is Safe Web ready for this browser right now?" into one
bounded, explainable, fail-closed judgment that the CLI, HTTP, and Desktop entry
can share.

## Decision

`BrowserSafeWebReadiness` records a short-lived, fail-closed judgment over one
`BrowserNetworkContainmentEvidence` + `BrowserNetworkContainmentReview` pair. It
binds the evidence, review, executable identity, acceptance, adapter, policy
version, and platform, and carries a single precise blocking reason whenever it
is not ready. A missing, expired, version-mismatched, hash-mismatched,
policy-mismatched, or rejected pair yields a `Ready=false` receipt rather than a
hard error, so the Desktop entry can display the exact blocking cause. The
readiness never authorizes a launch by itself; it only feeds the existing
`AuthorizeSafeWebStart` process-local gate.

## Consequences

The Safe Web entry now has one deterministic readiness predicate with a stable
blocking-reason taxonomy (`evidence_missing`, `evidence_version_mismatch`,
`executable_identity_mismatch`, `acceptance_mismatch`, `policy_version_mismatch`,
`adapter_mismatch`, `platform_mismatch`, `evidence_not_passed`,
`review_missing`, `review_binding_mismatch`, `review_not_accepted`,
`evidence_expired`). Operator-flow wiring (target selection, scope preview,
approval, and CLI/HTTP/Desktop surfaces) consumes this receipt; the receipt
itself changes no launch authority and keeps the product entry fail-closed.
