# ADR 0125: Traverse Board Windows Release Executable Name

Date: 2026-08-22

## Status / 状态

**Accepted.** Beginning with `v0.1.0-rc.2`, the public Windows executable is
`TraverseBoard.exe`.

**已接受。** 从 `v0.1.0-rc.2` 开始，对外发布的 Windows 可执行文件名为
`TraverseBoard.exe`。

## Context / 背景

ADR 0124 deliberately kept `cyberagent-desktop.exe` during the first display-brand phase.
After the official Traverse Board icon and display brand reached `main`, the owner explicitly
approved the `v0.1.0-rc.2` release and requested that its EXE filename carry the project brand.
This is a separately reviewed packaging migration rather than a global identifier replacement.

ADR 0124 在首轮展示品牌迁移中有意保留 `cyberagent-desktop.exe`。正式图标与展示品牌
进入 `main` 后，项目方明确批准发布 `v0.1.0-rc.2`，并要求 EXE 文件名使用项目品牌。
因此本次变更作为独立的发布件迁移审查，而不是全局替换内部标识。

## Decision / 决策

1. `scripts/build-desktop.ps1` produces `build/desktop/TraverseBoard.exe`.
2. The internal portable ZIP and MSIX candidate use
   `TraverseBoard.exe` as their executable entry.
3. A tagged GitHub Release may attach `TraverseBoard.exe` directly with provenance evidence.
   The direct EXE is the user product; the portable ZIP and sidecars are validation/evidence
   artifacts rather than additional products.
4. No duplicate `cyberagent-desktop.exe` is included in new release packages. Historical
   releases remain immutable and available under their original filenames.
5. The Go source entry `cmd/cyberagent-desktop`, CLI executable `cyberagent`, Go module,
   `CYBERAGENT_*` environment variables, data home, protocols, MSIX identity `PrayuDesktop`,
   and macOS executable remain unchanged by this filename decision. ADR 0145 later requires the
   Store-bound manifest to use the exact Partner Center identity; `PrayuDesktop` remains a
   historical/local-development identity unless it is the value Partner Center actually assigns.
6. Existing published `Prayu-portable-*`, `PrayuDesktop.msix`, and operator-preview launcher
   filenames remain immutable historical artifact names. ADR 0145 separately removes the
   current source helper and all launchers from new user-facing packages without a renamed
   replacement.

## Consequences / 结果

- Windows users receive a clearly branded primary executable and a direct EXE Release asset.
- Scripts that address `cyberagent-desktop.exe` by filename must move to `TraverseBoard.exe` for
  `v0.1.0-rc.2` and later; pre-release `v0.1.0-rc.1` remains available for reproducibility.
- The historical/local-development MSIX identity remains unchanged by this filename decision.
  A Partner Center identity and its upgrade/uninstall data behavior follow ADR 0145 and require
  separate Store lifecycle evidence.
- Avoiding a duplicate legacy EXE keeps the portable archive unambiguous and avoids shipping the
  same unsigned binary twice.

## Distribution clarification / 分发说明（2026-08-27）

[ADR 0145](0145-windows-two-deliverable-release-contract.md) narrows the Windows user-facing
surface to two deliverables: a certified Microsoft Store package and a directly double-clickable
`TraverseBoard.exe`. Portable ZIPs and provenance sidecars remain internal validation/evidence,
and the old Start helper is removed without a renamed replacement. This clarification does not
claim Store certification or production signing and does not mutate historical releases.

[ADR 0145](0145-windows-two-deliverable-release-contract.md) 将 Windows 用户界面收敛为两个
成品：通过认证的 Microsoft Store 包，以及可直接双击的 `TraverseBoard.exe`。便携 ZIP
与来源 sidecar 只作内部验证/证据；旧 Start helper 被移除，不提供改名替代。
该说明不宣称 Store 认证或正式签名已经完成，也不改写历史 Release。

## Validation / 验证

- build the Windows executable twice and compare SHA-256;
- bind `binary_name = TraverseBoard.exe` in release metadata;
- verify the portable ZIP allowlist, per-entry hashes, `SHA256SUMS`, and extracted startup smoke;
- verify the MSIX manifest points both full-trust executable declarations to `TraverseBoard.exe`;
- confirm the PE icon resource is present in the renamed file;
- publish `v0.1.0-rc.2` only from a commit reachable from `main` after CI passes.

## Rollback / 回滚

Published tags and historical assets are not replaced. If the new filename causes a release
blocker, fix the packaging contract in a later prerelease and publish a new tag; do not mutate the
contents of `v0.1.0-rc.2` after publication.
