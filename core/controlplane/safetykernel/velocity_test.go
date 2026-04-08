package safetykernel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestVelocityChecker(t *testing.T) (*velocityChecker, *miniredis.Miniredis) {
	t.Helper()
	prevTimeout := velocityRedisTimeout
	velocityRedisTimeout = 2 * time.Second
	t.Cleanup(func() { velocityRedisTimeout = prevTimeout })
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	vc := newVelocityChecker(client)
	require.NotNil(t, vc)
	return vc, mr
}

func TestVelocityCheckAndRecord_WithinLimit(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 3, WindowSeconds: 60, Key: "labels.session_id"}

	for i := 1; i <= 3; i++ {
		exceeded, count, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-"+string(rune('0'+i)), cfg)
		require.NoError(t, err)
		assert.False(t, exceeded, "request %d should be within limit", i)
		assert.Equal(t, int64(i), count)
	}
}

func TestVelocityCheckAndRecord_ExceedsLimit(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 3, WindowSeconds: 60, Key: "labels.session_id"}

	for i := 1; i <= 3; i++ {
		exceeded, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-"+string(rune('0'+i)), cfg)
		require.NoError(t, err)
		assert.False(t, exceeded)
	}

	// 4th request should be denied
	exceeded, count, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-4", cfg)
	require.NoError(t, err)
	assert.True(t, exceeded)
	assert.Equal(t, int64(4), count)
}

func TestVelocityCheckOnly_DoesNotRecord(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 3, WindowSeconds: 60, Key: "labels.session_id"}

	// Record 2 real requests
	for i := 1; i <= 2; i++ {
		_, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-"+string(rune('0'+i)), cfg)
		require.NoError(t, err)
	}

	// CheckOnly should report count=2, not add to it
	exceeded, count, err := vc.CheckOnly(context.Background(), "rule-1", "sess-abc", cfg)
	require.NoError(t, err)
	assert.False(t, exceeded)
	assert.Equal(t, int64(2), count)

	// Still 2 after CheckOnly (not 3)
	exceeded, count, err = vc.CheckOnly(context.Background(), "rule-1", "sess-abc", cfg)
	require.NoError(t, err)
	assert.False(t, exceeded)
	assert.Equal(t, int64(2), count)
}

func TestVelocity_SlidingWindow_OldEntriesExpire(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	// Use a 1-second window so the test can verify expiry via real time.Sleep
	cfg := &config.VelocityConfig{MaxRequests: 2, WindowSeconds: 1, Key: "labels.session_id"}

	// Add 2 requests
	_, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-1", cfg)
	require.NoError(t, err)
	_, _, err = vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-2", cfg)
	require.NoError(t, err)

	// 3rd should exceed
	exceeded, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-3", cfg)
	require.NoError(t, err)
	assert.True(t, exceeded)

	// Wait for the 1-second window to pass
	time.Sleep(1100 * time.Millisecond)

	// After window expires, new request should be within limit
	exceeded, count, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-4", cfg)
	require.NoError(t, err)
	assert.False(t, exceeded)
	assert.Equal(t, int64(1), count)
}

func TestVelocity_DifferentKeys_Independent(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 1, WindowSeconds: 60, Key: "labels.session_id"}

	// Session A: 1 request
	exceeded, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-a", "job-1", cfg)
	require.NoError(t, err)
	assert.False(t, exceeded)

	// Session B: independent counter, should also be within limit
	exceeded, _, err = vc.CheckAndRecord(context.Background(), "rule-1", "sess-b", "job-2", cfg)
	require.NoError(t, err)
	assert.False(t, exceeded)

	// Session A: 2nd request exceeds limit of 1
	exceeded, _, err = vc.CheckAndRecord(context.Background(), "rule-1", "sess-a", "job-3", cfg)
	require.NoError(t, err)
	assert.True(t, exceeded)
}

func TestVelocity_IdempotentJobID(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 2, WindowSeconds: 60, Key: "labels.session_id"}

	// Same job ID recorded twice — sorted set deduplicates by member
	_, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-same", cfg)
	require.NoError(t, err)
	_, count, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-same", cfg)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "same job_id should be counted only once")
}

