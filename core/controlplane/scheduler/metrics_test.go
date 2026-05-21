package scheduler

// Prometheus gauge tests for the heartbeat-demotion rollout. Real
// collectors + a scoped Prometheus registry per test — no mocks, no
// global state leakage across runs.

import (
	"strings"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func mustRegisterMetrics(t *testing.T) (*WorkerTrustMetrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := NewWorkerTrustMetrics(reg)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return m, reg
}

func TestWorkerTrustMetrics_ObserveTrust_ValidMapsToOne(t *testing.T) {
	t.Parallel()
	m, reg := mustRegisterMetrics(t)
	state := WorkerTrustState{
		SessionValid: true,
		Reason:       TrustReasonValid,
		Tenant:       "tenant-x",
		JTI:          "jti-1",
	}
	m.ObserveTrust("w1", state)

	want := `
# HELP cordum_scheduler_worker_session_valid 1 when the worker's session token is currently trusted (valid exp, not revoked); 0 otherwise. Authoritative signal for worker dispatch eligibility after the heartbeat-demotion rollout.
# TYPE cordum_scheduler_worker_session_valid gauge
cordum_scheduler_worker_session_valid{tenant="tenant-x",worker_id="w1"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "cordum_scheduler_worker_session_valid"); err != nil {
		t.Fatalf("gauge mismatch: %v", err)
	}
}

func TestWorkerTrustMetrics_ObserveTrust_RevokedMapsToZero(t *testing.T) {
	t.Parallel()
	m, reg := mustRegisterMetrics(t)
	now := time.Now().UTC()
	state := WorkerTrustState{
		SessionValid: false,
		Reason:       TrustReasonRevoked,
		RevokedAt:    &now,
		Tenant:       "tenant-y",
	}
	m.ObserveTrust("w2", state)

	got := testutil.ToFloat64(m.sessionValid.WithLabelValues("w2", "tenant-y"))
	if got != 0 {
		t.Fatalf("gauge=%v want 0", got)
	}
	_ = reg
}

func TestWorkerTrustMetrics_ObserveTrust_NoTenantFallsBackToUnknown(t *testing.T) {
	t.Parallel()
	m, _ := mustRegisterMetrics(t)
	m.ObserveTrust("w3", WorkerTrustState{SessionValid: true, Reason: TrustReasonValid})
	got := testutil.ToFloat64(m.sessionValid.WithLabelValues("w3", "unknown"))
	if got != 1 {
		t.Fatalf("unknown-tenant label gauge=%v want 1", got)
	}
}

func TestWorkerTrustMetrics_ObserveTrust_EmptyWorkerIDIsNoOp(t *testing.T) {
	t.Parallel()
	m, reg := mustRegisterMetrics(t)
	m.ObserveTrust("", WorkerTrustState{SessionValid: true, Reason: TrustReasonValid})
	count, err := testutil.GatherAndCount(reg, "cordum_scheduler_worker_session_valid")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if count != 0 {
		t.Fatalf("empty worker id produced %d rows, want 0", count)
	}
}

func TestWorkerTrustMetrics_HeartbeatAgeRecordsSeconds(t *testing.T) {
	t.Parallel()
	m, _ := mustRegisterMetrics(t)
	now := time.Date(2026, 4, 19, 12, 0, 30, 0, time.UTC)
	lastSeen := now.Add(-25 * time.Second)
	m.ObserveHeartbeatAge("w-age", lastSeen, now)
	got := testutil.ToFloat64(m.heartbeatAge.WithLabelValues("w-age"))
	if got != 25 {
		t.Fatalf("age gauge=%v want 25", got)
	}
}

func TestWorkerTrustMetrics_HeartbeatAgeClampsNegative(t *testing.T) {
	t.Parallel()
	m, _ := mustRegisterMetrics(t)
	now := time.Now()
	future := now.Add(5 * time.Second)
	m.ObserveHeartbeatAge("w-skew", future, now)
	got := testutil.ToFloat64(m.heartbeatAge.WithLabelValues("w-skew"))
	if got != 0 {
		t.Fatalf("skewed age gauge=%v want 0", got)
	}
}

func TestWorkerTrustMetrics_HeartbeatAgeIgnoresZeroLastSeen(t *testing.T) {
	t.Parallel()
	m, reg := mustRegisterMetrics(t)
	m.ObserveHeartbeatAge("w-nobody", time.Time{}, time.Now())
	count, err := testutil.GatherAndCount(reg, "cordum_scheduler_worker_heartbeat_age_seconds")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if count != 0 {
		t.Fatalf("zero lastSeen produced %d rows, want 0", count)
	}
}

func TestWorkerTrustMetrics_ObserveHeartbeat(t *testing.T) {
	t.Parallel()
	m, _ := mustRegisterMetrics(t)
	m.ObserveHeartbeat(&pb.Heartbeat{WorkerId: "w-hb"})
	got := testutil.ToFloat64(m.heartbeatAge.WithLabelValues("w-hb"))
	if got < 0 {
		t.Fatalf("age must not be negative; got %v", got)
	}
}

func TestWorkerTrustMetrics_ForgetWorkerRemovesRows(t *testing.T) {
	t.Parallel()
	m, reg := mustRegisterMetrics(t)
	m.ObserveTrust("w-forget", WorkerTrustState{SessionValid: true, Reason: TrustReasonValid, Tenant: "t1"})
	m.ObserveHeartbeatAge("w-forget", time.Now().Add(-10*time.Second), time.Now())
	before, _ := testutil.GatherAndCount(reg, "cordum_scheduler_worker_session_valid")
	if before != 1 {
		t.Fatalf("expected 1 session_valid row, got %d", before)
	}
	m.ForgetWorker("w-forget")
	after, _ := testutil.GatherAndCount(reg, "cordum_scheduler_worker_session_valid")
	if after != 0 {
		t.Fatalf("ForgetWorker left %d session_valid rows, want 0", after)
	}
	afterAge, _ := testutil.GatherAndCount(reg, "cordum_scheduler_worker_heartbeat_age_seconds")
	if afterAge != 0 {
		t.Fatalf("ForgetWorker left %d heartbeat_age rows, want 0", afterAge)
	}
}

func TestWorkerTrustMetrics_NilMetricsIsNoOp(t *testing.T) {
	t.Parallel()
	var m *WorkerTrustMetrics
	// All four methods must be safe on a nil receiver so production
	// callers don't need to nil-check every call site.
	m.ObserveTrust("w", WorkerTrustState{})
	m.ObserveHeartbeatAge("w", time.Now(), time.Now())
	m.ObserveHeartbeat(&pb.Heartbeat{WorkerId: "w"})
	m.ForgetWorker("w")
}
