[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$entry = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\ui-test.ps1"))
$protectedNames = @("HOME", "USERPROFILE", "CODEX_HOME")
$managedNames = @(
    "TEMP", "TMP", "TMPDIR", "npm_config_cache", "NPM_CONFIG_UPDATE_NOTIFIER",
    "NO_UPDATE_NOTIFIER", "GOCACHE", "GOMODCACHE", "GOPATH", "GOTMPDIR",
    "GOENV", "PLAYWRIGHT_BROWSERS_PATH",
    "PLAYWRIGHT_ARTIFACTS_DIR", "TRAVERSE_PLAYWRIGHT_OUTPUT_DIR",
    "CYBERAGENT_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"
)
$processBefore = @{}
foreach ($name in $managedNames + $protectedNames) {
    $processBefore[$name] =
        [System.Environment]::GetEnvironmentVariable($name, "Process")
}
$before = @{}
foreach ($name in $protectedNames) {
    $before[$name] = [System.Environment]::GetEnvironmentVariable($name, "Process")
}
$persistentBefore = @{}
foreach ($target in @("User", "Machine")) {
    foreach ($name in $managedNames + $protectedNames) {
        $persistentBefore["$target/$name"] =
            [System.Environment]::GetEnvironmentVariable($name, $target)
    }
}

$output = @(& $entry -Action environment)
$summary = ($output | Select-Object -Last 1) | ConvertFrom-Json
if ([string]$summary.protocol_version -cne "traverse_ui_test_environment.v1") {
    throw "UI test environment summary protocol drifted"
}
if (-not ([string]$summary.repository_root).StartsWith("D:\",
        [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "UI test environment summary did not use the D: worktree"
}

foreach ($name in $protectedNames) {
    $after = [System.Environment]::GetEnvironmentVariable($name, "Process")
    if ($after -cne $before[$name]) {
        throw "UI test entry changed protected environment variable $name"
    }
}

foreach ($name in $managedNames + $protectedNames) {
    $after = [System.Environment]::GetEnvironmentVariable($name, "Process")
    if ($after -cne $processBefore[$name]) {
        throw "UI test entry did not restore process environment variable $name"
    }
}

foreach ($target in @("User", "Machine")) {
    foreach ($name in $managedNames + $protectedNames) {
        $after = [System.Environment]::GetEnvironmentVariable($name, $target)
        if ($after -cne $persistentBefore["$target/$name"]) {
            throw "UI test entry changed persistent $target environment variable $name"
        }
    }
}

Write-Output "ui_test_entry_environment_restoration: pass"
