package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	"github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inputFailOpenSpy tracks IncInputFailOpen calls alongside all other Metrics methods.
type inputFailOpenSpy struct {
	mu            sync.Mutex
	failOpenCalls map[string]int
	rules         map[string]string
	unavailable   map[string]int
}

func newInputFailOpenSpy() *inputFailOpenSpy {
	return &inputFailOpenSpy{
		failOpenCalls: map[string]int{},
		rules:         map[string]string{},
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
func (m *inputFailOpenSpy) IncDispatchFlushOnWorkerOnline(string)             {}
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

func (m *inputFailOpenSpy) IncInputFailOpenRule(topic, rule string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failOpenCalls[topic]++
	m.rules[topic] = rule
}

func (m *inputFailOpenSpy) getRule(topic string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rules[topic]
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
	engine.WithFailModeResolver(newFailOpenScopeResolver(t, "open", failOpenRules("test-low-risk", "sys.unavailable")))

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
	assert.Equal(t, "test-low-risk", spy.getRule("sys.unavailable"))
}

// TestSafetyUnavailable_FailOpen_Metric verifies that IncInputFailOpen is called.
func TestSafetyUnavailable_FailOpen_Metric(t *testing.T) {
	spy := newInputFailOpenSpy()
	bus := &fakeBus{}
	store := newFakeJobStore()
	registry := newTestRegistry(t)

	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, spy)
	engine.WithFailModeResolver(newFailOpenScopeResolver(t, "open", failOpenRules("test-low-risk", "sys.unavailable")))

	req := &pb.JobRequest{
		JobId: "job-metric-1",
		Topic: "sys.unavailable",
	}
	_ = engine.processJob(context.Background(), req, "trace-metric-1")

	if spy.getFailOpenCount("sys.unavailable") != 1 {
		t.Fatalf("expected 1 IncInputFailOpen call, got %d", spy.getFailOpenCount("sys.unavailable"))
	}
	assert.Equal(t, "test-low-risk", spy.getRule("sys.unavailable"))
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
	resolver.SetSystemInputScope(failOpenRules("system-low-risk", "sys.unavailable"))
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

func failOpenRules(rule string, topics ...any) []any {
	return []any{map[string]any{"rule": rule, "topics": topics}}
}

func newFailOpenScopeResolver(t *testing.T, mode string, scope any) *FailModeResolver {
	t.Helper()
	r := NewFailModeResolver(nil, time.Hour)
	r.SetSystemInputMode(mode)
	r.SetSystemInputScope(scope)
	return r
}

func bypassEvents(sink *recordingSink) []audit.SIEMEvent {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var events []audit.SIEMEvent
	for _, event := range sink.events {
		if event.EventType == audit.EventSafetyBypassAdmit {
			events = append(events, event)
		}
	}
	return events
}

func TestFailOpenScopeRequeuesUnlistedTopic(t *testing.T) {
	bus, store, sink, spy := &fakeBus{}, newFakeJobStore(), &recordingSink{}, newInputFailOpenSpy()
	r := newFailOpenScopeResolver(t, "open", failOpenRules("reports-read", "job.reports.read"))
	e := NewEngine(bus, unavailableSafety{}, newTestRegistry(t), NewNaiveStrategy(), store, spy).
		WithFailModeResolver(r).WithDispatchAuditSink(sink)
	req := &pb.JobRequest{JobId: "unlisted", Topic: "job.default", TenantId: "tenant-x"}
	err := e.processJob(testCtx(t), req, "scope")
	var retry *retryableError
	assert.ErrorAs(t, err, &retry)
	assert.ErrorContains(t, err, "safety unavailable")
	assert.Empty(t, bus.published)
	assert.Empty(t, store.states[req.JobId])
	assert.Empty(t, bypassEvents(sink))
	assert.Zero(t, spy.getFailOpenCount(req.Topic))
	r.SetSystemInputScope(failOpenRules("default-low-risk", "job.default"))
	req = &pb.JobRequest{JobId: "listed", Topic: "job.default", TenantId: "tenant-x"}
	require.NoError(t, e.processJob(testCtx(t), req, "scope"))
	require.Len(t, bus.published, 1)
	assert.Equal(t, req.Topic, bus.published[0].subject)
	assert.Equal(t, JobStateRunning, store.states[req.JobId])
	events := bypassEvents(sink)
	require.Len(t, events, 1)
	assert.Equal(t, req.JobId, events[0].JobID)
	assert.Equal(t, req.TenantId, events[0].TenantID)
	assert.Equal(t, "default-low-risk", events[0].Extra["scope_rule"])
	assert.Equal(t, "default-low-risk", spy.getRule(req.Topic))
	assert.Equal(t, 1, spy.getFailOpenCount(req.Topic))
}

func TestFailOpenScopeTenantRulesReplaceSystem(t *testing.T) {
	provider := newMockFailModeProvider()
	provider.configs["tenant-a"] = map[string]any{"scheduler": map[string]any{
		"input_fail_mode": "open", "input_fail_open_scope": failOpenRules("tenant-reports", "job.reports.read"),
	}}
	provider.configs["tenant-c"] = map[string]any{"scheduler": map[string]any{
		"input_fail_mode": "open", "input_fail_open_scope": []any{},
	}}
	r := NewFailModeResolver(provider, time.Hour)
	r.SetSystemInputMode("open")
	r.SetSystemInputScope(failOpenRules("system-default", "job.default"))
	r.RefreshTenant(testCtx(t), "tenant-a")
	r.RefreshTenant(testCtx(t), "tenant-c")
	for _, tc := range []struct{ tenant, topic, rule string }{
		{"tenant-a", "job.default", ""}, {"tenant-a", "job.reports.read", "tenant-reports"},
		{"tenant-c", "job.default", ""}, {"tenant-b", "job.default", "system-default"},
	} {
		t.Run(tc.tenant+"/"+tc.topic, func(t *testing.T) {
			bus, store, sink := &fakeBus{}, newFakeJobStore(), &recordingSink{}
			e := NewEngine(bus, unavailableSafety{}, newTestRegistry(t), NewNaiveStrategy(), store, nil).
				WithFailModeResolver(r).WithDispatchAuditSink(sink)
			req := &pb.JobRequest{JobId: t.Name(), Topic: tc.topic, TenantId: tc.tenant}
			err := e.processJob(testCtx(t), req, "tenant")
			if tc.rule == "" {
				assert.ErrorContains(t, err, "safety unavailable")
				assert.Empty(t, bus.published)
				assert.Empty(t, store.states[req.JobId])
				assert.Empty(t, bypassEvents(sink))
				return
			}
			require.NoError(t, err)
			require.Len(t, bus.published, 1)
			assert.Equal(t, tc.topic, bus.published[0].subject)
			assert.Equal(t, JobStateRunning, store.states[req.JobId])
			events := bypassEvents(sink)
			require.Len(t, events, 1)
			assert.Equal(t, tc.rule, events[0].Extra["scope_rule"])
		})
	}
}

func TestFailOpenScopeParseSkipsInvalidRules(t *testing.T) {
	valid := failOpenRules("reports", "job.reports.read")
	for _, tc := range []struct {
		name string
		raw  any
		want failOpenScope
	}{
		{"valid", valid, failOpenScope{"job.reports.read": "reports"}},
		{"missing rule", []any{map[string]any{"topics": []any{"job.default"}}}, nil},
		{"empty rule", failOpenRules(" ", "job.default"), nil},
		{"non-string rule", []any{map[string]any{"rule": 42, "topics": []any{"job.default"}}}, nil},
		{"missing topics", []any{map[string]any{"rule": "reports"}}, nil},
		{"empty topics", failOpenRules("reports"), nil},
		{"non-list topics", []any{map[string]any{"rule": "reports", "topics": "job.default"}}, nil},
		{"non-string topic", failOpenRules("reports", 42), nil},
		{"empty and wildcard topics", failOpenRules("reports", "", " ", "job.*", "job.>"), nil},
		{"approval gates", failOpenRules("reports", capsdk.SubjectApprovalGate, capsdk.SubjectWorkflowApprovalGate), nil},
		{"raw map", map[string]any{}, nil}, {"raw string", "job.default", nil}, {"raw nil", nil, nil},
		{"non-object rule", []any{"reports"}, nil},
		{"duplicate first wins", append(failOpenRules("first", "job.default"), failOpenRules("second", "job.default")...), failOpenScope{"job.default": "first"}},
		{"mixed", failOpenRules(" reports ", " job.reports.read ", false), failOpenScope{"job.reports.read": "reports"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFailOpenScope(tc.raw)
			assert.Len(t, got, len(tc.want))
			for topic, rule := range tc.want {
				assert.Equal(t, rule, got[topic])
			}
		})
	}
}

var failOpenMetricNamespace atomic.Uint64

func TestFailOpenScopeRuleLabelsPromMetric(t *testing.T) {
	ns := fmt.Sprintf("pr116_failopen_%d", failOpenMetricNamespace.Add(1))
	metrics := infraMetrics.NewProm(ns)
	r := newFailOpenScopeResolver(t, "open", failOpenRules("default-low-risk", "job.default"))
	e := NewEngine(&fakeBus{}, unavailableSafety{}, newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), metrics).
		WithFailModeResolver(r)
	require.NoError(t, e.processJob(testCtx(t), &pb.JobRequest{JobId: ns, Topic: "job.default"}, "metric"))
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != ns+"_input_fail_open_total" {
			continue
		}
		require.Len(t, family.Metric, 1)
		sample := family.Metric[0]
		labels := map[string]string{}
		for _, label := range sample.Label {
			labels[label.GetName()] = label.GetValue()
		}
		assert.Equal(t, "job.default", labels["topic"])
		assert.Equal(t, "default-low-risk", labels["scope_rule"])
		assert.Equal(t, float64(1), sample.GetCounter().GetValue())
		return
	}
	t.Fatal("input fail-open metric family missing")
}

