# PowerShell equivalent of run.sh — for operators on Windows without
# MSYS bash. Idempotent.

param(
    [switch]$GoOnly,
    [switch]$PythonOnly,
    [switch]$TypescriptOnly
)

$ErrorActionPreference = 'Continue'
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Here

$Targets = @('go', 'python', 'typescript')
if ($GoOnly)         { $Targets = @('go') }
if ($PythonOnly)     { $Targets = @('python') }
if ($TypescriptOnly) { $Targets = @('typescript') }

Write-Host '[run] building simulator'
& make sim | Out-Null

foreach ($t in $Targets) {
    Write-Host "[run] conformance-$t"
    & make "conformance-$t"
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "$t harness reported failures — continuing to next harness"
    }
}

Write-Host '[run] aggregating'
& make aggregate
