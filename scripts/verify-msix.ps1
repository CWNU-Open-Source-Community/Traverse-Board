[CmdletBinding()]
param(
    [string]$MsixPath = "build/desktop/PrayuDesktop.msix",
    [string]$ManifestPath = "build/desktop/msix-manifest.json",
    [ValidateSet("inspect", "install", "uninstall", "verify")]
    [string]$Action = "inspect",
    [switch]$AllowUnsigned,
    [switch]$AllowLegacyV1
)

<#
.SYNOPSIS
Inspects or exercises the install lifecycle of a generated MSIX.

.DESCRIPTION
The package identity is read from msix-manifest.json and cross-checked against
the embedded AppxManifest.xml instead of assuming the historical PrayuDesktop
name. inspect performs no installation and is suitable for an unsigned Store
submission candidate. install and verify require a valid signature unless the
package intentionally uses the Windows 11 unsigned-development OID identity;
that identity must never be used for a Store candidate.

The formal path requires msix_manifest.v2. Historical v1 evidence is available
only through explicit -AllowLegacyV1 -Action inspect and never authorizes an
install, uninstall, or lifecycle verification operation.
#>

$ErrorActionPreference = "Stop"
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "MSIX verification requires Windows"
}
$Action = $Action.ToLowerInvariant()

function Get-SHA256 {
    param([string]$Path)
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Get-ZipEntrySHA256 {
    param([System.IO.Compression.ZipArchiveEntry]$Entry)
    $stream = $Entry.Open()
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([System.BitConverter]::ToString($sha.ComputeHash($stream))).Replace(
            "-", "").ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
        $stream.Dispose()
    }
}

function Get-PEProcessorArchitecture {
    param([string]$Path)
    $stream = [System.IO.File]::Open(
        $Path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read,
        [System.IO.FileShare]::Read)
    $reader = [System.IO.BinaryReader]::new($stream)
    try {
        if ($stream.Length -lt 64 -or $reader.ReadUInt16() -ne 0x5a4d) {
            throw "Direct executable is not a valid PE file"
        }
        $stream.Position = 0x3c
        $peOffset = [uint32]$reader.ReadUInt32()
        if ([uint64]$peOffset + 6 -gt [uint64]$stream.Length) {
            throw "Direct executable has an invalid PE header offset"
        }
        $stream.Position = $peOffset
        if ($reader.ReadUInt32() -ne 0x00004550) {
            throw "Direct executable has an invalid PE signature"
        }
        switch ($reader.ReadUInt16()) {
            0x8664 { return "x64" }
            0xaa64 { return "arm64" }
            default { throw "Direct executable uses an unsupported PE machine type" }
        }
    }
    finally {
        $reader.Dispose()
        $stream.Dispose()
    }
}

