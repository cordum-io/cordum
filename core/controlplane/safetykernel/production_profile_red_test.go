package safetykernel

import "testing"

func TestProductionCacheKeyIncludesJobAndPolicySnapshot(t *testing.T) {
	inputA := ProductionDecisionInput{TenantID: "tenant-a", JobID: "job-a", Topic: "job.pay"}
	inputB := ProductionDecisionInput{TenantID: "tenant-a", JobID: "job-b", Topic: "job.pay"}

	keyA, err := CanonicalProductionCacheKey(inputA, "policy-sha-a")
	if err != nil {
		t.Fatalf("key A: %v", err)
	}
	keyB, err := CanonicalProductionCacheKey(inputB, "policy-sha-a")
	if err != nil {
		t.Fatalf("key B: %v", err)
	}
	if keyA == keyB {
		t.Fatal("job-scoped decisions collided after JobID changed")
	}

	rotated, err := CanonicalProductionCacheKey(inputA, "policy-sha-b")
	if err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	if keyA == rotated {
		t.Fatal("policy snapshot rotation did not invalidate the cache key")
	}
}
