[CmdletBinding()]
param(
    [string]$OutputDirectory = "build/desktop",
    [string]$ExpectedVersion = "",
    [string]$ExpectedRevision = "",
    [string]$ExpectedDirectSignerSubject = "",
    [string]$ExpectedDirectSignerThumbprint = "",
    [switch]$SmokeTest
)

<#
.SYNOPSIS
Verifies the portable ZIP, companion checksums, manifest, provenance, and
optionally starts the extracted desktop executable.

.DESCRIPTION
Fails closed on stale metadata, unexpected or duplicate ZIP entries, unsafe
paths, per-entry hash/size drift, checksum drift, missing toolchain provenance,
incomplete bundled-asset notices, or a startup smoke failure. Verification only
reads checked-in release-policy inputs outside the repository output directory.
#>

$ErrorActionPreference = "Stop"
$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$outputRoot = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $OutputDirectory))
$repositoryPrefix = $repositoryRoot.TrimEnd('\', '/') + [System.IO.Path]::DirectorySeparatorChar
if (-not $outputRoot.StartsWith($repositoryPrefix,
        [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Portable verification output must remain inside the repository"
}

function Assert-ReleaseCondition {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

function Get-SHA256 {
    param([string]$Path)
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function ConvertTo-LFText {
    param([string]$Value)
    $normalized = $Value.Replace("`r`n", "`n").Replace("`r", "`n")
    return $normalized.TrimEnd([char[]]"`r`n") + "`n"
}

function Assert-JSONBooleanFields {
    param(
        [object]$Value,
        [string[]]$Names,
        [string]$Label
    )
    foreach ($name in $Names) {
        $property = $Value.PSObject.Properties[$name]
        Assert-ReleaseCondition ($null -ne $property -and $property.Value -is [bool]) `
            "$Label field must be a JSON boolean: $name"
    }
}

$manifestPath = Join-Path $outputRoot "portable-zip-manifest.json"
$metadataPath = Join-Path $outputRoot "release-metadata.json"
$sumsPath = Join-Path $outputRoot "verification-SHA256SUMS"
$publicSumsPath = Join-Path $outputRoot "SHA256SUMS"
$binaryPath = Join-Path $outputRoot "TraverseBoard.exe"
$directBinaryPath = Join-Path $outputRoot "TraverseBoard.direct.exe"
$directSigningEvidencePath = Join-Path $outputRoot "direct-exe-signing.json"
$sbomPath = Join-Path $outputRoot "sbom.json"
$noticePath = Join-Path $outputRoot "NOTICE"
$fontUseNoticePath = Join-Path $repositoryRoot "web/public/THIRD-PARTY-NOTICES.txt"
$fontLicensePath = Join-Path $repositoryRoot "web/public/licenses/HarmonyOS-Sans.txt"
foreach ($required in @($manifestPath, $metadataPath, $sumsPath, $publicSumsPath,
        $binaryPath, $sbomPath, $noticePath, $fontUseNoticePath, $fontLicensePath)) {
    Assert-ReleaseCondition (Test-Path -LiteralPath $required -PathType Leaf) `
        "Portable verification input is missing: $required"
}

$manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
$metadata = Get-Content -LiteralPath $metadataPath -Raw | ConvertFrom-Json
$sbom = Get-Content -LiteralPath $sbomPath -Raw | ConvertFrom-Json
Assert-JSONBooleanFields -Value $manifest -Names @(
    "zip_reproducibility_checked", "zip_timestamps_reproducible"
) -Label "Portable manifest"
Assert-JSONBooleanFields -Value $metadata -Names @(
    "modified", "reproducibility_checked", "reproducible", "trimpath",
    "operator_preview_included", "installer_included", "registry_writes",
    "startup_task", "auto_update_enabled", "manual_windows_10_matrix_required"
) -Label "Release metadata"

Assert-ReleaseCondition ($manifest.protocol_version -eq "portable_zip_manifest.v1") `
    "Portable manifest protocol is unsupported"
Assert-ReleaseCondition ($metadata.protocol_version -eq "portable_release_metadata.v1") `
    "Release metadata protocol is unsupported"
Assert-ReleaseCondition ($sbom.bomFormat -eq "CycloneDX" -and $sbom.specVersion -eq "1.4") `
    "CycloneDX SBOM metadata is invalid"
Assert-ReleaseCondition ($manifest.version -eq $metadata.app_version) `
    "Portable manifest and release metadata versions differ"
Assert-ReleaseCondition ($manifest.revision -eq $metadata.revision) `
    "Portable manifest and release metadata revisions differ"
Assert-ReleaseCondition ($manifest.zip_reproducibility_checked -and
    $manifest.zip_timestamps_reproducible) `
    "Portable ZIP reproducibility was not checked"
if (-not [string]::IsNullOrWhiteSpace($ExpectedVersion)) {
    Assert-ReleaseCondition ($manifest.version -ceq $ExpectedVersion) `
        "Portable release version does not match the expected version"
}
if (-not [string]::IsNullOrWhiteSpace($ExpectedRevision)) {
    Assert-ReleaseCondition ($metadata.revision -ceq $ExpectedRevision) `
        "Portable release revision does not match the expected revision"
}
Assert-ReleaseCondition ($metadata.revision -match '^[0-9a-f]{40}$' -and
    -not $metadata.modified -and $metadata.reproducibility_checked -and
    $metadata.reproducible) "Release metadata is not clean and reproducible"
Assert-ReleaseCondition (-not $metadata.operator_preview_included -and
    [string]::IsNullOrEmpty([string]$metadata.operator_preview_launcher_name) -and
    [string]::IsNullOrEmpty([string]$metadata.operator_preview_launcher_sha256)) `
    "Windows release metadata still requires an operator-preview launcher"
Assert-ReleaseCondition ($metadata.go_version -match '^go[0-9]+\.[0-9]+' -and
    $metadata.node_version -match '^v[0-9]+\.[0-9]+\.[0-9]+' -and
    $metadata.npm_version -match '^[0-9]+\.[0-9]+\.[0-9]+' -and
    $metadata.rust_version -match '^[0-9]+\.[0-9]+\.[0-9]+' -and
    $metadata.go_sum_sha256 -match '^[0-9a-f]{64}$' -and
    $metadata.node_lock_sha256 -match '^[0-9a-f]{64}$' -and
    $metadata.cargo_lock_sha256 -match '^[0-9a-f]{64}$' -and
    $metadata.embedded_analyzer_sha256 -match '^[0-9a-f]{64}$') `
    "Release toolchain or lockfile provenance is incomplete"

$zipName = [string]$manifest.zip_name
Assert-ReleaseCondition (-not [string]::IsNullOrWhiteSpace($zipName) -and
    [System.IO.Path]::GetFileName($zipName) -ceq $zipName) "Portable ZIP name is unsafe"
$zipPath = Join-Path $outputRoot $zipName
Assert-ReleaseCondition (Test-Path -LiteralPath $zipPath -PathType Leaf) `
    "Portable ZIP is missing: $zipName"

$allowedContents = @(
    "TraverseBoard.exe", "LOCAL-TEST-GUIDE.txt", "LICENSE", "README.md", "NOTICE",
    "sbom.json", "release-metadata.json"
)
$contents = @($manifest.contents | ForEach-Object { [string]$_ })
$entries = @($manifest.entries)
Assert-ReleaseCondition ($contents.Count -eq $allowedContents.Count -and
    $entries.Count -eq $allowedContents.Count) "Portable manifest entry count is invalid"
for ($index = 0; $index -lt $allowedContents.Count; $index++) {
    Assert-ReleaseCondition ($contents[$index] -ceq $allowedContents[$index]) `
        "Portable manifest allowlist differs at entry $index"
    Assert-ReleaseCondition ([string]$entries[$index].name -ceq $allowedContents[$index]) `
        "Portable per-entry manifest differs at entry $index"
}

Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [System.IO.Compression.ZipFile]::OpenRead($zipPath)
try {
    $archiveEntries = @($archive.Entries)
    Assert-ReleaseCondition ($archiveEntries.Count -eq $allowedContents.Count) `
        "Portable ZIP contains an unexpected number of entries"
    $seen = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    $actual = [System.Collections.Generic.Dictionary[string, object]]::new(
        [System.StringComparer]::Ordinal)
    foreach ($entry in $archiveEntries) {
        $name = [string]$entry.FullName
        Assert-ReleaseCondition (-not [string]::IsNullOrWhiteSpace($name) -and
            [System.IO.Path]::GetFileName($name) -ceq $name -and
            -not $name.Contains('\') -and -not $name.Contains(':')) `
            "Portable ZIP contains an unsafe entry: $name"
        Assert-ReleaseCondition ($seen.Add($name)) "Portable ZIP contains a duplicate entry: $name"

        $stream = $entry.Open()
        $sha = [System.Security.Cryptography.SHA256]::Create()
        try {
            $digest = ([System.BitConverter]::ToString($sha.ComputeHash($stream))).Replace("-", "").ToLowerInvariant()
        }
        finally {
            $sha.Dispose()
            $stream.Dispose()
        }
        $actual.Add($name, [pscustomobject]@{ size = [int64]$entry.Length; sha256 = $digest })
    }
    foreach ($expectedEntry in $entries) {
        $name = [string]$expectedEntry.name
        Assert-ReleaseCondition ($actual.ContainsKey($name)) "Portable ZIP entry is missing: $name"
        Assert-ReleaseCondition ($actual[$name].size -eq [int64]$expectedEntry.size -and
            $actual[$name].sha256 -ceq [string]$expectedEntry.sha256) `
            "Portable ZIP entry hash or size differs: $name"
    }
}
finally {
    $archive.Dispose()
}

$binaryHash = Get-SHA256 $binaryPath
$zipHash = Get-SHA256 $zipPath
$sbomHash = Get-SHA256 $sbomPath
$noticeHash = Get-SHA256 $noticePath
$metadataHash = Get-SHA256 $metadataPath
$manifestHash = Get-SHA256 $manifestPath
Assert-ReleaseCondition ($manifest.zip_sha256 -ceq $zipHash -and
    $manifest.binary_sha256 -ceq $binaryHash -and
    $manifest.sbom_sha256 -ceq $sbomHash -and
    $manifest.notice_sha256 -ceq $noticeHash -and
    $metadata.sha256 -ceq $binaryHash) "Portable top-level hashes differ"
Assert-ReleaseCondition ($actual["TraverseBoard.exe"].sha256 -ceq $binaryHash -and
    $actual["sbom.json"].sha256 -ceq $sbomHash -and
    $actual["NOTICE"].sha256 -ceq $noticeHash -and
    $actual["release-metadata.json"].sha256 -ceq $metadataHash) `
    "Portable ZIP and companion files differ"

$expectedSums = [System.Collections.Generic.Dictionary[string, string]]::new(
    [System.StringComparer]::Ordinal)
$expectedSums.Add("TraverseBoard.exe", $binaryHash)
$expectedSums.Add($zipName, $zipHash)
$expectedSums.Add("sbom.json", $sbomHash)
$expectedSums.Add("NOTICE", $noticeHash)
$expectedSums.Add("release-metadata.json", $metadataHash)
$expectedSums.Add("portable-zip-manifest.json", $manifestHash)
$sumLines = @([System.IO.File]::ReadAllLines($sumsPath) | Where-Object { $_.Length -ne 0 })
Assert-ReleaseCondition ($sumLines.Count -eq $expectedSums.Count) "SHA256SUMS entry count is invalid"
$seenSums = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
foreach ($line in $sumLines) {
    $match = [regex]::Match($line, '^(?<hash>[0-9a-f]{64})  (?<name>[^\\/:]+)$')
    Assert-ReleaseCondition $match.Success "SHA256SUMS contains an invalid line"
    $name = $match.Groups['name'].Value
    Assert-ReleaseCondition ($seenSums.Add($name)) "SHA256SUMS contains a duplicate entry: $name"
    Assert-ReleaseCondition ($expectedSums.ContainsKey($name) -and
        $expectedSums[$name] -ceq $match.Groups['hash'].Value) "SHA256SUMS differs for: $name"
}

$expectedPublicSums = [System.Collections.Generic.Dictionary[string, string]]::new(
    [System.StringComparer]::Ordinal)
$directBinaryPresent = Test-Path -LiteralPath $directBinaryPath -PathType Leaf
$directEvidencePresent = Test-Path -LiteralPath $directSigningEvidencePath -PathType Leaf
Assert-ReleaseCondition ($directBinaryPresent -eq $directEvidencePresent) `
    "Direct EXE and signing evidence must be staged together"
if ($directBinaryPresent) {
    & (Join-Path $PSScriptRoot "stage-direct-exe.ps1") `
        -OutputDirectory $OutputDirectory `
        -ExpectedVersion $ExpectedVersion `
        -ExpectedRevision $ExpectedRevision `
        -ExpectedSignerSubject $ExpectedDirectSignerSubject `
        -ExpectedSignerThumbprint $ExpectedDirectSignerThumbprint `
        -VerifyOnly
    $expectedPublicSums.Add("TraverseBoard.exe", (Get-SHA256 $directBinaryPath))
    $expectedPublicSums.Add("direct-exe-signing.json", (Get-SHA256 $directSigningEvidencePath))
    $directSigningEvidence = Get-Content -LiteralPath $directSigningEvidencePath -Raw |
        ConvertFrom-Json
    if ($directSigningEvidence.trusted_release) {
        $directSigningRequestPath = Join-Path $outputRoot "direct-exe-signing-request.json"
        Assert-ReleaseCondition (Test-Path -LiteralPath $directSigningRequestPath -PathType Leaf) `
            "Trusted direct EXE signing request is missing"
        $expectedPublicSums.Add("direct-exe-signing-request.json",
            (Get-SHA256 $directSigningRequestPath))
        $directSigningHandoffPath = Join-Path $outputRoot "direct-exe-signing-handoff.json"
        Assert-ReleaseCondition (Test-Path -LiteralPath $directSigningHandoffPath -PathType Leaf) `
            "Trusted direct EXE signing handoff is missing"
        $expectedPublicSums.Add("direct-exe-signing-handoff.json",
            (Get-SHA256 $directSigningHandoffPath))
    }
}
else {
    $expectedPublicSums.Add("TraverseBoard.exe", $binaryHash)
}
$expectedPublicSums.Add("sbom.json", $sbomHash)
$expectedPublicSums.Add("NOTICE", $noticeHash)
$expectedPublicSums.Add("release-metadata.json", $metadataHash)
$publicSumLines = @([System.IO.File]::ReadAllLines($publicSumsPath) |
    Where-Object { $_.Length -ne 0 })
Assert-ReleaseCondition ($publicSumLines.Count -eq $expectedPublicSums.Count) "Public SHA256SUMS entry count is invalid"
$seenPublicSums = [System.Collections.Generic.HashSet[string]]::new(
    [System.StringComparer]::Ordinal)
foreach ($line in $publicSumLines) {
    $match = [regex]::Match($line, '^(?<hash>[0-9a-f]{64})  (?<name>[^\\/:]+)$')
    Assert-ReleaseCondition $match.Success "Public SHA256SUMS contains an invalid line"
    $name = $match.Groups['name'].Value
    Assert-ReleaseCondition ($seenPublicSums.Add($name)) "Public SHA256SUMS contains a duplicate entry: $name"
    Assert-ReleaseCondition ($expectedPublicSums.ContainsKey($name) -and
        [string]$expectedPublicSums[$name] -ceq $match.Groups['hash'].Value) "Public SHA256SUMS differs for: $name"
}

$notice = ConvertTo-LFText ([System.IO.File]::ReadAllText($noticePath))
Assert-ReleaseCondition (-not [regex]::IsMatch($notice, '(?m)^- .+ \(unknown\)$')) `
    "NOTICE contains a dependency without a recognized license"
$fontUseNotice = ConvertTo-LFText ([System.IO.File]::ReadAllText($fontUseNoticePath))
$fontLicense = ConvertTo-LFText ([System.IO.File]::ReadAllText($fontLicensePath))
Assert-ReleaseCondition ($notice.IndexOf($fontUseNotice,
        [System.StringComparison]::Ordinal) -ge 0) `
    "NOTICE does not contain the complete HarmonyOS Sans product-use statement"
Assert-ReleaseCondition ($notice.IndexOf($fontLicense,
        [System.StringComparison]::Ordinal) -ge 0) `
    "NOTICE does not contain the complete HarmonyOS Sans license agreement"

if ($SmokeTest) {
    $smokeRoot = Join-Path $outputRoot (".portable-smoke-" + [guid]::NewGuid().ToString("N"))
    [System.IO.Directory]::CreateDirectory($smokeRoot) | Out-Null
    try {
        [System.IO.Compression.ZipFile]::ExtractToDirectory($zipPath, $smokeRoot)
        & (Join-Path $PSScriptRoot "smoke-desktop-operator-preview.ps1") `
            -BinaryPath (Join-Path $smokeRoot "TraverseBoard.exe")
    }
    finally {
        $resolvedSmoke = [System.IO.Path]::GetFullPath($smokeRoot)
        $outputPrefix = $outputRoot.TrimEnd('\', '/') + [System.IO.Path]::DirectorySeparatorChar
        if (-not $resolvedSmoke.StartsWith($outputPrefix,
                [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to clean a smoke directory outside the release output"
        }
        if (Test-Path -LiteralPath $resolvedSmoke) {
            Remove-Item -LiteralPath $resolvedSmoke -Recurse -Force
        }
    }
}

Write-Output "portable_release_verification: pass"
Write-Output "portable_release_version: $($manifest.version)"
Write-Output "portable_release_revision: $($manifest.revision)"
Write-Output "portable_release_zip_sha256: $zipHash"
