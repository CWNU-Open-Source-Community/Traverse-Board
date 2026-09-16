# ADR 0148: Exact approval previews and task review through existing services

- Status: Accepted for the local UX convergence implementation
- Date: 2026-09-08
- Scope: F06, the supported checkpoint/delivery portion of F07, and project-file references in F13

## Problem and decision

V2 exposed approval decisions without enough proposal detail and left existing
file review, delivery, evidence and recovery services in the Inspector. Reuse
those services and their components in a task review drawer; do not introduce
another execution engine, evidence ledger, rollback service or UI state machine.

Keep the approval queue metadata-only. Add a read-only, bounded
`GET /runs/{run_id}/approvals/{approval_id}/preview` projection in the existing
`approval_queue.v1` family. It resolves the proposal from Go's stored binding,
checks Run/Session/Workspace/proposal identity and the applicable fingerprint,
redacts through the existing credential rules, and declares the actual effect.
Generic Shell and ScriptProcess approvals are dry runs; Git approval records an
intent; file writes use their separate review/apply flow; web fetch uses its
existing exact host decision. Real host and controlled command execution retain
their existing exact-envelope proposal panels and gates.

Project-file selection reuses WorkspaceExplorer and the evidence attachment API.
The existing QueryClient holds only draft references. Sending attaches each
selected digest with a stable operation key before submitting the message. Go
rejects changed content. Attachments are evidence, never instruction authority.
This multi-request flow is not atomic: an earlier attachment remains if a later
attachment or message fails. The UI explains this and preserves unsent input.
Successful submission clears only the references captured by that submission;
later selections and drafts survive. A known attachment failure's obsolete local
notice is removed after the same message is successfully resubmitted.

Task review selects an existing Run within the Thread. FileEditPanel keeps exact
review/apply controls, RepositoryDiffPanel shows current project changes with
explicit attribution limits, and correction requests carry file, hashes and Run
identity. Delivery uses the existing report and its current-revision validation.
Evidence and paged structured tool records remain in their existing stores.
Opening a delivery file uses the report's bound Drydock workspace.

## Rejection, recovery and limits

- Stale, failed or truncated previews cannot enable approval. The Go decision
  service rechecks shell and script fingerprints before using existing review.
  Decision retries retain their exact operation key and chosen scope.
- A restore remains an explicit, paused, authorized whole-project checkpoint
  transaction. Its preview, preflight, replay and final manifest must agree.
  External changes, including unrelated files added after the snapshot, can
  block it. Skipping these conflicts only in the preview would be unsafe: the
  transaction applies from its captured preflight state to the target snapshot.
  Keep this contract; selective per-file undo needs a separate bounded design.
- Preview truncation/unavailable recovery blocks UI confirmation. Retrying an
  uncertain restore retains the preview's operation key; refreshing state remains
  necessary. Conservative permission permits preview but not restore writes.
- No automatic commit, push, merge, broad permission increase, or rollback of
  effects outside the recorded workspace is introduced. V2 does not expose the
  legacy Fork action until it can navigate the resulting object correctly.
- Unavailable delivery services remain nil at HTTP composition boundaries,
  including Desktop. A typed nil service reports unavailable, never a fabricated
  invalid Run identity. Old passing receipts are not treated as current evidence.
- File references currently start in an existing task and allow up to four
  project files. Images, arbitrary local uploads, first-message references,
  application-restart draft persistence and a complete native matrix are separate.

## Validation and alternatives

Exercise real Gateway proposals and HTTP decisions, exact binding drift,
unauthorized reads, retry identity, source digest mismatch, actual model-context
inclusion, file apply, checkpoint conflict/undo/redo, pause/resume and keyboard
focus. Use existing deterministic tests and a local scripted provider; do not
infer real model quality or platform coverage from these fixtures.

Adding an all-purpose attachment store, replacing the frontend framework, or
loosening Go validation would add maintenance or authority without fixing the
observed gaps. The chosen implementation adds no package, database migration or
execution protocol. Concrete results and remaining limits are recorded in
[Phase B validation](../UX_FIXES_PHASE_B_VALIDATION.md).
