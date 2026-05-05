package safetykernel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/licensing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

// velocityLua atomically cleans expired entries, records the current request,
// sets TTL, and returns the count — all in a single Redis round-trip.
// Using a Lua script prevents race conditions between concurrent requests.
const velocityLua = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window_start = tonumber(ARGV[2])
local member = ARGV[3]
local window_ttl = tonumber(ARGV[4])
redis.call('ZREMRANGEBYSCORE', key, '-inf', tostring(window_start))
redis.call('ZADD', key, now, member)
redis.call('EXPIRE', key, window_ttl)
return redis.call('ZCARD', key)
`

// velocityCheckOnlyLua checks the count without recording (for simulate/explain).
const velocityCheckOnlyLua = `
local key = KEYS[1]
local window_start = tonumber(ARGV[1])
redis.call('ZREMRANGEBYSCORE', key, '-inf', tostring(window_start))
return redis.call('ZCARD', key)
`

var velocityRedisTimeout = 500 * time.Millisecond

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

var (
	velocityCheckTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cordum_safety_velocity_check_total",
		Help: "Total velocity checks performed by the safety kernel",
	}, []string{"rule_id", "result"}) // result: "exceeded", "within_limit", "error", "skipped"

	velocityCheckDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cordum_safety_velocity_check_duration_seconds",
		Help:    "Velocity check Redis round-trip latency",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5},
	}, []string{"rule_id"})
)

func init() {
	prometheus.MustRegister(velocityCheckTotal)
	prometheus.MustRegister(velocityCheckDuration)
}

// ---------------------------------------------------------------------------
// Velocity checker
// ---------------------------------------------------------------------------

type velocityChecker struct {
	client    redis.UniversalClient
	script    *redis.Script
	checkOnly *redis.Script
}

func newVelocityChecker(client redis.UniversalClient) *velocityChecker {
	if client == nil {
		return nil
	}
	return &velocityChecker{
		client:    client,
		script:    redis.NewScript(velocityLua),
		checkOnly: redis.NewScript(velocityCheckOnlyLua),
	}
}

// sanitizeKeyValue replaces characters that could cause Redis key ambiguity.
func sanitizeKeyValue(raw string) string {
	r := strings.NewReplacer(":", "_", " ", "_", "\n", "_", "\r", "_", "\t", "_")
	return r.Replace(raw)
}

// velocityKey builds the Redis key for a velocity rule+bucket combination.
// Key format: cordum:velocity:{ruleID}:{sanitized_keyValue}
func velocityKey(ruleID, keyValue string) string {
	return fmt.Sprintf("cordum:velocity:%s:%s", sanitizeKeyValue(ruleID), sanitizeKeyValue(keyValue))
}

// CheckAndRecord atomically records the request and checks if velocity is exceeded.
// Returns (exceeded, currentCount, error). On error the caller should fail-open.
func (vc *velocityChecker) CheckAndRecord(ctx context.Context, ruleID, keyValue, requestID string, cfg *config.VelocityConfig) (bool, int64, error) {
	if vc == nil || vc.client == nil {
		return false, 0, fmt.Errorf("velocity checker not initialized")
	}
	if cfg.MaxRequests <= 0 || cfg.WindowSeconds <= 0 {
		slog.Error("velocity config invalid, skipping check (fail-open)",
			"rule", ruleID, "max_requests", cfg.MaxRequests, "window_seconds", cfg.WindowSeconds)
		return false, 0, fmt.Errorf("invalid velocity config: max_requests=%d window_seconds=%d", cfg.MaxRequests, cfg.WindowSeconds)
	}

	ctx, cancel := context.WithTimeout(ctx, velocityRedisTimeout)
	defer cancel()

	now := float64(time.Now().Unix())
	windowStart := now - float64(cfg.WindowSeconds)
	ttl := cfg.WindowSeconds + 60 // buffer for clock skew

	key := velocityKey(ruleID, keyValue)
	start := time.Now()
	result, err := vc.script.Run(ctx, vc.client, []string{key},
		now, windowStart, requestID, ttl,
	).Int64()
	elapsed := time.Since(start)
	velocityCheckDuration.WithLabelValues(ruleID).Observe(elapsed.Seconds())

	if err != nil {
		velocityCheckTotal.WithLabelValues(ruleID, "error").Inc()
		slog.Error("velocity redis eval failed",
			"component", "safety", "error", err,
			"latency_ms", elapsed.Milliseconds())
		return false, 0, fmt.Errorf("velocity redis eval: %w", err)
	}

	exceeded := result > int64(cfg.MaxRequests)
	if exceeded {
		velocityCheckTotal.WithLabelValues(ruleID, "exceeded").Inc()
	} else {
		velocityCheckTotal.WithLabelValues(ruleID, "within_limit").Inc()
	}
	slog.Debug("velocity check completed",
		"rule", ruleID, "key", keyValue, "count", result,
		"max", cfg.MaxRequests, "exceeded", exceeded,
		"latency_ms", elapsed.Milliseconds())
	return exceeded, result, nil
}

// CheckOnly checks velocity without recording (for simulate/explain).
func (vc *velocityChecker) CheckOnly(ctx context.Context, ruleID, keyValue string, cfg *config.VelocityConfig) (bool, int64, error) {
	if vc == nil || vc.client == nil {
		return false, 0, fmt.Errorf("velocity checker not initialized")
	}
	if cfg.MaxRequests <= 0 || cfg.WindowSeconds <= 0 {
		slog.Error("velocity config invalid, skipping check (fail-open)",
			"rule", ruleID, "max_requests", cfg.MaxRequests, "window_seconds", cfg.WindowSeconds)
		return false, 0, fmt.Errorf("invalid velocity config: max_requests=%d window_seconds=%d", cfg.MaxRequests, cfg.WindowSeconds)
	}

	ctx, cancel := context.WithTimeout(ctx, velocityRedisTimeout)
	defer cancel()

	now := float64(time.Now().Unix())
	windowStart := now - float64(cfg.WindowSeconds)

	key := velocityKey(ruleID, keyValue)
	start := time.Now()
	result, err := vc.checkOnly.Run(ctx, vc.client, []string{key},
		windowStart,
	).Int64()
	elapsed := time.Since(start)
	velocityCheckDuration.WithLabelValues(ruleID).Observe(elapsed.Seconds())

	if err != nil {
		velocityCheckTotal.WithLabelValues(ruleID, "error").Inc()
		slog.Error("velocity redis eval failed (check-only)",
			"component", "safety", "error", err,
			"latency_ms", elapsed.Milliseconds())
		return false, 0, fmt.Errorf("velocity redis eval: %w", err)
	}

	exceeded := result > int64(cfg.MaxRequests)
	slog.Debug("velocity check-only completed",
		"rule", ruleID, "key", keyValue, "count", result,
		"max", cfg.MaxRequests, "exceeded", exceeded)
	return exceeded, result, nil
}

// ---------------------------------------------------------------------------
// Member resolution
// ---------------------------------------------------------------------------

// resolveVelocityMember returns the sorted-set member for a velocity check.
// It prefers the job_id (which gives idempotency for retried jobs). When job_id
// is empty — common for direct gRPC callers like the Visa demo — it generates a
// unique per-call identifier so each request advances the sliding window.
func resolveVelocityMember(jobID string) string {
	if jobID != "" {
		return jobID
	}
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("anon-%d-%s", time.Now().UnixNano(), hex.EncodeToString(buf[:]))
}

// ---------------------------------------------------------------------------
// Rule helpers
// ---------------------------------------------------------------------------

// hasVelocityRules returns true if any rule in the policy has a velocity config.
func hasVelocityRules(rules []config.PolicyRule) bool {
	for i := range rules {
		if rules[i].Velocity != nil {
			return true
		}
	}
	return false
}

// evaluateRulesWithVelocity iterates rules with velocity-aware logic.
// For rules without velocity configs, this behaves identically to SafetyPolicy.Evaluate().
// Velocity rules only fire when the rate limit is exceeded; otherwise they are skipped.
func (s *server) evaluateRulesWithVelocity(ctx context.Context, policy *config.SafetyPolicy, input config.PolicyInput, jobID, method string) config.PolicyDecision {
	rules := policy.EffectiveRules()
	dryRun := method == "simulate" || method == "explain"
	velocityRuleLimit := s.velocityRuleLimit()
	velocityRuleCount := int64(0)

	for _, rule := range rules {
		if rule.Velocity != nil {
			velocityRuleCount++
			if velocityRuleLimit != licensing.Unlimited && velocityRuleCount > velocityRuleLimit {
				slog.Warn("velocity rule skipped by tier limit",
					"component", "safety",
					"rule", rule.ID,
					"configured_index", velocityRuleCount,
					"allowed", velocityRuleLimit,
					"plan", s.resolvedPlan(),
					"upgrade_url", licensing.DefaultUpgradeURL,
				)
				continue
			}
		}
		if !config.MatchRule(rule.Match, input) {
			continue
		}
		// Rule matches on metadata. Check velocity if configured.
		if rule.Velocity != nil {
			if s.velocityChecker == nil {
				velocityCheckTotal.WithLabelValues(rule.ID, "skipped").Inc()
				slog.Warn("velocity rule matched but checker unavailable (fail-open, skipping)",
					"component", "safety", "rule", rule.ID,
					"skip_reason", "no_redis_client",
					"max", rule.Velocity.MaxRequests,
					"window_seconds", rule.Velocity.WindowSeconds,
					"key_expr", rule.Velocity.Key,
					"job_id", jobID)
				continue // fail-open: skip this rule
			}
			keyValue := rule.Velocity.ResolveKey(input)
			if keyValue == "" {
				velocityCheckTotal.WithLabelValues(rule.ID, "skipped").Inc()
				slog.Warn("velocity key resolved to empty, skipping rule",
					"component", "safety", "rule", rule.ID,
					"skip_reason", "empty_key",
					"key_expr", rule.Velocity.Key, "job_id", jobID)
				continue
			}
			bucketKey := velocityKey(rule.ID, keyValue)

			var exceeded bool
			var count int64
			var err error
			if dryRun {
				exceeded, count, err = s.velocityChecker.CheckOnly(ctx, rule.ID, keyValue, rule.Velocity)
			} else {
				member := resolveVelocityMember(jobID)
				exceeded, count, err = s.velocityChecker.CheckAndRecord(ctx, rule.ID, keyValue, member, rule.Velocity)
			}
			if err != nil {
				failMode := strings.ToLower(strings.TrimSpace(os.Getenv("VELOCITY_FAIL_MODE")))
				if failMode == "open" {
					slog.Error("velocity check failed (fail-open override)",
						"component", "safety", "error", err, "job_id", jobID)
					continue
				}
				slog.Error("velocity check failed (fail-closed)",
					"component", "safety", "error", err, "job_id", jobID)
				return config.PolicyDecision{
					Decision:         "require_approval",
					Reason:           "velocity check unavailable",
					RuleTier:         config.PolicyTierGlobal,
					ApprovalRequired: true,
				}
			}
			slog.Info("velocity evaluation",
				"component", "safety",
				"rule", rule.ID,
				"bucket_key", bucketKey,
				"count", count,
				"threshold", rule.Velocity.MaxRequests,
				"window_seconds", rule.Velocity.WindowSeconds,
				"key", keyValue,
				"exceeded", exceeded,
				"job_id", jobID,
				"dry_run", dryRun,
			)
			if !exceeded {
				continue // within limits — skip this rule, evaluate next
			}
		}
		// Rule fires (either no velocity or velocity exceeded).
		return config.BuildDecision(rule)
	}
	return policy.DefaultPolicyDecision()
}
