[CmdletBinding()]
param(
    [string]$BinaryPath = "build/desktop/TraverseBoard.exe",
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
$minimumWebView2RuntimeVersion = "94.0.992.31"

$binaryItem = Get-Item -LiteralPath $binary
$binaryHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $binary).Hash.ToLowerInvariant()
$releaseMetadataPath = Join-Path (Split-Path -Parent $binary) "release-metadata.json"
$releaseMetadata = $null
$releaseMetadataHashMatches = $false
if (Test-Path -LiteralPath $releaseMetadataPath -PathType Leaf) {
    try {
        $releaseMetadata = Get-Content -Raw -LiteralPath $releaseMetadataPath | ConvertFrom-Json
    } catch {
        throw "Desktop release metadata is not valid JSON"
    }
    $releaseMetadataHashMatches = [string]$releaseMetadata.sha256 -eq $binaryHash
}

$results = [System.Collections.Generic.List[object]]::new()
function Add-Result {
    param([string]$ID, [string]$Status, [object]$Facts)
    [void]$results.Add([pscustomobject][ordered]@{ id = $ID; status = $Status; facts = $Facts })
}

# Redact any user/home/workspace identity that could leak a personal secret.
function Protect-Path {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return "" }
    return [System.IO.Path]::GetFileName($Path)
}

