[CmdletBinding()]
param(
    [string]$BinaryPath = "build/desktop/TraverseBoard.exe",
    [string]$SeedDatabasePath = "",
    [ValidateRange(3, 60)][int]$StartupTimeoutSeconds = 15,
    [switch]$KeepData
)

$ErrorActionPreference = "Stop"

function Find-SqlitePython {
    $candidates = @(
        @{ Name = "py"; Prefix = @("-3") },
        @{ Name = "python"; Prefix = @() },
        @{ Name = "python3"; Prefix = @() }
    )
    foreach ($candidate in $candidates) {
        $command = Get-Command $candidate.Name -CommandType Application -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if ($null -eq $command) {
            continue
        }
        $arguments = @($candidate.Prefix) + @(
            "-c",
            'import sqlite3; print("traverse-board-sqlite-ready")'
        )
        $probe = @(& $command.Source $arguments 2>&1)
        $probeExitCode = $LASTEXITCODE
        $probeText = (($probe | ForEach-Object { $_.ToString() }) -join "`n").Trim()
        if ($probeExitCode -eq 0 -and $probeText -ceq "traverse-board-sqlite-ready") {
            return [pscustomobject]@{
                Executable = $command.Source
                Prefix = @($candidate.Prefix)
            }
        }
    }
    throw "Seeded Desktop smoke requires Python with the standard sqlite3 module"
}

function Read-DatabaseSchemaVersion {
    param(
        [Parameter(Mandatory = $true)][string]$DatabasePath,
        [Parameter(Mandatory = $true)]$PythonRuntime,
        [Parameter(Mandatory = $true)][string]$Description,
        [switch]$Immutable
    )

    $query = @'
import pathlib
import sqlite3
import sys

database = pathlib.Path(sys.argv[1]).resolve()
uri = database.as_uri() + "?mode=ro"
if sys.argv[2] == "true":
    uri += "&immutable=1"
connection = sqlite3.connect(uri, uri=True, timeout=1.0)
try:
    minimum, maximum, count = connection.execute(
        "SELECT MIN(version), MAX(version), COUNT(*) FROM schema_migrations"
    ).fetchone()
    if minimum != 1 or not isinstance(maximum, int) or count != maximum:
        raise RuntimeError("schema_migrations is not a contiguous v1..vN ledger")
    print(maximum)
finally:
    connection.close()
'@
    $arguments = @($PythonRuntime.Prefix) + @(
        "-c",
        $query,
        $DatabasePath,
        $Immutable.IsPresent.ToString().ToLowerInvariant()
    )
    $result = @(& $PythonRuntime.Executable $arguments 2>&1)
    $queryExitCode = $LASTEXITCODE
    $resultText = (($result | ForEach-Object { $_.ToString() }) -join "`n").Trim()
    if ($queryExitCode -ne 0) {
        throw "Could not read $Description schema version in read-only mode: $resultText"
    }
    if (-not [regex]::IsMatch($resultText, '^[1-9][0-9]*$')) {
        throw "Could not parse $Description schema version: $resultText"
    }
    return [int]::Parse($resultText, [System.Globalization.CultureInfo]::InvariantCulture)
}

function Read-LatestSchemaVersion {
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $migrationSource = Join-Path $RepositoryRoot "internal/store/migrations.go"
    if (-not (Test-Path -LiteralPath $migrationSource -PathType Leaf)) {
        throw "Latest schema version source is missing: $migrationSource"
    }
    $source = [System.IO.File]::ReadAllText($migrationSource)
    $matches = [regex]::Matches($source,
        '(?m)^const LatestSchemaVersion = (?<version>[1-9][0-9]*)\r?$')
    if ($matches.Count -ne 1) {
        throw "LatestSchemaVersion must have one strict declaration in: $migrationSource"
    }
    return [int]::Parse($matches[0].Groups['version'].Value,
        [System.Globalization.CultureInfo]::InvariantCulture)
}

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
    throw "Desktop direct-launch smoke requires Windows"
}
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    throw "Desktop binary is missing: $binary"
}

