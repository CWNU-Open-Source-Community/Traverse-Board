[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$builder = Join-Path $repositoryRoot 'scripts/build-desktop.ps1'
$tokens = $null
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($builder, [ref]$tokens, [ref]$errors)
if ($errors.Count -ne 0) { throw 'Desktop builder has parse errors.' }
# Execute only the real toolchain preflight with a fake Go executable; never
# run frontend checks, build a package, or change the machine's Go installation.
$statements = @($ast.EndBlock.Statements | Where-Object {
    ($_ -is [Management.Automation.Language.AssignmentStatementAst] -and
        $_.Left.Extent.Text -in @('$goDirective', '$expectedGoVersion', '$goVersion')) -or
    ($_ -is [Management.Automation.Language.IfStatementAst] -and
        ($_.Clauses[0].Item1.Extent.Text.Contains('$goDirective.Success') -or
         $_.Clauses[0].Item1.Extent.Text.Contains('$goVersion -cne $expectedGoVersion')))
})
if ($statements.Count -ne 5) { throw 'Expected exactly one complete toolchain preflight.' }
$preflight = [scriptblock]::Create(($statements.Extent.Text -join [Environment]::NewLine))
$pinned = 'go' + [regex]::Match(
    [IO.File]::ReadAllText((Join-Path $repositoryRoot 'go.mod')),
    '(?m)^go ([0-9]+\.[0-9]+\.[0-9]+)\r?$').Groups[1].Value
foreach ($case in @(
    @{ Version = $pinned; Accepted = $true },
    @{ Version = 'go1.26.8'; Accepted = $false },
    @{ Version = 'go1.27.0'; Accepted = $false },
    @{ Version = 'go1.27.1'; Accepted = $false },
    @{ Version = 'go1.27.2'; Accepted = $false },
    @{ Version = "$pinned-custom"; Accepted = $false }
)) {
    & {
        function go { $case.Version }
        $LASTEXITCODE = 0
        $accepted = $true
        try { . $preflight } catch { $accepted = $false }
        if ($accepted -ne $case.Accepted) { throw 'Desktop builder accepted an unpinned toolchain.' }
    }
    Write-Output "desktop_toolchain_$($case.Version): pass"
}
