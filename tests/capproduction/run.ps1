[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$CordumRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path

if ([string]::IsNullOrWhiteSpace($env:CAP_PRODUCTION_REDIS_URL)) {
    throw 'CAP_PRODUCTION_REDIS_URL is required'
}
if (Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.go' |
    Select-String -Pattern '\bt\.Skip(?:f|Now)?\s*\(') {
    throw 'CAP-PRODUCTION gate must not contain t.Skip calls'
}

function Invoke-GoGate {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)

    $Output = & go @Arguments 2>&1
    $ExitCode = $LASTEXITCODE
    $Output | ForEach-Object { Write-Host $_ }
    if ($ExitCode -ne 0) {
        throw "go $($Arguments -join ' ') failed with exit code $ExitCode"
    }
    if ($Output | Select-String -Pattern '^--- SKIP') {
        throw "go $($Arguments -join ' ') reported a skipped test"
    }
}

Push-Location $CordumRoot
try {
    $CapVersion = (& go list -m -f '{{.Version}}' github.com/cordum-io/cap/v2).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'could not resolve the pinned CAP module' }
    Write-Host "CAP_PRODUCTION_PIN=$CapVersion"

    Invoke-GoGate @('test', '-v', '-tags=capproduction', '-count=3', '-timeout=5m', './tests/capproduction')
    Invoke-GoGate @('test', '-v', '-count=3', './core/controlplane/scheduler', '-run',
        'Test(HandleProductionJobResultRetriesTransientStoreFailure|ProductionRawAdmissionHookSnapshotsBoundaryConfiguration|ProductionRawAdmissionHookSnapshotsResolvedIdentity|BoundTrustResolverRejectsCredentialResolutionFailures|SagaCompensation_SafetyErrorFailsClosedToDLQ|SagaCompensation_ExplicitUnavailableDecisionFailsClosedToDLQ|SafetyClientResolvesStructuredContextBeforePolicyRPC|OutputClientResolvesStructuredResultBeforePolicyRPC|SafetyClientPreservesFullStructuredInputSizeAfterTruncation|OutputClientPreservesResolvedFullMetadataBeforeTruncation)')
    Invoke-GoGate @('test', '-v', '-count=3', './core/infra/store', '-run',
        'Test(RollbackDispatchCannotClearNewerAttempt|ApplyJobResultRejectsMessageIDDigestConflict|ApplyJobResultCommitsStatePointerAndOneOutboxEffect)')
    Invoke-GoGate @('test', '-v', '-count=3', './core/controlplane/safetykernel', '-run',
        'Test(CacheKeyForRequest|DecisionCache|ReferencedInputVerified)')
    Invoke-GoGate @('test', '-v', '-count=3', './core/infra/resource', '-run', 'TestRegistry')
}
finally {
    Pop-Location
}
