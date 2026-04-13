package scheduler

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/assert"
)

// inputFailOpenSpy tracks IncInputFailOpen calls alongside all other Metrics methods.
type inputFailOpenSpy struct {
	mu            sync.Mutex
	failOpenCalls map[string]int
	unavailable   map[string]int
}

func newInputFailOpenSpy() *inputFailOpenSpy {
	return &inputFailOpenSpy{
		failOpenCalls: map[string]int{},
		unavailable:   map[string]int{},
	}
}

func (m *inputFailOpenSpy) IncJobsReceived(string)          {}
func (m *inputFailOpenSpy) IncJobsDispatched(string)        {}
func (m *inputFailOpenSpy) IncJobsCompleted(string, string) {}
func (m *inputFailOpenSpy) IncSafetyDenied(string)          {}
func (m *inputFailOpenSpy) IncSafetyUnavailable(topic string) {
	m.mu.Lock()
	m.unavailable[topic]++
	m.mu.Unlock()
}
func (m *inputFailOpenSpy) IncOutputPolicyChecked(string)                     {}
func (m *inputFailOpenSpy) IncOutputPolicyQuarantined(string)                 {}
func (m *inputFailOpenSpy) IncOutputPolicySkipped(string)                     {}
func (m *inputFailOpenSpy) IncAsyncOutputTimeout(string)                      {}
func (m *inputFailOpenSpy) IncOutputEvaluations(string)                       {}
func (m *inputFailOpenSpy) IncOutputDenials(string)                           {}
func (m *inputFailOpenSpy) IncOutputRedactions(string)                        {}
func (m *inputFailOpenSpy) IncOrphanReplayed(string)                          {}
func (m *inputFailOpenSpy) ObserveJobLockWait(float64)                        {}
func (m *inputFailOpenSpy) ObserveDispatchLatency(string, float64)            {}
func (m *inputFailOpenSpy) ObserveOutputCheckLatency(string, string, float64) {}
func (m *inputFailOpenSpy) ObserveOutputEvalDuration(string, float64)         {}
func (m *inputFailOpenSpy) SetActiveGoroutines(int)                           {}
func (m *inputFailOpenSpy) SetStaleJobs(string, int)                          {}
func (m *inputFailOpenSpy) IncDLQEmitFailure(string)                          {}
func (m *inputFailOpenSpy) IncJobCancelFailures()                             {}
func (m *inputFailOpenSpy) IncValidationRejections()                          {}
func (m *inputFailOpenSpy) IncJobLockAbandoned()                              {}
func (m *inputFailOpenSpy) IncResultPtrWriteFailure()                         {}
func (m *inputFailOpenSpy) IncDispatchRollback(string)                        {}
func (m *inputFailOpenSpy) IncInputFailOpen(topic string) {
	m.mu.Lock()
	m.failOpenCalls[topic]++
	m.mu.Unlock()
}

func (m *inputFailOpenSpy) getFailOpenCount(topic string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failOpenCalls[topic]
}

func (m *inputFailOpenSpy) getUnavailableCount(topic string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unavailable[topic]
}

// TestSafetyUnavailable_FailClosed verifies the default behavior:
// when the safety kernel is unavailable, the job is retried (not allowed through).
func TestSafetyUnavailable_FailClosed(t *testing.T) {
	spy := newInputFailOpenSpy()
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), spy)

	req := &pb.JobRequest{
		JobId: "job-closed-1",
		Topic: "sys.unavailable",
	}
	err := engine.processJob(context.Background(), req, "trace-closed-1")
	if err == nil {
		t.Fatal("expected retryable error for fail-closed, got nil")
	}
	retryErr, ok := err.(*retryableError)
	if !ok {
		t.Fatalf("expected retryableError, got %T", err)
	}
	if !strings.Contains(retryErr.Error(), "safety unavailable") {
		t.Fatalf("expected 'safety unavailable' in error, got: %s", retryErr.Error())
	}
	if spy.getFailOpenCount("sys.unavailable") != 0 {
		t.Fatal("IncInputFailOpen should NOT be called in fail-closed mode")
	}
	if spy.getUnavailableCount("sys.unavailable") != 1 {
		t.Fatalf("expected 1 IncSafetyUnavailable call, got %d", spy.getUnavailableCount("sys.unavailable"))
	}
}

// TestSafetyUnavailable_FailOpen verifies that fail-open mode allows the job
// through when the safety kernel is unavailable.
func TestSafetyUnavailable_FailOpen(t *testing.T) {
	spy := newInputFailOpenSpy()
	bus := &fakeBus{}
	store := newFakeJobStore()
	registry := newTestRegistry(t)

	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, spy)
	engine.WithInputFailMode("open")

	req := &pb.JobRequest{
		JobId: "job-open-1",
		Topic: "sys.unavailable",
	}
	err := engine.processJob(context.Background(), req, "trace-open-1")
	if err != nil {
		// A retryable error from no workers is acceptable — the key check is
		// that we did NOT get an immediate "safety unavailable" retry.
		retryErr, ok := err.(*retryableError)
		if ok && strings.Contains(retryErr.Error(), "safety unavailable") {
			t.Fatalf("fail-open should NOT retry for safety unavailable, got: %v", err)
		}
	}
	if spy.getUnavailableCount("sys.unavailable") != 1 {
		t.Fatalf("expected 1 IncSafetyUnavailable call, got %d", spy.getUnavailableCount("sys.unavailable"))
	}
}

