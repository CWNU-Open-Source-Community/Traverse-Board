# ADR 0154: Keep Standard Code configuration consistent with Thread continuation

- Status: Accepted as an architecture decision; continuous browser acceptance is in progress
- Date: 2026-09-08
- Scope: F07 coding setup and Plan controls, with F14 configuration recovery

## Problem

The Standard Code preset changed the Run permission to Workspace Access while
leaving the Thread preference conservative. The next ordinary Thread turn treated
that difference as a pending user setting, cancelled the configured Run and
created a successor without its configured environment. The real API experiment
then failed before the model could run. Generating a report through separate Run
controls did not repair this Thread continuation gap.

V2 also lacked the existing preset and Plan controls in its normal review flow.
Reusing those controls requires their request identity and recovery feedback to
survive panel changes. A response with `null` required arrays was additionally
rejected by the client despite a successful HTTP response.

## Decision

Record the existing Thread permission snapshot ID and revision in the preset's
existing intent event when Begin persists the operation. Read that binding inside
the same database transaction as the intent; do not infer ordering from request
timestamps. A permission request can be prepared before Begin and commit after
it. The immutable snapshot identifies the preference actually observed by Begin.

On a fresh Commit, require the exact active Thread, Run, Mission and Workspace
binding, then compare the current Thread permission snapshot and revision with
the recorded anchor. Synchronize the Thread preference to the preset's fixed
Workspace Access permission in the same transaction as the Run snapshots,
configuration event and any required pause. Append the existing Thread permission
snapshot and audit event; do not introduce a second permission operation ledger.
If any later write fails, roll back this whole transaction, including the Thread
change and attempted pause. Existing Drydock preparation retains its own recovery
semantics; this is not a transaction over all filesystem work.

The preset still requires its existing exact tuple: controlled Code execution,
Plan phase, the selected supported sandbox profile, disabled network, restricted
CDP and no runtime capability grant. Ordinary Thread configuration is not silently
upgraded. If the operator changes the Thread preference after Begin, reject that
obsolete attempt rather than overwrite the later choice. A new explicitly
reviewed attempt can bind the new preference. Later model selection/reset and
permission changes retain their existing current-versus-successor behavior.

Keep the already-configured replay path before these new mutation checks. An old
preset key returns its original configured result without rewriting current Run
or Thread preferences. Replaying a historical success is not permission to undo a
newer setting or to repair historical configuration by mutation.

For a new preset-created Run, the existing creation transaction first creates
its initial Thread and permission, then records the anchor. When no Thread is
associated with the Run, store an explicit empty binding and do not manufacture
a Thread preference. A changed binding at Commit must not acquire another
Thread's authority. A historical still-pending Thread-bound intent without an
anchor cannot establish the required comparison and needs a new attempt.

Distinguish that permanent intent invalidation from temporary quiescence or
Thread-state conflicts. Use the narrow preset error identity for an obsolete
immutable permission anchor, a missing historical anchor, or disappearance of a
previously bound Thread. Do not absorb it into the generic `waiting_for_pause`
result. The HTTP error may include `operation_key_invalidated: true` only for
this Conflict classification. Temporary status/binding conflicts, ordinary
conflicts and network failures do not receive this marker. In particular,
archiving and reopening a Thread must not falsely make its original key
permanently unusable. This is an optional additive error field, not a generic
instruction to change keys after every failed request.

Before cancelling an active Run for a different pending model, use the existing
Thread model catalog's `Selectable` result for the desired Provider/model.
Resolving or parsing a model reference alone does not establish availability or
execution qualification. Reject an already-disabled or ineligible selection
while preserving the original Run and unqueued message; the operator can select
an available model and retry the original request. Keep successor preparation's
existing validation as well. This bounded precheck does not make cancellation
and successor creation one atomic operation or exclude every later preparation
failure or concurrent model change.

Expose the existing StandardCodeReadinessPanel and PlanDeliveryPanel in V2's
task review, using the selected Run's detail and actual capability readiness.
Offer fresh setup for the current eligible Code execution; historical executions
retain their state. Reuse the established preset, workspace trust, lifecycle,
direction-selection and Deliver-transition APIs and their backend gates. A
running execution must reach the required paused/quiescent state before the
applicable Plan action. Configuring an environment neither generates a plan nor
chooses a direction, starts the model, executes tests or publishes changes.

Use the existing page QueryClient for preset and Plan attempts, scoped to the
target Run and retaining the action, exact body, operation key and relevant
Thread refresh target. Reopening a panel or selecting another Run does not
create a replacement for a pending or unknown attempt; late callbacks resolve
only their matching operation. Confirm unknown results with the original
request. A changed trust confirmation is a new reviewed intent. For an unresolved
failed attempt, only the explicit invalidated-key response permits recovery with
a fresh preflight and a new key; the UI must not silently reuse the obsolete
anchor or parse English error text to choose recovery behavior. Renderer intent persistence remains
limited to the lifetime of the current QueryClient.

Fix the HTTP projection of required readiness collections to encode empty
arrays, including top-level blockers/next steps and each backend's
blockers/remediation. Keep the client's strict response validation and preserve
nonempty blockers. A Go nil slice must not turn a supported successful response
into a client contract error.

## Evidence and limits

The Phase H backend validation record distinguishes actual SQLite/Git service
tests from deterministic model and sandbox fixtures. Its coverage includes
Created/Paused/Running configuration continuation, old-key replay after a later
preference, opposite preparation/commit ordering across two store connections,
and injected failure after the attempted Thread write. Model precheck regression
logs separately record the old cancellation failure and recovery using the
original Run and request. These results do not themselves prove real OS command
execution, the complete browser journey or a native-platform matrix.

At this decision, continuous browser and command-flow acceptance is still being
worked through. Record its actual outcome in the Phase H validation document;
the status of this ADR accepts the architecture, not an uncompleted journey.
There is no new database table, migration, dependency, worker or configuration
framework. Cross-refresh/application-restart renderer intent persistence and
atomic recovery of every successor preparation failure remain outside this
change.

V2 exposes direction selection and entry into Deliver, but the selected Plan
WorkItems retain their existing completion gates. Recording their operator
DeliveryCheckpoint is currently CLI-only; the HTTP projection and Plan panel
display checkpoint history without a write control. A successful workspace
apply, command verification or Standard Code report does not replace that
operator attestation or complete the selected WorkItems. The existing CLI can
start each WorkItem, record its checkpoint while the Run is paused, and complete
that exact version after review. Any acceptance that uses those commands must
be reported as a browser-plus-CLI journey, not a complete UI delivery flow.
Exposing that existing control is a remaining UX item; this change neither adds
a new checkpoint framework nor relaxes the gate to claim full delivery closure.
