package safetykernel

// CAP-PRODUCTION safety-cache key binding (task-a13f83fa step-11, DoD #2).
// Replaces the legacy cacheKeyForRequest's JobId-zeroing (see
// mem:task-task-a13f83fab9f84c8292cc01e424a5494c-handoff) with a key that
// binds the full canonical decision input AND the active policy snapshot,
// so a job-scoped decision never leaks across jobs and a snapshot rotation
// always invalidates stale cache entries.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ProductionDecisionInput is the canonical, full decision input a cache key
// is derived from. Extend deliberately — every field added here changes the
// key, which is the point: two requests differing in any authoritative
// dimension must never share a cached decision.
type ProductionDecisionInput struct {
	TenantID string
	JobID    string
	Topic    string
}

// CanonicalProductionCacheKey binds the exact decision input plus the
// selected active policy snapshot/version. Never omit JobID for job-scoped
// policies, and never reuse a key across a policy snapshot rotation.
func CanonicalProductionCacheKey(input ProductionDecisionInput, policySnapshot string) (string, error) {
	if policySnapshot == "" {
		return "", fmt.Errorf("safetykernel: policy snapshot required for cache key")
	}
	canonical := fmt.Sprintf("tenant=%s\x00job=%s\x00topic=%s", input.TenantID, input.JobID, input.Topic)
	sum := sha256.Sum256([]byte(canonical))
	return policySnapshot + ":" + hex.EncodeToString(sum[:]), nil
}
