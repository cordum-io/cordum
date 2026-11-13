package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/cordum/cordum/core/controlplane/safetykernel"
	"github.com/redis/go-redis/v9"
)

// policySignatureModeRank orders the reported modes from weakest to strongest.
var policySignatureModeRank = map[string]int{"off": 0, "warn": 1, "enforce": 2}

var policySignatureSources = [...]string{"file", "url", "store"}

// signatureEnforcementStatus reports the signature mode in effect at each
// enforcement point: the policy modes the safety kernels publish, and this
// gateway's pack-install strictness.
func (s *server) signatureEnforcementStatus(ctx context.Context) map[string]any {
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	client := s.redisClient()
	return map[string]any{
		"policy": readPolicySignatureEnforcement(readCtx, client),
		// WithoutCancel: the resolver's process-wide cache also serves pack
		// installs, so a cancelled status request must not be what fills it.
		"packs": map[string]any{"strict": resolveGatewayPackStrict(context.WithoutCancel(ctx), client)},
	}
}

// readPolicySignatureEnforcement returns the weakest mode per policy source
// across the live kernel reports, or exactly {"status":"unknown"} when no
// report can be read or any report is unreadable or invalid.
func readPolicySignatureEnforcement(ctx context.Context, client redis.UniversalClient) map[string]any {
	unknown := map[string]any{"status": "unknown"}
	if client == nil {
		return unknown
	}
	keys, err := scanPolicySignatureReportKeys(ctx, client)
	if err != nil {
		return unknown
	}
	var reports []map[string]string
	for _, key := range keys {
		raw, err := client.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			continue // the report expired between SCAN and GET
		}
		var report safetykernel.PolicySignatureReport
		if err != nil || json.Unmarshal(raw, &report) != nil || !validPolicySignatureReport(report) {
			return unknown
		}
		reports = append(reports, report.Sources)
	}
	if len(reports) == 0 {
		return unknown
	}
	weakest, consistent := map[string]string{}, true
	for _, src := range policySignatureSources {
		weakest[src] = reports[0][src]
		for _, sources := range reports[1:] {
			if policySignatureModeRank[sources[src]] < policySignatureModeRank[weakest[src]] {
				weakest[src] = sources[src]
			}
			consistent = consistent && sources[src] == reports[0][src]
		}
	}
	return map[string]any{"status": "reported", "sources": weakest, "reporters": len(reports), "consistent": consistent}
}

// validPolicySignatureReport requires an exact off/warn/enforce mode for every
// source, an RFC3339 timestamp and a non-negative key count.
func validPolicySignatureReport(report safetykernel.PolicySignatureReport) bool {
	for _, src := range policySignatureSources {
		if _, ok := policySignatureModeRank[report.Sources[src]]; !ok {
			return false
		}
	}
	_, err := time.Parse(time.RFC3339, report.ReportedAt)
	return err == nil && report.TrustedKeys >= 0
}

// scanPolicySignatureReportKeys lists the kernel report keys, sorted and
// deduplicated. A cluster client is scanned on every master.
func scanPolicySignatureReportKeys(ctx context.Context, client redis.UniversalClient) ([]string, error) {
	var mu sync.Mutex
	set := map[string]struct{}{}
	scan := func(ctx context.Context, node redis.Cmdable) error {
		iter := node.Scan(ctx, 0, safetykernel.PolicySignatureReportKeyPrefix+"*", 100).Iterator()
		var found []string
		for iter.Next(ctx) {
			found = append(found, iter.Val())
		}
		if err := iter.Err(); err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, key := range found {
			set[key] = struct{}{}
		}
		return nil
	}
	var err error
	if cluster, ok := client.(*redis.ClusterClient); ok {
		err = cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
			return scan(ctx, node)
		})
	} else {
		err = scan(ctx, client)
	}
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(set)), nil
}
