# ADR 0153: Separate recorded delivery evidence from current observation and retry

- Status: Accepted as an architecture decision; full journey acceptance remains open
- Date: 2026-09-08
- Scope: F07 delivery report truth and F14 recovery of the original request

## Problem

A delivery report records evidence for a particular Run, isolated workspace and
revision. Its historical passed result cannot establish that the current files
still pass, especially after a revision change or a failed refresh. The previous
interface mixed those meanings, offered output and recovery buttons with different
destinations from their labels, and lost report request identity when the review
panel closed or changed Run.

An already-recorded operation could also fail during a retry because Record first
captured and checked the newer workspace state. Separately, Supervisor discarded
the Job IDs of failed verification attempts, so an automatic report selection
could omit the actual failure. These gaps concern the existing evidence and
operation contracts; they do not require another delivery framework.

## Decision

Keep the sealed receipt immutable. Its revision, files, verification conclusions,
reasons and digest describe what was recorded. A fresh observation separately
reports whether that receipt still applies to the last checked workspace and
authority state. Display the observation time and reason, and label file rows and
the original conclusion as recorded evidence. During a refresh, after a refresh
failure, or without an observation, cached passed data must not claim that the
current revision has been confirmed. Reading a report does not continuously watch
the filesystem or prove that it stayed unchanged after the observation.

For Record retries, normalize and validate the request, then look up the exact
Run-scoped operation digest in the existing durable report store before preparing
a new capture. Match the original request fingerprint and return that report with
a fresh observation. Never substitute the latest report for the requested one.
Reconstruct mutable environment fields used by the old fingerprint from the
sealed binding; continue binding the submitted Run, requester, declaration,
uncovered items and explicit Job selection. A changed intent under the same key
remains a conflict. An observation failure or stale result does not create a new
receipt or overwrite the old evidence.

The existing fingerprint records the effective Job list. An empty automatic
selection and the same explicit list therefore retain their historical
equivalence. For an empty selection on replay, use the original report's Job
order, not the current Supervisor selection. There is no new table, migration,
request schema, background worker or second operation ledger for this replay.
Fresh record creation retains its existing exact bindings and validation.

Preserve only the precise verification Job IDs whose own facts prove a nonpassing
result: failed, timed out, cancelled, killed or interrupted state, nonzero exit,
or truncated output. Retaining these IDs lets the existing report evaluator show
the actual failure or limitation instead of omitting the attempt. Do not retain
otherwise successful Jobs solely because the enclosing projection has incomplete
evidence: the report cannot reconstruct that projection's incomplete reasons from
those IDs alone. Invalid or missing projections do not manufacture evidence.
Supervisor remains in diagnosis after failure, the current-epoch completion gate
is unchanged, and a new mutation clears the previous verification selection.
Do not select every command in the Run as a substitute for verification intent.

Expose whether a Standard Code preset is configured as a read-only fact derived
from the stored configured operation and its Run/Mission binding. A similar
execution mode, permission or backend tuple is not proof of configuration.
Detail projections can provide this optional fact; omission means unknown, not
false. The UI confirms the fact before offering a new report and explains the
unconfigured or unavailable state. This hint grants no authority and does not
guarantee a usable Drydock or complete verification; Record still checks those
requirements. Generating a report records available evidence and does not run
tests, commit changes or publish them.

Retain the original report request in the existing page QueryClient, scoped to
its Run, including the original body, operation key and refresh targets. Pending
and unknown attempts survive switching review tabs, selecting another Run and
closing/reopening the panel. Resolve or clear only the attempt that produced the
callback. Confirm an unknown result with that original request before creating a
new one. This is page-lifetime recovery: it does not persist renderer keys through
a browser refresh or application restart. Durable backend replay and renderer
key persistence are different guarantees.

Open the exact Artifact descriptor referenced by each verification, using its
artifact ID and checking the expected Run, source Job, digest, size and stream.
Reuse ArtifactDetail for the metadata view and report mismatches or read failures
with a retry action. Describe this as an output record, not output text: the view
does not fetch or verify the actual stdout/stderr body. Opening a recorded file
uses the report's Drydock workspace and reads its current content, which may have
changed since the receipt. The report does not prove the source project was
merged or published.

Replace the misleading separate Undo/Rewind/Fork shortcuts with one entry into
the existing workspace recovery options. That destination applies its actual
scope, permission and preview rules. It does not promise that entering the view
performs recovery, or that effects outside the checkpoint can be reversed. Keep
the existing exact FileEdit and checkpoint recovery mechanisms rather than
creating report-specific mutation paths.

## Acceptance and remaining work

Accepted records the architecture decisions above, not a completed Standard Code
journey. Record the actual commands, failures and browser evidence separately in
the Phase G validation record. Required checks include stale and failed-refresh
presentation, exact-operation replay after workspace change, intent mismatch,
failed verification evidence without weakening completion, late mutation results
across Runs, lost-response confirmation after reopening, and exact output and
recovery destinations.

At the time of this decision, full Standard Code onboarding and a continuous
real-command/browser/native acceptance journey remain incomplete. The isolated
Thread experiment reached preset configuration but a subsequent `/turns`
request encountered a pending-configuration conflict. Keep that observed
integration gap open; successful configuration, sandbox readiness or isolated
report fixtures cannot stand in for a working first execution and delivery flow.
The present review page does not supply the missing onboarding entry. This ADR
does not change Thread configuration semantics to bypass that conflict or claim
that all platforms, command backends or recovery cases have been validated.
