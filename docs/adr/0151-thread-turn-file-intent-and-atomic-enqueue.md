# ADR 0151: Bind project files to one durable Thread submission

- Status: Accepted and implemented; bounded Web acceptance recorded in Phase E
- Date: 2026-09-08
- Scope: F13 first messages and successor turns, with F05/F14 failure recovery

## Problem

The V2 composer used separate evidence attachment requests before submitting a
Thread turn. Evidence requires a running or paused Run, so this cannot support a
new Thread's first message or a cancelled/completed Run's successor. Calling the
message endpoint to create that successor first would make the message visible to
execution before its files are ready. The current steering operation record also
exists only after enqueue and is scoped to a Run; it cannot reserve and verify the
Thread-level file intent of an attachment failure across restart or Run changes.

## Decision

Keep `thread_message_submission.v1` and add optional `files` to `/threads/{id}/turns`.
Each item declares `source_kind: workspace_file`, a project-relative `path`, and
`expected_sha256`. Accept at most four unique paths. Reuse the existing Workspace
Explorer for selection and projected digests, including before Thread creation.
Keep the legacy message request shape and text-only queuing behavior.

Bind the original operation key, operator message, requester and canonical file
manifest to a durable Thread submission intent. A small forward migration is
justified here: existing steering records require an already-consumable message,
and existing evidence records bind individual files to a Run. Neither can protect
the missing pre-enqueue boundary. Do not hide differing manifests by hashing them
into different derived operation keys or substitute an in-memory UI ledger.

The Thread turn service prepares the actual Run and uses the existing evidence
snapshot, lifecycle and steering implementations. Validate source projections
before unnecessary lifecycle work. Publish prepared evidence and the operator
message together in one SQLite transaction, rechecking their Run/Session/Workspace
binding and whether execution can consume input. A file failure must expose no
partially accepted operator message. Reuse transaction helpers instead of copying
evidence validation or queue rules.

Replays must bind the same original intent, including calls through the older
message endpoint; changing text or files requires a new operation. Restart and
multiple store instances must obey the same durable identity. No detached worker,
new execution lease, model adapter or file browser is introduced.

Reserve content and manifest before preparation, but bind the actual Run and
message only when the atomic enqueue succeeds. Otherwise a preparation failure
followed by another request completing that Run would permanently strand the
original key. A successful concurrent replay uses its durable message binding.

For an actionable file rejection, mark that matching intent rejected in a write
transaction only if it still has no message. This is a one-way terminal bit, not
another execution lifecycle. Only that durable outcome permits the error response
to declare `message_queued: false`; the rejected key cannot later enqueue through
another process or the legacy endpoint. The client may then use a new turn key
after correcting the draft, while keeping the same Thread. Already committed or
different-intent requests must not receive that false claim. A network error
without a conclusive response keeps the original key for result confirmation.

## User-visible boundary

For this implementation, file-bearing messages wait until the current execution
is idle and no approval is pending. Pure text may still use the existing queue.
The UI explains that distinction, keeps references removable, and preserves the
draft when files cannot be submitted. It must not describe this as immediate
model steering or silently discard files to make a request succeed.

A new Thread still opens after its durable creation. The submitted text and file
snapshot move to that Thread so first-turn failure is recoverable there. Clear
only text still equal to the submitted string and references with matching IDs;
there is no separate text revision or ABA detection. Typing added while
creation is pending must remain in its original project draft. A rejected file
projection can be selected again or removed and retried without creating another
Thread. Cross-refresh draft persistence is outside this change. Keep unknown
submission identities for the same page lifetime as their drafts; remove successful
mutation records after refresh attempts settle; successful refresh is not assumed.
Scope concurrent creation
attempts by their existing request fingerprint so a late response cannot erase
another project's retry identity.

## Evidence required

Verify first-message files actually reach the local model, successor files bind
to the new Run, stale source failure executes no message, and corrected retry
keeps one Thread. Exercise manifest mismatch, multi-file failure, concurrent or
restarted store access and legacy endpoint bypass against real persistence.
Check text queuing, stop identity and draft isolation for regressions. Record
actual checks and any remaining limits in the Phase E validation document; this
decision alone is not a completed capability or native-platform acceptance. See
[Phase E validation](../UX_FIXES_PHASE_E_VALIDATION.md) for the completed checks,
including a discarded successful HTTP response and confirmation with the same
identity, source-change rejection without partial attachments, and successor files.
