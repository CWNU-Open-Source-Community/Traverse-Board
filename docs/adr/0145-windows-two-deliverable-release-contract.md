# ADR 0145: Windows Two-Deliverable Release Contract

Date: 2026-08-27

## Status / 状态

**Accepted as the release contract; delivery remains evidence-gated.** This decision defines
what the project intends to give Windows users. It does not claim that Partner Center has
accepted a submission, that Microsoft Store certification has passed, or that a production
code-signing identity has been obtained.

**发布契约已接受，交付仍须通过证据门。** 本决策定义项目准备交给 Windows 用户的
成品，但不表示 Partner Center 已接受提交、Microsoft Store 认证已经通过，也不表示
项目已经取得正式代码签名身份。

[Issue #123](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/123) remains open.
Accepting this ADR or merging its repository implementation does not complete the issue. One real
stable candidate must still pass the direct-EXE trust gate, Microsoft Store certification and live
installed-package checks, and the exact lifecycle evidence gate.

[Issue #123](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/123) 继续保持开启。
接受本 ADR 或合并仓库实现都不等于完成该 Issue；仍须由同一个真实稳定候选通过直发 EXE
信任门、Microsoft Store 认证与实装包检查，以及精确生命周期证据门。

## Context / 背景

Earlier prereleases exposed a direct executable, a portable ZIP, a preview launcher, and several
hash/report/manifest files together. That was useful for build and compatibility validation, but
it made an ordinary user choose between implementation artifacts. The old
`Start-Prayu-Operator-Preview.cmd` name also carried a retired display brand and suggested that a
special launcher was required even though `TraverseBoard.exe` now owns the safe default startup.

此前预发布同时展示直发 EXE、便携 ZIP、预览启动器以及多种 hash、报告与 manifest。
这些内容适合构建和兼容性验证，却把实现细节变成了普通用户必须理解的选择。旧文件名
`Start-Prayu-Operator-Preview.cmd` 还保留了已经停用的展示品牌，并让人误以为安全启动
必须经过特殊脚本；实际上安全默认入口已经由 `TraverseBoard.exe` 自身负责。

## Decision / 决策

### 1. Exactly two user-facing Windows deliverables

The Windows release surface has exactly two products:

1. **Microsoft Store package.** The Store-bound MSIX is built with the exact product identity
   assigned by Partner Center and declares the real target architecture. Users obtain and update
   it through Microsoft Store. A repository-built MSIX, an Actions artifact, or a local sideload
   package is only a candidate until Partner Center accepts it and Store certification succeeds.
2. **Direct `TraverseBoard.exe`.** The GitHub release executable is a self-contained application
   entry that starts by double-click. It requires no archive extraction, command script,
   command-line flag, or separately started backend. A stable direct-download release requires
   trusted Authenticode signing; any unsigned build must remain explicitly labelled as a
   prerelease candidate and may trigger SmartScreen.

Windows 面向用户只保留两个成品：

1. **Microsoft Store 成品。** Store MSIX 必须使用 Partner Center 分配的精确产品身份并
   声明真实目标架构，用户从 Microsoft Store 获取和更新。仓库本地生成的 MSIX、
   Actions artifact 或旁加载包在 Partner Center 接收且认证成功前都只是候选件。
2. **可直接双击的 `TraverseBoard.exe`。** GitHub Release 中的 EXE 是完整应用入口，
   无需解压、命令脚本、命令行参数或另行启动后端。稳定直发版本需要可信
   Authenticode 签名；未签名构建只能明确标为预发布候选，并可能触发 SmartScreen。

Both products must bind the same semantic version, source revision, embedded frontend, and
release-gate evidence. The Store payload keeps the reproducible pre-sign hash, while the public
EXE records both that payload hash and the post-Authenticode artifact hash. Passing one route does
not certify the other.

两个成品必须绑定同一语义版本、源码提交、内嵌前端和发布门证据。Store payload 保留
可复现的签名前 hash，公开 EXE 同时记录该 payload hash 与 Authenticode 签名后的 artifact
hash；其中一条路线通过不能替另一条路线宣称认证完成。

### 2. Portable archives and sidecars are not products

The portable ZIP remains available only where CI, compatibility testing, packaged E2E, or
reproducibility checks require an exact container. It is not a third user product and must not be
presented as the normal download or Store upload. `SHA256SUMS`, SBOM, NOTICE, release metadata,
manifests, Standard Code reports, and GitHub attestation bundles are verification/provenance
sidecars, not launch choices. A stable public Release also carries the exact signing request,
`direct_exe_signing_handoff.v1`, and `direct_exe_signing.v1` as public verification sidecars. The
signer-returned `TraverseBoard-signed.exe` is intake-only and is removed before publication. These
files may accompany a release without appearing as additional products.

便携 ZIP 只在 CI、兼容性测试、packaged E2E 或可复现性检查需要精确容器时保留；它
不是第三种用户产品，也不能作为常规下载或 Store 上传对象。`SHA256SUMS`、SBOM、
NOTICE、release metadata、manifest、Standard Code 报告与 GitHub attestation bundle
属于校验/来源证据，不是启动方式。稳定版公开 Release 还会把精确 signing request、
`direct_exe_signing_handoff.v1` 与 `direct_exe_signing.v1` 作为公开验证 sidecar；签名者
返回的 `TraverseBoard-signed.exe` 只用于受控接收，发布前必须移除。上述文件可以伴随
Release 存在，但不能被描述成额外成品。

### 3. The command launcher is removed

The old `Start-Prayu-Operator-Preview.cmd` helper is removed without a renamed replacement.
Developers and users invoke `TraverseBoard.exe` directly, so a source helper cannot drift into a
third launch contract or hide required startup arguments. New packages carry no old-name alias.
Historical release assets and their checksums remain immutable under the names they originally
shipped.

旧的 `Start-Prayu-Operator-Preview.cmd` 辅助脚本直接移除，不提供改名后的替代脚本。
开发者与用户都直接运行 `TraverseBoard.exe`，避免源码 helper 漂移成第三套启动合同或
掩盖必需参数。新包不携带旧名别名；历史 Release 资产及其校验和保持原样，不做追溯
替换。

### 4. Compatibility identities remain deliberate

This decision changes distribution presentation, not durable runtime identity. The Go module,
CLI, `CYBERAGENT_*`, data home, protocols, credential discovery, and existing user state keep
their compatibility rules. Partner Center product identity values are injected exactly for the
Store build; their eventual values and acceptance must be recorded as evidence rather than
inferred from the legacy `PrayuDesktop` development identity.

本决策改变的是分发展示，不迁移持久运行身份。Go module、CLI、`CYBERAGENT_*`、数据
目录、协议、凭证发现和既有用户状态继续遵循兼容规则。Store 构建必须精确注入
Partner Center 产品身份；其最终值与接收状态必须以证据记录，不能从旧的
`PrayuDesktop` 开发 identity 推断。

## Consequences / 结果

- A user chooses between Store-managed installation and one direct executable, not between
  packaging internals.
- Documentation and release notes must say whether each of the two products is actually
  available for that version.
- CI may continue uploading internal ZIPs and evidence artifacts, but release automation must not
  promote them as user products.
- Store certification, Store signing, and direct-EXE Authenticode signing remain independently
  verifiable facts; no local build step may claim them. A protected signer returns a
  `direct_exe_signing_handoff.v1`; repository verification proves append-only Authenticode payload
  equivalence, the expected signer, a trusted timestamp, and the RFC 3161 handoff before producing
  `direct_exe_signing.v1`.
- Packaging evidence remains immutable at `lifecycle_validation_status=not_run`. External Store,
  bilingual listing/privacy/age-rating, Microsoft re-signing, and Windows matrix facts are consumed
  by `finalize-windows-release.ps1`, which emits `windows_release_completion.v1` only after exact
  readback and offline GitHub attestation verification.
- GitHub provenance and CycloneDX SBOM attestations bind the final public EXE, and provenance also
  binds the Partner Center upload. Attestation bundles are evidence sidecars, not extra products.
- Evidence strength remains explicit. Authenticode and GitHub/Sigstore bundles receive
  cryptographic verification; live package identity and payload receive an independent local
  readback. Partner Center exports, Store screenshots, listing/privacy/age-rating records, and
  operator-authored lifecycle rows remain reviewer-attested external evidence. Hash-binding
  detects later drift, but it does not prove that an external event happened or that a reviewer
  statement is true.
- ADR 0110 still governs per-user install/data lifecycle where compatible. This ADR supersedes
  its description of the portable ZIP as a user-facing alternative and, for the Store route,
  its literal development identity/publisher values whenever Partner Center assigns different
  values. Existing local installs are migration evidence, not authority to alter Store identity.
- ADR 0125 still governs the public executable filename and historical immutability. This ADR
  supersedes its decision to publish the operator-preview launcher or treat its old filename as a
  current artifact name.

## Validation / 验证

- A clean Windows machine can double-click the release `TraverseBoard.exe` without a ZIP, CMD,
  flags, or separately started service.
- The direct EXE and submitted Store candidate resolve to the same source revision and release
  evidence, with explicit pre-sign payload and post-sign artifact hashes.
- The Store candidate uses the exact Partner Center identity and correct processor architecture,
  binds and reverifies the same aggregate release evidence as the direct EXE, and records the
  Windows 10/11 install/launch/upgrade/downgrade/repair/uninstall/reinstall matrix; certification
  status is read back from Partner Center before it is called published.
- Final Store acceptance runs against the actually installed package and requires exactly one
  healthy `SignatureKind=Store` result with the submitted identity, version, architecture, and
  payload, plus the exact four `windows_store_lifecycle_row.v1` reports for Windows 10/11 at
  100%/200% DPI with `zh-CN` IME.
- Release inventory classifies only Store MSIX and direct EXE as products; ZIPs and sidecars are
  classified as internal validation or evidence.
- Source and new release packages contain no `Start-*.cmd` launcher. Tests may still inject such
  a filename as a rejection fixture.
- Historical tags and assets are not replaced or renamed.
- Stable promotion verifies the exact signer, trusted timestamp, public signing-service handoff,
  and Sigstore/GitHub artifact attestations. The finalizer independently verifies the installed
  `SignatureKind=Store` package and hash-binds reviewer-attested bilingual listing, privacy-policy,
  age-rating, icon, and four-row Windows/DPI/IME evidence without rewriting the original candidate
  manifest.

The operational Store identity, versioning, upload, restricted-capability statement, and
completion-evidence steps are defined in the
[Microsoft Store submission runbook](../../packaging/windows/STORE-SUBMISSION.md).
