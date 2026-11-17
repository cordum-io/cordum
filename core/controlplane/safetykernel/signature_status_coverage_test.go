package safetykernel

import (
	"encoding/base64"
	"maps"
	"slices"
	"testing"
	"time"
)

// TestPolicySignatureStatusCoversEverySource fails when a policy source that
// can activate policy is in the matrix inventory but has no mode in the
// signature report behind the status API, or the other way round.
func TestPolicySignatureStatusCoversEverySource(t *testing.T) {
	t.Setenv("CORDUM_POLICY_STRICT", "enforce")
	pub, _ := genKeyPair(t)
	t.Setenv("CORDUM_POLICY_PUBLIC_KEY_COVERAGE", base64.StdEncoding.EncodeToString(pub))
	var inventory []string
	for id, source := range policySignatureSources {
		if source.exclusion == "" {
			inventory = append(inventory, id)
		}
	}
	slices.Sort(inventory)
	reported := slices.Sorted(maps.Keys(buildPolicySignatureReport(time.Now()).Sources))
	if !slices.Equal(inventory, reported) {
		t.Fatalf("matrix inventory sources %v, status report sources %v", inventory, reported)
	}
}
