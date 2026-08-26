# Pre-1.0 product convergence / pre-1.0 产品收敛

- Policy version: `pre1-convergence.v1`
- Evidence baseline: `main@0751fadbe74feb7640750e4944643e14c41acaa2`
- Decision: [ADR 0135](../adr/0135-pre-1-0-product-convergence.md)
- Effective date: 2026-08-25
- Mandatory reassessment: 2026-11-30, or before the first `v0.2.0-beta.1`
  release candidate, whichever happens first

本目录是 #155 的可审阅决策包。它把“产品入口”“持久协议”“durable operation”
和“用户词汇”分开，避免把代码数量、表数量或字符串后缀本身当成删除理由。

This directory is the reviewable decision package for issue #155. It separates
product entry points, persisted protocols, durable operations, and user vocabulary
so that counts alone never authorize removal or compatibility breakage.

## Inventories

| Inventory | Question answered |
| --- | --- |
| [Surface inventory](surface-inventory.md) ([registry](surface-registry.json)) | What is active, maintenance-only, extension-only, or deferred? |
| [Schema and protocol inventory](protocol-inventory.md) | Which boundaries are external durable, internal durable, projections, or ephemeral? |
| [Generated protocol registry](protocol-registry.md) | Which exact production identifiers belong to each family, and which test/golden identifiers are explicitly allowlisted? |
| [Durable operation samples](durable-operation-inventory.md) | Where do identity, replay, authority, receipts, recovery, and cleanup repeat, and where must domains differ? |
| [Canonical vocabulary](vocabulary.md) | Which words are user concepts, internal identities, or compatibility labels? |

## Evidence snapshot

The baseline contains schema v132 and a contiguous v1-to-v132 migration plan.
Migration names and checksums are validated before any new migration runs. The raw
repository scan found 846 unique `*.vN`-shaped tokens across Go and TypeScript,
including production, fixture, golden-vector, and test-only identifiers. It also
found 397 Go files mentioning a request fingerprint, 551 mentioning an operation
identity, and 58 mentioning a receipt shape or receipt table. These are search
measurements, not protocol classifications or deletion targets.

The inventories therefore use representative protocol *families* and concrete
operation flows. The [machine-readable registry](../../protocols/registry.json) and
its generated readable view now classify exact production identifiers, bind every
test/golden exemption to exact files, and fail CI on inventory or reader-history
drift. The registry is governance metadata only and never grants runtime authority.

## Approved bounded follow-ups

| Issue | Approved change | Explicit boundary |
| --- | --- | --- |
| [#163](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/163) | Enforce Surface entry/exit gates | No feature removal or runtime capability |
| [#164](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/164) | [Clean-install consolidated schema baseline](../schema-baseline.md) | No legacy squash, renumbering, or checksum rewrite |
| [#165](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/165) | [Pilot a minimal operation identity/replay validator](../adr/0141-minimal-durable-operation-identity.md) | No generic persistence or universal state machine |
| [#166](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/166) | Presentation-first vocabulary convergence | No API, database, protocol, executable, or authority rename |
| [#167](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/167) | Classified protocol registry and drift check | No automatic reader deletion or data migration |

All five are GitHub sub-issues of #155. Approval of this decision package does not
approve a combined implementation PR; every follow-up remains independently
reviewable, reversible, and testable.

## Generated governance checks

`docs/convergence/surface-registry.json` is the single machine-readable source for
Surface tiers, owners, platform support, contracts, authority impact, release/test
evidence, compatibility, and exit plans. Regenerate its human-readable inventory
with:

```console
go run ./cmd/surfacecheck -write
```

CI runs the command without `-write`, comparing the proposal with the reviewed PR
base after the initial registry bootstrap. It fails when an entry disappears instead
of retaining a removal tombstone, reviewed transition history is rewritten, the
registry is invalid, the inventory has drifted, or the PR template no longer requests
the mandatory Surface entry/exit declarations. Registry metadata is governance
evidence only and is never read by the product runtime to grant authority.

## Change control

- A PR that changes a tier, freeze class, canonical term, or follow-up boundary must
  update ADR 0135 or add a superseding ADR. Editing an inventory alone cannot widen
  authority.
- Historical ADRs, migration checksums, exported receipts, and compatibility IDs are
  not rewritten to make the current narrative look simpler.
- If code contradicts an inventory, treat the inventory as stale and fail closed on
  compatibility or authority decisions until the discrepancy is reviewed.
- Emergency security removals may shorten a deprecation window, but require a
  security advisory, exact affected versions, a recovery path, and a follow-up ADR.
