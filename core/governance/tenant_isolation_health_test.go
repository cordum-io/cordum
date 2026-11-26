package governance

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestTenantIsolationGovernanceHealthCache(t *testing.T) {
	t.Parallel()
	a, b := baseDeps(), baseDeps()
	b.tenant = "tenant-b"
	b.decisions = DecisionCounts{Deny: 100}
	b.latencies = []time.Duration{5 * time.Minute}
	b.covered = nil
	b.chain = ChainStatusCompromised
	cache := NewCache(time.Minute)
	read := func(deps *fakeDeps) *HealthScore {
		t.Helper()
		got, err := ComputeHealth(context.Background(), deps, cache)
		if err != nil || got == nil {
			t.Fatalf("ComputeHealth: score=%v err=%v", got, err)
		}
		return got
	}
	first := read(a)
	if first.Score <= FloorCompromisedChain || !reflect.DeepEqual(first, read(a)) {
		t.Fatalf("A did not receive its healthy cached score: %+v", first)
	}
	other := read(b)
	if other.Score != 0 || other.Grade != "F" {
		t.Fatalf("B received a score not computed from its own data: %+v", other)
	}
	if !reflect.DeepEqual(first, read(a)) || !reflect.DeepEqual(other, read(b)) {
		t.Fatal("one tenant changed the other's cached score")
	}
	for _, deps := range []*fakeDeps{a, b} {
		if deps.scanCalls != 1 || deps.latencyCalls != 1 || deps.topicCalls != 1 || deps.coveredCalls != 1 || deps.chainCalls != 1 {
			t.Fatalf("%s: dependencies re-read on cache hit: scan=%d latency=%d topic=%d covered=%d chain=%d",
				deps.tenant, deps.scanCalls, deps.latencyCalls, deps.topicCalls, deps.coveredCalls, deps.chainCalls)
		}
	}
}
