[CmdletBinding()]
param(
    [string]$OutputDirectory = "build/desktop",
    [string]$Version = "v0.1.0",
    [switch]$SkipSmoke
)

<#
.SYNOPSIS
Builds and verifies one reproducible Windows portable release candidate.

.DESCRIPTION
This is the clean-checkout, single-command entry point for issue #56. It runs
the full frontend and Desktop build twice, creates the allowlisted ZIP, emits
SBOM/NOTICE/checksum/manifest companions, verifies every byte binding, and by
default starts the executable extracted from the ZIP in an isolated profile.
#>

$ErrorActionPreference = "Stop"
& (Join-Path $PSScriptRoot "build-desktop.ps1") `
    -OutputDirectory $OutputDirectory -Version $Version -VerifyReproducible
& (Join-Path $PSScriptRoot "package-portable-zip.ps1") `
    -OutputDirectory $OutputDirectory -Version $Version

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$outputRoot = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $OutputDirectory))
$metadataPath = Join-Path $outputRoot "release-metadata.json"
$metadata = Get-Content -LiteralPath $metadataPath -Raw | ConvertFrom-Json
$verifyParameters = @{
    OutputDirectory = $OutputDirectory
    ExpectedVersion = $Version
    ExpectedRevision = [string]$metadata.revision
}
if (-not $SkipSmoke) { $verifyParameters.SmokeTest = $true }
& (Join-Path $PSScriptRoot "verify-portable-zip.ps1") @verifyParameters

Write-Output "desktop_portable_release: pass"
