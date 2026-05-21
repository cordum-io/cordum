# install.ps1 — EDGE-151 binary integrity pre-activation gate (PowerShell
# mirror of tools/scripts/install.sh). Unlike install.sh this script does
# NOT include the docker-compose orchestrator path; Windows users run the
# platform install via WSL + install.sh. install.ps1 is purpose-built for
# verifying and activating signed cordum-hook/cordum-agentd/cordum-claude
# binaries on Windows endpoints.
#
# Usage:
#   pwsh -NoProfile -File install.ps1 -ReleaseDir <dir> [-DevAllowUnsigned] [-InstallDir <dir>]
#
# See docs/security/binary-signing.md for the threat model.

#Requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ReleaseDir,
    [switch]$DevAllowUnsigned,
    [string]$InstallDir = $env:CORDUM_INSTALL_DIR,
    [string]$GpgExe = 'gpg.exe',
    [string]$FloorFile = $env:CORDUM_BINARY_FLOOR_FILE,
    [switch]$RollbackOperatorOverride,
    [string]$RollbackReason = ''
)

$ErrorActionPreference = 'Stop'
$ProdFingerprintPin = $env:CORDUM_RELEASE_FINGERPRINT

. (Join-Path $PSScriptRoot 'windows\gpg-path.ps1')

# Audit-log context — see docs/security/binary-signing.md §8 for the
# stable schema. Empty values are emitted as "".
$script:AuditSigScheme = ''
$script:AuditFingerprint = ''

function Emit-Audit {
    param(
        [string]$Event,
        [string]$Reason = '',
        [string]$Hash = '',
        [string]$Rel = '',
        [int]$ExitCode = 0
    )
    $payload = [ordered]@{
        event       = $Event
        hash        = $Hash
        path        = $Rel
        sig_scheme  = $script:AuditSigScheme
        fingerprint = $script:AuditFingerprint
        reason      = $Reason
        exit_code   = $ExitCode
    }
    [Console]::Error.WriteLine(($payload | ConvertTo-Json -Compress))
}

# EDGE-151-DOWNGRADE: binary-floor-{advance,rollback} audit event. Mirrors
# install.sh's emit_floor_audit. Schema is additive over the binary-verify
# event; downstream SIEMs key off `event` and the extra {from,to,operator,
# reason} fields.
function Emit-FloorAudit {
    param(
        [string]$Event,
        [string]$From,
        [string]$To,
        [string]$Operator = 'unknown',
        [string]$Reason = ''
    )
    $payload = [ordered]@{
        event       = $Event
        from        = $From
        to          = $To
        sig_scheme  = $script:AuditSigScheme
        fingerprint = $script:AuditFingerprint
        operator    = $Operator
        reason      = $Reason
        exit_code   = 0
    }
    [Console]::Error.WriteLine(($payload | ConvertTo-Json -Compress))
}

function Verify-Fail {
    param([string]$Reason)
    Emit-Audit -Event 'binary-verify-fail' -Reason $Reason -ExitCode 1
    [Console]::Error.WriteLine("BINARY-VERIFY-FAIL: $Reason")
    exit 1
}