function Get-WebView2RuntimeVersion {
    $channelIDs = @(
        "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}",
        "{2CD8A007-E189-409D-A2C8-9AF4EF3C72AA}",
        "{0D50BFEC-CD6A-4F9A-964C-C7416E3ACB10}",
        "{65C35B14-6C1D-4122-AC46-7148CC9D6497}"
    )
    # Exhaust the ClientState layout used by the bundled loader before the
    # documented Clients fallback. Otherwise a stale stable-channel Clients
    # key could hide the preview channel the application will actually load.
    foreach ($layout in @("ClientState", "Clients")) {
        foreach ($channelID in $channelIDs) {
            $roots = @(
                "HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\$layout\$channelID",
                "HKCU:\Software\Microsoft\EdgeUpdate\$layout\$channelID",
                "HKLM:\SOFTWARE\Microsoft\EdgeUpdate\$layout\$channelID"
            )
            foreach ($root in $roots) {
                if (Test-Path -LiteralPath $root) {
                    $values = Get-ItemProperty -LiteralPath $root -ErrorAction SilentlyContinue
                    if (-not [string]::IsNullOrWhiteSpace([string]$values.pv)) {
                        return [string]$values.pv
                    }
                    if (-not [string]::IsNullOrWhiteSpace([string]$values.EBWebView)) {
                        return [System.IO.Path]::GetFileName(
                            ([string]$values.EBWebView).TrimEnd('\', '/'))
                    }
                }
            }
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
    return [int][Math]::Round(100 * $pixelsPerInch / 96)
}

function Get-MonitorLayout {
    try {
        Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop | Out-Null
        if (-not ("DesktopMatrixNative" -as [type])) {
            Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class DesktopMatrixNative
{
    [StructLayout(LayoutKind.Sequential)]
    public struct POINT
    {
        public int X;
        public int Y;
    }

    [DllImport("user32.dll")]
    public static extern IntPtr MonitorFromPoint(POINT point, uint flags);

    [DllImport("shcore.dll")]
    public static extern int GetDpiForMonitor(IntPtr monitor, int dpiType,
        out uint dpiX, out uint dpiY);

    [DllImport("shcore.dll")]
    public static extern int GetScaleFactorForMonitor(IntPtr monitor,
        out int scalePercent);
}
'@ -ErrorAction Stop | Out-Null
        }
        $screens = [System.Windows.Forms.Screen]::AllScreens
        $fallbackDpi = Get-DpiPercent
        $items = foreach ($screen in $screens) {
            $dpiPercent = $fallbackDpi
            try {
                $point = [DesktopMatrixNative+POINT]::new()
                $point.X = $screen.Bounds.X + [int]($screen.Bounds.Width / 2)
                $point.Y = $screen.Bounds.Y + [int]($screen.Bounds.Height / 2)
                $monitor = [DesktopMatrixNative]::MonitorFromPoint($point, 2)
                [int]$scalePercent = 0
                [uint32]$dpiX = 0
                [uint32]$dpiY = 0
                if ($monitor -ne [IntPtr]::Zero -and
                    [DesktopMatrixNative]::GetScaleFactorForMonitor($monitor,
                        [ref]$scalePercent) -eq 0 -and $scalePercent -gt 0) {
                    $dpiPercent = $scalePercent
                } elseif ($monitor -ne [IntPtr]::Zero -and
                    [DesktopMatrixNative]::GetDpiForMonitor($monitor, 0,
                        [ref]$dpiX, [ref]$dpiY) -eq 0 -and $dpiX -gt 0) {
                    $dpiPercent = [int][Math]::Round(100 * $dpiX / 96)
                }
            } catch {
                $dpiPercent = $fallbackDpi
            }
            [pscustomobject][ordered]@{
                primary     = $screen.Primary
                dpi_percent = $dpiPercent
                bounds      = [pscustomobject][ordered]@{
                    x      = $screen.Bounds.X
                    y      = $screen.Bounds.Y
                    width  = $screen.Bounds.Width
                    height = $screen.Bounds.Height
                }
            }
        }
        return @($items)
    } catch {
        return @()
    }
}

function Test-VersionAtLeast {
    param([string]$Actual, [string]$Minimum)
    try {
        return ([version]$Actual -ge [version]$Minimum)
    } catch {
        return $false
    }
}

# ---- Environment ----
$osInfo = Get-CimInstance Win32_OperatingSystem
$webView2Runtime = Get-WebView2RuntimeVersion
$monitorLayout = @(Get-MonitorLayout)
$environment = [pscustomobject][ordered]@{
    windows_version = [string]$osInfo.Caption
    build_number    = [string]$osInfo.BuildNumber
    architecture    = [string]$osInfo.OSArchitecture
    webview2_runtime = $webView2Runtime
    minimum_webview2_runtime = $minimumWebView2RuntimeVersion
    webview2_supported = (Test-VersionAtLeast -Actual $webView2Runtime `
        -Minimum $minimumWebView2RuntimeVersion)
    primary_dpi_percent = (Get-DpiPercent)
    monitors        = $monitorLayout
}
$releaseMetadataValid = $null -ne $releaseMetadata -and $releaseMetadataHashMatches -and
    [string]$releaseMetadata.revision -match '^[0-9a-f]{40}$' -and
    -not [bool]$releaseMetadata.modified
Add-Result "candidate_provenance" ($(if ($releaseMetadataValid) { "pass" } else { "fail" })) `
    ([pscustomobject][ordered]@{
        release_metadata_present = $null -ne $releaseMetadata
        sha256_matches            = $releaseMetadataHashMatches
        revision_bound            = $null -ne $releaseMetadata -and
            [string]$releaseMetadata.revision -match '^[0-9a-f]{40}$'
        modified                  = $(if ($null -ne $releaseMetadata) {
            [bool]$releaseMetadata.modified
        } else { $null })
    })

# ---- Scenarios ----
$temporaryRoot = Join-Path $repositoryRoot ".tmp"
$isolatedHome = Join-Path $temporaryRoot ("desktop-test-matrix-" + [guid]::NewGuid().ToString("N"))
$database = Join-Path $isolatedHome "cyberagent.db"
[System.IO.Directory]::CreateDirectory($isolatedHome) | Out-Null
$previousHome = $env:CYBERAGENT_HOME
$primary = $null
$second = $null
$startedProcessIDs = [System.Collections.Generic.List[int]]::new()
try {
    $env:CYBERAGENT_HOME = $isolatedHome

    # S1 cold start
    $primary = Start-Process -FilePath $binary -PassThru
    [void]$startedProcessIDs.Add($primary.Id)
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
    $primary.Refresh()
    $primaryAlive = -not $primary.HasExited
    Add-Result "cold_start" ($(if ($storeCreated -and $primaryAlive) { "pass" } else { "fail" })) `
        ([pscustomobject][ordered]@{
            process_alive = $primaryAlive
            store_created = $storeCreated
        })

    # S2 second instance yields to the first
    $second = Start-Process -FilePath $binary -PassThru
    [void]$startedProcessIDs.Add($second.Id)
    $secondExited = $false
    $secondDeadline = [DateTime]::UtcNow.AddSeconds(15)
    while ([DateTime]::UtcNow -lt $secondDeadline) {
        $second.Refresh()
        if ($second.HasExited) { $secondExited = $true; break }
        Start-Sleep -Milliseconds 100
    }
    $secondExitCode = $null
    if ($secondExited) {
        $second.Refresh()
        $secondExitCode = $second.ExitCode
    } else {
        $null = Stop-Process -Id $second.Id -Force
        $second.WaitForExit()
    }
    $secondYielded = $secondExited -and $secondExitCode -eq 0
    Add-Result "second_instance" ($(if ($secondYielded) { "pass" } else { "fail" })) `
        ([pscustomobject][ordered]@{
            second_exited    = $secondExited
            second_exit_code = $secondExitCode
        })
    $second.Dispose()
    $second = $null

    # S3 normal exit: the store may be created before the native window handle is
    # published, especially on a freshly provisioned Windows 10 VM. Wait for the
    # actual main window before sending WM_CLOSE so startup speed cannot turn a
    # successful graceful exit into a false negative.
    $windowDeadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $windowReady = $false
    while ([DateTime]::UtcNow -lt $windowDeadline) {
        $primary.Refresh()
        if ($primary.HasExited) { break }
        if ($primary.MainWindowHandle -ne [IntPtr]::Zero) {
            $windowReady = $true
            break
        }
        Start-Sleep -Milliseconds 100
    }
    $closeRequested = $windowReady -and $primary.CloseMainWindow()
    $cleanExit = $closeRequested -and $primary.WaitForExit(10000)
    $normalExitCode = $null
    if ($cleanExit) {
        $primary.Refresh()
        $normalExitCode = $primary.ExitCode
    } else {
        Stop-Process -Id $primary.Id -Force -ErrorAction SilentlyContinue
        $primary.WaitForExit()
    }
    $storePresentAfterExit = Test-Path -LiteralPath $database -PathType Leaf
    $normalExitPassed = $cleanExit -and $normalExitCode -eq 0 -and $storePresentAfterExit
    $primary.Dispose()
    $primary = $null
    Add-Result "normal_exit" ($(if ($normalExitPassed) { "pass" } else { "fail" })) `
        ([pscustomobject][ordered]@{
            window_ready            = $windowReady
            close_requested         = $closeRequested
            clean_exit              = $cleanExit
            exit_code               = $normalExitCode
            store_present_after_exit = $storePresentAfterExit
        })

    # S4 force kill then reopen with data retention
    $primary = Start-Process -FilePath $binary -PassThru
    [void]$startedProcessIDs.Add($primary.Id)
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
    $storeLengthBeforeKill = if (Test-Path -LiteralPath $database -PathType Leaf) {
        [int64](Get-Item -LiteralPath $database).Length
    } else { [int64]0 }
    $null = Stop-Process -Id $primary.Id -Force
    $primary.WaitForExit()
    $primary.Dispose()
    $primary = $null
    $retained = Test-Path -LiteralPath $database -PathType Leaf
    $primary = Start-Process -FilePath $binary -PassThru
    [void]$startedProcessIDs.Add($primary.Id)
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
    $storeLengthAfterReopen = if (Test-Path -LiteralPath $database -PathType Leaf) {
        [int64](Get-Item -LiteralPath $database).Length
    } else { [int64]0 }
    $killReopenPassed = $killReady -and $retained -and $reopened -and
        $storeLengthBeforeKill -gt 0 -and $storeLengthAfterReopen -gt 0
    Add-Result "kill_reopen" ($(if ($killReopenPassed) { "pass" } else { "fail" })) `
        ([pscustomobject][ordered]@{
            ready_before_kill       = $killReady
            store_retained          = $retained
            store_bytes_before_kill = $storeLengthBeforeKill
            reopened                = $reopened
            store_bytes_after_reopen = $storeLengthAfterReopen
        })
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
    # A failed scenario can lose its Process object after disposal. Revisit
    # only PIDs started by this run; never terminate a pre-existing user-owned
    # instance merely because it shares the candidate binary path.
    foreach ($startedProcessID in $startedProcessIDs) {
        $remaining = Get-Process -Id $startedProcessID -ErrorAction SilentlyContinue
        if ($null -ne $remaining) {
            try {
                if ($remaining.Path -and $remaining.Path.Equals($binary,
                        [System.StringComparison]::OrdinalIgnoreCase)) {
                    Stop-Process -Id $startedProcessID -Force -ErrorAction SilentlyContinue
                }
            } catch {
                # An exited process or denied Path read needs no broader cleanup.
            }
        }
    }
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

$automatedFailed = @($results | Where-Object { $_.status -ne "pass" }).Count -gt 0
$manualChecks = @(
    [pscustomobject][ordered]@{
        id = "ui_1024x768_and_200_percent"
        status = "not_run"
        detail = "Requires operator visual review at both 1024x768 and 200% DPI"
    },
    [pscustomobject][ordered]@{
        id = "multi_monitor_mixed_dpi"
        status = "not_run"
        detail = "Requires two physical or virtual displays with different scaling"
    },
    [pscustomobject][ordered]@{
        id = "webview2_missing_old_corrupt"
        status = "not_run"
        detail = "Requires an isolated host or VM with the runtime unavailable or unsupported"
    },
    [pscustomobject][ordered]@{
        id = "offline_start"
        status = "not_run"
        detail = "Requires operator-controlled network isolation"
    }
)
$candidate = [pscustomobject][ordered]@{
    name              = (Protect-Path $binary)
    sha256            = $binaryHash
    size_bytes        = [int64]$binaryItem.Length
    app_version       = $(if ($null -ne $releaseMetadata) { [string]$releaseMetadata.app_version } else { "" })
    revision          = $(if ($null -ne $releaseMetadata) { [string]$releaseMetadata.revision } else { "" })
    source_date_epoch = $(if ($null -ne $releaseMetadata) { [int64]$releaseMetadata.source_date_epoch } else { [int64]0 })
    modified          = $(if ($null -ne $releaseMetadata) { [bool]$releaseMetadata.modified } else { $null })
}
$report = [pscustomobject][ordered]@{
    protocol_version = "desktop_test_matrix.v2"
    generated_at     = [DateTime]::UtcNow.ToString("o")
    automated_status = $(if ($automatedFailed) { "fail" } else { "pass" })
    overall_status   = $(if ($automatedFailed) { "fail" } else { "needs_manual_evidence" })
    candidate        = $candidate
    environment      = $environment
    results          = @($results)
    manual_checks    = $manualChecks
}
$report | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $output -Encoding utf8
Write-Output "desktop_test_matrix_written: $output"