$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$manifestFile = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $ManifestPath))
$repositoryPrefix = $repositoryRoot.TrimEnd('\', '/') +
    [System.IO.Path]::DirectorySeparatorChar
if (-not $manifestFile.StartsWith($repositoryPrefix,
        [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "MSIX evidence manifest must remain inside the repository"
}
if (-not (Test-Path -LiteralPath $manifestFile -PathType Leaf)) {
    throw "MSIX evidence manifest is missing: $manifestFile"
}
$evidence = Get-Content -LiteralPath $manifestFile -Raw | ConvertFrom-Json
if ([string]$evidence.protocol_version -cnotin @("msix_manifest.v1", "msix_manifest.v2")) {
    throw "MSIX evidence manifest protocol is unsupported"
}
if ([string]$evidence.protocol_version -ceq "msix_manifest.v1" -and
    (-not $AllowLegacyV1 -or $Action -cne "inspect")) {
    throw "Legacy msix_manifest.v1 is read-only and requires -AllowLegacyV1 -Action inspect"
}
if ([string]$evidence.protocol_version -ceq "msix_manifest.v2") {
    foreach ($booleanField in @(
            "binary_matches_direct_download", "signed", "contract_test",
            "store_submission_candidate", "release_gate_complete",
            "microsoft_store_resigning_expected", "symbols_included",
            "restricted_capability_justification_required", "lifecycle_validation_required"
        )) {
        $property = $evidence.PSObject.Properties[$booleanField]
        if ($null -eq $property -or $property.Value -isnot [bool]) {
            throw "MSIX evidence field must be a JSON boolean: $booleanField"
        }
    }
}

$manifestDirectory = Split-Path -Parent $manifestFile
$declaredMsixName = [string]$evidence.msix_name
if ([string]::IsNullOrWhiteSpace($declaredMsixName) -or
    [System.IO.Path]::GetFileName($declaredMsixName) -cne $declaredMsixName) {
    throw "MSIX evidence contains an unsafe package filename"
}
$expectedMsix = [System.IO.Path]::GetFullPath((Join-Path $manifestDirectory $declaredMsixName))
$msix = if (-not $PSBoundParameters.ContainsKey("MsixPath")) {
    $expectedMsix
}
else {
    [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $MsixPath))
}
if (-not $msix.Equals($expectedMsix, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Selected MSIX path does not match msix-manifest.json"
}
$packageName = if ([string]$evidence.protocol_version -ceq "msix_manifest.v2") {
    [string]$evidence.package_identity_name
}
else {
    "PrayuDesktop"
}
if ([string]::IsNullOrWhiteSpace($packageName)) {
    throw "MSIX package identity is missing from its evidence"
}
if ($packageName -notmatch '^[A-Za-z0-9.-]{3,50}$') {
    throw "MSIX package identity Name contains unsafe characters"
}

$packageVersion = [string]$evidence.version
$packagePublisher = if ([string]$evidence.protocol_version -ceq "msix_manifest.v2") {
    [string]$evidence.package_publisher
}
else {
    ""
}
$storeCandidate = if ([string]$evidence.protocol_version -ceq "msix_manifest.v2") {
    $evidence.store_submission_candidate
}
else {
    $false
}
$storeContractTest = if ([string]$evidence.protocol_version -ceq "msix_manifest.v2") {
    $evidence.contract_test
}
else {
    $false
}
if ([string]$evidence.protocol_version -ceq "msix_manifest.v2") {
    $restrictedCapabilities = @($evidence.restricted_capabilities | ForEach-Object {
            [string]$_
        })
    if ([string]$evidence.processor_architecture -notin @("x64", "arm64") -or
        [string]$evidence.binary_name -cne "TraverseBoard.exe" -or
        [string]$evidence.installer_kind -cne "per_user_msix" -or
        [string]$evidence.data_home_contract -cne "external_user_data_home" -or
        [string]$evidence.lifecycle_validation_status -cne "not_run" -or
        -not [string]::IsNullOrEmpty([string]$evidence.lifecycle_evidence_sha256) -or
        $evidence.symbols_included -or
        $restrictedCapabilities.Count -ne 1 -or
        $restrictedCapabilities[0] -cne "runFullTrust" -or
        ($storeCandidate -and $storeContractTest)) {
        throw "MSIX evidence architecture, capability, or lifecycle contract is invalid"
    }
    if (-not $storeCandidate -and -not $storeContractTest -and
        ([string]$evidence.distribution -cne "development" -or
            $evidence.release_gate_complete -or
            $evidence.lifecycle_validation_required -or
            $evidence.microsoft_store_resigning_expected -or
            $evidence.restricted_capability_justification_required -or
            @($evidence.release_evidence).Count -ne 0 -or
            -not [string]::IsNullOrEmpty([string]$evidence.msixupload_name) -or
            -not [string]::IsNullOrEmpty([string]$evidence.msixupload_sha256))) {
        throw "Development MSIX evidence claims Store-only state"
    }
}

function Inspect-Package {
    if (-not (Test-Path -LiteralPath $msix -PathType Leaf)) {
        throw "MSIX is missing: $msix"
    }
    $msixHash = Get-SHA256 $msix
    if ([string]$evidence.msix_sha256 -cne $msixHash) {
        throw "MSIX hash differs from msix-manifest.json"
    }
    if ([string]$evidence.protocol_version -ceq "msix_manifest.v2") {
        $actualSignature = Get-AuthenticodeSignature -LiteralPath $msix
        if (($evidence.signed -and
                ($actualSignature.Status -ne [System.Management.Automation.SignatureStatus]::Valid -or
                    [string]$evidence.signature_status -cne "Valid")) -or
            (-not $evidence.signed -and
                ($actualSignature.Status -ne [System.Management.Automation.SignatureStatus]::NotSigned -or
                    [string]$evidence.signature_status -cne "NotSigned"))) {
            throw "MSIX signature state differs from msix-manifest.json"
        }
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::OpenRead($msix)
    try {
        $manifestEntries = @($archive.Entries | Where-Object {
                [string]$_.FullName -ceq "AppxManifest.xml"
            })
        $binaryEntries = @($archive.Entries | Where-Object {
                [string]$_.FullName -ceq "TraverseBoard.exe"
            })
        $executableEntries = @($archive.Entries | Where-Object {
                [System.IO.Path]::GetExtension([string]$_.FullName) -ieq ".exe"
            })
        $launchers = @($archive.Entries | Where-Object {
                [System.IO.Path]::GetExtension([string]$_.FullName) -ieq ".cmd"
            })
        if ($manifestEntries.Count -ne 1 -or $binaryEntries.Count -ne 1 -or
            $executableEntries.Count -ne 1 -or
            $launchers.Count -ne 0) {
            throw "MSIX must contain one manifest, only TraverseBoard.exe, and no launcher scripts"
        }
        $manifestStream = $manifestEntries[0].Open()
        $reader = [System.IO.StreamReader]::new(
            $manifestStream, [System.Text.Encoding]::UTF8, $true)
        try {
            [xml]$embeddedManifest = $reader.ReadToEnd()
        }
        finally {
            $reader.Dispose()
            $manifestStream.Dispose()
        }
        $namespace = [System.Xml.XmlNamespaceManager]::new($embeddedManifest.NameTable)
        $namespace.AddNamespace(
            "f", "http://schemas.microsoft.com/appx/manifest/foundation/windows10")
        $namespace.AddNamespace(
            "rescap", "http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities")
        $namespace.AddNamespace(
            "desktop", "http://schemas.microsoft.com/appx/manifest/desktop/windows10")
        $identity = $embeddedManifest.SelectSingleNode("/f:Package/f:Identity", $namespace)
        $publisherDisplay = $embeddedManifest.SelectSingleNode(
            "/f:Package/f:Properties/f:PublisherDisplayName", $namespace)
        $applications = @($embeddedManifest.SelectNodes(
                "/f:Package/f:Applications/f:Application", $namespace))
        $application = if ($applications.Count -eq 1) { $applications[0] } else { $null }
        $extensions = @($embeddedManifest.SelectNodes(
                "/f:Package/f:Applications/f:Application/f:Extensions/*", $namespace))
        $capabilities = @($embeddedManifest.SelectNodes(
                "/f:Package/f:Capabilities/*", $namespace))
        $applicationChildren = @($application.ChildNodes | Where-Object {
                $_.NodeType -eq [System.Xml.XmlNodeType]::Element
            })
        $visualElements = @($applicationChildren | Where-Object {
                [string]$_.LocalName -ceq "VisualElements" -and
                [string]$_.NamespaceURI -ceq
                    "http://schemas.microsoft.com/appx/manifest/uap/windows10"
            })
        $extensionContainers = @($applicationChildren | Where-Object {
                [string]$_.LocalName -ceq "Extensions" -and
                [string]$_.NamespaceURI -ceq
                    "http://schemas.microsoft.com/appx/manifest/foundation/windows10"
            })
        $expectedIdentityAttributeCount = if (
            [string]$evidence.protocol_version -ceq "msix_manifest.v2") { 4 } else { 3 }
        if ($null -eq $identity -or
            $identity.Attributes.Count -ne $expectedIdentityAttributeCount -or
            $identity.GetAttribute("Name") -cne $packageName -or
            $identity.GetAttribute("Version") -cne $packageVersion -or
            $null -eq $application -or
            $application.Attributes.Count -ne 3 -or
            $application.GetAttribute("Id") -cne "Prayu" -or
            $application.GetAttribute("Executable") -cne "TraverseBoard.exe" -or
            $application.GetAttribute("EntryPoint") -cne "Windows.FullTrustApplication" -or
            $applicationChildren.Count -ne 2 -or
            $visualElements.Count -ne 1 -or
            $extensionContainers.Count -ne 1 -or
            $extensions.Count -ne 1 -or
            $extensions[0].Attributes.Count -ne 2 -or
            $extensions[0].HasChildNodes -or
            [string]$extensions[0].NamespaceURI -cne
                "http://schemas.microsoft.com/appx/manifest/desktop/windows10" -or
            $extensions[0].GetAttribute("Category") -cne "windows.fullTrustProcess" -or
            $extensions[0].GetAttribute("Executable") -cne "TraverseBoard.exe" -or
            $capabilities.Count -ne 1 -or
            $capabilities[0].Attributes.Count -ne 1 -or
            $capabilities[0].HasChildNodes -or
            [string]$capabilities[0].NamespaceURI -cne
                "http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities" -or
            $capabilities[0].GetAttribute("Name") -cne "runFullTrust") {
            throw "Embedded MSIX identity differs from msix-manifest.json"
        }
        if ([string]$evidence.protocol_version -ceq "msix_manifest.v1") {
            $script:packagePublisher = [string]$identity.GetAttribute("Publisher")
        }
        if ([string]$evidence.protocol_version -ceq "msix_manifest.v2") {
            if ($identity.GetAttribute("Publisher") -cne $packagePublisher -or
                $identity.GetAttribute("ProcessorArchitecture") -cne
                    [string]$evidence.processor_architecture -or
                $null -eq $publisherDisplay -or
                [string]$publisherDisplay.InnerText -cne
                    [string]$evidence.publisher_display_name -or
                (Get-ZipEntrySHA256 $binaryEntries[0]) -cne [string]$evidence.binary_sha256 -or
                -not $evidence.binary_matches_direct_download) {
                throw "Embedded MSIX publisher, architecture, or EXE binding differs"
            }
            $directBinary = Join-Path $manifestDirectory "TraverseBoard.exe"
            if (-not (Test-Path -LiteralPath $directBinary -PathType Leaf) -or
                (Get-SHA256 $directBinary) -cne [string]$evidence.binary_sha256) {
                throw "MSIX payload no longer matches the direct-download TraverseBoard.exe"
            }
            if ((Get-PEProcessorArchitecture $directBinary) -cne
                [string]$evidence.processor_architecture) {
                throw "MSIX architecture differs from the direct executable PE machine type"
            }
            $releaseMetadataPath = Join-Path $manifestDirectory "release-metadata.json"
            if (-not (Test-Path -LiteralPath $releaseMetadataPath -PathType Leaf) -or
                (Get-SHA256 $releaseMetadataPath) -cne
                    [string]$evidence.release_metadata_sha256) {
                throw "MSIX release metadata file differs from its evidence"
            }
            $releaseMetadata = Get-Content -LiteralPath $releaseMetadataPath -Raw |
                ConvertFrom-Json
            foreach ($booleanField in @(
                    "modified", "reproducibility_checked", "reproducible"
                )) {
                $property = $releaseMetadata.PSObject.Properties[$booleanField]
                if ($null -eq $property -or $property.Value -isnot [bool]) {
                    throw "Release metadata field must be a JSON boolean: $booleanField"
                }
            }
            $expectedGoArch = if ([string]$evidence.processor_architecture -ceq "x64") {
                "amd64"
            }
            else {
                "arm64"
            }
            if ([string]$evidence.marketing_version -notmatch
                    '^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$' -or
                [string]$evidence.release_revision -notmatch '^[0-9a-f]{40}$' -or
                [string]$releaseMetadata.protocol_version -cne
                    "portable_release_metadata.v1" -or
                [string]$releaseMetadata.app_version -cne
                    [string]$evidence.marketing_version -or
                [string]$releaseMetadata.revision -cne
                    [string]$evidence.release_revision -or
                [string]$releaseMetadata.binary_name -cne "TraverseBoard.exe" -or
                [string]$releaseMetadata.sha256 -cne [string]$evidence.binary_sha256 -or
                [string]$releaseMetadata.target_os -cne "windows" -or
                [string]$releaseMetadata.target_arch -cne $expectedGoArch) {
                throw "MSIX marketing version or release provenance differs from release metadata"
            }
            $justificationDocument = [string]$evidence.restricted_capability_justification_document
            if ($justificationDocument -cne "packaging/windows/STORE-SUBMISSION.md" -or
                [string]::IsNullOrWhiteSpace(
                    [string]$evidence.restricted_capability_justification)) {
                throw "MSIX evidence is missing the runFullTrust submission justification"
            }
            $justificationPath = Join-Path $repositoryRoot $justificationDocument
            if (-not (Test-Path -LiteralPath $justificationPath -PathType Leaf) -or
                (Get-SHA256 $justificationPath) -cne
                    [string]$evidence.restricted_capability_justification_document_sha256) {
                throw "MSIX runFullTrust justification document differs from its evidence"
            }
            if ($storeCandidate -or $storeContractTest) {
                $versionParts = @($packageVersion.Split('.'))
                $expectedDistribution = if ($storeCandidate) {
                    "microsoft_store"
                }
                else {
                    "microsoft_store_contract_test"
                }
                if ([string]$evidence.distribution -cne $expectedDistribution -or
                    $evidence.signed -or
                    $packageName -ieq "PrayuDesktop" -or $packagePublisher -ieq "CN=Prayu" -or
                    $versionParts.Count -ne 4 -or $versionParts[0] -notmatch '^[1-9][0-9]*$' -or
                    $versionParts[1] -notmatch '^[0-9]+$' -or
                    $versionParts[2] -notmatch '^[0-9]+$' -or
                    $versionParts[3] -cne "0") {
                    throw "Store MSIX evidence does not satisfy the Partner Center-bound contract"
                }
                if ($storeCandidate -and
                    (-not $evidence.microsoft_store_resigning_expected -or
                        -not $evidence.restricted_capability_justification_required -or
                        -not $evidence.release_gate_complete -or
                        -not $evidence.lifecycle_validation_required)) {
                    throw "Store submission candidate is missing signing, gate, capability, or lifecycle requirements"
                }
                if ($storeContractTest -and
                    ($evidence.microsoft_store_resigning_expected -or
                        $evidence.restricted_capability_justification_required -or
                        $evidence.release_gate_complete -or
                        $evidence.lifecycle_validation_required -or
                        @($evidence.release_evidence).Count -ne 0)) {
                    throw "Synthetic Store contract evidence claims real submission authority"
                }
                if ($releaseMetadata.modified -or
                    -not $releaseMetadata.reproducibility_checked -or
                    -not $releaseMetadata.reproducible) {
                    throw "Store MSIX release metadata is not clean and reproducible"
                }
                foreach ($part in $versionParts) {
                    if ([uint64]$part -gt 65535) {
                        throw "Store MSIX version component is too large"
                    }
                }
                $uploadName = [string]$evidence.msixupload_name
                if ([string]::IsNullOrWhiteSpace($uploadName) -or
                    [System.IO.Path]::GetFileName($uploadName) -cne $uploadName) {
                    throw "Store MSIX upload filename is unsafe"
                }
                $uploadPath = Join-Path $manifestDirectory $uploadName
                if (-not (Test-Path -LiteralPath $uploadPath -PathType Leaf) -or
                    (Get-SHA256 $uploadPath) -cne [string]$evidence.msixupload_sha256) {
                    throw "Store MSIX upload differs from msix-manifest.json"
                }
                $uploadArchive = [System.IO.Compression.ZipFile]::OpenRead($uploadPath)
                try {
                    $uploadEntries = @($uploadArchive.Entries)
                    if ($uploadEntries.Count -ne 1 -or
                        [string]$uploadEntries[0].FullName -cne $declaredMsixName -or
                        (Get-ZipEntrySHA256 $uploadEntries[0]) -cne $msixHash) {
                        throw "Store upload must contain exactly the bound MSIX at its root"
                    }
                }
                finally {
                    $uploadArchive.Dispose()
                }
                if ($storeCandidate) {
                    $portableManifestPath = Join-Path $manifestDirectory "portable-zip-manifest.json"
                    if (-not (Test-Path -LiteralPath $portableManifestPath -PathType Leaf)) {
                        throw "Store release evidence is missing its portable manifest"
                    }
                    $portableManifest = Get-Content -LiteralPath $portableManifestPath -Raw |
                        ConvertFrom-Json
                    $portableZipName = [string]$portableManifest.zip_name
                    if ([string]::IsNullOrWhiteSpace($portableZipName) -or
                        [System.IO.Path]::GetFileName($portableZipName) -cne $portableZipName) {
                        throw "Store release evidence contains an unsafe portable archive name"
                    }
                    $expectedEvidenceNames = @(
                        $portableZipName,
                        "portable-zip-manifest.json",
                        "standard-code-packaged-e2e.json",
                        "standard-code-product-e2e.json",
                        "standard-code-security-evidence.json",
                        "standard-code-release-gate.json",
                        "sbom.json",
                        "NOTICE",
                        "windows-compatibility.json",
                        "SHA256SUMS",
                        "verification-SHA256SUMS"
                    )
                    $boundEvidence = @($evidence.release_evidence)
                    if ($boundEvidence.Count -ne $expectedEvidenceNames.Count) {
                        throw "Store release evidence inventory is incomplete"
                    }
                    for ($index = 0; $index -lt $expectedEvidenceNames.Count; $index++) {
                        $name = [string]$boundEvidence[$index].name
                        if ($name -cne $expectedEvidenceNames[$index] -or
                            [System.IO.Path]::GetFileName($name) -cne $name) {
                            throw "Store release evidence inventory differs at entry $index"
                        }
                        $evidencePath = Join-Path $manifestDirectory $name
                        if (-not (Test-Path -LiteralPath $evidencePath -PathType Leaf) -or
                            (Get-SHA256 $evidencePath) -cne
                                [string]$boundEvidence[$index].sha256) {
                            throw "Store release evidence file differs: $name"
                        }
                    }
                    & go -C $repositoryRoot run ./cmd/releasegate `
                        --binary $directBinary `
                        --archive (Join-Path $manifestDirectory $portableZipName) `
                        --portable-manifest $portableManifestPath `
                        --release-metadata $releaseMetadataPath `
                        --bootstrap (Join-Path $manifestDirectory "standard-code-packaged-e2e.json") `
                        --product (Join-Path $manifestDirectory "standard-code-product-e2e.json") `
                        --security (Join-Path $manifestDirectory "standard-code-security-evidence.json") `
                        --expected-revision ([string]$evidence.release_revision) `
                        --verify-report (Join-Path $manifestDirectory "standard-code-release-gate.json")
                    if ($LASTEXITCODE -ne 0) {
                        throw "Store Standard Code release evidence revalidation failed"
                    }
                }
            }
        }
    }
    finally {
        $archive.Dispose()
    }
    Write-Output "msix_inspect: pass"
    Write-Output "msix_package_name: $packageName"
    Write-Output "msix_version: $packageVersion"
}

function Assert-InstallPolicy {
    $signature = Get-AuthenticodeSignature -LiteralPath $msix
    if ($signature.Status -eq [System.Management.Automation.SignatureStatus]::Valid) {
        return
    }
    if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::NotSigned) {
        throw "MSIX contains an invalid or untrusted signature"
    }
    if (-not $AllowUnsigned) {
        throw "MSIX install verification requires a valid signature"
    }
    if ($storeCandidate) {
        throw "An unsigned Store-identity package cannot be installed as an unsigned test package"
    }
    if ($packagePublisher -notmatch '(?i)(^|,\s*)OID\.2\.25\.[0-9]+=1($|,\s*)') {
        throw "AllowUnsigned requires a dedicated Windows 11 development identity with the unsigned OID"
    }
}

function Invoke-AppxInstall {
    Assert-InstallPolicy
    if ($AllowUnsigned) {
        Add-AppxPackage -Path $msix -AllowUnsigned
    }
    else {
        Add-AppxPackage -Path $msix
    }
}

function Get-ExactInstalledPackage {
    if ([string]::IsNullOrWhiteSpace($packagePublisher)) {
        throw "Installed-package lookup requires the manifest-bound Publisher"
    }
    $matches = @(Get-AppxPackage -Name $packageName -ErrorAction SilentlyContinue |
        Where-Object {
            [string]$_.Name -ceq $packageName -and
            [string]$_.Publisher -ceq $packagePublisher
        })
    if ($matches.Count -gt 1) {
        throw "More than one installed package matched the exact Name and Publisher"
    }
    if ($matches.Count -eq 1) { return $matches[0] }
    return $null
}

switch ($Action) {
    "inspect" {
        Inspect-Package
    }
    "install" {
        Inspect-Package
        Invoke-AppxInstall
        Write-Output "msix_installed: true"
    }
    "uninstall" {
        Inspect-Package
        $package = Get-ExactInstalledPackage
        if ($null -eq $package) {
            Write-Output "msix_uninstalled: true (not installed)"
        }
        else {
            Remove-AppxPackage -Package $package.PackageFullName
            Write-Output "msix_uninstalled: true (user data left in place)"
        }
    }
    "verify" {
        Inspect-Package
        Invoke-AppxInstall
        $package = Get-ExactInstalledPackage
        if ($null -eq $package -or [string]$package.Version -cne $packageVersion) {
            throw "MSIX did not register with the expected identity and version"
        }
        Write-Output "msix_verify: pass"
        Write-Output "msix_package_full_name: $($package.PackageFullName)"
        Write-Output "msix_version: $($package.Version)"
    }
}
