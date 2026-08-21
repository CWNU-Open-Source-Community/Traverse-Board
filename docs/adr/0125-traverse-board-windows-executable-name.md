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
2. The portable ZIP, operator-preview launcher, and MSIX package all use
   `TraverseBoard.exe` as their executable entry.
3. A tagged GitHub Release attaches `TraverseBoard.exe` directly in addition to the verified
   portable ZIP and provenance files.
4. No duplicate `cyberagent-desktop.exe` is included in new release packages. Historical
   releases remain immutable and available under their original filenames.
5. The Go source entry `cmd/cyberagent-desktop`, CLI executable `cyberagent`, Go module,
   `CYBERAGENT_*` environment variables, data home, protocols, MSIX identity `PrayuDesktop`,
   and macOS executable remain unchanged.
6. Existing `Prayu-portable-*`, `PrayuDesktop.msix`, and operator-preview launcher filenames
   remain compatibility artifact names; changing them is outside this decision.

## Consequences / 结果

- Windows users receive a clearly branded primary executable and a direct EXE Release asset.
- Scripts that address `cyberagent-desktop.exe` by filename must move to `TraverseBoard.exe` for
  `v0.1.0-rc.2` and later; pre-release `v0.1.0-rc.1` remains available for reproducibility.
- The MSIX upgrade/uninstall identity and all user data remain continuous.
- Avoiding a duplicate legacy EXE keeps the portable archive unambiguous and avoids shipping the
  same unsigned binary twice.

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