$seedDatabase = $null
$seedDatabaseHash = $null
$seedSchemaVersion = $null
$latestSchemaVersion = $null
$sqlitePython = $null
if (-not [string]::IsNullOrWhiteSpace($SeedDatabasePath)) {
    $seedDatabase = if ([System.IO.Path]::IsPathRooted($SeedDatabasePath)) {
        [System.IO.Path]::GetFullPath($SeedDatabasePath)
    } else {
        [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $SeedDatabasePath))
    }
    if (-not $seedDatabase.StartsWith($repositoryPrefix,
            [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Desktop smoke seed database must remain inside the repository: $seedDatabase"
    }
    if (-not (Test-Path -LiteralPath $seedDatabase -PathType Leaf)) {
        throw "Desktop smoke seed database is missing: $seedDatabase"
    }
    foreach ($suffix in @("-wal", "-shm", "-journal")) {
        $sidecar = $seedDatabase + $suffix
        if (Test-Path -LiteralPath $sidecar) {
            throw "Desktop smoke seed database must be quiescent; sidecar is present: $sidecar"
        }
    }
    $seedDatabaseHash = (Get-FileHash -LiteralPath $seedDatabase -Algorithm SHA256).Hash
    $latestSchemaVersion = Read-LatestSchemaVersion -RepositoryRoot $repositoryRoot
    $sqlitePython = Find-SqlitePython
    $seedSchemaVersion = Read-DatabaseSchemaVersion -DatabasePath $seedDatabase `
        -PythonRuntime $sqlitePython -Description "seed database" -Immutable
    $seedDatabaseHashAfterRead = (Get-FileHash -LiteralPath $seedDatabase -Algorithm SHA256).Hash
    if ($seedDatabaseHash -cne $seedDatabaseHashAfterRead) {
        throw "Desktop smoke seed database changed while its schema version was inspected"
    }
    if ($seedSchemaVersion -ge $latestSchemaVersion) {
        throw "Desktop smoke seed schema v$seedSchemaVersion must be older than repository schema v$latestSchemaVersion"
    }
}

$temporaryRoot = Join-Path $repositoryRoot ".tmp"
$isolatedHome = Join-Path $temporaryRoot ("desktop-direct-launch-smoke-" + [guid]::NewGuid().ToString("N"))
$database = Join-Path $isolatedHome "cyberagent.db"
[System.IO.Directory]::CreateDirectory($isolatedHome) | Out-Null
$previousHome = $env:CYBERAGENT_HOME
$process = $null
$startedProcessID = $null
$startupSucceeded = $false
$observedSchemaVersion = $null
$lastDatabaseProbeError = $null
try {
    if ($null -ne $seedDatabase) {
        [System.IO.File]::Copy($seedDatabase, $database, $false)
        $copiedDatabaseHash = (Get-FileHash -LiteralPath $database -Algorithm SHA256).Hash
        if ($copiedDatabaseHash -cne $seedDatabaseHash) {
            throw "Isolated Desktop smoke database does not match the seed before launch"
        }
    }
    $env:CYBERAGENT_HOME = $isolatedHome
    $process = Start-Process -FilePath $binary -PassThru
    $startedProcessID = $process.Id
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        $process.Refresh()
        if ($process.HasExited) {
            throw "Desktop exited during startup with code $($process.ExitCode)"
        }
        if (Test-Path -LiteralPath $database -PathType Leaf) {
            if ($null -ne $seedDatabase) {
                try {
                    $observedSchemaVersion = Read-DatabaseSchemaVersion -DatabasePath $database `
                        -PythonRuntime $sqlitePython -Description "isolated database"
                    $lastDatabaseProbeError = $null
                }
                catch {
                    $lastDatabaseProbeError = $_.Exception.Message
                    Start-Sleep -Milliseconds 250
                    continue
                }
                if ($observedSchemaVersion -gt $latestSchemaVersion) {
                    throw "Desktop upgraded the isolated database beyond repository schema v$latestSchemaVersion to v$observedSchemaVersion"
                }
                if ($observedSchemaVersion -lt $latestSchemaVersion) {
                    Start-Sleep -Milliseconds 250
                    continue
                }
            }
            Start-Sleep -Milliseconds 750
            $process.Refresh()
            if ($process.HasExited) {
                throw "Desktop exited after creating its local store with code $($process.ExitCode)"
            }
            if ($null -ne $seedDatabase) {
                $confirmedSchemaVersion = Read-DatabaseSchemaVersion -DatabasePath $database `
                    -PythonRuntime $sqlitePython -Description "isolated database"
                if ($confirmedSchemaVersion -ne $latestSchemaVersion -or
                        $confirmedSchemaVersion -le $seedSchemaVersion) {
                    throw "Desktop database upgrade was not stable: seed=v$seedSchemaVersion expected=v$latestSchemaVersion observed=v$confirmedSchemaVersion"
                }
                $observedSchemaVersion = $confirmedSchemaVersion
            }
            $startupSucceeded = $true
            break
        }
        Start-Sleep -Milliseconds 100
    }
    if (-not $startupSucceeded -and $null -ne $seedDatabase) {
        $observed = if ($null -eq $observedSchemaVersion) { "unreadable" } else { "v$observedSchemaVersion" }
        $probeDetail = if ([string]::IsNullOrWhiteSpace($lastDatabaseProbeError)) {
            "none"
        } else {
            $lastDatabaseProbeError
        }
        throw "Desktop did not upgrade the isolated database before the startup deadline: seed=v$seedSchemaVersion expected=v$latestSchemaVersion observed=$observed last_probe_error=$probeDetail"
    }
    if (-not $startupSucceeded) {
        throw "Desktop did not create its isolated local store before the startup deadline"
    }
}
finally {
    try {
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
    finally {
        if ($null -ne $seedDatabase) {
            $seedDatabaseHashAfterSmoke = (Get-FileHash -LiteralPath $seedDatabase -Algorithm SHA256).Hash
            if ($seedDatabaseHash -cne $seedDatabaseHashAfterSmoke) {
                throw "Desktop smoke seed database was modified"
            }
        }
    }
}

Write-Output "desktop_direct_launch_smoke: pass"
Write-Output "desktop_direct_launch_pid: $startedProcessID"
Write-Output "desktop_direct_launch_store_created: true"
Write-Output ("desktop_direct_launch_seeded_database: $($null -ne $seedDatabase)".ToLowerInvariant())
if ($null -ne $seedDatabase) {
    Write-Output "desktop_direct_launch_seed_schema_version: $seedSchemaVersion"
    Write-Output "desktop_direct_launch_observed_schema_version: $observedSchemaVersion"
    Write-Output "desktop_direct_launch_expected_schema_version: $latestSchemaVersion"
    Write-Output "desktop_direct_launch_seed_source_unchanged: true"
}
