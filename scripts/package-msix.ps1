[CmdletBinding()]
param(
    [string]$OutputDirectory = "build/desktop",
    [string]$Version = "0.1.0.0",
    [string]$CertificatePath = "",
    [string]$CertificatePassword = "",
    [string]$TimestampURL = "http://timestamp.digicert.com"
)

<#
.SYNOPSIS
Packages the per-user MSIX and (optionally) signs it (issue #57).

.DESCRIPTION
Stages the desktop EXE, AppxManifest.xml, and reviewed Traverse Board logos, then
runs makeappx to produce PrayuDesktop.msix. Signing is fail-closed: without a
code-signing certificate the script writes an unsigned MSIX and reports
signed=false; with --CertificatePath it runs signtool and fails on any signing,
timestamping, or verification error. The private key is never logged.
#>

$ErrorActionPreference = "Stop"
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "MSIX packaging requires Windows"
}
$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$outputRoot = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $OutputDirectory))
$binaryPath = Join-Path $outputRoot "cyberagent-desktop.exe"
$manifestPath = Join-Path $repositoryRoot "packaging/windows/AppxManifest.xml"
$assetRoot = Join-Path $repositoryRoot "packaging/windows/Assets"
$msixPath = Join-Path $outputRoot "PrayuDesktop.msix"

foreach ($required in @(
        $binaryPath,
        $manifestPath,
        (Join-Path $assetRoot "StoreLogo.png"),
        (Join-Path $assetRoot "Square150x150Logo.png"),
        (Join-Path $assetRoot "Square44x44Logo.png")
    )) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "MSIX input is missing: $required"
    }
}

function Find-SDKTool {
    param([string]$Name)
    $kitRoot = "C:\Program Files (x86)\Windows Kits\10\bin"
    if (-not (Test-Path -LiteralPath $kitRoot)) { return $null }
    $versions = Get-ChildItem -LiteralPath $kitRoot -Directory |
        Sort-Object Name -Descending
    foreach ($version in $versions) {
        $candidate = Join-Path $version.FullName "x64\$Name.exe"
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    return $null
}

# ---- Stage ----
$staging = Join-Path $outputRoot (".msix-staging-" + [guid]::NewGuid().ToString("N"))
[System.IO.Directory]::CreateDirectory((Join-Path $staging "Assets")) | Out-Null
try {
    Copy-Item -LiteralPath $binaryPath -Destination (Join-Path $staging "cyberagent-desktop.exe")
    $stagedManifest = Join-Path $staging "AppxManifest.xml"
    $manifestContent = [System.IO.File]::ReadAllText($manifestPath)
    $manifestContent = $manifestContent.Replace('Version="0.1.0.0"', "Version=`"$Version`"")
    [System.IO.File]::WriteAllText($stagedManifest, $manifestContent,
        [System.Text.UTF8Encoding]::new($false))
    Copy-Item -LiteralPath (Join-Path $assetRoot "StoreLogo.png") `
        -Destination (Join-Path $staging "Assets\StoreLogo.png")
    Copy-Item -LiteralPath (Join-Path $assetRoot "Square150x150Logo.png") `
        -Destination (Join-Path $staging "Assets\Square150x150Logo.png")
    Copy-Item -LiteralPath (Join-Path $assetRoot "Square44x44Logo.png") `
        -Destination (Join-Path $staging "Assets\Square44x44Logo.png")

    $makeappx = Find-SDKTool "makeappx"
    if ($null -eq $makeappx) { throw "makeappx.exe was not found in the Windows SDK" }
    if (Test-Path -LiteralPath $msixPath -PathType Leaf) { Remove-Item -LiteralPath $msixPath -Force }
    & $makeappx pack /d $staging /p $msixPath /o
    if ($LASTEXITCODE -ne 0) { throw "makeappx pack failed" }

    $signed = $false
    if (-not [string]::IsNullOrWhiteSpace($CertificatePath)) {
        if (-not (Test-Path -LiteralPath $CertificatePath -PathType Leaf)) {
            throw "Code-signing certificate was not found: $CertificatePath"
        }
        $signtool = Find-SDKTool "signtool"
        if ($null -eq $signtool) { throw "signtool.exe was not found in the Windows SDK" }
        $passwordArg = @()
        if (-not [string]::IsNullOrWhiteSpace($CertificatePassword)) {
            $passwordArg = @("/p", $CertificatePassword)
        }
        & $signtool sign /fd SHA256 /f $CertificatePath @passwordArg `
            /tr $TimestampURL /td SHA256 $msixPath
        if ($LASTEXITCODE -ne 0) { throw "signtool sign failed" }
        & $signtool verify /pa $msixPath
        if ($LASTEXITCODE -ne 0) { throw "signtool verify failed" }
        $signed = $true
    }

    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $msixPath).Hash.ToLowerInvariant()
    $manifest = [ordered]@{
        protocol_version = "msix_manifest.v1"
        msix_name = "PrayuDesktop.msix"
        msix_sha256 = $hash
        version = $Version
        signed = $signed
        installer_kind = "per_user_msix"
        data_preserved_on_upgrade = $true
        default_uninstall_preserves_user_data = $true
    }
    $manifestOut = Join-Path $outputRoot "msix-manifest.json"
    [System.IO.File]::WriteAllText($manifestOut, (($manifest | ConvertTo-Json -Depth 3) + "`n"),
        [System.Text.UTF8Encoding]::new($false))

    Write-Output "msix: $msixPath"
    Write-Output "msix_sha256: $hash"
    Write-Output "msix_signed: $signed"
    Write-Output "msix_manifest: $manifestOut"
    if (-not $signed) {
        Write-Output "msix_note: unsigned; release requires a protected code-signing certificate"
    }
}
finally {
    if (Test-Path -LiteralPath $staging) {
        Remove-Item -LiteralPath $staging -Recurse -Force
    }
}
