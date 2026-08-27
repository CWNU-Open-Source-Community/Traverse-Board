[CmdletBinding()]
param(
    [string]$BinaryPath = "build/desktop/TraverseBoard.exe",
    [string]$SeedDatabasePath = "",
    [ValidateRange(3, 60)][int]$StartupTimeoutSeconds = 15,
    [switch]$KeepData
)

$ErrorActionPreference = "Stop"

function Invoke-NativeCommandCapture {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][object[]]$Arguments
    )

    # Windows PowerShell 5.1 turns redirected native stderr into an ErrorRecord.
    # With the script-wide Stop preference that would become a terminating error
    # before callers can inspect LASTEXITCODE or try the next executable.
    $savedErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $capturedOutput = @(& $Executable $Arguments 2>&1)
        $capturedExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $savedErrorActionPreference
    }
    return [pscustomobject]@{
        Output = @($capturedOutput)
        ExitCode = $capturedExitCode
    }
}

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
            "import sqlite3; print('traverse-board-sqlite-ready')"
        )
        $probe = Invoke-NativeCommandCapture -Executable $command.Source -Arguments $arguments
        $probeText = (($probe.Output | ForEach-Object { $_.ToString() }) -join "`n").Trim()
        if ($probe.ExitCode -eq 0 -and $probeText -ceq "traverse-board-sqlite-ready") {
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
    $resultText = Invoke-SqlitePython -PythonRuntime $PythonRuntime -Script $query `
        -ScriptArguments @($DatabasePath,
            $Immutable.IsPresent.ToString().ToLowerInvariant()) `
        -Description "Read $Description schema version in read-only mode"
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

function Invoke-SqlitePython {
    param(
        [Parameter(Mandatory = $true)]$PythonRuntime,
        [Parameter(Mandatory = $true)][string]$Script,
        [string[]]$ScriptArguments = @(),
        [Parameter(Mandatory = $true)][string]$Description
    )

    # WinPS 5.1 strips literal double quotes from a native -c argument. Passing
    # a single-quoted base64 bootstrap keeps multiline Python identical on 5.1
    # and pwsh without shell-specific command-line reparsing.
    $encodedScript = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Script))
    $bootstrap = "import base64;exec(base64.b64decode('$encodedScript'))"
    $arguments = @($PythonRuntime.Prefix) + @("-c", $bootstrap) + @($ScriptArguments)
    $result = Invoke-NativeCommandCapture -Executable $PythonRuntime.Executable `
        -Arguments $arguments
    $resultText = (($result.Output | ForEach-Object { $_.ToString() }) -join "`n").Trim()
    if ($result.ExitCode -ne 0) {
        throw "$Description failed: $resultText"
    }
    return $resultText
}

function Add-IsolatedWorkspaceSentinel {
    param(
        [Parameter(Mandatory = $true)][string]$DatabasePath,
        [Parameter(Mandatory = $true)]$PythonRuntime,
        [Parameter(Mandatory = $true)][string]$SentinelID,
        [Parameter(Mandatory = $true)][string]$SentinelName,
        [Parameter(Mandatory = $true)][string]$SentinelRoot,
        [Parameter(Mandatory = $true)][string]$SentinelCreatedAt
    )

    $script = @'
import pathlib
import sqlite3
import sys

database = pathlib.Path(sys.argv[1]).resolve()
connection = sqlite3.connect(str(database), timeout=5.0)
try:
    connection.execute("PRAGMA foreign_keys = ON")
    connection.execute("BEGIN IMMEDIATE")
    if connection.execute(
        "SELECT COUNT(*) FROM workspaces WHERE id = ? OR name = ?",
        (sys.argv[2], sys.argv[3]),
    ).fetchone()[0] != 0:
        raise RuntimeError("workspace sentinel unexpectedly collides with seed data")
    connection.execute(
        "INSERT INTO workspaces (id, name, root_path, created_at) VALUES (?, ?, ?, ?)",
        (sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5]),
    )
    connection.commit()
    print("traverse-board-sentinel-ready")
except Exception:
    if connection.in_transaction:
        connection.rollback()
    raise
finally:
    connection.close()