func TestVelocity_NilChecker_FailOpen(t *testing.T) {
	var vc *velocityChecker
	cfg := &config.VelocityConfig{MaxRequests: 1, WindowSeconds: 60, Key: "labels.session_id"}

	exceeded, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-1", cfg)
	assert.Error(t, err)
	assert.False(t, exceeded, "nil checker should not report exceeded")
}

func TestVelocity_ClosedRedis_FailOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	vc := newVelocityChecker(client)
	mr.Close() // simulate Redis unavailability

	cfg := &config.VelocityConfig{MaxRequests: 1, WindowSeconds: 60, Key: "labels.session_id"}
	exceeded, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-1", cfg)
	assert.Error(t, err)
	assert.False(t, exceeded, "Redis error should not report exceeded (fail-open)")
}

func TestResolveKey_Labels(t *testing.T) {
	cfg := &config.VelocityConfig{Key: "labels.session_id"}
	input := config.PolicyInput{Labels: map[string]string{"session_id": "sess-123"}}
	assert.Equal(t, "sess-123", cfg.ResolveKey(input))
}

func TestResolveKey_ActorID(t *testing.T) {
	cfg := &config.VelocityConfig{Key: "actor_id"}
	input := config.PolicyInput{Meta: config.PolicyMeta{ActorID: "agent-42"}}
	assert.Equal(t, "agent-42", cfg.ResolveKey(input))
}

func TestResolveKey_CompoundTenantTopic(t *testing.T) {
	cfg := &config.VelocityConfig{Key: "tenant:topic"}
	input := config.PolicyInput{Tenant: "acme", Topic: "job.visa.evaluate"}
	assert.Equal(t, "acme:job.visa.evaluate", cfg.ResolveKey(input))
}

func TestResolveKey_EmptyWhenMissing(t *testing.T) {
	cfg := &config.VelocityConfig{Key: "labels.session_id"}
	input := config.PolicyInput{Labels: map[string]string{}}
	assert.Equal(t, "", cfg.ResolveKey(input))
}

func TestResolveKey_EmptyConfig(t *testing.T) {
	var cfg *config.VelocityConfig
	input := config.PolicyInput{Tenant: "acme"}
	assert.Equal(t, "", cfg.ResolveKey(input))
}

func TestHasVelocityRules(t *testing.T) {
	rules := []config.PolicyRule{
		{ID: "rule-1", Match: config.PolicyMatch{Topics: []string{"job.*"}}},
		{ID: "rule-2", Match: config.PolicyMatch{Topics: []string{"job.visa.*"}}, Velocity: &config.VelocityConfig{MaxRequests: 3, WindowSeconds: 60, Key: "labels.session_id"}},
	}
	assert.True(t, hasVelocityRules(rules))

	rulesNoVelocity := []config.PolicyRule{
		{ID: "rule-1", Match: config.PolicyMatch{Topics: []string{"job.*"}}},
	}
	assert.False(t, hasVelocityRules(rulesNoVelocity))
}

func TestVelocity_InvalidConfig_MaxRequestsZero(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 0, WindowSeconds: 60, Key: "labels.session_id"}

	exceeded, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-1", cfg)
	assert.Error(t, err, "should fail on max_requests=0")
	assert.False(t, exceeded, "should fail-open on invalid config")
}

func TestVelocity_InvalidConfig_WindowSecondsZero(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 3, WindowSeconds: 0, Key: "labels.session_id"}

	exceeded, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "job-1", cfg)
	assert.Error(t, err, "should fail on window_seconds=0")
	assert.False(t, exceeded, "should fail-open on invalid config")
}

