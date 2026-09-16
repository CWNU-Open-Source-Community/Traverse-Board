# ADR 0157: Runtime scratch outside the project and saved report output

Date: 2026-09-09. Status: implemented; final acceptance is recorded
in `../UX_FIXES_PHASE_N_VALIDATION.md`.

## Problem

The real Windows PowerShell 7 check in phases L/M succeeded, but HOME/TEMP lived
under the coding directory. PowerShell startup caches consequently appeared as
project changes and entered delivery checkpoints. The report was accurate;
its captured input scope included runtime state the user had not edited.

Reports also showed command artifact metadata while the existing Thread activity
reader could safely return saved stdout/stderr. Reviewing a long failure required
leaving the report and locating the original activity.

The real long-output journey exposed two related blockers: `inline_window` was
treated as lost saved output even when both artifact bodies were complete, and
command intent deduplication rejected the same command on a new user message.
An external user file change did not advance the internal edit epoch, so a user
could not simply ask for another check.

## Decision

For new Local Sandbox commands, allocate disposable runtime scratch outside the
project under the private owner journal. Preserve the project working directory,
PowerShell normalizer, exact runtime capabilities, process-tree ownership,
network denial, and credential policy. Derive and pin each scratch internally;
journal its removal identity before granting access. Measure project and scratch
writes together. Cleanup must reap descendants and reject replacement/reparse
paths. Retain old v1/v2 owner recovery without granting it new deletion authority.

Do not exclude `.traverse-board` paths from Git diffs or checkpoints. They can
contain user files. Do not delete old caches or reseal historical report receipts.
This keeps new behavior clear without inventing a migration that guesses file
ownership from names.

Expose optional read-time `output_sources` alongside the HTTP report. Each entry
links one reported Job/artifact to an actual durable Thread command activity,
or states why only metadata is available. Resolve this with descriptors and Job
metadata, without loading output bodies on report GET/POST. Share the existing
Call/Run/Job/artifact binding checks with the body endpoint. The sidecar is not
part of sealed evidence and cannot change report status or receipt hashes.

Reuse a single `SavedCommandOutput` renderer in reports and the activity timeline.
Only an explicit expansion loads the existing safe body endpoint. Keep source
metadata validation, re-scrubbing, public-content digest checks, cancellation,
independent identities, retry and honest capture-limit text. A re-scrubbed body
may have a different digest/size from the sealed saved descriptor; display that
distinction instead of weakening either validation.

Distinguish an inline preview limit from a saved-output limit. For new report
generation and Supervisor verification, accept `inline_window` only when both
observed streams have valid saved blobs with exact Job scope, descriptors, hashes
and content. Keep actual storage limits, missing or corrupt artifacts, unsafe
stdin/credential policies and adapter incomplete reasons conservative. Current
observations and original-key report replays never reevaluate sealed receipts.
Raw CRLF byte counts need not equal normalized UTF-8 saved sizes.

Use the existing durable operator-message delivery to distinguish a new user
input from recovery of the same input. Keep historical command fingerprints;
exclude prior command launches from duplicate scope only when they belong to a
different explicitly delivered message. Preserve file-edit and job-management
deduplication. In Diagnose, an eligible new input records an existing ledger
turn-prepared entry permitting its first command launch, while retaining the
ability to propose a correction first. Do not globally switch to Execute,
manufacture an edit, reset budgets, erase repeated failures or bypass Stop and
permission checks. A new kernel turn or an old request replay alone grants no
new launch. Recovery retains the durable message identity.

## Tradeoffs and rejected alternatives

- Per-command scratch gives bounded cleanup and avoids a persistent cache store.
  Cache reuse across commands is lost; this is not a performance improvement
  claim. Shared persistent caches need measured value and separate ownership.
- Path filters would hide legitimate user data and make report scope ambiguous.
  Retrofitting old receipts would erase their original evidence.
- Generic artifact body links would bypass the existing Thread source boundary.
  Copying output into reports would duplicate storage and eagerly load up to
  4 MiB per artifact. Neither is needed.
- No new database tables, migration version, log framework, download endpoint or
  parallel output protocol is introduced. An unavailable old/CLI activity remains
  metadata-only. Plan-form burden and terminal editing controls remain separate
  UX work; they do not justify another execution engine.
- Extending the existing delivery/ledger identity avoids another turn state
  machine. Full saved-body validation is needed only for the narrow inline
  completeness exception; report read-time source projection remains cheap.
  This proves retained output is complete, not that the model read every line.

## Verification boundary

Use actual Windows LPAC/PS7 execution, process descendants and crash recovery;
real Git/SQLite/HTTP source binding; and a browser journey with long Chinese
stdout/stderr, body failure/retry and narrow-window reading. Preserve original
failures and environment constraints. Refer to phase N validation for tested
build identities and results; this ADR alone does not assert tests passed.
