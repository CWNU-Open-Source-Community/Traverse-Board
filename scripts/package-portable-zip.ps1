[CmdletBinding()]
param(
    [string]$OutputDirectory = "build/desktop",
    [string]$Version = "v0.1.0",
    [switch]$VerifyReproducible
)

<#
.SYNOPSIS
Packages the Desktop portable ZIP with SBOM, NOTICE, LICENSE, README, checksums,
and a build manifest (issue #56).

.DESCRIPTION
Collects the built EXE, operator-preview launcher, local test guide, LICENSE,
README, NOTICE, CycloneDX SBOM, and release metadata into a single portable ZIP,
then emits a SHA256SUMS file and a per-entry build manifest. No secret, user
data, cache, debug log, source map, or untracked directory is included. ZIP
entries are sorted and carry the fixed DOS epoch, so the archive is byte-stable
under a fixed toolchain and source revision.
#>

$ErrorActionPreference = "Stop"
$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$outputRoot = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $OutputDirectory))
$binaryPath = Join-Path $outputRoot "TraverseBoard.exe"
$metadataPath = Join-Path $outputRoot "release-metadata.json"
$launcherPath = Join-Path $outputRoot "Start-Prayu-Operator-Preview.cmd"
$guidePath = Join-Path $outputRoot "LOCAL-TEST-GUIDE.txt"
$licensePath = Join-Path $repositoryRoot "LICENSE"
$readmePath = Join-Path $repositoryRoot "README.md"
$zipName = "Prayu-portable-$Version-windows-amd64.zip"
$zipPath = Join-Path $outputRoot $zipName

if ($Version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$') {
    throw "Portable ZIP version is invalid"
}

foreach ($required in @($binaryPath, $metadataPath, $launcherPath, $guidePath, $licensePath, $readmePath)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Portable ZIP input is missing: $required"
    }
}

$metadata = Get-Content -LiteralPath $metadataPath -Raw | ConvertFrom-Json
$metadataProblems = [System.Collections.Generic.List[string]]::new()
if ($metadata.protocol_version -ne "portable_release_metadata.v1") { $metadataProblems.Add("protocol") }
if ($metadata.app_version -ne $Version) { $metadataProblems.Add("version") }
if ($metadata.revision -notmatch '^[0-9a-f]{40}$') { $metadataProblems.Add("revision") }
if ([bool]$metadata.modified) { $metadataProblems.Add("modified") }
if (-not [bool]$metadata.reproducibility_checked) { $metadataProblems.Add("reproducibility_not_checked") }
if (-not [bool]$metadata.reproducible) { $metadataProblems.Add("not_reproducible") }
if ($metadataProblems.Count -ne 0) {
    throw "Portable ZIP release metadata failed: $($metadataProblems -join ', ')"
}

# SBOM + NOTICE
& go run ./cmd/releasegen -out $outputRoot -version $Version
if ($LASTEXITCODE -ne 0) { throw "releasegen failed" }
$sbomPath = Join-Path $outputRoot "sbom.json"
$noticePath = Join-Path $outputRoot "NOTICE"

