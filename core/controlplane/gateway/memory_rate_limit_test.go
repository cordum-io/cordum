package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

// memoryRateLimitStartup is RunWithAuth's API limit: the tier default, then the
// API_RATE_LIMIT_* override, then the entitlement clamp.
func memoryRateLimitStartup(s *server) (int, int) {
	rps, burst := s.tierRateLimitDefaults()
	rps, burst = rateLimitFromEnv("API_RATE_LIMIT_RPS", "API_RATE_LIMIT_BURST", rps, burst)
	return clampRateLimitToEntitlements(rps, burst, s.currentEntitlements())
}

// memoryRateLimitBound is the most a rps/burst limit may allow on one key over
// elapsed: the burst, the refill, and one token of float slack.
func memoryRateLimitBound(rps, burst int, elapsed time.Duration) int {
	return burst + int(float64(rps)*elapsed.Seconds()) + 1
}

// memoryRateLimitCheck asserts the limiter's fields, then sends burst+1000
// back-to-back Allow calls on one key and bounds how many pass.
func memoryRateLimitCheck(t *testing.T, rl *keyedRateLimiter, wantRPS, wantBurst int) {
	t.Helper()
	if rl == nil {
		t.Fatalf("limiter is nil, want %d/%d", wantRPS, wantBurst)
	}
	if int(rl.rps) != wantRPS || int(rl.burst) != wantBurst {
		t.Errorf("limiter = %v/%v, want %d/%d", rl.rps, rl.burst, wantRPS, wantBurst)
	}
	allowed := 0
	start := time.Now()
	for i := 0; i < wantBurst+1000; i++ {
		if rl.Allow("tenant:default") {
			allowed++
		}
	}
	if bound := memoryRateLimitBound(wantRPS, wantBurst, time.Since(start)); allowed > bound {
		t.Errorf("allowed %d of %d calls, want at most %d", allowed, wantBurst+1000, bound)
	}
}

// memoryRateLimitHandled sends burst+200 API requests through the rate limit
// middleware and bounds how many reach the handler.
func memoryRateLimitHandled(t *testing.T, rl *keyedRateLimiter, rps, burst int) {
	t.Helper()
	handled := 0
	handler := rateLimitMiddleware(nil, rl, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handled++
		w.WriteHeader(http.StatusOK)
	}))
	start := time.Now()
	for i := 0; i < burst+200; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil))
	}
	if bound := memoryRateLimitBound(rps, burst, time.Since(start)); handled > bound {
		t.Errorf("handler ran %d times for %d requests, want at most %d", handled, burst+200, bound)
	}
}

func TestMemoryRateLimitFollowsLicenseTier(t *testing.T) {
	cases := []struct {
		name      string
		plan      licensing.Plan
		status    string
		unlimited bool // entitlement RequestsPerSecond = licensing.Unlimited
		envRPS    string
		envBurst  string
		wantRPS   int
		wantBurst int
		tierArgs  bool // also build the limiter from s.tierRateLimitDefaults()
		effect    bool // also drive requests through rateLimitMiddleware
	}{
		{"missing", licensing.PlanCommunity, "active", false, "50000", "100000", 500, 1000, true, true},
		{"degraded", licensing.PlanCommunity, "fallback", false, "50000", "100000", 500, 1000, true, true},
		{"valid", licensing.PlanEnterprise, "active", false, "50000", "100000", 10000, 20000, true, false},
		{"control_env_lower", licensing.PlanCommunity, "active", false, "5", "5", 5, 5, true, false},
		{"control_unlimited", licensing.PlanEnterprise, "active", true, "50000", "100000", 50000, 100000, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("API_RATE_LIMIT_RPS", tc.envRPS)
			t.Setenv("API_RATE_LIMIT_BURST", tc.envBurst)
			s, _, _ := newTestGateway(t)
			entitlements := licensing.DefaultEntitlements(tc.plan)
			if tc.unlimited {
				entitlements.RequestsPerSecond = licensing.Unlimited
			}
			s.entitlements.ForceStateWithStatus(tc.plan, entitlements, nil, tc.status)

			limiters := map[string]*keyedRateLimiter{
				"startup": newKeyedRateLimiterFromEnvWithDefaults(memoryRateLimitStartup(s)),
			}
			if tc.tierArgs {
				limiters["tier"] = newKeyedRateLimiterFromEnvWithDefaults(s.tierRateLimitDefaults())
			}
			for way, rl := range limiters {
				t.Run(way, func(t *testing.T) {
					memoryRateLimitCheck(t, rl, tc.wantRPS, tc.wantBurst)
				})
			}
			if tc.effect {
				memoryRateLimitHandled(t, newKeyedRateLimiterFromEnvWithDefaults(memoryRateLimitStartup(s)), tc.wantRPS, tc.wantBurst)
			}
		})
	}
}
