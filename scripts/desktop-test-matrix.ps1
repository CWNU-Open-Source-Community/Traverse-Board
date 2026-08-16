[CmdletBinding()]
param(
    [string]$BinaryPath = "build/desktop/cyberagent-desktop.exe",
    [ValidateRange(3, 60)][int]$StartupTimeoutSeconds = 15,
    [string]$OutputPath = ""
)

<#
.SYNOPSIS
Reproducible Windows Desktop pre-release test matrix (issue #55).

.DESCRIPTION
Collects the environment (OS, WebView2 runtime, DPI, monitor layout), then runs
the automatable scenarios — cold start, second instance yield, force-kill/reopen
with data retention — against an isolated CYBERAGENT_HOME. Every path, API key,
and chat-content-bearing string is redacted before output; no screenshot is
captured without an operator decision. The manual UI/DPI checklist remains in
docs/DESKTOP_TEST_MATRIX.md.

The script never operates, closes, or reuses a Codex/Chrome window or Profile.
#>

$ErrorActionPreference = "Stop"
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "Desktop test matrix requires Windows"
}

$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$binary = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $BinaryPath))
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    throw "Desktop binary is missing: $binary"
}
if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $OutputPath = Join-Path (Split-Path -Parent $binary) "desktop-test-matrix.json"
}
$output = [System.IO.Path]::GetFullPath($OutputPath)

$results = [System.Collections.Generic.List[object]]::new()
function Add-Result {
    param([string]$ID, [string]$Status, [string]$Detail)
    $results.Add([pscustomobject][ordered]@{ id = $ID; status = $Status; detail = $Detail })
}

# Redact any user/home/workspace identity that could leak a personal secret.
function Protect-Path {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return "" }
    return [System.IO.Path]::GetFileName($Path)
}

function Get-WebView2RuntimeVersion {
    $guid = "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
    $roots = @(
        "HKCU:\Software\Microsoft\EdgeUpdate\Clients\$guid",
        "HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\$guid",
        "HKLM:\SOFTWARE\Microsoft\EdgeUpdate\Clients\$guid"
    )
    foreach ($root in $roots) {
        if (Test-Path -LiteralPath $root) {
            $value = (Get-ItemProperty -LiteralPath $root -Name pv -ErrorAction SilentlyContinue).pv
            if (-not [string]::IsNullOrWhiteSpace($value)) { return [string]$value }
        }
    }
    return ""
}

function Get-DpiPercent {
    $pixelsPerInch = 96
    $applied = (Get-ItemProperty -LiteralPath "HKCU:\Control Panel\Desktop\WindowMetrics" `
        -Name AppliedDPI -ErrorAction SilentlyContinue).AppliedDPI
    if ($applied -is [int] -and $applied -gt 0) {
        $pixelsPerInch = $applied
    } else {
        $logPixels = (Get-ItemProperty -LiteralPath "HKCU:\Control Panel\Desktop" `
            -Name LogPixels -ErrorAction SilentlyContinue).LogPixels
        if ($logPixels -is [int] -and $logPixels -gt 0) { $pixelsPerInch = $logPixels }
    }
    return [Math]::Round(100 * $pixelsPerInch / 96)
}

function Get-MonitorLayout {
    try {
        Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop | Out-Null
        $screens = [System.Windows.Forms.Screen]::AllScreens
        $items = foreach ($screen in $screens) {
            [pscustomobject][ordered]@{
                primary = $screen.Primary
                bounds  = "$($screen.Bounds.Width)x$($screen.Bounds.Height)"
            }
        }
        return @($items)
    } catch {
        return @()
    }
}

# ---- Environment ----
$osInfo = Get-CimInstance Win32_OperatingSystem
$environment = [pscustomobject][ordered]@{
    windows_version = [string]$osInfo.Caption
    build_number    = [string]$osInfo.BuildNumber
    webview2_runtime = (Get-WebView2RuntimeVersion)
    dpi_percent     = (Get-DpiPercent)
    monitors        = (Get-MonitorLayout)
}