'@
    $resultText = Invoke-SqlitePython -PythonRuntime $PythonRuntime -Script $script `
        -ScriptArguments @($DatabasePath, $SentinelID, $SentinelName, $SentinelRoot,
            $SentinelCreatedAt) -Description "Prepare isolated Desktop smoke sentinel"
    if ($resultText -cne "traverse-board-sentinel-ready") {
        throw "Could not confirm the isolated Desktop smoke sentinel: $resultText"
    }
}

function Read-DatabaseUpgradeSnapshot {
    param(
        [Parameter(Mandatory = $true)][string]$DatabasePath,
        [Parameter(Mandatory = $true)]$PythonRuntime,
        [Parameter(Mandatory = $true)][string]$SentinelID,
        [Parameter(Mandatory = $true)][string]$Description
    )

    $script = @'
import hashlib
import json
import pathlib
import sqlite3
import struct
import sys

def quote_identifier(value):
    return '"' + value.replace('"', '""') + '"'

def append_value(digest, value):
    if value is None:
        kind, payload = b"n", b""
    elif isinstance(value, int):
        kind, payload = b"i", str(value).encode("ascii")
    elif isinstance(value, float):
        kind, payload = b"f", struct.pack(">d", value)
    elif isinstance(value, str):
        kind, payload = b"t", value.encode("utf-8")
    elif isinstance(value, (bytes, bytearray, memoryview)):
        kind, payload = b"b", bytes(value)
    else:
        raise TypeError("unsupported SQLite value type: " + type(value).__name__)
    digest.update(kind)
    digest.update(struct.pack(">Q", len(payload)))
    digest.update(payload)

def row_digest(row):
    digest = hashlib.sha256()
    digest.update(struct.pack(">Q", len(row)))
    for value in row:
        append_value(digest, value)
    return digest.digest()

database = pathlib.Path(sys.argv[1]).resolve()
uri = database.as_uri() + "?mode=ro"
connection = sqlite3.connect(uri, uri=True, timeout=2.0)
try:
    connection.execute("BEGIN")
    ledger = [
        {"version": row[0], "name": row[1], "checksum": row[2]}
        for row in connection.execute(
            "SELECT version, name, checksum FROM schema_migrations ORDER BY version"
        )
    ]
    versions = [item["version"] for item in ledger]
    if not versions or versions != list(range(1, versions[-1] + 1)):
        raise RuntimeError("schema_migrations is not a contiguous v1..vN ledger")

    table_names = [
        row[0]
        for row in connection.execute(
            "SELECT name FROM sqlite_master "
            "WHERE type = 'table' AND name NOT LIKE 'sqlite_%' "
            "AND name <> 'schema_migrations' ORDER BY name"
        )
    ]
    business_tables = []
    for table_name in table_names:
        hashes = [
            row_digest(row)
            for row in connection.execute("SELECT * FROM " + quote_identifier(table_name))
        ]
        hashes.sort()
        content = hashlib.sha256()
        content.update(struct.pack(">Q", len(hashes)))
        for item in hashes:
            content.update(item)
        business_tables.append({
            "name": table_name,
            "row_count": len(hashes),
            "content_sha256": content.hexdigest(),
        })

    sentinel = connection.execute(
        "SELECT id, name, root_path, created_at FROM workspaces WHERE id = ?",
        (sys.argv[2],),
    ).fetchone()
    trigger_names = (
        "trg_risk_escalation_supervisor_authority_insert",
        "trg_host_command_supervisor_envelope_immutable",
    )
    triggers = []
    for trigger_name in trigger_names:
        row = connection.execute(
            "SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?",
            (trigger_name,),
        ).fetchone()
        triggers.append({"name": trigger_name, "sql": None if row is None else row[0]})

    integrity = [row[0] for row in connection.execute("PRAGMA integrity_check")]
    foreign_key_violations = sum(1 for _ in connection.execute("PRAGMA foreign_key_check"))
    snapshot = {
        "schema_version": versions[-1],
        "ledger": ledger,
        "business_tables": business_tables,
        "sentinel": None if sentinel is None else {
            "id": sentinel[0],
            "name": sentinel[1],
            "root_path": sentinel[2],
            "created_at_text": "sqlite-text:" + sentinel[3],
        },
        "integrity_check": integrity,
        "foreign_key_violation_count": foreign_key_violations,
        "triggers": triggers,
    }
    print(json.dumps(snapshot, sort_keys=True, separators=(",", ":"), ensure_ascii=True))
finally:
    if connection.in_transaction:
        connection.rollback()
    connection.close()
'@
    $resultText = Invoke-SqlitePython -PythonRuntime $PythonRuntime -Script $script `
        -ScriptArguments @($DatabasePath, $SentinelID) -Description $Description
    try {
        return $resultText | ConvertFrom-Json
    }
    catch {
        throw "Could not parse ${Description}: $($_.Exception.Message)"
    }
}

