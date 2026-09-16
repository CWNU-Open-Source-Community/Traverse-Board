# ADR 0158: Reviewed inverse edits through ordinary conversation continuation

Date: 2026-09-09. Status: accepted in the scoped local Phase O journey; evidence is recorded separately
in Phase O validation. This extends ADR0152 without reopening historical edits.

## Problem

The reviewed single-file inverse path required a Running Run. Pausing after an
edit disabled the primary undo action, and a completed execution retained an
old Session/approval binding that a successor could not use. Merely enabling a
button would disagree with service, storage, lease and apply checks.

## Decision

Use ordinary Thread messages to request a reviewed inverse. In paused or
historical review, add a concise editable request to the existing composer,
preserve its draft and focus, and include exact source Run/Edit/Workspace/path
and applied hash. Do not send automatically. Existing Thread continuation owns
resume or successor publication and the stable physical working directory.

Extend `workspace_change` with `propose_revert`, accepting source identities,
path and expected hash only. Derive the inverse from the complete saved edit;
the model does not reconstruct its body. Reuse the current inverse service,
atomic proposal insert, review, policy, execution lease and compare-and-swap
application. The source must be an applied, unredacted supported text edit with
its original approval and exact scope. A different source Run must belong to
the same Thread and physical workspace. New writes require the current holder.

Create a new pending edit in the current Session. The old approval establishes
what happened, not permission to apply the inverse. The live file must still
match the applied source hash; otherwise refuse without overwriting later user
work. Historical edit/approval/Job/report records remain unchanged. Original-key
proposal replay returns its existing state, without reading current file content
or resealing history. A reversed creation still needs the existing separately
confirmed delete-apply path.

Permit this exact proposal action in Standard Code's Execute state as well as
existing proposal states. A user may review an inverse before rechecking the
current change. Keep inspection, selected Deliver plan, Stop, budgets and active
Job restrictions; leave Observe and arbitrary patch proposals unchanged.

## Reuse and limits

Keep the existing Running/current-edit direct preview path and unknown-request
confirmation. A previously created proposal remains readable; a draft is not a
proposal or a file write. Archived/read-only conversations cannot initiate this
action. Current and historical execution scope stay visible during review.

Adding an action instead of another tool name preserves the existing capability
tool list, avoiding unnecessary drift for configured Runs. No new message/control
endpoint, database migration, rollback store or automatic resume state machine is
needed. The HTTP same-Run inverse contract remains compatible. This is a bounded
extension of the existing code tool, not a second agent runtime.

Unsupported moves, redacted/missing source content, another Thread or a changed
physical directory remain explicit failures. Arbitrary external edits cannot be
made safe by a path-name heuristic. Full snapshot restoration retains its own
broader scope and interlocks.

## Verification

Use real SQLite/Git/gateway integration for fresh proposal/approval/application,
source preservation, cross-scope negatives and replay. Validate the actual paused
and completed conversation journeys in the browser, with a real file change,
ordinary message continuation, review, approval and apply. Preserve source and
unrelated user files; demonstrate a later user edit cannot be overwritten.
Component tests alone do not complete acceptance. Plan simplification is a
separate reviewed follow-up, not an additional implementation in this decision.

## Actual acceptance additions

The completed-Run browser journey exposed a remaining creator-only filter in the
file-edit queue. It now uses the existing durable Run binding, matching detail
ownership and retaining the legacy-schema fallback and scope-before-pagination
rule. The existing pending proposal survived the fix and restart unchanged.

Interactive Finish and a provider failure close their Turn while retaining the
Run. The terminal case therefore used the existing explicit CLI Finish, followed
by ordinary Web submission, fresh successor proposal/review/application in the
same physical directory. This does not introduce a required recovery entry point.

Later external content caused CONFLICT without a new proposal or overwrite. The
saved failure now asks the user to check source and current file before proceeding,
without claiming that every inverse conflict has the same cause. Cold original-key
confirmation created no additional execution. The final persistence audit passed
106 checks; the Phase O record specifies tested and untested surfaces.
