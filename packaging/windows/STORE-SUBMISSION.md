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

`STORE_PACKAGE_VERSION` is separate from the marketing/tag version. It must have four numeric
parts, use `Major >= 1`, use `Revision = 0`, keep every part at or below 65535, and increase for
every submission. Never derive it from an RC suffix or reuse a version already uploaded for the
same architecture.

Official references: [view app identity details](https://learn.microsoft.com/en-us/windows/apps/publish/view-app-identity-details),
[app package requirements](https://learn.microsoft.com/en-us/windows/apps/publish/publish-your-app/msix/app-package-requirements),
and [upload app packages](https://learn.microsoft.com/en-us/windows/apps/publish/publish-your-app/msix/upload-app-packages).

## 2. Produce the candidate

Run the Windows release workflow from the exact source revision. When all four repository
variables are present, the retained Actions artifact contains:

- `TraverseBoard_<store-version>_<architecture>.msixupload`, for Partner Center;
- the corresponding `.msix`, for inspection only;
- `msix-manifest.json`, which binds Store identity, architecture, source revision, marketing
  version, package hashes, and the exact direct-download `TraverseBoard.exe` hash;
- `release-metadata.json`, whose hash and provenance are bound by the MSIX evidence;
- the internal ZIP, SBOM/NOTICE/checksums, compatibility report, three producer reports, and the
  aggregate Standard Code release gate. `msix-manifest.json` hashes this exact inventory and the
  verifier reruns the aggregate gate before accepting a real Store submission candidate.

The public GitHub Release contains the direct `TraverseBoard.exe` and provenance sidecars. It
does not publish the internal ZIP, development MSIX, Store upload, or a command launcher as an
additional user product.

Microsoft Store candidates must remain unsigned by the repository: Microsoft re-signs accepted
packages after certification. A repository PFX is neither required nor accepted by the Store
mode. Do not sideload the unsigned Store-identity candidate; use the separate Development mode
for local signing/lifecycle tests. See Microsoft's
[Store signing guidance](https://learn.microsoft.com/en-us/windows/msix/package/sign-msix-package-guide#production-microsoft-store-distribution).

## 3. Inspect before upload

Place the downloaded files together under `build/desktop`, then run:

```powershell
./scripts/verify-msix.ps1 -Action inspect
```

The verifier fails if the upload or MSIX hash drifts; if Name, Publisher,
PublisherDisplayName, version, architecture, `runFullTrust`, or the embedded EXE differs from
the evidence; if the EXE differs from the direct download; or if a CMD launcher is present.

Upload the `.msixupload` through Partner Center. The archive root contains exactly the bound
MSIX. Do not rename a portable ZIP to `.msixupload`.

## 4. Restricted capability statement

Use this statement when Partner Center asks why the package declares `runFullTrust`:

> Traverse Board is a packaged classic Win32 local development workbench. runFullTrust launches
> the primary desktop process and, only after the user explicitly selects and trusts a workspace
> and approves the relevant permission, invokes local developer tools. It runs as the current
> user, requests no administrator elevation, installs no service or driver, does not modify
> system security policy, and does not declare allowElevation.

`runFullTrust` is a restricted capability and its approval is decided by certification. See
[App capability declarations](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/app-capability-declarations#restricted-capabilities).

## 5. Completion evidence

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

An Actions success, a generated `.msixupload`, or a locally unpacked package is not Store
certification evidence.
