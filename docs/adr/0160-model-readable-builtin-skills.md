# ADR 0160: Model-readable built-in skill guidance

Date: 2026-09-30

## Status

Accepted. Extends ADR 0111 with a model read path for embedded skills. Does not change the operator selection or external package contracts.

## Context

`model_invocable` was metadata only. Ordinary Supervisor requests did not expose a skill catalog or reader when no operator selection existed. Sending Markdown as ordinary tool output alone would also conflict with the root policy that treats tool text as untrusted. Generic bounded receipts and summaries cannot guarantee later delivery of the exact guidance.

## Decision

1. Offer a read-only `skill_read` tool in the shared Supervisor. Its description contains the current compatible catalog. Admit only the exact advertised name/version/content SHA, current mode and model invocation policy. Exclude explicit-only and already operator-selected names.
2. Use existing fenced tool execution and result transactions. Recheck active root, attempt, session, checkpoint, lease, cancellation and mode around the read. A successful committed tool row is a model-read fact, never an operator selection. Uncommitted reads have no activation effect.
3. Before every root provider request is prepared and checked against its context window, rebuild guidance from successful rows and exact embedded content. Do not trust a receipt's stdout as instructions. Recheck both current and pinned invocation/mode policy, redact content and keep source and delivered hashes distinct. Operator pins take precedence. Unavailable pinned guidance is explicit; a missing registry with existing reads fails visibly.
4. Deduplicate successful rows by skill name before querying the bounded projection. Failed or pending reads cannot replace a successful pin. Restore at most eight shared embedded items and the existing 8192 byte-based token ceiling, including operator selections. Repeated reads do not consume another slot. A same-batch later read sees earlier committed reads.
5. Keep provenance separate: no new operator selection/preparation records are manufactured. Tool ledger input binds the exact content and source; per-request Go reconstruction supplies system guidance subordinate to root policy and current operator instructions. External packages remain operator-selected, untrusted guidance.
6. Schema v173 expands only the admitted tool-name CHECK using the existing rebuild path; preserve rowids, actor foreign keys, old authority guards and records. Regenerate the clean-install baseline. There is no new capability, automatic approval, autonomous installation or specialist tool path.

## Content organization

Add `frontend-design@1.0.0` for Code/root visual work. Update `code` to 1.3.0 for concise implementation routing and `run-verify` to 1.2.0 for general runtime evidence rather than requiring the optional UI-evidence operation for every page check. Archive the exact old bytes/manifests before changing current entries. Retain the existing specialist context budget; the code skill remains below it. See [built-in skill guide](../builtin-skills.md).

## Evidence and limits

Tests exercise actual HTTP wire content, deduplication beyond generic receipt bounds, eight segment boundaries, generated summaries, SQLite reopen, pending read recovery, cancellation before result commit, same-batch budget rejection, old operator pins, and v172 rowid/FK/guard migration preservation. These establish delivery and state semantics.

The bounded real task discovered `code` and `frontend-design` without either name in its prompt. The next and post-compaction root requests contained both exact bodies. It stopped later after two authorized edit proposals and invalid tool-free continue replies, with no applied edits, screenshot or interaction check. Delivery success is not evidence that the model consistently follows guidance or that the frontend task succeeded. No retry or larger output allowance was added for this result.

History-based activation is scoped to the same Run. Successor Run inheritance, external autonomous discovery, specialist tool use, and guaranteed image inspection are outside this change. Existing migration rollback rules apply; preserve a pre-upgrade database for an older binary.