// TestSafetyUnavailable_FailOpen_Metric verifies that IncInputFailOpen is called.
func TestSafetyUnavailable_FailOpen_Metric(t *testing.T) {
	spy := newInputFailOpenSpy()
	bus := &fakeBus{}
	store := newFakeJobStore()
	registry := newTestRegistry(t)

	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, spy)
	engine.WithInputFailMode("open")

	req := &pb.JobRequest{
		JobId: "job-metric-1",
		Topic: "sys.unavailable",
	}
	_ = engine.processJob(context.Background(), req, "trace-metric-1")

	if spy.getFailOpenCount("sys.unavailable") != 1 {
		t.Fatalf("expected 1 IncInputFailOpen call, got %d", spy.getFailOpenCount("sys.unavailable"))
	}
}

// TestWithInputFailMode_InvalidValue verifies that invalid values are ignored
// and the engine stays in fail-closed mode.
func TestWithInputFailMode_InvalidValue(t *testing.T) {
	spy := newInputFailOpenSpy()
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), spy)
	engine.WithInputFailMode("invalid")
	engine.WithInputFailMode("")
	engine.WithInputFailMode("OPEN") // case sensitive

	req := &pb.JobRequest{
		JobId: "job-invalid-1",
		Topic: "sys.unavailable",
	}
	err := engine.processJob(context.Background(), req, "trace-invalid-1")
	if err == nil {
		t.Fatal("expected retryable error (invalid values should keep fail-closed), got nil")
	}
	if spy.getFailOpenCount("sys.unavailable") != 0 {
		t.Fatal("IncInputFailOpen should NOT be called with invalid fail mode")
	}
}

// TestInputFailMode_PerTenant verifies that per-tenant fail mode overrides
// work correctly through the engine's processJob path.
func TestInputFailMode_PerTenant(t *testing.T) {
	spy := newInputFailOpenSpy()
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), spy)

	// Set global fail mode to closed.
	engine.WithInputFailMode("closed")

	// Configure a resolver where "prod" is fail-closed, "sandbox" is fail-open.
	provider := newMockFailModeProvider()
	provider.setTenantConfig("prod", "closed", "closed")
	provider.setTenantConfig("sandbox", "open", "open")

	resolver := NewFailModeResolver(provider, 1*time.Hour)
	resolver.SetSystemInputMode("closed")
	resolver.RefreshTenant(context.Background(), "prod")
	resolver.RefreshTenant(context.Background(), "sandbox")
	engine.WithFailModeResolver(resolver)

	// Tenant "prod" should be fail-closed: safety unavailable → retryable error.
	reqProd := &pb.JobRequest{
		JobId:    "job-prod-1",
		Topic:    "sys.unavailable",
		TenantId: "prod",
	}
	err := engine.processJob(context.Background(), reqProd, "trace-prod-1")
	assert.NotNil(t, err, "prod tenant should get retryable error (fail-closed)")
	retryErr, ok := err.(*retryableError)
	assert.True(t, ok, "expected retryableError for prod")
	assert.Contains(t, retryErr.Error(), "safety unavailable")

	assert.Equal(t, 0, spy.getFailOpenCount("sys.unavailable"),
		"IncInputFailOpen should NOT be called for fail-closed tenant")
	assert.Equal(t, 1, spy.getUnavailableCount("sys.unavailable"),
		"IncSafetyUnavailable should be called once for prod")

	// Tenant "sandbox" should be fail-open: safety unavailable → job allowed through.
	reqSandbox := &pb.JobRequest{
		JobId:    "job-sandbox-1",
		Topic:    "sys.unavailable",
		TenantId: "sandbox",
	}
	err = engine.processJob(context.Background(), reqSandbox, "trace-sandbox-1")
	if err != nil {
		// A retryable error from no-workers is acceptable — the key check is
		// that we did NOT get a "safety unavailable" retry.
		if retryErr, ok := err.(*retryableError); ok {
			assert.NotContains(t, retryErr.Error(), "safety unavailable",
				"fail-open should NOT retry for safety unavailable")
		}
	}

	assert.Equal(t, 1, spy.getFailOpenCount("sys.unavailable"),
		"IncInputFailOpen should be called once for sandbox")
	assert.Equal(t, 2, spy.getUnavailableCount("sys.unavailable"),
		"IncSafetyUnavailable should be called twice total")
}
