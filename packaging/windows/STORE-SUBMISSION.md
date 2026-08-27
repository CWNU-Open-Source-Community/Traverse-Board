# Microsoft Store submission runbook

This runbook covers the Store half of the Windows two-deliverable contract in
[ADR 0145](../../docs/adr/0145-windows-two-deliverable-release-contract.md). It produces and
checks a Partner Center-bound candidate; only a successful Partner Center submission and
certification make that candidate a Microsoft Store product.

## 1. Record the exact Partner Center values

Copy these values verbatim from **Partner Center > Product management > Product identity** into
GitHub repository variables. Do not infer them from the checked-in `PrayuDesktop` development
identity, normalize punctuation, or paste signing secrets.

| Repository variable | Partner Center value |
| --- | --- |
| `STORE_PACKAGE_IDENTITY_NAME` | Package/Identity/Name |
| `STORE_PACKAGE_PUBLISHER` | Package/Identity/Publisher |
| `STORE_PUBLISHER_DISPLAY_NAME` | Package/Properties/PublisherDisplayName |
| `STORE_PACKAGE_VERSION` | Explicit package version reserved for this submission |
| `DIRECT_EXE_SIGNER_SUBJECT` | Exact Subject of the protected public Authenticode certificate |
| `DIRECT_EXE_SIGNER_THUMBPRINT` | Exact certificate thumbprint approved for this release lane |

`STORE_PACKAGE_VERSION` is separate from the marketing/tag version. It must have four numeric
parts, use `Major >= 1`, use `Revision = 0`, keep every part at or below 65535, and increase for
every submission. Never derive it from an RC suffix or reuse a version already uploaded for the
same architecture.

