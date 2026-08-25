# ADR 0135: Pre-1.0 Product Convergence Policy

- Status: Accepted
- Date: 2026-08-25
- Scope: GitHub issue #155; documentation/governance policy only
- Evidence baseline: `main@0751fadbe74feb7640750e4944643e14c41acaa2`
- Decision package: [`docs/convergence/`](../convergence/README.md)
- Reassess: 2026-11-30 or before `v0.2.0-beta.1`, whichever is first

## Context

Traverse Board has accumulated a capable local-first control plane and also four
forms of pre-1.0 review pressure:

1. schema v132 and the contiguous v1-to-v132 migration plan contain real audit,
   authority, exactly-once, ownership, recovery and cleanup proof, but clean installs
   pay the cost of replaying all historical construction;
2. CLI, TUI, HTTP/OpenAPI, React, Desktop, MCP, Analyzer, Plugin/Hook, Docker,
   Browser, GitHub Review and multi-agent work have been described together as
   “surfaces,” obscuring which are products, adapters, backends, integrations or
   optional extension seams;
3. `runmutation`, `operationreceipt`, and many domain ledgers repeat operation
   identity and replay checks, while their authority, write-ahead, receipt, recovery
   and cleanup semantics remain intentionally different; and
4. Thread-first navigation now coexists with older Mission, Session, Task, WorkItem,
   `cyberagent`, `.prayu`, and Prayu compatibility names.

Deleting migrations, building a universal operation framework, or globally replacing
names would make the source look smaller while weakening compatibility and review.
Leaving every capability “active” and every version token equally frozen would make
focus and maintenance cost unbounded. This ADR makes bounded keep/change/defer
decisions without implementing the risky follow-ups in one PR.

## Evidence and interpretation

- `LatestSchemaVersion` is 132. The plan is contiguous, validates names/checksums,
  accepts only explicitly recorded legacy checksums, and uses old-database fixtures.
- A raw Go/TypeScript scan finds 846 unique `*.vN`-shaped tokens. It includes public
  formats, internal rows, projections, transports, tests and golden vectors; the
  number proves inventory pressure, not that 846 external protocols exist.
- 397 Go files mention request fingerprints, 551 mention operation identity, 58
  mention receipt shapes/tables, and `runmutation` alone has 30 identity/fingerprint
  helpers. Concrete samples show a common identity/replay shape but different
  transactions, authority and recovery.
- ADR 0132 already fixes `Thread` as stable user identity, `Mission` as immutable
  intent/Scope, `Run` as finite attempt, and `Session` as Run-local context. ADR 0124
  already freezes technical brand compatibility identifiers unless a separate
  migration proves safety.

The counts above are a reproducible snapshot at the evidence commit. They are not
quality targets, and future drift is governed by #163 and #167 rather than keeping
the numbers constant.

## Decision A: Surface policy

Adopt the four tiers in the
[Surface inventory](../convergence/surface-inventory.md):

- **active:** Windows/macOS Desktop, the shared React Thread workbench,
  `cyberagent` CLI, loopback HTTP/OpenAPI, and core capabilities reached through the
  same Go Application contract;
- **maintenance-only:** Bubble Tea TUI, headless event/CI commands, and legacy
  Run/Session diagnostic views;
- **extension-only:** MCP, signed Plugin/restricted Hook, third-party Skill,
  Rust/WASI Analyzer contracts, and CTF/cyber add-on skeletons; and
- **deferred:** hosted/multi-tenant control plane, native Linux/mobile/editor full
  clients, public marketplace, and autonomous offensive packs.

Docker/AppContainer, Browser/CDP, Git/GitHub Review and multi-agent coordination are
classified as backends/capabilities/integrations rather than additional user
surfaces. Their active security and recovery obligations remain; the classification
does not grant authority or make optional integrations startup dependencies.

New or promoted surfaces require an owner, shared Go contract, threat/authority and
recovery analysis, supported-platform/accessibility matrix, release/CI owner,
compatibility window, export and removal plan. Downgrade/removal requires a separate
decision; maintenance removal normally waits at least 90 days and two tagged
prereleases. Protocol obligations survive a Surface tier change.

