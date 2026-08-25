[CmdletBinding()]
param(
    [string]$BinaryPath = "build/desktop/TraverseBoard.exe",
    [ValidateRange(3, 60)][int]$StartupTimeoutSeconds = 15,
    [switch]$KeepData
)

$ErrorActionPreference = "Stop"
$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$binary = if ([System.IO.Path]::IsPathRooted($BinaryPath)) {
    [System.IO.Path]::GetFullPath($BinaryPath)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $BinaryPath))
}
$repositoryPrefix = $repositoryRoot.TrimEnd('\', '/') + [System.IO.Path]::DirectorySeparatorChar
if (-not $binary.StartsWith($repositoryPrefix,
        [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Desktop smoke binary must remain inside the repository"
}
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "Desktop operator-preview smoke requires Windows"
}
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    throw "Desktop binary is missing: $binary"
}

$temporaryRoot = Join-Path $repositoryRoot ".tmp"
$isolatedHome = Join-Path $temporaryRoot ("desktop-operator-preview-smoke-" + [guid]::NewGuid().ToString("N"))
$database = Join-Path $isolatedHome "cyberagent.db"
[System.IO.Directory]::CreateDirectory($isolatedHome) | Out-Null
$previousHome = $env:CYBERAGENT_HOME
$process = $null
try {
    $env:CYBERAGENT_HOME = $isolatedHome
    $process = Start-Process -FilePath $binary -ArgumentList "--operator-preview" -PassThru
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        $process.Refresh()
        if ($process.HasExited) {
            throw "Desktop exited during startup with code $($process.ExitCode)"
        }
        if (Test-Path -LiteralPath $database -PathType Leaf) {
            Start-Sleep -Milliseconds 750
            $process.Refresh()
            if ($process.HasExited) {
                throw "Desktop exited after creating its local store with code $($process.ExitCode)"
            }
            Write-Output "desktop_operator_preview_smoke: pass"
            Write-Output "desktop_operator_preview_pid: $($process.Id)"
            Write-Output "desktop_operator_preview_store_created: true"
            break
        }
        Start-Sleep -Milliseconds 100
    }
    if (-not (Test-Path -LiteralPath $database -PathType Leaf)) {
        throw "Desktop did not create its isolated local store before the startup deadline"
    }
}
finally {
    $env:CYBERAGENT_HOME = $previousHome
    if ($null -ne $process) {
        $process.Refresh()
        if (-not $process.HasExited) {
            $null = $process.CloseMainWindow()
            if (-not $process.WaitForExit(5000)) {
                Stop-Process -Id $process.Id -Force
                $process.WaitForExit()
            }
        }
        $process.Dispose()
    }
    if (-not $KeepData -and (Test-Path -LiteralPath $isolatedHome)) {
        $resolvedHome = [System.IO.Path]::GetFullPath($isolatedHome)
        $resolvedTemporaryRoot = [System.IO.Path]::GetFullPath($temporaryRoot).TrimEnd('\') + '\'
        if (-not $resolvedHome.StartsWith($resolvedTemporaryRoot,
                [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to clean a Desktop smoke directory outside the repository temporary root"
        }
        Remove-Item -LiteralPath $resolvedHome -Recurse -Force
    }
}
