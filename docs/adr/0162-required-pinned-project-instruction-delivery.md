# ADR 0162: Required pinned project instruction delivery

Status: proposed for issue #214

Date: 2026-10-01

## Context

Project instructions are discovered inside one Workspace and pinned for one
TargetPath. Their source order, scope, precedence, hashes and workflow-only
authority already persist under `project_instruction_snapshot.v1`. Context
selection previously treated every instruction as optional at priority 760–799,
so higher-priority summaries, work boards and notes could displace all project
rules while the model and subsequent tools continued.

Raising priority does not distinguish a required rule from dispensable guidance.
Inferring requirements or overrides from natural language would create a new,
ambiguous policy authority. Live disk bytes also cannot replace an operator's
confirmed Run snapshot without the existing reviewed refresh.

## Decision

1. Add optional `project_instruction_delivery.v1` metadata to the pinned snapshot.
   An operator classifies every exact discovered source as `mandatory`, `optional`
   or `excluded`, with its path and content SHA-256. Exclusion requires the explicit
   reason `superseded`, `not_applicable` or `withdrawn`. This first slice classifies
   whole sources; it does not parse individual clauses or infer semantic overrides.
   Discovery creates no classification. Source order is normalized to the existing
   root-to-target precedence order.
2. Bind the complete classification to the snapshot fingerprint. The existing
   TargetPath, hierarchy, precedence, applicability and workflow-only authority
   remain part of that fingerprint. A live preview carries the pinned classification
   only when the entire discovered source set and target still match. Changed disk
   content remains an unclassified preview until the operator supplies renewed
   classifications against both expected pinned and live fingerprints.
3. Use the existing control-authenticated HTTP refresh endpoint as the minimum
   product entry. `confirm: true` and an operator actor are required to persist a
   classification or refresh. Classified snapshots may change only while the Run
   is created or paused and its execution lease has been released. The Store
   checks both in the confirmation transaction, preventing refresh during an
   in-flight model/tool dispatch even if a pause has already been requested.
   A later refresh cannot silently remove a delivery contract. Explicitly changing
   a requirement remains an operator decision and creates an immutable revision.
4. Reserve complete mandatory sections before selecting optional context, while
   retaining the existing priority/order rules for optional content. Mandatory
   envelopes include complete redacted pinned content, path, scope, precedence,
   source hash, snapshot fingerprint, requirement and existing authority. If the
   complete required set cannot fit, stop with `RESOURCE_EXHAUSTED`. Excluded
   sources remain auditable but are never selected. Optional omissions record a
   budget reason and estimate.
5. Check exact required envelopes after selection, after history reassembly, and
   on the final actual request immediately before root model dispatch. Check the
   original source-bound `model.started` receipt before any pending or newly
   returned tool round dispatch. Its Run, turn, attempt, model number and full
   classified snapshot identity must match. An old or refreshed-contract model
   start cannot authorize a tool continuation. This check precedes the existing
   gateway authorization; it grants no permission and restores no delivery lease.
   Generated-summary requests also carry the complete mandatory user-role
   envelopes and verify them after final window fitting. A request that cannot
   fit is refused before a model start or Provider dispatch; the existing
   extractive fallback remains subject to the root request's delivery checks.
6. Encode classified audit identities as
   `project-rule.v1/{requirement}/{snapshot-fingerprint}/{content-sha256}/{ordinal}`.
   Optional and excluded omissions use the corresponding `omitted/budget/` or
   `omitted/operator_excluded/` prefix. Paths, scope, precedence, requirement and
   excluded reasons can be resolved from the immutable snapshot history. Existing
   model-audit DTOs and Provider protocols remain unchanged.
   Auxiliary summaries omit optional rules as `omitted/auxiliary_scope/`, rather
   than claiming a budget failure or delivery. Their `context_compaction` starts
   retain the same source-bound mandatory identities and exclusion evidence.
   The protocol registry retains legacy, classified dispatch and immutable-history
   readers; retiring them requires migration or retention evidence and rollback.

## Compatibility and recovery

There is no database migration or historical checksum rewrite. When delivery
metadata is absent, the original canonical fingerprint, guidance JSON, source
audit identities and optional-selection behavior are retained, including Runs
with no project rules. This extension is opt-in rather than a claim that legacy
Runs already enforce mandatory delivery.

New readers validate both legacy snapshots and classified snapshots. A legacy
reader ignores the extension but cannot reproduce a classified fingerprint, so
it rejects that snapshot. Rolling back the writer must retain the new reader and
dispatch checks for already-classified Runs; an older binary cannot safely resume
those Runs. Immutable revisions retain operator decisions for inspection.

Disk drift does not invalidate an otherwise valid pinned snapshot. Restart,
compaction and native-tool continuation reconstruct context from that snapshot,
never from a silent live reload. A confirmed contract change invalidates pending
tools proposed by the former model context. Such a pending round stops with
`FAILED_PRECONDITION` and preserves its durable evidence for operator recovery;
it is not silently replayed or rebound to the new contract.

These are delivery constraints for the existing root Supervisor loop. Persistent
child task briefs and autonomous child read-only tools remain separate issues
#215 and #216. This slice does not claim that the earlier batch-delivery substrate
is an autonomous tool loop, or that every delivered rule was followed by a model.
Model configuration, permissions, scope, approvals, budgets and execution leases
remain governed by their existing contracts.

## Validation

Deterministic tests capture actual outbound Provider messages and compare complete
pinned content and source metadata. They exercise pressure from higher-priority
notes, optional/excluded omissions, selection and final-request overflow with
zero model/tool dispatch, native-tool continuation, actual history compaction,
generated-summary dispatch with complete pinned rules and purpose-specific audits,
summary-window refusal without dropping original history or mandatory rules,
SQLite close/reopen with live disk drift, contract changes before pending tool
dispatch, complete-rule retries, and rejected refresh during a live model lease.
API/service tests exercise operator confirmation, source hash
and pinned/live fingerprint checks, paused changes, immutable revision history and
unclassified compatibility. These tests establish local delivery behavior; exact
candidate CI and independent review remain separate acceptance evidence.

Operator usage is documented in [Project instruction delivery](../project-instruction-delivery.md).