# Test-SemverLt — returns $true when $A < $B per the same ordering as
# tools/sign/version.go's SemverCompare (semver-2.0 + natural-sort for
# `<alpha><digits>` pre-release fields). Throws on unparseable input so
# callers can Verify-Fail with the right text.
function Test-SemverLt {
    param([string]$A, [string]$B)
    if ([string]::IsNullOrWhiteSpace($A) -or [string]::IsNullOrWhiteSpace($B)) {
        throw "invalid semver: empty"
    }
    $a = $A.TrimStart('v'); $b = $B.TrimStart('v')
    $aMain = $a; $aPre = ''
    $bMain = $b; $bPre = ''
    if ($a -match '^([^-]+)-(.+)$') { $aMain = $matches[1]; $aPre = $matches[2] }
    if ($b -match '^([^-]+)-(.+)$') { $bMain = $matches[1]; $bPre = $matches[2] }
    $aParts = $aMain -split '\.'
    $bParts = $bMain -split '\.'
    if ($aParts.Count -ne 3 -or $bParts.Count -ne 3) { throw "invalid semver: $A or $B" }
    for ($i = 0; $i -lt 3; $i++) {
        $an = 0; $bn = 0
        if (-not [int]::TryParse($aParts[$i], [ref]$an)) { throw "invalid semver: $A" }
        if (-not [int]::TryParse($bParts[$i], [ref]$bn)) { throw "invalid semver: $B" }
        if ($an -lt $bn) { return $true }
        if ($an -gt $bn) { return $false }
    }
    if ([string]::IsNullOrEmpty($aPre) -and [string]::IsNullOrEmpty($bPre)) { return $false }
    if ([string]::IsNullOrEmpty($aPre)) { return $false }
    if ([string]::IsNullOrEmpty($bPre)) { return $true }
    # Natural-sort within a single pre-release identifier so rc2 < rc10.
    $aAlpha = ($aPre -replace '\d+$', '')
    $bAlpha = ($bPre -replace '\d+$', '')
    $aNumStr = $aPre.Substring($aAlpha.Length)
    $bNumStr = $bPre.Substring($bAlpha.Length)
    if ($aAlpha -eq $bAlpha -and $aNumStr -match '^\d+$' -and $bNumStr -match '^\d+$') {
        return ([int]$aNumStr) -lt ([int]$bNumStr)
    }
    return ([string]::Compare($aPre, $bPre) -lt 0)
}

function Resolve-FloorPath {
    param([string]$Override)
    if ($Override) { return $Override }
    # NOTE: PowerShell defines a read-only $HOME automatic; never assign to
    # it. Use a local variable to avoid the "read-only or constant" error.
    $userHome = $env:USERPROFILE
    if (-not $userHome) { $userHome = $env:HOME }
    if (-not $userHome) { $userHome = [System.IO.Path]::GetTempPath() }
    return (Join-Path $userHome '.cordum\binary-version-floor.json')
}

function Read-FloorVersion {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return '' }
    $raw = Get-Content -LiteralPath $Path -Raw -ErrorAction Stop
    if ([string]::IsNullOrWhiteSpace($raw)) { return '' }
    try {
        $obj = $raw | ConvertFrom-Json -ErrorAction Stop
    } catch {
        Verify-Fail "malformed floor file: $Path"
    }
    if ($obj -and $obj.version) { return [string]$obj.version }
    return ''
}