# ---- Scenarios ----
$temporaryRoot = Join-Path $repositoryRoot ".tmp"
$isolatedHome = Join-Path $temporaryRoot ("desktop-test-matrix-" + [guid]::NewGuid().ToString("N"))
$database = Join-Path $isolatedHome "cyberagent.db"
[System.IO.Directory]::CreateDirectory($isolatedHome) | Out-Null
$previousHome = $env:CYBERAGENT_HOME
$primary = $null
$second = $null
try {
    $env:CYBERAGENT_HOME = $isolatedHome

    # S1 cold start
    $primary = Start-Process -FilePath $binary -ArgumentList "--operator-preview" -PassThru
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $storeCreated = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        $primary.Refresh()
        if ($primary.HasExited) {
            throw "Desktop exited during cold start with code $($primary.ExitCode)"
        }
        if (Test-Path -LiteralPath $database -PathType Leaf) {
            Start-Sleep -Milliseconds 750
            $storeCreated = $true
            break
        }
        Start-Sleep -Milliseconds 100
    }
    Add-Result "cold_start" ($(if ($storeCreated) { "pass" } else { "fail" })) `
        ("store_created=" + $storeCreated + " pid=" + $primary.Id)

    # S2 second instance yields to the first
    $second = Start-Process -FilePath $binary -ArgumentList "--operator-preview" -PassThru
    $secondExited = $false
    $secondDeadline = [DateTime]::UtcNow.AddSeconds(15)
    while ([DateTime]::UtcNow -lt $secondDeadline) {
        $second.Refresh()
        if ($second.HasExited) { $secondExited = $true; break }
        Start-Sleep -Milliseconds 100
    }
    if (-not $secondExited) { $null = Stop-Process -Id $second.Id -Force; $second.WaitForExit() }
    Add-Result "second_instance" ($(if ($secondExited) { "pass" } else { "fail" })) `
        ("second_exited=" + $secondExited)

    # S3 normal exit: graceful close must terminate without force kill
    $null = $primary.CloseMainWindow()
    $cleanExit = $primary.WaitForExit(5000)
    $primary.Dispose()
    $primary = $null
    Add-Result "normal_exit" ($(if ($cleanExit) { "pass" } else { "fail" })) `
        ("clean_exit=" + $cleanExit)

    # S4 force kill then reopen with data retention
    $primary = Start-Process -FilePath $binary -ArgumentList "--operator-preview" -PassThru
    $reopenDeadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $killReady = $false
    while ([DateTime]::UtcNow -lt $reopenDeadline) {
        $primary.Refresh()
        if ($primary.HasExited) {
            throw "Desktop exited before kill/reopen with code $($primary.ExitCode)"
        }
        if (Test-Path -LiteralPath $database -PathType Leaf) {
            Start-Sleep -Milliseconds 750
            $killReady = $true
            break
        }
        Start-Sleep -Milliseconds 100
    }
    $null = Stop-Process -Id $primary.Id -Force
    $primary.WaitForExit()
    $primary.Dispose()
    $primary = $null
    $retained = Test-Path -LiteralPath $database -PathType Leaf
    $primary = Start-Process -FilePath $binary -ArgumentList "--operator-preview" -PassThru
    $reopenDeadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $reopened = $false
    while ([DateTime]::UtcNow -lt $reopenDeadline) {
        $primary.Refresh()
        if ($primary.HasExited) {
            throw "Desktop exited during reopen with code $($primary.ExitCode)"
        }
        if (Test-Path -LiteralPath $database -PathType Leaf) {
            Start-Sleep -Milliseconds 750
            $reopened = $true
            break
        }
        Start-Sleep -Milliseconds 100
    }
    Add-Result "kill_reopen" ($(if ($killReady -and $retained -and $reopened) { "pass" } else { "fail" })) `
        ("kill_ready=" + $killReady + " data_retained=" + $retained + " reopened=" + $reopened)
}
finally {
    $env:CYBERAGENT_HOME = $previousHome
    foreach ($process in @($second, $primary)) {
        if ($null -ne $process) {
            $process.Refresh()
            if (-not $process.HasExited) {
                $null = $process.CloseMainWindow()
                if (-not $process.WaitForExit(5000)) {
                    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
                }
            }
            $process.Dispose()
        }
    }
    # The single-instance / crash-recovery path may leave a lingering desktop
    # process holding the SQLite store. Terminate any remaining desktop process
    # that shares the tested binary path before cleaning up.
    Get-Process -Name "cyberagent-desktop" -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -and $_.Path.Equals($binary,
            [System.StringComparison]::OrdinalIgnoreCase) } |
        Stop-Process -Force -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $isolatedHome) {
        $resolvedHome = [System.IO.Path]::GetFullPath($isolatedHome)
        $resolvedTemporaryRoot = [System.IO.Path]::GetFullPath($temporaryRoot).TrimEnd('\') + '\'
        if (-not $resolvedHome.StartsWith($resolvedTemporaryRoot,
                [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to clean a Desktop test directory outside the repository temporary root"
        }
        for ($attempt = 1; $attempt -le 5; $attempt++) {
            try {
                Remove-Item -LiteralPath $resolvedHome -Recurse -Force -ErrorAction Stop
                break
            } catch {
                Start-Sleep -Milliseconds 400
            }
        }
    }
}

$report = [pscustomobject][ordered]@{
    protocol_version = "desktop_test_matrix.v1"
    generated_at     = [DateTime]::UtcNow.ToString("o")
    binary           = (Protect-Path $binary)
    environment      = $environment
    results          = @($results)
}
$report | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $output -Encoding utf8
Write-Output "desktop_test_matrix_written: $output"
