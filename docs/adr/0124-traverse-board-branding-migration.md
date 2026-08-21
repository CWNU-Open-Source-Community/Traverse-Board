# ADR 0124: Traverse Board Branding and Compatibility Boundary

Date: 2026-08-21

## Status / 状态

**Accepted.** This ADR approves the product display brand **Traverse Board · 针路簿** and
the GitHub slug `Traverse-Board`. It does not approve compatibility-identity or user-data
migrations.

**已接受。** 本 ADR 批准产品展示品牌 **Traverse Board · 针路簿** 与 GitHub slug
`Traverse-Board`，但不批准兼容身份或用户数据迁移。

## Context / 背景

ADR 0064 established **Prayu** as the user-visible product name while preserving
`cyberagent`, storage, environment, API, credential, and OS integration identifiers. The
`Traverse Board · 针路簿` theme accurately reflects the product's resumable and
auditable runtime: model proposals remain non-authorizing, Go owns control and durable state,
and execution facts survive process failure.

This is not a simple rename from the old repository title. Prayu is embedded in
user-visible copy, project configuration paths, browser storage keys, packaging workflows,
Windows MSIX identity, macOS bundle identity, tests, and accepted historical ADRs. In addition,
an active third-party software platform uses `TraverseBoard`/`Traverse Board` for digital-twin
planning and scheduling. The owner considers that product direction distinct from this
open-source Agent workbench and accepts the residual confusion risk. This is a project naming
decision, not a legal determination of trademark availability.

## Decision / 决策

### 1. Accept the display brand and record the known risk

The canonical product display brand is **Traverse Board · 针路簿**. The owner has approved the
English and Chinese mark, the `Traverse-Board` slug, and this compatibility boundary while
acknowledging the known cross-domain software name. Ordinary web research records the risk but
does not provide a trademark opinion.

### 2. Distinguish product display name from repository slug

The product displays `Traverse Board · 针路簿`. GitHub repository names are ASCII slugs, so the
repository name is `Traverse-Board`; the bilingual name
belongs in README, description, and product surfaces. Existing web and Git transport redirects
are migration aids, not a reason to leave canonical links stale. The old slug must not be reused.

### 3. Presentation migration does not migrate compatibility identities

The first implementation phase may change user-visible documentation and display strings only.
It keeps the `cyberagent` executables, Go module, data home, `CYBERAGENT_*`, HTTP/OpenAPI,
SQLite, protocol IDs, credential targets, `.prayu/**`, browser storage keys, CSS/component
internals, MSIX identity `PrayuDesktop`, macOS bundle identifier `workbench.prayu.desktop`, and
existing artifact/launcher names unless a separately approved migration says otherwise.

### 4. Theme vocabulary is explanatory and bounded

The v2 vocabulary in `docs/branding/` is presentation metadata, not an executable rename map.
Every first use carries a technical subtitle, an ordinary screen highlights no more than six
theme modules, and explicit Policy/Scope/Approval/Capability/Lease and Sandbox reasons remain
visible.

The v1 `Wake · 航迹` alias is rejected because `wake` is already a durable Run protocol, worker,
and Agent control-message semantic. V2 uses `Change Track · 变更航迹` for Diff/Code Journey/
Handoff trail. `Kamal · 牵星板` is conceptual reserve until a real trusted Run-state projection
exists. The deterministic analyzer is described as a Rust-implemented WASI Analyzer guest, not
the potentially misleading unqualified product name “Rust Analyzer.”

### 5. Historical decisions remain historical

ADR 0064 and other accepted ADRs are not rewritten. This ADR supersedes ADR 0064 only for the
current product-display name while preserving its compatibility and safety facts. Historical
progress ledgers and evidence retain the names that were true when recorded.

### 6. GitHub settings change is performed last

The code/documentation change must be reviewed and ready before the repository setting changes.
After the repository rename, the same bounded delivery updates canonical URLs and verifies
redirects. This repository does not publish a reusable GitHub Action and currently has no GitHub
Pages site, which reduces known redirect exceptions; those facts must be rechecked immediately
before execution.

## Consequences / 结果

Benefits:

- the historically grounded theme becomes the approved public brand;
- compatibility identities and recoverable user state stay intact;
- the GitHub slug, product display name, package identity, and artifact filename become explicit
  separate decisions;
- existing Run Wake semantics cannot be confused with Diff/Journey presentation;
- future implementation can be split into reviewable documentation, UI, packaging, and optional
  compatibility phases.

Costs:

- internal Prayu/CyberAgent identifiers may remain visible in advanced troubleshooting and paths;
- a full visual theme still requires responsive and accessibility validation;
- name-confusion and trademark risk cannot be eliminated by this technical migration.

## Rejected Alternatives / 被拒绝方案

- **Treat the cross-domain name collision as a release blocker:** rejected by the owner because
  this is an open-source Agent workbench in a different product direction; the residual risk is
  recorded rather than hidden.
- **Use `Traverse Board · 针路簿` literally as the GitHub repository name:** rejected because it is
  not a valid repository slug.
- **Global replacement of Prayu/cyberagent:** rejected because it breaks project configuration,
  package identity, scripts, data discovery, credentials, tests, and historical evidence.
- **Keep v1 `Wake · 航迹`:** rejected because it collides with existing Run Wake contracts.
- **Treat Kamal as an implemented authority source:** rejected because no independent trusted Run
  Fix domain object exists.
- **Replace the current READMEs with the v1 review draft:** rejected because the draft omits large
  current capability, safety, compatibility, and release sections.
- **Rewrite ADR 0064 to make history look consistent:** rejected because ADRs are append-only
  decision evidence.

## Validation / 验证

This ADR was prepared against `origin/main@93ab9af` after PR 122. The v1 archive contained only text-based
Markdown, YAML, JSON, and Mermaid sources; its 32-entry YAML matched its manifest, while its human
dictionary added Sea Trial and reserved Armillary for 34 visible rows.

Repository inspection confirmed the existing Run Wake collision, `.prayu` project paths,
`PrayuDesktop` MSIX identity, `workbench.prayu.desktop` bundle identifier, and current user-visible
Prayu surfaces. External evidence confirms active third-party software use of TraverseBoard; the
owner explicitly accepted that residual risk after distinguishing this project's direction and
open-source model.

The detailed decision is in [Branding Migration](../branding/README.md) and the exact
compatibility boundary is in [Branding Compatibility and Migration Matrix](../branding/compatibility-matrix.md).
