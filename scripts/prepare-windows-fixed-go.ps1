# Only the disposable GitHub runner may prepare this fixture. Product executable
# discovery stays restricted to Win32 Known Folders (ADR 0076), never PATH.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or
    $env:RUNNER_OS -ne 'Windows' -or $env:RUNNER_ARCH -ne 'X64') {
    throw 'Fixed Go preparation is restricted to GitHub-hosted Windows X64 CI.'
}

$setupGo = (Get-Command go -CommandType Application).Source
$goVersion = (& $setupGo env GOVERSION).Trim()
if ($LASTEXITCODE -ne 0 -or $goVersion -notmatch '^go1\.25\.[0-9]+$') {
    throw 'Expected the Go 1.25 patch selected by this workflow.'
}
$programFilesRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFiles)
if (-not [IO.Path]::IsPathFullyQualified($programFilesRoot)) {
    throw 'Windows Program Files Known Folder is unavailable.'
}
$installRoot = [IO.Path]::GetFullPath((Join-Path $programFilesRoot 'Go'))
$installedGo = Join-Path $installRoot 'bin\go.exe'
Write-Host "setup-go executable: $setupGo"
Write-Host "fixed Go installation: $installRoot"
foreach ($root in @($programFilesRoot,
    [Environment]::GetFolderPath([Environment+SpecialFolder]::ProgramFilesX86)) | Select-Object -Unique) {
    if ([string]::IsNullOrWhiteSpace($root)) { continue }
    $candidate = Join-Path $root 'Go\bin\go.exe'
    try {
        $item = Get-Item -LiteralPath $candidate
        Write-Host "fixed candidate: $candidate; attributes=$($item.Attributes); bytes=$($item.Length)"
        $probe = [IO.File]::Open($candidate, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
        $probe.Dispose()
        Write-Host 'fixed candidate read pin: available'
    } catch {
        Write-Host "fixed candidate: $candidate; probe=$($_.Exception.GetType().Name); hresult=$($_.Exception.HResult)"
    }
}
if (Test-Path -LiteralPath $installRoot) {
    $existingRoot = Get-Item -LiteralPath $installRoot
    if (-not $existingRoot.PSIsContainer -or
        ($existingRoot.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw 'Existing fixed Go root is not a regular directory.'
    }
}

# Fetch the exact release from Go's own catalog, then verify bytes before
# extraction. No user cache or alternative download source is accepted.
$catalog = Invoke-RestMethod -Uri 'https://go.dev/dl/?mode=json&include=all' -TimeoutSec 60
$release = @($catalog | Where-Object { $_.version -eq $goVersion -and $_.stable })
if ($release.Count -ne 1) { throw 'Selected Go release is absent from the official catalog.' }
$archiveName = "$goVersion.windows-amd64.zip"
$archive = @($release[0].files | Where-Object {
    $_.filename -eq $archiveName -and $_.os -eq 'windows' -and
    $_.arch -eq 'amd64' -and $_.kind -eq 'archive'
})
if ($archive.Count -ne 1 -or $archive[0].sha256 -notmatch '^[0-9a-f]{64}$') {
    throw 'Official Windows AMD64 archive metadata is invalid.'
}
if (-not [IO.Path]::IsPathFullyQualified($env:RUNNER_TEMP)) {
    throw 'CI temporary directory must be absolute.'
}
$staging = Join-Path ([IO.Path]::GetFullPath($env:RUNNER_TEMP)) ('traverse-fixed-go-' + [guid]::NewGuid())
$null = New-Item -ItemType Directory -Path $staging
$zipPath = Join-Path $staging $archiveName
$download = "https://go.dev/dl/$archiveName"
Invoke-WebRequest -Uri $download -OutFile $zipPath -TimeoutSec 120
$archiveHash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($archiveHash -ne $archive[0].sha256 -or (Get-Item -LiteralPath $zipPath).Length -ne $archive[0].size) {
    throw 'Official Go archive size or SHA256 mismatch.'
}
$zip = [IO.Compression.ZipFile]::OpenRead($zipPath)
try {
    $entry = $zip.GetEntry('go/bin/go.exe')
    if ($null -eq $entry) { throw 'Official archive has no Go executable.' }
    $stream = $entry.Open()
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        $officialExeHash = [Convert]::ToHexString($hasher.ComputeHash($stream))
    } finally {
        $hasher.Dispose()
        $stream.Dispose()
    }
} finally {
    $zip.Dispose()
}
if ((Get-FileHash -LiteralPath $setupGo -Algorithm SHA256).Hash -ne $officialExeHash) {
    throw 'setup-go executable differs from the selected official distribution.'
}

# Do not overlay or remove any existing installation. Extract directly below
# Program Files so new files inherit that directory's standard permissions.
if (Test-Path -LiteralPath $installRoot) {
    if ((Get-FileHash -LiteralPath $installedGo -Algorithm SHA256).Hash -ne $officialExeHash) {
        throw 'Existing fixed Go executable differs; refusing to overlay the installation.'
    }
} else {
    if ([IO.Path]::GetDirectoryName($installRoot) -ne [IO.Path]::GetFullPath($programFilesRoot) -or
        [IO.Path]::GetFileName($installRoot) -ne 'Go') {
        throw 'Fixed Go extraction escaped the named target directory.'
    }
    Expand-Archive -LiteralPath $zipPath -DestinationPath $programFilesRoot
}
if ((Get-Item -LiteralPath $installedGo).Attributes -band [IO.FileAttributes]::ReparsePoint) {
    throw 'Installed Go executable is a reparse point.'
}
$reportedVersion = (& $installedGo version).Trim()
if ($LASTEXITCODE -ne 0 -or $reportedVersion -ne "go version $goVersion windows/amd64") {
    throw 'Installed fixed Go executable did not report the selected version.'
}
Write-Host "official Go archive: $download"
Write-Host "archive SHA256: $archiveHash"
Write-Host "installed executable SHA256: $officialExeHash"
Write-Host $reportedVersion
# No PATH, registry, ACL, GOROOT, authentication, or product trust changes.
