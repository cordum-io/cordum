# install_test.ps1 — synthetic-tampered + unsigned + valid scenarios for
# install.ps1.
#
# EDGE-151 reopen #2 RED: covers Windows/MSYS path-conversion fix. Fixture
# signing/import errors are HARD failures with captured gpg diagnostics
# (never silently fall through as "unsigned manifest"). Three assertions:
#   (1) valid synthetic TEST-ONLY release verifies OK through install.ps1
#   (2) signed-then-tampered cordum-hook fails with
#       BINARY-VERIFY-FAIL: hash mismatch cordum-hook
#   (3) removing SHA256SUMS.asc fails with
#       BINARY-VERIFY-FAIL: unsigned manifest
# The script accepts -GpgExe <path> so CI can target Git/MSYS gpg
# (`C:\Program Files\Git\usr\bin\gpg.exe`) and Gpg4Win/native gpg
# independently.

#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$GpgExe = 'gpg.exe'
)

$ErrorActionPreference = 'Stop'
$scriptDir = $PSScriptRoot
$repoRoot = Resolve-Path (Join-Path $scriptDir '..\..')
$installPs1 = Join-Path $scriptDir 'install.ps1'
$testKeysDir = Join-Path $repoRoot 'tools\test-keys'
$privKey = Join-Path $testKeysDir 'TEST-ONLY-release.priv.asc'

. (Join-Path $scriptDir 'windows\gpg-path.ps1')

try {
    $gpgCmd = Resolve-CordumGpgCommand -GpgExe $GpgExe
} catch {
    Write-Error "install_test.ps1: $_"
    exit 2
}
if (-not (Test-Path -LiteralPath $privKey)) {
    Write-Error "install_test.ps1: missing $privKey -- run tools/test-keys/gen.sh"
    exit 2
}
$gpgMode = Get-CordumGpgPathMode -GpgCommand $gpgCmd

