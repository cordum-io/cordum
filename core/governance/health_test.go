package governance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeDeps satisfies HealthDeps with injectable behaviour per test.
type fakeDeps struct {
	tenant      string
	now         time.Time
	decisions   DecisionCounts
	decisionErr error
	latencies   []time.Duration
	latencyErr  error
	topics      []string
	topicsErr   error
	covered     []string
	coveredErr  error
	chain       ChainStatus
	chainErr    error

	// Call counters — used to verify the cache hits vs misses.
	scanCalls     int
	latencyCalls  int
	topicCalls    int
	coveredCalls  int
	chainCalls    int
	mu            sync.Mutex
}

func (f *fakeDeps) Tenant() string { return f.tenant }
func (f *fakeDeps) Now() time.Time { return f.now }
func (f *fakeDeps) ScanDecisions(_ context.Context, _ time.Duration, _ time.Time) (DecisionCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scanCalls++
	return f.decisions, f.decisionErr
}
func (f *fakeDeps) ApprovalLatencies(_ context.Context, _ time.Duration, _ time.Time) ([]time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latencyCalls++
	return f.latencies, f.latencyErr
}
func (f *fakeDeps) ListTopics(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.topicCalls++
	return f.topics, f.topicsErr
}
func (f *fakeDeps) CoveredTopics(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.coveredCalls++
	return f.covered, f.coveredErr
}
func (f *fakeDeps) VerifyChain(_ context.Context) (ChainStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chainCalls++
	return f.chain, f.chainErr
}

func baseDeps() *fakeDeps {
	return &fakeDeps{
		tenant:    "tenant-a",
		now:       time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		decisions: DecisionCounts{Allow: 95, Deny: 5},
		latencies: []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second},
		topics:    []string{"job.a", "job.b", "job.c", "job.d"},
		covered:   []string{"job.a", "job.b", "job.c", "job.d"},
		chain:     ChainStatusOK,
	}
}

func TestComputeHealth_HappyPath(t *testing.T) {
	t.Parallel()
	deps := baseDeps()
	got, err := ComputeHealth(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("ComputeHealth: %v", err)
	}
	if got.Score < 85 {
		t.Errorf("expected score ≥ 85 for clean tenant, got %d (%+v)", got.Score, got.Factors)
	}
	if got.Grade == "F" {
		t.Errorf("clean tenant graded F: %+v", got)
	}
	if _, ok := got.Factors[FactorDenialRate]; !ok {
		t.Error("missing denial_rate factor")
	}
}

func TestComputeHealth_CompromisedChainFloors(t *testing.T) {
	t.Parallel()
	deps := baseDeps()
	deps.chain = ChainStatusCompromised
	got, err := ComputeHealth(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("ComputeHealth: %v", err)
	}
	if got.Score > FloorCompromisedChain {
		t.Errorf("compromised chain should floor score to ≤ %d, got %d", FloorCompromisedChain, got.Score)
	}
	if got.Grade != "F" {
		t.Errorf("compromised chain should grade F, got %s", got.Grade)
	}
}

func TestComputeHealth_PerFactorFailureIsPartial(t *testing.T) {
	t.Parallel()
	deps := baseDeps()
	deps.decisionErr = errors.New("redis timeout")
	got, err := ComputeHealth(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("ComputeHealth should tolerate a single factor failure: %v", err)
	}
	dr := got.Factors[FactorDenialRate]
	if dr.Notes == "" {
		t.Error("failed factor should populate Notes")
	}
	if dr.Score != NeutralFactorScore {
		t.Errorf("failed factor Score = %d want %d", dr.Score, NeutralFactorScore)
	}
}