[#163](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/163)
implements drift/entry/exit enforcement. This ADR itself removes no feature.

## Decision B: Schema and protocol policy

Adopt the four freeze classes in the
[Schema/protocol inventory](../convergence/protocol-inventory.md): external durable,
internal durable, projection and ephemeral.

- The v1-to-v132 legacy migration path remains supported and append-only. Historical
  names/checksums/readers are not deleted for source-size reduction.
- Audit, authority, receipt, ownership, recovery and cleanup ledgers are internal
  durable, not disposable UI projections.
- A projection may be rebuilt only from a named retained source with stable order,
  cursor, invalidation and redaction behavior.
- Ephemeral handles/streams cannot become restart authority merely because metadata
  about them was stored.
- Durable version changes follow register -> dual-read -> write-new -> transactional
  migration/retention -> evidence-gated reader retirement. Unknown/conflicting
  authority versions fail closed.

A consolidated latest-schema baseline for **proven empty new databases** is approved
in principle under [#164](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/164).
It is not a squash: old databases still traverse the legacy plan, and baseline output
must be equivalent to representative upgraded fixtures in schema and invariants.

[#167](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/167)
adds a machine-readable family registry and drift check. No reader removal is
approved by this ADR.

## Decision C: Durable operation boundary

Do not create a universal operation kernel.

`runmutation.Operation` stays specific to structured WorkItem/Note mutations and its
Run/Session/lease invariants. `operationreceipt.Receipt` stays a content-free
presentation projection for declared kinds. Neither becomes a generic transaction,
authority, recovery, or cleanup framework.

Domain packages continue to own:

- canonical request fields and authority snapshots;
- transaction/WAL placement and external-effect ordering;
- approval, lease, revision and generation fencing;
- result/receipt fields and public errors;
- restart reconciliation and exact resource cleanup; and
- database triggers proving cross-row lifecycle invariants.

The only approved extraction is the pilot in
[#165](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/165):
a small immutable identity/replay validator with a versioned domain separator,
length-delimited fingerprinting, normalized digests, and the shared
same-key/same-request replay versus same-key/changed-request conflict rule. Two
low-risk flows must adopt it without schema, protocol or error changes before any
expansion is considered. The concrete risk matrix is in the
[durable operation inventory](../convergence/durable-operation-inventory.md).

## Decision D: Canonical vocabulary

Use the [canonical vocabulary](../convergence/vocabulary.md) in ordinary product
presentation:

- **Thread:** stable user-facing task and history identity, with no authority;
- **Run:** one finite execution attempt;
- **Step / Tool Item:** narrative grouping and exact structured tool lifecycle item;
- **Workspace:** operator-selected source/repository scope, not a sandbox; and
- **Plan item:** presentation label for the existing WorkItem planning identity.

Mission remains the internal immutable intent/Scope object. Session remains the
Run-local conversation/context and authority boundary. “Task” is generic prose or a
qualified child/batch domain term, not another top-level entity. Agent is an actor,
not a task or control plane.

Presentation converges first through
[#166](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/166).
The `cyberagent` executable, Go module, `CYBERAGENT_*`, data paths, `.prayu` project
paths, API v1 routes/DTOs, database names, event/protocol IDs, package identities and
historical artifacts remain compatibility identifiers. Cosmetic vocabulary does not
select a Surface, permission, approval, Profile, lease, backend or capability.

## Approved delivery sequence

1. Merge this ADR and its inventories; no runtime/schema mutation occurs.
2. Deliver #163 and #167 governance checks so later additions update the inventories.
3. Deliver #166 presentation copy independently, with API/database compatibility
   tests and no authority change.
4. Pilot #165 in two flows and compare migration/restart/concurrency behavior before
   considering another adopter.
5. Implement #164 only after exact empty-database detection and legacy-equivalence
   fixtures exist; keep the historical path available through rollout.

Each item is a GitHub sub-issue of #155, has an explicit non-goal and rollback
boundary, and must be independently reviewable. There is no combined rewrite phase.

## Rejected alternatives

### Squash migrations now

Rejected. It would conflate clean-install cost with user-database identity and could
delete the only executable proof for historical audit/recovery invariants. A new
empty-database baseline is the bounded alternative.

### Treat every `*.v1` as an external forever-contract

Rejected. Test vectors, projections and process-local transport do not need the same
reader retirement rule as exported formats or durable authority rows. Classification
must identify the exact object and source of truth.

### Expand `runmutation` into a generic framework

Rejected. Its target and lease fields are valuable precisely because they are not
optional. A generic state machine would either exclude important domains or hide
their differences behind callbacks and unchecked JSON.

### Global rename to Thread/Traverse Board

Rejected. It would break durable identities, scripts, configs, credentials, package
upgrades, API clients and historical evidence without improving the user model.
Presentation aliases and explicit compatibility explanations provide the benefit
without changing authority.

### Keep all implemented paths active

Rejected. Implementation existence is not a product commitment. Explicit maintenance
and extension tiers preserve compatibility while keeping beta release work focused.

## Security, migration, and rollback

- This ADR and inventory PR perform no SQLite migration, process/network call,
  credential action, file mutation outside documentation, or runtime capability
  change.
- No historical audit, receipt, authority, recovery, or cleanup record is removed.
- Inventory/tier words are non-authorizing metadata. Runtime continues to derive
  authority exclusively from Go-owned validated state.
- If a follow-up cannot preserve same-key replay/conflict, stale-generation refusal,
  old-database upgrade, backup restoration, or exact cleanup behavior, it is blocked
  rather than “mostly compatible.”
- Reverting this documentation returns the prior policy text but cannot authorize
  reverting/deleting user data or protocols introduced after it. A superseding ADR
  must account for already published contracts.

## Non-goals

- No schema squash, migration deletion, database/table rename, or baseline
  implementation.
- No full Store/Application/Domain rewrite or generic repository.
- No Surface implementation/removal, API v2, CLI executable rename, package-identity
  migration, or remote/cloud launch.
- No claim that file/type/protocol counts are defects by themselves.
- No weakening of Go control-plane, Scope, policy, approval, lease, sandbox,
  credential, privacy, audit, or recovery boundaries.

## Reassessment

Reassess this policy on 2026-11-30 or before `v0.2.0-beta.1`, whichever occurs first.
Review earlier if any of these happens:

- a sixth top-level Surface is proposed or an extension becomes a core startup
  dependency;
- latest schema reaches v160, clean install exceeds the agreed release budget, or a
  migration compatibility incident occurs;
- #165 cannot migrate two flows without semantic drift, or three new operation
  families duplicate the same identity/replay code;
- API v2, package/data identity migration, hosted service, or external plugin
  marketplace work begins; or
- user research shows that Thread/Run/Workspace vocabulary still prevents ordinary
  task completion.

The reassessment may keep, amend, or supersede this ADR. It cannot silently delete
historical readers or shorten compatibility guarantees already published.

## 中文结论

Beta 前只把 Desktop/React、CLI、loopback HTTP/OpenAPI 与同一 Go Application 合同下的
核心能力视为 active；TUI/headless/旧 Run-Session 视图转 maintenance-only；MCP、Plugin、
Hook、第三方 Skill、Analyzer 和 CTF 骨架是 extension-only；云端、多租户、新原生客户端与
市场延后。旧数据库继续走 v1-v132 迁移链，新安装 baseline 只能在证明空库和最终等价后单独落地。
Durable operation 只抽取最小身份/重放验证器，领域状态机、审批、WAL、收据、恢复与清理不合并。
用户主词汇收敛到 Thread、Run、Step/Tool Item、Workspace；Mission、Session 与历史品牌/CLI/API/DB
标识按兼容边界保留。本 ADR 不进行大重写，而是由 #163–#167 五个独立、可回滚子 Issue 实施。
