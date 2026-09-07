[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$RepositoryRoot
)

$ErrorActionPreference = "Stop"
$root = [System.IO.Path]::GetFullPath($RepositoryRoot).TrimEnd('\')
$rootPrefix = $root + [System.IO.Path]::DirectorySeparatorChar
$runtimeRoot = [System.IO.Path]::GetFullPath(
    (Join-Path $root ".tmp\ui-test-runtime")).TrimEnd('\')
$runtimePrefix = $runtimeRoot + [System.IO.Path]::DirectorySeparatorChar
$playwrightRoot = [System.IO.Path]::GetFullPath(
    (Join-Path $root "output\playwright")).TrimEnd('\')
$playwrightPrefix = $playwrightRoot + [System.IO.Path]::DirectorySeparatorChar

if (-not [string]::Equals([System.IO.Path]::GetPathRoot($root), "D:\",
        [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "UI test repository must be on D:"
}

$pathVariables = @(
    "TEMP",
    "TMP",
    "TMPDIR",
    "npm_config_cache",
    "GOCACHE",
    "GOMODCACHE",
    "GOPATH",
    "GOTMPDIR",
    "PLAYWRIGHT_BROWSERS_PATH",
    "PLAYWRIGHT_ARTIFACTS_DIR",
    "TRAVERSE_PLAYWRIGHT_OUTPUT_DIR",
    "CYBERAGENT_HOME",
    "XDG_CACHE_HOME",
    "APPDATA",
    "LOCALAPPDATA"
)

foreach ($name in $pathVariables) {
    $value = [System.Environment]::GetEnvironmentVariable($name, "Process")
    if ([string]::IsNullOrWhiteSpace($value) -or
            -not [System.IO.Path]::IsPathFullyQualified($value)) {
        throw "UI test environment variable $name is not an absolute path"
    }
    $resolved = [System.IO.Path]::GetFullPath($value)
    if (-not $resolved.StartsWith($rootPrefix,
            [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "UI test environment variable $name escaped the D: worktree: $resolved"
    }
}

$runtimeVariables = @(
    "TEMP",
    "TMP",
    "TMPDIR",
    "npm_config_cache",
    "GOCACHE",
    "GOMODCACHE",
    "GOPATH",
    "GOTMPDIR",
    "PLAYWRIGHT_BROWSERS_PATH",
    "CYBERAGENT_HOME",
    "XDG_CACHE_HOME",
    "APPDATA",
    "LOCALAPPDATA"
)
foreach ($name in $runtimeVariables) {
    $resolved = [System.IO.Path]::GetFullPath(
        [System.Environment]::GetEnvironmentVariable($name, "Process"))
    if (-not $resolved.StartsWith($runtimePrefix,
            [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "UI test runtime variable $name escaped .tmp/ui-test-runtime: $resolved"
    }
}

foreach ($name in @("PLAYWRIGHT_ARTIFACTS_DIR", "TRAVERSE_PLAYWRIGHT_OUTPUT_DIR")) {
    $resolved = [System.IO.Path]::GetFullPath(
        [System.Environment]::GetEnvironmentVariable($name, "Process"))
    if (-not ([string]::Equals($resolved, $playwrightRoot,
                [System.StringComparison]::OrdinalIgnoreCase) -or
            $resolved.StartsWith($playwrightPrefix,
                [System.StringComparison]::OrdinalIgnoreCase))) {
        throw "UI test artifact variable $name escaped output/playwright: $resolved"
    }
}

if ([System.Environment]::GetEnvironmentVariable("GOENV", "Process") -cne "off") {
    throw "GOENV must be off inside the UI test process"
}
$goTelemetryRaw = @(& go env -json GOTELEMETRY GOTELEMETRYDIR)
if ($LASTEXITCODE -ne 0) {
    throw "Could not inspect Go telemetry isolation"
}
$goTelemetry = ($goTelemetryRaw -join "`n") | ConvertFrom-Json
if ([string]$goTelemetry.GOTELEMETRY -cne "off") {
    throw "Go telemetry must be off inside the UI test process"
}
$goTelemetryDirectory = [System.IO.Path]::GetFullPath(
    [string]$goTelemetry.GOTELEMETRYDIR)
if (-not $goTelemetryDirectory.StartsWith($runtimePrefix,
        [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Go telemetry directory escaped .tmp/ui-test-runtime: $goTelemetryDirectory"
}

Write-Output "ui_test_environment: pass"
