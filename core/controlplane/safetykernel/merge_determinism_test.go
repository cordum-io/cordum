package safetykernel

import (
	"encoding/json"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

// Regression guard for task-3527fdc5: the smoke-test revealed
// "duplicate policy rule ID in merge" warnings at every reload. The
// warnings were benign on their own, but they hinted that mergePolicies
// was being fed non-deterministic input, which would in turn produce
// non-identical policy snapshots across reloads — a false StaleSnapshot
// trigger in the approval-repair classifier.
//
// This test pins mergePolicies(base, extra) as a PURE FUNCTION of its
// inputs: two calls with byte-identical arguments produce byte-identical
// policies (marshalled to JSON for a stable comparison shape, since
// config.SafetyPolicy slices of maps would panic on reflect.DeepEqual
// when maps are nil vs empty).

func policySnapshotBytes(t *testing.T, p *config.SafetyPolicy) []byte {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal policy snapshot: %v", err)
	}
	return raw
}

func TestMergePolicies_Deterministic_IdenticalInputsProduceIdenticalOutput(t *testing.T) {
	t.Parallel()
	base := &config.SafetyPolicy{
		Version: "1.0",
		Rules: []config.PolicyRule{
			{ID: "r1", Decision: "allow", Reason: "base"},
			{ID: "r2", Decision: "deny", Reason: "base"},
		},
	}
	extra := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "r3", Decision: "allow", Reason: "pack"},
			{ID: "r1", Decision: "deny", Reason: "pack override"},
		},
	}

	m1 := mergePolicies(base, extra)
	m2 := mergePolicies(base, extra)

	b1 := policySnapshotBytes(t, m1)
	b2 := policySnapshotBytes(t, m2)

	if string(b1) != string(b2) {
		t.Fatalf("mergePolicies must be deterministic across calls with identical inputs\n  first:  %s\n  second: %s", b1, b2)
	}
}

func TestMergePolicies_Deterministic_AcrossMultipleReloads(t *testing.T) {
	// More aggressive regression: simulate the 'reloads' the kernel
	// performs when pack fragments are installed. The merged policy
	// must be byte-stable across 10 merges of the same inputs, so
	// the snapshot hash never drifts.
	t.Parallel()
	base := &config.SafetyPolicy{
		Version: "2.0",
		Rules: []config.PolicyRule{
			{ID: "rule-a", Decision: "allow"},
			{ID: "rule-b", Decision: "require_approval"},
			{ID: "rule-c", Decision: "deny"},
		},
		OutputRules: []config.OutputPolicyRule{
			{ID: "out-1", Decision: "allow"},
			{ID: "out-2", Decision: "deny"},
		},
	}
	extra := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "rule-d", Decision: "allow"},
			{ID: "rule-b", Decision: "deny"}, // duplicate — tests the replace path
		},
	}

	first := policySnapshotBytes(t, mergePolicies(base, extra))
	for i := 0; i < 10; i++ {
		got := policySnapshotBytes(t, mergePolicies(base, extra))
		if string(got) != string(first) {
			t.Fatalf("merge iteration %d produced different output than first\n  first: %s\n  got:   %s", i, first, got)
		}
	}
}

func TestMergePolicies_NilBase_Deterministic(t *testing.T) {
	// Edge case: base is nil (no base safety.yaml, only pack
	// fragments). The clone path must still be deterministic.
	t.Parallel()
	extra := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{ID: "r1", Decision: "allow"},
			{ID: "r2", Decision: "deny"},
		},
	}
	m1 := mergePolicies(nil, extra)
	m2 := mergePolicies(nil, extra)
	if string(policySnapshotBytes(t, m1)) != string(policySnapshotBytes(t, m2)) {
		t.Fatal("mergePolicies(nil, extra) must be deterministic")
	}
}

func TestMergePolicies_NilExtra_Deterministic(t *testing.T) {
	t.Parallel()
	base := &config.SafetyPolicy{
		Rules: []config.PolicyRule{{ID: "r1", Decision: "allow"}},
	}
	m1 := mergePolicies(base, nil)
	m2 := mergePolicies(base, nil)
	if string(policySnapshotBytes(t, m1)) != string(policySnapshotBytes(t, m2)) {
		t.Fatal("mergePolicies(base, nil) must be deterministic")
	}
}