func TestComputeHealth_NoJobsReturnsNeutral(t *testing.T) {
	t.Parallel()
	deps := baseDeps()
	deps.decisions = DecisionCounts{}
	got, err := ComputeHealth(context.Background(), deps, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Factors[FactorDenialRate].Score != NeutralFactorScore {
		t.Errorf("no-jobs denial_rate = %d want %d", got.Factors[FactorDenialRate].Score, NeutralFactorScore)
	}
}

func TestComputeHealth_NoTopicsReturnsNeutralCoverage(t *testing.T) {
	t.Parallel()
	deps := baseDeps()
	deps.topics = nil
	got, _ := ComputeHealth(context.Background(), deps, nil)
	if got.Factors[FactorPolicyCoverage].Score != NeutralFactorScore {
		t.Errorf("no-topics coverage = %d want %d", got.Factors[FactorPolicyCoverage].Score, NeutralFactorScore)
	}
}

func TestLinearScore_Boundaries(t *testing.T) {
	t.Parallel()
	// denial_rate best→worst: 0→0.5, higher-is-worse.
	if got := linearScore(0, 0, 0.5); got != 100 {
		t.Errorf("0%% denial = %d want 100", got)
	}
	if got := linearScore(0.5, 0, 0.5); got != 0 {
		t.Errorf("50%% denial = %d want 0", got)
	}
	if got := linearScore(0.25, 0, 0.5); got != 50 {
		t.Errorf("25%% denial = %d want 50", got)
	}
	if got := linearScore(1.0, 0, 0.5); got != 0 {
		t.Errorf("100%% denial clamps = %d want 0", got)
	}
}

func TestPercentileDuration(t *testing.T) {
	t.Parallel()
	samples := []time.Duration{
		10 * time.Second, 20 * time.Second, 30 * time.Second,
		40 * time.Second, 50 * time.Second,
	}
	if got := percentileDuration(samples, 95); got != 50*time.Second {
		t.Errorf("p95 = %v want 50s", got)
	}
	if got := percentileDuration(samples, 50); got != 30*time.Second {
		t.Errorf("p50 = %v want 30s", got)
	}
	if got := percentileDuration(nil, 95); got != 0 {
		t.Errorf("empty p95 = %v want 0", got)
	}
}

func TestGradeFor(t *testing.T) {
	t.Parallel()
	cases := map[int]string{
		100: "A", 95: "A", 90: "A",
		89: "B", 85: "B", 80: "B",
		79: "C", 70: "C",
		69: "D", 60: "D",
		59: "F", 30: "F", 0: "F",
	}
	for score, want := range cases {
		if got := gradeFor(score); got != want {
			t.Errorf("gradeFor(%d) = %q want %q", score, got, want)
		}
	}
}

func TestCache_GetPutInvalidate(t *testing.T) {
	t.Parallel()
	c := NewCache(1 * time.Second)
	now := time.Now().UTC()
	hs := &HealthScore{Score: 87}
	if _, ok := c.Get("t1", now); ok {
		t.Error("empty cache should miss")
	}
	c.Put("t1", hs, now)
	got, ok := c.Get("t1", now)
	if !ok {
		t.Fatal("should hit")
	}
	if got.Score != 87 {
		t.Errorf("score = %d want 87", got.Score)
	}
	// Returned copy should not alias the stored value.
	got.Score = 0
	again, _ := c.Get("t1", now)
	if again.Score != 87 {
		t.Error("cache returned an aliased pointer — callers can mutate")
	}
	// Expiry.
	if _, ok := c.Get("t1", now.Add(2*time.Second)); ok {
		t.Error("expired entry should miss")
	}
	// Invalidate.
	c.Put("t1", hs, now)
	c.Invalidate("t1")
	if _, ok := c.Get("t1", now); ok {
		t.Error("invalidated entry should miss")
	}
}

func TestComputeHealth_CacheHitSkipsDeps(t *testing.T) {
	t.Parallel()
	deps := baseDeps()
	cache := NewCache(1 * time.Minute)
	_, _ = ComputeHealth(context.Background(), deps, cache)
	before := deps.scanCalls
	_, _ = ComputeHealth(context.Background(), deps, cache)
	if deps.scanCalls != before {
		t.Errorf("second call should hit cache; scanCalls went %d → %d", before, deps.scanCalls)
	}
}

func TestComputeHealth_CacheSeparatesPerTenant(t *testing.T) {
	t.Parallel()
	cache := NewCache(1 * time.Minute)
	a := baseDeps()
	a.tenant = "tenant-a"
	b := baseDeps()
	b.tenant = "tenant-b"
	_, _ = ComputeHealth(context.Background(), a, cache)
	_, _ = ComputeHealth(context.Background(), b, cache)
	if a.scanCalls != 1 || b.scanCalls != 1 {
		t.Errorf("each tenant should recompute: a=%d b=%d", a.scanCalls, b.scanCalls)
	}
}

func TestComputeHealth_NilDepsErrors(t *testing.T) {
	t.Parallel()
	if _, err := ComputeHealth(context.Background(), nil, nil); err == nil {
		t.Error("expected error on nil deps")
	}
}
