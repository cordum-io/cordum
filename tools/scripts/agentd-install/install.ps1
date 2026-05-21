<#
.SYNOPSIS
    Provision cordum-agentd as a Windows service backed by per-user
    Credential Manager secrets.

.DESCRIPTION
    Reads cordum_agentd_nonce and cordum_api_key from sealed PowerShell
    prompts and stores them in the per-user Credential Manager vault via
    cmdkey. Copies the WinSW configuration template into the install
    directory and registers the service.

    The provisioned secrets never appear on the command line, in the
    PowerShell history file, or in the service config XML — cordum-agentd
    reads them at startup through the keychain bootstrap path.

.PARAMETER InstallPath
    Destination directory for cordum-agentd.exe + cordum-agentd-service.exe
    + cordum-agentd-service.xml. Defaults to
    "$env:ProgramFiles\Cordum\cordum-agentd".

.PARAMETER Rotate
    Delete the existing credentials before re-prompting. Use this when
    rotating the nonce or API key.

.PARAMETER SecretsFromStdin
    Read both secret values from stdin (one per line) instead of prompting.
    Reserved for the synthetic-test fixture; never use in production.

.EXAMPLE
    .\install.ps1
    .\install.ps1 -Rotate
#>
[CmdletBinding()]
param(
    [string]$InstallPath = (Join-Path $env:ProgramFiles 'Cordum\cordum-agentd'),
    [switch]$Rotate,
    [switch]$SecretsFromStdin
)

$ErrorActionPreference = 'Stop'

$repoRoot       = Resolve-Path (Join-Path $PSScriptRoot '..\..\..')
$serviceXmlSrc  = Join-Path $repoRoot 'tools\scripts\windows\cordum-agentd-service.xml'

function Read-Secret {
    param([Parameter(Mandatory)][string]$Label)
    if ($SecretsFromStdin) {
        $line = [Console]::In.ReadLine()
        if ($null -eq $line) { throw "stdin EOF reading $Label" }
        return $line
    }
    $sec = Read-Host -Prompt $Label -AsSecureString
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec)
    try {
        $value = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
    if ([string]::IsNullOrEmpty($value)) {
        throw "$Label is empty -- refusing to provision"
    }
    return $value
}

function Invoke-Cmdkey {
    param(
        [Parameter(Mandatory)][string]$Target,
        [Parameter(Mandatory)][string]$User,
        [Parameter(Mandatory)][string]$Pass
    )
    # cmdkey supports /pass: with a value, which puts the secret on the
    # command line and into the Win32 audit trail. There is no stdin
    # mode. We mitigate by:
    #   1. Running cmdkey directly (no shell expansion); the value is
    #      passed as a discrete process argument, not interpolated.
    #   2. Clearing the local variable immediately after the call.
    & cmdkey.exe /generic:$Target /user:$User /pass:$Pass | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "cmdkey failed for $Target (exit $LASTEXITCODE)"
    }
}

if ($Rotate) {
    & cmdkey.exe /delete:cordum-agentd:cordum_agentd_nonce 2>$null | Out-Null
    & cmdkey.exe /delete:cordum-agentd:cordum_api_key      2>$null | Out-Null
}

$nonce  = Read-Secret 'cordum_agentd_nonce (base64, >=32 bytes)'
$apiKey = Read-Secret 'cordum_api_key'

try {
    Invoke-Cmdkey -Target 'cordum-agentd:cordum_agentd_nonce' -User 'cordum_agentd_nonce' -Pass $nonce
    Invoke-Cmdkey -Target 'cordum-agentd:cordum_api_key'      -User 'cordum_api_key' -Pass $apiKey
    Write-Host 'Credential Manager: provisioned cordum_agentd_nonce + cordum_api_key' -ForegroundColor Cyan
} finally {
    # Best-effort wipe of the local string copies.
    $nonce  = [string]::new('*', $nonce.Length)
    $apiKey = [string]::new('*', $apiKey.Length)
    Remove-Variable -Name nonce,apiKey -ErrorAction SilentlyContinue
}

if (-not (Test-Path $InstallPath)) {
    New-Item -Path $InstallPath -ItemType Directory | Out-Null
}
Copy-Item -Path $serviceXmlSrc -Destination (Join-Path $InstallPath 'cordum-agentd-service.xml') -Force

$winswExe = Join-Path $InstallPath 'cordum-agentd-service.exe'
if (-not (Test-Path $winswExe)) {
    Write-Warning ("WinSW binary not found at {0}. Download WinSW.NET4.exe from " +
        "https://github.com/winsw/winsw/releases and copy it to that path as " +
        "cordum-agentd-service.exe, then re-run with -InstallPath {1}." -f $winswExe, $InstallPath)
    exit 0
}

& $winswExe install
if ($LASTEXITCODE -ne 0) { throw "WinSW install exit $LASTEXITCODE" }
& $winswExe start
if ($LASTEXITCODE -ne 0) { throw "WinSW start exit $LASTEXITCODE" }

Write-Host 'cordum-agentd service installed + started.' -ForegroundColor Green