Official references: [view app identity details](https://learn.microsoft.com/en-us/windows/apps/publish/view-app-identity-details),
[app package requirements](https://learn.microsoft.com/en-us/windows/apps/publish/publish-your-app/msix/app-package-requirements),
and [upload app packages](https://learn.microsoft.com/en-us/windows/apps/publish/publish-your-app/msix/upload-app-packages).

Before packaging, freeze these repository-controlled Store facts as reviewed files and retain their
SHA-256 values:

- the complete Simplified Chinese listing and complete English listing, including title, short and
  long descriptions, keywords, support contact, and release notes;
- the HTTPS privacy-policy text and its published URL;
- the selected age-rating questionnaire/result;
- every submitted Store icon/screenshot, including the exact product icon;
- a Partner Center/public Store readback export or screenshot set proving what was actually
  published.

The two language listings must describe the same two products and the same security defaults.
Neither may call the development MSIX, internal ZIP, self-signed package, or signing handoff a
third user product. GitHub Release publication renders
[`RELEASE-NOTES.md`](RELEASE-NOTES.md) with the exact version and verified trust state instead of
preserving an arbitrary Draft Release body; compare the published readback with both Store
languages before finalization.

## 2. Bind the protected direct-EXE signer

The reproducible `build/desktop/TraverseBoard.exe` is the unsigned payload shared with the Store
MSIX. Run the release workflow with `phase=prepare` for the stable version; its isolated signing
request artifact contains the payload, release metadata, and `direct-exe-signing-request.json`.
A protected external signer signs that exact request payload and returns these three Draft Release
intake assets for a stable tag:

- `direct-exe-signing-request.json` (unchanged from the prepare artifact);
- `TraverseBoard-signed.exe`;
- `direct-exe-signing-handoff.json` using `direct_exe_signing_handoff.v1`.

The handoff records the signing-request SHA-256, release version/revision, pre-sign payload SHA-256, post-sign artifact
SHA-256, exact signer Subject/thumbprint, `timestamp_protocol=rfc3161`, timestamp-authority
Subject/thumbprint, `signature_digest_algorithm=sha256`, `timestamp_digest_algorithm=sha256`,
signing service and operation ID, and the RFC 3161 token's PowerShell round-trip (`"o"`)
`signed_at_utc` value. No private
key or exported PFX enters the repository, Actions artifact, Draft Release, or ordinary Secret.

`stage-direct-exe.ps1` verifies Authenticode policy and the trusted timestamp, enforces the
repository signer identity, and proves that signing changed only the PE checksum, certificate
directory/alignment, and appended certificate table. It then emits `direct_exe_signing.v1` with
both hashes. On the final stable GitHub Release, `direct-exe-signing-request.json`,
`direct-exe-signing-handoff.json`, and `direct-exe-signing.json` are deliberately public,
hash-bound verification sidecars. The handoff therefore must contain no secret or private signing
material. `TraverseBoard-signed.exe` is an intake-only filename; the workflow removes it before
publication and publishes the verified bytes once, as the sole direct entry `TraverseBoard.exe`.
Every later verifier repeats the full policy instead of trusting derived JSON. An unsigned
prerelease instead records `trusted_release=false`; it cannot be promoted as a stable trusted
release.

## 3. Produce the candidate

Run the Windows release workflow from the exact source revision. When all six repository
variables are present, the retained Actions artifact contains:

- `TraverseBoard_<store-version>_<architecture>.msixupload`, for Partner Center;
- the corresponding `.msix`, for inspection only;
- `msix-manifest.json`, which binds Store identity, architecture, source revision, marketing
  version, package hashes, and the exact direct-download `TraverseBoard.exe` hash;
- `release-metadata.json`, whose hash and provenance are bound by the MSIX evidence;
- `TraverseBoard.direct.exe` and `direct-exe-signing.json`, which bind the Store payload hash to
  the final public-EXE hash without pretending the signed and unsigned files are byte-identical;
- the retained signing request and handoff, which let a later verifier repeat the exact signer,
  SHA-256, RFC 3161, source, payload, timestamp and service-receipt checks;
- the internal ZIP, SBOM/NOTICE/checksums, compatibility report, three producer reports, and the
  aggregate Standard Code release gate. `msix-manifest.json` hashes this exact inventory and the
  verifier reruns the aggregate gate before accepting a real Store submission candidate.

The public GitHub Release contains the sole direct entry `TraverseBoard.exe` and its public
verification sidecars, including the stable signing request/handoff/evidence. It does not publish
the intake-only `TraverseBoard-signed.exe`, internal ZIP, development MSIX, Store upload,
`Start-Prayu-Operator-Preview.cmd`, a renamed Start helper, or any command launcher as an
additional user product.

Microsoft Store candidates must remain unsigned by the repository: Microsoft re-signs accepted
packages after certification. A repository PFX is neither required nor accepted by the Store
mode. Do not sideload the unsigned Store-identity candidate; use the separate Development mode
for local signing/lifecycle tests. See Microsoft's
[Store signing guidance](https://learn.microsoft.com/en-us/windows/msix/package/sign-msix-package-guide#production-microsoft-store-distribution).

## 4. Inspect before upload

Place the downloaded files together under `build/desktop`, then run:

```powershell
./scripts/verify-msix.ps1 -Action inspect
```

The verifier fails if the upload or MSIX hash drifts; if Name, Publisher,
PublisherDisplayName, version, architecture, `runFullTrust`, or the embedded EXE differs from
the evidence; if the EXE differs from the direct download; or if a CMD launcher is present.

Upload the `.msixupload` through Partner Center. The archive root contains exactly the bound
MSIX. Do not rename a portable ZIP to `.msixupload`.

## 5. Restricted capability statement

Use this statement when Partner Center asks why the package declares `runFullTrust`:

> Traverse Board is a packaged classic Win32 local development workbench. runFullTrust launches
> the primary desktop process and, only after the user explicitly selects and trusts a workspace
> and approves the relevant permission, invokes local developer tools. It runs as the current
> user, requests no administrator elevation, installs no service or driver, does not modify
> system security policy, and does not declare allowElevation.

`runFullTrust` is a restricted capability and its approval is decided by certification. See
[App capability declarations](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/app-capability-declarations#restricted-capabilities).

## 6. Attest the exact artifacts

For non-PR release runs, the narrowly permissioned attestation job generates:

- SLSA build provenance for the final public `TraverseBoard.exe`;
- a CycloneDX SBOM attestation for that same EXE digest;
- SLSA build provenance for the exact `.msixupload` when a real Store candidate exists.

The workflow uses GitHub OIDC/Sigstore through `actions/attest`; the build/PR job itself receives no
attestation or OIDC write authority. Preserve the three bundles and
`windows_artifact_attestations.v1` index. Verify each bundle offline with `gh attestation verify`,
pinning this repository, `.github/workflows/release-desktop.yml`, the source revision, subject
digest, and predicate type.

## 7. Completion evidence

Record all of the following before marking the Store deliverable complete:

- source revision, marketing version, Store package version, architecture, and
  `msix-manifest.json` SHA-256;
- Partner Center submission ID and accepted upload timestamp;
- restricted-capability review outcome and final certification status;
- Windows 10 and Windows 11 lifecycle evidence for install, first launch, upgrade with a durable
  user-data sentinel, downgrade rejection, repair, uninstall, reinstall, and the documented
  default data-preservation behavior; record the tested versions, DPI/IME row, package full
  names, timestamps, and evidence hash;
- Microsoft Store product URL and the version visible there.
- SHA-256 of the frozen Chinese/English listing, privacy-policy text, product icon, and
  Store/Partner Center readback set that includes the age-rating result;
- SHA-256 and offline verification result for the exact GitHub attestation index and bundles;
- a live `Get-AppxPackage`/`Get-AppxPackageManifest` readback proving the exact identity, version,
  architecture, `SignatureKind=Store`, healthy package status and installed `TraverseBoard.exe`
  hash. Partner Center does not document a developer download for a post-certification signed
  MSIX, so the verifier uses the authoritative installed-package classification instead of
  inventing such an artifact.

Each lifecycle matrix row references a sibling `windows_store_lifecycle_row.v1` JSON file by safe
basename and SHA-256. The row report binds the exact release/package/OS/DPI/IME, old and current
package full names, monotonic operation timestamps, and equal before/after user-data sentinel
hashes.

Keep the evidence levels explicit:

- Authenticode and the GitHub/Sigstore bundles receive cryptographic verification.
- The finalizer independently reads the actually installed package and checks its exact
  `SignatureKind=Store`, identity, version, architecture, payload, and package manifest.
- Partner Center exports, public-Store screenshots, bilingual listing/privacy/age-rating records,
  and operator-authored lifecycle rows remain reviewer-attested external evidence. Hash-binding
  detects later drift and candidate mismatch; it does not cryptographically prove that the
  recorded external operation happened or that the reviewer statement is true.

### Required reviewer-attested JSON shapes

Use UTF-8 JSON, lowercase SHA-256 hex, JSON booleans (not strings), and UTC round-trip timestamps
such as `2026-08-27T12:34:56.0000000+00:00`. The finalizer rejects missing, stale,
unsafe-filename, or candidate-mismatched evidence; it never fills required fields from another
document.

| Document | Required fields |
| --- | --- |
| `windows_store_readback.v1` | `protocol_version`, `release_revision`, `marketing_version`, `store_package_version`, `processor_architecture`, `msix_manifest_sha256`, `submitted_upload_sha256`, `package_identity_name`, `package_publisher`, `publisher_display_name`, `partner_center_submission_id`, `accepted_at_utc`, `certification_passed`, `run_full_trust_approved`, `microsoft_resigned`, `listing_published`, `visible_store_version`, `store_product_url`, `privacy_policy_url`, `age_rating`, `listing_zh_cn_sha256`, `listing_en_us_sha256`, `privacy_policy_sha256`, `store_icon_sha256`, `listing_readback_sha256`, `github_release_readback_sha256`, `installed_package_full_name`, `installed_package_family_name`, `store_signature_kind`, `installed_payload_sha256` |
| `windows_store_listing_readback.v1` | `protocol_version`, `published`, `release_revision`, `marketing_version`, `store_package_version`, `partner_center_submission_id`, `store_product_url`, `privacy_policy_url`, `age_rating`, `listing_zh_cn_sha256`, `listing_en_us_sha256`, `privacy_policy_sha256`, `store_icon_sha256`, `captured_at_utc` |
| `windows_store_lifecycle.v1` | `protocol_version`, `release_revision`, `marketing_version`, `store_package_version`, `processor_architecture`, `msix_manifest_sha256`, `installed_package_full_name`, `installed_package_family_name`, `store_signature_kind`, `payload_sha256`, `direct_exe_sha256`, `standard_code_product_e2e_sha256`, and `matrix` |

`matrix` contains exactly these four keys once each: `windows_10|100|zh-CN`,
`windows_10|200|zh-CN`, `windows_11|100|zh-CN`, and `windows_11|200|zh-CN`. Every matrix object
contains `os_family`, `os_build`, `dpi_percent`, `ime`, the current `package_full_name`, a unique
safe-basename `evidence_file`, its `evidence_sha256`, and these nine true booleans: `install`,
`first_launch`, `upgrade`, `downgrade_rejected`, `repair`, `uninstall`, `reinstall`,
`data_sentinel_preserved`, and `accessible_name_checked`.

Each referenced `windows_store_lifecycle_row.v1` report repeats the release, Store package,
architecture, manifest, Store signature kind, payload, direct EXE, final product report,
package-family, current-package and OS/DPI/IME bindings. It also records a lower
`previous_store_package_version`, its same-family `previous_package_full_name`, equal
`sentinel_before_sha256`/`sentinel_after_sha256`, the same nine true booleans, and monotonic
`install_at_utc`, `first_launch_at_utc`, `upgrade_at_utc`, `downgrade_rejected_at_utc`,
`repair_at_utc`, `uninstall_at_utc`, and `reinstall_at_utc` values. The four row files are retained
beside the lifecycle index.

Do not edit `msix-manifest.json` from `not_run` to `passed`: it is immutable packaging evidence.
Install the published Store version on the finalization machine. Place
`windows_store_readback.v1`, `windows_store_lifecycle.v1` plus its four row reports, the frozen
listing/privacy/icon files, listing readback, exact GitHub Release body snapshot, attestation
index/bundles, direct EXE signing request/handoff/evidence, Store upload and original manifests
under a reviewed workspace, then run:

```powershell
./scripts/finalize-windows-release.ps1 `
  -StoreReadbackPath <windows-store-readback.json> `
  -LifecycleEvidencePath <windows-store-lifecycle.json> `
  -StoreListingZhCNPath <listing.zh-CN.md> `
  -StoreListingEnUSPath <listing.en-US.md> `
  -PrivacyPolicyPath <privacy-policy.md> `
  -StoreIconPath packaging/windows/StoreListingIcon.png `
  -StoreListingReadbackPath <listing-readback.json> `
  -GitHubReleaseReadbackPath <github-release-readback.md> `
  -ArtifactAttestationIndexPath <artifact-attestations.json> `
  -ExpectedDirectSignerSubject $env:DIRECT_EXE_SIGNER_SUBJECT `
  -ExpectedDirectSignerThumbprint $env:DIRECT_EXE_SIGNER_THUMBPRINT
```

The finalizer is fail closed. It emits `windows_release_completion.v1` only after independently
reading the live installed package and verifying `SignatureKind=Store`, exact
identity/version/architecture/payload/manifest, protected direct-EXE signing
request/handoff/evidence, four hash-resolved Windows 10/11 × 100%/200% DPI × `zh-CN` IME lifecycle
row reports, exact live GitHub Release body/assets, final Standard Code product-report hash, and
all three GitHub attestation bundles. It hash-binds the reviewer-attested bilingual listing,
privacy policy, age rating, icon, Partner Center, and lifecycle readbacks without upgrading them
to cryptographic proof.

An Actions success, a generated `.msixupload`, or a locally unpacked package is not Store
certification evidence.

[Issue #123](https://github.com/CWNU-Open-Source-Community/Traverse-Board/issues/123) remains open
until one real stable candidate passes both the public direct-EXE trust gate and this installed
Store completion gate. Merging the workflow, producing candidate JSON, or completing only one
distribution route does not close it.
