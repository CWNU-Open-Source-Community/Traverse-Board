[CmdletBinding()]
param(
    [ValidateSet("environment", "stage", "self-test", "verify", "compare", "command")]
    [string]$Action = "verify",
    [ValidateSet("main-workbench", "settings")]
    [string]$BaselineCase = "main-workbench",
    [string]$ActualPath = "",
    [string[]]$Roi = @(),
    [string[]]$Mask = @(),
    [ValidateRange(-1, 255)][int]$ChannelThreshold = -1,
    [ValidateRange(-1.0, 1.0)][double]$MaxDiffRatio = -1.0,
    [ValidateRange(-1.0, 255.0)][double]$MaxMeanAbsoluteError = -1.0,
    [string]$Executable = "",
    [string[]]$ArgumentList = @()
)

$ErrorActionPreference = "Stop"
$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$repositoryPrefix = $repositoryRoot.TrimEnd('\') + '\'
if (-not [string]::Equals([System.IO.Path]::GetPathRoot($repositoryRoot), "D:\",
        [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "UI tests must run from the D: worktree"
}

# Capture the source location before this process receives its isolated APPDATA.
# The authoritative images are read from the user's existing local Temp folder;
# no file is ever written back to that location.
$sourceLocalAppData = [System.Environment]::GetFolderPath(
    [System.Environment+SpecialFolder]::LocalApplicationData)
if ([string]::IsNullOrWhiteSpace($sourceLocalAppData)) {
    $sourceLocalAppData = [System.Environment]::GetEnvironmentVariable("LOCALAPPDATA", "Process")
}
$sourceTempDirectory = [System.IO.Path]::GetFullPath((Join-Path $sourceLocalAppData "Temp"))
$baselineConfigPath = Join-Path $PSScriptRoot "ui-reference-baselines.json"
$baselineStageRoot = Join-Path $repositoryRoot ".tmp\reference-baselines"
$runtimeRoot = Join-Path $repositoryRoot ".tmp\ui-test-runtime"
$playwrightOutputRoot = Join-Path $repositoryRoot "output\playwright"

function Assert-PathWithinRepository {
    param([Parameter(Mandatory = $true)][string]$Path)

    $candidate = [System.IO.Path]::GetFullPath($Path)
    if (-not $candidate.StartsWith($repositoryPrefix,
            [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "UI test path escaped the worktree: $candidate"
    }
    return $candidate
}

function Assert-NoReparsePoint {
    param([Parameter(Mandatory = $true)][string]$Path)

    $candidate = Assert-PathWithinRepository -Path $Path
    $relative = $candidate.Substring($repositoryPrefix.Length)
    $current = $repositoryRoot
    foreach ($component in [regex]::Split($relative, '[\\/]')) {
        if ([string]::IsNullOrWhiteSpace($component)) { continue }
        $current = Join-Path $current $component
        if (-not (Test-Path -LiteralPath $current)) { break }
        $item = Get-Item -Force -LiteralPath $current
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "UI test path cannot traverse a reparse point: $current"
        }
    }
    return $candidate
}

function New-IsolatedDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)

    $candidate = Assert-NoReparsePoint -Path $Path
    [System.IO.Directory]::CreateDirectory($candidate) | Out-Null
    $item = Get-Item -Force -LiteralPath $candidate
    if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "UI test directory became a reparse point: $candidate"
    }
    return $candidate
}

function Get-PngDimensions {
    param([Parameter(Mandatory = $true)][string]$Path)

    $stream = [System.IO.File]::Open($Path, [System.IO.FileMode]::Open,
        [System.IO.FileAccess]::Read, [System.IO.FileShare]::Read)
    try {
        $header = [byte[]]::new(24)
        $read = $stream.Read($header, 0, $header.Length)
        $signature = [byte[]](137, 80, 78, 71, 13, 10, 26, 10)
        if ($read -ne $header.Length) { throw "PNG header is truncated: $Path" }
        for ($index = 0; $index -lt $signature.Length; $index++) {
            if ($header[$index] -ne $signature[$index]) {
                throw "Reference baseline is not a PNG: $Path"
            }
        }
        if ([System.Text.Encoding]::ASCII.GetString($header, 12, 4) -cne "IHDR") {
            throw "Reference PNG does not begin with IHDR: $Path"
        }
        [uint32]$width = ([uint32]$header[16] -shl 24) -bor
            ([uint32]$header[17] -shl 16) -bor ([uint32]$header[18] -shl 8) -bor
            [uint32]$header[19]
        [uint32]$height = ([uint32]$header[20] -shl 24) -bor
            ([uint32]$header[21] -shl 16) -bor ([uint32]$header[22] -shl 8) -bor
            [uint32]$header[23]
        return [pscustomobject]@{ Width = [int]$width; Height = [int]$height }
    }
    finally {
        $stream.Dispose()
    }
}

function Read-BaselineConfiguration {
    if (-not (Test-Path -LiteralPath $baselineConfigPath -PathType Leaf)) {
        throw "UI reference baseline configuration is missing"
    }
    $configuration = Get-Content -Raw -LiteralPath $baselineConfigPath | ConvertFrom-Json
    if ([string]$configuration.protocol_version -cne "traverse_ui_reference_baselines.v1") {
        throw "UI reference baseline configuration protocol is invalid"
    }
    $cases = @($configuration.cases)
    if ($cases.Count -ne 2) {
        throw "UI reference baseline configuration must contain exactly two cases"
    }
    $seen = @{}
    foreach ($case in $cases) {
        $name = [string]$case.name
        if ($name -notin @("main-workbench", "settings") -or $seen.ContainsKey($name)) {
            throw "UI reference baseline case identity is invalid: $name"
        }
        $seen[$name] = $true
        if ([string]$case.source_file -match '[\\/]' -or
                [string]$case.staged_file -match '[\\/]' -or
                [string]$case.sha256 -notmatch '^[0-9a-f]{64}$' -or
                [int]$case.width -le 0 -or [int]$case.height -le 0) {
            throw "UI reference baseline case metadata is invalid: $name"
        }
    }
    return $cases
}

function Assert-IgnoredRuntimePath {
    param([Parameter(Mandatory = $true)][string]$Path)

    $relative = [System.IO.Path]::GetRelativePath($repositoryRoot,
        (Assert-PathWithinRepository -Path $Path)).Replace('\', '/')
    & git -C $repositoryRoot check-ignore --quiet -- $relative
    if ($LASTEXITCODE -ne 0) {
        throw "Reference baseline runtime path is not gitignored: $relative"
    }
}

function Stage-ReferenceBaselines {
    $destinationRoot = New-IsolatedDirectory -Path $baselineStageRoot
    $results = @()
    foreach ($case in (Read-BaselineConfiguration)) {
        $source = [System.IO.Path]::GetFullPath(
            (Join-Path $sourceTempDirectory ([string]$case.source_file)))
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
            throw "Authoritative UI reference is missing: $source"
        }
        $sourceItem = Get-Item -Force -LiteralPath $source
        if (($sourceItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "Authoritative UI reference cannot be a reparse point: $source"
        }
        $sourceHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $source).Hash.ToLowerInvariant()
        $sourceDimensions = Get-PngDimensions -Path $source
        if ($sourceHash -cne [string]$case.sha256 -or
                $sourceDimensions.Width -ne [int]$case.width -or
                $sourceDimensions.Height -ne [int]$case.height) {
            throw "Authoritative UI reference failed hash or dimension verification: $($case.name)"
        }

        $destination = Assert-NoReparsePoint -Path (
            Join-Path $destinationRoot ([string]$case.staged_file))
        Assert-IgnoredRuntimePath -Path $destination
        if (Test-Path -LiteralPath $destination) {
            $destinationItem = Get-Item -Force -LiteralPath $destination
            if (($destinationItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "Staged UI reference cannot be a reparse point: $destination"
            }
            $destinationHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $destination).Hash.ToLowerInvariant()
            if ($destinationHash -cne $sourceHash) {
                throw "Existing staged UI reference differs from its authority: $destination"
            }
        }
        else {
            $partial = "$destination.partial-$PID-$([guid]::NewGuid().ToString('N'))"
            try {
                Copy-Item -LiteralPath $source -Destination $partial
                $partialHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $partial).Hash.ToLowerInvariant()
                if ($partialHash -cne $sourceHash) {
                    throw "Staged UI reference copy failed verification: $($case.name)"
                }
                [System.IO.File]::Move($partial, $destination)
            }
            finally {
                if (Test-Path -LiteralPath $partial) {
                    Remove-Item -LiteralPath $partial -Force
                }
            }
        }

        $results += [pscustomobject][ordered]@{
            name = [string]$case.name
            file = [string]$case.staged_file
            sha256 = $sourceHash
            width = [int]$case.width
            height = [int]$case.height
        }
    }

    $runtimeManifest = [ordered]@{
        protocol_version = "traverse_ui_staged_baselines.v1"
        cases = $results
    }
    $runtimeManifestPath = Join-Path $destinationRoot "manifest.json"
    Assert-IgnoredRuntimePath -Path $runtimeManifestPath
    [System.IO.File]::WriteAllText($runtimeManifestPath,
        (($runtimeManifest | ConvertTo-Json -Depth 5) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
    return $results
}

function Invoke-CheckedNative {
    param(
        [Parameter(Mandatory = $true)][string]$Command,
        [Parameter(Mandatory = $true)][object[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Failure
    )

    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Failure (exit $LASTEXITCODE)"
    }
}

function Invoke-VisualComparison {
    $cases = @(Stage-ReferenceBaselines)
    $selected = $cases | Where-Object { $_.name -ceq $BaselineCase }
    if ($null -eq $selected) { throw "Unknown UI reference baseline: $BaselineCase" }
    if ([string]::IsNullOrWhiteSpace($ActualPath)) {
        throw "-ActualPath is required for the compare action"
    }
    $actual = if ([System.IO.Path]::IsPathFullyQualified($ActualPath)) {
        [System.IO.Path]::GetFullPath($ActualPath)
    } else {
        [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $ActualPath))
    }
    Assert-NoReparsePoint -Path $actual | Out-Null
    if (-not (Test-Path -LiteralPath $actual -PathType Leaf)) {
        throw "Actual UI screenshot is missing: $actual"
    }

    $configuration = @(Read-BaselineConfiguration) |
        Where-Object { [string]$_.name -ceq $BaselineCase }
    $effectiveChannelThreshold = if ($ChannelThreshold -ge 0) {
        $ChannelThreshold
    } else { [int]$configuration.thresholds.channel_threshold }
    $effectiveMaxDiffRatio = if ($MaxDiffRatio -ge 0) {
        $MaxDiffRatio
    } else { [double]$configuration.thresholds.max_diff_ratio }
    $effectiveMaxMeanError = if ($MaxMeanAbsoluteError -ge 0) {
        $MaxMeanAbsoluteError
    } else { [double]$configuration.thresholds.max_mean_absolute_error }

    $arguments = @(
        "run", "./cmd/visualdiff",
        "-baseline", (Join-Path $baselineStageRoot ([string]$selected.file)),
        "-baseline-sha256", [string]$selected.sha256,
        "-actual", $actual,
        "-output-dir", $playwrightOutputRoot,
        "-name", $BaselineCase,
        "-channel-threshold", [string]$effectiveChannelThreshold,
        "-max-diff-ratio", $effectiveMaxDiffRatio.ToString(
            [System.Globalization.CultureInfo]::InvariantCulture),
        "-max-mean-error", $effectiveMaxMeanError.ToString(
            [System.Globalization.CultureInfo]::InvariantCulture)
    )
    foreach ($value in @($configuration.rois) + $Roi) {
        if (-not [string]::IsNullOrWhiteSpace([string]$value)) {
            $arguments += @("-roi", [string]$value)
        }
    }
    foreach ($value in @($configuration.masks) + $Mask) {
        if (-not [string]::IsNullOrWhiteSpace([string]$value)) {
            $arguments += @("-mask", [string]$value)
        }
    }
    Push-Location $repositoryRoot
    try {
        Invoke-CheckedNative -Command "go" -Arguments $arguments `
            -Failure "UI visual comparison failed"
    }
    finally {
        Pop-Location
    }
}

$directoryMap = [ordered]@{
    temp = Join-Path $runtimeRoot "temp"
    npm_cache = Join-Path $runtimeRoot "npm-cache"
    go_build = Join-Path $runtimeRoot "go-build"
    go_mod = Join-Path $runtimeRoot "go-mod"
    go_path = Join-Path $runtimeRoot "go-path"
    go_temp = Join-Path $runtimeRoot "go-temp"
    playwright_browsers = Join-Path $runtimeRoot "ms-playwright"
    cyberagent_home = Join-Path $runtimeRoot "cyberagent-home"
    xdg_cache = Join-Path $runtimeRoot "xdg-cache"
    appdata_roaming = Join-Path $runtimeRoot "appdata\roaming"
    appdata_local = Join-Path $runtimeRoot "appdata\local"
    playwright_output = $playwrightOutputRoot
}
foreach ($path in $directoryMap.Values) {
    New-IsolatedDirectory -Path $path | Out-Null
}

$environmentValues = [ordered]@{
    TEMP = $directoryMap.temp
    TMP = $directoryMap.temp
    TMPDIR = $directoryMap.temp
    npm_config_cache = $directoryMap.npm_cache
    NPM_CONFIG_UPDATE_NOTIFIER = "false"
    NO_UPDATE_NOTIFIER = "1"
    GOCACHE = $directoryMap.go_build
    GOMODCACHE = $directoryMap.go_mod
    GOPATH = $directoryMap.go_path
    GOTMPDIR = $directoryMap.go_temp
    GOENV = "off"
    PLAYWRIGHT_BROWSERS_PATH = $directoryMap.playwright_browsers
    PLAYWRIGHT_ARTIFACTS_DIR = $directoryMap.playwright_output
    TRAVERSE_PLAYWRIGHT_OUTPUT_DIR = $directoryMap.playwright_output
    CYBERAGENT_HOME = $directoryMap.cyberagent_home
    XDG_CACHE_HOME = $directoryMap.xdg_cache
    APPDATA = $directoryMap.appdata_roaming
    LOCALAPPDATA = $directoryMap.appdata_local
}
$protectedNames = @("HOME", "USERPROFILE", "CODEX_HOME")
$protectedBefore = @{}
foreach ($name in $protectedNames) {
    $protectedBefore[$name] = [System.Environment]::GetEnvironmentVariable($name, "Process")
}
$previousEnvironment = @{}
foreach ($name in $environmentValues.Keys) {
    $previousEnvironment[$name] = [System.Environment]::GetEnvironmentVariable($name, "Process")
    [System.Environment]::SetEnvironmentVariable($name, [string]$environmentValues[$name], "Process")
}

try {
    Invoke-CheckedNative -Command "go" -Arguments @("telemetry", "off") `
        -Failure "Could not disable Go telemetry inside the isolated D-drive profile"
    & (Join-Path $PSScriptRoot "tests\assert-ui-test-environment.ps1") `
        -RepositoryRoot $repositoryRoot | Out-Host
    foreach ($name in $protectedNames) {
        if ([System.Environment]::GetEnvironmentVariable($name, "Process") -cne
                $protectedBefore[$name]) {
            throw "UI test environment changed protected variable $name"
        }
    }

    switch ($Action) {
        "environment" {
            [ordered]@{
                protocol_version = "traverse_ui_test_environment.v1"
                repository_root = $repositoryRoot
                runtime_root = $runtimeRoot
                playwright_output = $playwrightOutputRoot
                variables = $environmentValues
            } | ConvertTo-Json -Depth 4 -Compress
        }
        "stage" {
            Stage-ReferenceBaselines | Format-Table -AutoSize | Out-Host
        }
        "self-test" {
            Push-Location $repositoryRoot
            try {
                Invoke-CheckedNative -Command "go" `
                    -Arguments @("test", "-count=1", "./cmd/visualdiff") `
                    -Failure "Visual diff unit tests failed"
            }
            finally {
                Pop-Location
            }
        }
        "verify" {
            Stage-ReferenceBaselines | Format-Table -AutoSize | Out-Host
            Push-Location $repositoryRoot
            try {
                Invoke-CheckedNative -Command "go" `
                    -Arguments @("test", "-count=1", "./cmd/visualdiff") `
                    -Failure "Visual diff unit tests failed"
            }
            finally {
                Pop-Location
            }
        }
        "compare" {
            Invoke-VisualComparison
        }
        "command" {
            if ([string]::IsNullOrWhiteSpace($Executable)) {
                throw "-Executable is required for the command action"
            }
            Push-Location $repositoryRoot
            try {
                Invoke-CheckedNative -Command $Executable -Arguments $ArgumentList `
                    -Failure "D-drive test command failed"
            }
            finally {
                Pop-Location
            }
        }
    }
}
finally {
    foreach ($name in $environmentValues.Keys) {
        if ($null -eq $previousEnvironment[$name]) {
            Remove-Item -Path "Env:$name" -ErrorAction SilentlyContinue
        }
        else {
            [System.Environment]::SetEnvironmentVariable($name,
                $previousEnvironment[$name], "Process")
        }
    }
    foreach ($name in $protectedNames) {
        if ([System.Environment]::GetEnvironmentVariable($name, "Process") -cne
                $protectedBefore[$name]) {
            throw "UI test environment did not preserve protected variable $name"
        }
    }
}