function Normalize-TriggerSQL {
    param([Parameter(Mandatory = $true)][string]$SQL)

    $normalized = $SQL.Trim()
    if ($normalized.EndsWith(";", [System.StringComparison]::Ordinal)) {
        $normalized = $normalized.Substring(0, $normalized.Length - 1).TrimEnd()
    }
    return $normalized.Replace("`r`n", "`n").Replace("`r", "`n")
}

function Read-CanonicalRepairTriggers {
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $baselinePath = Join-Path $RepositoryRoot "internal/store/clean_install_baseline.sql"
    if (-not (Test-Path -LiteralPath $baselinePath -PathType Leaf)) {
        throw "Clean-install baseline is missing: $baselinePath"
    }
    $source = [System.IO.File]::ReadAllText($baselinePath)
    $blocks = [regex]::Split($source,
        '(?m)^-- traverse-board-clean-install-object-boundary --\r?$')
    $result = @{}
    foreach ($name in @(
            "trg_risk_escalation_supervisor_authority_insert",
            "trg_host_command_supervisor_envelope_immutable")) {
        $pattern = '^CREATE TRIGGER ' + [regex]::Escape($name) + '(?:\s|$)'
        $matches = @($blocks | Where-Object {
                [regex]::IsMatch($_.Trim(), $pattern)
            })
        if ($matches.Count -ne 1) {
            throw "Clean-install baseline must contain one canonical trigger: $name"
        }
        $result[$name] = Normalize-TriggerSQL -SQL $matches[0]
    }
    return $result
}

function Assert-SeededUpgradePreserved {
    param(
        [Parameter(Mandatory = $true)]$Before,
        [Parameter(Mandatory = $true)]$After,
        [Parameter(Mandatory = $true)][int]$ExpectedSchemaVersion,
        [Parameter(Mandatory = $true)][hashtable]$ExpectedTriggerSQL,
        [Parameter(Mandatory = $true)][string]$SentinelID,
        [Parameter(Mandatory = $true)][string]$SentinelName,
        [Parameter(Mandatory = $true)][string]$SentinelRoot,
        [Parameter(Mandatory = $true)][string]$SentinelCreatedAt
    )

    if ([int]$After.schema_version -ne $ExpectedSchemaVersion -or
            [int]$After.schema_version -le [int]$Before.schema_version) {
        throw "Desktop database upgrade was not complete: seed=v$($Before.schema_version) expected=v$ExpectedSchemaVersion observed=v$($After.schema_version)"
    }

    $beforeLedger = @($Before.ledger)
    $afterLedger = @($After.ledger)
    if ($afterLedger.Count -ne $ExpectedSchemaVersion -or
            $beforeLedger.Count -ne [int]$Before.schema_version) {
        throw "Desktop database migration ledger length is invalid after upgrade"
    }
    for ($index = 0; $index -lt $beforeLedger.Count; $index++) {
        $expected = $beforeLedger[$index]
        $observed = $afterLedger[$index]
        if ([int]$observed.version -ne [int]$expected.version -or
                [string]$observed.name -cne [string]$expected.name -or
                [string]$observed.checksum -cne [string]$expected.checksum) {
            throw "Desktop database upgrade rewrote historical migration v$($expected.version)"
        }
    }

    $afterTables = @{}
    foreach ($table in @($After.business_tables)) {
        $name = [string]$table.name
        if ($afterTables.ContainsKey($name)) {
            throw "Desktop database upgrade snapshot contains a duplicate table: $name"
        }
        $afterTables[$name] = $table
    }
    foreach ($expected in @($Before.business_tables)) {
        $name = [string]$expected.name
        if (-not $afterTables.ContainsKey($name)) {
            throw "Desktop database upgrade removed an existing business table: $name"
        }
        $observed = $afterTables[$name]
        if ([long]$observed.row_count -ne [long]$expected.row_count -or
                [string]$observed.content_sha256 -cne [string]$expected.content_sha256) {
            throw "Desktop database upgrade changed existing business data in table: $name"
        }
    }

    if ($null -eq $After.sentinel -or
            [string]$After.sentinel.id -cne $SentinelID -or
            [string]$After.sentinel.name -cne $SentinelName -or
            [string]$After.sentinel.root_path -cne $SentinelRoot -or
            [string]$After.sentinel.created_at_text -cne "sqlite-text:$SentinelCreatedAt") {
        throw "Desktop database upgrade did not preserve the isolated Workspace sentinel"
    }

    $integrity = @($After.integrity_check)
    if ($integrity.Count -ne 1 -or [string]$integrity[0] -cne "ok") {
        throw "Desktop database integrity_check failed after upgrade: $($integrity -join ', ')"
    }
    if ([int]$After.foreign_key_violation_count -ne 0) {
        throw "Desktop database has foreign-key violations after upgrade"
    }

    $observedTriggers = @{}
    foreach ($trigger in @($After.triggers)) {
        $observedTriggers[[string]$trigger.name] = $trigger.sql
    }
    foreach ($name in $ExpectedTriggerSQL.Keys) {
        if (-not $observedTriggers.ContainsKey($name) -or
                [string]::IsNullOrWhiteSpace([string]$observedTriggers[$name])) {
            throw "Desktop database upgrade did not restore canonical trigger: $name"
        }
        $observedSQL = Normalize-TriggerSQL -SQL ([string]$observedTriggers[$name])
        if ($observedSQL -cne [string]$ExpectedTriggerSQL[$name]) {
            throw "Desktop database trigger differs from clean-install baseline: $name"
        }
    }
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
$canonicalRepairTriggers = $null
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
    $canonicalRepairTriggers = Read-CanonicalRepairTriggers -RepositoryRoot $repositoryRoot
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
$sentinelToken = [guid]::NewGuid().ToString("N")
$sentinelID = "desktop-seeded-smoke-$sentinelToken"
$sentinelName = "Traverse Board seeded smoke $sentinelToken"
$sentinelRoot = "C:\TraverseBoardSeededSmoke\$sentinelToken"
$sentinelCreatedAt = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffffffZ",
    [System.Globalization.CultureInfo]::InvariantCulture)
