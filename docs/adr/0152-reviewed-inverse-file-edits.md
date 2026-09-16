# ADR 0152: Undo an applied file edit through a reviewed inverse proposal

- Status: Accepted and implemented; bounded acceptance recorded in Phase F
- Date: 2026-09-08
- Scope: F07 single-file review, with F14 recovery identity

## Decision

The checkpoint restore path compares and verifies a complete project snapshot and
Git index. Adding a file selector only to its preview would disagree with its
apply, restart and final verification semantics. Extending that entire path is
larger than the immediate need to undo a recorded text edit.

Derive a new ordinary FileEdit proposal from an applied source edit. Support
replace, create and delete operations, with exact inverse operation semantics;
move, unavailable originals and redacted content are outside this increment.
Require the stored original content to match its original digest, and the live
file to still match the source edit's applied result. The server derives the path,
text and hashes. Reuse FileEdit's actual diff, exact review, policy-checked apply,
durable apply result and checkpoint mutation boundaries. Historical edits remain
unchanged; reversing the new applied edit permits a reviewed redo.

Use a stable request key scoped to the Run and source edit. A user may start a new
attempt after declining an earlier inverse proposal; a transport retry retains
the same key. Derive the proposal identity from this bounded operation and use an
atomic insert-if-absent into the existing FileEdit table. Replays validate the
stored content and identity, retaining its current review/application state.
The existing general SaveFileEdit upsert cannot safely serve this creation path.
No database migration or second rollback store is needed.

The new proposal belongs to the source Run and its exact stored approval binding.
New proposals and writes require a Running Run and active Session. Keep the
current task permission; conservative mode can use the existing exact FileEdit
approval chain. Paused/terminal Runs need their own later workflow, and the UI
must explain this limit. Artificially moving a Run into another state to make an
undo button work would change the user's execution intent.

Use the existing execution lease for manual FileEdit apply, including checkpoint
completion; tool calls already carrying a valid lease retain their owned path.
Completed operations remain readable through their durable replay result. The new
endpoint reuses the file review capability and control bearer. The independently
disabled arbitrary-text proposal/source capability remains unchanged.

Deleting a newly created file must also expose the text being removed. Enrich the
read-only delete detail and mutation response only when the original body is
complete, unredacted and matches its hash; leave the persisted diff and operation
identity untouched. Empty files retain explicit deletion metadata. The selected
delete drawer loads that verified detail instead of relying on its metadata-only
queue row; an unavailable body is never presented as zero removed lines.

## Existing recovery fixes

Keep checkpoint previews bound to the selection that requested them; a late
response cannot replace a newer target. Retain an unknown restore operation in
the existing page QueryClient across review tabs, including its Run, action,
expected/target checkpoint and key. Confirm the original request before another
restore. An HTTP success carrying a failed transaction is not a completed restore.
Use the same existing page cache for unknown inverse proposal and file apply
requests. These identities survive component/tab changes, not a browser refresh
or process restart. The server can durably replay a retained key after restart;
that does not imply the renderer persists the key across application restarts.

Checkpoint preparation and resuming/acquiring execution must also exclude each
other for the same Run. Reuse the existing prepared/applying restore transaction
as the exclusion record: validate live paused/session/lease state when preparing
it, and reject Resume or execution acquisition while it remains open. Release
comes from its existing terminal status. Keep filesystem I/O outside database
transactions. This does not claim a general cross-Run workspace lock or exclusion
against an uncooperative external editor.

## Acceptance

Exercise inverse replace/create/delete through real disk and exact review/apply;
preserve unrelated files and reject a changed target or redacted original.
Verify retry/restart/concurrent creation cannot reset approved proposals, manual
apply owns its lease, and checkpoint preparation/resume acquisition are exclusive.
Check late preview selection, tab changes, lost response confirmation and failed
transaction feedback. Record actual tests and bounded browser evidence in
[Phase F validation](../UX_FIXES_PHASE_F_VALIDATION.md).
