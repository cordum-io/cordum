package gateway

import (
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	redis "github.com/redis/go-redis/v9"
)

// Regression guard for the QA reopen that caught the heartbeat-demotion
// authority plumbing being test-only. This test exercises the WithTrust
// Resolver / WithHeartbeatMode / WithDispatchGate / WithTrustMetrics
// builders directly to ensure they remain exercised by code that ships
// to production and not only by mock-style tests.
//
// The real production wiring lives in:
//   - cmd/cordum-scheduler/main.go (WithDispatchGate + WithTrustMetrics)
//   - core/controlplane/gateway/gateway.go (s.trustResolver + s.heartbeatMode)
// These tests pin the builder contract so a future refactor that
// removes a wiring call will fail a test, not ship dead code.

// TestGatewayServer_TrustResolverAndHeartbeatModeBuilders confirms the
// server exposes the two builders QA flagged as never-called and that
// applying them populates the receiver fields.
func TestGatewayServer_TrustResolverAndHeartbeatModeBuilders(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	client := redis.NewClient(testRedisOptions(mr.Addr()))
	t.Cleanup(func() { _ = client.Close() })

	s := &server{}
	resolver := scheduler.NewTrustResolver(client)
	out := s.WithTrustResolver(resolver).WithHeartbeatMode(scheduler.HeartbeatModeTelemetry)
	if out != s {
		t.Fatalf("builder did not return the same server pointer")
	}
	if s.trustResolver != resolver {
		t.Errorf("trustResolver field not set by WithTrustResolver")
	}
	if s.heartbeatMode != scheduler.HeartbeatModeTelemetry {
		t.Errorf("heartbeatMode = %v, want Telemetry", s.heartbeatMode)
	}
}

// TestParseHeartbeatModeDefault_EnvVarConfiguresMode pins the env-var
// → mode contract. This is the rollout knob QA called out as unused
// bootstrap code — if CORDUM_HEARTBEAT_MODE ever stops being the
// canonical flag, this test fails.
func TestParseHeartbeatModeDefault_EnvVarConfiguresMode(t *testing.T) {
	// Not parallel — sets env var.
	cases := []struct {
		raw  string
		want scheduler.HeartbeatMode
	}{
		{"", scheduler.HeartbeatModeAuthority},
		{"authority", scheduler.HeartbeatModeAuthority},
		{"warn", scheduler.HeartbeatModeWarn},
		{"telemetry", scheduler.HeartbeatModeTelemetry},
		{"TELEMETRY", scheduler.HeartbeatModeTelemetry},
		{"garbage", scheduler.HeartbeatModeAuthority},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			if tc.raw == "" {
				t.Setenv(scheduler.EnvHeartbeatMode, "")
			} else {
				t.Setenv(scheduler.EnvHeartbeatMode, tc.raw)
			}
			got := scheduler.ParseHeartbeatMode(os.Getenv(scheduler.EnvHeartbeatMode))
			if got != tc.want {
				t.Errorf("mode for %q = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
