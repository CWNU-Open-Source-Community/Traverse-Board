[CmdletBinding()]
param(
    [string]$MsixPath = "build/desktop/PrayuDesktop.msix",
    [ValidateSet("install", "uninstall", "verify")][string]$Action = "verify",
    [switch]$AllowUnsigned
)

<#
.SYNOPSIS
Installs, verifies, or uninstalls the per-user MSIX (issue #57).

.DESCRIPTION
Local install-lifecycle helper for the candidate MSIX. `verify` installs and
checks registration, `install` only installs, `uninstall` removes by package
name. Unsigned local packages require the Windows "Developer Mode" setting or
-AllowUnsigned. Uninstall removes the package but leaves the app's user data
directory for the operator to delete separately.
#>

$ErrorActionPreference = "Stop"
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "MSIX verification requires Windows"
}
$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$msix = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $MsixPath))
$packageName = "PrayuDesktop"

function Invoke-AppxInstall {
    param([string]$Path)
    if ($AllowUnsigned) {
        Add-AppxPackage -Path $Path -AllowUnsigned
    } else {
        Add-AppxPackage -Path $Path
    }
}

switch ($Action) {
    "install" {
        if (-not (Test-Path -LiteralPath $msix -PathType Leaf)) { throw "MSIX is missing: $msix" }
        Invoke-AppxInstall $msix
        Write-Output "msix_installed: true"
    }
    "uninstall" {
        $package = Get-AppxPackage -Name $packageName -ErrorAction SilentlyContinue
        if ($null -eq $package) {
            Write-Output "msix_uninstalled: true (not installed)"
        } else {
            Remove-AppxPackage -Package $package.PackageFullName
            Write-Output "msix_uninstalled: true (user data left in place)"
        }
    }
    "verify" {
        if (-not (Test-Path -LiteralPath $msix -PathType Leaf)) { throw "MSIX is missing: $msix" }
        Invoke-AppxInstall $msix
        $package = Get-AppxPackage -Name $packageName
        if ($null -eq $package) { throw "MSIX did not register after install" }
        Write-Output "msix_verify: pass"
        Write-Output "msix_package_full_name: $($package.PackageFullName)"
        Write-Output "msix_version: $($package.Version)"
    }
}