$work = New-Item -ItemType Directory -Path ([System.IO.Path]::Combine([System.IO.Path]::GetTempPath(), [System.Guid]::NewGuid().ToString('N')))
try {
    $gpgHome = Join-Path $work.FullName 'gpg'
    New-Item -ItemType Directory -Path $gpgHome | Out-Null

    $gpgHomeArg = ConvertTo-CordumGpgArgPath -Path $gpgHome -Mode $gpgMode
    $privKeyArg = ConvertTo-CordumGpgArgPath -Path $privKey -Mode $gpgMode

    $importOut = & $gpgCmd.Source --homedir $gpgHomeArg --batch --quiet --import $privKeyArg 2>&1
    if ($LASTEXITCODE -ne 0) {
        $diag = ($importOut | Out-String).Trim()
        throw "install_test.ps1: gpg --import of TEST-ONLY private key failed (exit=$LASTEXITCODE)`n$diag"
    }

    function Build-ReleaseDir {
        param(
            [string]$Dest,
            [string]$Version
        )
        New-Item -ItemType Directory -Path $Dest | Out-Null
        $binPath = Join-Path $Dest 'cordum-hook'
        Set-Content -LiteralPath $binPath -Value 'placeholder' -NoNewline -Encoding ASCII
        $hash = (Get-FileHash -LiteralPath $binPath -Algorithm SHA256).Hash.ToLower()
        $manifestPath = Join-Path $Dest 'SHA256SUMS'
        if ($Version) {
            # Use LF line endings — the install.sh / install.ps1 parsers
            # tolerate either but the GPG signature must cover identical
            # bytes regardless of host CRLF defaults.
            $lines = "# version: $Version`n$hash  cordum-hook"
            [System.IO.File]::WriteAllText($manifestPath, $lines, [System.Text.UTF8Encoding]::new($false))
        } else {
            $manifestLine = "$hash  cordum-hook"
            Set-Content -LiteralPath $manifestPath -Value $manifestLine -NoNewline -Encoding ASCII
        }
        $sigPath = Join-Path $Dest 'SHA256SUMS.asc'
        $manifestArg = ConvertTo-CordumGpgArgPath -Path $manifestPath -Mode $gpgMode
        $sigArg      = ConvertTo-CordumGpgArgPath -Path $sigPath      -Mode $gpgMode
        $signOut = & $gpgCmd.Source --homedir $gpgHomeArg --batch --yes --quiet --detach-sign --armor `
            --output $sigArg $manifestArg 2>&1
        if ($LASTEXITCODE -ne 0) {
            $diag = ($signOut | Out-String).Trim()
            throw "install_test.ps1: gpg --detach-sign failed for $Dest (exit=$LASTEXITCODE)`n$diag"
        }
        if (-not (Test-Path -LiteralPath $sigPath)) {
            throw "install_test.ps1: --output $sigPath was not produced by gpg --detach-sign"
        }
    }

    function Seed-Floor {
        param([string]$Path, [string]$Version)
        $dir = Split-Path -Parent $Path
        if (-not (Test-Path -LiteralPath $dir)) {
            New-Item -ItemType Directory -Path $dir -Force | Out-Null
        }
        $payload = ('{{"version":"{0}","advanced_at":"2026-01-01T00:00:00Z","sig_scheme":"dev","fingerprint":"","operator":"seed"}}' -f $Version)
        Set-Content -LiteralPath $Path -Value $payload -NoNewline -Encoding ASCII
    }

    function Invoke-Install {
        param(
            [string]$ReleaseDir,
            [string]$FloorFile = '',
            [string]$InstallDir = '',
            [switch]$RollbackOverride,
            [string]$RollbackReason = ''
        )
        $argsList = @(
            '-NoProfile', '-File', $installPs1,
            '-DevAllowUnsigned',
            '-ReleaseDir', $ReleaseDir,
            '-GpgExe', $GpgExe
        )
        if ($InstallDir)      { $argsList += @('-InstallDir', $InstallDir) }
        if ($FloorFile)       { $argsList += @('-FloorFile', $FloorFile) }
        if ($RollbackOverride.IsPresent) { $argsList += '-RollbackOperatorOverride' }
        if ($RollbackReason)  { $argsList += @('-RollbackReason', $RollbackReason) }
        return (& pwsh @argsList 2>&1)
    }

    function Expect-VerifyFail {
        param([string]$Label, [string]$Needle, [string]$ReleaseDir)
        if (-not (Test-Path -LiteralPath $installPs1)) {
            Write-Host "FAIL [$Label]: install.ps1 missing" -ForegroundColor Red
            return $false
        }
        $out = Invoke-Install -ReleaseDir $ReleaseDir
        $rc = $LASTEXITCODE
        if ($rc -eq 0) {
            Write-Host "FAIL [$Label]: install.ps1 exit=0; expected nonzero" -ForegroundColor Red
            $out | ForEach-Object { Write-Host $_ }
            return $false
        }
        if (-not ($out -match [regex]::Escape($Needle))) {
            Write-Host "FAIL [$Label]: stderr missing '$Needle'" -ForegroundColor Red
            $out | ForEach-Object { Write-Host $_ }
            return $false
        }
        Write-Host "PASS [$Label]: install.ps1 refused with '$Needle'" -ForegroundColor Green
        return $true
    }

    function Expect-VerifyOk {
        param([string]$Label, [string]$ReleaseDir)
        if (-not (Test-Path -LiteralPath $installPs1)) {
            Write-Host "FAIL [$Label]: install.ps1 missing" -ForegroundColor Red
            return $false
        }
        $out = Invoke-Install -ReleaseDir $ReleaseDir
        $rc = $LASTEXITCODE
        if ($rc -ne 0) {
            Write-Host "FAIL [$Label]: install.ps1 exit=$rc; expected 0" -ForegroundColor Red
            $out | ForEach-Object { Write-Host $_ }
            return $false
        }
        Write-Host "PASS [$Label]: install.ps1 verified $ReleaseDir" -ForegroundColor Green
        return $true
    }

    $failures = 0

    # Scenario 1 — valid synthetic release: install.ps1 must verify cleanly.
    $valid = Join-Path $work.FullName 'release-valid'
    Build-ReleaseDir $valid
    if (-not (Expect-VerifyOk 'valid-release' $valid)) { $failures++ }

    # Scenario 2 — tampered binary: post-sign mutation must trigger the
    # SHA-256 comparison inside install.ps1's pre-activation gate.
    $tampered = Join-Path $work.FullName 'release-tampered'
    Build-ReleaseDir $tampered
    Add-Content -LiteralPath (Join-Path $tampered 'cordum-hook') -Value 'tamper' -NoNewline
    if (-not (Expect-VerifyFail 'tampered-binary' 'BINARY-VERIFY-FAIL: hash mismatch cordum-hook' $tampered)) {
        $failures++
    }

    # Scenario 3 — unsigned manifest: SHA256SUMS.asc removed; install must
    # abort even though SHA256SUMS itself is intact.
    $unsigned = Join-Path $work.FullName 'release-unsigned'
    Build-ReleaseDir $unsigned
    Remove-Item -LiteralPath (Join-Path $unsigned 'SHA256SUMS.asc') -Force
    if (-not (Expect-VerifyFail 'unsigned-manifest' 'BINARY-VERIFY-FAIL: unsigned manifest' $unsigned)) {
        $failures++
    }

    # ------------------------------------------------------------------
    # EDGE-151-DOWNGRADE scenarios.
    # ------------------------------------------------------------------

    # Scenario 4 — downgrade-refused: floor v1.2.0; release embeds v1.0.0.
    $downgradeDir = Join-Path $work.FullName 'release-downgrade'
    $downgradeFloor = Join-Path $work.FullName 'floor-downgrade\binary-version-floor.json'
    Seed-Floor -Path $downgradeFloor -Version 'v1.2.0'
    Build-ReleaseDir -Dest $downgradeDir -Version 'v1.0.0'
    $out = Invoke-Install -ReleaseDir $downgradeDir -FloorFile $downgradeFloor
    if ($LASTEXITCODE -eq 0) {
        Write-Host "FAIL [downgrade-refused]: install.ps1 exit=0; expected nonzero" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } elseif (-not ($out -match 'BINARY-VERIFY-FAIL: downgrade attempt v1\.0\.0 < v1\.2\.0')) {
        Write-Host "FAIL [downgrade-refused]: stderr missing downgrade-attempt message" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } else {
        Write-Host "PASS [downgrade-refused]: install.ps1 refused v1.0.0 < v1.2.0" -ForegroundColor Green
    }

    # Scenario 5 — legit-upgrade: floor v1.2.0; release embeds v1.3.0.
    $upgradeRel = Join-Path $work.FullName 'release-upgrade'
    $upgradeFloor = Join-Path $work.FullName 'floor-upgrade\binary-version-floor.json'
    $upgradeInstall = Join-Path $work.FullName 'install-upgrade'
    Seed-Floor -Path $upgradeFloor -Version 'v1.2.0'
    Build-ReleaseDir -Dest $upgradeRel -Version 'v1.3.0'
    $out = Invoke-Install -ReleaseDir $upgradeRel -FloorFile $upgradeFloor -InstallDir $upgradeInstall
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAIL [legit-upgrade]: install.ps1 exit=$LASTEXITCODE; expected 0" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } elseif (-not ($out -match '"event":"binary-floor-advance"')) {
        Write-Host "FAIL [legit-upgrade]: stderr missing binary-floor-advance audit event" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } elseif (-not ((Get-Content -LiteralPath $upgradeFloor -Raw) -match '"version":"v1\.3\.0"')) {
        Write-Host "FAIL [legit-upgrade]: floor file not advanced to v1.3.0" -ForegroundColor Red
        Get-Content -LiteralPath $upgradeFloor | ForEach-Object { Write-Host $_ }
        $failures++
    } else {
        Write-Host "PASS [legit-upgrade]: install.ps1 advanced floor v1.2.0 -> v1.3.0" -ForegroundColor Green
    }

    # Scenario 6 — operator-override-rollback: floor v1.2.0; release embeds
    # v1.1.0; with --rollback-operator-override and --rollback-reason.
    $rollbackRel = Join-Path $work.FullName 'release-rollback'
    $rollbackFloor = Join-Path $work.FullName 'floor-rollback\binary-version-floor.json'
    $rollbackInstall = Join-Path $work.FullName 'install-rollback'
    Seed-Floor -Path $rollbackFloor -Version 'v1.2.0'
    Build-ReleaseDir -Dest $rollbackRel -Version 'v1.1.0'
    $out = Invoke-Install -ReleaseDir $rollbackRel -FloorFile $rollbackFloor -InstallDir $rollbackInstall `
        -RollbackOverride -RollbackReason 'CVE-rollback'
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAIL [operator-rollback]: install.ps1 exit=$LASTEXITCODE; expected 0" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } elseif (-not ($out -match '"event":"binary-floor-rollback"')) {
        Write-Host "FAIL [operator-rollback]: stderr missing binary-floor-rollback audit event" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } elseif (-not ($out -match '"reason":"CVE-rollback"')) {
        Write-Host "FAIL [operator-rollback]: audit event missing reason=CVE-rollback" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } elseif (-not ((Get-Content -LiteralPath $rollbackFloor -Raw) -match '"version":"v1\.1\.0"')) {
        Write-Host "FAIL [operator-rollback]: floor file not rolled back to v1.1.0" -ForegroundColor Red
        Get-Content -LiteralPath $rollbackFloor | ForEach-Object { Write-Host $_ }
        $failures++
    } else {
        Write-Host "PASS [operator-rollback]: install.ps1 rolled back floor v1.2.0 -> v1.1.0" -ForegroundColor Green
    }

    # Scenario 7a — garbage version in manifest must be rejected.
    $garbageRel = Join-Path $work.FullName 'release-garbage-ver'
    Build-ReleaseDir -Dest $garbageRel -Version 'v1.0-garbage'
    $out = Invoke-Install -ReleaseDir $garbageRel
    if ($LASTEXITCODE -eq 0) {
        Write-Host "FAIL [garbage-version]: install.ps1 exit=0; expected nonzero" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } elseif (-not ($out -match 'invalid manifest version')) {
        Write-Host "FAIL [garbage-version]: stderr missing 'invalid manifest version'" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } else {
        Write-Host "PASS [garbage-version]: install.ps1 refused malformed version" -ForegroundColor Green
    }

    # Scenario 7 — rollback override without reason: must refuse.
    $rollbackNoReasonRel = Join-Path $work.FullName 'release-rollback-noreason'
    $rollbackNoReasonFloor = Join-Path $work.FullName 'floor-rollback-noreason\binary-version-floor.json'
    Seed-Floor -Path $rollbackNoReasonFloor -Version 'v1.2.0'
    Build-ReleaseDir -Dest $rollbackNoReasonRel -Version 'v1.1.0'
    $out = Invoke-Install -ReleaseDir $rollbackNoReasonRel -FloorFile $rollbackNoReasonFloor `
        -RollbackOverride
    if ($LASTEXITCODE -eq 0) {
        Write-Host "FAIL [rollback-no-reason]: install.ps1 exit=0; expected nonzero" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } elseif (-not ($out -match 'RollbackReason')) {
        Write-Host "FAIL [rollback-no-reason]: stderr missing RollbackReason requirement" -ForegroundColor Red
        $out | ForEach-Object { Write-Host $_ }
        $failures++
    } else {
        Write-Host "PASS [rollback-no-reason]: install.ps1 refused rollback without reason" -ForegroundColor Green
    }

    if ($failures -ne 0) {
        Write-Error "install_test.ps1: $failures scenario(s) failed"
        exit 1
    }
    Write-Host 'install_test.ps1: all synthetic verification scenarios passed'
}
finally {
    Remove-Item -Recurse -Force -LiteralPath $work.FullName -ErrorAction SilentlyContinue
}
