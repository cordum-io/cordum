package safetykernel

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/policysign"
	"github.com/redis/go-redis/v9"
)

const PolicySignatureReportKeyPrefix = "cordum:safety:policy_signature:"

// policySignatureReportWriteBound bounds each report write's context and how
// long startup waits for the first write; a timer enforces the startup wait.
const policySignatureReportWriteBound = 2 * time.Second

// PolicySignatureReport reports the modes this instance's loaders resolve from
// its environment: configuration, not evidence that a policy was signed or verified.
type PolicySignatureReport struct {
	Sources     map[string]string `json:"sources"`
	TrustedKeys int               `json:"trusted_keys"`
	Production  bool              `json:"production"`
	ReportedAt  string            `json:"reported_at"`
}

func buildPolicySignatureReport(now time.Time) PolicySignatureReport {
	fileMode := resolveFileLoaderMode().String()
	keyCount := 0
	if store, err := policysign.LoadTrustStoreFromEnv(); err == nil {
		keyCount = store.Len()
	}
	return PolicySignatureReport{
		Sources: map[string]string{
			"file": fileMode, "url": fileMode, "store": resolveStoreBundleMode().String(),
		},
		TrustedKeys: keyCount,
		Production:  env.IsProduction(),
		ReportedAt:  now.UTC().Format(time.RFC3339),
	}
}

func policySignatureReportInterval() time.Duration {
	interval := 30 * time.Second
	if raw := os.Getenv("SAFETY_POLICY_RELOAD_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			interval = d
		}
	}
	return interval
}

func policySignatureReportTTL(interval time.Duration) time.Duration {
	// An overflow expires the report early; consumers then report unknown.
	return max(2*interval, 60*time.Second)
}

func publishPolicySignatureReport(ctx context.Context, client redis.UniversalClient, key string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(buildPolicySignatureReport(time.Now()))
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, policySignatureReportWriteBound)
	defer cancel()
	return client.Set(writeCtx, key, payload, ttl).Err()
}

func startPolicySignatureReporter(ctx context.Context, wg *sync.WaitGroup, client redis.UniversalClient) {
	if client == nil {
		slog.Warn("safety-kernel: policy signature report unavailable without Redis")
		return
	}
	key := PolicySignatureReportKeyPrefix + registry.ResolveInstanceID()
	interval := policySignatureReportInterval()
	ttl := policySignatureReportTTL(interval)
	publish := func() {
		if err := publishPolicySignatureReport(ctx, client, key, ttl); err != nil && ctx.Err() == nil {
			slog.Warn("safety-kernel: policy signature report publish failed", "key", key, "err", err)
		}
	}
	first := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		publish()
		close(first)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				publish()
			}
		}
	}()
	timer := time.NewTimer(policySignatureReportWriteBound)
	defer timer.Stop()
	select {
	case <-first:
	case <-timer.C:
		slog.Warn("safety-kernel: policy signature report still pending; startup continues", "key", key)
	}
}