# Staging directory (inside build output, never containing user data)
$staging = Join-Path $outputRoot (".portable-staging-" + [guid]::NewGuid().ToString("N"))
[System.IO.Directory]::CreateDirectory($staging) | Out-Null
try {
    Copy-Item -LiteralPath $binaryPath -Destination (Join-Path $staging "TraverseBoard.exe")
    Copy-Item -LiteralPath $launcherPath -Destination (Join-Path $staging (Split-Path -Leaf $launcherPath))
    Copy-Item -LiteralPath $guidePath -Destination (Join-Path $staging (Split-Path -Leaf $guidePath))
    Copy-Item -LiteralPath $licensePath -Destination (Join-Path $staging "LICENSE")
    Copy-Item -LiteralPath $readmePath -Destination (Join-Path $staging "README.md")
    Copy-Item -LiteralPath $noticePath -Destination (Join-Path $staging "NOTICE")
    Copy-Item -LiteralPath $sbomPath -Destination (Join-Path $staging "sbom.json")
    Copy-Item -LiteralPath $metadataPath -Destination (Join-Path $staging "release-metadata.json")

    $contents = @(
        "TraverseBoard.exe", "Start-Prayu-Operator-Preview.cmd",
        "LOCAL-TEST-GUIDE.txt", "LICENSE", "README.md", "NOTICE",
        "sbom.json", "release-metadata.json"
    )
    $entries = foreach ($name in $contents) {
        $entryPath = Join-Path $staging $name
        $entry = Get-Item -LiteralPath $entryPath
        [ordered]@{
            name = $name
            size = [int64]$entry.Length
            sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $entryPath).Hash.ToLowerInvariant()
        }
    }

    if (Test-Path -LiteralPath $zipPath -PathType Leaf) {
        Remove-Item -LiteralPath $zipPath -Force
    }
    & go run ./cmd/releasegen -zip $staging -out $outputRoot -zip-name $zipName
    if ($LASTEXITCODE -ne 0) { throw "deterministic ZIP packaging failed" }

    if ($VerifyReproducible) {
        $reproZipName = ".portable-repro-" + [guid]::NewGuid().ToString("N") + ".zip"
        $reproZipPath = Join-Path $outputRoot $reproZipName
        try {
            & go run ./cmd/releasegen -zip $staging -out $outputRoot -zip-name $reproZipName
            if ($LASTEXITCODE -ne 0) { throw "portable ZIP reproducibility build failed" }
            $firstZipHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $zipPath).Hash
            $secondZipHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $reproZipPath).Hash
            if ($firstZipHash -cne $secondZipHash) {
                throw "Portable ZIP reproducibility check failed: consecutive archive hashes differ"
            }
        }
        finally {
            if (Test-Path -LiteralPath $reproZipPath -PathType Leaf) {
                Remove-Item -LiteralPath $reproZipPath -Force
            }
        }
    }

    $zipHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $zipPath).Hash.ToLowerInvariant()
    $binaryHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $binaryPath).Hash.ToLowerInvariant()
    $sbomHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $sbomPath).Hash.ToLowerInvariant()
    $noticeHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $noticePath).Hash.ToLowerInvariant()

    $manifest = [ordered]@{
        protocol_version = "portable_zip_manifest.v1"
        zip_name = $zipName
        zip_sha256 = $zipHash
        binary_sha256 = $binaryHash
        sbom_sha256 = $sbomHash
        notice_sha256 = $noticeHash
        version = $Version
        revision = [string]$metadata.revision
        zip_reproducibility_checked = [bool]$VerifyReproducible
        zip_timestamps_reproducible = $true
        contents = $contents
        entries = @($entries)
    }
    $manifestPath = Join-Path $outputRoot "portable-zip-manifest.json"
    [System.IO.File]::WriteAllText($manifestPath, (($manifest | ConvertTo-Json -Depth 4) + "`n"),
        [System.Text.UTF8Encoding]::new($false))

    $metadataHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $metadataPath).Hash.ToLowerInvariant()
    $manifestHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $manifestPath).Hash.ToLowerInvariant()
    $sumsPath = Join-Path $outputRoot "SHA256SUMS"
    $sums = @(
        "$binaryHash  TraverseBoard.exe",
        "$zipHash  $zipName",
        "$sbomHash  sbom.json",
        "$noticeHash  NOTICE",
        "$metadataHash  release-metadata.json",
        "$manifestHash  portable-zip-manifest.json"
    ) -join "`n"
    [System.IO.File]::WriteAllText($sumsPath, $sums + "`n", [System.Text.UTF8Encoding]::new($false))

    & (Join-Path $PSScriptRoot "verify-portable-zip.ps1") `
        -OutputDirectory $OutputDirectory -ExpectedVersion $Version `
        -ExpectedRevision ([string]$metadata.revision)

    Write-Output "portable_zip: $zipPath"
    Write-Output "portable_zip_sha256: $zipHash"
    Write-Output "sha256sums: $sumsPath"
    Write-Output "manifest: $manifestPath"
}
finally {
    if (Test-Path -LiteralPath $staging) {
        Remove-Item -LiteralPath $staging -Recurse -Force
    }
}
