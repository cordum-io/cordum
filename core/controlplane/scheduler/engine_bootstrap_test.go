package scheduler

import (
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

// Regression guard for the QA reopen that caught scheduler.NewEngine
// shipping without WithDispatchGate / WithTrustMetrics. If these
// builders ever stop taking effect, this test fails before the binary
// ever ships — shorter feedback loop than QA round-tripping the
// rejection.

func TestEngineBuilders_DispatchGateAndTrustMetricsTakeEffect(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	resolver := NewTrustResolver(client)
	gate := NewDispatchGate(resolver, HeartbeatModeTelemetry)
	metrics := DefaultWorkerTrustMetrics()

	e := &Engine{}
	out := e.WithDispatchGate(gate).WithTrustMetrics(metrics)
	if out != e {
		t.Fatalf("builders did not return the same engine pointer")
	}
	if e.dispatchGate != gate {
		t.Errorf("dispatchGate not wired onto engine after WithDispatchGate")
	}
	if e.trustMetrics != metrics {
		t.Errorf("trustMetrics not wired onto engine after WithTrustMetrics")
	}
}

// TestSchedulerEnvVarParsesHeartbeatMode pins the env-var flag so the
// scheduler binary's boot-time ParseHeartbeatMode(os.Getenv(...))
// call keeps driving rollout mode end-to-end.
func TestSchedulerEnvVarParsesHeartbeatMode(t *testing.T) {
	// Not parallel — sets env.
	t.Setenv(EnvHeartbeatMode, "warn")
	got := ParseHeartbeatMode(os.Getenv(EnvHeartbeatMode))
	if got != HeartbeatModeWarn {
		t.Fatalf("mode = %v, want Warn", got)
	}
	if !got.EnforcesSession() || !got.EmitsDisagreement() {
		t.Errorf("warn mode should EnforceSession + EmitDisagreement; got %+v", got)
	}
}