function Write-FloorAtomic {
    param(
        [string]$Path,
        [string]$Version,
        [string]$SigScheme,
        [string]$Fingerprint,
        [string]$Operator,
        [string]$Reason
    )
    $dir = Split-Path -Parent $Path
    if ($dir -and -not (Test-Path -LiteralPath $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
    $payload = [ordered]@{
        version     = $Version
        advanced_at = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
        sig_scheme  = $SigScheme
        fingerprint = $Fingerprint
        operator    = $Operator
        reason      = $Reason
    } | ConvertTo-Json -Compress
    $tmp = "$Path.tmp." + [Guid]::NewGuid().ToString('N')
    [System.IO.File]::WriteAllText($tmp, $payload, [System.Text.UTF8Encoding]::new($false))
    [System.IO.File]::Move($tmp, $Path, $true)
}

function Normalise-Fpr {
    param([string]$Value)
    return (($Value -replace '[\s\r\n]', '')).ToUpperInvariant()
}

function Reject-Path {
    param([string]$P)
    if ($P -match '^[/\\]') { return $true }
    if ($P -match '^[A-Za-z]:') { return $true }
    $forward = $P -replace '\\', '/'
    if ($forward -match '(^|/)\.\.(/|$)') { return $true }
    return $false
}

function Invoke-BinaryVerify {
    param(
        [string]$ReleaseDir,
        [bool]$Dev,
        [string]$InstallTo
    )

    if (-not (Test-Path -LiteralPath $ReleaseDir -PathType Container)) {
        Verify-Fail "release-dir not found: $ReleaseDir"
    }
    $manifest = Join-Path $ReleaseDir 'SHA256SUMS'
    $sig = Join-Path $ReleaseDir 'SHA256SUMS.asc'
    if (-not (Test-Path -LiteralPath $manifest)) { Verify-Fail 'manifest not found' }
    if (-not (Test-Path -LiteralPath $sig)) { Verify-Fail 'unsigned manifest' }

    try {
        $gpg = Resolve-CordumGpgCommand -GpgExe $GpgExe
    } catch {
        Verify-Fail 'gpg required for signature verification'
    }
    $gpgMode = Get-CordumGpgPathMode -GpgCommand $gpg

    # $PSScriptRoot is the canonical "directory of the running script" in
    # PowerShell 5.1+; $MyInvocation.MyCommand.Path is unset when the
    # script is invoked via `-File` so we must not rely on it.
    $scriptDir = $PSScriptRoot
    $repoRoot = Resolve-Path (Join-Path $scriptDir '..\..')

    if ($Dev) {
        $pubkey = Join-Path $repoRoot 'tools\test-keys\TEST-ONLY-release.pub.asc'
        if (-not (Test-Path -LiteralPath $pubkey)) {
            Verify-Fail "dev mode but TEST-ONLY pubkey missing at $pubkey"
        }
        $script:AuditSigScheme = 'dev'
    } else {
        $pubkey = Join-Path $repoRoot 'tools\keys\cordum-release.pub.asc'
        if (-not (Test-Path -LiteralPath $pubkey)) {
            Verify-Fail 'production release pubkey not provisioned at tools/keys/cordum-release.pub.asc; pass -DevAllowUnsigned for TEST-ONLY mode'
        }
        if (-not $ProdFingerprintPin) {
            Verify-Fail 'production release fingerprint not provisioned (set $env:CORDUM_RELEASE_FINGERPRINT)'
        }
        $script:AuditSigScheme = 'gpg'
    }

    $tmpHome = New-Item -ItemType Directory -Path ([IO.Path]::Combine([IO.Path]::GetTempPath(), [Guid]::NewGuid().ToString('N')))
    try {
        # Route every filesystem argument to gpg through the path adapter.
        # Git/MSYS gpg requires POSIX form (`/c/Users/...`) because the
        # drive letter is a valid POSIX path component and gets treated
        # as repo-relative otherwise. Gpg4Win/native gpg gets absolute
        # Windows paths. PowerShell cmdlets keep using `-LiteralPath`
        # with the native paths.
        $tmpHomeArg = ConvertTo-CordumGpgArgPath -Path $tmpHome.FullName -Mode $gpgMode
        $pubkeyArg  = ConvertTo-CordumGpgArgPath -Path $pubkey           -Mode $gpgMode

        $importOutput = & $gpg.Source --homedir $tmpHomeArg --batch --import $pubkeyArg 2>&1
        if ($LASTEXITCODE -ne 0) {
            # Surface the gpg error so reviewers and CI logs can diagnose
            # path / format issues without re-running with --verbose by hand.
            $importDiag = ($importOutput | Out-String).Trim()
            if ($importDiag) {
                [Console]::Error.WriteLine("gpg --import diagnostic: $importDiag")
            }
            Verify-Fail "gpg --import failed for $pubkey"
        }

        $colonLines = & $gpg.Source --homedir $tmpHomeArg --with-colons --list-keys 2>$null
        $importedFpr = $null
        foreach ($cl in $colonLines) {
            if ($cl -match '^fpr:::::::::([0-9A-F]+):') {
                $importedFpr = $matches[1]
                break
            }
        }
        if (-not $importedFpr) { Verify-Fail 'failed to extract fingerprint from pubkey' }
        $script:AuditFingerprint = $importedFpr

        if ($Dev) {
            $uidHasMarker = $false
            foreach ($cl in $colonLines) {
                if ($cl -match '^uid:.*TEST-ONLY') { $uidHasMarker = $true; break }
            }
            if (-not $uidHasMarker) {
                Verify-Fail 'dev mode but imported pubkey UID lacks TEST-ONLY marker'
            }
            if ($ProdFingerprintPin) {
                $g = Normalise-Fpr $importedFpr
                $w = Normalise-Fpr $ProdFingerprintPin
                if ($g -eq $w) { Verify-Fail '-DevAllowUnsigned refuses production fingerprint' }
            }
        } else {
            $g = Normalise-Fpr $importedFpr
            $w = Normalise-Fpr $ProdFingerprintPin
            if ($g -ne $w) {
                Verify-Fail "release pubkey fingerprint $g does not match pinned $w"
            }
        }

        $sigArg = ConvertTo-CordumGpgArgPath -Path $sig      -Mode $gpgMode
        $manifestArg = ConvertTo-CordumGpgArgPath -Path $manifest -Mode $gpgMode
        & $gpg.Source --homedir $tmpHomeArg --batch --quiet --verify $sigArg $manifestArg 2>$null
        if ($LASTEXITCODE -ne 0) { Verify-Fail 'gpg signature invalid' }

        # EDGE-151-DOWNGRADE: enforce monotonic version floor.
        $candidateVersion = ''
        $firstLine = (Get-Content -LiteralPath $manifest -TotalCount 1).TrimEnd("`r")
        if ($firstLine -match '^#\s*version:\s*(\S+)\s*$') {
            $candidateVersion = $matches[1]
        }
        $floorPath = Resolve-FloorPath -Override $FloorFile
        $persistedFloor = Read-FloorVersion -Path $floorPath
        if ($persistedFloor -and -not $candidateVersion) {
            Verify-Fail "downgrade attempt: candidate manifest has no embedded version but floor is $persistedFloor"
        }
        # Validate parseability of BOTH the candidate version AND the floor
        # before comparing — Test-SemverLt throws on garbage; the catch
        # converts that to a Verify-Fail so a malformed `# version:` line
        # never silently slips through the gate.
        if ($candidateVersion) {
            try {
                Test-SemverLt -A 'v0.0.0' -B $candidateVersion | Out-Null
            } catch {
                Verify-Fail "invalid manifest version: $candidateVersion"
            }
        }
        if ($candidateVersion -and $persistedFloor) {
            try {
                Test-SemverLt -A 'v0.0.0' -B $persistedFloor | Out-Null
            } catch {
                Verify-Fail "malformed floor file: persisted version $persistedFloor"
            }
            $isDowngrade = $false
            try {
                $isDowngrade = (Test-SemverLt -A $candidateVersion -B $persistedFloor)
            } catch {
                Verify-Fail "invalid version metadata: $_"
            }
            if ($isDowngrade) {
                if ($RollbackOperatorOverride.IsPresent) {
                    if ([string]::IsNullOrWhiteSpace($RollbackReason)) {
                        Verify-Fail "-RollbackOperatorOverride requires -RollbackReason <text>"
                    }
                } else {
                    Verify-Fail "downgrade attempt $candidateVersion < $persistedFloor"
                }
            }
        }

        $lines = Get-Content -LiteralPath $manifest
        $entries = New-Object System.Collections.Generic.List[hashtable]
        foreach ($raw in $lines) {
            $line = $raw.TrimEnd("`r")
            if ([string]::IsNullOrWhiteSpace($line)) { continue }
            # EDGE-151-DOWNGRADE: skip `# version:` metadata + future comments.
            if ($line.StartsWith('#')) { continue }
            $parts = $line -split ' ', 2
            if ($parts.Count -lt 2) { Verify-Fail "malformed manifest line: $line" }
            $hash = $parts[0]
            $rel = $parts[1].TrimStart(@(' ', '*'))
            if ([string]::IsNullOrEmpty($hash) -or [string]::IsNullOrEmpty($rel)) {
                Verify-Fail "malformed manifest line: $line"
            }
            if (Reject-Path $rel) { Verify-Fail "manifest path traversal: $rel" }
            $target = Join-Path $ReleaseDir $rel
            if (-not (Test-Path -LiteralPath $target)) { Verify-Fail "binary missing $rel" }
            $got = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLower()
            if ($got -ne $hash.ToLower()) { Verify-Fail "hash mismatch $rel" }

            # Get-AuthenticodeSignature is a Windows-only convenience check.
            # Status `NotSigned` is permissible for Tier 1-only (GPG-signed
            # manifest); Tier 2 enforcement against `Valid` happens on the
            # Windows runner via binaries-pr-validation.yml when the
            # workflow's signtool step has executed.
            if ($target -match '\.exe$' -and (Get-Command 'Get-AuthenticodeSignature' -ErrorAction SilentlyContinue)) {
                $auth = Get-AuthenticodeSignature -LiteralPath $target
                if ($auth.Status -ne 'NotSigned' -and $auth.Status -ne 'Valid') {
                    Verify-Fail "authenticode verify failed $rel (status=$($auth.Status))"
                }
                if ($auth.Status -eq 'Valid') {
                    $prevScheme = $script:AuditSigScheme
                    $script:AuditSigScheme = 'authenticode'
                    Emit-Audit -Event 'binary-verify-ok' -Hash $hash -Rel $rel
                    $script:AuditSigScheme = $prevScheme
                }
            }
            Emit-Audit -Event 'binary-verify-ok' -Hash $hash -Rel $rel
            $entries.Add(@{ Hash = $hash; Rel = $rel; Target = $target }) | Out-Null
        }

        if ($InstallTo) {
            New-Item -ItemType Directory -Path $InstallTo -Force | Out-Null
            foreach ($e in $entries) {
                $dst = Join-Path $InstallTo $e.Rel
                New-Item -ItemType Directory -Path (Split-Path -Parent $dst) -Force | Out-Null
                Move-Item -LiteralPath $e.Target -Destination $dst -Force
                # Recompute SHA-256 AFTER the move — defence-in-depth
                # against sig-then-swap race.
                $post = (Get-FileHash -LiteralPath $dst -Algorithm SHA256).Hash.ToLower()
                if ($post -ne $e.Hash.ToLower()) {
                    Verify-Fail "post-activation hash mismatch $($e.Rel)"
                }
                Emit-Audit -Event 'binary-verify-ok' -Hash $post -Rel $e.Rel
            }
            Write-Host "[install] activated $($entries.Count) binaries under $InstallTo"

            # EDGE-151-DOWNGRADE: advance the persisted floor on success.
            if ($candidateVersion) {
                $operator = $env:USERNAME
                if (-not $operator) { $operator = 'unknown' }
                $isRollback = $false
                if ($persistedFloor) {
                    try {
                        $isRollback = (Test-SemverLt -A $candidateVersion -B $persistedFloor)
                    } catch {
                        $isRollback = $false
                    }
                }
                if ($RollbackOperatorOverride.IsPresent -and $isRollback) {
                    Write-FloorAtomic -Path $floorPath -Version $candidateVersion `
                        -SigScheme $script:AuditSigScheme -Fingerprint $script:AuditFingerprint `
                        -Operator $operator -Reason $RollbackReason
                    Emit-FloorAudit -Event 'binary-floor-rollback' -From $persistedFloor `
                        -To $candidateVersion -Operator $operator -Reason $RollbackReason
                } else {
                    Write-FloorAtomic -Path $floorPath -Version $candidateVersion `
                        -SigScheme $script:AuditSigScheme -Fingerprint $script:AuditFingerprint `
                        -Operator $operator -Reason ''
                    Emit-FloorAudit -Event 'binary-floor-advance' -From $persistedFloor `
                        -To $candidateVersion -Operator $operator
                }
            }
        } else {
            Write-Host "[install] release-dir verified: $ReleaseDir ($($entries.Count) binaries match manifest)"
        }
    } finally {
        Remove-Item -Recurse -Force -LiteralPath $tmpHome.FullName -ErrorAction SilentlyContinue
    }
}

# Pre-flight: cap rollback-reason at 256 chars and refuse the override flag
# without a reason. Mirrors install.sh's argv-parsing guard.
if ($RollbackReason -and $RollbackReason.Length -gt 256) {
    $RollbackReason = $RollbackReason.Substring(0, 256)
}
if ($RollbackOperatorOverride.IsPresent -and [string]::IsNullOrWhiteSpace($RollbackReason)) {
    Verify-Fail "-RollbackOperatorOverride requires -RollbackReason <text>"
}

Invoke-BinaryVerify -ReleaseDir $ReleaseDir -Dev ($DevAllowUnsigned.IsPresent) -InstallTo $InstallDir
exit 0