$previousHome = $env:CYBERAGENT_HOME
$process = $null
$startedProcessID = $null
$startupSucceeded = $false
$observedSchemaVersion = $null
$lastDatabaseProbeError = $null
$seedUpgradeSnapshot = $null
try {
    if ($null -ne $seedDatabase) {
        [System.IO.File]::Copy($seedDatabase, $database, $false)
        $copiedDatabaseHash = (Get-FileHash -LiteralPath $database -Algorithm SHA256).Hash
        if ($copiedDatabaseHash -cne $seedDatabaseHash) {
            throw "Isolated Desktop smoke database does not match the seed before launch"
        }
        Add-IsolatedWorkspaceSentinel -DatabasePath $database -PythonRuntime $sqlitePython `
            -SentinelID $sentinelID -SentinelName $sentinelName -SentinelRoot $sentinelRoot `
            -SentinelCreatedAt $sentinelCreatedAt
        $seedUpgradeSnapshot = Read-DatabaseUpgradeSnapshot -DatabasePath $database `
            -PythonRuntime $sqlitePython -SentinelID $sentinelID `
            -Description "Capture isolated seed database preservation snapshot"
        if ([int]$seedUpgradeSnapshot.schema_version -ne $seedSchemaVersion) {
            throw "Isolated seed database schema changed before Desktop launch"
        }
        $seedIntegrity = @($seedUpgradeSnapshot.integrity_check)
        if ($seedIntegrity.Count -ne 1 -or [string]$seedIntegrity[0] -cne "ok" -or
                [int]$seedUpgradeSnapshot.foreign_key_violation_count -ne 0) {
            throw "Desktop smoke seed database is not healthy before launch"
        }
        if ($null -eq $seedUpgradeSnapshot.sentinel -or
                [string]$seedUpgradeSnapshot.sentinel.id -cne $sentinelID -or
                [string]$seedUpgradeSnapshot.sentinel.name -cne $sentinelName -or
                [string]$seedUpgradeSnapshot.sentinel.root_path -cne $sentinelRoot -or
                [string]$seedUpgradeSnapshot.sentinel.created_at_text -cne
                    "sqlite-text:$sentinelCreatedAt") {
            throw "Isolated Workspace sentinel was not durable before Desktop launch"
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
                $upgradedSnapshot = Read-DatabaseUpgradeSnapshot -DatabasePath $database `
                    -PythonRuntime $sqlitePython -SentinelID $sentinelID `
                    -Description "Verify isolated upgraded database preservation"
                Assert-SeededUpgradePreserved -Before $seedUpgradeSnapshot `
                    -After $upgradedSnapshot -ExpectedSchemaVersion $latestSchemaVersion `
                    -ExpectedTriggerSQL $canonicalRepairTriggers -SentinelID $sentinelID `
                    -SentinelName $sentinelName -SentinelRoot $sentinelRoot `
                    -SentinelCreatedAt $sentinelCreatedAt
                $observedSchemaVersion = [int]$upgradedSnapshot.schema_version
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
    Write-Output "desktop_direct_launch_existing_business_data_preserved: true"
    Write-Output "desktop_direct_launch_historical_ledger_preserved: true"
    Write-Output "desktop_direct_launch_repair_triggers_canonical: true"
}
