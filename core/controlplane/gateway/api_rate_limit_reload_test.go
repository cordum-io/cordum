package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
)

// apiRateReloadCalls is the number of back-to-back Allow calls in each phase.
const apiRateReloadCalls = 30

// apiRateReloadSet moves the license to an Enterprise state whose
// RequestsPerSecond is rps, by forcing the state or through the reload handler.
func apiRateReloadSet(t *testing.T, s *server, reload bool, rps int64) {
	t.Helper()
	if reload {
		installReloadableLicense(t, licensing.Claims{
			OrgID:        "org-rate-reload",
			LicenseID:    "lic-rate-reload",
			Plan:         string(licensing.PlanEnterprise),
			Entitlements: &licensing.Entitlements{RequestsPerSecond: rps},
			ExpiresAt:    time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		})
		rec := httptest.NewRecorder()
		s.handleReloadLicense(rec, adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/license/reload", nil)))
		if rec.Code != http.StatusOK {
			t.Fatalf("license reload = %d: %s", rec.Code, rec.Body.String())
		}
	} else {
		entitlements := licensing.DefaultEntitlements(licensing.PlanEnterprise)
		entitlements.RequestsPerSecond = rps
		s.entitlements.ForceStateWithStatus(licensing.PlanEnterprise, entitlements, nil, "active")
	}
	if got := s.currentEntitlements().RequestsPerSecond; got != rps {
		t.Fatalf("entitlement RequestsPerSecond = %d after the change, want %d", got, rps)
	}
}

// apiRateReloadRun sends apiRateReloadCalls Allow calls on key. It returns how
// many passed, the elapsed time and the distinct Unix seconds the calls touched.
func apiRateReloadRun(rl rateLimiter, key string) (int, time.Duration, int) {
	allowed := 0
	seconds := map[int64]bool{}
	start := time.Now()
	for i := 0; i < apiRateReloadCalls; i++ {
		seconds[time.Now().Unix()] = true
		if rl.Allow(key) {
			allowed++
		}
		seconds[time.Now().Unix()] = true
	}
	return allowed, time.Since(start), len(seconds)
}

// apiRateReloadKeys counts the Redis rate limit window keys.
func apiRateReloadKeys(t *testing.T, client redis.UniversalClient) int {
	t.Helper()
	keys, err := client.Keys(context.Background(), rateLimitKeyPrefix+"*").Result()
	if err != nil {
		t.Fatalf("list rate limit keys: %v", err)
	}
	return len(keys)
}

func TestAPIRateLimitFollowsLicenseChange(t *testing.T) {
	// State A is RequestsPerSecond 50 (limit 50/100), state B is 5 (limit
	// 5/10). The env is above both, so the license cap sets the limit.
	const highRPS, lowRPS, lowBurst = 50, 5, 10
	cases := []struct {
		name   string
		redis  bool
		reload bool
	}{
		{"memory_forced", false, false},
		{"memory_reload", false, true},
		{"redis_forced", true, false},
		{"redis_reload", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("API_RATE_LIMIT_RPS", "50000")
			t.Setenv("API_RATE_LIMIT_BURST", "100000")
			s, _, _ := newTestGateway(t)
			var client redis.UniversalClient
			wantMode := "memory"
			if tc.redis {
				client = s.redisClient()
				wantMode = "redis"
			}
			apiRateReloadSet(t, s, tc.reload, highRPS)
			rl := newAPIRateLimiter(s, client)
			if got := rateLimiterMode(rl); got != wantMode {
				t.Errorf("rateLimiterMode = %q, want %q", got, wantMode)
			}

			t.Run("1_control", func(t *testing.T) {
				if allowed, _, _ := apiRateReloadRun(rl, "apiRateReload:"+tc.name+":1"); allowed != apiRateReloadCalls {
					t.Errorf("allowed %d of %d calls at %d rps, want all", allowed, apiRateReloadCalls, highRPS)
				}
			})
			t.Run("2_lower", func(t *testing.T) {
				apiRateReloadSet(t, s, tc.reload, lowRPS)
				allowed, elapsed, seconds := apiRateReloadRun(rl, "apiRateReload:"+tc.name+":2")
				bound := lowBurst + int(float64(lowRPS)*elapsed.Seconds()) + 1
				if tc.redis {
					bound = lowBurst * seconds
				}
				if allowed < 1 || allowed >= apiRateReloadCalls || allowed > bound {
					t.Errorf("allowed %d of %d calls after the change to %d rps, want 1 to %d", allowed, apiRateReloadCalls, lowRPS, min(bound, apiRateReloadCalls-1))
				}
			})
			t.Run("3_raise", func(t *testing.T) {
				apiRateReloadSet(t, s, tc.reload, highRPS)
				if allowed, _, _ := apiRateReloadRun(rl, "apiRateReload:"+tc.name+":3"); allowed != apiRateReloadCalls {
					t.Errorf("allowed %d of %d calls after the change back to %d rps, want all", allowed, apiRateReloadCalls, highRPS)
				}
			})

			keys := apiRateReloadKeys(t, s.redisClient())
			if tc.redis && keys == 0 {
				t.Errorf("found no %s keys, want the Redis limiter to count in Redis", rateLimitKeyPrefix)
			}
			if !tc.redis && keys != 0 {
				t.Errorf("found %d %s keys, want none from the in-memory limiter", keys, rateLimitKeyPrefix)
			}
		})
	}
}
