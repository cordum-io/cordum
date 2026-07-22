#!/usr/bin/env bash
set -euo pipefail

: "${CAP_PRODUCTION_REDIS_URL:?CAP_PRODUCTION_REDIS_URL is required}"
cordum_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if grep -R -n -E '\bt\.Skip(f|Now)?[[:space:]]*\(' "$cordum_root/tests/capproduction" --include='*.go'; then
  echo 'CAP-PRODUCTION gate must not contain t.Skip calls' >&2
  exit 1
fi

run_go_gate() {
  local output
  output="$(mktemp)"
  if ! go "$@" 2>&1 | tee "$output"; then
    rm -f "$output"
    return 1
  fi
  if grep -q '^--- SKIP' "$output"; then
    echo "go $* reported a skipped test" >&2
    rm -f "$output"
    return 1
  fi
  rm -f "$output"
}

cd "$cordum_root"
printf 'CAP_PRODUCTION_PIN=%s\n' "$(go list -m -f '{{.Version}}' github.com/cordum-io/cap/v2)"
run_go_gate test -v -tags=capproduction -count=3 -timeout=5m ./tests/capproduction
run_go_gate test -v -count=3 ./core/controlplane/scheduler -run \
  'Test(HandleProductionJobResultRetriesTransientStoreFailure|ProductionRawAdmissionHookSnapshotsBoundaryConfiguration|ProductionRawAdmissionHookSnapshotsResolvedIdentity|BoundTrustResolverRejectsCredentialResolutionFailures|SagaCompensation_SafetyErrorFailsClosedToDLQ|SagaCompensation_ExplicitUnavailableDecisionFailsClosedToDLQ|SafetyClientResolvesStructuredContextBeforePolicyRPC|OutputClientResolvesStructuredResultBeforePolicyRPC|SafetyClientPreservesFullStructuredInputSizeAfterTruncation|OutputClientPreservesResolvedFullMetadataBeforeTruncation)'
run_go_gate test -v -count=3 ./core/infra/store -run \
  'Test(RollbackDispatchCannotClearNewerAttempt|ApplyJobResultRejectsMessageIDDigestConflict|ApplyJobResultCommitsStatePointerAndOneOutboxEffect)'
run_go_gate test -v -count=3 ./core/controlplane/safetykernel -run \
  'Test(CacheKeyForRequest|DecisionCache|ReferencedInputVerified)'
run_go_gate test -v -count=3 ./core/infra/resource -run 'TestRegistry'
