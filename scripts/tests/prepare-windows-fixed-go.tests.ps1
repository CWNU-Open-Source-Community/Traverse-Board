[CmdletBinding()]
param(
    [string]$InstallerPath = (Join-Path $PSScriptRoot '..\prepare-windows-fixed-go.ps1')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
# Parse the installer, but execute only its candidate assignment and version
# guard. No Go executable, download, installation, or PATH mutation is needed.
$parseTokens = $null
$parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile(
    $InstallerPath, [ref]$parseTokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) { throw 'Fixed Go installer has parse errors.' }
$assignments = @($ast.EndBlock.Statements | Where-Object {
    $_ -is [Management.Automation.Language.AssignmentStatementAst] -and
    $_.Left.Extent.Text -eq '$setupGo'
})
$guards = @($ast.EndBlock.Statements | Where-Object {
    $_ -is [Management.Automation.Language.IfStatementAst] -and
    $_.Clauses[0].Item1.Extent.Text.Contains('$goVersion -notmatch')
})
if ($assignments.Count -ne 1 -or $guards.Count -ne 1) {
    throw 'Expected one candidate assignment and one version guard.'
}
$selectCandidate = [scriptblock]::Create($assignments[0].Extent.Text)
$checkVersion = [scriptblock]::Create($guards[0].Extent.Text)
$current = 'C:\hostedtoolcache\windows\go\1.25.14\x64\bin\go.exe'
$older = 'C:\hostedtoolcache\windows\go\1.24.13\x64\bin\go.exe'
foreach ($candidates in @(@($current), @($current, $older), @($older, $current))) {
    & {
        param([string[]]$Paths)
        function Get-Command {
            [CmdletBinding()]
            param([string]$Name, [string]$CommandType, [switch]$All)
            if ($Name -ne 'go' -or $CommandType -ne 'Application') {
                throw 'Candidate lookup must request the Go application.'
            }
            foreach ($path in $Paths) { [pscustomobject]@{ Source = $path } }
        }
        . $selectCandidate
        if ($setupGo -isnot [string] -or $setupGo -cne $Paths[0]) {
            throw 'Candidate lookup did not select one application in resolution order.'
        }
    } -Paths $candidates
    Write-Output "fixed_go_candidate_order_$($candidates.Count): pass"
}
foreach ($case in @(
    @{ Version = 'go1.25.14'; Expected = '1.25.14'; ExitCode = 0; Accepted = $true },
    @{ Version = 'go1.25.13'; Expected = '1.25.14'; ExitCode = 0; Accepted = $false },
    @{ Version = 'go1.24.13'; Expected = '1.24.13'; ExitCode = 0; Accepted = $false },
    @{ Version = 'go1.25.14'; Expected = ''; ExitCode = 0; Accepted = $false },
    @{ Version = 'go1.25.14'; Expected = '1.25.14'; ExitCode = 1; Accepted = $false }
)) {
    & {
        $goVersion = $case.Version
        $ExpectedGoVersion = $case.Expected
        $LASTEXITCODE = $case.ExitCode
        $accepted = $true
        try { . $checkVersion } catch { $accepted = $false }
        if ($accepted -ne $case.Accepted) {
            throw 'Version guard did not enforce the exact successful setup-go selection.'
        }
    }
    Write-Output "fixed_go_version_$($case.Version)_exit_$($case.ExitCode): pass"
}
