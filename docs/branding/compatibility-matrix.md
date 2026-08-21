# 品牌兼容与迁移矩阵 / Branding Compatibility and Migration Matrix

Status: **Accepted implementation boundary**

本矩阵区分四类对象：可直接改变的展示文案、需要同步更新的规范地址、必须保留的兼容
标识，以及只有独立版本化迁移才能改变的身份。任何阶段都禁止全局替换品牌字符串。

## 1. 展示与规范地址

| Surface | Previous | Accepted target | First-phase rule |
|---|---|---|---|
| Product hero/About | `Prayu` | locale-aware `Traverse Board` / `针路簿`; full bilingual hero | Approved |
| Go user-visible product name | `internal/buildinfo.ProductName = Prayu` | `Traverse Board` | Change with focused Go/UI tests |
| React wordmark/title/ARIA | `Prayu` | Responsive locale-aware display name | Keep technical and accessibility subtitles |
| Windows/macOS display name | `Prayu` | `Traverse Board` | Display fields may change; package identities do not |
| GitHub repository slug | `CTF-CyberAgent-Workbench` | `Traverse-Board` | Settings change after exact URL audit |
| README clone/cd commands | Old repository slug | New canonical slug | Update immediately after GitHub rename |
| Badges and canonical links | Old repository slug | New canonical slug | Do not rely on redirects as canonical docs |
| Issue templates / SECURITY links | Old repository slug | New canonical slug | Update and verify every link |
| SARIF information URI | Old repository URL | New canonical repository URL | URI-only change; do not rename SARIF schema fields blindly |

GitHub redirects old web and Git transport locations after a rename, but the old repository
name must not be reused. Existing local clones should update `origin` explicitly. The full
name `Traverse Board · 针路簿` is not a valid GitHub repository slug because the slug is
ASCII-only and cannot contain spaces, the middle dot, or Chinese characters.

## 2. 第一阶段保持不变的兼容标识

| Identifier | Current value/example | Why it remains |
|---|---|---|
| CLI executable | `cyberagent` | Scripts, docs, automation, operator muscle memory |
| Desktop executable | `cyberagent-desktop.exe` / `cyberagent-desktop` | Packaging and launch contracts |
| Go module | `cyberagent-workbench` | Import/build identity |
| Process/data home | `.cyberagent-workbench`, `CYBERAGENT_HOME` | Existing user state and recovery |
| Environment variables | `CYBERAGENT_*` | Provider/runtime configuration compatibility |
| HTTP/OpenAPI protocol names | Existing routes, DTOs, headers and versions | API compatibility and generated clients |
| SQLite schema | Existing table/column/version names | Durable state and recovery |
| Protocol IDs | `workspace-checkpoint.v1`, `code-intel-lsp.v1`, etc. | Persisted and external contracts |
| Credential targets | Existing CyberAgent/Prayu target identifiers | Existing secrets must remain discoverable |
| Windows class/interop names | Existing compatibility values | OS integration and tests |
| Project configuration path | `.prayu/config.yaml` | Public per-project configuration contract |
| Project instruction paths | `.prayu/instructions.md`, `.prayu/rules/**` | Discovery order and fingerprints |
| Browser storage keys | `prayu.*` | Existing UI preferences |
| CSS/component internals | `prayu-*`, `PrayuBrand` | Non-user-visible; churn has no migration value |
| npm package name | `prayu-web` | Build-only package identity |
| Windows MSIX identity | `PrayuDesktop` | ADR 0110 upgrade/uninstall identity |
| macOS bundle identifier | `workbench.prayu.desktop` | Installed-app identity and future signing/notarization |
| Current artifact names | `Prayu-portable-*`, `PrayuDesktop.msix`, `Start-Prayu-*` | Download scripts and verification patterns |

An internal identifier may retain an old brand indefinitely if changing it creates risk but no
user benefit. Documentation must identify it as a compatibility identifier, not unfinished
duplicate branding.

## 3. Later migration requirements

### `.prayu` project files

A future path rename needs a versioned dual-read migration, deterministic precedence when both
locations exist, conflict rejection, stable fingerprint tests, ignore-file parity, and an
explicit retirement window. A one-release global rename is not acceptable.

### Windows MSIX and macOS bundle identity

Changing either identity creates a new installed application rather than a display-only rename.
It requires a separate ADR, clean-install and upgrade/downgrade/uninstall matrices, data-root and
credential discovery tests, signing/notarization decisions, and explicit handling of side-by-side
old/new installations. ADR 0110 remains authoritative until superseded.

### Artifact and launcher names

The repository currently has no published GitHub Release, so the owner may choose a deliberate
pre-release break. That decision must still update workflows, packaging scripts, checksum and
SBOM metadata, verification scripts, README download patterns, Windows/macOS guides, and smoke
tests in one reviewed change. Package identity is a separate decision from artifact filename.

### CLI, module, environment and data directories

These are not part of the presentation migration. If ever changed, use aliases or dual lookup,
deprecation warnings, restart/recovery tests, credential tests, and an explicitly versioned major
migration. Do not couple them to README or UI copy.

## 4. Accepted execution sequence

1. **Owner decision:** record the approved bilingual mark, GitHub slug, compatibility policy, and
   acceptance of the known cross-domain naming risk.
2. **Documentation presentation:** merge a small brand hero and architecture-metaphor section into
   the existing Chinese and English READMEs; never replace the full READMEs with the v1 draft.
   Prepare canonical URLs, badges, templates, security links, SARIF URI, and clone instructions
   against the approved slug in the same review branch.
3. **UI presentation:** change display strings through the Go-owned product name and responsive
   brand component; keep first-use technical subtitles and run desktop/mobile visual tests.
4. **Packaging display:** change only display fields unless a separate identity ADR is
   approved.
5. **Validation and review:** publish the exact branch only after tests, link/manifest checks,
   responsive visual review, and a classified compatibility inventory pass.
6. **Repository rename:** change GitHub settings last, verify old/new redirects and canonical
   metadata, update local remotes, and do not reuse the old slug.
7. **Compatibility migration:** defer indefinitely or deliver as an independently versioned major
   change with dual-read/rollback evidence.

## 5. Required verification

- exact repository-wide inventory classified as display, canonical URL, compatibility, test,
  generated output, or historical evidence;
- Chinese/English README parity and link validation;
- no OpenAPI, SQLite or protocol diff during presentation phases;
- focused Go buildinfo/CLI/TUI/Desktop tests;
- React unit tests, strict TypeScript, production build and accessibility-name checks;
- 1440×900, 1024×768, 390×844 and mixed-DPI visual checks for full/compact/icon brand variants;
- Windows portable/MSIX and macOS packaging verification when their display fields change;
- clean worktree and exact diff review before any GitHub settings change.

## 6. Rollback boundary

Before the GitHub rename, rollback is an ordinary code/doc revert. After the GitHub rename,
GitHub can rename the repository again, but canonical links and external caches may diverge.
Therefore the settings change is last and performed only after the code/docs change is ready to
merge. User data, package identities and compatibility paths are never part of
that rollback.
