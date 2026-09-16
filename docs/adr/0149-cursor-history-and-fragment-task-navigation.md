# ADR 0149: Reachable cursor history and addressable tasks

- Status: Accepted for the local UX convergence implementation
- Date: 2026-09-08
- Scope: F11 and adjacent navigation, draft and state feedback

## Decision

V2 read only the first 100 tasks, grouped by display name and selected another
task when the current selection was absent from that page. Reuse the existing
cursor endpoints and TanStack Query's infinite queries for active tasks, archives
and workspaces. Preserve loaded pages on failure, expose retry and further loading,
deduplicate by ID, and group by workspace ID. Search explicitly filters loaded
titles within the selected archive status; it is not full-text search.

Use a small projection of the browser History API for `#/threads/<id>`, `#/new`
and their settings sections. Fragments work with the existing loopback and Desktop
asset servers without new rewrite rules. URLs hold navigation identity only;
tokens and drafts remain in their existing stores. Reject malformed identities
and unknown sections instead of guessing another task.

The route owns current task selection. The connection store mirrors it for the
existing app integration. Only an initial address without a task bootstraps from
the remembered selection or first record; page refreshes never replace an explicit
task route. Settings retain their source task, and switching settings sections
replaces that entry so returning to the task remains a single navigation action.
Existing in-memory draft ownership is unchanged. New tasks inherit the current
task's workspace using its detail even when it is outside loaded history.

Do not poll every loaded history page every four seconds. Keep existing durable
change invalidation, explicit refresh and Query refetch behavior; the open task
continues its own state synchronization. Build each sidebar group in one pass.

## Alternatives and limits

A full router could become useful with nested loaders or many route families.
Those requirements are absent here; adding one now would not solve a missing
backend capability. The chosen adapter handles only navigation and uses native
history, not a new application state machine. Reconsider a maintained router if
the route families or lifecycle requirements grow.

There is no new search engine, task index, database migration or archive ledger.
The current database enforces unique workspace names; duplicate-name grouping is
tested as a frontend identity boundary, not evidence of database support for
duplicate names. Loading more records is not a scalability benchmark. Authenticated
task reopening does not promise draft recovery across browser or app restarts.

Real Web verification covers more than 100 active and archived records, old task
reopening, browser back/forward, reload and reconnect, archive reading/restoration,
draft preservation and a 390×480 viewport. Pagination failure and malformed route
cases have component/unit coverage. Native package and full-text search are not
claimed. See [Phase C validation](../UX_FIXES_PHASE_C_VALIDATION.md).