func TestFailOpenScopeRequiresResolverAndNonProduction(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resolver   *FailModeResolver
		production bool
	}{
		{"no resolver", nil, false},
		{"no scope", newFailOpenScopeResolver(t, "open", nil), false},
		{"production", newFailOpenScopeResolver(t, "open", failOpenRules("low-risk", "job.default")), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEngine(nil, nil, nil, nil, nil, nil).WithInputFailMode("open").
				WithFailModeResolver(tc.resolver).WithProductionIdentityEnforcement(tc.production)
			_, ok := e.inputFailOpenRule("", "job.default")
			assert.False(t, ok)
		})
	}
	var nilResolver *FailModeResolver
	nilResolver.SetSystemInputScope(nil)
	_, ok := nilResolver.InputFailOpenScopeRule("", "job.default")
	assert.False(t, ok)
}

func TestDecisionStoreWriteFailureRequeuesInBothInputModes(t *testing.T) {
	for _, decision := range []SafetyDecision{SafetyAllow, SafetyRequireApproval, SafetyDeny} {
		for _, mode := range []string{"closed", "open"} {
			t.Run(string(decision)+"/"+mode, func(t *testing.T) {
				writeErr := errors.New("decision write unavailable")
				bus, base, sink, spy := &fakeBus{}, newFakeJobStore(), &recordingSink{}, newInputFailOpenSpy()
				store := &failingSafetyDecisionStore{fakeJobStore: base, err: writeErr}
				checker := &fixedSafetyRecordChecker{record: SafetyDecisionRecord{
					Decision: decision, ApprovalRequired: decision == SafetyRequireApproval,
				}}
				e := NewEngine(bus, checker, newTestRegistry(t), NewNaiveStrategy(), store, spy).
					WithFailModeResolver(newFailOpenScopeResolver(t, mode, failOpenRules("default-low-risk", "job.default"))).
					WithDispatchAuditSink(sink)
				req := &pb.JobRequest{JobId: t.Name(), Topic: "job.default"}
				err := e.processJob(testCtx(t), req, "decision-store")
				assert.ErrorIs(t, err, writeErr)
				var retry *retryableError
				if assert.ErrorAs(t, err, &retry) {
					assert.Equal(t, safetyThrottleDelay, retry.RetryDelay())
				}
				assert.Empty(t, bus.published)
				assert.Empty(t, base.states[req.JobId])
				assert.Empty(t, bypassEvents(sink))
				assert.Zero(t, spy.getFailOpenCount(req.Topic))
			})
		}
	}
}

type lateDenySafety struct{}

func (lateDenySafety) Check(ctx context.Context, _ *pb.JobRequest) (SafetyDecisionRecord, error) {
	<-ctx.Done()
	return SafetyDecisionRecord{Decision: SafetyDeny, Reason: "late deny"}, nil
}

func TestFailOpenLateKernelAnswerKeepsDecision(t *testing.T) {
	bus, store, sink := &fakeBus{}, newFakeJobStore(), &recordingSink{}
	e := NewEngine(bus, lateDenySafety{}, newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithFailModeResolver(newFailOpenScopeResolver(t, "open", failOpenRules("default-low-risk", "job.default"))).
		WithDispatchAuditSink(sink)
	req := &pb.JobRequest{JobId: "late-answer", Topic: "job.default"}
	require.NoError(t, e.processJob(testCtx(t), req, "late-answer"))
	assert.Equal(t, JobStateDenied, store.states[req.JobId])
	for _, published := range bus.published {
		assert.NotEqual(t, req.Topic, published.subject)
	}
	assert.Empty(t, bypassEvents(sink))
}