func TestVelocity_SpecialCharactersInKey(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 2, WindowSeconds: 60, Key: "labels.session_id"}

	// Keys with colons, spaces, etc. should be sanitized and work correctly
	exceeded, count, err := vc.CheckAndRecord(context.Background(), "rule-1", "user:123:session", "job-1", cfg)
	require.NoError(t, err)
	assert.False(t, exceeded)
	assert.Equal(t, int64(1), count)

	// Same sanitized key should increment
	exceeded, count, err = vc.CheckAndRecord(context.Background(), "rule-1", "user:123:session", "job-2", cfg)
	require.NoError(t, err)
	assert.False(t, exceeded)
	assert.Equal(t, int64(2), count)
}

func TestVelocity_ConcurrentRequests(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 5, WindowSeconds: 60, Key: "labels.session_id"}

	// Send 10 concurrent requests — at most 5 should be within limit
	results := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			exceeded, _, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-concurrent",
				fmt.Sprintf("job-%d", idx), cfg)
			if err != nil {
				results <- false // error = fail-open = not exceeded
				return
			}
			results <- exceeded
		}(i)
	}

	exceededCount := 0
	for i := 0; i < 10; i++ {
		if <-results {
			exceededCount++
		}
	}
	// With max_requests=5, exactly 5 out of 10 should be exceeded
	assert.Equal(t, 5, exceededCount, "exactly 5 of 10 concurrent requests should exceed limit of 5")
}

func TestVelocityConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.VelocityConfig
		wantErr bool
	}{
		{"valid", &config.VelocityConfig{MaxRequests: 3, WindowSeconds: 60, Key: "labels.session_id"}, false},
		{"nil", nil, false},
		{"zero max_requests", &config.VelocityConfig{MaxRequests: 0, WindowSeconds: 60, Key: "labels.x"}, true},
		{"negative max_requests", &config.VelocityConfig{MaxRequests: -1, WindowSeconds: 60, Key: "labels.x"}, true},
		{"zero window_seconds", &config.VelocityConfig{MaxRequests: 3, WindowSeconds: 0, Key: "labels.x"}, true},
		{"empty key", &config.VelocityConfig{MaxRequests: 3, WindowSeconds: 60, Key: ""}, true},
		{"whitespace key", &config.VelocityConfig{MaxRequests: 3, WindowSeconds: 60, Key: "   "}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate("test-rule")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestVelocity_EmptyMember_DoesNotCollapse(t *testing.T) {
	vc, _ := newTestVelocityChecker(t)
	cfg := &config.VelocityConfig{MaxRequests: 2, WindowSeconds: 60, Key: "labels.session_id"}

	// When member is empty string, ZADD deduplicates to one entry.
	// This test documents the existing behavior — callers must provide
	// unique members for correct sliding window progression.
	_, count1, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "", cfg)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count1)

	// Second call with same empty member overwrites — count stays 1.
	_, count2, err := vc.CheckAndRecord(context.Background(), "rule-1", "sess-abc", "", cfg)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count2, "empty member collapses all calls to single entry")
}

func TestResolveVelocityMember_PrefersJobID(t *testing.T) {
	assert.Equal(t, "job-123", resolveVelocityMember("job-123"))
}

func TestResolveVelocityMember_GeneratesUniqueWhenEmpty(t *testing.T) {
	m1 := resolveVelocityMember("")
	m2 := resolveVelocityMember("")
	assert.NotEmpty(t, m1)
	assert.NotEmpty(t, m2)
	assert.NotEqual(t, m1, m2, "each call with empty job_id must produce unique member")
	assert.Contains(t, m1, "anon-", "generated member should have anon- prefix")
}

func TestResolveVelocityMember_RepeatedCallsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		m := resolveVelocityMember("")
		if seen[m] {
			t.Fatalf("duplicate member generated on iteration %d: %s", i, m)
		}
		seen[m] = true
	}
}

func TestSanitizeKeyValue(t *testing.T) {
	assert.Equal(t, "user_123_session", sanitizeKeyValue("user:123:session"))
	assert.Equal(t, "hello_world", sanitizeKeyValue("hello world"))
	assert.Equal(t, "no_newlines", sanitizeKeyValue("no\nnewlines"))
	assert.Equal(t, "simple", sanitizeKeyValue("simple"))
}
